package resourcepool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

func TestStoreImportsYAMLAndListsAvailableProxies(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte(`
database-path: resource-pools.db
proxies:
  - name: 美国出口
    proxy-url: http://user:pass@127.0.0.1:8080
    exit-ip: 203.0.113.10
    tags: [us, claude]
`), 0o644); err != nil {
		t.Fatalf("write init yaml: %v", err)
	}
	store, err := Open(configPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	proxies, err := store.ListAvailableProxies(context.Background())
	if err != nil {
		t.Fatalf("ListAvailableProxies() error = %v", err)
	}
	if len(proxies) != 1 {
		t.Fatalf("available proxy count = %d, want 1", len(proxies))
	}
	if got := proxies[0].ProxyURLPreview; strings.Contains(got, "pass") || !strings.Contains(got, "redacted") {
		t.Fatalf("proxy preview = %q, want redacted credentials", got)
	}
}

func TestOpenSerializesConcurrentNewDatabaseInitialization(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte("database-path: resource-pools.db\n"), 0o644); err != nil {
		t.Fatalf("write init yaml: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}

	const openers = 16
	start := make(chan struct{})
	stores := make(chan *Store, openers)
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			store, err := Open(configPath, cfg)
			if err != nil {
				errs <- err
				return
			}
			stores <- store
		}()
	}
	close(start)
	wg.Wait()
	close(stores)
	close(errs)

	for store := range stores {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}
	for err := range errs {
		t.Errorf("Open() error = %v", err)
	}
}

func TestOpenSQLiteStoreMigratesLegacyMultiPoolColumnsBeforeIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	legacySchema := `
CREATE TABLE claude_code_accounts (
	id TEXT PRIMARY KEY,
	auth_id TEXT NOT NULL UNIQUE,
	cloak_user_id TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL
);
CREATE TABLE claude_code_routing_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE TABLE claude_code_usage_ledger (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	raw_input_tokens INTEGER NOT NULL DEFAULT 0,
	raw_total_tokens INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
INSERT INTO claude_code_accounts(id, auth_id, updated_at) VALUES('account-1', 'auth-1', '2026-07-12T00:00:00Z');
INSERT INTO claude_code_routing_events(account_id, created_at) VALUES('account-1', '2026-07-12T00:00:00Z');
INSERT INTO claude_code_usage_ledger(account_id, model, created_at) VALUES('account-1', 'claude-sonnet-4-6', '2026-07-12T00:00:00Z');
`
	if _, err := legacyDB.Exec(legacySchema); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("seed legacy sqlite: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	db, err := openSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("openSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, table := range []string{"claude_code_accounts", "claude_code_routing_events", "claude_code_usage_ledger"} {
		var poolID string
		if err := db.QueryRow(`SELECT pool_id FROM ` + table + ` LIMIT 1`).Scan(&poolID); err != nil {
			t.Fatalf("read migrated %s pool id: %v", table, err)
		}
		if poolID != DefaultAccountPoolID {
			t.Fatalf("migrated %s pool id = %q, want %q", table, poolID, DefaultAccountPoolID)
		}
	}

	for _, index := range []string{
		"idx_claude_code_accounts_pool",
		"idx_claude_code_routing_events_pool",
		"idx_claude_code_usage_ledger_pool",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatalf("inspect migrated index %s: %v", index, err)
		}
		if count != 1 {
			t.Fatalf("migrated index %s count = %d, want 1", index, count)
		}
	}
}

func TestStoreEnforcesOneToOneBinding(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "socks5://127.0.0.1:1080"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	first, err := store.RegisterClaudeCodeAccount(ctx, "claude-a.json", "a@example.com", proxy.ID)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccount(first) error = %v", err)
	}
	if first.ProxyResourceID != proxy.ID {
		t.Fatalf("first.ProxyResourceID = %q, want %q", first.ProxyResourceID, proxy.ID)
	}
	if _, err := store.RegisterClaudeCodeAccount(ctx, "claude-b.json", "b@example.com", proxy.ID); err == nil {
		t.Fatalf("RegisterClaudeCodeAccount(second) succeeded with already bound proxy")
	}
	if _, err := store.UnbindAccountProxy(ctx, first.ID); err != nil {
		t.Fatalf("UnbindAccountProxy() error = %v", err)
	}
	second, err := store.RegisterClaudeCodeAccount(ctx, "claude-b.json", "b@example.com", proxy.ID)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccount(second after unbind) error = %v", err)
	}
	if second.ProxyResourceID != proxy.ID {
		t.Fatalf("second.ProxyResourceID = %q, want %q", second.ProxyResourceID, proxy.ID)
	}
}

func TestProxyReservationsExcludeAndConsumeHealthyProxy(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	if _, err := store.UpdateProxyHealth(ctx, proxy.ID, true, time.Millisecond, nil, 1); err != nil {
		t.Fatalf("UpdateProxyHealth() error = %v", err)
	}
	reservations, err := store.ReserveHealthyProxies(ctx, "job-1", "session-key-login", []string{"item-1"}, 2*time.Minute)
	if err != nil {
		t.Fatalf("ReserveHealthyProxies() error = %v", err)
	}
	if len(reservations) != 1 || reservations[0].ProxyResourceID != proxy.ID {
		t.Fatalf("reservations = %+v, want proxy %s", reservations, proxy.ID)
	}
	available, err := store.ListAvailableProxies(ctx)
	if err != nil {
		t.Fatalf("ListAvailableProxies() error = %v", err)
	}
	if len(available) != 0 {
		t.Fatalf("available count = %d, want 0 while reserved", len(available))
	}
	if _, err := store.RegisterClaudeCodeAccount(ctx, "claude-other.json", "other@example.com", proxy.ID); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("RegisterClaudeCodeAccount() error = %v, want reserved", err)
	}
	account, err := store.RegisterClaudeCodeAccountWithAuthReservation(ctx, "claude-owner.json", "owner@example.com", proxy.ID, nil, "job-1", "item-1")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuthReservation() error = %v", err)
	}
	if account.ProxyResourceID != proxy.ID {
		t.Fatalf("account proxy = %q, want %q", account.ProxyResourceID, proxy.ID)
	}
	if _, err := store.GetProxyReservation(ctx, "job-1", "item-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetProxyReservation() error = %v, want sql.ErrNoRows", err)
	}
}

