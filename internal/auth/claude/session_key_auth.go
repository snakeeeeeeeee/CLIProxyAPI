package claude

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/imroc/req/v3"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

const (
	SessionKeyOAuthScope  = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	credentialStepTimeout = 30 * time.Second
	credentialResponseMax = 1 << 20

	sessionKeyChromeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	sessionKeyChromeSecCHUA   = `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`
)

// SessionKeyAuthError is a sanitized credential-acquisition failure.
type SessionKeyAuthError struct {
	Code       string
	StatusCode int
	Retryable  bool
	Cause      error
}

func (e *SessionKeyAuthError) Error() string {
	if e == nil {
		return "session key authorization failed"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	return e.Code
}

func (e *SessionKeyAuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// SessionKeyAuthenticator exchanges one Claude web session for standard OAuth tokens.
type SessionKeyAuthenticator struct {
	httpClient *http.Client
	baseURL    string
	tokenURL   string
	initErr    error
}

// NewSessionKeyAuthenticator creates a cookie-assisted OAuth client using the selected proxy.
func NewSessionKeyAuthenticator(cfg *config.Config, proxyURL string) *SessionKeyAuthenticator {
	client, err := newSessionKeyBrowserHTTPClient(cfg, proxyURL)
	authenticator := newSessionKeyAuthenticator(client, "https://claude.ai", ClaudeCodeTokenURL)
	authenticator.initErr = err
	return authenticator
}

func newSessionKeyAuthenticator(client *http.Client, baseURL, tokenURL string) *SessionKeyAuthenticator {
	if client == nil {
		client = http.DefaultClient
	}
	return &SessionKeyAuthenticator{
		httpClient: client,
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		tokenURL:   strings.TrimSpace(tokenURL),
	}
}

// Authenticate performs organization lookup, PKCE authorization, and token exchange.
func (a *SessionKeyAuthenticator) Authenticate(ctx context.Context, sessionKey string) (*ClaudeAuthBundle, error) {
	if a != nil && a.initErr != nil {
		return nil, &SessionKeyAuthError{Code: "proxy_error", Cause: a.initErr}
	}
	if a == nil || a.httpClient == nil {
		return nil, &SessionKeyAuthError{Code: "authorize_failed", Cause: fmt.Errorf("authenticator unavailable")}
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil, &SessionKeyAuthError{Code: "invalid_session", Cause: fmt.Errorf("session key is required")}
	}
	orgUUID, err := a.organizationUUID(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, &SessionKeyAuthError{Code: "authorize_failed", Cause: fmt.Errorf("generate PKCE: %w", err)}
	}
	state, err := generateSessionKeyState()
	if err != nil {
		return nil, &SessionKeyAuthError{Code: "authorize_failed", Cause: fmt.Errorf("generate state: %w", err)}
	}
	code, err := a.authorizationCode(ctx, sessionKey, orgUUID, pkce.CodeChallenge, state)
	if err != nil {
		return nil, err
	}
	bundle, err := a.exchangeCode(ctx, code, state, pkce.CodeVerifier)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bundle.TokenData.AccessToken) == "" {
		return nil, &SessionKeyAuthError{Code: "token_exchange_failed", Cause: fmt.Errorf("access token missing")}
	}
	if strings.TrimSpace(bundle.TokenData.RefreshToken) == "" {
		return nil, &SessionKeyAuthError{Code: "missing_refresh_token", Cause: fmt.Errorf("refresh token missing")}
	}
	if strings.TrimSpace(bundle.TokenData.Email) == "" || strings.TrimSpace(bundle.TokenData.AccountUUID) == "" {
		return nil, &SessionKeyAuthError{Code: "token_exchange_failed", Cause: fmt.Errorf("account identity missing")}
	}
	if strings.TrimSpace(bundle.TokenData.OrganizationUUID) == "" {
		bundle.TokenData.OrganizationUUID = orgUUID
	}
	return bundle, nil
}

func (a *SessionKeyAuthenticator) organizationUUID(ctx context.Context, sessionKey string) (string, error) {
	type organization struct {
		UUID      string  `json:"uuid"`
		RavenType *string `json:"raven_type"`
	}
	var organizations []organization
	err := a.doWithRetry(ctx, "authorize_failed", func(stepCtx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(stepCtx, http.MethodGet, a.baseURL+"/api/organizations", nil)
		if err == nil {
			req.AddCookie(&http.Cookie{Name: "sessionKey", Value: sessionKey, Path: "/", Secure: true, HttpOnly: true})
			applySessionKeyBrowserHeaders(req)
		}
		return req, err
	}, func(resp *http.Response) error {
		return decodeCredentialJSON(resp.Body, &organizations)
	})
	if err != nil {
		return "", err
	}
	if len(organizations) == 0 {
		return "", &SessionKeyAuthError{Code: "no_organization", Cause: fmt.Errorf("no organization found")}
	}
	for _, org := range organizations {
		if org.RavenType != nil && strings.EqualFold(strings.TrimSpace(*org.RavenType), "team") && strings.TrimSpace(org.UUID) != "" {
			return strings.TrimSpace(org.UUID), nil
		}
	}
	if strings.TrimSpace(organizations[0].UUID) == "" {
		return "", &SessionKeyAuthError{Code: "no_organization", Cause: fmt.Errorf("organization UUID missing")}
	}
	return strings.TrimSpace(organizations[0].UUID), nil
}

func (a *SessionKeyAuthenticator) authorizationCode(ctx context.Context, sessionKey, orgUUID, challenge, state string) (string, error) {
	payload := map[string]any{
		"response_type":         "code",
		"client_id":             ClientID,
		"organization_uuid":     orgUUID,
		"redirect_uri":          ClaudeCodeRedirectURI,
		"scope":                 SessionKeyOAuthScope,
		"state":                 state,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	}
	type response struct {
		RedirectURI string `json:"redirect_uri"`
	}
	var result response
	err := a.doWithRetry(ctx, "authorize_failed", func(stepCtx context.Context) (*http.Request, error) {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(stepCtx, http.MethodPost, a.baseURL+"/v1/oauth/"+url.PathEscape(orgUUID)+"/authorize", strings.NewReader(string(body)))
		if err == nil {
			req.AddCookie(&http.Cookie{Name: "sessionKey", Value: sessionKey, Path: "/", Secure: true, HttpOnly: true})
			applySessionKeyBrowserHeaders(req)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Origin", "https://claude.ai")
			req.Header.Set("Referer", "https://claude.ai/new")
			req.Header.Set("Content-Type", "application/json")
		}
		return req, err
	}, func(resp *http.Response) error {
		return decodeCredentialJSON(resp.Body, &result)
	})
	if err != nil {
		return "", err
	}
	redirect, err := url.Parse(strings.TrimSpace(result.RedirectURI))
	if err != nil || redirect == nil {
		return "", &SessionKeyAuthError{Code: "authorize_failed", Cause: fmt.Errorf("invalid authorization redirect")}
	}
	code := strings.TrimSpace(redirect.Query().Get("code"))
	responseState := strings.TrimSpace(redirect.Query().Get("state"))
	if code == "" {
		return "", &SessionKeyAuthError{Code: "authorize_failed", Cause: fmt.Errorf("authorization code missing")}
	}
	if responseState == "" || responseState != state {
		return "", &SessionKeyAuthError{Code: "state_mismatch", Cause: fmt.Errorf("authorization state mismatch")}
	}
	return code, nil
}

func (a *SessionKeyAuthenticator) exchangeCode(ctx context.Context, code, state, verifier string) (*ClaudeAuthBundle, error) {
	payload := map[string]any{
		"code":          code,
		"state":         state,
		"grant_type":    "authorization_code",
		"client_id":     ClientID,
		"redirect_uri":  ClaudeCodeRedirectURI,
		"code_verifier": verifier,
	}
	var result tokenResponse
	err := a.doWithRetry(ctx, "token_exchange_failed", func(stepCtx context.Context) (*http.Request, error) {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(stepCtx, http.MethodPost, a.tokenURL, strings.NewReader(string(body)))
		if err == nil {
			applySessionKeyBrowserHeaders(req)
			req.Header.Set("Accept", "application/json, text/plain, */*")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "axios/1.13.6")
		}
		return req, err
	}, func(resp *http.Response) error {
		return decodeCredentialJSON(resp.Body, &result)
	})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &ClaudeAuthBundle{
		TokenData: ClaudeTokenData{
			AccessToken:      result.AccessToken,
			RefreshToken:     result.RefreshToken,
			Email:            result.Account.EmailAddress,
			OrganizationUUID: result.Organization.UUID,
			AccountUUID:      result.Account.UUID,
			Expire:           now.Add(time.Duration(result.ExpiresIn) * time.Second).Format(time.RFC3339),
		},
		LastRefresh: now.Format(time.RFC3339),
	}, nil
}

