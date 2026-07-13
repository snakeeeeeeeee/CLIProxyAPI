package resourcepool

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestAccountPoolConfigInheritanceAndNullReset(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	globalPure := false
	if _, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		PureMode: &globalPure,
		Routing: claudeapipool.RoutingConfig{
			PerAccountRPM:            12,
			PerAccountConcurrency:    3,
			StickyConcurrencyReserve: 2,
			MaxSessions:              40,
		},
	}); err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	pool, err := store.CreateAccountPool(ctx, "Inherited", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}

	view, err := store.GetAccountPoolConfig(ctx, pool.ID)
	if err != nil {
		t.Fatalf("GetAccountPoolConfig() error = %v", err)
	}
	if view.Effective.PureMode || view.Effective.Routing.PerAccountRPM != 12 || view.Sources["routing.per_account_rpm"] != "global" {
		t.Fatalf("initial inherited view = %+v", view)
	}

	view, err = store.PatchAccountPoolConfig(ctx, pool.ID, json.RawMessage(`{
		"pure_mode": true,
		"routing": {
			"per_account_rpm": 7,
			"max_sessions": 55
		}
	}`))
	if err != nil {
		t.Fatalf("PatchAccountPoolConfig() error = %v", err)
	}
	if !view.Effective.PureMode || view.Effective.Routing.PerAccountRPM != 7 || view.Effective.Routing.PerAccountConcurrency != 3 || view.Effective.Routing.MaxSessions != 55 {
		t.Fatalf("overridden view = %+v", view)
	}
	if view.Sources["pure_mode"] != "pool" || view.Sources["routing.per_account_rpm"] != "pool" || view.Sources["routing.per_account_concurrency"] != "global" {
		t.Fatalf("override sources = %+v", view.Sources)
	}
	persisted, err := store.GetAccountPool(ctx, pool.ID)
	if err != nil {
		t.Fatalf("GetAccountPool() error = %v", err)
	}
	if !persisted.HasConfigOverride || persisted.ConfigOverrideCount != 3 {
		t.Fatalf("pool override summary = %+v", persisted)
	}

	if _, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		PureMode: &globalPure,
		Routing: claudeapipool.RoutingConfig{
			PerAccountRPM:            20,
			PerAccountConcurrency:    4,
			StickyConcurrencyReserve: 3,
			MaxSessions:              60,
		},
	}); err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig(updated) error = %v", err)
	}
	view, err = store.GetAccountPoolConfig(ctx, pool.ID)
	if err != nil {
		t.Fatalf("GetAccountPoolConfig(updated) error = %v", err)
	}
	if view.Effective.Routing.PerAccountRPM != 7 || view.Effective.Routing.PerAccountConcurrency != 4 || view.Effective.Routing.MaxSessions != 55 {
		t.Fatalf("effective config after global update = %+v", view.Effective.Routing)
	}

	view, err = store.PatchAccountPoolConfig(ctx, pool.ID, json.RawMessage(`{
		"pure_mode": null,
		"routing": {
			"per_account_rpm": null
		}
	}`))
	if err != nil {
		t.Fatalf("PatchAccountPoolConfig(reset fields) error = %v", err)
	}
	if view.Effective.PureMode || view.Effective.Routing.PerAccountRPM != 20 || view.Effective.Routing.MaxSessions != 55 {
		t.Fatalf("field reset view = %+v", view)
	}
	if view.Sources["pure_mode"] != "global" || view.Sources["routing.per_account_rpm"] != "global" || view.Sources["routing.max_sessions"] != "pool" {
		t.Fatalf("field reset sources = %+v", view.Sources)
	}

	view, err = store.PatchAccountPoolConfig(ctx, pool.ID, json.RawMessage(`{"routing": null}`))
	if err != nil {
		t.Fatalf("PatchAccountPoolConfig(reset routing) error = %v", err)
	}
	if view.Effective.Routing.MaxSessions != 60 || view.Overrides.Routing.MaxSessions != nil || view.Overrides.Routing.PerAccountRPM != nil {
		t.Fatalf("routing reset view = %+v", view)
	}
}