func TestProxyReservationsReleaseAndExpire(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	for _, port := range []string{"18081", "18082"} {
		proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:" + port})
		if err != nil {
			t.Fatalf("CreateProxy() error = %v", err)
		}
		if _, err := store.UpdateProxyHealth(ctx, proxy.ID, true, time.Millisecond, nil, 1); err != nil {
			t.Fatalf("UpdateProxyHealth() error = %v", err)
		}
	}
	reservations, err := store.ReserveHealthyProxies(ctx, "job-2", "session-key-login", []string{"item-1", "item-2", "item-3"}, 2*time.Minute)
	if err != nil {
		t.Fatalf("ReserveHealthyProxies() error = %v", err)
	}
	if len(reservations) != 2 {
		t.Fatalf("reservation count = %d, want 2", len(reservations))
	}
	if err := store.ReleaseProxyReservation(ctx, "job-2", "item-1"); err != nil {
		t.Fatalf("ReleaseProxyReservation() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE proxy_reservations SET expires_at = ? WHERE owner_id = ?`, dbTime(time.Now().Add(-time.Minute)), "job-2"); err != nil {
		t.Fatalf("expire reservation: %v", err)
	}
	available, err := store.ListHealthyAvailableProxies(ctx)
	if err != nil {
		t.Fatalf("ListHealthyAvailableProxies() error = %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("healthy available count = %d, want 2", len(available))
	}
}

func TestRegisterClaudeCodeAccountGeneratesClaudeCodeUserID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	account, err := store.RegisterClaudeCodeAccount(ctx, "claude-user.json", "user@example.com", "")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccount() error = %v", err)
	}
	if !strings.HasPrefix(account.CloakUserID, "user_") || !helps.IsValidUserID(account.CloakUserID) {
		t.Fatalf("CloakUserID = %q, want Claude Code metadata.user_id format", account.CloakUserID)
	}
}

func TestNewAccountStartsCheckingAndManualRecoveryNeedsExplicitClear(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	account, err := store.RegisterClaudeCodeAccount(ctx, "claude-health.json", "health@example.com", "")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccount() error = %v", err)
	}
	if account.HealthStatus != AccountHealthChecking || account.EffectiveSchedulable {
		t.Fatalf("new account lifecycle = %+v", account)
	}
	now := time.Now()
	account, err = store.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{Status: AccountHealthManualRecovery, Reason: "billing", CheckedAt: &now})
	if err != nil {
		t.Fatalf("UpdateAccountHealth(manual) error = %v", err)
	}
	account, err = store.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{Status: AccountHealthHealthy, CheckedAt: &now})
	if err != nil {
		t.Fatalf("UpdateAccountHealth(success) error = %v", err)
	}
	if account.HealthStatus != AccountHealthManualRecovery {
		t.Fatalf("ordinary success cleared manual recovery: %+v", account)
	}
	account, err = store.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{Status: AccountHealthHealthy, CheckedAt: &now, AllowManualRecovery: true})
	if err != nil {
		t.Fatalf("UpdateAccountHealth(explicit recovery) error = %v", err)
	}
	if account.HealthStatus != AccountHealthHealthy {
		t.Fatalf("explicit recovery did not clear state: %+v", account)
	}
}

