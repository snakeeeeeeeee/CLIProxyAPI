package resourcepool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const accountPoolConfigInheritanceMigrationMarker = "account_pool_config_inheritance_v9"

func (s *Store) migrateAccountPoolConfigInheritanceV9(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, accountPoolConfigInheritanceMigrationMarker).Scan(&value); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read account-pool config inheritance migration marker: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account-pool config inheritance migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `
UPDATE claude_code_pools
SET config_json = '{}'
WHERE TRIM(config_json) = ''
   OR json_valid(config_json) = 0
   OR json_type(CASE WHEN json_valid(config_json) THEN config_json ELSE '{}' END) <> 'object'
	`); err != nil {
		return fmt.Errorf("normalize account-pool config json: %w", err)
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pool_config(key, value, created_at, updated_at)
VALUES(?, '1', ?, ?)
ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at
	`, accountPoolConfigInheritanceMigrationMarker, now, now); err != nil {
		return fmt.Errorf("write account-pool config inheritance migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account-pool config inheritance migration: %w", err)
	}
	return nil
}

// GetAccountPoolConfig returns sparse overrides, effective values, and inheritance sources.
func (s *Store) GetAccountPoolConfig(ctx context.Context, poolID string) (*ClaudeCodeAccountPoolConfigView, error) {
	pool, err := s.GetAccountPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	overrides, err := decodeAccountPoolConfigOverrides(pool.configJSON)
	if err != nil {
		return nil, fmt.Errorf("decode account pool %s config: %w", pool.ID, err)
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	globalFull := EffectiveClaudeCodePool(doc.ClaudeCode)
	global := accountPoolEffectiveSubset(globalFull)
	effective := accountPoolEffectiveSubset(effectiveClaudeCodePoolWithOverrides(globalFull, overrides))
	return &ClaudeCodeAccountPoolConfigView{
		Overrides: overrides,
		Effective: effective,
		Global:    global,
		Sources:   accountPoolConfigSources(overrides, global),
	}, nil
}

// PatchAccountPoolConfig applies JSON merge-patch semantics to one pool's sparse overrides.
func (s *Store) PatchAccountPoolConfig(ctx context.Context, poolID string, patch json.RawMessage) (*ClaudeCodeAccountPoolConfigView, error) {
	pool, err := s.GetAccountPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	if pool.ArchivedAt != nil {
		return nil, ErrAccountPoolArchived
	}
	current, err := decodeAccountPoolConfigOverrides(pool.configJSON)
	if err != nil {
		return nil, fmt.Errorf("decode account pool %s config: %w", pool.ID, err)
	}
	next, err := mergeAccountPoolConfigPatch(current, patch)
	if err != nil {
		return nil, err
	}
	if err := validateAccountPoolConfigOverrides(next); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("encode account-pool config: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin patch account-pool config: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE claude_code_pools SET config_json = ?, updated_at = ? WHERE id = ?`, string(raw), dbTime(time.Now()), pool.ID); err != nil {
		return nil, fmt.Errorf("patch account-pool config: %w", err)
	}
	if err := insertEventTx(ctx, tx, "account_pool.config", "account pool config updated", map[string]string{"pool_id": pool.ID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit patch account-pool config: %w", err)
	}
	return s.GetAccountPoolConfig(ctx, pool.ID)
}

// EffectiveClaudeCodePoolForPool resolves global defaults and one pool's sparse overrides.
func (s *Store) EffectiveClaudeCodePoolForPool(ctx context.Context, poolID string) (EffectiveClaudeCodePoolConfig, error) {
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return EffectiveClaudeCodePoolConfig{}, err
	}
	global := EffectiveClaudeCodePool(doc.ClaudeCode)
	pool, err := s.GetAccountPool(ctx, poolID)
	if err != nil {
		return EffectiveClaudeCodePoolConfig{}, err
	}
	overrides, err := decodeAccountPoolConfigOverrides(pool.configJSON)
	if err != nil {
		return EffectiveClaudeCodePoolConfig{}, err
	}
	return effectiveClaudeCodePoolWithOverrides(global, overrides), nil
}