func (a *SessionKeyAuthenticator) doWithRetry(ctx context.Context, code string, requestFactory func(context.Context) (*http.Request, error), decode func(*http.Response) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		stepCtx, cancel := context.WithTimeout(ctx, credentialStepTimeout)
		req, err := requestFactory(stepCtx)
		if err != nil {
			cancel()
			return &SessionKeyAuthError{Code: code, Cause: fmt.Errorf("create request: %w", err)}
		}
		resp, err := a.httpClient.Do(req)
		if err != nil {
			cancel()
			if attempt == 0 && ctx.Err() == nil {
				continue
			}
			return &SessionKeyAuthError{Code: "proxy_error", Retryable: true, Cause: fmt.Errorf("credential request failed")}
		}
		status := resp.StatusCode
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			err = decode(resp)
		} else {
			responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, credentialResponseMax))
			err = classifySessionKeyHTTPError(code, status, resp.Header, responseBody)
		}
		_ = resp.Body.Close()
		cancel()
		if err == nil {
			return nil
		}
		var authErr *SessionKeyAuthError
		if errors.As(err, &authErr) {
			if attempt == 0 && authErr.Retryable && ctx.Err() == nil {
				continue
			}
			return authErr
		}
		return &SessionKeyAuthError{Code: code, Cause: fmt.Errorf("invalid credential response")}
	}
	return &SessionKeyAuthError{Code: code, Cause: fmt.Errorf("credential request failed")}
}