func TestImportProxiesSkipsDuplicatesAndAvailableFiltersBound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	result, err := store.ImportProxies(ctx, []ProxyResourceSeed{
		{ProxyURL: "http://127.0.0.1:8001"},
		{ProxyURL: "http://127.0.0.1:8001"},
		{ProxyURL: "http://127.0.0.1:8002"},
	})
	if err != nil {
		t.Fatalf("ImportProxies() error = %v", err)
	}
	if result.Created != 2 || result.Skipped != 1 {
		t.Fatalf("ImportProxies() = %+v, want created=2 skipped=1", result)
	}
	available, err := store.ListAvailableProxies(ctx)
	if err != nil {
		t.Fatalf("ListAvailableProxies() error = %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("available before bind = %d, want 2", len(available))
	}
	if _, err := store.RegisterClaudeCodeAccount(ctx, "claude-a.json", "a@example.com", available[0].ID); err != nil {
		t.Fatalf("RegisterClaudeCodeAccount() error = %v", err)
	}
	available, err = store.ListAvailableProxies(ctx)
	if err != nil {
		t.Fatalf("ListAvailableProxies(after bind) error = %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("available after bind = %d, want 1", len(available))
	}
}

func TestUpdateProxyHealthFailureThreshold(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:9000"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	first, err := store.UpdateProxyHealth(ctx, proxy.ID, false, 10*time.Millisecond, context.DeadlineExceeded, 2)
	if err != nil {
		t.Fatalf("UpdateProxyHealth(first) error = %v", err)
	}
	if first.HealthStatus != HealthUnknown || first.ConsecutiveFailures != 1 {
		t.Fatalf("first health = %+v, want unknown failure=1", first)
	}
	second, err := store.UpdateProxyHealth(ctx, proxy.ID, false, 12*time.Millisecond, context.DeadlineExceeded, 2)
	if err != nil {
		t.Fatalf("UpdateProxyHealth(second) error = %v", err)
	}
	if second.HealthStatus != HealthUnhealthy || second.ConsecutiveFailures != 2 {
		t.Fatalf("second health = %+v, want unhealthy failure=2", second)
	}
	ok, err := store.UpdateProxyHealth(ctx, proxy.ID, true, 8*time.Millisecond, nil, 2)
	if err != nil {
		t.Fatalf("UpdateProxyHealth(success) error = %v", err)
	}
	if ok.HealthStatus != HealthHealthy || ok.ConsecutiveFailures != 0 {
		t.Fatalf("success health = %+v, want healthy failure=0", ok)
	}
}

func TestStoreConfigSQLiteWinsAfterInitialImport(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	disabled := false
	health := ProxyHealthConfig{
		Enabled:          &disabled,
		Interval:         "17m",
		Timeout:          "3s",
		Concurrency:      2,
		FailureThreshold: 4,
		TestURL:          "https://example.test/health",
	}
	raw, err := json.Marshal(health)
	if err != nil {
		t.Fatalf("marshal health config: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ?, updated_at = ? WHERE key = 'proxy_health_json'`, string(raw), dbTime(time.Now())); err != nil {
		t.Fatalf("update sqlite config: %v", err)
	}
	doc, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	effective := EffectiveProxyHealth(doc.ProxyHealth)
	if effective.Enabled {
		t.Fatalf("EffectiveProxyHealth.Enabled = true, want false from sqlite")
	}
	if effective.IntervalText != "17m0s" || effective.TimeoutText != "3s" || effective.Concurrency != 2 || effective.FailureThreshold != 4 {
		t.Fatalf("effective health config = %+v, want sqlite override", effective)
	}
	if effective.TestURL != "https://example.test/health" {
		t.Fatalf("TestURL = %q, want sqlite value", effective.TestURL)
	}
}

func TestTraceConfigDefaultsAndYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte(`
database-path: resource-pools.db
trace:
  enabled: true
  dump-dir: traces/custom
  redact-user-content: false
`), 0o644); err != nil {
		t.Fatalf("write resource-pools.yaml: %v", err)
	}
	doc, err := LoadConfigFile(initPath)
	if err != nil {
		t.Fatalf("LoadConfigFile() error = %v", err)
	}
	effective := EffectiveTrace(doc.Trace)
	if !effective.Enabled || effective.DumpDir != "traces/custom" || effective.RedactUserContent {
		t.Fatalf("effective trace = %+v, want enabled custom dir redact=false", effective)
	}
	store, err := Open(configPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	stored, err := store.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	effective = EffectiveTrace(stored.Trace)
	if !effective.Enabled || effective.DumpDir != "traces/custom" || effective.RedactUserContent {
		t.Fatalf("stored effective trace = %+v, want enabled custom dir redact=false", effective)
	}
	defaultTrace := EffectiveTrace(TraceConfig{})
	if defaultTrace.Enabled || defaultTrace.DumpDir != "traces/ours" || !defaultTrace.RedactUserContent {
		t.Fatalf("default trace = %+v, want disabled traces/ours redact=true", defaultTrace)
	}
}

func TestStoreEmptyListsReturnNonNilSlices(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxies, err := store.ListProxies(ctx)
	if err != nil {
		t.Fatalf("ListProxies() error = %v", err)
	}
	if proxies == nil {
		t.Fatalf("ListProxies() returned nil slice")
	}
	available, err := store.ListAvailableProxies(ctx)
	if err != nil {
		t.Fatalf("ListAvailableProxies() error = %v", err)
	}
	if available == nil {
		t.Fatalf("ListAvailableProxies() returned nil slice")
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if accounts == nil {
		t.Fatalf("ListAccounts() returned nil slice")
	}
}

func TestAccountAvailabilityAggregatesTwoHourMinuteBuckets(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	account, err := store.RegisterClaudeCodeAccount(ctx, "claude-availability.json", "availability@example.com", "")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccount() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	entries := []UsageLedgerEntry{
		{AccountID: account.ID, Success: true, CreatedAt: now.Add(-2 * time.Minute)},
		{AccountID: account.ID, Success: true, CreatedAt: now.Add(-2 * time.Minute).Add(5 * time.Second)},
		{AccountID: account.ID, Success: true, CreatedAt: now.Add(-1 * time.Minute)},
		{AccountID: account.ID, Success: false, CreatedAt: now.Add(-1 * time.Minute).Add(10 * time.Second)},
		{AccountID: account.ID, Success: false, CreatedAt: now},
	}
	for _, entry := range entries {
		if err := store.RecordUsageLedger(ctx, entry); err != nil {
			t.Fatalf("RecordUsageLedger() error = %v", err)
		}
	}
	availability, err := store.AccountAvailability(ctx, account.ID, 2*time.Hour)
	if err != nil {
		t.Fatalf("AccountAvailability() error = %v", err)
	}
	if availability.WindowMinutes != 120 || len(availability.Buckets) != 120 {
		t.Fatalf("bucket count = window %d len %d, want 120", availability.WindowMinutes, len(availability.Buckets))
	}
	if availability.RequestCount != 5 || availability.SuccessCount != 3 || availability.FailureCount != 2 {
		t.Fatalf("availability totals = %+v, want requests=5 success=3 failures=2", availability)
	}
	if availability.Status != "degraded" {
		t.Fatalf("availability status = %q, want degraded", availability.Status)
	}
	byMinute := map[string]AccountAvailabilityBucket{}
	for _, bucket := range availability.Buckets {
		byMinute[bucket.StartedAt.Format(time.RFC3339)] = bucket
	}
	healthy := byMinute[now.Add(-2*time.Minute).Format(time.RFC3339)]
	if healthy.Status != "healthy" || healthy.RequestCount != 2 || healthy.SuccessCount != 2 {
		t.Fatalf("healthy bucket = %+v, want 2/2 healthy", healthy)
	}
	degraded := byMinute[now.Add(-1*time.Minute).Format(time.RFC3339)]
	if degraded.Status != "degraded" || degraded.RequestCount != 2 || degraded.SuccessCount != 1 {
		t.Fatalf("degraded bucket = %+v, want 1/2 degraded", degraded)
	}
	unhealthy := byMinute[now.Format(time.RFC3339)]
	if unhealthy.Status != "unhealthy" || unhealthy.RequestCount != 1 || unhealthy.SuccessCount != 0 {
		t.Fatalf("unhealthy bucket = %+v, want 0/1 unhealthy", unhealthy)
	}
	if availability.Buckets[0].Status != "none" || availability.Buckets[0].RequestCount != 0 {
		t.Fatalf("empty bucket = %+v, want none", availability.Buckets[0])
	}
}

func TestStorePersistsClaudeCodeAuthJSONAndSynthesizesAuth(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":          "claude",
			"email":         "user@example.com",
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "user@example.com", proxy.ID, auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	if !account.HasAuthData {
		t.Fatalf("account.HasAuthData = false, want true")
	}
	auths, err := ListStoredAuths(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("ListStoredAuths() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("stored auth count = %d, want 1", len(auths))
	}
	got := auths[0]
	if got.ID != auth.ID || got.Provider != "claude" {
		t.Fatalf("stored auth = %+v, want id=%q provider=claude", got, auth.ID)
	}
	if got.Metadata["access_token"] != "access-token" || got.Metadata["refresh_token"] != "refresh-token" {
		t.Fatalf("stored auth metadata missing tokens: %+v", got.Metadata)
	}
	if got.ProxyURL != proxy.ProxyURL {
		t.Fatalf("stored auth ProxyURL = %q, want %q", got.ProxyURL, proxy.ProxyURL)
	}
	if got.Attributes[AttrClaudeOAuthPool] != "true" || got.Attributes[AttrAccountID] != account.ID {
		t.Fatalf("stored auth attributes = %+v, want pool/account attrs", got.Attributes)
	}
	if got.Attributes[claudeapipool.AttrOAuthPool] != "true" {
		t.Fatalf("stored auth attributes = %+v, want claude oauth pool attr", got.Attributes)
	}
	if got.Attributes[claudeapipool.AttrPureMode] != "true" {
		t.Fatalf("stored auth attributes = %+v, want pure mode attr from defaults", got.Attributes)
	}
}

func TestStoreSaveClaudeCodeAccountAuthUpdatesJSON(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	expireAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	auth := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":          "claude",
			"email":         "user@example.com",
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
		},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "user@example.com", "", auth); err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	auth.Metadata["access_token"] = "new-access"
	auth.Metadata["refresh_token"] = "new-refresh"
	auth.Metadata["expired"] = expireAt.Format(time.RFC3339)
	if err := store.SaveClaudeCodeAccountAuth(ctx, auth); err != nil {
		t.Fatalf("SaveClaudeCodeAccountAuth() error = %v", err)
	}
	auths, err := ListStoredAuths(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("ListStoredAuths() error = %v", err)
	}
	if got := auths[0].Metadata["access_token"]; got != "new-access" {
		t.Fatalf("access_token = %q, want new-access", got)
	}
	if got := auths[0].Metadata["refresh_token"]; got != "new-refresh" {
		t.Fatalf("refresh_token = %q, want new-refresh", got)
	}
	account, err := store.GetAccountByAuthID(ctx, auth.ID)
	if err != nil {
		t.Fatalf("GetAccountByAuthID() error = %v", err)
	}
	if account.TokenExpiresAt == nil || !account.TokenExpiresAt.Equal(expireAt) {
		t.Fatalf("TokenExpiresAt = %v, want %v", account.TokenExpiresAt, expireAt)
	}
}

func TestStoreClaudeCodeModelsResolveEnabledAlias(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	model, err := store.UpsertModel(ctx, ClaudeCodeModel{
		Name:    "claude-sonnet-4-20260601",
		Alias:   "sonnet-4",
		Enabled: true,
		Source:  "manual",
	})
	if err != nil {
		t.Fatalf("UpsertModel() error = %v", err)
	}
	if model.Alias != "sonnet-4" || model.Name != "claude-sonnet-4-20260601" {
		t.Fatalf("model = %+v, want alias/name mapping", model)
	}
	upstream, ok, err := store.ResolveModelAlias(ctx, "sonnet-4")
	if err != nil {
		t.Fatalf("ResolveModelAlias() error = %v", err)
	}
	if !ok || upstream != "claude-sonnet-4-20260601" {
		t.Fatalf("ResolveModelAlias() = %q,%v want upstream,true", upstream, ok)
	}
	enabled := false
	if _, err := store.PatchModel(ctx, model.ID, ClaudeCodeModelPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("PatchModel(disable) error = %v", err)
	}
	_, ok, err = store.ResolveModelAlias(ctx, "sonnet-4")
	if err != nil {
		t.Fatalf("ResolveModelAlias(disabled) error = %v", err)
	}
	if ok {
		t.Fatalf("ResolveModelAlias(disabled) ok=true, want false")
	}
}

func TestStoreSavesClaudeCodePoolConfig(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	enabled := true
	pure := false
	clean := true
	allowClientCacheTTL := true
	doc, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		Enabled:             &enabled,
		PureMode:            &pure,
		AllowClientCacheTTL: &allowClientCacheTTL,
		Cloak: &config.CloakConfig{
			Mode:           "always",
			StrictMode:     true,
			SensitiveWords: []string{"Claude", "Anthropic", "claude"},
		},
		Usage: ClaudeCodeUsageConfig{
			CleanInputTokens: &clean,
		},
		Routing: claudeapipool.RoutingConfig{
			PerAccountRPM:         3,
			PerAccountConcurrency: 1,
		},
	})
	if err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	effective := EffectiveClaudeCodePool(doc.ClaudeCode)
	if !effective.Enabled || effective.PureMode {
		t.Fatalf("effective enabled/pure = %v/%v, want true/false", effective.Enabled, effective.PureMode)
	}
	if !effective.AllowClientCacheTTL {
		t.Fatal("effective allow_client_cache_ttl = false, want true")
	}
	if effective.Cloak.Mode != "always" || !effective.Cloak.StrictMode {
		t.Fatalf("effective cloak = %+v, want always strict", effective.Cloak)
	}
	if strings.Join(effective.Cloak.SensitiveWords, ",") != "Claude,Anthropic" {
		t.Fatalf("effective cloak sensitive words = %+v, want deduped words", effective.Cloak.SensitiveWords)
	}
	if effective.Usage.CleanInputTokens || effective.Usage.SystemPromptOverheadTokens != DefaultCleanInputOverheadTokens {
		t.Fatalf("effective usage = %+v, want pure mode to keep cleaning disabled with default overhead", effective.Usage)
	}
	if effective.Usage.ProfileFingerprint == "" {
		t.Fatal("effective usage profile fingerprint is empty")
	}
	if effective.Routing.PerAccountRPM != 3 {
		t.Fatalf("routing rpm = %d, want 3", effective.Routing.PerAccountRPM)
	}
}

