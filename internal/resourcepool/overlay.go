package resourcepool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ApplyAuthOverlay marks Claude OAuth auths that belong to the Claude Code account pool
// and overlays the bound proxy/runtime fields from SQLite.
func ApplyAuthOverlay(ctx context.Context, configPath string, cfg *config.Config, auth *coreauth.Auth) error {
	if auth == nil || cfg == nil || !cfg.ResourcePools.Enabled {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
		return nil
	}
	store, err := Open(configPath, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	email := ""
	if auth.Metadata != nil {
		if raw, ok := auth.Metadata["email"].(string); ok {
			email = raw
		}
	}
	overlay, ok, err := store.FindAccountOverlay(ctx, auth.ID, email)
	if err != nil || !ok || overlay == nil {
		return err
	}
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return err
	}
	effectivePool := EffectiveClaudeCodePool(doc.ClaudeCode)
	effectiveProfile := EffectiveClaudeCodeProfile(doc.Profile)
	usageOverheadsJSON := store.UsageOverheadMapJSON(ctx, effectivePool.Usage.ProfileFingerprint)
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[AttrClaudeOAuthPool] = "true"
	auth.Attributes[AttrAccountID] = overlay.Account.ID
	if overlay.Account.ProxyResourceID != "" {
		auth.Attributes[AttrProxyResourceID] = overlay.Account.ProxyResourceID
	}
	if overlay.Account.Priority != 0 {
		auth.Attributes["priority"] = strconv.Itoa(overlay.Account.Priority)
	}
	if strings.TrimSpace(overlay.Account.Note) != "" {
		auth.Attributes["note"] = strings.TrimSpace(overlay.Account.Note)
	}
	if !overlay.Account.Enabled {
		auth.Disabled = true
		auth.Status = coreauth.StatusDisabled
	}
	if overlay.Proxy != nil && strings.TrimSpace(overlay.Proxy.ProxyURL) != "" {
		auth.ProxyURL = strings.TrimSpace(overlay.Proxy.ProxyURL)
	}
	if capacity, err := store.GetAccountCapacity(ctx, overlay.Account.ID); err == nil {
		overlay.Account.Capacity = capacity
	}
	applyClaudeCodePoolAttributes(auth, effectivePool, effectiveProfile, overlay.Account.CloakUserID)
	applyClaudeCodeUsageOverheadsAttribute(auth, usageOverheadsJSON)
	applyClaudeCodeCapacityAttributes(auth, overlay.Account.Capacity)
	ApplyExcludedModelsOverlay(auth, overlay.Account.ExcludedModels)
	return nil
}

