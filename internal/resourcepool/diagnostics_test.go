package resourcepool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestDatabaseInstanceFingerprintIsPersistentAndCopyStable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, err := store.DatabaseInstanceFingerprint(ctx)
	if err != nil {
		t.Fatalf("DatabaseInstanceFingerprint() error = %v", err)
	}
	second, err := store.DatabaseInstanceFingerprint(ctx)
	if err != nil {
		t.Fatalf("DatabaseInstanceFingerprint() second error = %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("fingerprints = %q / %q", first, second)
	}

	if _, err := store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint error = %v", err)
	}
	sourcePath := store.Path()
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	copyDir := t.TempDir()
	copyDB := filepath.Join(copyDir, "copied.db")
	if err := os.WriteFile(copyDB, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(copy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "resource-pools.yaml"), []byte("database-path: copied.db\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	copied, err := Open(filepath.Join(copyDir, "config.yaml"), &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("Open(copy) error = %v", err)
	}
	defer func() { _ = copied.Close() }()
	copiedFingerprint, err := copied.DatabaseInstanceFingerprint(ctx)
	if err != nil {
		t.Fatalf("copied DatabaseInstanceFingerprint() error = %v", err)
	}
	if copiedFingerprint != first {
		t.Fatalf("copied fingerprint = %q, want %q", copiedFingerprint, first)
	}

	fresh := newTestStore(t)
	freshFingerprint, err := fresh.DatabaseInstanceFingerprint(ctx)
	if err != nil {
		t.Fatalf("fresh DatabaseInstanceFingerprint() error = %v", err)
	}
	if freshFingerprint == first {
		t.Fatalf("fresh fingerprint unexpectedly equals copied identity %q", first)
	}
}

func TestDiagnosticsRedactsCredentialsAndRequestIdentity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{
		Name:     "diagnostic proxy",
		ProxyURL: "http://proxy-user:proxy-password@127.0.0.1:19090",
		ExitIP:   "203.0.113.44",
	})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "sensitive-auth-id",
		Provider: "claude",
		ProxyURL: proxy.ProxyURL,
		Metadata: map[string]any{
			"email":         "private@example.com",
			"access_token":  "access-secret-value",
			"refresh_token": "refresh-secret-value",
			"session_id":    "session-secret-value",
			"expired":       time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "private@example.com", proxy.ID, auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: account.ID,
		Status:    "ok",
		CheckedAt: &now,
		Windows:   []QuotaWindow{{Key: "five_hour", UsedPercent: 10}},
		Probe: &AccountQuotaProbe{
			RequestedAt:      now,
			ProfileRevision:  DefaultClaudeCodeProfileRevision,
			TransportProfile: "oauth-usage/node-macos-arm64-http1",
			ProxyMode:        "bound",
			ProxyResourceID:  proxy.ID,
			StatusCode:       200,
		},
	}); err != nil {
		t.Fatalf("SaveAccountQuota() error = %v", err)
	}

	runtimeConfig := &config.Config{}
	runtimeConfig.ProxyURL = "direct"
	diagnostics, err := store.Diagnostics(ctx, runtimeConfig, now)
	if err != nil {
		t.Fatalf("Diagnostics() error = %v", err)
	}
	if len(diagnostics.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(diagnostics.Accounts))
	}
	item := diagnostics.Accounts[0]
	if item.AccountFingerprint == "" || item.AccountFingerprint == account.ID || item.DeviceFingerprint == "" {
		t.Fatalf("account diagnostic identity = %+v", item)
	}
	if item.LastObservedExitIP != "203.0.113.44" || item.ProxyResourceID != proxy.ID {
		t.Fatalf("account diagnostic proxy summary = %+v", item)
	}
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	serialized := string(raw)
	for _, secret := range []string{
		"sensitive-auth-id",
		"private@example.com",
		"access-secret-value",
		"refresh-secret-value",
		"session-secret-value",
		"proxy-user",
		"proxy-password",
		proxy.ProxyURL,
		account.CloakUserID,
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, serialized)
		}
	}
	var probeJSON string
	if err := store.db.QueryRowContext(ctx, `SELECT probe_json FROM claude_code_account_quota WHERE account_id = ?`, account.ID).Scan(&probeJSON); err != nil {
		t.Fatalf("read probe_json error = %v", err)
	}
	for _, secret := range []string{"access-secret-value", "refresh-secret-value", "session-secret-value", proxy.ProxyURL} {
		if strings.Contains(probeJSON, secret) {
			t.Fatalf("probe_json leaked %q: %s", secret, probeJSON)
		}
	}
}
