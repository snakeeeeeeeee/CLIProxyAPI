package resourcepool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"gopkg.in/yaml.v3"
)

// ResolveConfigPath resolves resource-pools.yaml relative to the main config file.
func ResolveConfigPath(configFilePath string, cfg *config.Config) string {
	path := ""
	if cfg != nil {
		path = strings.TrimSpace(cfg.ResourcePools.ConfigFile)
	}
	if path == "" {
		path = DefaultConfigFileName
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	baseDir := "."
	if strings.TrimSpace(configFilePath) != "" {
		baseDir = filepath.Dir(configFilePath)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

// ResolveDBPath resolves the SQLite store path from the initializer file.
func ResolveDBPath(configFilePath string, cfg *config.Config) (string, error) {
	initPath := ResolveConfigPath(configFilePath, cfg)
	initDoc, err := LoadConfigFile(initPath)
	if err != nil {
		return "", err
	}
	dbPath := strings.TrimSpace(initDoc.DatabasePath)
	if dbPath == "" {
		dbPath = DefaultDBFileName
	}
	if filepath.IsAbs(dbPath) {
		return filepath.Clean(dbPath), nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(initPath), dbPath)), nil
}

// LoadConfigFile reads resource-pools.yaml. A missing file returns defaults.
func LoadConfigFile(path string) (*ConfigFile, error) {
	doc := defaultConfigFile()
	path = strings.TrimSpace(path)
	if path == "" {
		return doc, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, nil
		}
		return nil, fmt.Errorf("read resource pools config: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return doc, nil
	}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("parse resource pools config: %w", err)
	}
	normalizeConfigFile(doc)
	return doc, nil
}

func defaultConfigFile() *ConfigFile {
	proxyHealthEnabled := true
	accountQuotaEnabled := true
	traceEnabled := false
	traceRedactUserContent := true
	claudeCodeEnabled := true
	pureMode := true
	cleanInputTokens := false
	virtualCacheEnabled := false
	hitRate := 0.95
	targetReuse := 0.90
	shrinkReset := 0.70
	routing := defaultClaudeCodeRoutingConfig()
	return &ConfigFile{
		DatabasePath: DefaultDBFileName,
		ProxyHealth: ProxyHealthConfig{
			Enabled:           &proxyHealthEnabled,
			Interval:          "5m",
			Timeout:           "10s",
			Concurrency:       8,
			FailureThreshold:  3,
			TestURL:           "https://api.anthropic.com/",
			OptionalExitIPURL: "https://api.ipify.org?format=json",
		},
		AccountQuota: AccountQuotaConfig{
			Enabled:     &accountQuotaEnabled,
			Interval:    "5m",
			Concurrency: 2,
		},
		Trace: TraceConfig{
			Enabled:           &traceEnabled,
			DumpDir:           "traces/ours",
			RedactUserContent: &traceRedactUserContent,
		},
		Profile: defaultClaudeCodeProfile(),
		ClaudeCode: ClaudeCodePoolConfig{
			Enabled:  &claudeCodeEnabled,
			PureMode: &pureMode,
			Cloak: &config.CloakConfig{
				Mode:       "auto",
				StrictMode: false,
			},
			Usage: ClaudeCodeUsageConfig{
				CleanInputTokens:           &cleanInputTokens,
				SystemPromptOverheadTokens: DefaultCleanInputOverheadTokens,
			},
			Log: AccountPoolLogConfig{
				Enabled:    boolPtr(true),
				Level:      "info",
				Dir:        "acc-pool-logs",
				MaxSizeMB:  50,
				MaxBackups: 3,
				Redact:     boolPtr(true),
			},
			VirtualCache: claudeapipool.VirtualCacheConfig{
				Enabled:                 &virtualCacheEnabled,
				Mode:                    claudeapipool.VirtualCacheModeNatural,
				HitRate:                 &hitRate,
				TargetCacheReuseRatio:   &targetReuse,
				ContextShrinkResetRatio: &shrinkReset,
			},
			Routing:                 routing,
			PerAccountRPM:           6,
			PerAccountConcurrency:   1,
			MaxSwitches:             2,
			SwitchDelayMS:           1000,
			RateLimitCooldownMS:     int((5 * time.Minute) / time.Millisecond),
			RateLimitMaxCooldownMS:  int((2 * time.Hour) / time.Millisecond),
			OverloadCooldownMS:      int((2 * time.Minute) / time.Millisecond),
			OverloadMaxCooldownMS:   int((30 * time.Minute) / time.Millisecond),
			SameAccountRetry429:     0,
			SameAccountRetry529:     1,
			SameAccountRetryDelayMS: 3000,
			SessionAffinity:         true,
			SessionAffinityTTL:      "1h",
		},
		PoolConfig: map[string]interface{}{},
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func defaultClaudeCodeRoutingConfig() claudeapipool.RoutingConfig {
	return claudeapipool.NormalizeRoutingConfig(claudeapipool.RoutingConfig{
		PerAccountRPM:            6,
		PerAccountConcurrency:    1,
		MaxSwitches:              2,
		SwitchDelayMS:            1000,
		RateLimitCooldownMS:      int((5 * time.Minute) / time.Millisecond),
		RateLimitMaxCooldownMS:   int((2 * time.Hour) / time.Millisecond),
		OverloadCooldownMS:       int((2 * time.Minute) / time.Millisecond),
		OverloadMaxCooldownMS:    int((30 * time.Minute) / time.Millisecond),
		SameAccountRetry429:      0,
		SameAccountRetry529:      1,
		SameAccountRetryDelayMS:  3000,
		CacheAffinityEnabled:     true,
		CacheAffinityAuto:        true,
		CacheAffinityAutoProfile: claudeapipool.AffinityAutoProfileCost,
		AccountCapacityProfile:   claudeapipool.AccountCapacityProfileCustom,
		CacheAffinityMinTokens:   4096,
		CacheAffinityLanes:       1,
		CacheAffinityMaxLanes:    2,
		CacheAffinityWaitMS:      250,
		CacheAffinityTTLMS:       int((5 * time.Minute) / time.Millisecond),
	})
}

func defaultClaudeCodeProfile() ClaudeCodeProfile {
	billingBlockEnabled := true
	return ClaudeCodeProfile{
		Version:   DefaultClaudeCodeProfileVersion,
		UserAgent: "claude-cli/" + DefaultClaudeCodeProfileVersion + " (external, sdk-cli)",
		Headers: map[string]string{
			"Anthropic-Version":       "2023-06-01",
			"X-App":                   "cli",
			"X-Stainless-Retry-Count": "0",
			"X-Stainless-Runtime":     "node",
			"X-Stainless-Lang":        "js",
			"X-Stainless-Timeout":     "600",
		},
		Betas: []string{
			"claude-code-20250219",
			"context-1m-2025-08-07",
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
			"mid-conversation-system-2026-04-07",
			"advisor-tool-2026-03-01",
			"effort-2025-11-24",
		},
		SystemPrompt:        helps.ClaudeCodeStaticPrompt(),
		BillingBlockEnabled: &billingBlockEnabled,
		MetadataUserIDMode:  "account",
		UpdatedFrom:         "builtin-trace-baseline",
		Locked:              true,
		SystemPromptMode:    "builtin_full_claude_code",
	}
}

func normalizeConfigFile(doc *ConfigFile) {
	if doc == nil {
		return
	}
	doc.DatabasePath = strings.TrimSpace(doc.DatabasePath)
	if doc.DatabasePath == "" {
		doc.DatabasePath = DefaultDBFileName
	}
	defaults := defaultConfigFile()
	if doc.ProxyHealth.Enabled == nil {
		doc.ProxyHealth.Enabled = defaults.ProxyHealth.Enabled
	}
	doc.ProxyHealth.Interval = strings.TrimSpace(doc.ProxyHealth.Interval)
	if doc.ProxyHealth.Interval == "" {
		doc.ProxyHealth.Interval = defaults.ProxyHealth.Interval
	}
	doc.ProxyHealth.Timeout = strings.TrimSpace(doc.ProxyHealth.Timeout)
	if doc.ProxyHealth.Timeout == "" {
		doc.ProxyHealth.Timeout = defaults.ProxyHealth.Timeout
	}
	if doc.ProxyHealth.Concurrency <= 0 {
		doc.ProxyHealth.Concurrency = defaults.ProxyHealth.Concurrency
	}
	if doc.ProxyHealth.FailureThreshold <= 0 {
		doc.ProxyHealth.FailureThreshold = defaults.ProxyHealth.FailureThreshold
	}
	doc.ProxyHealth.TestURL = strings.TrimSpace(doc.ProxyHealth.TestURL)
	if doc.ProxyHealth.TestURL == "" {
		doc.ProxyHealth.TestURL = defaults.ProxyHealth.TestURL
	}
	doc.ProxyHealth.OptionalExitIPURL = strings.TrimSpace(doc.ProxyHealth.OptionalExitIPURL)
	if doc.AccountQuota.Enabled == nil {
		doc.AccountQuota.Enabled = defaults.AccountQuota.Enabled
	}
	doc.AccountQuota.Interval = strings.TrimSpace(doc.AccountQuota.Interval)
	if doc.AccountQuota.Interval == "" {
		doc.AccountQuota.Interval = defaults.AccountQuota.Interval
	}
	if _, err := time.ParseDuration(doc.AccountQuota.Interval); err != nil {
		doc.AccountQuota.Interval = defaults.AccountQuota.Interval
	}
	if doc.AccountQuota.Concurrency <= 0 {
		doc.AccountQuota.Concurrency = defaults.AccountQuota.Concurrency
	}
	if doc.Trace.Enabled == nil {
		doc.Trace.Enabled = defaults.Trace.Enabled
	}
	doc.Trace.DumpDir = strings.TrimSpace(doc.Trace.DumpDir)
	if doc.Trace.DumpDir == "" {
		doc.Trace.DumpDir = defaults.Trace.DumpDir
	}
	if doc.Trace.RedactUserContent == nil {
		doc.Trace.RedactUserContent = defaults.Trace.RedactUserContent
	}
	doc.Profile = normalizeClaudeCodeProfile(doc.Profile)
	if doc.ClaudeCode.Enabled == nil {
		doc.ClaudeCode.Enabled = defaults.ClaudeCode.Enabled
	}
	if doc.ClaudeCode.PureMode == nil {
		doc.ClaudeCode.PureMode = defaults.ClaudeCode.PureMode
	}
	doc.ClaudeCode.Cloak = normalizeCloakConfig(doc.ClaudeCode.Cloak)
	if doc.ClaudeCode.Usage.CleanInputTokens == nil {
		doc.ClaudeCode.Usage.CleanInputTokens = defaults.ClaudeCode.Usage.CleanInputTokens
	}
	if doc.ClaudeCode.Usage.SystemPromptOverheadTokens <= 0 {
		doc.ClaudeCode.Usage.SystemPromptOverheadTokens = defaults.ClaudeCode.Usage.SystemPromptOverheadTokens
	}
	if doc.ClaudeCode.PerAccountRPM < 0 {
		doc.ClaudeCode.PerAccountRPM = 0
	}
	if doc.ClaudeCode.PerAccountConcurrency <= 0 {
		doc.ClaudeCode.PerAccountConcurrency = defaults.ClaudeCode.PerAccountConcurrency
	}
	if doc.ClaudeCode.MaxSwitches < 0 {
		doc.ClaudeCode.MaxSwitches = 0
	}
	if doc.ClaudeCode.SwitchDelayMS <= 0 {
		doc.ClaudeCode.SwitchDelayMS = defaults.ClaudeCode.SwitchDelayMS
	}
	if doc.ClaudeCode.RateLimitCooldownMS <= 0 {
		doc.ClaudeCode.RateLimitCooldownMS = defaults.ClaudeCode.RateLimitCooldownMS
	}
	if doc.ClaudeCode.RateLimitMaxCooldownMS < doc.ClaudeCode.RateLimitCooldownMS {
		doc.ClaudeCode.RateLimitMaxCooldownMS = doc.ClaudeCode.RateLimitCooldownMS
	}
	if doc.ClaudeCode.OverloadCooldownMS <= 0 {
		doc.ClaudeCode.OverloadCooldownMS = defaults.ClaudeCode.OverloadCooldownMS
	}
	if doc.ClaudeCode.OverloadMaxCooldownMS < doc.ClaudeCode.OverloadCooldownMS {
		doc.ClaudeCode.OverloadMaxCooldownMS = doc.ClaudeCode.OverloadCooldownMS
	}
	if doc.ClaudeCode.SameAccountRetry429 < 0 {
		doc.ClaudeCode.SameAccountRetry429 = 0
	}
	if doc.ClaudeCode.SameAccountRetry529 < 0 {
		doc.ClaudeCode.SameAccountRetry529 = 0
	}
	if doc.ClaudeCode.SameAccountRetryDelayMS <= 0 {
		doc.ClaudeCode.SameAccountRetryDelayMS = defaults.ClaudeCode.SameAccountRetryDelayMS
	}
	doc.ClaudeCode.SessionAffinityTTL = strings.TrimSpace(doc.ClaudeCode.SessionAffinityTTL)
	if doc.ClaudeCode.SessionAffinityTTL == "" {
		doc.ClaudeCode.SessionAffinityTTL = defaults.ClaudeCode.SessionAffinityTTL
	}
	doc.ClaudeCode.Routing = normalizeClaudeCodeRoutingConfig(doc.ClaudeCode, defaults.ClaudeCode.Routing)
	doc.ClaudeCode.VirtualCache = claudeapipool.NormalizeVirtualCacheConfig(doc.ClaudeCode.VirtualCache)
	if doc.PoolConfig == nil {
		doc.PoolConfig = map[string]interface{}{}
	}
	for i := range doc.Proxies {
		doc.Proxies[i].Name = strings.TrimSpace(doc.Proxies[i].Name)
		doc.Proxies[i].ProxyURL = strings.TrimSpace(doc.Proxies[i].ProxyURL)
		doc.Proxies[i].ExitIP = strings.TrimSpace(doc.Proxies[i].ExitIP)
		doc.Proxies[i].Note = strings.TrimSpace(doc.Proxies[i].Note)
		doc.Proxies[i].Tags = normalizeStringList(doc.Proxies[i].Tags)
	}
}

func normalizeClaudeCodeProfile(profile ClaudeCodeProfile) ClaudeCodeProfile {
	defaults := defaultClaudeCodeProfile()
	profile.Version = strings.TrimSpace(profile.Version)
	if profile.Version != "" {
		defaults.Version = profile.Version
	}
	profile.UserAgent = strings.TrimSpace(profile.UserAgent)
	if profile.UserAgent != "" {
		defaults.UserAgent = profile.UserAgent
	}
	if len(profile.Headers) > 0 {
		headers := make(map[string]string, len(profile.Headers))
		for key, value := range profile.Headers {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			headers[key] = value
		}
		if len(headers) > 0 {
			defaults.Headers = headers
		}
	}
	if len(profile.Betas) > 0 {
		defaults.Betas = normalizeStringList(profile.Betas)
	}
	profile.SystemPrompt = strings.TrimSpace(profile.SystemPrompt)
	if profile.SystemPrompt != "" {
		defaults.SystemPrompt = profile.SystemPrompt
	}
	if profile.BillingBlockEnabled != nil {
		defaults.BillingBlockEnabled = profile.BillingBlockEnabled
	}
	profile.MetadataUserIDMode = strings.TrimSpace(profile.MetadataUserIDMode)
	if profile.MetadataUserIDMode != "" {
		defaults.MetadataUserIDMode = profile.MetadataUserIDMode
	}
	profile.UpdatedFrom = strings.TrimSpace(profile.UpdatedFrom)
	if profile.UpdatedFrom != "" {
		defaults.UpdatedFrom = profile.UpdatedFrom
	}
	if profile.UpdatedAt != nil {
		defaults.UpdatedAt = profile.UpdatedAt
	}
	profile.SystemPromptMode = strings.TrimSpace(profile.SystemPromptMode)
	if profile.SystemPromptMode != "" {
		defaults.SystemPromptMode = profile.SystemPromptMode
	}
	defaults.Locked = true
	return defaults
}

func normalizeCloakConfig(cfg *config.CloakConfig) *config.CloakConfig {
	if cfg == nil {
		defaults := defaultConfigFile()
		return defaults.ClaudeCode.Cloak
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "", "auto":
		mode = "auto"
	case "always", "never":
	default:
		mode = "auto"
	}
	cfg.Mode = mode
	cfg.SensitiveWords = normalizeStringList(cfg.SensitiveWords)
	return cfg
}

func normalizeClaudeCodeRoutingConfig(cfg ClaudeCodePoolConfig, defaults claudeapipool.RoutingConfig) claudeapipool.RoutingConfig {
	routing := cfg.Routing
	if routing.PerAccountRPM == 0 {
		routing.PerAccountRPM = cfg.PerAccountRPM
		if routing.PerAccountRPM == 0 {
			routing.PerAccountRPM = defaults.PerAccountRPM
		}
	}
	if routing.PerAccountConcurrency == 0 {
		routing.PerAccountConcurrency = cfg.PerAccountConcurrency
		if routing.PerAccountConcurrency == 0 {
			routing.PerAccountConcurrency = defaults.PerAccountConcurrency
		}
	}
	if routing.MaxSwitches == 0 {
		routing.MaxSwitches = cfg.MaxSwitches
		if routing.MaxSwitches == 0 {
			routing.MaxSwitches = defaults.MaxSwitches
		}
	}
	if routing.SwitchDelayMS == 0 {
		routing.SwitchDelayMS = cfg.SwitchDelayMS
		if routing.SwitchDelayMS == 0 {
			routing.SwitchDelayMS = defaults.SwitchDelayMS
		}
	}
	if routing.RateLimitCooldownMS == 0 {
		routing.RateLimitCooldownMS = cfg.RateLimitCooldownMS
		if routing.RateLimitCooldownMS == 0 {
			routing.RateLimitCooldownMS = defaults.RateLimitCooldownMS
		}
	}
	if routing.RateLimitMaxCooldownMS == 0 {
		routing.RateLimitMaxCooldownMS = cfg.RateLimitMaxCooldownMS
		if routing.RateLimitMaxCooldownMS == 0 {
			routing.RateLimitMaxCooldownMS = defaults.RateLimitMaxCooldownMS
		}
	}
	if routing.OverloadCooldownMS == 0 {
		routing.OverloadCooldownMS = cfg.OverloadCooldownMS
		if routing.OverloadCooldownMS == 0 {
			routing.OverloadCooldownMS = defaults.OverloadCooldownMS
		}
	}
	if routing.OverloadMaxCooldownMS == 0 {
		routing.OverloadMaxCooldownMS = cfg.OverloadMaxCooldownMS
		if routing.OverloadMaxCooldownMS == 0 {
			routing.OverloadMaxCooldownMS = defaults.OverloadMaxCooldownMS
		}
	}
	if routing.SameAccountRetry429 == 0 {
		routing.SameAccountRetry429 = cfg.SameAccountRetry429
	}
	if routing.SameAccountRetry529 == 0 {
		routing.SameAccountRetry529 = cfg.SameAccountRetry529
		if routing.SameAccountRetry529 == 0 {
			routing.SameAccountRetry529 = defaults.SameAccountRetry529
		}
	}
	if routing.SameAccountRetryDelayMS == 0 {
		routing.SameAccountRetryDelayMS = cfg.SameAccountRetryDelayMS
		if routing.SameAccountRetryDelayMS == 0 {
			routing.SameAccountRetryDelayMS = defaults.SameAccountRetryDelayMS
		}
	}
	if routing.CacheAffinityMinTokens == 0 {
		routing.CacheAffinityMinTokens = defaults.CacheAffinityMinTokens
	}
	if routing.CacheAffinityLanes == 0 {
		routing.CacheAffinityLanes = defaults.CacheAffinityLanes
	}
	if routing.CacheAffinityMaxLanes == 0 {
		routing.CacheAffinityMaxLanes = defaults.CacheAffinityMaxLanes
	}
	if routing.CacheAffinityWaitMS == 0 {
		routing.CacheAffinityWaitMS = defaults.CacheAffinityWaitMS
	}
	if routing.CacheAffinityTTLMS == 0 {
		routing.CacheAffinityTTLMS = defaults.CacheAffinityTTLMS
	}
	if strings.TrimSpace(routing.CacheAffinityAutoProfile) == "" {
		routing.CacheAffinityAutoProfile = defaults.CacheAffinityAutoProfile
	}
	if strings.TrimSpace(routing.AccountCapacityProfile) == "" {
		routing.AccountCapacityProfile = defaults.AccountCapacityProfile
	}
	return claudeapipool.NormalizeRoutingConfig(routing)
}

// EffectiveClaudeCodePool returns normalized Claude Code account-pool settings.
func EffectiveClaudeCodePool(cfg ClaudeCodePoolConfig) EffectiveClaudeCodePoolConfig {
	doc := &ConfigFile{
		DatabasePath: DefaultDBFileName,
		ProxyHealth:  defaultConfigFile().ProxyHealth,
		AccountQuota: defaultConfigFile().AccountQuota,
		Trace:        defaultConfigFile().Trace,
		ClaudeCode:   cfg,
		PoolConfig:   map[string]interface{}{},
	}
	normalizeConfigFile(doc)
	enabled := true
	if doc.ClaudeCode.Enabled != nil {
		enabled = *doc.ClaudeCode.Enabled
	}
	pureMode := true
	if doc.ClaudeCode.PureMode != nil {
		pureMode = *doc.ClaudeCode.PureMode
	}
	cloak := EffectiveCloakConfig{Mode: "auto"}
	if doc.ClaudeCode.Cloak != nil {
		cloak.Mode = strings.TrimSpace(doc.ClaudeCode.Cloak.Mode)
		cloak.StrictMode = doc.ClaudeCode.Cloak.StrictMode
		cloak.SensitiveWords = normalizeStringList(doc.ClaudeCode.Cloak.SensitiveWords)
	}
	return EffectiveClaudeCodePoolConfig{
		Enabled:      enabled,
		PureMode:     pureMode,
		Cloak:        cloak,
		Usage:        EffectiveClaudeCodeUsage(doc.ClaudeCode.Usage, doc.Profile),
		Log:          EffectiveAccountPoolLog(doc.ClaudeCode.Log),
		VirtualCache: claudeapipool.EffectiveVirtualCache(doc.ClaudeCode.VirtualCache),
		Routing:      claudeapipool.EffectiveRouting(doc.ClaudeCode.Routing),
	}
}

// EffectiveAccountPoolLog returns normalized dedicated account-pool logging settings.
func EffectiveAccountPoolLog(cfg AccountPoolLogConfig) EffectiveAccountPoolLogConfig {
	defaults := defaultConfigFile().ClaudeCode.Log
	if cfg.Enabled == nil {
		cfg.Enabled = defaults.Enabled
	}
	level := strings.ToLower(strings.TrimSpace(cfg.Level))
	switch level {
	case "debug", "info", "warn", "error":
	default:
		level = strings.ToLower(strings.TrimSpace(defaults.Level))
		if level == "" {
			level = "info"
		}
	}
	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		dir = defaults.Dir
	}
	maxSize := cfg.MaxSizeMB
	if maxSize <= 0 {
		maxSize = defaults.MaxSizeMB
	}
	if maxSize <= 0 {
		maxSize = 50
	}
	maxBackups := cfg.MaxBackups
	if maxBackups <= 0 {
		maxBackups = defaults.MaxBackups
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	if cfg.Redact == nil {
		cfg.Redact = defaults.Redact
	}
	return EffectiveAccountPoolLogConfig{
		Enabled:    cfg.Enabled != nil && *cfg.Enabled,
		Level:      level,
		Dir:        dir,
		MaxSizeMB:  maxSize,
		MaxBackups: maxBackups,
		Redact:     cfg.Redact == nil || *cfg.Redact,
	}
}

// EffectiveClaudeCodeUsage returns normalized downstream-visible usage settings.
func EffectiveClaudeCodeUsage(cfg ClaudeCodeUsageConfig, profile ClaudeCodeProfile) EffectiveClaudeCodeUsageConfig {
	defaults := defaultConfigFile().ClaudeCode.Usage
	if cfg.CleanInputTokens == nil {
		cfg.CleanInputTokens = defaults.CleanInputTokens
	}
	if cfg.SystemPromptOverheadTokens <= 0 {
		cfg.SystemPromptOverheadTokens = defaults.SystemPromptOverheadTokens
	}
	effectiveProfile := EffectiveClaudeCodeProfile(profile)
	return EffectiveClaudeCodeUsageConfig{
		CleanInputTokens:           cfg.CleanInputTokens != nil && *cfg.CleanInputTokens,
		SystemPromptOverheadTokens: cfg.SystemPromptOverheadTokens,
		ProfileFingerprint:         ClaudeCodeProfileFingerprint(effectiveProfile),
	}
}

// EffectiveClaudeCodeProfile returns normalized Claude Code request-shape settings.
func EffectiveClaudeCodeProfile(cfg ClaudeCodeProfile) EffectiveClaudeCodeProfileConfig {
	normalized := normalizeClaudeCodeProfile(cfg)
	enabled := true
	if normalized.BillingBlockEnabled != nil {
		enabled = *normalized.BillingBlockEnabled
	}
	headers := make(map[string]string, len(normalized.Headers))
	for key, value := range normalized.Headers {
		headers[key] = value
	}
	betas := append([]string(nil), normalized.Betas...)
	return EffectiveClaudeCodeProfileConfig{
		Version:             normalized.Version,
		UserAgent:           normalized.UserAgent,
		Headers:             headers,
		Betas:               betas,
		SystemPrompt:        normalized.SystemPrompt,
		BillingBlockEnabled: enabled,
		MetadataUserIDMode:  normalized.MetadataUserIDMode,
		UpdatedFrom:         normalized.UpdatedFrom,
		UpdatedAt:           normalized.UpdatedAt,
		Locked:              normalized.Locked,
		SystemPromptMode:    normalized.SystemPromptMode,
	}
}

// ApplyClaudeCodePoolRuntimeConfig loads SQLite-backed pool config and applies runtime routing policy.
func ApplyClaudeCodePoolRuntimeConfig(ctx context.Context, configFilePath string, cfg *config.Config) error {
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return nil
	}
	store, err := Open(configFilePath, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return err
	}
	effective := EffectiveClaudeCodePool(doc.ClaudeCode)
	claudeapipool.SetScopedRoutingConfig(coreexecutor.PoolScopeClaudeAccountPool, effective.Routing)
	return nil
}

// EffectiveTrace returns normalized local trace dump settings.
func EffectiveTrace(cfg TraceConfig) EffectiveTraceConfig {
	defaults := defaultConfigFile().Trace
	if cfg.Enabled == nil {
		cfg.Enabled = defaults.Enabled
	}
	cfg.DumpDir = strings.TrimSpace(cfg.DumpDir)
	if cfg.DumpDir == "" {
		cfg.DumpDir = defaults.DumpDir
	}
	if cfg.RedactUserContent == nil {
		cfg.RedactUserContent = defaults.RedactUserContent
	}
	return EffectiveTraceConfig{
		Enabled:           cfg.Enabled != nil && *cfg.Enabled,
		DumpDir:           cfg.DumpDir,
		RedactUserContent: cfg.RedactUserContent == nil || *cfg.RedactUserContent,
	}
}

// EffectiveProxyHealth returns normalized health-check settings.
func EffectiveProxyHealth(cfg ProxyHealthConfig) EffectiveProxyHealthConfig {
	defaults := defaultConfigFile().ProxyHealth
	if cfg.Enabled == nil {
		cfg.Enabled = defaults.Enabled
	}
	if strings.TrimSpace(cfg.Interval) == "" {
		cfg.Interval = defaults.Interval
	}
	interval, errInterval := time.ParseDuration(strings.TrimSpace(cfg.Interval))
	if errInterval != nil || interval <= 0 {
		interval, _ = time.ParseDuration(defaults.Interval)
		cfg.Interval = defaults.Interval
	}
	if strings.TrimSpace(cfg.Timeout) == "" {
		cfg.Timeout = defaults.Timeout
	}
	timeout, errTimeout := time.ParseDuration(strings.TrimSpace(cfg.Timeout))
	if errTimeout != nil || timeout <= 0 {
		timeout, _ = time.ParseDuration(defaults.Timeout)
		cfg.Timeout = defaults.Timeout
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaults.Concurrency
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaults.FailureThreshold
	}
	cfg.TestURL = strings.TrimSpace(cfg.TestURL)
	if cfg.TestURL == "" {
		cfg.TestURL = defaults.TestURL
	}
	return EffectiveProxyHealthConfig{
		Enabled:           cfg.Enabled == nil || *cfg.Enabled,
		Interval:          interval,
		IntervalText:      interval.String(),
		Timeout:           timeout,
		TimeoutText:       timeout.String(),
		Concurrency:       cfg.Concurrency,
		FailureThreshold:  cfg.FailureThreshold,
		TestURL:           cfg.TestURL,
		OptionalExitIPURL: strings.TrimSpace(cfg.OptionalExitIPURL),
	}
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
