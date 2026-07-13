package resourcepool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte("database-path: resource-pools.db\nproxies: []\n"), 0o644); err != nil {
		t.Fatalf("write resource-pools.yaml: %v", err)
	}
	store, err := Open(configPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestParseClaudeOAuthUsage(t *testing.T) {
	raw := []byte(`{
		"five_hour":{"utilization":37.2,"resets_at":"2026-06-15T10:00:00Z"},
		"seven_day":{"utilization":12.8,"resets_at":"2026-06-20T10:00:00Z"},
		"seven_day_sonnet":{"utilization":20.1,"resets_at":"2026-06-20T10:00:00Z"},
		"seven_day_opus":{"utilization":5.4,"resets_at":"2026-06-20T10:00:00Z"},
		"seven_day_overage_included":{"utilization":56.7,"resets_at":"2026-06-20T10:00:00Z"},
		"extra_usage":{"is_enabled":true,"monthly_limit":100,"used_credits":12,"utilization":12}
	}`)

	windows, err := ParseClaudeOAuthUsage(raw)
	if err != nil {
		t.Fatalf("ParseClaudeOAuthUsage() error = %v", err)
	}
	if len(windows) != 6 {
		t.Fatalf("windows len = %d, want 6", len(windows))
	}
	if windows[0].Key != "five_hour" || windows[0].Name != "5 小时" {
		t.Fatalf("first window = %+v", windows[0])
	}
	if windows[0].UsedPercent != 37.2 || windows[0].RemainPercent != 62.8 {
		t.Fatalf("five_hour percentages = %.1f/%.1f", windows[0].UsedPercent, windows[0].RemainPercent)
	}
	if windows[0].UtilizationKnown == nil || !*windows[0].UtilizationKnown {
		t.Fatalf("five_hour utilization confidence = %#v, want known", windows[0].UtilizationKnown)
	}
	if windows[0].ResetsAt == nil || windows[0].ResetsAt.Format(time.RFC3339) != "2026-06-15T10:00:00Z" {
		t.Fatalf("five_hour resets_at = %v", windows[0].ResetsAt)
	}
	if windows[4].Key != "seven_day_fable" || windows[4].UsedPercent != 56.7 {
		t.Fatalf("fable window = %+v", windows[4])
	}
	extra := windows[5]
	if extra.Key != "extra_usage" || extra.MonthlyLimit == nil || *extra.MonthlyLimit != 100 {
		t.Fatalf("extra window = %+v", extra)
	}
}

func TestParseClaudeOAuthUsageSkipsInactiveScopedLimits(t *testing.T) {
	raw := []byte(`{
		"five_hour":{"utilization":0,"resets_at":null},
		"seven_day":{"utilization":0,"resets_at":null},
		"seven_day_sonnet":null,
		"seven_day_opus":null,
		"limits":[
			{"kind":"weekly_scoped","group":"weekly","percent":0,"resets_at":null,"scope":{"model":{"id":null,"display_name":"Fable"}},"is_active":false}
		]
	}`)
	windows, err := ParseClaudeOAuthUsage(raw)
	if err != nil {
		t.Fatalf("ParseClaudeOAuthUsage() error = %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want only shared 5h/7d", windows)
	}
	for _, window := range windows {
		if window.UtilizationKnown == nil || !*window.UtilizationKnown {
			t.Fatalf("shared zero utilization must be known: %#v", window)
		}
		if window.Key == "seven_day_fable" {
			t.Fatalf("inactive Fable window must not be exposed: %#v", window)
		}
	}
}

func TestParseClaudeOAuthUsageActiveScopedLimits(t *testing.T) {
	raw := []byte(`{
		"five_hour":{"utilization":0,"resets_at":null},
		"seven_day":{"utilization":0,"resets_at":null},
		"limits":[
			{"kind":"weekly_scoped","group":"weekly","percent":0,"resets_at":null,"scope":{"model":{"id":null,"display_name":"Fable"}},"is_active":true}
		]
	}`)
	windows, err := ParseClaudeOAuthUsage(raw)
	if err != nil {
		t.Fatalf("ParseClaudeOAuthUsage() error = %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %#v, want shared 5h/7d plus active Fable", windows)
	}
	fable := windows[2]
	if fable.Key != "seven_day_fable" || fable.UsedPercent != 0 || fable.RemainPercent != 100 {
		t.Fatalf("Fable window = %#v", fable)
	}
	if fable.UtilizationKnown == nil || !*fable.UtilizationKnown {
		t.Fatalf("Fable zero utilization confidence = %#v, want known", fable.UtilizationKnown)
	}
}

func TestMergeOAuthUsageWindowsKeepsPassiveFableFallback(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(48 * time.Hour)
	passiveAt := now.Add(-5 * time.Minute)
	existing := &AccountQuota{Windows: []QuotaWindow{{
		Key:         "model_7d_oi",
		UsedPercent: 87,
		ResetsAt:    &reset,
		Source:      "response_headers",
		UpdatedAt:   &passiveAt,
	}}}
	active := []QuotaWindow{{Key: "five_hour", UsedPercent: 10, Source: "oauth_usage", UpdatedAt: &now}}

	merged := mergeOAuthUsageWindows(existing, active, now)
	if len(merged) != 2 {
		t.Fatalf("merged windows = %#v", merged)
	}
	if merged[1].Key != "seven_day_fable" || merged[1].UsedPercent != 87 || merged[1].Source != "response_headers" {
		t.Fatalf("passive fable fallback = %#v", merged[1])
	}
}

func TestMergeOAuthUsageWindowsDropsInactiveOAuthFable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	existing := &AccountQuota{Windows: []QuotaWindow{{
		Key:         "seven_day_fable",
		UsedPercent: 0,
		Source:      "oauth_usage",
		UpdatedAt:   &now,
	}}}
	active := []QuotaWindow{{Key: "five_hour", UsedPercent: 10, Source: "oauth_usage", UpdatedAt: &now}}

	merged := mergeOAuthUsageWindows(existing, active, now)
	if len(merged) != 1 || merged[0].Key != "five_hour" {
		t.Fatalf("merged windows = %#v, want inactive OAuth Fable removed", merged)
	}
}

