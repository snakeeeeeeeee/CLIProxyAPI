// Package resourcepool manages Claude Code OAuth accounts and proxy resources.
package resourcepool

import (
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const (
	DefaultConfigFileName            = "resource-pools.yaml"
	DefaultDBFileName                = "resource-pools.db"
	DefaultAccountPoolID             = "default"
	DefaultClaudeCodeProfileVersion  = "2.1.207"
	DefaultClaudeCodeProfileRevision = "2.1.207-r3"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
	HealthDisabled  = "disabled"

	AccountHealthChecking           = "checking"
	AccountHealthHealthy            = "healthy"
	AccountHealthTemporarilyBlocked = "temporarily_blocked"
	AccountHealthManualRecovery     = "manual_recovery"

	AttrClaudeOAuthPool = "claude_oauth_pool"
	AttrAccountID       = "claude_code_account_id"
	AttrAccountPoolID   = coreexecutor.AccountPoolIDAttributeKey
	AttrProxyResourceID = "proxy_resource_id"

	AttrProfileRevision            = "claude_code_profile_revision"
	AttrProfileVersion             = "claude_code_profile_version"
	AttrProfileUserAgent           = "claude_code_profile_user_agent"
	AttrProfileHeadersJSON         = "claude_code_profile_headers_json"
	AttrProfileHeaderOrderJSON     = "claude_code_profile_header_order_json"
	AttrProfileBetasJSON           = "claude_code_profile_betas_json"
	AttrProfileSystemPrompt        = "claude_code_profile_system_prompt"
	AttrProfileBillingBlockEnabled = "claude_code_profile_billing_block_enabled"
	AttrProfileMetadataUserIDMode  = "claude_code_profile_metadata_user_id_mode"
	AttrProfileManaged             = "claude_code_profile_managed"

	AttrCapacityBaseRPM          = "claude_code_capacity_base_rpm"
	AttrCapacityConcurrencyLimit = "claude_code_capacity_concurrency_limit"
	AttrCapacityMaxSessions      = "claude_code_capacity_max_sessions"
	AttrCapacityStickyReserve    = "claude_code_capacity_sticky_concurrency_reserve"

	AttrCleanInputTokens          = "claude_code_clean_input_tokens"
	AttrCleanInputDefaultOverhead = "claude_code_clean_input_default_overhead_tokens"
	AttrProfileFingerprint        = "claude_code_profile_fingerprint"
	AttrUsageOverheadsJSON        = "claude_code_usage_overheads_json"
	AttrAllowClientCacheTTL       = "claude_code_allow_client_cache_ttl"
)

// IsClaudeCodeAccountPoolAuth reports whether attributes belong to the SQLite-backed Claude Code account pool.
func IsClaudeCodeAccountPoolAuth(attrs map[string]string) bool {
	if len(attrs) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(attrs[AttrClaudeOAuthPool]), "true") &&
		strings.TrimSpace(attrs[AttrAccountID]) != ""
}

// ConfigFile is the YAML initializer for the resource pools subsystem.
type ConfigFile struct {
	DatabasePath string                 `yaml:"database-path,omitempty" json:"database_path,omitempty"`
	ProxyHealth  ProxyHealthConfig      `yaml:"proxy-health,omitempty" json:"proxy_health,omitempty"`
	AccountQuota AccountQuotaConfig     `yaml:"account-quota,omitempty" json:"account_quota,omitempty"`
	Trace        TraceConfig            `yaml:"trace,omitempty" json:"trace,omitempty"`
	ClaudeCode   ClaudeCodePoolConfig   `yaml:"claude-code-pool,omitempty" json:"claude_code_pool,omitempty"`
	Profile      ClaudeCodeProfile      `yaml:"claude-code-profile,omitempty" json:"claude_code_profile,omitempty"`
	Proxies      []ProxyResourceSeed    `yaml:"proxies,omitempty" json:"proxies,omitempty"`
	PoolConfig   map[string]interface{} `yaml:"pool-config,omitempty" json:"pool_config,omitempty"`
}

// ProxyHealthConfig controls scheduled and manual proxy health tests.
type ProxyHealthConfig struct {
	Enabled           *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Interval          string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout           string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Concurrency       int    `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	FailureThreshold  int    `yaml:"failure-threshold,omitempty" json:"failure_threshold,omitempty"`
	TestURL           string `yaml:"test-url,omitempty" json:"test_url,omitempty"`
	OptionalExitIPURL string `yaml:"optional-exit-ip-url,omitempty" json:"optional_exit_ip_url,omitempty"`
}

// AccountQuotaConfig controls background Claude OAuth usage refreshes.
type AccountQuotaConfig struct {
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Interval    string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Concurrency int    `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
}

// TraceConfig controls local redacted Claude Code request trace dumps.
type TraceConfig struct {
	Enabled           *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	DumpDir           string `yaml:"dump-dir,omitempty" json:"dump_dir,omitempty"`
	RedactUserContent *bool  `yaml:"redact-user-content,omitempty" json:"redact_user_content,omitempty"`
}

// AccountPoolLogConfig controls dedicated Claude Code account-pool JSONL diagnostics.
type AccountPoolLogConfig struct {
	Enabled    *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Level      string `yaml:"level,omitempty" json:"level,omitempty"`
	Dir        string `yaml:"dir,omitempty" json:"dir,omitempty"`
	MaxSizeMB  int    `yaml:"max-size-mb,omitempty" json:"max_size_mb,omitempty"`
	MaxBackups int    `yaml:"max-backups,omitempty" json:"max_backups,omitempty"`
	Redact     *bool  `yaml:"redact,omitempty" json:"redact,omitempty"`
}

// EffectiveAccountPoolLogConfig is the normalized runtime view of account-pool logging.
type EffectiveAccountPoolLogConfig struct {
	Enabled    bool   `json:"enabled"`
	Level      string `json:"level"`
	Dir        string `json:"dir"`
	MaxSizeMB  int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
	Redact     bool   `json:"redact"`
}

// EffectiveTraceConfig is the normalized runtime trace dump policy.
type EffectiveTraceConfig struct {
	Enabled           bool   `json:"enabled"`
	DumpDir           string `json:"dump_dir"`
	RedactUserContent bool   `json:"redact_user_content"`
}