func TestClaudeCodeUsageConfigUnmarshalJSONAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{
			name: "snake case",
			raw:  `{"usage":{"clean_input_tokens":true,"system_prompt_overhead_tokens":1909}}`,
		},
		{
			name: "kebab case",
			raw:  `{"usage":{"clean-input-tokens":true,"system-prompt-overhead-tokens":1909}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ClaudeCodePoolConfig
			if err := json.Unmarshal([]byte(tc.raw), &cfg); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if cfg.Usage.CleanInputTokens == nil || !*cfg.Usage.CleanInputTokens {
				t.Fatalf("clean input tokens = %v, want true", cfg.Usage.CleanInputTokens)
			}
			if cfg.Usage.SystemPromptOverheadTokens != 1909 {
				t.Fatalf("overhead = %d, want 1909", cfg.Usage.SystemPromptOverheadTokens)
			}
		})
	}
}

func TestStoreUsageCalibrationAndAuthOverlay(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	clean := true
	doc, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		Usage: ClaudeCodeUsageConfig{
			CleanInputTokens:           &clean,
			SystemPromptOverheadTokens: 1909,
		},
	})
	if err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	fingerprint := EffectiveClaudeCodePool(doc.ClaudeCode).Usage.ProfileFingerprint
	if fingerprint == "" {
		t.Fatal("profile fingerprint is empty")
	}
	calibration, err := store.UpsertUsageCalibration(ctx, UsageCalibration{
		Model:              "claude-opus-4-8",
		ProfileFingerprint: fingerprint,
		OverheadTokens:     1909,
		Status:             UsageCalibrationCalibrated,
	})
	if err != nil {
		t.Fatalf("UpsertUsageCalibration() error = %v", err)
	}
	if calibration.Status != UsageCalibrationCalibrated || calibration.OverheadTokens != 1909 {
		t.Fatalf("calibration = %+v, want calibrated 1909", calibration)
	}
	auth := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":         "claude",
			"email":        "user@example.com",
			"access_token": "access-token",
		},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "user@example.com", "", auth); err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	auths, err := ListStoredAuths(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("ListStoredAuths() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("stored auth count = %d, want 1", len(auths))
	}
	attrs := auths[0].Attributes
	if attrs[AttrCleanInputTokens] != "true" {
		t.Fatalf("clean input attr = %q, want true", attrs[AttrCleanInputTokens])
	}
	if attrs[AttrProfileFingerprint] != fingerprint {
		t.Fatalf("fingerprint attr = %q, want %q", attrs[AttrProfileFingerprint], fingerprint)
	}
	var overheads map[string]int64
	if err := json.Unmarshal([]byte(attrs[AttrUsageOverheadsJSON]), &overheads); err != nil {
		t.Fatalf("decode overhead map: %v", err)
	}
	if overheads["claude-opus-4-8"] != 1909 {
		t.Fatalf("overhead map = %+v, want claude-opus-4-8=1909", overheads)
	}
}

func TestUsagePluginKeepsRawUsageInPureMode(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	pure := true
	_, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		PureMode: &pure,
		Usage: ClaudeCodeUsageConfig{
			SystemPromptOverheadTokens: 1909,
		},
	})
	if err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "claude-clean@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":         "claude",
			"email":        "clean@example.com",
			"access_token": "access-token",
		},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "clean@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	plugin := UsagePlugin{
		ConfigPath: store.initPath,
		Config:     &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}},
	}
	plugin.HandleUsage(ctx, coreusage.Record{
		Provider: "claude",
		Model:    "claude-opus-4-8",
		Alias:    "claude-opus-4-8",
		AuthID:   auth.ID,
		APIKey:   "sk-test-abcdef123456",
		Detail: coreusage.Detail{
			InputTokens:         1910,
			OutputTokens:        5,
			CacheReadTokens:     15933,
			CacheCreationTokens: 2386,
		},
		RequestedAt: time.Now(),
	})
	reopened, err := Open(store.initPath, plugin.Config)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	summary, err := reopened.UsageSummary(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if summary.InputTokens != 1910 || summary.OutputTokens != 5 || summary.CacheReadTokens != 15933 || summary.CacheCreationTokens != 2386 {
		t.Fatalf("usage summary = %+v, want raw Anthropic usage", summary)
	}
	if summary.RawInputTokens != 1910 || summary.RawTotalTokens != 20234 {
		t.Fatalf("raw usage summary = input %d total %d, want 1910/20234", summary.RawInputTokens, summary.RawTotalTokens)
	}
	if len(summary.Recent) != 1 || summary.Recent[0].AccountID != account.ID {
		t.Fatalf("recent usage = %+v, want one row for account %s", summary.Recent, account.ID)
	}
}

func TestListStoredAuthsAppliesClaudeCodePoolCloakAttributes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	pure := false
	if _, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		PureMode: &pure,
		Cloak: &config.CloakConfig{
			Mode:           "always",
			StrictMode:     true,
			SensitiveWords: []string{"Cursor", "Windsurf"},
		},
	}); err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":         "claude",
			"email":        "user@example.com",
			"access_token": "access-token",
		},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "user@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	auths, err := ListStoredAuths(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("ListStoredAuths() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("stored auth count = %d, want 1", len(auths))
	}
	attrs := auths[0].Attributes
	if attrs[claudeapipool.AttrPureMode] != "false" {
		t.Fatalf("pure mode attr = %q, want explicit false when disabled", attrs[claudeapipool.AttrPureMode])
	}
	if attrs[AttrAllowClientCacheTTL] != "false" {
		t.Fatalf("allow client cache TTL attr = %q, want default false", attrs[AttrAllowClientCacheTTL])
	}
	if attrs[claudeapipool.AttrOAuthPool] != "true" || attrs[AttrClaudeOAuthPool] != "true" {
		t.Fatalf("pool attrs = %+v, want oauth pool attrs", attrs)
	}
	if attrs["cloak_mode"] != "always" || attrs["cloak_strict_mode"] != "true" {
		t.Fatalf("cloak attrs = %+v, want always/strict", attrs)
	}
	if attrs["cloak_cache_user_id"] != "" {
		t.Fatalf("cloak_cache_user_id = %q, want empty", attrs["cloak_cache_user_id"])
	}
	if attrs["cloak_user_id"] == "" || attrs["cloak_user_id"] != account.CloakUserID {
		t.Fatalf("cloak_user_id = %q, want account fixed id %q", attrs["cloak_user_id"], account.CloakUserID)
	}
	if attrs["cloak_sensitive_words"] != "Cursor,Windsurf" {
		t.Fatalf("cloak_sensitive_words = %q, want Cursor,Windsurf", attrs["cloak_sensitive_words"])
	}
}

func TestClaudeCodeProfileSnapshotPromoteDisabled(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	trace := `{"request":{"headers":{"User-Agent":"claude-cli/9.9.9 (external, sdk-cli)","anthropic-version":"2023-06-01","x-app":"cli","x-stainless-runtime":"node","x-stainless-lang":"js","x-stainless-retry-count":"0","x-stainless-timeout":"600","anthropic-beta":"claude-code-20250219,context-management-2025-06-27"},"body":{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=9.9.9.abc; cc_entrypoint=sdk-cli; cch=00000;"},{"type":"text","text":"You are a Claude agent."}],"tools":[]}}}`
	snapshot, err := BuildClaudeCodeProfileSnapshot("phistory", "9.9.9", `{"version":"9.9.9"}`, trace, "prompt baseline")
	if err != nil {
		t.Fatalf("BuildClaudeCodeProfileSnapshot() error = %v", err)
	}
	saved, err := store.UpsertClaudeCodeProfileSnapshot(ctx, *snapshot)
	if err != nil {
		t.Fatalf("UpsertClaudeCodeProfileSnapshot() error = %v", err)
	}
	if saved.NormalizedProfile == nil || saved.NormalizedProfile.UserAgent != "claude-cli/9.9.9 (external, sdk-cli)" {
		t.Fatalf("normalized profile = %+v, want trace user-agent", saved.NormalizedProfile)
	}
	diff, err := store.RefreshClaudeCodeProfileSnapshotDiff(ctx, saved.ID)
	if err != nil {
		t.Fatalf("RefreshClaudeCodeProfileSnapshotDiff() error = %v", err)
	}
	if diff.WarnCount == 0 {
		t.Fatalf("diff warn count = 0, want drift from current profile")
	}
	before, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() before promote error = %v", err)
	}
	doc, promoted, err := store.PromoteClaudeCodeProfileSnapshot(ctx, saved.ID)
	if err == nil || !strings.Contains(err.Error(), "reference-only") {
		t.Fatalf("PromoteClaudeCodeProfileSnapshot() error = %v, want reference-only error", err)
	}
	if doc != nil {
		t.Fatalf("PromoteClaudeCodeProfileSnapshot() doc = %+v, want nil", doc)
	}
	if promoted == nil || promoted.ID != saved.ID {
		t.Fatalf("PromoteClaudeCodeProfileSnapshot() snapshot = %+v, want saved snapshot", promoted)
	}
	after, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() after promote error = %v", err)
	}
	beforeEffective := EffectiveClaudeCodeProfile(before.Profile)
	afterEffective := EffectiveClaudeCodeProfile(after.Profile)
	if afterEffective.Version != beforeEffective.Version || afterEffective.UserAgent != beforeEffective.UserAgent {
		t.Fatalf("effective profile changed after disabled promote: before=%+v after=%+v", beforeEffective, afterEffective)
	}
	reloaded, err := store.GetClaudeCodeProfileSnapshot(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetClaudeCodeProfileSnapshot() error = %v", err)
	}
	if reloaded.Promoted || reloaded.PromotedAt != nil || reloaded.Status == "promoted" {
		t.Fatalf("snapshot promoted marker = %+v, want unchanged reference snapshot", reloaded)
	}
}

func TestPhistoryManifestJSONFindsClaudeCodeLatest(t *testing.T) {
	page := []byte(`<!doctype html><html><body><script id="manifest" type="application/json">{"agents":[{"id":"claude-code","latest":{"version":"2.1.207"}}]}</script></body></html>`)
	raw, err := phistoryManifestJSON(page)
	if err != nil {
		t.Fatalf("phistoryManifestJSON() error = %v", err)
	}
	if !strings.Contains(string(raw), `"version":"2.1.207"`) {
		t.Fatalf("manifest = %s", raw)
	}
}

func TestAccountPoolConfigV3RemovesLegacyVirtualCache(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	raw := `{"enabled":true,"virtual_cache":{"enabled":true,"hit-rate":0.95},"routing":{"per-account-rpm":6}}`
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_pool_json'`, raw); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = 'account_pool_config_v3'`); err != nil {
		t.Fatalf("delete migration marker: %v", err)
	}
	if err := store.migrateAccountPoolConfigV3(ctx); err != nil {
		t.Fatalf("migrateAccountPoolConfigV3() error = %v", err)
	}
	var migrated string
	if err := store.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = 'claude_code_pool_json'`).Scan(&migrated); err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if strings.Contains(migrated, "virtual_cache") || strings.Contains(migrated, "virtual-cache") {
		t.Fatalf("migrated config still contains virtual cache: %s", migrated)
	}
}

func TestAccountPoolPureUsageV4MakesPureModeAuthoritative(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	raw := `{"enabled":true,"pure_mode":false,"usage":{"clean_input_tokens":true,"system_prompt_overhead_tokens":1909}}`
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_pool_json'`, raw); err != nil {
		t.Fatalf("seed legacy pure usage config: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = 'account_pool_pure_usage_v4'`); err != nil {
		t.Fatalf("delete migration marker: %v", err)
	}
	if err := store.migrateAccountPoolPureUsageV4(ctx); err != nil {
		t.Fatalf("migrateAccountPoolPureUsageV4() error = %v", err)
	}
	var migrated string
	if err := store.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = 'claude_code_pool_json'`).Scan(&migrated); err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if gjson.Get(migrated, "usage.clean_input_tokens").Bool() {
		t.Fatalf("legacy clean input flag did not follow pure mode: %s", migrated)
	}
}

func TestBuildClaudeCodeProfileSnapshotArtifactsSeparatesStaticAndFullPrompt(t *testing.T) {
	snapshot, err := BuildClaudeCodeProfileSnapshotArtifacts(
		"phistory",
		"2.1.207",
		`{"version":"2.1.207"}`,
		"",
		"full dynamic prompt",
		"stable prompt",
		`[{"text":"stable prompt"}]`,
	)
	if err != nil {
		t.Fatalf("BuildClaudeCodeProfileSnapshotArtifacts() error = %v", err)
	}
	if snapshot.StaticPromptLength != len("stable prompt") || snapshot.FullPromptLength != len("full dynamic prompt") {
		t.Fatalf("snapshot lengths = static %d full %d", snapshot.StaticPromptLength, snapshot.FullPromptLength)
	}
	if snapshot.StaticPromptHash == "" || snapshot.FullPromptHash == "" || snapshot.StaticPromptHash == snapshot.FullPromptHash {
		t.Fatalf("snapshot hashes = static %q full %q", snapshot.StaticPromptHash, snapshot.FullPromptHash)
	}
	if snapshot.NormalizedProfile == nil || snapshot.NormalizedProfile.SystemPrompt != "stable prompt" {
		t.Fatalf("normalized profile = %+v, want stable prompt", snapshot.NormalizedProfile)
	}
}

func TestStoreMigratesLegacyBuiltinClaudeCodeProfileWithoutDroppingData(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{ID: "legacy-profile@example.com.json", Provider: "claude", Metadata: map[string]any{
		"type":         "claude",
		"email":        "legacy-profile@example.com",
		"access_token": "token",
	}}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "legacy-profile@example.com", "", auth); err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuth() error = %v", err)
	}
	snapshot, err := BuildClaudeCodeProfileSnapshot("phistory", "2.1.178", `{}`, "", "reference prompt")
	if err != nil {
		t.Fatalf("BuildClaudeCodeProfileSnapshot() error = %v", err)
	}
	if _, err := store.UpsertClaudeCodeProfileSnapshot(ctx, *snapshot); err != nil {
		t.Fatalf("UpsertClaudeCodeProfileSnapshot() error = %v", err)
	}
	legacy := ClaudeCodeProfile{Version: "2.1.178", UpdatedFrom: "builtin-trace-baseline", Locked: true}
	raw, _ := json.Marshal(legacy)
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_profile_json'`, string(raw)); err != nil {
		t.Fatalf("write legacy profile: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = 'claude_code_profile_2_1_207_r3'`); err != nil {
		t.Fatalf("delete migration marker: %v", err)
	}
	if err := store.migrateClaudeCodeProfileRevision(ctx); err != nil {
		t.Fatalf("migrateClaudeCodeProfileRevision() error = %v", err)
	}
	doc, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if doc.Profile.Version != DefaultClaudeCodeProfileVersion || doc.Profile.Revision != DefaultClaudeCodeProfileRevision {
		t.Fatalf("profile = %+v, want %s", doc.Profile, DefaultClaudeCodeProfileRevision)
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts after migration = %d, err=%v", len(accounts), err)
	}
	snapshots, err := store.ListClaudeCodeProfileSnapshots(ctx)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after migration = %d, err=%v", len(snapshots), err)
	}
}