func TestStoreSaveAccountQuotaRoundTrips(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{
		ID:       "claude-quota-auth",
		Provider: "claude",
		Metadata: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
		Attributes: map[string]string{AttrClaudeOAuthPool: "true"},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "quota@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: account.ID,
		Status:    "ok",
		CheckedAt: &now,
		Windows: []QuotaWindow{
			{Key: "five_hour", Name: "5 小时", UsedPercent: 25, RemainPercent: 75},
		},
		RawJSON: `{"five_hour":{"utilization":25}}`,
	}); err != nil {
		t.Fatalf("SaveAccountQuota() error = %v", err)
	}

	got, err := store.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if got.Quota == nil || got.Quota.Status != "ok" || len(got.Quota.Windows) != 1 {
		t.Fatalf("quota = %+v", got.Quota)
	}
	if got.Quota.Windows[0].RemainPercent != 75 {
		t.Fatalf("remain percent = %.1f", got.Quota.Windows[0].RemainPercent)
	}
	items, err := store.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	found := false
	for _, item := range items {
		if item.ID == account.ID {
			found = item.Quota != nil && item.Quota.Status == "ok"
		}
	}
	if !found {
		t.Fatalf("saved quota was not returned by ListAccounts(): %+v", items)
	}
}

func TestRefreshAccountQuotaSavesUsageSnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			http.Error(w, fmt.Sprintf("Authorization = %q", got), http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("anthropic-beta"); got != quotaOAuthBetaHeader {
			http.Error(w, fmt.Sprintf("anthropic-beta = %q", got), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":20,"resets_at":"2026-06-15T10:00:00Z"},"seven_day":{"utilization":35,"resets_at":"2026-06-20T10:00:00Z"}}`))
	}))
	defer server.Close()

	auth := &coreauth.Auth{
		ID:       "claude-usage-auth",
		Provider: "claude",
		Metadata: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expired":       time.Now().Add(time.Hour).Format(time.RFC3339),
		},
		Attributes: map[string]string{AttrClaudeOAuthPool: "true"},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "usage@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	oldUsageURL := claudeOAuthUsageURL
	claudeOAuthUsageURL = server.URL
	t.Cleanup(func() { claudeOAuthUsageURL = oldUsageURL })

	got, err := RefreshAccountQuota(ctx, &config.Config{}, store, account.ID, auth, nil)
	if err != nil {
		t.Fatalf("RefreshAccountQuota() error = %v", err)
	}
	if got == nil || got.Quota == nil || got.Quota.Status != "ok" || len(got.Quota.Windows) != 2 {
		t.Fatalf("quota after refresh = %+v", got)
	}
	if got.Quota.Windows[0].RemainPercent != 80 {
		t.Fatalf("remain percent = %.1f", got.Quota.Windows[0].RemainPercent)
	}
}

func TestRefreshAccountQuotaSavesErrorSnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{
		ID:       "claude-usage-error-auth",
		Provider: "claude",
		Metadata: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expired":       time.Now().Add(time.Hour).Format(time.RFC3339),
		},
		Attributes: map[string]string{AttrClaudeOAuthPool: "true"},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "usage-error@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	oldUsageURL := claudeOAuthUsageURL
	claudeOAuthUsageURL = "http://127.0.0.1:1/unreachable"
	t.Cleanup(func() { claudeOAuthUsageURL = oldUsageURL })

	got, err := RefreshAccountQuota(ctx, &config.Config{}, store, account.ID, auth, nil)
	if err == nil {
		t.Fatal("RefreshAccountQuota() expected error")
	}
	if got == nil || got.Quota == nil || got.Quota.Status != "error" {
		t.Fatalf("quota after error = %+v", got)
	}
	if !strings.Contains(got.Quota.LastError, "connect") {
		t.Fatalf("last error = %q, want connect error", got.Quota.LastError)
	}
}

func TestPersistAndSyncAccountAuthUpdatesSQLiteAndRuntime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{
		ID:       "claude-refresh-sync-auth",
		Provider: "claude",
		Metadata: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "refresh-token",
		},
		Attributes: map[string]string{AttrClaudeOAuthPool: "true"},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "sync@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	updated := auth.Clone()
	updated.Metadata["access_token"] = "new-access-token"
	synced := false
	err = persistAndSyncAccountAuth(ctx, store, updated, func(_ context.Context, runtimeAuth *coreauth.Auth) error {
		synced = true
		if got := metadataString(runtimeAuth.Metadata, "access_token"); got != "new-access-token" {
			t.Fatalf("runtime access token = %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("persistAndSyncAccountAuth() error = %v", err)
	}
	if !synced {
		t.Fatal("runtime auth sync was not called")
	}

	stored, err := GetStoredAuth(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}, account.ID)
	if err != nil {
		t.Fatalf("GetStoredAuth() error = %v", err)
	}
	if got := metadataString(stored.Metadata, "access_token"); got != "new-access-token" {
		t.Fatalf("stored access token = %q", got)
	}
}