// EffectiveProxyHealthConfig is the normalized runtime health-check policy.
type EffectiveProxyHealthConfig struct {
	Enabled           bool          `json:"enabled"`
	Interval          time.Duration `json:"-"`
	IntervalText      string        `json:"interval"`
	Timeout           time.Duration `json:"-"`
	TimeoutText       string        `json:"timeout"`
	Concurrency       int           `json:"concurrency"`
	FailureThreshold  int           `json:"failure_threshold"`
	TestURL           string        `json:"test_url"`
	OptionalExitIPURL string        `json:"optional_exit_ip_url"`
}

// ClaudeCodePoolConfig stores conservative defaults for OAuth account routing.
type ClaudeCodePoolConfig struct {
	Enabled             *bool                       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	PureMode            *bool                       `yaml:"pure-mode,omitempty" json:"pure_mode,omitempty"`
	AllowClientCacheTTL *bool                       `yaml:"allow-client-cache-ttl,omitempty" json:"allow_client_cache_ttl,omitempty"`
	Cloak               *config.CloakConfig         `yaml:"cloak,omitempty" json:"cloak,omitempty"`
	Usage               ClaudeCodeUsageConfig       `yaml:"usage,omitempty" json:"usage,omitempty"`
	Log                 AccountPoolLogConfig        `yaml:"log,omitempty" json:"log,omitempty"`
	Routing             claudeapipool.RoutingConfig `yaml:"routing,omitempty" json:"routing,omitempty"`
}

// ClaudeCodeUsageConfig controls downstream-only usage display behavior.
type ClaudeCodeUsageConfig struct {
	// CleanInputTokens is retained as a stored-config compatibility alias for pure-mode.
	CleanInputTokens           *bool `yaml:"clean-input-tokens,omitempty" json:"clean_input_tokens,omitempty"`
	SystemPromptOverheadTokens int64 `yaml:"system-prompt-overhead-tokens,omitempty" json:"system_prompt_overhead_tokens,omitempty"`
}

// EffectiveClaudeCodePoolConfig is the management/API view of Claude Code pool settings.
type EffectiveClaudeCodePoolConfig struct {
	Enabled             bool                                 `json:"enabled"`
	PureMode            bool                                 `json:"pure_mode"`
	AllowClientCacheTTL bool                                 `json:"allow_client_cache_ttl"`
	Cloak               EffectiveCloakConfig                 `json:"cloak"`
	Usage               EffectiveClaudeCodeUsageConfig       `json:"usage"`
	Log                 EffectiveAccountPoolLogConfig        `json:"log"`
	Routing             claudeapipool.EffectiveRoutingConfig `json:"routing"`
}

// ClaudeCodeAccountPoolConfigOverrides stores only settings explicitly overridden by one account pool.
type ClaudeCodeAccountPoolConfigOverrides struct {
	PureMode *bool                                 `json:"pure_mode,omitempty"`
	Routing  ClaudeCodeAccountPoolRoutingOverrides `json:"routing,omitempty"`
}

// ClaudeCodeAccountPoolRoutingOverrides is the sparse pool-level routing policy.
type ClaudeCodeAccountPoolRoutingOverrides struct {
	PerAccountRPM            *int    `json:"per_account_rpm,omitempty"`
	PerAccountConcurrency    *int    `json:"per_account_concurrency,omitempty"`
	StickyConcurrencyReserve *int    `json:"sticky_concurrency_reserve,omitempty"`
	MaxSessions              *int    `json:"max_sessions,omitempty"`
	StickyWaitMS             *int    `json:"sticky_wait_ms,omitempty"`
	FallbackWaitMS           *int    `json:"fallback_wait_ms,omitempty"`
	MaxWaitersPerAccount     *int    `json:"max_waiters_per_account,omitempty"`
	MaxWaitersGlobal         *int    `json:"max_waiters_global,omitempty"`
	SessionAffinityTTLMS     *int    `json:"session_affinity_ttl_ms,omitempty"`
	ActiveSessionIdleTTLMS   *int    `json:"active_session_idle_ttl_ms,omitempty"`
	MaxSwitches              *int    `json:"max_switches,omitempty"`
	SwitchDelayMS            *int    `json:"switch_delay_ms,omitempty"`
	RateLimitCooldownMS      *int    `json:"rate_limit_cooldown_ms,omitempty"`
	RateLimitMaxCooldownMS   *int    `json:"rate_limit_max_cooldown_ms,omitempty"`
	OverloadCooldownMS       *int    `json:"overload_cooldown_ms,omitempty"`
	OverloadMaxCooldownMS    *int    `json:"overload_max_cooldown_ms,omitempty"`
	SameAccountRetry429      *int    `json:"same_account_retry_429,omitempty"`
	SameAccountRetry529      *int    `json:"same_account_retry_529,omitempty"`
	SameAccountRetryDelayMS  *int    `json:"same_account_retry_delay_ms,omitempty"`
	CacheAffinityEnabled     *bool   `json:"cache_affinity_enabled,omitempty"`
	CacheAffinityAuto        *bool   `json:"cache_affinity_auto,omitempty"`
	CacheAffinityAutoProfile *string `json:"cache_affinity_auto_profile,omitempty"`
	AccountCapacityProfile   *string `json:"account_capacity_profile,omitempty"`
	CacheAffinityMinTokens   *int    `json:"cache_affinity_min_cache_tokens,omitempty"`
	CacheAffinityLanes       *int    `json:"cache_affinity_lanes,omitempty"`
	CacheAffinityMaxLanes    *int    `json:"cache_affinity_max_lanes,omitempty"`
	CacheAffinityWaitMS      *int    `json:"cache_affinity_wait_ms,omitempty"`
	CacheAffinityTTLMS       *int    `json:"cache_affinity_ttl_ms,omitempty"`
}

// EffectiveClaudeCodeAccountPoolConfig is the pool-overridable runtime subset.
type EffectiveClaudeCodeAccountPoolConfig struct {
	PureMode bool                                 `json:"pure_mode"`
	Routing  claudeapipool.EffectiveRoutingConfig `json:"routing"`
}

