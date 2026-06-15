// Package resourcepool manages Claude Code OAuth accounts and proxy resources.
package resourcepool

import (
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const (
	DefaultConfigFileName           = "resource-pools.yaml"
	DefaultDBFileName               = "resource-pools.db"
	DefaultClaudeCodeProfileVersion = "2.1.177"
	DefaultCleanInputOverheadTokens = 1909

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
	HealthDisabled  = "disabled"

	AttrClaudeOAuthPool = "claude_oauth_pool"
	AttrAccountID       = "claude_code_account_id"
	AttrProxyResourceID = "proxy_resource_id"

	AttrProfileVersion             = "claude_code_profile_version"
	AttrProfileUserAgent           = "claude_code_profile_user_agent"
	AttrProfileHeadersJSON         = "claude_code_profile_headers_json"
	AttrProfileBetasJSON           = "claude_code_profile_betas_json"
	AttrProfileSystemPrompt        = "claude_code_profile_system_prompt"
	AttrProfileBillingBlockEnabled = "claude_code_profile_billing_block_enabled"
	AttrProfileMetadataUserIDMode  = "claude_code_profile_metadata_user_id_mode"

	AttrCapacityBaseRPM          = "claude_code_capacity_base_rpm"
	AttrCapacityConcurrencyLimit = "claude_code_capacity_concurrency_limit"
	AttrCapacityMaxSessions      = "claude_code_capacity_max_sessions"
	AttrCapacityStickyBuffer     = "claude_code_capacity_sticky_buffer"

	AttrCleanInputTokens          = "claude_code_clean_input_tokens"
	AttrCleanInputDefaultOverhead = "claude_code_clean_input_default_overhead_tokens"
	AttrProfileFingerprint        = "claude_code_profile_fingerprint"
	AttrUsageOverheadsJSON        = "claude_code_usage_overheads_json"
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
	Enabled                 *bool                            `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	PureMode                *bool                            `yaml:"pure-mode,omitempty" json:"pure_mode,omitempty"`
	Cloak                   *config.CloakConfig              `yaml:"cloak,omitempty" json:"cloak,omitempty"`
	Usage                   ClaudeCodeUsageConfig            `yaml:"usage,omitempty" json:"usage,omitempty"`
	VirtualCache            claudeapipool.VirtualCacheConfig `yaml:"virtual-cache,omitempty" json:"virtual_cache,omitempty"`
	Routing                 claudeapipool.RoutingConfig      `yaml:"routing,omitempty" json:"routing,omitempty"`
	PerAccountRPM           int                              `yaml:"per-account-rpm,omitempty" json:"per_account_rpm,omitempty"`
	PerAccountConcurrency   int                              `yaml:"per-account-concurrency,omitempty" json:"per_account_concurrency,omitempty"`
	MaxSwitches             int                              `yaml:"max-switches,omitempty" json:"max_switches,omitempty"`
	SwitchDelayMS           int                              `yaml:"switch-delay-ms,omitempty" json:"switch_delay_ms,omitempty"`
	RateLimitCooldownMS     int                              `yaml:"rate-limit-cooldown-ms,omitempty" json:"rate_limit_cooldown_ms,omitempty"`
	RateLimitMaxCooldownMS  int                              `yaml:"rate-limit-max-cooldown-ms,omitempty" json:"rate_limit_max_cooldown_ms,omitempty"`
	OverloadCooldownMS      int                              `yaml:"overload-cooldown-ms,omitempty" json:"overload_cooldown_ms,omitempty"`
	OverloadMaxCooldownMS   int                              `yaml:"overload-max-cooldown-ms,omitempty" json:"overload_max_cooldown_ms,omitempty"`
	SameAccountRetry429     int                              `yaml:"same-account-retry-429,omitempty" json:"same_account_retry_429,omitempty"`
	SameAccountRetry529     int                              `yaml:"same-account-retry-529,omitempty" json:"same_account_retry_529,omitempty"`
	SameAccountRetryDelayMS int                              `yaml:"same-account-retry-delay-ms,omitempty" json:"same_account_retry_delay_ms,omitempty"`
	SessionAffinity         bool                             `yaml:"session-affinity,omitempty" json:"session_affinity,omitempty"`
	SessionAffinityTTL      string                           `yaml:"session-affinity-ttl,omitempty" json:"session_affinity_ttl,omitempty"`
}

// ClaudeCodeUsageConfig controls downstream-only usage display behavior.
type ClaudeCodeUsageConfig struct {
	CleanInputTokens           *bool `yaml:"clean-input-tokens,omitempty" json:"clean_input_tokens,omitempty"`
	SystemPromptOverheadTokens int64 `yaml:"system-prompt-overhead-tokens,omitempty" json:"system_prompt_overhead_tokens,omitempty"`
}

// EffectiveClaudeCodePoolConfig is the management/API view of Claude Code pool settings.
type EffectiveClaudeCodePoolConfig struct {
	Enabled      bool                                      `json:"enabled"`
	PureMode     bool                                      `json:"pure_mode"`
	Cloak        EffectiveCloakConfig                      `json:"cloak"`
	Usage        EffectiveClaudeCodeUsageConfig            `json:"usage"`
	VirtualCache claudeapipool.EffectiveVirtualCacheConfig `json:"virtual_cache"`
	Routing      claudeapipool.EffectiveRoutingConfig      `json:"routing"`
}

// EffectiveClaudeCodeUsageConfig is the normalized management/API view of usage display behavior.
type EffectiveClaudeCodeUsageConfig struct {
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
	Version             string            `yaml:"version,omitempty" json:"version,omitempty"`
	UserAgent           string            `yaml:"user-agent,omitempty" json:"user_agent,omitempty"`
	Headers             map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Betas               []string          `yaml:"betas,omitempty" json:"betas,omitempty"`
	SystemPrompt        string            `yaml:"system-prompt,omitempty" json:"system_prompt,omitempty"`
	BillingBlockEnabled *bool             `yaml:"billing-block-enabled,omitempty" json:"billing_block_enabled,omitempty"`
	MetadataUserIDMode  string            `yaml:"metadata-user-id-mode,omitempty" json:"metadata_user_id_mode,omitempty"`
	UpdatedFrom         string            `yaml:"updated-from,omitempty" json:"updated_from,omitempty"`
	UpdatedAt           *time.Time        `yaml:"updated-at,omitempty" json:"updated_at,omitempty"`
	Locked              bool              `yaml:"locked,omitempty" json:"locked,omitempty"`
	SystemPromptMode    string            `yaml:"system-prompt-mode,omitempty" json:"system_prompt_mode,omitempty"`
}

// EffectiveClaudeCodeProfileConfig is the normalized runtime request-shape profile.
type EffectiveClaudeCodeProfileConfig struct {
	Version             string            `json:"version"`
	UserAgent           string            `json:"user_agent"`
	Headers             map[string]string `json:"headers"`
	Betas               []string          `json:"betas"`
	SystemPrompt        string            `json:"system_prompt"`
	BillingBlockEnabled bool              `json:"billing_block_enabled"`
	MetadataUserIDMode  string            `json:"metadata_user_id_mode"`
	UpdatedFrom         string            `json:"updated_from,omitempty"`
	UpdatedAt           *time.Time        `json:"updated_at,omitempty"`
	Locked              bool              `json:"locked"`
	SystemPromptMode    string            `json:"system_prompt_mode"`
}

// AccountCapacityConfig is the persisted lightweight local capacity model.
type AccountCapacityConfig struct {
	AccountID        string    `json:"account_id,omitempty"`
	BaseRPM          int       `json:"base_rpm"`
	ConcurrencyLimit int       `json:"concurrency_limit"`
	MaxSessions      int       `json:"max_sessions"`
	StickyBuffer     int       `json:"sticky_buffer"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

// AccountRuntimeCapacity is the management view for current local pressure.
type AccountRuntimeCapacity struct {
	AccountID        string `json:"account_id,omitempty"`
	BaseRPM          int    `json:"base_rpm"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
	MaxSessions      int    `json:"max_sessions"`
	StickyBuffer     int    `json:"sticky_buffer"`
	CapacityUsed     int    `json:"capacity_used"`
	CapacityLimit    int    `json:"capacity_limit"`
	InFlight         int64  `json:"in_flight"`
	RPMUsed          int    `json:"rpm_used"`
	RPMLimit         int    `json:"rpm_limit"`
	StickySessions   int    `json:"sticky_sessions"`
	BufferUsed       int    `json:"buffer_used"`
	Cooling          bool   `json:"cooling"`
	CoolingUntil     string `json:"cooling_until,omitempty"`
	Unavailable      bool   `json:"unavailable"`
}

// AccountCapacityPatch updates per-account capacity fields.
type AccountCapacityPatch struct {
	BaseRPM          *int `json:"base_rpm,omitempty"`
	ConcurrencyLimit *int `json:"concurrency_limit,omitempty"`
	MaxSessions      *int `json:"max_sessions,omitempty"`
	StickyBuffer     *int `json:"sticky_buffer,omitempty"`
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
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// UsageLedgerEntry stores a lightweight request accounting row.
type UsageLedgerEntry struct {
	ID                  int64     `json:"id,omitempty"`
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
	EstimatedCost       float64   `json:"estimated_cost,omitempty"`
	Success             bool      `json:"success"`
	CreatedAt           time.Time `json:"created_at"`
}

// UsageSummary is the account-pool usage ledger aggregate shown in the console.
type UsageSummary struct {
	WindowSeconds       int64              `json:"window_seconds"`
	RequestCount        int64              `json:"request_count"`
	SuccessCount        int64              `json:"success_count"`
	FailureCount        int64              `json:"failure_count"`
	SuccessRate         float64            `json:"success_rate"`
	InputTokens         int64              `json:"input_tokens"`
	OutputTokens        int64              `json:"output_tokens"`
	CacheReadTokens     int64              `json:"cache_read_tokens"`
	CacheCreationTokens int64              `json:"cache_creation_tokens"`
	EstimatedCost       float64            `json:"estimated_cost"`
	ByAccount           []UsageSummaryItem `json:"by_account"`
	ByModel             []UsageSummaryItem `json:"by_model"`
	Recent              []UsageLedgerEntry `json:"recent"`
}

// UsageSummaryItem is one aggregate bucket in the usage ledger summary.
type UsageSummaryItem struct {
	Key                 string  `json:"key"`
	RequestCount        int64   `json:"request_count"`
	SuccessCount        int64   `json:"success_count"`
	FailureCount        int64   `json:"failure_count"`
	SuccessRate         float64 `json:"success_rate"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	EstimatedCost       float64 `json:"estimated_cost"`
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
	Tags                []string   `json:"tags"`
	Note                string     `json:"note,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ClaudeCodeAccount is one OAuth-backed Claude Code account row.
type ClaudeCodeAccount struct {
	ID                  string                  `json:"id"`
	AuthID              string                  `json:"auth_id"`
	CloakUserID         string                  `json:"cloak_user_id,omitempty"`
	Email               string                  `json:"email"`
	HasAuthData         bool                    `json:"has_auth_data"`
	Enabled             bool                    `json:"enabled"`
	Priority            int                     `json:"priority"`
	ProxyResourceID     string                  `json:"proxy_resource_id,omitempty"`
	Proxy               *ProxyResource          `json:"proxy,omitempty"`
	Note                string                  `json:"note,omitempty"`
	ExcludedModels      []string                `json:"excluded_models"`
	Quota               *AccountQuota           `json:"quota,omitempty"`
	Capacity            *AccountCapacityConfig  `json:"capacity,omitempty"`
	RuntimeCapacity     *AccountRuntimeCapacity `json:"runtime_capacity,omitempty"`
	ModelStatuses       []AccountModelStatus    `json:"model_statuses,omitempty"`
	TestStatus          string                  `json:"test_status,omitempty"`
	ConsecutiveFailures int                     `json:"consecutive_failures"`
	LastTestAt          *time.Time              `json:"last_test_at,omitempty"`
	LastError           string                  `json:"last_error,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

// AccountQuota is the latest Claude OAuth usage snapshot for one account.
type AccountQuota struct {
	AccountID string        `json:"account_id,omitempty"`
	Status    string        `json:"status"`
	Windows   []QuotaWindow `json:"windows"`
	CheckedAt *time.Time    `json:"checked_at,omitempty"`
	LastError string        `json:"last_error,omitempty"`
	RawJSON   string        `json:"raw_json,omitempty"`
}

// QuotaWindow is one usage window returned by Anthropic's OAuth usage endpoint.
type QuotaWindow struct {
	Key           string     `json:"key"`
	Name          string     `json:"name"`
	UsedPercent   float64    `json:"used_percent"`
	RemainPercent float64    `json:"remain_percent"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	MonthlyLimit  *float64   `json:"monthly_limit,omitempty"`
	UsedCredits   *float64   `json:"used_credits,omitempty"`
}

// ClaudeCodeModel maps an external model name to an upstream Claude model.
type ClaudeCodeModel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Alias     string    `json:"alias"`
	Enabled   bool      `json:"enabled"`
	Source    string    `json:"source"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
