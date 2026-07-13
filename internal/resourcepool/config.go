package resourcepool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
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
	allowClientCacheTTL := false
	cleanInputTokens := true
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
			Enabled:             &claudeCodeEnabled,
			PureMode:            &pureMode,
			AllowClientCacheTTL: &allowClientCacheTTL,
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
			Routing: routing,
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
		StickyConcurrencyReserve: 1,
		MaxSessions:              30,
		StickyWaitMS:             2000,
		FallbackWaitMS:           500,
		MaxWaitersPerAccount:     5,
		MaxWaitersGlobal:         200,
		SessionAffinityTTLMS:     int(time.Hour / time.Millisecond),
		ActiveSessionIdleTTLMS:   int((5 * time.Minute) / time.Millisecond),
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
		Revision:  DefaultClaudeCodeProfileRevision,
		Version:   DefaultClaudeCodeProfileVersion,
		UserAgent: "claude-cli/" + DefaultClaudeCodeProfileVersion + " (external, sdk-cli)",
		Headers: map[string]string{
			"Anthropic-Version":                         "2023-06-01",
			"Anthropic-Dangerous-Direct-Browser-Access": "true",
			"X-App":                       "cli",
			"X-Stainless-Retry-Count":     "0",
			"X-Stainless-Runtime":         "node",
			"X-Stainless-Runtime-Version": "v26.3.0",
			"X-Stainless-Package-Version": "0.94.0",
			"X-Stainless-Os":              "MacOS",
			"X-Stainless-Arch":            "arm64",
			"X-Stainless-Lang":            "js",
			"X-Stainless-Timeout":         "600",
		},
		HeaderOrder: helps.ClaudeCodeNodeHeaderOrder(),
		Betas: []string{
			"claude-code-20250219",
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
		},
		SystemPrompt:        helps.ClaudeCodeOrdinaryStablePrompt(),
		BillingBlockEnabled: &billingBlockEnabled,
		MetadataUserIDMode:  "account",
		UpdatedFrom:         "builtin-trace-baseline:2.1.207",
		Locked:              true,
		SystemPromptMode:    "builtin_ordinary_2.1.207_r3",
		TLSProfile:          helps.ClaudeCodeNodeTLSProfileName,
		TLSJA3:              helps.ClaudeCodeNodeTLSJA3,
		TLSJA4:              helps.ClaudeCodeNodeTLSJA4,
		TLSALPN:             helps.ClaudeCodeNodeTLSALPN,
	}
}

func builtinClaudeCodeProfileR2() ClaudeCodeProfile {
	billingBlockEnabled := true
	return ClaudeCodeProfile{
		Revision:  "2.1.207-r2",
		Version:   "2.1.207",
		UserAgent: "claude-cli/2.1.207 (external, sdk-cli)",
		Headers: map[string]string{
			"Anthropic-Version":                         "2023-06-01",
			"Anthropic-Dangerous-Direct-Browser-Access": "true",
			"X-App":                       "cli",
			"X-Stainless-Retry-Count":     "0",
			"X-Stainless-Runtime":         "node",
			"X-Stainless-Runtime-Version": "v26.3.0",
			"X-Stainless-Package-Version": "0.94.0",
			"X-Stainless-Os":              "MacOS",
			"X-Stainless-Arch":            "arm64",
			"X-Stainless-Lang":            "js",
			"X-Stainless-Timeout":         "600",
		},
		HeaderOrder: helps.ClaudeCodeNodeR2HeaderOrder(),
		Betas: []string{
			"claude-code-20250219",
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
		},
		SystemPrompt:        helps.ClaudeCodeStaticPrompt(),
		BillingBlockEnabled: &billingBlockEnabled,
		MetadataUserIDMode:  "account",
		UpdatedFrom:         "builtin-trace-baseline:2.1.207",
		Locked:              true,
		SystemPromptMode:    "builtin_stable_2.1.207",
		TLSProfile:          helps.ClaudeCodeNodeTLSProfileName,
		TLSJA3:              helps.ClaudeCodeNodeTLSJA3,
		TLSJA4:              helps.ClaudeCodeNodeTLSJA4,
		TLSALPN:             helps.ClaudeCodeNodeTLSALPN,
	}
}