// ClaudeCodeAccountPoolConfigView explains inherited and explicitly overridden values.
type ClaudeCodeAccountPoolConfigView struct {
	Overrides ClaudeCodeAccountPoolConfigOverrides `json:"overrides"`
	Effective EffectiveClaudeCodeAccountPoolConfig `json:"effective"`
	Global    EffectiveClaudeCodeAccountPoolConfig `json:"global"`
	Sources   map[string]string                    `json:"sources"`
}

// EffectiveClaudeCodeUsageConfig is the normalized management/API view of usage display behavior.
type EffectiveClaudeCodeUsageConfig struct {
	// CleanInputTokens mirrors PureMode for management API compatibility.
	CleanInputTokens           bool   `json:"clean_input_tokens"`
	SystemPromptOverheadTokens int64  `json:"system_prompt_overhead_tokens"`
	ProfileFingerprint         string `json:"profile_fingerprint"`
}

// EffectiveCloakConfig is the normalized management/API view of request cloaking.
type EffectiveCloakConfig struct {
	Mode           string   `json:"mode"`
	StrictMode     bool     `json:"strict_mode"`
	SensitiveWords []string `json:"sensitive_words"`
}

// ClaudeCodeProfile describes the built-in request shape used by the dedicated account pool.
// It is intentionally not user-editable at runtime because small mismatches in
// this profile make Claude Code OAuth traffic easier to distinguish.
type ClaudeCodeProfile struct {
	Revision            string            `yaml:"revision,omitempty" json:"revision,omitempty"`
	Version             string            `yaml:"version,omitempty" json:"version,omitempty"`
	UserAgent           string            `yaml:"user-agent,omitempty" json:"user_agent,omitempty"`
	Headers             map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	HeaderOrder         []string          `yaml:"header-order,omitempty" json:"header_order,omitempty"`
	Betas               []string          `yaml:"betas,omitempty" json:"betas,omitempty"`
	SystemPrompt        string            `yaml:"system-prompt,omitempty" json:"system_prompt,omitempty"`
	BillingBlockEnabled *bool             `yaml:"billing-block-enabled,omitempty" json:"billing_block_enabled,omitempty"`
	MetadataUserIDMode  string            `yaml:"metadata-user-id-mode,omitempty" json:"metadata_user_id_mode,omitempty"`
	UpdatedFrom         string            `yaml:"updated-from,omitempty" json:"updated_from,omitempty"`
	UpdatedAt           *time.Time        `yaml:"updated-at,omitempty" json:"updated_at,omitempty"`
	Locked              bool              `yaml:"locked,omitempty" json:"locked,omitempty"`
	SystemPromptMode    string            `yaml:"system-prompt-mode,omitempty" json:"system_prompt_mode,omitempty"`
	TLSProfile          string            `yaml:"tls-profile,omitempty" json:"tls_profile,omitempty"`
	TLSJA3              string            `yaml:"tls-ja3,omitempty" json:"tls_ja3,omitempty"`
	TLSJA4              string            `yaml:"tls-ja4,omitempty" json:"tls_ja4,omitempty"`
	TLSALPN             string            `yaml:"tls-alpn,omitempty" json:"tls_alpn,omitempty"`
}

// EffectiveClaudeCodeProfileConfig is the normalized runtime request-shape profile.
type EffectiveClaudeCodeProfileConfig struct {
	Revision            string            `json:"revision"`
	Version             string            `json:"version"`
	UserAgent           string            `json:"user_agent"`
	Headers             map[string]string `json:"headers"`
	HeaderOrder         []string          `json:"header_order"`
	Betas               []string          `json:"betas"`
	SystemPrompt        string            `json:"system_prompt"`
	BillingBlockEnabled bool              `json:"billing_block_enabled"`
	MetadataUserIDMode  string            `json:"metadata_user_id_mode"`
	UpdatedFrom         string            `json:"updated_from,omitempty"`
	UpdatedAt           *time.Time        `json:"updated_at,omitempty"`
	Locked              bool              `json:"locked"`
	SystemPromptMode    string            `json:"system_prompt_mode"`
	TLSProfile          string            `json:"tls_profile"`
	TLSJA3              string            `json:"tls_ja3"`
	TLSJA4              string            `json:"tls_ja4"`
	TLSALPN             string            `json:"tls_alpn"`
}

// ClaudeCodeProfileSnapshot stores a versioned request-shape baseline fetched
// from an external source such as Phistory.
type ClaudeCodeProfileSnapshot struct {
	ID                    string             `json:"id"`
	Source                string             `json:"source"`
	Version               string             `json:"version"`
	Status                string             `json:"status"`
	MetaJSON              string             `json:"meta_json,omitempty"`
	TraceJSONL            string             `json:"trace_jsonl,omitempty"`
	PromptMD              string             `json:"prompt_md,omitempty"`
	StaticPromptsMD       string             `json:"static_prompts_md,omitempty"`
	StaticPromptsJSON     string             `json:"static_prompts_json,omitempty"`
	NormalizedProfileJSON string             `json:"normalized_profile_json,omitempty"`
	NormalizedProfile     *ClaudeCodeProfile `json:"normalized_profile,omitempty"`
	PromptHash            string             `json:"prompt_hash,omitempty"`
	StaticPromptHash      string             `json:"static_prompt_hash,omitempty"`
	StaticPromptLength    int                `json:"static_prompt_length"`
	FullPromptHash        string             `json:"full_prompt_hash,omitempty"`
	FullPromptLength      int                `json:"full_prompt_length"`
	RequestKindSummary    map[string]int     `json:"request_kind_summary,omitempty"`
	TraceHash             string             `json:"trace_hash,omitempty"`
	DiffReport            string             `json:"diff_report,omitempty"`
	FatalCount            int                `json:"fatal_count"`
	WarnCount             int                `json:"warn_count"`
	Promoted              bool               `json:"promoted"`
	LastError             string             `json:"last_error,omitempty"`
	FetchedAt             *time.Time         `json:"fetched_at,omitempty"`
	PromotedAt            *time.Time         `json:"promoted_at,omitempty"`
	CreatedAt             time.Time          `json:"created_at,omitempty"`
	UpdatedAt             time.Time          `json:"updated_at,omitempty"`
}

