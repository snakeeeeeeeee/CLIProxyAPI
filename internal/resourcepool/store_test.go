package resourcepool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
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
	doc, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		Enabled:  &enabled,
		PureMode: &pure,
		Cloak: &config.CloakConfig{
			Mode:           "always",
			StrictMode:     true,
			SensitiveWords: []string{"Claude", "Anthropic", "claude"},
		},
		Usage: ClaudeCodeUsageConfig{
			CleanInputTokens:           &clean,
			SystemPromptOverheadTokens: 1909,
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
	if effective.Cloak.Mode != "always" || !effective.Cloak.StrictMode {
		t.Fatalf("effective cloak = %+v, want always strict", effective.Cloak)
	}
	if strings.Join(effective.Cloak.SensitiveWords, ",") != "Claude,Anthropic" {
		t.Fatalf("effective cloak sensitive words = %+v, want deduped words", effective.Cloak.SensitiveWords)
	}
	if !effective.Usage.CleanInputTokens || effective.Usage.SystemPromptOverheadTokens != DefaultCleanInputOverheadTokens {
		t.Fatalf("effective usage = %+v, want clean enabled with default overhead", effective.Usage)
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

func TestUsagePluginRecordsCleanInputTokens(t *testing.T) {
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
	if _, err := store.UpsertUsageCalibration(ctx, UsageCalibration{
		Model:              "claude-opus-4-8",
		ProfileFingerprint: fingerprint,
		OverheadTokens:     1909,
		Status:             UsageCalibrationCalibrated,
	}); err != nil {
		t.Fatalf("UpsertUsageCalibration() error = %v", err)
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
	ctx = WithCleanInputFloor(ctx, 3)
	plugin.HandleUsage(ctx, coreusage.Record{
		Provider: "claude",
		Model:    "claude-opus-4-8",
		Alias:    "claude-opus-4-8",
		AuthID:   auth.ID,
		APIKey:   "sk-test-abcdef123456",
		Detail: coreusage.Detail{
			InputTokens:  1910,
			OutputTokens: 5,
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
	if summary.InputTokens != 3 || summary.OutputTokens != 5 {
		t.Fatalf("usage tokens = input %d output %d, want 3/5", summary.InputTokens, summary.OutputTokens)
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
	if attrs[claudeapipool.AttrPureMode] != "" {
		t.Fatalf("pure mode attr = %q, want empty when disabled", attrs[claudeapipool.AttrPureMode])
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