func isExactBuiltinClaudeCodeProfileR2(profile ClaudeCodeProfile) bool {
	profile.UpdatedAt = nil
	expected := builtinClaudeCodeProfileR2()
	expected.UpdatedAt = nil
	return reflect.DeepEqual(profile, expected)
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
	if doc.ClaudeCode.AllowClientCacheTTL == nil {
		doc.ClaudeCode.AllowClientCacheTTL = defaults.ClaudeCode.AllowClientCacheTTL
	}
	doc.ClaudeCode.Cloak = normalizeCloakConfig(doc.ClaudeCode.Cloak)
	// pure-mode is the single downstream usage-cleaning switch. Keep the old
	// clean-input field synchronized for stored-config and API compatibility.
	cleanInputTokens := doc.ClaudeCode.PureMode != nil && *doc.ClaudeCode.PureMode
	doc.ClaudeCode.Usage.CleanInputTokens = boolPtr(cleanInputTokens)
	if doc.ClaudeCode.Usage.SystemPromptOverheadTokens <= 0 {
		doc.ClaudeCode.Usage.SystemPromptOverheadTokens = defaults.ClaudeCode.Usage.SystemPromptOverheadTokens
	}
	doc.ClaudeCode.Routing = normalizeClaudeCodeRoutingConfig(doc.ClaudeCode, defaults.ClaudeCode.Routing)
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
	profile.Revision = strings.TrimSpace(profile.Revision)
	if shouldMigrateBuiltinClaudeCodeProfile(profile) {
		return defaults
	}
	if profile.Revision != "" {
		defaults.Revision = profile.Revision
	}
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
	if len(profile.HeaderOrder) > 0 {
		if order := normalizeStringList(profile.HeaderOrder); len(order) > 0 {
			defaults.HeaderOrder = order
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
	if value := strings.TrimSpace(profile.TLSProfile); value != "" {
		defaults.TLSProfile = value
	}
	if value := strings.TrimSpace(profile.TLSJA3); value != "" {
		defaults.TLSJA3 = value
	}
	if value := strings.TrimSpace(profile.TLSJA4); value != "" {
		defaults.TLSJA4 = value
	}
	if value := strings.TrimSpace(profile.TLSALPN); value != "" {
		defaults.TLSALPN = value
	}
	defaults.Locked = true
	return defaults
}

func shouldMigrateBuiltinClaudeCodeProfile(profile ClaudeCodeProfile) bool {
	version := strings.TrimSpace(profile.Version)
	if !profile.Locked {
		return false
	}
	updatedFrom := strings.ToLower(strings.TrimSpace(profile.UpdatedFrom))
	if updatedFrom != "" && !strings.HasPrefix(updatedFrom, "builtin") {
		return false
	}
	if version == "2.1.177" || version == "2.1.178" {
		return true
	}
	return version == DefaultClaudeCodeProfileVersion && strings.TrimSpace(profile.Revision) == "2.1.207-r1"
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
		routing.PerAccountRPM = defaults.PerAccountRPM
	}
	if routing.PerAccountConcurrency == 0 {
		routing.PerAccountConcurrency = defaults.PerAccountConcurrency
	}
	if routing.StickyConcurrencyReserve == 0 {
		routing.StickyConcurrencyReserve = defaults.StickyConcurrencyReserve
	}
	if routing.MaxSessions == 0 {
		routing.MaxSessions = defaults.MaxSessions
	}
	if routing.StickyWaitMS == 0 {
		routing.StickyWaitMS = defaults.StickyWaitMS
	}
	if routing.FallbackWaitMS == 0 {
		routing.FallbackWaitMS = defaults.FallbackWaitMS
	}
	if routing.MaxWaitersPerAccount == 0 {
		routing.MaxWaitersPerAccount = defaults.MaxWaitersPerAccount
	}
	if routing.MaxWaitersGlobal == 0 {
		routing.MaxWaitersGlobal = defaults.MaxWaitersGlobal
	}
	if routing.SessionAffinityTTLMS == 0 {
		routing.SessionAffinityTTLMS = defaults.SessionAffinityTTLMS
	}
	if routing.ActiveSessionIdleTTLMS == 0 {
		routing.ActiveSessionIdleTTLMS = defaults.ActiveSessionIdleTTLMS
	}
	if routing.MaxSwitches == 0 {
		routing.MaxSwitches = defaults.MaxSwitches
	}
	if routing.SwitchDelayMS == 0 {
		routing.SwitchDelayMS = defaults.SwitchDelayMS
	}
	if routing.RateLimitCooldownMS == 0 {
		routing.RateLimitCooldownMS = defaults.RateLimitCooldownMS
	}
	if routing.RateLimitMaxCooldownMS == 0 {
		routing.RateLimitMaxCooldownMS = defaults.RateLimitMaxCooldownMS
	}
	if routing.OverloadCooldownMS == 0 {
		routing.OverloadCooldownMS = defaults.OverloadCooldownMS
	}
	if routing.OverloadMaxCooldownMS == 0 {
		routing.OverloadMaxCooldownMS = defaults.OverloadMaxCooldownMS
	}
	if routing.SameAccountRetry529 == 0 {
		routing.SameAccountRetry529 = defaults.SameAccountRetry529
	}
	if routing.SameAccountRetryDelayMS == 0 {
		routing.SameAccountRetryDelayMS = defaults.SameAccountRetryDelayMS
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
	allowClientCacheTTL := false
	if doc.ClaudeCode.AllowClientCacheTTL != nil {
		allowClientCacheTTL = *doc.ClaudeCode.AllowClientCacheTTL
	}
	cloak := EffectiveCloakConfig{Mode: "auto"}
	if doc.ClaudeCode.Cloak != nil {
		cloak.Mode = strings.TrimSpace(doc.ClaudeCode.Cloak.Mode)
		cloak.StrictMode = doc.ClaudeCode.Cloak.StrictMode
		cloak.SensitiveWords = normalizeStringList(doc.ClaudeCode.Cloak.SensitiveWords)
	}
	usage := EffectiveClaudeCodeUsage(doc.ClaudeCode.Usage, doc.Profile)
	usage.CleanInputTokens = pureMode
	return EffectiveClaudeCodePoolConfig{
		Enabled:             enabled,
		PureMode:            pureMode,
		AllowClientCacheTTL: allowClientCacheTTL,
		Cloak:               cloak,
		Usage:               usage,
		Log:                 EffectiveAccountPoolLog(doc.ClaudeCode.Log),
		Routing:             claudeapipool.EffectiveRouting(doc.ClaudeCode.Routing),
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
	cleanInputTokens := false
	if cfg.CleanInputTokens == nil {
		cfg.CleanInputTokens = &cleanInputTokens
	}
	effectiveProfile := EffectiveClaudeCodeProfile(profile)
	if cfg.SystemPromptOverheadTokens <= 0 {
		cfg.SystemPromptOverheadTokens = ClaudeCodeProfileInjectedOverheadTokens(effectiveProfile)
	}
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
		Revision:            normalized.Revision,
		Version:             normalized.Version,
		UserAgent:           normalized.UserAgent,
		Headers:             headers,
		HeaderOrder:         append([]string(nil), normalized.HeaderOrder...),
		Betas:               betas,
		SystemPrompt:        normalized.SystemPrompt,
		BillingBlockEnabled: enabled,
		MetadataUserIDMode:  normalized.MetadataUserIDMode,
		UpdatedFrom:         normalized.UpdatedFrom,
		UpdatedAt:           normalized.UpdatedAt,
		Locked:              normalized.Locked,
		SystemPromptMode:    normalized.SystemPromptMode,
		TLSProfile:          normalized.TLSProfile,
		TLSJA3:              normalized.TLSJA3,
		TLSJA4:              normalized.TLSJA4,
		TLSALPN:             normalized.TLSALPN,
	}
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
