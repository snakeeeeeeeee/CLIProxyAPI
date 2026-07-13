package resourcepool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

const (
	defaultQuotaUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	quotaRefreshLeeway     = 2 * time.Minute
	quotaResponseMaxBytes  = 2 << 20
	quotaRequestUserAgent  = "CLIProxyAPI Resource Pool"
	quotaOAuthBetaHeader   = "oauth-2025-04-20"
	quotaDefaultInterval   = 5 * time.Minute
	quotaDefaultConcurrent = 2
)

var claudeOAuthUsageURL = defaultQuotaUsageURL

// RefreshAccountQuota refreshes and stores one Claude OAuth usage snapshot.
func RefreshAccountQuota(ctx context.Context, cfg *config.Config, store *Store, accountID string, auth *coreauth.Auth, persistAuth func(*coreauth.Auth) error) (*ClaudeCodeAccount, error) {
	return refreshAccountQuota(ctx, cfg, store, accountID, auth, persistAuth, false)
}

func refreshAccountQuota(ctx context.Context, cfg *config.Config, store *Store, accountID string, auth *coreauth.Auth, persistAuth func(*coreauth.Auth) error, allowManualRecovery bool) (*ClaudeCodeAccount, error) {
	if store == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	account, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return saveAccountQuotaError(ctx, store, account, 0, "stored auth not found")
	}
	if strings.TrimSpace(auth.ProxyURL) == "" && account.Proxy != nil {
		auth.ProxyURL = strings.TrimSpace(account.Proxy.ProxyURL)
	}

	if err := ensureClaudeAccessToken(ctx, cfg, auth, persistAuth); err != nil {
		return saveAccountQuotaError(ctx, store, account, http.StatusUnauthorized, err.Error())
	}
	raw, statusCode, err := fetchClaudeOAuthUsage(ctx, cfg, auth)
	if err != nil || statusCode >= 500 {
		raw, statusCode, err = fetchClaudeOAuthUsage(ctx, cfg, auth)
	}
	if statusCode == http.StatusUnauthorized {
		if errRefresh := refreshClaudeAccessToken(ctx, cfg, auth, persistAuth); errRefresh != nil {
			return saveAccountQuotaError(ctx, store, account, statusCode, errRefresh.Error())
		}
		raw, statusCode, err = fetchClaudeOAuthUsage(ctx, cfg, auth)
	}
	if err != nil {
		return saveAccountQuotaError(ctx, store, account, statusCode, err.Error())
	}
	if statusCode < 200 || statusCode >= 300 {
		return saveAccountQuotaError(ctx, store, account, statusCode, fmt.Sprintf("usage endpoint returned status %d: %s", statusCode, strings.TrimSpace(string(raw))))
	}
	windows, err := ParseClaudeOAuthUsage(raw)
	if err != nil {
		return saveAccountQuotaError(ctx, store, account, statusCode, err.Error())
	}
	now := time.Now()
	windows = mergeOAuthUsageWindows(account.Quota, windows, now)
	_, err = store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: account.ID,
		Status:    "ok",
		Windows:   windows,
		CheckedAt: &now,
		RawJSON:   string(raw),
		Source:    "oauth_usage",
	})
	if err != nil {
		return nil, err
	}
	return store.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{
		Source:              "oauth_usage",
		Status:              AccountHealthHealthy,
		CheckedAt:           &now,
		NextCheckAt:         timePtr(nextAccountHealthCheck(account.ID, now, quotaDefaultInterval)),
		AllowManualRecovery: allowManualRecovery,
	})
}

// RefreshStoredAccountQuota refreshes quota using only SQLite-backed auth data.
func RefreshStoredAccountQuota(ctx context.Context, configPath string, cfg *config.Config, store *Store, accountID string) (*ClaudeCodeAccount, error) {
	return refreshStoredAccountQuota(ctx, configPath, cfg, store, accountID, false, nil)
}

// RecheckStoredAccountQuota explicitly allows a successful probe to clear manual recovery.
func RecheckStoredAccountQuota(ctx context.Context, configPath string, cfg *config.Config, store *Store, accountID string) (*ClaudeCodeAccount, error) {
	return refreshStoredAccountQuota(ctx, configPath, cfg, store, accountID, true, nil)
}

type accountQuotaAuthSync func(context.Context, *coreauth.Auth) error

