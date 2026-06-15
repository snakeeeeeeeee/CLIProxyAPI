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
		"extra_usage":{"is_enabled":true,"monthly_limit":100,"used_credits":12,"utilization":12}
	}`)

	windows, err := ParseClaudeOAuthUsage(raw)
	if err != nil {
		t.Fatalf("ParseClaudeOAuthUsage() error = %v", err)
	}
	if len(windows) != 5 {
		t.Fatalf("windows len = %d, want 5", len(windows))
	}
	if windows[0].Key != "five_hour" || windows[0].Name != "5 小时" {
		t.Fatalf("first window = %+v", windows[0])
	}
	if windows[0].UsedPercent != 37.2 || windows[0].RemainPercent != 62.8 {
		t.Fatalf("five_hour percentages = %.1f/%.1f", windows[0].UsedPercent, windows[0].RemainPercent)
	}
	if windows[0].ResetsAt == nil || windows[0].ResetsAt.Format(time.RFC3339) != "2026-06-15T10:00:00Z" {
		t.Fatalf("five_hour resets_at = %v", windows[0].ResetsAt)
	}
	extra := windows[4]
	if extra.Key != "extra_usage" || extra.MonthlyLimit == nil || *extra.MonthlyLimit != 100 {
		t.Fatalf("extra window = %+v", extra)
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