func TestBuiltinClaudeCodeProfileR3MigrationBoundary(t *testing.T) {
	tests := []struct {
		name    string
		profile ClaudeCodeProfile
		want    bool
	}{
		{name: "legacy r1", profile: ClaudeCodeProfile{Revision: "2.1.207-r1", Version: "2.1.207", UpdatedFrom: "builtin-trace-baseline:2.1.207", Locked: true}, want: true},
		{name: "current r3", profile: defaultClaudeCodeProfile(), want: false},
		{name: "external profile", profile: ClaudeCodeProfile{Revision: "2.1.207-r1", Version: "2.1.207", UpdatedFrom: "manual", Locked: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMigrateBuiltinClaudeCodeProfile(tt.profile); got != tt.want {
				t.Fatalf("shouldMigrateBuiltinClaudeCodeProfile() = %v, want %v", got, tt.want)
			}
		})
	}

	profile := EffectiveClaudeCodeProfile(ClaudeCodeProfile{})
	if profile.Revision != "2.1.207-r3" || profile.Headers["X-Stainless-Os"] != "MacOS" || profile.Headers["X-Stainless-Arch"] != "arm64" {
		t.Fatalf("effective profile identity = revision %q platform %q/%q", profile.Revision, profile.Headers["X-Stainless-Os"], profile.Headers["X-Stainless-Arch"])
	}
	if len(profile.HeaderOrder) == 0 || profile.TLSJA3 == "" || profile.TLSJA4 == "" || profile.TLSALPN != "http/1.1" {
		t.Fatalf("effective transport profile = %+v", profile)
	}
	if !isExactBuiltinClaudeCodeProfileR2(builtinClaudeCodeProfileR2()) {
		t.Fatal("exact r2 built-in profile was not recognized")
	}
	customHeader := builtinClaudeCodeProfileR2()
	customHeader.Headers["X-Stainless-Timeout"] = "900"
	if isExactBuiltinClaudeCodeProfileR2(customHeader) {
		t.Fatal("custom r2 Header was treated as the built-in baseline")
	}
	customPrompt := builtinClaudeCodeProfileR2()
	customPrompt.SystemPrompt = "custom prompt"
	if isExactBuiltinClaudeCodeProfileR2(customPrompt) {
		t.Fatal("custom r2 prompt was treated as the built-in baseline")
	}
}