func refreshStoredAccountQuota(ctx context.Context, configPath string, cfg *config.Config, store *Store, accountID string, allowManualRecovery bool, syncAuth accountQuotaAuthSync) (*ClaudeCodeAccount, error) {
	auth, err := GetStoredAuth(ctx, configPath, cfg, accountID)
	if err != nil {
		stored, _ := store.GetAccount(ctx, accountID)
		if account, saveErr := saveAccountQuotaError(ctx, store, stored, 0, err.Error()); saveErr == nil {
			return account, err
		}
		return nil, err
	}
	return refreshAccountQuota(ctx, cfg, store, accountID, auth, func(updated *coreauth.Auth) error {
		return persistAndSyncAccountAuth(ctx, store, updated, syncAuth)
	}, allowManualRecovery)
}

func persistAndSyncAccountAuth(ctx context.Context, store *Store, auth *coreauth.Auth, syncAuth accountQuotaAuthSync) error {
	if store == nil {
		return fmt.Errorf("resource pool store is nil")
	}
	if auth == nil {
		return fmt.Errorf("auth is nil")
	}
	if err := store.SaveClaudeCodeAccountAuth(ctx, auth); err != nil {
		return err
	}
	if syncAuth != nil {
		if err := syncAuth(ctx, auth.Clone()); err != nil {
			return fmt.Errorf("sync refreshed runtime auth: %w", err)
		}
	}
	return nil
}

// EffectiveAccountQuota returns normalized background quota refresh settings.
func EffectiveAccountQuota(raw AccountQuotaConfig) AccountQuotaConfig {
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	interval := strings.TrimSpace(raw.Interval)
	if interval == "" {
		interval = quotaDefaultInterval.String()
	}
	if _, err := time.ParseDuration(interval); err != nil {
		interval = quotaDefaultInterval.String()
	}
	concurrency := raw.Concurrency
	if concurrency <= 0 {
		concurrency = quotaDefaultConcurrent
	}
	if concurrency > 8 {
		concurrency = 8
	}
	return AccountQuotaConfig{
		Enabled:     &enabled,
		Interval:    interval,
		Concurrency: concurrency,
	}
}