func TestAccountPoolConfigProjectsPureModeAndAccountCapacityPrecedence(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	globalPure := false
	if _, err := store.SaveClaudeCodePoolConfig(ctx, ClaudeCodePoolConfig{
		PureMode: &globalPure,
		Routing: claudeapipool.RoutingConfig{
			PerAccountRPM:         12,
			PerAccountConcurrency: 2,
			MaxSessions:           40,
		},
	}); err != nil {
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	pool, err := store.CreateAccountPool(ctx, "Scoped", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	if _, err := store.PatchAccountPoolConfig(ctx, pool.ID, json.RawMessage(`{
		"pure_mode": true,
		"routing": {
			"per_account_rpm": 8,
			"per_account_concurrency": 3,
			"max_sessions": 24
		}
	}`)); err != nil {
		t.Fatalf("PatchAccountPoolConfig() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "scoped@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{"type": "claude", "email": "scoped@example.com", "access_token": "test-token"},
	}
	account, err := store.RegisterClaudeCodeAccountWithAuthInPool(ctx, pool.ID, auth.ID, "scoped@example.com", "", auth)
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuthInPool() error = %v", err)
	}
	capacity, err := store.GetAccountCapacity(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccountCapacity() error = %v", err)
	}
	if capacity.BaseRPM != 8 || capacity.ConcurrencyLimit != 3 || capacity.MaxSessions != 24 {
		t.Fatalf("inherited account capacity = %+v", capacity)
	}

	capacity, err = store.SaveAccountCapacity(ctx, account.ID, AccountCapacityConfig{
		BaseRPM:                  5,
		ConcurrencyLimit:         4,
		MaxSessions:              18,
		StickyConcurrencyReserve: 2,
	})
	if err != nil {
		t.Fatalf("SaveAccountCapacity() error = %v", err)
	}
	if _, err := store.PatchAccountPoolConfig(ctx, pool.ID, json.RawMessage(`{
		"routing": {
			"per_account_rpm": 9,
			"per_account_concurrency": 6,
			"max_sessions": 30
		}
	}`)); err != nil {
		t.Fatalf("PatchAccountPoolConfig(updated) error = %v", err)
	}
	capacity, err = store.GetAccountCapacity(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccountCapacity(updated) error = %v", err)
	}
	if capacity.BaseRPM != 5 || capacity.ConcurrencyLimit != 4 || capacity.MaxSessions != 18 {
		t.Fatalf("account override lost after pool update: %+v", capacity)
	}

	runtimeAuth, err := GetStoredAuth(ctx, store.initPath, &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}, account.ID)
	if err != nil {
		t.Fatalf("GetStoredAuth() error = %v", err)
	}
	if runtimeAuth.Attributes[claudeapipool.AttrPureMode] != "true" || runtimeAuth.Attributes[AttrCleanInputTokens] != "true" {
		t.Fatalf("pool pure-mode attributes = %+v", runtimeAuth.Attributes)
	}
	if runtimeAuth.Attributes[AttrCapacityBaseRPM] != strconv.Itoa(capacity.BaseRPM) || runtimeAuth.Attributes[AttrCapacityConcurrencyLimit] != strconv.Itoa(capacity.ConcurrencyLimit) {
		t.Fatalf("account capacity attributes = %+v", runtimeAuth.Attributes)
	}
}

func TestAccountPoolConfigInheritanceMigrationIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `UPDATE claude_code_pools SET config_json = 'invalid' WHERE id = ?`, DefaultAccountPoolID); err != nil {
		t.Fatalf("corrupt config_json: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = ?`, accountPoolConfigInheritanceMigrationMarker); err != nil {
		t.Fatalf("delete migration marker: %v", err)
	}
	if err := store.migrateAccountPoolConfigInheritanceV9(ctx); err != nil {
		t.Fatalf("migrateAccountPoolConfigInheritanceV9() error = %v", err)
	}
	if err := store.migrateAccountPoolConfigInheritanceV9(ctx); err != nil {
		t.Fatalf("migrateAccountPoolConfigInheritanceV9(second) error = %v", err)
	}
	var configJSON string
	if err := store.db.QueryRowContext(ctx, `SELECT config_json FROM claude_code_pools WHERE id = ?`, DefaultAccountPoolID).Scan(&configJSON); err != nil {
		t.Fatalf("read migrated config_json: %v", err)
	}
	if configJSON != "{}" {
		t.Fatalf("migrated config_json = %q, want {}", configJSON)
	}
}