// ClaudeCodeProfileSnapshotFetchRequest controls a profile baseline fetch.
type ClaudeCodeProfileSnapshotFetchRequest struct {
	Version string `json:"version,omitempty"`
	Latest  bool   `json:"latest,omitempty"`
	Source  string `json:"source,omitempty"`
}

// ClaudeCodeProfileSnapshotDiff summarizes current-profile drift from a snapshot.
type ClaudeCodeProfileSnapshotDiff struct {
	SnapshotID         string   `json:"snapshot_id"`
	Version            string   `json:"version"`
	CurrentVersion     string   `json:"current_version"`
	ProfileFingerprint string   `json:"profile_fingerprint"`
	FatalCount         int      `json:"fatal_count"`
	WarnCount          int      `json:"warn_count"`
	Report             string   `json:"report"`
	Issues             []string `json:"issues"`
}

// AccountCapacityConfig is the persisted lightweight local capacity model.
type AccountCapacityConfig struct {
	AccountID                string    `json:"account_id,omitempty"`
	BaseRPM                  int       `json:"base_rpm"`
	ConcurrencyLimit         int       `json:"concurrency_limit"`
	MaxSessions              int       `json:"max_sessions"`
	StickyConcurrencyReserve int       `json:"sticky_concurrency_reserve"`
	UpdatedAt                time.Time `json:"updated_at,omitempty"`
}

// AccountRuntimeCapacity is the management view for current local pressure.
type AccountRuntimeCapacity struct {
	AccountID                string `json:"account_id,omitempty"`
	BaseRPM                  int    `json:"base_rpm"`
	ConcurrencyLimit         int    `json:"concurrency_limit"`
	MaxSessions              int    `json:"max_sessions"`
	StickyConcurrencyReserve int    `json:"sticky_concurrency_reserve"`
	CapacityUsed             int    `json:"capacity_used"`
	CapacityLimit            int    `json:"capacity_limit"`
	InFlight                 int64  `json:"in_flight"`
	RPMUsed                  int    `json:"rpm_used"`
	RPMLimit                 int    `json:"rpm_limit"`
	StickySessions           int    `json:"sticky_sessions"`
	ReserveUsed              int    `json:"reserve_used"`
	ActiveSessions           int    `json:"active_sessions"`
	Waiters                  int    `json:"waiters"`
	Cooling                  bool   `json:"cooling"`
	AccountCooling           bool   `json:"account_cooling"`
	ModelCoolingCount        int    `json:"model_cooling_count"`
	CoolingUntil             string `json:"cooling_until,omitempty"`
	Unavailable              bool   `json:"unavailable"`
}

// AccountAvailabilityBucket summarizes one minute of recent account traffic.
type AccountAvailabilityBucket struct {
	StartedAt    time.Time `json:"started_at"`
	RequestCount int64     `json:"request_count"`
	SuccessCount int64     `json:"success_count"`
	SuccessRate  float64   `json:"success_rate"`
	Status       string    `json:"status"`
}

// AccountAvailabilitySummary summarizes recent per-minute account availability.
type AccountAvailabilitySummary struct {
	WindowMinutes int                         `json:"window_minutes"`
	RequestCount  int64                       `json:"request_count"`
	SuccessCount  int64                       `json:"success_count"`
	FailureCount  int64                       `json:"failure_count"`
	SuccessRate   float64                     `json:"success_rate"`
	Status        string                      `json:"status"`
	Buckets       []AccountAvailabilityBucket `json:"buckets"`
}

// AccountCapacityPatch updates per-account capacity fields.
type AccountCapacityPatch struct {
	BaseRPM                  *int `json:"base_rpm,omitempty"`
	ConcurrencyLimit         *int `json:"concurrency_limit,omitempty"`
	MaxSessions              *int `json:"max_sessions,omitempty"`
	StickyConcurrencyReserve *int `json:"sticky_concurrency_reserve,omitempty"`
}