func TestClaudeCodeProfileR3MigrationUpgradesExactR2AndStalesCalibration(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	r2 := builtinClaudeCodeProfileR2()
	raw, _ := json.Marshal(r2)
	oldEffective := EffectiveClaudeCodeProfile(r2)
	oldFingerprint := ClaudeCodeProfileFingerprint(oldEffective)
	oldOverhead := ClaudeCodeProfileInjectedOverheadTokens(oldEffective)
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_profile_json'`, string(raw)); err != nil {
		t.Fatalf("write r2 profile: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_pool_json'`, fmt.Sprintf(`{"pure_mode":true,"usage":{"system_prompt_overhead_tokens":%d}}`, oldOverhead)); err != nil {
		t.Fatalf("write r2 usage config: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = 'claude_code_profile_2_1_207_r3'`); err != nil {
		t.Fatalf("delete r3 marker: %v", err)
	}
	if _, err := store.UpsertUsageCalibration(ctx, UsageCalibration{
		Model:              "claude-opus-4-8",
		ProfileFingerprint: oldFingerprint,
		OverheadTokens:     oldOverhead,
		Status:             UsageCalibrationCalibrated,
	}); err != nil {
		t.Fatalf("seed r2 calibration: %v", err)
	}

	if err := store.migrateClaudeCodeProfileRevision(ctx); err != nil {
		t.Fatalf("migrate exact r2 profile: %v", err)
	}
	doc, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if doc.Profile.Revision != DefaultClaudeCodeProfileRevision {
		t.Fatalf("profile revision = %q, want %q", doc.Profile.Revision, DefaultClaudeCodeProfileRevision)
	}
	if doc.ClaudeCode.Usage.SystemPromptOverheadTokens != DefaultCleanInputOverheadTokens {
		t.Fatalf("overhead = %d, want recomputed %d", doc.ClaudeCode.Usage.SystemPromptOverheadTokens, DefaultCleanInputOverheadTokens)
	}
	calibration, err := store.GetUsageCalibration(ctx, "claude-opus-4-8", oldFingerprint)
	if err != nil || calibration.Status != UsageCalibrationStale {
		t.Fatalf("old calibration = %+v, err=%v, want stale", calibration, err)
	}
	if err := store.migrateClaudeCodeProfileRevision(ctx); err != nil {
		t.Fatalf("repeat r3 migration: %v", err)
	}
}

func TestClaudeCodeProfileR3MigrationPreservesCustomR2(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	custom := builtinClaudeCodeProfileR2()
	custom.Headers["X-Stainless-Timeout"] = "900"
	raw, _ := json.Marshal(custom)
	if _, err := store.db.ExecContext(ctx, `UPDATE pool_config SET value = ? WHERE key = 'claude_code_profile_json'`, string(raw)); err != nil {
		t.Fatalf("write custom r2 profile: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = 'claude_code_profile_2_1_207_r3'`); err != nil {
		t.Fatalf("delete r3 marker: %v", err)
	}
	if err := store.migrateClaudeCodeProfileRevision(ctx); err != nil {
		t.Fatalf("migrate custom r2 profile: %v", err)
	}
	doc, err := store.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if doc.Profile.Revision != "2.1.207-r2" || doc.Profile.Headers["X-Stainless-Timeout"] != "900" {
		t.Fatalf("custom r2 profile was overwritten: %+v", doc.Profile)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	store, err := Open(configPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