// ListStoredAuths synthesizes runtime Claude OAuth auths from resource-pools.db.
func ListStoredAuths(ctx context.Context, configPath string, cfg *config.Config) ([]*coreauth.Auth, error) {
	out := make([]*coreauth.Auth, 0)
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return out, nil
	}
	store, err := Open(configPath, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = store.Close()
	}()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	effectivePool := EffectiveClaudeCodePool(doc.ClaudeCode)
	effectiveProfile := EffectiveClaudeCodeProfile(doc.Profile)
	usageOverheadsJSON := store.UsageOverheadMapJSON(ctx, effectivePool.Usage.ProfileFingerprint)
	rows, err := store.db.QueryContext(ctx, `
SELECT a.id, a.auth_id, a.cloak_user_id, a.auth_json, a.email, a.enabled, a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.created_at, a.updated_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE TRIM(a.auth_json) <> ''
ORDER BY a.enabled DESC, a.updated_at DESC, a.email ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list stored claude code auths: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		auth, err := scanStoredAuth(rows, cfg, effectivePool, effectiveProfile, usageOverheadsJSON)
		if err != nil {
			return nil, err
		}
		if auth != nil {
			out = append(out, auth)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored claude code auths: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close stored claude code auths rows: %w", err)
	}
	for _, auth := range out {
		if auth == nil || auth.Attributes == nil {
			continue
		}
		if capacity, err := store.GetAccountCapacity(ctx, auth.Attributes[AttrAccountID]); err == nil {
			applyClaudeCodeCapacityAttributes(auth, capacity)
		}
	}
	return out, nil
}

// GetStoredAuth returns the SQLite-backed runtime auth for one Claude Code account.
func GetStoredAuth(ctx context.Context, configPath string, cfg *config.Config, accountID string) (*coreauth.Auth, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return nil, fmt.Errorf("resource pools disabled")
	}
	store, err := Open(configPath, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = store.Close()
	}()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	effectivePool := EffectiveClaudeCodePool(doc.ClaudeCode)
	effectiveProfile := EffectiveClaudeCodeProfile(doc.Profile)
	usageOverheadsJSON := store.UsageOverheadMapJSON(ctx, effectivePool.Usage.ProfileFingerprint)
	rows, err := store.db.QueryContext(ctx, `
SELECT a.id, a.auth_id, a.cloak_user_id, a.auth_json, a.email, a.enabled, a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.created_at, a.updated_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE a.id = ? AND TRIM(a.auth_json) <> ''
`, accountID)
	if err != nil {
		return nil, fmt.Errorf("get stored claude code auth: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate stored claude code auth: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	auth, err := scanStoredAuth(rows, cfg, effectivePool, effectiveProfile, usageOverheadsJSON)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored claude code auth: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close stored claude code auth rows: %w", err)
	}
	if auth != nil && auth.Attributes != nil {
		if capacity, err := store.GetAccountCapacity(ctx, auth.Attributes[AttrAccountID]); err == nil {
			applyClaudeCodeCapacityAttributes(auth, capacity)
		}
	}
	return auth, nil
}

func scanStoredAuth(rows interface {
	Scan(dest ...interface{}) error
}, cfg *config.Config, poolCfg EffectiveClaudeCodePoolConfig, profile EffectiveClaudeCodeProfileConfig, usageOverheadsJSON string) (*coreauth.Auth, error) {
	var accountID, authID, cloakUserID, authJSON, email, proxyResourceID, note, excludedJSON, accountCreatedRaw, accountUpdatedRaw string
	var enabled, priority int
	var proxyID sql.NullString
	var proxyName, proxyURL, proxyExitIP, proxyHealth, proxyLastError, proxyTagsJSON, proxyNote sql.NullString
	var proxyEnabled, proxyLatencyMS, proxyFailures sql.NullInt64
	var proxyLastChecked, proxyCreatedRaw, proxyUpdatedRaw sql.NullString
	if err := rows.Scan(
		&accountID,
		&authID,
		&cloakUserID,
		&authJSON,
		&email,
		&enabled,
		&priority,
		&proxyResourceID,
		&note,
		&excludedJSON,
		&accountCreatedRaw,
		&accountUpdatedRaw,
		&proxyID,
		&proxyName,
		&proxyURL,
		&proxyExitIP,
		&proxyEnabled,
		&proxyHealth,
		&proxyLatencyMS,
		&proxyFailures,
		&proxyLastChecked,
		&proxyLastError,
		&proxyTagsJSON,
		&proxyNote,
		&proxyCreatedRaw,
		&proxyUpdatedRaw,
	); err != nil {
		return nil, fmt.Errorf("scan stored claude code auth: %w", err)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(authJSON), &metadata); err != nil {
		return nil, fmt.Errorf("decode stored claude code auth json: %w", err)
	}
	if strings.TrimSpace(email) != "" {
		metadata["email"] = strings.TrimSpace(email)
	}
	metadata["resource_pool_account"] = true
	metadata["resource_pool_type"] = "claude-code"
	if strings.TrimSpace(proxyResourceID) != "" {
		metadata["proxy_resource_id"] = strings.TrimSpace(proxyResourceID)
	}
	disabled := enabled == 0
	if rawDisabled, ok := metadata["disabled"].(bool); ok && rawDisabled {
		disabled = true
	}
	status := coreauth.StatusActive
	if disabled {
		status = coreauth.StatusDisabled
	}
	label := strings.TrimSpace(email)
	if label == "" {
		if raw, _ := metadata["email"].(string); strings.TrimSpace(raw) != "" {
			label = strings.TrimSpace(raw)
		}
	}
	if label == "" {
		label = "claude-code-account"
	}
	prefix := ""
	if rawPrefix, _ := metadata["prefix"].(string); strings.TrimSpace(rawPrefix) != "" {
		trimmed := strings.Trim(strings.TrimSpace(rawPrefix), "/")
		if !strings.Contains(trimmed, "/") {
			prefix = trimmed
		}
	}
	attrs := map[string]string{
		"source":            "resource-pool:claude-code-account",
		"auth_kind":         "oauth",
		AttrClaudeOAuthPool: "true",
		AttrAccountID:       accountID,
	}
	if strings.TrimSpace(proxyResourceID) != "" {
		attrs[AttrProxyResourceID] = strings.TrimSpace(proxyResourceID)
	}
	if priority != 0 {
		attrs["priority"] = strconv.Itoa(priority)
	}
	if strings.TrimSpace(note) != "" {
		attrs["note"] = strings.TrimSpace(note)
	}
	auth := &coreauth.Auth{
		ID:         strings.TrimSpace(authID),
		Provider:   "claude",
		FileName:   strings.TrimSpace(authID),
		Label:      label,
		Prefix:     prefix,
		Status:     status,
		Disabled:   disabled,
		Attributes: attrs,
		Metadata:   metadata,
		CreatedAt:  parseDBTime(accountCreatedRaw),
		UpdatedAt:  parseDBTime(accountUpdatedRaw),
	}
	if proxyID.Valid && strings.TrimSpace(proxyURL.String) != "" {
		auth.ProxyURL = strings.TrimSpace(proxyURL.String)
	}
	applyClaudeCodePoolAttributes(auth, poolCfg, profile, cloakUserID)
	applyClaudeCodeUsageOverheadsAttribute(auth, usageOverheadsJSON)
	if auth.CreatedAt.IsZero() {
		auth.CreatedAt = time.Now()
	}
	if auth.UpdatedAt.IsZero() {
		auth.UpdatedAt = auth.CreatedAt
	}
	coreauth.ApplyCustomHeadersFromMetadata(auth)
	ApplyExcludedModelsOverlay(auth, decodeStringList(excludedJSON))
	applyOAuthExcludedModels(auth, cfg)
	return auth, nil
}

func applyClaudeCodePoolAttributes(auth *coreauth.Auth, poolCfg EffectiveClaudeCodePoolConfig, profile EffectiveClaudeCodeProfileConfig, cloakUserID string) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[claudeapipool.AttrOAuthPool] = "true"
	if poolCfg.PureMode {
		auth.Attributes[claudeapipool.AttrPureMode] = "true"
	} else {
		delete(auth.Attributes, claudeapipool.AttrPureMode)
	}
	mode := strings.TrimSpace(poolCfg.Cloak.Mode)
	if mode == "" {
		mode = "auto"
	}
	auth.Attributes["cloak_mode"] = mode
	auth.Attributes["cloak_strict_mode"] = strconv.FormatBool(poolCfg.Cloak.StrictMode)
	if trimmed := strings.TrimSpace(cloakUserID); trimmed != "" {
		auth.Attributes["cloak_user_id"] = trimmed
	} else {
		delete(auth.Attributes, "cloak_user_id")
	}
	delete(auth.Attributes, "cloak_cache_user_id")
	if len(poolCfg.Cloak.SensitiveWords) > 0 {
		auth.Attributes["cloak_sensitive_words"] = strings.Join(poolCfg.Cloak.SensitiveWords, ",")
	} else {
		delete(auth.Attributes, "cloak_sensitive_words")
	}
	applyClaudeCodeProfileAttributes(auth, profile)
	applyClaudeCodeUsageAttributes(auth, poolCfg.Usage)
}

func applyClaudeCodeProfileAttributes(auth *coreauth.Auth, profile EffectiveClaudeCodeProfileConfig) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[AttrProfileManaged] = "true"
	auth.Attributes[AttrProfileVersion] = strings.TrimSpace(profile.Version)
	auth.Attributes[AttrProfileUserAgent] = strings.TrimSpace(profile.UserAgent)
	auth.Attributes[AttrProfileSystemPrompt] = strings.TrimSpace(profile.SystemPrompt)
	auth.Attributes[AttrProfileBillingBlockEnabled] = strconv.FormatBool(profile.BillingBlockEnabled)
	auth.Attributes[AttrProfileMetadataUserIDMode] = strings.TrimSpace(profile.MetadataUserIDMode)
	if len(profile.Headers) > 0 {
		if raw, err := json.Marshal(profile.Headers); err == nil {
			auth.Attributes[AttrProfileHeadersJSON] = string(raw)
		}
	} else {
		delete(auth.Attributes, AttrProfileHeadersJSON)
	}
	if len(profile.Betas) > 0 {
		if raw, err := json.Marshal(profile.Betas); err == nil {
			auth.Attributes[AttrProfileBetasJSON] = string(raw)
		}
	} else {
		delete(auth.Attributes, AttrProfileBetasJSON)
	}
}

func applyClaudeCodeUsageAttributes(auth *coreauth.Auth, usage EffectiveClaudeCodeUsageConfig) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[AttrCleanInputTokens] = strconv.FormatBool(usage.CleanInputTokens)
	overhead := usage.SystemPromptOverheadTokens
	if overhead <= 0 {
		overhead = DefaultCleanInputOverheadTokens
	}
	auth.Attributes[AttrCleanInputDefaultOverhead] = strconv.FormatInt(overhead, 10)
	auth.Attributes[AttrProfileFingerprint] = strings.TrimSpace(usage.ProfileFingerprint)
}

func applyClaudeCodeUsageOverheadsAttribute(auth *coreauth.Auth, raw string) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		delete(auth.Attributes, AttrUsageOverheadsJSON)
		return
	}
	auth.Attributes[AttrUsageOverheadsJSON] = raw
}

func applyClaudeCodeCapacityAttributes(auth *coreauth.Auth, capacity *AccountCapacityConfig) {
	if auth == nil || capacity == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[AttrCapacityBaseRPM] = strconv.Itoa(capacity.BaseRPM)
	auth.Attributes[AttrCapacityConcurrencyLimit] = strconv.Itoa(capacity.ConcurrencyLimit)
	auth.Attributes[AttrCapacityMaxSessions] = strconv.Itoa(capacity.MaxSessions)
	auth.Attributes[AttrCapacityStickyBuffer] = strconv.Itoa(capacity.StickyBuffer)
}

func applyOAuthExcludedModels(auth *coreauth.Auth, cfg *config.Config) {
	if auth == nil || cfg == nil || len(cfg.OAuthExcludedModels) == 0 {
		return
	}
	providerKey := strings.ToLower(strings.TrimSpace(auth.Provider))
	ApplyExcludedModelsOverlay(auth, cfg.OAuthExcludedModels[providerKey])
}

// ApplyExcludedModelsOverlay appends account-level excluded models to auth metadata.
func ApplyExcludedModelsOverlay(auth *coreauth.Auth, excluded []string) {
	if auth == nil || len(excluded) == 0 {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	existing := map[string]struct{}{}
	var merged []string
	if raw, ok := auth.Metadata["excluded_models"]; ok {
		switch values := raw.(type) {
		case []string:
			for _, value := range values {
				trimmed := strings.TrimSpace(value)
				if trimmed == "" {
					continue
				}
				key := strings.ToLower(trimmed)
				if _, seen := existing[key]; seen {
					continue
				}
				existing[key] = struct{}{}
				merged = append(merged, trimmed)
			}
		case []interface{}:
			for _, value := range values {
				text, _ := value.(string)
				trimmed := strings.TrimSpace(text)
				if trimmed == "" {
					continue
				}
				key := strings.ToLower(trimmed)
				if _, seen := existing[key]; seen {
					continue
				}
				existing[key] = struct{}{}
				merged = append(merged, trimmed)
			}
		}
	}
	for _, value := range excluded {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, seen := existing[key]; seen {
			continue
		}
		existing[key] = struct{}{}
		merged = append(merged, trimmed)
	}
	if len(merged) > 0 {
		auth.Metadata["excluded_models"] = merged
	}
}
