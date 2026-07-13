package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSessionKeyAuthenticateSelectsTeamAndKeepsCookieOffTokenEndpoint(t *testing.T) {
	t.Helper()
	var mu sync.Mutex
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/organizations":
			if cookie, err := r.Cookie("sessionKey"); err != nil || cookie.Value != "sk-ant-sid-test" {
				t.Fatalf("organization cookie = %v, %v", cookie, err)
			}
			assertSessionKeyBrowserHeaders(t, r)
			_, _ = w.Write([]byte(`[{"uuid":"personal"},{"uuid":"team-org","raven_type":"team"}]`))
		case r.URL.Path == "/v1/oauth/team-org/authorize":
			if cookie, err := r.Cookie("sessionKey"); err != nil || cookie.Value != "sk-ant-sid-test" {
				t.Fatalf("authorize cookie = %v, %v", cookie, err)
			}
			assertSessionKeyBrowserHeaders(t, r)
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Fatalf("authorize Accept = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode authorize body: %v", err)
			}
			if body["scope"] != SessionKeyOAuthScope || strings.Contains(body["scope"].(string), "org:create_api_key") {
				t.Fatalf("unexpected scope %v", body["scope"])
			}
			redirect := ClaudeCodeRedirectURI + "?code=auth-code&state=" + body["state"].(string)
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": redirect})
		case r.URL.Path == "/oauth/token":
			if _, err := r.Cookie("sessionKey"); err == nil {
				t.Fatal("token endpoint received sessionKey cookie")
			}
			if got := r.Header.Get("User-Agent"); got != "axios/1.13.6" {
				t.Fatalf("token User-Agent = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access",
				"refresh_token": "refresh",
				"expires_in":    3600,
				"organization":  map[string]string{"uuid": "team-org"},
				"account":       map[string]string{"uuid": "account-id", "email_address": "owner@example.com"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	authenticator := newSessionKeyAuthenticator(server.Client(), server.URL, server.URL+"/oauth/token")
	bundle, err := authenticator.Authenticate(context.Background(), "sk-ant-sid-test")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if bundle.TokenData.AccountUUID != "account-id" || bundle.TokenData.RefreshToken != "refresh" {
		t.Fatalf("unexpected bundle: %+v", bundle.TokenData)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
}

func TestSessionKeyAuthenticateRejectsStateMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"org-id"}]`))
		case "/v1/oauth/org-id/authorize":
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": ClaudeCodeRedirectURI + "?code=auth-code&state=wrong"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := newSessionKeyAuthenticator(server.Client(), server.URL, server.URL+"/oauth/token").Authenticate(context.Background(), "sk-ant-sid-test")
	authErr, ok := err.(*SessionKeyAuthError)
	if !ok || authErr.Code != "state_mismatch" {
		t.Fatalf("Authenticate() error = %v, want state_mismatch", err)
	}
}

func TestSessionKeyAuthenticateRequiresRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"org-id"}]`))
		case strings.HasSuffix(r.URL.Path, "/authorize"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": ClaudeCodeRedirectURI + "?code=auth-code&state=" + body["state"].(string)})
		case r.URL.Path == "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access",
				"expires_in":   3600,
				"organization": map[string]string{"uuid": "org-id"},
				"account":      map[string]string{"uuid": "account-id", "email_address": "owner@example.com"},
			})
		}
	}))
	defer server.Close()

	_, err := newSessionKeyAuthenticator(server.Client(), server.URL, server.URL+"/oauth/token").Authenticate(context.Background(), "sk-ant-sid-test")
	authErr, ok := err.(*SessionKeyAuthError)
	if !ok || authErr.Code != "missing_refresh_token" {
		t.Fatalf("Authenticate() error = %v, want missing_refresh_token", err)
	}
}

func TestSessionKeyAuthenticateDoesNotRetryTokenUnauthorized(t *testing.T) {
	tokenRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"org-id"}]`))
		case strings.HasSuffix(r.URL.Path, "/authorize"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": ClaudeCodeRedirectURI + "?code=auth-code&state=" + body["state"].(string)})
		case r.URL.Path == "/oauth/token":
			tokenRequests++
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"rejected"}`))
		}
	}))
	defer server.Close()

	_, err := newSessionKeyAuthenticator(server.Client(), server.URL, server.URL+"/oauth/token").Authenticate(context.Background(), "sk-ant-sid-test")
	authErr, ok := err.(*SessionKeyAuthError)
	if !ok || authErr.Code != "token_exchange_failed" {
		t.Fatalf("Authenticate() error = %v, want token_exchange_failed", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("token request count = %d, want 1", tokenRequests)
	}
}

func TestSessionKeyAuthenticateClassifiesWebResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		headers     map[string]string
		body        string
		wantCode    string
		wantRetries int
	}{
		{name: "unauthorized session", status: http.StatusUnauthorized, wantCode: "invalid_session", wantRetries: 1},
		{name: "cloudflare challenge", status: http.StatusForbidden, headers: map[string]string{"CF-Mitigated": "challenge"}, body: "Just a moment", wantCode: "proxy_error", wantRetries: 1},
		{name: "ordinary forbidden", status: http.StatusForbidden, body: `{"error":"forbidden"}`, wantCode: "authorize_failed", wantRetries: 1},
		{name: "server error retries once", status: http.StatusBadGateway, wantCode: "authorize_failed", wantRetries: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				for key, value := range test.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			_, err := newSessionKeyAuthenticator(server.Client(), server.URL, server.URL+"/oauth/token").Authenticate(context.Background(), "sk-ant-sid-test")
			authErr, ok := err.(*SessionKeyAuthError)
			if !ok || authErr.Code != test.wantCode || authErr.StatusCode != test.status {
				t.Fatalf("Authenticate() error = %#v, want code=%s status=%d", err, test.wantCode, test.status)
			}
			if requests != test.wantRetries {
				t.Fatalf("request count = %d, want %d", requests, test.wantRetries)
			}
		})
	}
}

func assertSessionKeyBrowserHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	checks := map[string]string{
		"User-Agent":         sessionKeyChromeUserAgent,
		"Sec-Ch-Ua":          sessionKeyChromeSecCHUA,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"macOS"`,
		"Sec-Fetch-Mode":     "navigate",
		"Sec-Fetch-Dest":     "document",
	}
	for key, want := range checks {
		if got := request.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