// ParseClaudeOAuthUsage converts Anthropic's OAuth usage response into UI windows.
func ParseClaudeOAuthUsage(raw []byte) ([]QuotaWindow, error) {
	var payload struct {
		FiveHour       quotaUsageWindow `json:"five_hour"`
		SevenDay       quotaUsageWindow `json:"seven_day"`
		SevenDaySonnet quotaUsageWindow `json:"seven_day_sonnet"`
		SevenDayOpus   quotaUsageWindow `json:"seven_day_opus"`
		SevenDayFable  quotaUsageWindow `json:"seven_day_overage_included"`
		Limits         []struct {
			Kind     string   `json:"kind"`
			Group    string   `json:"group"`
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resets_at"`
			IsActive bool     `json:"is_active"`
			Scope    *struct {
				Model *struct {
					ID          *string `json:"id"`
					DisplayName string  `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
		ExtraUsage struct {
			IsEnabled    bool     `json:"is_enabled"`
			Utilization  *float64 `json:"utilization"`
			MonthlyLimit *float64 `json:"monthly_limit"`
			UsedCredits  *float64 `json:"used_credits"`
		} `json:"extra_usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	for _, limit := range payload.Limits {
		if !strings.EqualFold(strings.TrimSpace(limit.Kind), "weekly_scoped") && !strings.EqualFold(strings.TrimSpace(limit.Group), "weekly") {
			continue
		}
		if !limit.IsActive && strings.TrimSpace(limit.ResetsAt) == "" {
			continue
		}
		if limit.Scope == nil || limit.Scope.Model == nil {
			continue
		}
		modelName := strings.TrimSpace(limit.Scope.Model.DisplayName)
		if limit.Scope.Model.ID != nil && strings.TrimSpace(*limit.Scope.Model.ID) != "" {
			modelName += " " + strings.TrimSpace(*limit.Scope.Model.ID)
		}
		var target *quotaUsageWindow
		switch {
		case strings.Contains(strings.ToLower(modelName), "sonnet"):
			target = &payload.SevenDaySonnet
		case strings.Contains(strings.ToLower(modelName), "opus"):
			target = &payload.SevenDayOpus
		case strings.Contains(strings.ToLower(modelName), "fable"):
			target = &payload.SevenDayFable
		}
		if target == nil || target.Utilization != nil || strings.TrimSpace(target.ResetsAt) != "" {
			continue
		}
		target.Utilization = limit.Percent
		target.ResetsAt = strings.TrimSpace(limit.ResetsAt)
	}
	out := make([]QuotaWindow, 0, 6)
	for _, spec := range []struct {
		key    string
		name   string
		window quotaUsageWindow
	}{
		{key: "five_hour", name: "5 小时", window: payload.FiveHour},
		{key: "seven_day", name: "7 天", window: payload.SevenDay},
		{key: "seven_day_sonnet", name: "Sonnet 周额度", window: payload.SevenDaySonnet},
		{key: "seven_day_opus", name: "Opus 周额度", window: payload.SevenDayOpus},
		{key: "seven_day_fable", name: "Fable 周额度", window: payload.SevenDayFable},
	} {
		if spec.window.Utilization == nil && strings.TrimSpace(spec.window.ResetsAt) == "" {
			continue
		}
		window := quotaWindowFromUsage(spec.key, spec.name, spec.window)
		window.Source = "oauth_usage"
		now := time.Now()
		window.UpdatedAt = &now
		out = append(out, window)
	}
	if payload.ExtraUsage.IsEnabled || payload.ExtraUsage.Utilization != nil || payload.ExtraUsage.MonthlyLimit != nil || payload.ExtraUsage.UsedCredits != nil {
		used := 0.0
		if payload.ExtraUsage.Utilization != nil {
			used = clampPercent(*payload.ExtraUsage.Utilization)
		}
		out = append(out, QuotaWindow{
			Key:              "extra_usage",
			Name:             "额外用量",
			UsedPercent:      used,
			RemainPercent:    clampPercent(100 - used),
			UtilizationKnown: boolPtr(payload.ExtraUsage.Utilization != nil),
			MonthlyLimit:     payload.ExtraUsage.MonthlyLimit,
			UsedCredits:      payload.ExtraUsage.UsedCredits,
			Source:           "oauth_usage",
		})
	}
	return out, nil
}

type quotaUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func quotaWindowFromUsage(key, name string, usage quotaUsageWindow) QuotaWindow {
	used := 0.0
	if usage.Utilization != nil {
		used = clampPercent(*usage.Utilization)
	}
	window := QuotaWindow{
		Key:              key,
		Name:             name,
		UsedPercent:      used,
		RemainPercent:    clampPercent(100 - used),
		UtilizationKnown: boolPtr(usage.Utilization != nil),
	}
	if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(usage.ResetsAt)); err == nil {
		window.ResetsAt = &ts
	}
	return window
}

func saveAccountQuotaError(ctx context.Context, store *Store, current *ClaudeCodeAccount, statusCode int, message string) (*ClaudeCodeAccount, error) {
	if current == nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(message))
	}
	now := time.Now()
	windows := []QuotaWindow{}
	if current.Quota != nil {
		windows = current.Quota.Windows
	}
	account, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: current.ID,
		Status:    "error",
		Windows:   windows,
		CheckedAt: &now,
		LastError: strings.TrimSpace(message),
		Source:    quotaSource(current.Quota),
	})
	if err != nil {
		return nil, err
	}
	status, reason, blockedUntil := classifyQuotaProbeFailure(statusCode, message, now)
	_, _ = store.UpdateAccountHealth(ctx, current.ID, AccountHealthUpdate{
		Source:       "oauth_usage",
		Status:       status,
		Reason:       reason,
		BlockedUntil: blockedUntil,
		CheckedAt:    &now,
		NextCheckAt:  blockedUntil,
	})
	return account, fmt.Errorf("%s", strings.TrimSpace(message))
}