func (s *Store) effectiveClaudeCodePools(ctx context.Context, global EffectiveClaudeCodePoolConfig) (map[string]EffectiveClaudeCodePoolConfig, error) {
	pools, err := s.ListAccountPools(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]EffectiveClaudeCodePoolConfig, len(pools))
	for _, pool := range pools {
		overrides, err := decodeAccountPoolConfigOverrides(pool.configJSON)
		if err != nil {
			return nil, fmt.Errorf("decode account pool %s config: %w", pool.ID, err)
		}
		out[pool.ID] = effectiveClaudeCodePoolWithOverrides(global, overrides)
	}
	return out, nil
}

func effectiveClaudeCodePoolWithOverrides(global EffectiveClaudeCodePoolConfig, overrides ClaudeCodeAccountPoolConfigOverrides) EffectiveClaudeCodePoolConfig {
	effective := global
	if overrides.PureMode != nil {
		effective.PureMode = *overrides.PureMode
		effective.Usage.CleanInputTokens = *overrides.PureMode
	}
	effective.Routing = applyAccountPoolRoutingOverrides(global.Routing, overrides.Routing)
	return effective
}

func accountPoolEffectiveSubset(full EffectiveClaudeCodePoolConfig) EffectiveClaudeCodeAccountPoolConfig {
	return EffectiveClaudeCodeAccountPoolConfig{PureMode: full.PureMode, Routing: full.Routing}
}

func applyAccountPoolRoutingOverrides(base claudeapipool.EffectiveRoutingConfig, overrides ClaudeCodeAccountPoolRoutingOverrides) claudeapipool.EffectiveRoutingConfig {
	rawBase, _ := json.Marshal(base)
	values := make(map[string]json.RawMessage)
	_ = json.Unmarshal(rawBase, &values)
	rawOverrides, _ := json.Marshal(overrides)
	var overrideValues map[string]json.RawMessage
	_ = json.Unmarshal(rawOverrides, &overrideValues)
	for key, value := range overrideValues {
		values[key] = value
	}
	rawEffective, _ := json.Marshal(values)
	effective := base
	_ = json.Unmarshal(rawEffective, &effective)
	return effective
}

func decodeAccountPoolConfigOverrides(raw string) (ClaudeCodeAccountPoolConfigOverrides, error) {
	var overrides ClaudeCodeAccountPoolConfigOverrides
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return overrides, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&overrides); err != nil {
		return overrides, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return overrides, fmt.Errorf("unexpected trailing config data")
	}
	return overrides, validateAccountPoolConfigOverrides(overrides)
}

func mergeAccountPoolConfigPatch(current ClaudeCodeAccountPoolConfigOverrides, patch json.RawMessage) (ClaudeCodeAccountPoolConfigOverrides, error) {
	var patchValues map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(patch))
	if err := decoder.Decode(&patchValues); err != nil {
		return current, fmt.Errorf("invalid account-pool config patch: %w", err)
	}
	if patchValues == nil {
		return current, fmt.Errorf("account-pool config patch must be an object")
	}
	for key := range patchValues {
		if key != "pure_mode" && key != "routing" {
			return current, fmt.Errorf("unsupported account-pool config field %q", key)
		}
	}
	currentRaw, _ := json.Marshal(current)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(currentRaw, &merged)
	for key, value := range patchValues {
		if isJSONNull(value) {
			delete(merged, key)
			continue
		}
		if key != "routing" {
			merged[key] = value
			continue
		}
		var routingPatch map[string]json.RawMessage
		if err := json.Unmarshal(value, &routingPatch); err != nil || routingPatch == nil {
			return current, fmt.Errorf("routing patch must be an object or null")
		}
		var routingCurrent map[string]json.RawMessage
		if existing, ok := merged["routing"]; ok {
			_ = json.Unmarshal(existing, &routingCurrent)
		}
		if routingCurrent == nil {
			routingCurrent = make(map[string]json.RawMessage)
		}
		for routingKey, routingValue := range routingPatch {
			if isJSONNull(routingValue) {
				delete(routingCurrent, routingKey)
			} else {
				routingCurrent[routingKey] = routingValue
			}
		}
		if len(routingCurrent) == 0 {
			delete(merged, "routing")
		} else {
			merged["routing"], _ = json.Marshal(routingCurrent)
		}
	}
	rawMerged, _ := json.Marshal(merged)
	return decodeAccountPoolConfigOverrides(string(rawMerged))
}