// ClaudeCodeAccountPool is one isolated group of Claude Code OAuth accounts.
type ClaudeCodeAccountPool struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	Description         string              `json:"description,omitempty"`
	Enabled             bool                `json:"enabled"`
	IsDefault           bool                `json:"is_default"`
	HasConfigOverride   bool                `json:"has_config_override"`
	ConfigOverrideCount int                 `json:"config_override_count"`
	ArchivedAt          *time.Time          `json:"archived_at,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
	Summary             *AccountPoolSummary `json:"summary,omitempty"`
	configJSON          string
}

// AccountPoolSummary is the compact management overview for one pool.
type AccountPoolSummary struct {
	AccountCount         int                  `json:"account_count"`
	HealthyAccountCount  int                  `json:"healthy_account_count"`
	APIKeyCount          int                  `json:"api_key_count"`
	RequestCount         int64                `json:"request_count"`
	AttemptCount         int64                `json:"attempt_count"`
	SuccessRate          float64              `json:"success_rate"`
	RawTotalTokens       int64                `json:"raw_total_tokens"`
	EstimatedCost        float64              `json:"estimated_cost"`
	UnpricedRequestCount int64                `json:"unpriced_request_count"`
	PricingCoverage      float64              `json:"pricing_coverage"`
	Health               PoolHealthSummary    `json:"health"`
	ModelCapacity        ModelCapacitySummary `json:"model_capacity"`
}

// ModelCapacitySummary contains relative capacity for the fixed model families shown by the console.
type ModelCapacitySummary struct {
	Sonnet ModelCapacityItem `json:"sonnet"`
	Opus   ModelCapacityItem `json:"opus"`
	Fable  ModelCapacityItem `json:"fable"`
}

// ModelCapacityItem summarizes truthful, measured relative quota for one model family.
type ModelCapacityItem struct {
	AccountCount          int        `json:"account_count"`
	EligibleCount         int        `json:"eligible_count"`
	RoutableCount         int        `json:"routable_count"`
	MeasuredCount         int        `json:"measured_count"`
	ExhaustedCount        int        `json:"exhausted_count"`
	StaleCount            int        `json:"stale_count"`
	UnknownCount          int        `json:"unknown_count"`
	ExactCount            int        `json:"exact_count"`
	SharedCount           int        `json:"shared_count"`
	ObservedCount         int        `json:"observed_count"`
	AverageHeadroom       *float64   `json:"average_headroom,omitempty"`
	HeadroomEquivalent    float64    `json:"headroom_equivalent"`
	Coverage              float64    `json:"coverage"`
	LatestObservationTime *time.Time `json:"latest_observation_time,omitempty"`
}

// PoolHealthSummary is an explainable, observational account-pool health score.
type PoolHealthSummary struct {
	Score      *float64                       `json:"score,omitempty"`
	Status     string                         `json:"status"`
	Confidence float64                        `json:"confidence"`
	Components map[string]PoolHealthComponent `json:"components"`
	Issues     []PoolHealthIssue              `json:"issues"`
	AsOf       time.Time                      `json:"as_of"`
}

// PoolHealthComponent describes one weighted score input.
type PoolHealthComponent struct {
	Score           *float64 `json:"score,omitempty"`
	BaseWeight      float64  `json:"base_weight"`
	EffectiveWeight float64  `json:"effective_weight"`
	Coverage        float64  `json:"coverage"`
	SampleCount     int64    `json:"sample_count"`
}

// PoolHealthIssue is a redacted aggregate issue; it never contains raw upstream errors.
type PoolHealthIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Count    int    `json:"count,omitempty"`
	Model    string `json:"model,omitempty"`
}

// PoolHealthDistribution counts active account pools by health state.
type PoolHealthDistribution struct {
	Healthy     int `json:"healthy"`
	Attention   int `json:"attention"`
	Critical    int `json:"critical"`
	Unavailable int `json:"unavailable"`
	Paused      int `json:"paused"`
	Empty       int `json:"empty"`
}

// AccountPoolOperationalInsights is the batched live view used by pool summaries and stats.
type AccountPoolOperationalInsights struct {
	AccountCount        int
	HealthyAccountCount int
	APIKeyCount         int
	AvailableAccounts   int
	CoolingAccounts     int
	InFlight            int64
	RPMUsed             int
	RPMLimit            int
	ActiveSessions      int
	MaxSessions         int
	Health              PoolHealthSummary
	ModelCapacity       ModelCapacitySummary
}

// AccountPoolInsightsSnapshot contains per-pool and enabled-pool aggregate insights.
type AccountPoolInsightsSnapshot struct {
	Pools        map[string]AccountPoolOperationalInsights
	Global       AccountPoolOperationalInsights
	Distribution PoolHealthDistribution
}

// ClaudeCodeAccountPoolPatch updates editable pool fields.
type ClaudeCodeAccountPoolPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// ClaudeCodePoolAPIKey is the safe management view of a pool-bound API key.
type ClaudeCodePoolAPIKey struct {
	ID              string            `json:"id"`
	PoolID          string            `json:"pool_id"`
	Name            string            `json:"name"`
	KeyPrefix       string            `json:"key_prefix"`
	SecretAvailable bool              `json:"secret_available"`
	Enabled         bool              `json:"enabled"`
	ExpiresAt       *time.Time        `json:"-"`
	RevokedAt       *time.Time        `json:"revoked_at,omitempty"`
	LastUsedAt      *time.Time        `json:"last_used_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Usage           *UsageSummaryItem `json:"usage,omitempty"`
}