func classifyQuotaProbeFailure(statusCode int, message string, now time.Time) (string, string, *time.Time) {
	lower := strings.ToLower(strings.TrimSpace(message))
	manual := func(reason string) (string, string, *time.Time) {
		return AccountHealthManualRecovery, reason, nil
	}
	temporary := func(reason string, duration time.Duration) (string, string, *time.Time) {
		until := now.Add(duration)
		return AccountHealthTemporarilyBlocked, reason, &until
	}
	switch statusCode {
	case http.StatusUnauthorized:
		if strings.Contains(lower, "invalid_grant") || strings.Contains(lower, "missing refresh_token") || strings.Contains(lower, "invalid refresh") {
			return manual("OAuth 凭据已失效")
		}
		return temporary("OAuth 刷新暂时失败", 10*time.Minute)
	case http.StatusPaymentRequired:
		return manual("账号需要处理账单")
	case http.StatusForbidden:
		if strings.Contains(lower, "cloudflare") || strings.Contains(lower, "challenge") {
			return temporary("Cloudflare 验证", 10*time.Minute)
		}
		if strings.Contains(lower, "suspend") || strings.Contains(lower, "disabled") || strings.Contains(lower, "billing") || strings.Contains(lower, "invalid session") {
			return manual("账号被拒绝，需要人工处理")
		}
		return temporary("上游拒绝访问", 5*time.Minute)
	default:
		return temporary("额度检查暂时失败", 5*time.Minute)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func ensureClaudeAccessToken(ctx context.Context, cfg *config.Config, auth *coreauth.Auth, persistAuth func(*coreauth.Auth) error) error {
	if auth == nil {
		return fmt.Errorf("auth is nil")
	}
	accessToken := metadataString(auth.Metadata, "access_token")
	expiresAt, hasExpiry := auth.ExpirationTime()
	if accessToken != "" && (!hasExpiry || time.Now().Add(quotaRefreshLeeway).Before(expiresAt)) {
		return nil
	}
	return refreshClaudeAccessToken(ctx, cfg, auth, persistAuth)
}

func refreshClaudeAccessToken(ctx context.Context, cfg *config.Config, auth *coreauth.Auth, persistAuth func(*coreauth.Auth) error) error {
	if auth == nil {
		return fmt.Errorf("auth is nil")
	}
	refreshToken := metadataString(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		return fmt.Errorf("missing refresh_token")
	}
	service := claudeauth.NewClaudeAuthWithProxyURL(cfg, auth.ProxyURL)
	tokenData, err := service.RefreshClaudeCodeTokensWithRetry(ctx, refreshToken, 3)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokenData.AccessToken) == "" {
		return fmt.Errorf("refresh response did not include access_token")
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata["access_token"] = strings.TrimSpace(tokenData.AccessToken)
	if strings.TrimSpace(tokenData.RefreshToken) != "" {
		auth.Metadata["refresh_token"] = strings.TrimSpace(tokenData.RefreshToken)
	}
	if strings.TrimSpace(tokenData.Email) != "" {
		auth.Metadata["email"] = strings.TrimSpace(tokenData.Email)
	}
	if strings.TrimSpace(tokenData.OrganizationUUID) != "" {
		auth.Metadata["org_uuid"] = strings.TrimSpace(tokenData.OrganizationUUID)
	}
	if strings.TrimSpace(tokenData.AccountUUID) != "" {
		auth.Metadata["account_uuid"] = strings.TrimSpace(tokenData.AccountUUID)
	}
	if strings.TrimSpace(tokenData.Expire) != "" {
		auth.Metadata["expired"] = strings.TrimSpace(tokenData.Expire)
	}
	auth.Metadata["type"] = "claude"
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)
	if persistAuth != nil {
		if err := persistAuth(auth); err != nil {
			return err
		}
	}
	return nil
}

func fetchClaudeOAuthUsage(ctx context.Context, cfg *config.Config, auth *coreauth.Auth) ([]byte, int, error) {
	accessToken := metadataString(auth.Metadata, "access_token")
	if accessToken == "" {
		return nil, 0, fmt.Errorf("missing access_token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOAuthUsageURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", quotaOAuthBetaHeader)
	req.Header.Set("User-Agent", quotaRequestUserAgent)
	client, closeIdle, err := quotaHTTPClient(cfg, auth)
	if err != nil {
		return nil, 0, err
	}
	if closeIdle != nil {
		defer closeIdle()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, quotaResponseMaxBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func quotaHTTPClient(cfg *config.Config, auth *coreauth.Auth) (*http.Client, func(), error) {
	proxyURL := ""
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}
	transport, _, err := proxyutil.BuildHTTPTransport(proxyURL)
	if err != nil {
		return nil, nil, err
	}
	if transport == nil {
		return http.DefaultClient, nil, nil
	}
	return &http.Client{Transport: transport}, transport.CloseIdleConnections, nil
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key]; ok {
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case fmt.Stringer:
			return strings.TrimSpace(typed.String())
		}
	}
	if tokenRaw, ok := metadata["token"]; ok {
		if tokenMap, ok := tokenRaw.(map[string]any); ok {
			if value, ok := tokenMap[key]; ok {
				if text, ok := value.(string); ok {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}