func newSessionKeyBrowserHTTPClient(cfg *config.Config, proxyURL string) (*http.Client, error) {
	effectiveProxyURL := strings.TrimSpace(proxyURL)
	if effectiveProxyURL == "" && cfg != nil {
		effectiveProxyURL = strings.TrimSpace(cfg.ProxyURL)
	}
	setting, err := proxyutil.Parse(effectiveProxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy configuration")
	}
	client := req.C().ImpersonateChrome().SetCookieJar(nil).SetLogger(nil)
	switch setting.Mode {
	case proxyutil.ModeProxy:
		client.SetProxyURL(setting.URL.String())
	case proxyutil.ModeDirect, proxyutil.ModeInherit:
		client.SetProxy(nil)
	default:
		return nil, fmt.Errorf("invalid proxy configuration")
	}
	return client.GetClient(), nil
}

func applySessionKeyBrowserHeaders(request *http.Request) {
	if request == nil {
		return
	}
	request.Header.Set("Pragma", "no-cache")
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("Sec-Ch-Ua", sessionKeyChromeSecCHUA)
	request.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	request.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	request.Header.Set("Upgrade-Insecure-Requests", "1")
	request.Header.Set("User-Agent", sessionKeyChromeUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	request.Header.Set("Sec-Fetch-Site", "none")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.Header.Set("Sec-Fetch-User", "?1")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
}

func classifySessionKeyHTTPError(code string, status int, header http.Header, body []byte) error {
	if status == http.StatusProxyAuthRequired {
		return &SessionKeyAuthError{Code: "proxy_error", StatusCode: status, Cause: fmt.Errorf("proxy authentication rejected")}
	}
	if status == http.StatusUnauthorized && code != "token_exchange_failed" {
		return &SessionKeyAuthError{Code: "invalid_session", StatusCode: status, Cause: fmt.Errorf("web session rejected")}
	}
	if status == http.StatusForbidden && code != "token_exchange_failed" && isSessionKeyChallengeResponse(header, body) {
		return &SessionKeyAuthError{Code: "proxy_error", StatusCode: status, Cause: fmt.Errorf("web authorization challenge")}
	}
	return &SessionKeyAuthError{
		Code:       code,
		StatusCode: status,
		Retryable:  status >= http.StatusInternalServerError,
		Cause:      fmt.Errorf("credential endpoint returned HTTP %d", status),
	}
}

func isSessionKeyChallengeResponse(header http.Header, body []byte) bool {
	if strings.TrimSpace(header.Get("CF-Ray")) != "" || strings.EqualFold(strings.TrimSpace(header.Get("CF-Mitigated")), "challenge") || strings.Contains(strings.ToLower(header.Get("Server")), "cloudflare") {
		return true
	}
	lowerBody := strings.ToLower(string(body))
	for _, marker := range []string{"cf-chl", "cdn-cgi/challenge-platform", "attention required", "just a moment", "cloudflare"} {
		if strings.Contains(lowerBody, marker) {
			return true
		}
	}
	return false
}

func decodeCredentialJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, credentialResponseMax))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode credential response: %w", err)
	}
	return nil
}

func generateSessionKeyState() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