// ClaudeCodePoolAPIKeyPatch updates non-secret API key fields.
type ClaudeCodePoolAPIKeyPatch struct {
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

// ClaudeCodePoolAPIKeyCredential contains an explicitly requested key secret.
type ClaudeCodePoolAPIKeyCredential struct {
	Item   ClaudeCodePoolAPIKey `json:"item"`
	Secret string               `json:"secret"`
}

// AccountModelStatus tracks model-level health and cooling for one account.
type AccountModelStatus struct {
	AccountID           string     `json:"account_id"`
	Model               string     `json:"model"`
	Status              string     `json:"status"`
	SuccessCount        int64      `json:"success_count"`
	FailureCount        int64      `json:"failure_count"`
	RateLimitCount      int64      `json:"rate_limit_count"`
	OverloadCount       int64      `json:"overload_count"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	CoolingUntil        *time.Time `json:"cooling_until,omitempty"`
	LastStatusCode      int        `json:"last_status_code"`
	LastError           string     `json:"last_error,omitempty"`
	LastTestAt          *time.Time `json:"last_test_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// RoutingEvent records one scheduling/execution decision for account-pool observability.
type RoutingEvent struct {
	ID              int64     `json:"id,omitempty"`
	PoolID          string    `json:"pool_id"`
	APIKeyID        string    `json:"api_key_id,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	AccountID       string    `json:"account_id,omitempty"`
	AuthID          string    `json:"auth_id,omitempty"`
	Model           string    `json:"model,omitempty"`
	RequestedModel  string    `json:"requested_model,omitempty"`
	ProxyResourceID string    `json:"proxy_resource_id,omitempty"`
	Sticky          bool      `json:"sticky"`
	SessionKey      string    `json:"session_key,omitempty"`
	CapacityUsed    int       `json:"capacity_used,omitempty"`
	CapacityLimit   int       `json:"capacity_limit,omitempty"`
	InFlight        int64     `json:"in_flight,omitempty"`
	Concurrency     int       `json:"concurrency_limit,omitempty"`
	RPMUsed         int       `json:"rpm_used,omitempty"`
	RPMLimit        int       `json:"rpm_limit,omitempty"`
	Attempt         int       `json:"attempt,omitempty"`
	SwitchCount     int       `json:"switch_count,omitempty"`
	WaitMS          int64     `json:"wait_ms,omitempty"`
	AffinityMode    string    `json:"affinity_mode,omitempty"`
	PrimaryHit      bool      `json:"primary_hit"`
	BackupLane      bool      `json:"backup_lane"`
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// UsageLedgerEntry stores a lightweight request accounting row.
type UsageLedgerEntry struct {
	ID                  int64     `json:"id,omitempty"`
	PoolID              string    `json:"pool_id"`
	APIKeyID            string    `json:"api_key_id,omitempty"`
	RequestID           string    `json:"request_id,omitempty"`
	APIKeyPreview       string    `json:"api_key_preview,omitempty"`
	AccountID           string    `json:"account_id,omitempty"`
	AuthID              string    `json:"auth_id,omitempty"`
	Model               string    `json:"model,omitempty"`
	RequestedModel      string    `json:"requested_model,omitempty"`
	StatusCode          int       `json:"status_code,omitempty"`
	InputTokens         int64     `json:"input_tokens,omitempty"`
	OutputTokens        int64     `json:"output_tokens,omitempty"`
	CacheReadTokens     int64     `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64     `json:"cache_creation_tokens,omitempty"`
	CacheCreation5m     int64     `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1h     int64     `json:"cache_creation_1h_tokens,omitempty"`
	RawInputTokens      int64     `json:"raw_input_tokens,omitempty"`
	RawTotalTokens      int64     `json:"raw_total_tokens,omitempty"`
	PriceVersionID      int64     `json:"price_version_id,omitempty"`
	PriceModelPattern   string    `json:"price_model_pattern,omitempty"`
	PricingStatus       string    `json:"pricing_status"`
	EstimatedCost       float64   `json:"estimated_cost,omitempty"`
	Success             bool      `json:"success"`
	CreatedAt           time.Time `json:"created_at"`
}

// UsageSummary is the account-pool usage ledger aggregate shown in the console.
type UsageSummary struct {
	WindowSeconds        int64              `json:"window_seconds"`
	RequestCount         int64              `json:"request_count"`
	AttemptCount         int64              `json:"attempt_count"`
	SuccessCount         int64              `json:"success_count"`
	FailureCount         int64              `json:"failure_count"`
	SuccessRate          float64            `json:"success_rate"`
	InputTokens          int64              `json:"input_tokens"`
	OutputTokens         int64              `json:"output_tokens"`
	CacheReadTokens      int64              `json:"cache_read_tokens"`
	CacheCreationTokens  int64              `json:"cache_creation_tokens"`
	CacheCreation5m      int64              `json:"cache_creation_5m_tokens"`
	CacheCreation1h      int64              `json:"cache_creation_1h_tokens"`
	RawInputTokens       int64              `json:"raw_input_tokens"`
	RawTotalTokens       int64              `json:"raw_total_tokens"`
	EstimatedCost        float64            `json:"estimated_cost"`
	UnpricedRequestCount int64              `json:"unpriced_request_count"`
	PricingCoverage      float64            `json:"pricing_coverage"`
	ByAccount            []UsageSummaryItem `json:"by_account"`
	ByPool               []UsageSummaryItem `json:"by_pool"`
	ByAPIKey             []UsageSummaryItem `json:"by_api_key"`
	ByModel              []UsageSummaryItem `json:"by_model"`
	ByRequestedModel     []UsageSummaryItem `json:"by_requested_model"`
	Recent               []UsageLedgerEntry `json:"recent"`
}

// UsageSummaryItem is one aggregate bucket in the usage ledger summary.
type UsageSummaryItem struct {
	Key                  string  `json:"key"`
	RequestCount         int64   `json:"request_count"`
	AttemptCount         int64   `json:"attempt_count"`
	SuccessCount         int64   `json:"success_count"`
	FailureCount         int64   `json:"failure_count"`
	SuccessRate          float64 `json:"success_rate"`
	InputTokens          int64   `json:"input_tokens"`
	OutputTokens         int64   `json:"output_tokens"`
	CacheReadTokens      int64   `json:"cache_read_tokens"`
	CacheCreationTokens  int64   `json:"cache_creation_tokens"`
	CacheCreation5m      int64   `json:"cache_creation_5m_tokens"`
	CacheCreation1h      int64   `json:"cache_creation_1h_tokens"`
	RawInputTokens       int64   `json:"raw_input_tokens"`
	RawTotalTokens       int64   `json:"raw_total_tokens"`
	EstimatedCost        float64 `json:"estimated_cost"`
	UnpricedRequestCount int64   `json:"unpriced_request_count"`
	PricingCoverage      float64 `json:"pricing_coverage"`
}

// UsageQuery scopes account-pool usage aggregation.
type UsageQuery struct {
	Window    time.Duration
	AllTime   bool
	PoolID    string
	AccountID string
	APIKeyID  string
	Model     string
	Limit     int
	Offset    int
}

// ModelPrice is one immutable model or prefix-pattern price snapshot.
type ModelPrice struct {
	VersionID              int64     `json:"version_id"`
	Revision               int       `json:"revision"`
	ModelPattern           string    `json:"model_pattern"`
	InputPerMillion        float64   `json:"input_per_million"`
	OutputPerMillion       float64   `json:"output_per_million"`
	CacheWrite5mPerMillion float64   `json:"cache_write_5m_per_million"`
	CacheWrite1hPerMillion float64   `json:"cache_write_1h_per_million"`
	CacheReadPerMillion    float64   `json:"cache_read_per_million"`
	CreatedAt              time.Time `json:"created_at"`
}

// ModelPriceVersion is an immutable global model-price revision.
type ModelPriceVersion struct {
	ID        int64        `json:"id"`
	Revision  int          `json:"revision"`
	Source    string       `json:"source"`
	Note      string       `json:"note,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	Prices    []ModelPrice `json:"prices"`
}

// ModelPriceUpdate replaces or removes one pattern in a new revision.
type ModelPriceUpdate struct {
	ModelPattern           string  `json:"model_pattern"`
	InputPerMillion        float64 `json:"input_per_million"`
	OutputPerMillion       float64 `json:"output_per_million"`
	CacheWrite5mPerMillion float64 `json:"cache_write_5m_per_million"`
	CacheWrite1hPerMillion float64 `json:"cache_write_1h_per_million"`
	CacheReadPerMillion    float64 `json:"cache_read_per_million"`
	Remove                 bool    `json:"remove,omitempty"`
}

// AccountPoolStats is the unified management view for Claude Code account-pool operations.
type AccountPoolStats struct {
	WindowSeconds          int64                   `json:"window_seconds"`
	AccountCount           int                     `json:"account_count"`
	AvailableAccounts      int                     `json:"available_accounts"`
	CoolingAccounts        int                     `json:"cooling_accounts"`
	InFlight               int64                   `json:"in_flight"`
	RPMUsed                int                     `json:"rpm_used"`
	RPMLimit               int                     `json:"rpm_limit"`
	ActiveAffinityKeys     int                     `json:"active_affinity_keys"`
	WarmLanes              int                     `json:"warm_lanes"`
	RequestCount           int64                   `json:"request_count"`
	AttemptCount           int64                   `json:"attempt_count"`
	SuccessCount           int64                   `json:"success_count"`
	FailureCount           int64                   `json:"failure_count"`
	SuccessRate            float64                 `json:"success_rate"`
	RealCacheRatio         float64                 `json:"real_cache_ratio"`
	InputTokens            int64                   `json:"input_tokens"`
	OutputTokens           int64                   `json:"output_tokens"`
	CacheReadTokens        int64                   `json:"cache_read_tokens"`
	CacheCreationTokens    int64                   `json:"cache_creation_tokens"`
	RawInputTokens         int64                   `json:"raw_input_tokens"`
	RawTotalTokens         int64                   `json:"raw_total_tokens"`
	EstimatedCost          float64                 `json:"estimated_cost"`
	UnpricedRequestCount   int64                   `json:"unpriced_request_count"`
	PricingCoverage        float64                 `json:"pricing_coverage"`
	LocalRejectCount       int64                   `json:"local_reject_count"`
	RecentErrors           []RoutingEvent          `json:"recent_errors,omitempty"`
	Health                 PoolHealthSummary       `json:"health"`
	ModelCapacity          ModelCapacitySummary    `json:"model_capacity"`
	PoolHealthDistribution *PoolHealthDistribution `json:"pool_health_distribution,omitempty"`
}

// UsageCalibration stores prompt/profile overhead calibration for one model/profile pair.
type UsageCalibration struct {
	Model              string     `json:"model"`
	ProfileFingerprint string     `json:"profile_fingerprint"`
	OverheadTokens     int64      `json:"overhead_tokens"`
	Status             string     `json:"status"`
	CheckedAt          *time.Time `json:"checked_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at,omitempty"`
}

// UsageCalibrationView includes the effective overhead currently used by runtime.
type UsageCalibrationView struct {
	UsageCalibration
	EffectiveOverheadTokens int64 `json:"effective_overhead_tokens"`
	Estimated               bool  `json:"estimated"`
}

// ProxyResourceSeed is a YAML-friendly proxy initializer.
type ProxyResourceSeed struct {
	Name     string   `yaml:"name,omitempty" json:"name,omitempty"`
	ProxyURL string   `yaml:"proxy-url" json:"proxy_url"`
	ExitIP   string   `yaml:"exit-ip,omitempty" json:"exit_ip,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Note     string   `yaml:"note,omitempty" json:"note,omitempty"`
}

// ProxyResource is one proxy row in the global proxy pool.
type ProxyResource struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	ProxyURL            string     `json:"proxy_url"`
	ProxyURLPreview     string     `json:"proxy_url_preview,omitempty"`
	ExitIP              string     `json:"exit_ip"`
	Enabled             bool       `json:"enabled"`
	HealthStatus        string     `json:"health_status"`
	LatencyMS           int64      `json:"latency_ms"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	BoundAccountID      string     `json:"bound_account_id,omitempty"`
	BoundAccountEmail   string     `json:"bound_account_email,omitempty"`
	Reserved            bool       `json:"reserved"`
	ReservedUntil       *time.Time `json:"reserved_until,omitempty"`
	Tags                []string   `json:"tags"`
	Note                string     `json:"note,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ProxyReservation is a short-lived exclusive hold used during credential acquisition.
type ProxyReservation struct {
	ProxyResourceID string    `json:"proxy_resource_id"`
	OwnerID         string    `json:"owner_id"`
	ItemID          string    `json:"item_id"`
	Purpose         string    `json:"purpose"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ClaudeCodeAccount is one OAuth-backed Claude Code account row.
type ClaudeCodeAccount struct {
	ID                   string                      `json:"id"`
	PoolID               string                      `json:"pool_id"`
	AuthID               string                      `json:"auth_id"`
	CloakUserID          string                      `json:"cloak_user_id,omitempty"`
	Email                string                      `json:"email"`
	HasAuthData          bool                        `json:"has_auth_data"`
	TokenExpiresAt       *time.Time                  `json:"token_expires_at,omitempty"`
	Enabled              bool                        `json:"enabled"`
	Schedulable          bool                        `json:"schedulable"`
	HealthStatus         string                      `json:"health_status"`
	EffectiveSchedulable bool                        `json:"effective_schedulable"`
	BlockedUntil         *time.Time                  `json:"blocked_until,omitempty"`
	BlockedReason        string                      `json:"blocked_reason,omitempty"`
	LastHealthCheckAt    *time.Time                  `json:"last_health_check_at,omitempty"`
	NextHealthCheckAt    *time.Time                  `json:"next_health_check_at,omitempty"`
	QuotaSource          string                      `json:"quota_source,omitempty"`
	QuotaFreshness       string                      `json:"quota_freshness"`
	Headroom             *float64                    `json:"headroom,omitempty"`
	QuotaBand            string                      `json:"quota_band"`
	SharedQuotaBand      string                      `json:"shared_quota_band"`
	QuotaWindow          string                      `json:"quota_window,omitempty"`
	QuotaResetAt         *time.Time                  `json:"quota_reset_at,omitempty"`
	QuotaWindowStates    []QuotaWindowState          `json:"quota_window_states"`
	AffinityBindings     int                         `json:"affinity_bindings"`
	Priority             int                         `json:"priority"`
	ProxyResourceID      string                      `json:"proxy_resource_id,omitempty"`
	Proxy                *ProxyResource              `json:"proxy,omitempty"`
	Note                 string                      `json:"note,omitempty"`
	ExcludedModels       []string                    `json:"excluded_models"`
	Quota                *AccountQuota               `json:"quota,omitempty"`
	Capacity             *AccountCapacityConfig      `json:"capacity,omitempty"`
	RuntimeCapacity      *AccountRuntimeCapacity     `json:"runtime_capacity,omitempty"`
	Availability         *AccountAvailabilitySummary `json:"availability,omitempty"`
	ModelStatuses        []AccountModelStatus        `json:"model_statuses,omitempty"`
	Usage                *UsageSummaryItem           `json:"usage,omitempty"`
	TestStatus           string                      `json:"test_status,omitempty"`
	ConsecutiveFailures  int                         `json:"consecutive_failures"`
	LastTestAt           *time.Time                  `json:"last_test_at,omitempty"`
	LastError            string                      `json:"last_error,omitempty"`
	CreatedAt            time.Time                   `json:"created_at"`
	UpdatedAt            time.Time                   `json:"updated_at"`
}

// AccountQuota is the latest Claude OAuth usage snapshot for one account.
type AccountQuota struct {
	AccountID string             `json:"account_id,omitempty"`
	Status    string             `json:"status"`
	Windows   []QuotaWindow      `json:"windows"`
	CheckedAt *time.Time         `json:"checked_at,omitempty"`
	LastError string             `json:"last_error,omitempty"`
	RawJSON   string             `json:"raw_json,omitempty"`
	Source    string             `json:"source,omitempty"`
	Probe     *AccountQuotaProbe `json:"probe,omitempty"`
}

// AccountQuotaProbe is a safe summary of the latest OAuth usage transport.
// It intentionally excludes credentials, request identity, proxy URLs, and response bodies.
type AccountQuotaProbe struct {
	RequestedAt      time.Time `json:"requested_at"`
	ProfileRevision  string    `json:"profile_revision"`
	TransportProfile string    `json:"transport_profile"`
	ProxyMode        string    `json:"proxy_mode"`
	ProxyResourceID  string    `json:"proxy_resource_id,omitempty"`
	StatusCode       int       `json:"status_code"`
}

// QuotaWindow is one usage window returned by Anthropic's OAuth usage endpoint.
type QuotaWindow struct {
	Key                 string     `json:"key"`
	Name                string     `json:"name"`
	UsedPercent         float64    `json:"used_percent"`
	RemainPercent       float64    `json:"remain_percent"`
	UtilizationKnown    *bool      `json:"utilization_known,omitempty"`
	ResetsAt            *time.Time `json:"resets_at,omitempty"`
	MonthlyLimit        *float64   `json:"monthly_limit,omitempty"`
	UsedCredits         *float64   `json:"used_credits,omitempty"`
	Status              string     `json:"status,omitempty"`
	Remaining           *float64   `json:"remaining,omitempty"`
	RepresentativeClaim string     `json:"representative_claim,omitempty"`
	Source              string     `json:"source,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

// QuotaWindowState is a derived management view of one canonical quota window.
// It is never persisted or used by routing.
type QuotaWindowState struct {
	Key              string     `json:"key"`
	Name             string     `json:"name"`
	Confidence       string     `json:"confidence"`
	Freshness        string     `json:"freshness"`
	Source           string     `json:"source,omitempty"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	SharedFrom       string     `json:"shared_from,omitempty"`
	UtilizationKnown bool       `json:"utilization_known"`
	UsedPercent      *float64   `json:"used_percent,omitempty"`
	RemainPercent    *float64   `json:"remain_percent,omitempty"`
	ResetsAt         *time.Time `json:"resets_at,omitempty"`
	Status           string     `json:"status,omitempty"`
	Remaining        *float64   `json:"remaining,omitempty"`
	Exhausted        bool       `json:"exhausted"`
}

// ClaudeCodeModel maps an external model name to an upstream Claude model.
type ClaudeCodeModel struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Alias     string      `json:"alias"`
	Enabled   bool        `json:"enabled"`
	Source    string      `json:"source"`
	Note      string      `json:"note,omitempty"`
	Price     *ModelPrice `json:"price,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// ClaudeCodeModelPatch updates one model mapping.
type ClaudeCodeModelPatch struct {
	Name    *string `json:"name,omitempty"`
	Alias   *string `json:"alias,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
	Source  *string `json:"source,omitempty"`
	Note    *string `json:"note,omitempty"`
}

// AccountOverlay contains the fields needed to patch a runtime auth.
type AccountOverlay struct {
	Account ClaudeCodeAccount
	Proxy   *ProxyResource
}

// ProxyPatch updates one proxy resource.
type ProxyPatch struct {
	Name     *string   `json:"name,omitempty"`
	ProxyURL *string   `json:"proxy_url,omitempty"`
	ExitIP   *string   `json:"exit_ip,omitempty"`
	Enabled  *bool     `json:"enabled,omitempty"`
	Tags     *[]string `json:"tags,omitempty"`
	Note     *string   `json:"note,omitempty"`
}

// AccountPatch updates one Claude Code account.
type AccountPatch struct {
	Email           *string   `json:"email,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
	Schedulable     *bool     `json:"schedulable,omitempty"`
	Priority        *int      `json:"priority,omitempty"`
	ProxyResourceID *string   `json:"proxy_resource_id,omitempty"`
	Note            *string   `json:"note,omitempty"`
	ExcludedModels  *[]string `json:"excluded_models,omitempty"`
}

// ImportResult summarizes a batch proxy import.
type ImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// HealthResult is returned by manual and scheduled proxy tests.
type HealthResult struct {
	ID                  string     `json:"id"`
	HealthStatus        string     `json:"health_status"`
	LatencyMS           int64      `json:"latency_ms"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

// ConsoleSummary gives the frontend compact counters for top-level cards.
type ConsoleSummary struct {
	ProxyTotal     int `json:"proxy_total"`
	ProxyHealthy   int `json:"proxy_healthy"`
	ProxyUnknown   int `json:"proxy_unknown"`
	ProxyUnhealthy int `json:"proxy_unhealthy"`
	ProxyDisabled  int `json:"proxy_disabled"`
	ProxyBound     int `json:"proxy_bound"`
	AccountTotal   int `json:"account_total"`
	AccountEnabled int `json:"account_enabled"`
	AccountBound   int `json:"account_bound"`
}

func normalizeHealthStatus(status string, enabled bool) string {
	if !enabled {
		return HealthDisabled
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case HealthHealthy:
		return HealthHealthy
	case HealthUnhealthy:
		return HealthUnhealthy
	default:
		return HealthUnknown
	}
}