func validateAccountPoolConfigOverrides(overrides ClaudeCodeAccountPoolConfigOverrides) error {
	routingValue := reflect.ValueOf(overrides.Routing)
	routingType := routingValue.Type()
	for index := 0; index < routingValue.NumField(); index++ {
		field := routingValue.Field(index)
		if field.Kind() != reflect.Pointer || field.IsNil() || field.Elem().Kind() != reflect.Int {
			continue
		}
		if field.Elem().Int() < 0 {
			return fmt.Errorf("routing field %s cannot be negative", routingType.Field(index).Tag.Get("json"))
		}
	}
	if value := overrides.Routing.PerAccountRPM; value != nil && *value > 1000 {
		return fmt.Errorf("per_account_rpm cannot exceed 1000")
	}
	if value := overrides.Routing.PerAccountConcurrency; value != nil && *value > 100 {
		return fmt.Errorf("per_account_concurrency cannot exceed 100")
	}
	if value := overrides.Routing.MaxSessions; value != nil && *value > 1000 {
		return fmt.Errorf("max_sessions cannot exceed 1000")
	}
	if value := overrides.Routing.StickyConcurrencyReserve; value != nil && *value > 100 {
		return fmt.Errorf("sticky_concurrency_reserve cannot exceed 100")
	}
	if value := overrides.Routing.CacheAffinityAutoProfile; value != nil {
		switch strings.TrimSpace(*value) {
		case claudeapipool.AffinityAutoProfileCost, claudeapipool.AffinityAutoProfileBalanced, claudeapipool.AffinityAutoProfileThroughput:
		default:
			return fmt.Errorf("invalid cache_affinity_auto_profile")
		}
	}
	if value := overrides.Routing.AccountCapacityProfile; value != nil {
		switch strings.TrimSpace(*value) {
		case claudeapipool.AccountCapacityProfileConservative, claudeapipool.AccountCapacityProfileStandard, claudeapipool.AccountCapacityProfileAggressive, claudeapipool.AccountCapacityProfileCustom:
		default:
			return fmt.Errorf("invalid account_capacity_profile")
		}
	}
	return nil
}

func countAccountPoolConfigOverrides(overrides ClaudeCodeAccountPoolConfigOverrides) int {
	count := 0
	if overrides.PureMode != nil {
		count++
	}
	raw, _ := json.Marshal(overrides.Routing)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	return count + len(values)
}

func accountPoolConfigSources(overrides ClaudeCodeAccountPoolConfigOverrides, global EffectiveClaudeCodeAccountPoolConfig) map[string]string {
	sources := map[string]string{"pure_mode": "global"}
	globalRaw, _ := json.Marshal(global.Routing)
	var globalValues map[string]json.RawMessage
	_ = json.Unmarshal(globalRaw, &globalValues)
	for key := range globalValues {
		sources["routing."+key] = "global"
	}
	if overrides.PureMode != nil {
		sources["pure_mode"] = "pool"
	}
	overridesRaw, _ := json.Marshal(overrides.Routing)
	var overrideValues map[string]json.RawMessage
	_ = json.Unmarshal(overridesRaw, &overrideValues)
	for key := range overrideValues {
		sources["routing."+key] = "pool"
	}
	return sources
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

// ApplyClaudeCodePoolRuntimeConfig loads global and per-pool routing policies for future requests.
func ApplyClaudeCodePoolRuntimeConfig(ctx context.Context, configFilePath string, cfg *config.Config) error {
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return nil
	}
	store, err := Open(configFilePath, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return err
	}
	global := EffectiveClaudeCodePool(doc.ClaudeCode)
	claudeapipool.SetScopedRoutingConfig(coreexecutor.PoolScopeClaudeAccountPool, global.Routing)
	pools, err := store.ListAccountPools(ctx, true)
	if err != nil {
		return err
	}
	for _, pool := range pools {
		scope := AccountRoutingScope(pool.ID)
		if pool.ArchivedAt != nil {
			claudeapipool.RemoveScopedRoutingScope(scope)
			continue
		}
		overrides, err := decodeAccountPoolConfigOverrides(pool.configJSON)
		if err != nil {
			return fmt.Errorf("decode account pool %s config: %w", pool.ID, err)
		}
		effective := effectiveClaudeCodePoolWithOverrides(global, overrides)
		claudeapipool.SetScopedRoutingConfig(scope, effective.Routing)
	}
	return nil
}
