// Package claudeapipool loads and normalizes the Claude API pool.
package claudeapipool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultFileName is the fixed YAML import/export filename.
	DefaultFileName = "claude-api-pool.yaml"
	// DefaultDBFileName is the fixed SQLite-backed pool filename.
	DefaultDBFileName       = "claude-api-pool.db"
	VirtualCacheModeNatural = "natural"
	VirtualCacheModeForced  = "forced"
	// SimpleImportWorkspaceHeader is populated by apiKey-----workspaceId imports.
	SimpleImportWorkspaceHeader = "anthropic-workspace-id"

	AttrPool       = "claude_api_pool"
	AttrPosition   = "claude_api_pool_position"
	AttrItemHash   = "claude_api_pool_item_hash"
	AttrModelsJSON = "claude_api_pool_models_json"
	AttrCCHSigning = "claude_api_pool_experimental_cch_signing"
	AttrPureMode   = "claude_api_pool_pure_mode"

	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	StatusCooling  = "cooling"
)

// File is the YAML document stored in claude-api-pool.yaml.
type File struct {
	Version      int                  `yaml:"version" json:"version"`
	PureMode     bool                 `yaml:"pure-mode,omitempty" json:"pure-mode,omitempty"`
	VirtualCache VirtualCacheConfig   `yaml:"virtual-cache,omitempty" json:"virtual-cache,omitempty"`
	Routing      RoutingConfig        `yaml:"routing,omitempty" json:"routing,omitempty"`
	Defaults     Defaults             `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Models       []config.ClaudeModel `yaml:"models,omitempty" json:"models,omitempty"`
	Items        []Item               `yaml:"items" json:"items"`
}

// VirtualCacheConfig controls downstream virtual prompt-cache usage rewriting.
type VirtualCacheConfig struct {
	Enabled                 *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Mode                    string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	HitRate                 *float64 `yaml:"hit-rate,omitempty" json:"hit-rate,omitempty"`
	TargetCacheReuseRatio   *float64 `yaml:"target-cache-reuse-ratio,omitempty" json:"target-cache-reuse-ratio,omitempty"`
	MinCacheTokens          int64    `yaml:"min-cache-tokens,omitempty" json:"min-cache-tokens,omitempty"`
	MaxCacheTokens          int64    `yaml:"max-cache-tokens,omitempty" json:"max-cache-tokens,omitempty"`
	UncachedInputTokens     int64    `yaml:"uncached-input-tokens,omitempty" json:"uncached-input-tokens,omitempty"`
	ContextShrinkResetRatio *float64 `yaml:"context-shrink-reset-ratio,omitempty" json:"context-shrink-reset-ratio,omitempty"`
	MinCreationTokens       int64    `yaml:"min-creation-tokens,omitempty" json:"min-creation-tokens,omitempty"`
	MaxCreationTokens       int64    `yaml:"max-creation-tokens,omitempty" json:"max-creation-tokens,omitempty"`
}

// EffectiveVirtualCacheConfig is the fully-defaulted runtime virtual-cache policy.
type EffectiveVirtualCacheConfig struct {
	Enabled                 bool    `json:"enabled"`
	Mode                    string  `json:"mode"`
	HitRate                 float64 `json:"hit_rate"`
	TargetCacheReuseRatio   float64 `json:"target_cache_reuse_ratio"`
	ReadScale               float64 `json:"-"`
	MinCacheTokens          int64   `json:"min_cache_tokens"`
	MaxCacheTokens          int64   `json:"max_cache_tokens"`
	UncachedInputTokens     int64   `json:"uncached_input_tokens"`
	ContextShrinkResetRatio float64 `json:"context_shrink_reset_ratio"`
	MinCreationTokens       int64   `json:"min_creation_tokens"`
	MaxCreationTokens       int64   `json:"max_creation_tokens"`
}

// RoutingConfig controls local pool routing pressure before an upstream call is made.
type RoutingConfig struct {
	PerAccountRPM           int  `yaml:"per-account-rpm,omitempty" json:"per-account-rpm,omitempty"`
	PerAccountConcurrency   int  `yaml:"per-account-concurrency,omitempty" json:"per-account-concurrency,omitempty"`
	MaxSwitches             int  `yaml:"max-switches,omitempty" json:"max-switches,omitempty"`
	SwitchDelayMS           int  `yaml:"switch-delay-ms,omitempty" json:"switch-delay-ms,omitempty"`
	RateLimitCooldownMS     int  `yaml:"rate-limit-cooldown-ms,omitempty" json:"rate-limit-cooldown-ms,omitempty"`
	RateLimitMaxCooldownMS  int  `yaml:"rate-limit-max-cooldown-ms,omitempty" json:"rate-limit-max-cooldown-ms,omitempty"`
	OverloadCooldownMS      int  `yaml:"overload-cooldown-ms,omitempty" json:"overload-cooldown-ms,omitempty"`
	OverloadMaxCooldownMS   int  `yaml:"overload-max-cooldown-ms,omitempty" json:"overload-max-cooldown-ms,omitempty"`
	SameAccountRetry429     int  `yaml:"same-account-retry-429,omitempty" json:"same-account-retry-429,omitempty"`
	SameAccountRetry529     int  `yaml:"same-account-retry-529,omitempty" json:"same-account-retry-529,omitempty"`
	SameAccountRetryDelayMS int  `yaml:"same-account-retry-delay-ms,omitempty" json:"same-account-retry-delay-ms,omitempty"`
	CacheAffinityEnabled    bool `yaml:"cache-affinity-enabled,omitempty" json:"cache-affinity-enabled,omitempty"`
	CacheAffinityAuto       bool `yaml:"cache-affinity-auto,omitempty" json:"cache-affinity-auto,omitempty"`
	CacheAffinityMinTokens  int  `yaml:"cache-affinity-min-cache-tokens,omitempty" json:"cache-affinity-min-cache-tokens,omitempty"`
	CacheAffinityLanes      int  `yaml:"cache-affinity-lanes,omitempty" json:"cache-affinity-lanes,omitempty"`
	CacheAffinityMaxLanes   int  `yaml:"cache-affinity-max-lanes,omitempty" json:"cache-affinity-max-lanes,omitempty"`
	CacheAffinityWaitMS     int  `yaml:"cache-affinity-wait-ms,omitempty" json:"cache-affinity-wait-ms,omitempty"`
	CacheAffinityTTLMS      int  `yaml:"cache-affinity-ttl-ms,omitempty" json:"cache-affinity-ttl-ms,omitempty"`
}

// EffectiveRoutingConfig is the fully-defaulted runtime routing policy.
type EffectiveRoutingConfig struct {
	PerAccountRPM           int  `json:"per_account_rpm"`
	PerAccountConcurrency   int  `json:"per_account_concurrency"`
	MaxSwitches             int  `json:"max_switches"`
	SwitchDelayMS           int  `json:"switch_delay_ms"`
	RateLimitCooldownMS     int  `json:"rate_limit_cooldown_ms"`
	RateLimitMaxCooldownMS  int  `json:"rate_limit_max_cooldown_ms"`
	OverloadCooldownMS      int  `json:"overload_cooldown_ms"`
	OverloadMaxCooldownMS   int  `json:"overload_max_cooldown_ms"`
	SameAccountRetry429     int  `json:"same_account_retry_429"`
	SameAccountRetry529     int  `json:"same_account_retry_529"`
	SameAccountRetryDelayMS int  `json:"same_account_retry_delay_ms"`
	CacheAffinityEnabled    bool `json:"cache_affinity_enabled"`
	CacheAffinityAuto       bool `json:"cache_affinity_auto"`
	CacheAffinityMinTokens  int  `json:"cache_affinity_min_cache_tokens"`
	CacheAffinityLanes      int  `json:"cache_affinity_lanes"`
	CacheAffinityMaxLanes   int  `json:"cache_affinity_max_lanes"`
	CacheAffinityWaitMS     int  `json:"cache_affinity_wait_ms"`
	CacheAffinityTTLMS      int  `json:"cache_affinity_ttl_ms"`
}

// Defaults contains pool-level values inherited by every item.
type Defaults struct {
	BaseURL        string            `yaml:"base-url,omitempty" json:"base-url,omitempty"`
	ProxyURL       string            `yaml:"proxy-url,omitempty" json:"proxy-url,omitempty"`
	Priority       int               `yaml:"priority,omitempty" json:"priority,omitempty"`
	DisableCooling bool              `yaml:"disable-cooling,omitempty" json:"disable-cooling,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// Item is one raw account entry. Optional scalar overrides use pointers so
// zero values can intentionally override defaults.
type Item struct {
	APIKey                 string               `yaml:"api-key" json:"api-key"`
	BaseURL                *string              `yaml:"base-url,omitempty" json:"base-url,omitempty"`
	ProxyURL               *string              `yaml:"proxy-url,omitempty" json:"proxy-url,omitempty"`
	Priority               *int                 `yaml:"priority,omitempty" json:"priority,omitempty"`
	DisableCooling         *bool                `yaml:"disable-cooling,omitempty" json:"disable-cooling,omitempty"`
	Disabled               bool                 `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Headers                map[string]string    `yaml:"headers,omitempty" json:"headers,omitempty"`
	Models                 []config.ClaudeModel `yaml:"models,omitempty" json:"models,omitempty"`
	ExcludedModels         []string             `yaml:"excluded-models,omitempty" json:"excluded-models,omitempty"`
	Cloak                  *config.CloakConfig  `yaml:"cloak,omitempty" json:"cloak,omitempty"`
	ExperimentalCCHSigning bool                 `yaml:"experimental-cch-signing,omitempty" json:"experimental-cch-signing,omitempty"`
}

// ResolvedItem is an item after defaults and inherited models are applied.
type ResolvedItem struct {
	Position int              `json:"position"`
	ItemHash string           `json:"item_hash"`
	Status   string           `json:"status"`
	PureMode bool             `json:"pure_mode"`
	Raw      Item             `json:"raw"`
	Config   config.ClaudeKey `json:"config"`
}

// RuntimeStatus holds optional runtime state for management list responses.
type RuntimeStatus struct {
	AuthID    string
	InFlight  int64
	RPMUsed   int
	RPMLimit  int
	WarmKeys  int
	Cooling   bool
	CoolingTo time.Time
	Disabled  bool
	Metrics   AccountMetricsSnapshot
}

// ItemView is returned by the management API.
type ItemView struct {
	Position       int                    `json:"position"`
	ItemHash       string                 `json:"item_hash"`
	AuthID         string                 `json:"auth_id,omitempty"`
	Status         string                 `json:"status"`
	APIKeyPreview  string                 `json:"api_key_preview"`
	BaseURL        string                 `json:"base-url"`
	ProxyURL       string                 `json:"proxy-url"`
	Priority       int                    `json:"priority"`
	DisableCooling bool                   `json:"disable-cooling"`
	Headers        map[string]string      `json:"headers,omitempty"`
	Models         []config.ClaudeModel   `json:"models,omitempty"`
	ExcludedModels []string               `json:"excluded-models,omitempty"`
	Raw            Item                   `json:"raw"`
	InFlight       int64                  `json:"in_flight"`
	RPMUsed        int                    `json:"rpm_used"`
	RPMLimit       int                    `json:"rpm_limit"`
	Cooling        bool                   `json:"cooling"`
	CoolingUntil   string                 `json:"cooling_until,omitempty"`
	WarmKeys       int                    `json:"warm_keys"`
	Metrics        AccountMetricsSnapshot `json:"metrics"`
}

// ListQuery filters and pages resolved items.
type ListQuery struct {
	Page     int
	PageSize int
	Q        string
	Status   string
	Model    string
	Runtime  map[int]RuntimeStatus
	AuthIDs  map[int]string
}

// ListResult is a DB-friendly page shape.
type ListResult struct {
	Items    []ItemView `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// MutationRef identifies one item mutation guarded by the current item hash.
type MutationRef struct {
	Position int    `json:"position"`
	ItemHash string `json:"item_hash"`
}

// ResolvePath resolves the fixed pool file beside the main config file.
func ResolvePath(configFilePath string, _ *config.Config) string {
	baseDir := "."
	if configFilePath != "" {
		baseDir = filepath.Dir(configFilePath)
	}
	return filepath.Clean(filepath.Join(baseDir, DefaultFileName))
}

// Load reads a pool file. A missing file is treated as an empty pool.
func Load(path string) (*File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &File{Version: 1}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{Version: 1}, nil
		}
		return nil, fmt.Errorf("read claude api pool: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &File{Version: 1}, nil
	}
	doc, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// Decode parses YAML or JSON pool content.
func Decode(data []byte) (*File, error) {
	var doc File
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return &File{Version: 1}, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("parse claude api pool json: %w", err)
		}
	} else if err := yaml.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("parse claude api pool yaml: %w", err)
	}
	Normalize(&doc)
	return &doc, nil
}

// Save writes the pool file using YAML.
func Save(path string, doc *File) error {
	if doc == nil {
		doc = &File{Version: 1}
	}
	Normalize(doc)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create claude api pool dir: %w", err)
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal claude api pool: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-api-pool-*.yaml")
	if err != nil {
		return fmt.Errorf("create claude api pool temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, errWrite := tmp.Write(data); errWrite != nil {
		_ = tmp.Close()
		return fmt.Errorf("write claude api pool temp file: %w", errWrite)
	}
	if errSync := tmp.Sync(); errSync != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync claude api pool temp file: %w", errSync)
	}
	if errClose := tmp.Close(); errClose != nil {
		return fmt.Errorf("close claude api pool temp file: %w", errClose)
	}
	if errRename := os.Rename(tmpName, path); errRename != nil {
		return fmt.Errorf("replace claude api pool file: %w", errRename)
	}
	return nil
}

// Normalize trims entries without changing item order.
func Normalize(doc *File) {
	if doc == nil {
		return
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	doc.VirtualCache = NormalizeVirtualCacheConfig(doc.VirtualCache)
	doc.Routing = NormalizeRoutingConfig(doc.Routing)
	doc.Defaults.BaseURL = strings.TrimSpace(doc.Defaults.BaseURL)
	doc.Defaults.ProxyURL = strings.TrimSpace(doc.Defaults.ProxyURL)
	doc.Defaults.Headers = config.NormalizeHeaders(doc.Defaults.Headers)
	doc.Models = normalizeModels(doc.Models)
	for i := range doc.Items {
		NormalizeItem(&doc.Items[i])
	}
}

// NormalizeVirtualCacheConfig clamps file-backed virtual-cache settings.
func NormalizeVirtualCacheConfig(cfg VirtualCacheConfig) VirtualCacheConfig {
	cfg.Mode = normalizeVirtualCacheMode(cfg.Mode)
	if cfg.HitRate != nil {
		rate := *cfg.HitRate
		if rate > 1 {
			rate = rate / 100
		}
		if rate < 0 {
			rate = 0
		}
		if rate > 1 {
			rate = 1
		}
		cfg.HitRate = &rate
	}
	if cfg.TargetCacheReuseRatio != nil {
		ratio := *cfg.TargetCacheReuseRatio
		if ratio > 1 {
			ratio = ratio / 100
		}
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		cfg.TargetCacheReuseRatio = &ratio
	}
	if cfg.MinCacheTokens < 0 {
		cfg.MinCacheTokens = 0
	}
	if cfg.MaxCacheTokens < 0 {
		cfg.MaxCacheTokens = 0
	}
	if cfg.UncachedInputTokens < 0 {
		cfg.UncachedInputTokens = 0
	}
	if cfg.ContextShrinkResetRatio != nil {
		ratio := *cfg.ContextShrinkResetRatio
		if ratio > 1 {
			ratio = ratio / 100
		}
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		cfg.ContextShrinkResetRatio = &ratio
	}
	if cfg.MinCreationTokens < 0 {
		cfg.MinCreationTokens = 0
	}
	if cfg.MaxCreationTokens < 0 {
		cfg.MaxCreationTokens = 0
	}
	return cfg
}

func normalizeVirtualCacheMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", VirtualCacheModeNatural:
		return VirtualCacheModeNatural
	case VirtualCacheModeForced:
		return VirtualCacheModeForced
	default:
		return VirtualCacheModeNatural
	}
}

// NormalizeRoutingConfig clamps file-backed pool routing settings.
func NormalizeRoutingConfig(cfg RoutingConfig) RoutingConfig {
	if cfg.PerAccountRPM < 0 {
		cfg.PerAccountRPM = 0
	}
	if cfg.PerAccountConcurrency < 0 {
		cfg.PerAccountConcurrency = 0
	}
	if cfg.MaxSwitches < 0 {
		cfg.MaxSwitches = 0
	}
	if cfg.SwitchDelayMS < 0 {
		cfg.SwitchDelayMS = 0
	}
	if cfg.RateLimitCooldownMS < 0 {
		cfg.RateLimitCooldownMS = 0
	}
	if cfg.RateLimitMaxCooldownMS < 0 {
		cfg.RateLimitMaxCooldownMS = 0
	}
	if cfg.OverloadCooldownMS < 0 {
		cfg.OverloadCooldownMS = 0
	}
	if cfg.OverloadMaxCooldownMS < 0 {
		cfg.OverloadMaxCooldownMS = 0
	}
	if cfg.SameAccountRetry429 < 0 {
		cfg.SameAccountRetry429 = 0
	}
	if cfg.SameAccountRetry529 < 0 {
		cfg.SameAccountRetry529 = 0
	}
	if cfg.SameAccountRetryDelayMS < 0 {
		cfg.SameAccountRetryDelayMS = 0
	}
	if cfg.CacheAffinityMinTokens < 0 {
		cfg.CacheAffinityMinTokens = 0
	}
	if cfg.CacheAffinityLanes < 0 {
		cfg.CacheAffinityLanes = 0
	}
	if cfg.CacheAffinityMaxLanes < 0 {
		cfg.CacheAffinityMaxLanes = 0
	}
	if cfg.CacheAffinityWaitMS < 0 {
		cfg.CacheAffinityWaitMS = 0
	}
	if cfg.CacheAffinityTTLMS < 0 {
		cfg.CacheAffinityTTLMS = 0
	}
	return cfg
}

// EffectiveRouting returns the runtime routing policy with defaults applied.
func EffectiveRouting(cfg RoutingConfig) EffectiveRoutingConfig {
	cfg = NormalizeRoutingConfig(cfg)
	rateLimitCooldownMS := cfg.RateLimitCooldownMS
	if rateLimitCooldownMS <= 0 {
		rateLimitCooldownMS = int(time.Second / time.Millisecond)
	}
	rateLimitMaxCooldownMS := cfg.RateLimitMaxCooldownMS
	if rateLimitMaxCooldownMS <= 0 {
		rateLimitMaxCooldownMS = int((30 * time.Minute) / time.Millisecond)
	}
	if rateLimitMaxCooldownMS < rateLimitCooldownMS {
		rateLimitMaxCooldownMS = rateLimitCooldownMS
	}
	overloadCooldownMS := cfg.OverloadCooldownMS
	if overloadCooldownMS <= 0 {
		overloadCooldownMS = int((10 * time.Second) / time.Millisecond)
	}
	overloadMaxCooldownMS := cfg.OverloadMaxCooldownMS
	if overloadMaxCooldownMS <= 0 {
		overloadMaxCooldownMS = int(time.Minute / time.Millisecond)
	}
	if overloadMaxCooldownMS < overloadCooldownMS {
		overloadMaxCooldownMS = overloadCooldownMS
	}
	sameAccountRetryDelayMS := cfg.SameAccountRetryDelayMS
	if sameAccountRetryDelayMS <= 0 {
		sameAccountRetryDelayMS = 1500
	}
	cacheAffinityMinTokens := cfg.CacheAffinityMinTokens
	if cacheAffinityMinTokens <= 0 {
		cacheAffinityMinTokens = 4096
	}
	cacheAffinityLanes := cfg.CacheAffinityLanes
	if cacheAffinityLanes <= 0 {
		cacheAffinityLanes = 2
	}
	cacheAffinityMaxLanes := cfg.CacheAffinityMaxLanes
	if cacheAffinityMaxLanes <= 0 {
		cacheAffinityMaxLanes = 4
	}
	if cacheAffinityMaxLanes < cacheAffinityLanes {
		cacheAffinityMaxLanes = cacheAffinityLanes
	}
	cacheAffinityWaitMS := cfg.CacheAffinityWaitMS
	if cacheAffinityWaitMS <= 0 {
		cacheAffinityWaitMS = 250
	}
	cacheAffinityTTLMS := cfg.CacheAffinityTTLMS
	if cacheAffinityTTLMS <= 0 {
		cacheAffinityTTLMS = int((5 * time.Minute) / time.Millisecond)
	}
	return EffectiveRoutingConfig{
		PerAccountRPM:           cfg.PerAccountRPM,
		PerAccountConcurrency:   cfg.PerAccountConcurrency,
		MaxSwitches:             cfg.MaxSwitches,
		SwitchDelayMS:           cfg.SwitchDelayMS,
		RateLimitCooldownMS:     rateLimitCooldownMS,
		RateLimitMaxCooldownMS:  rateLimitMaxCooldownMS,
		OverloadCooldownMS:      overloadCooldownMS,
		OverloadMaxCooldownMS:   overloadMaxCooldownMS,
		SameAccountRetry429:     cfg.SameAccountRetry429,
		SameAccountRetry529:     cfg.SameAccountRetry529,
		SameAccountRetryDelayMS: sameAccountRetryDelayMS,
		CacheAffinityEnabled:    cfg.CacheAffinityEnabled,
		CacheAffinityAuto:       cfg.CacheAffinityAuto,
		CacheAffinityMinTokens:  cacheAffinityMinTokens,
		CacheAffinityLanes:      cacheAffinityLanes,
		CacheAffinityMaxLanes:   cacheAffinityMaxLanes,
		CacheAffinityWaitMS:     cacheAffinityWaitMS,
		CacheAffinityTTLMS:      cacheAffinityTTLMS,
	}
}

// RoutingConfigFromEffective converts an API view back to the file shape.
func RoutingConfigFromEffective(cfg EffectiveRoutingConfig) RoutingConfig {
	return NormalizeRoutingConfig(RoutingConfig{
		PerAccountRPM:           cfg.PerAccountRPM,
		PerAccountConcurrency:   cfg.PerAccountConcurrency,
		MaxSwitches:             cfg.MaxSwitches,
		SwitchDelayMS:           cfg.SwitchDelayMS,
		RateLimitCooldownMS:     cfg.RateLimitCooldownMS,
		RateLimitMaxCooldownMS:  cfg.RateLimitMaxCooldownMS,
		OverloadCooldownMS:      cfg.OverloadCooldownMS,
		OverloadMaxCooldownMS:   cfg.OverloadMaxCooldownMS,
		SameAccountRetry429:     cfg.SameAccountRetry429,
		SameAccountRetry529:     cfg.SameAccountRetry529,
		SameAccountRetryDelayMS: cfg.SameAccountRetryDelayMS,
		CacheAffinityEnabled:    cfg.CacheAffinityEnabled,
		CacheAffinityAuto:       cfg.CacheAffinityAuto,
		CacheAffinityMinTokens:  cfg.CacheAffinityMinTokens,
		CacheAffinityLanes:      cfg.CacheAffinityLanes,
		CacheAffinityMaxLanes:   cfg.CacheAffinityMaxLanes,
		CacheAffinityWaitMS:     cfg.CacheAffinityWaitMS,
		CacheAffinityTTLMS:      cfg.CacheAffinityTTLMS,
	})
}

// EffectiveVirtualCache returns the runtime virtual-cache policy with defaults applied.
func EffectiveVirtualCache(cfg VirtualCacheConfig) EffectiveVirtualCacheConfig {
	cfg = NormalizeVirtualCacheConfig(cfg)
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	hitRate := 0.90
	if cfg.HitRate != nil {
		hitRate = *cfg.HitRate
	}
	targetCacheReuseRatio := 0.0
	if cfg.TargetCacheReuseRatio != nil {
		targetCacheReuseRatio = *cfg.TargetCacheReuseRatio
	}
	contextShrinkResetRatio := 0.70
	if cfg.ContextShrinkResetRatio != nil {
		contextShrinkResetRatio = *cfg.ContextShrinkResetRatio
	}
	return EffectiveVirtualCacheConfig{
		Enabled:                 enabled,
		Mode:                    cfg.Mode,
		HitRate:                 hitRate,
		TargetCacheReuseRatio:   targetCacheReuseRatio,
		MinCacheTokens:          cfg.MinCacheTokens,
		MaxCacheTokens:          cfg.MaxCacheTokens,
		UncachedInputTokens:     cfg.UncachedInputTokens,
		ContextShrinkResetRatio: contextShrinkResetRatio,
		MinCreationTokens:       cfg.MinCreationTokens,
		MaxCreationTokens:       cfg.MaxCreationTokens,
	}
}

// VirtualCacheConfigFromEffective converts an API view back to the file shape.
func VirtualCacheConfigFromEffective(cfg EffectiveVirtualCacheConfig) VirtualCacheConfig {
	enabled := cfg.Enabled
	hitRate := cfg.HitRate
	targetCacheReuseRatio := cfg.TargetCacheReuseRatio
	contextShrinkResetRatio := cfg.ContextShrinkResetRatio
	return NormalizeVirtualCacheConfig(VirtualCacheConfig{
		Enabled:                 &enabled,
		Mode:                    cfg.Mode,
		HitRate:                 &hitRate,
		TargetCacheReuseRatio:   &targetCacheReuseRatio,
		MinCacheTokens:          cfg.MinCacheTokens,
		MaxCacheTokens:          cfg.MaxCacheTokens,
		UncachedInputTokens:     cfg.UncachedInputTokens,
		ContextShrinkResetRatio: &contextShrinkResetRatio,
		MinCreationTokens:       cfg.MinCreationTokens,
		MaxCreationTokens:       cfg.MaxCreationTokens,
	})
}

// NormalizeItem trims one raw item.
func NormalizeItem(item *Item) {
	if item == nil {
		return
	}
	item.APIKey = strings.TrimSpace(item.APIKey)
	if item.BaseURL != nil {
		v := strings.TrimSpace(*item.BaseURL)
		item.BaseURL = &v
	}
	if item.ProxyURL != nil {
		v := strings.TrimSpace(*item.ProxyURL)
		item.ProxyURL = &v
	}
	item.Headers = config.NormalizeHeaders(item.Headers)
	item.Models = normalizeModels(item.Models)
	item.ExcludedModels = config.NormalizeExcludedModels(item.ExcludedModels)
}

// Resolve expands every item with defaults.
func Resolve(doc *File) []ResolvedItem {
	if doc == nil {
		return nil
	}
	Normalize(doc)
	out := make([]ResolvedItem, 0, len(doc.Items))
	for i := range doc.Items {
		resolved := ResolveOne(doc, i)
		if resolved == nil {
			continue
		}
		out = append(out, *resolved)
	}
	return out
}

// ResolveOne expands one item by zero-based index.
func ResolveOne(doc *File, index int) *ResolvedItem {
	if doc == nil || index < 0 || index >= len(doc.Items) {
		return nil
	}
	Normalize(doc)
	raw := doc.Items[index]
	headers := make(map[string]string, len(doc.Defaults.Headers)+len(raw.Headers))
	for k, v := range doc.Defaults.Headers {
		headers[k] = v
	}
	for k, v := range raw.Headers {
		headers[k] = v
	}
	if len(headers) == 0 {
		headers = nil
	}

	baseURL := doc.Defaults.BaseURL
	if raw.BaseURL != nil {
		baseURL = *raw.BaseURL
	}
	proxyURL := doc.Defaults.ProxyURL
	if raw.ProxyURL != nil {
		proxyURL = *raw.ProxyURL
	}
	priority := doc.Defaults.Priority
	if raw.Priority != nil {
		priority = *raw.Priority
	}
	disableCooling := doc.Defaults.DisableCooling
	if raw.DisableCooling != nil {
		disableCooling = *raw.DisableCooling
	}
	models := doc.Models
	if len(raw.Models) > 0 {
		models = raw.Models
	}
	models = append([]config.ClaudeModel(nil), models...)
	status := StatusEnabled
	if raw.Disabled {
		status = StatusDisabled
	}
	ck := config.ClaudeKey{
		APIKey:                 raw.APIKey,
		BaseURL:                baseURL,
		ProxyURL:               proxyURL,
		Priority:               priority,
		Models:                 models,
		Headers:                headers,
		ExcludedModels:         append([]string(nil), raw.ExcludedModels...),
		DisableCooling:         disableCooling,
		Cloak:                  raw.Cloak,
		ExperimentalCCHSigning: raw.ExperimentalCCHSigning,
	}
	return &ResolvedItem{
		Position: index + 1,
		ItemHash: ItemHash(raw),
		Status:   status,
		PureMode: doc.PureMode,
		Raw:      raw,
		Config:   ck,
	}
}

// List returns a paged, filtered view.
func List(doc *File, query ListQuery) ListResult {
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 50
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	q := strings.ToLower(strings.TrimSpace(query.Q))
	statusFilter := strings.ToLower(strings.TrimSpace(query.Status))
	modelFilter := strings.ToLower(strings.TrimSpace(query.Model))
	all := Resolve(doc)
	filtered := make([]ItemView, 0, len(all))
	for i := range all {
		view := ToView(all[i], query.Runtime[all[i].Position])
		if authID := strings.TrimSpace(query.AuthIDs[all[i].Position]); authID != "" {
			view.AuthID = authID
		}
		if statusFilter != "" && statusFilter != "all" && !strings.EqualFold(view.Status, statusFilter) {
			continue
		}
		if modelFilter != "" && modelFilter != "all" && !viewHasModel(view, modelFilter) {
			continue
		}
		if q != "" && !viewMatches(view, q) {
			continue
		}
		filtered = append(filtered, view)
	}
	total := len(filtered)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	return ListResult{
		Items:    filtered[start:end],
		Page:     query.Page,
		PageSize: query.PageSize,
		Total:    total,
	}
}

// ToView converts a resolved item into the management API shape.
func ToView(item ResolvedItem, runtime RuntimeStatus) ItemView {
	status := item.Status
	if runtime.Disabled {
		status = StatusDisabled
	}
	coolingUntil := ""
	if runtime.Cooling {
		status = StatusCooling
		if !runtime.CoolingTo.IsZero() {
			coolingUntil = runtime.CoolingTo.UTC().Format(time.RFC3339)
		}
	}
	return ItemView{
		Position:       item.Position,
		ItemHash:       item.ItemHash,
		AuthID:         runtime.AuthID,
		Status:         status,
		APIKeyPreview:  PreviewKey(item.Config.APIKey),
		BaseURL:        item.Config.BaseURL,
		ProxyURL:       item.Config.ProxyURL,
		Priority:       item.Config.Priority,
		DisableCooling: item.Config.DisableCooling,
		Headers:        cloneStringMap(item.Config.Headers),
		Models:         append([]config.ClaudeModel(nil), item.Config.Models...),
		ExcludedModels: append([]string(nil), item.Config.ExcludedModels...),
		Raw:            item.Raw,
		InFlight:       runtime.InFlight,
		RPMUsed:        runtime.RPMUsed,
		RPMLimit:       runtime.RPMLimit,
		Cooling:        runtime.Cooling,
		CoolingUntil:   coolingUntil,
		WarmKeys:       runtime.WarmKeys,
		Metrics:        runtime.Metrics,
	}
}

// ItemHash returns a short stable hash for optimistic concurrency.
func ItemHash(item Item) string {
	NormalizeItem(&item)
	data, _ := json.Marshal(canonicalItem(item))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// ApplyImport appends or replaces items from a YAML/JSON payload.
func ApplyImport(doc *File, data []byte, replace bool) (int, error) {
	if doc == nil {
		return 0, fmt.Errorf("pool document is nil")
	}
	imported, err := DecodeImportFile(data)
	if err != nil {
		return 0, err
	}
	if replace {
		if importHasPoolConfig(imported) {
			*doc = *imported
		} else {
			doc.Items = imported.Items
		}
	} else {
		resolved := Resolve(imported)
		for i := range resolved {
			doc.Items = append(doc.Items, itemFromClaudeKey(resolved[i].Config))
		}
	}
	Normalize(doc)
	return len(imported.Items), nil
}

func importHasPoolConfig(doc *File) bool {
	if doc == nil {
		return false
	}
	if doc.PureMode {
		return true
	}
	if doc.VirtualCache.Enabled != nil ||
		(doc.VirtualCache.Mode != "" && doc.VirtualCache.Mode != VirtualCacheModeNatural) ||
		doc.VirtualCache.HitRate != nil ||
		doc.VirtualCache.TargetCacheReuseRatio != nil ||
		doc.VirtualCache.MinCacheTokens != 0 ||
		doc.VirtualCache.MaxCacheTokens != 0 ||
		doc.VirtualCache.UncachedInputTokens != 0 ||
		doc.VirtualCache.ContextShrinkResetRatio != nil ||
		doc.VirtualCache.MinCreationTokens != 0 ||
		doc.VirtualCache.MaxCreationTokens != 0 {
		return true
	}
	if doc.Routing != (RoutingConfig{}) {
		return true
	}
	if doc.Defaults.BaseURL != "" ||
		doc.Defaults.ProxyURL != "" ||
		doc.Defaults.Priority != 0 ||
		doc.Defaults.DisableCooling ||
		len(doc.Defaults.Headers) > 0 {
		return true
	}
	return len(doc.Models) > 0
}

// DecodeImport accepts a full pool document, an array of items, {"items": [...]}, or apiKey-----workspaceId lines.
func DecodeImport(data []byte) ([]Item, error) {
	doc, err := DecodeImportFile(data)
	if err != nil {
		return nil, err
	}
	return doc.Items, nil
}

// DecodeImportFile accepts a full pool document, an array of items, {"items": [...]}, or apiKey-----workspaceId lines.
func DecodeImportFile(data []byte) (*File, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("import body is empty")
	}
	var doc File
	if trimmed[0] == '[' {
		var items []Item
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("parse import json: %w", err)
		}
		doc = File{Version: 1, Items: items}
	} else if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("parse import json: %w", err)
		}
	} else if err := yaml.Unmarshal(trimmed, &doc); err != nil {
		simpleDoc, errSimple := DecodeSimpleImportLines(trimmed)
		if errSimple == nil {
			doc = *simpleDoc
		} else {
			var items []Item
			if err2 := yaml.Unmarshal(trimmed, &items); err2 != nil {
				return nil, fmt.Errorf("parse import yaml: %w", err)
			}
			doc = File{Version: 1, Items: items}
		}
	}
	Normalize(&doc)
	if len(doc.Items) == 0 {
		return nil, fmt.Errorf("import contains no items")
	}
	return &doc, nil
}

// DecodeSimpleImportLines accepts one apiKey-----workspaceId account per line.
func DecodeSimpleImportLines(data []byte) (*File, error) {
	lines := strings.Split(string(data), "\n")
	items := make([]Item, 0, len(lines))
	for lineNumber, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "-----")
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d must use apiKey-----workspaceId", lineNumber+1)
		}
		apiKey := strings.TrimSpace(parts[0])
		workspaceID := strings.TrimSpace(parts[1])
		if apiKey == "" || workspaceID == "" {
			return nil, fmt.Errorf("line %d must include both apiKey and workspaceId", lineNumber+1)
		}
		items = append(items, Item{
			APIKey: apiKey,
			Headers: map[string]string{
				SimpleImportWorkspaceHeader: workspaceID,
			},
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("simple import contains no items")
	}
	doc := &File{Version: 1, Items: items}
	Normalize(doc)
	return doc, nil
}

// ModelsToAttribute serializes pool models for runtime auth model registration.
func ModelsToAttribute(models []config.ClaudeModel) string {
	models = normalizeModels(models)
	if len(models) == 0 {
		return ""
	}
	data, err := json.Marshal(models)
	if err != nil {
		return ""
	}
	return string(data)
}

// ModelsFromAttributes reads pool models from runtime auth attributes.
func ModelsFromAttributes(attrs map[string]string) []config.ClaudeModel {
	if len(attrs) == 0 {
		return nil
	}
	raw := strings.TrimSpace(attrs[AttrModelsJSON])
	if raw == "" {
		return nil
	}
	var models []config.ClaudeModel
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil
	}
	return normalizeModels(models)
}

// NormalizeDefaults normalizes pool-level inherited account settings.
func NormalizeDefaults(defaults Defaults) Defaults {
	defaults.BaseURL = strings.TrimSpace(defaults.BaseURL)
	defaults.ProxyURL = strings.TrimSpace(defaults.ProxyURL)
	defaults.Headers = config.NormalizeHeaders(defaults.Headers)
	return defaults
}

// NormalizeModelList normalizes a public model list for management updates.
func NormalizeModelList(models []config.ClaudeModel) []config.ClaudeModel {
	return normalizeModels(models)
}

// IsAttributesPoolAuth reports whether the attributes belong to a pool auth.
func IsAttributesPoolAuth(attrs map[string]string) bool {
	if len(attrs) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(attrs[AttrPool]), "true")
}

// ReplaceItem replaces one one-based position guarded by item hash.
func ReplaceItem(doc *File, position int, expectedHash string, item Item) (*ResolvedItem, error) {
	if doc == nil {
		return nil, fmt.Errorf("pool document is nil")
	}
	index := position - 1
	if index < 0 || index >= len(doc.Items) {
		return nil, fmt.Errorf("item not found")
	}
	if !hashMatches(doc.Items[index], expectedHash) {
		return nil, fmt.Errorf("item hash mismatch")
	}
	NormalizeItem(&item)
	doc.Items[index] = item
	Normalize(doc)
	return ResolveOne(doc, index), nil
}

// AppendItem appends one account entry and returns its resolved view.
func AppendItem(doc *File, item Item) (*ResolvedItem, error) {
	if doc == nil {
		return nil, fmt.Errorf("pool document is nil")
	}
	NormalizeItem(&item)
	if strings.TrimSpace(item.APIKey) == "" {
		return nil, fmt.Errorf("api key is required")
	}
	doc.Items = append(doc.Items, item)
	Normalize(doc)
	return ResolveOne(doc, len(doc.Items)-1), nil
}

// DeleteItem removes one one-based position guarded by item hash.
func DeleteItem(doc *File, position int, expectedHash string) error {
	if doc == nil {
		return fmt.Errorf("pool document is nil")
	}
	index := position - 1
	if index < 0 || index >= len(doc.Items) {
		return fmt.Errorf("item not found")
	}
	if !hashMatches(doc.Items[index], expectedHash) {
		return fmt.Errorf("item hash mismatch")
	}
	doc.Items = append(doc.Items[:index], doc.Items[index+1:]...)
	Normalize(doc)
	return nil
}

// SetDisabled toggles one item guarded by item hash.
func SetDisabled(doc *File, position int, expectedHash string, disabled bool) (*ResolvedItem, error) {
	if doc == nil {
		return nil, fmt.Errorf("pool document is nil")
	}
	index := position - 1
	if index < 0 || index >= len(doc.Items) {
		return nil, fmt.Errorf("item not found")
	}
	if !hashMatches(doc.Items[index], expectedHash) {
		return nil, fmt.Errorf("item hash mismatch")
	}
	doc.Items[index].Disabled = disabled
	Normalize(doc)
	return ResolveOne(doc, index), nil
}

// SetDisabledBatch toggles multiple items guarded by item hashes.
func SetDisabledBatch(doc *File, refs []MutationRef, disabled bool) ([]ResolvedItem, error) {
	if doc == nil {
		return nil, fmt.Errorf("pool document is nil")
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("no items selected")
	}
	seen := make(map[int]struct{}, len(refs))
	indexes := make([]int, 0, len(refs))
	for _, ref := range refs {
		index := ref.Position - 1
		if index < 0 || index >= len(doc.Items) {
			return nil, fmt.Errorf("item not found")
		}
		if _, ok := seen[index]; ok {
			return nil, fmt.Errorf("duplicate item position")
		}
		if !hashMatches(doc.Items[index], ref.ItemHash) {
			return nil, fmt.Errorf("item hash mismatch")
		}
		seen[index] = struct{}{}
		indexes = append(indexes, index)
	}
	for _, index := range indexes {
		doc.Items[index].Disabled = disabled
	}
	Normalize(doc)
	resolved := make([]ResolvedItem, 0, len(indexes))
	for _, index := range indexes {
		item := ResolveOne(doc, index)
		if item != nil {
			resolved = append(resolved, *item)
		}
	}
	return resolved, nil
}

func hashMatches(item Item, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	return strings.EqualFold(ItemHash(item), expected)
}

func normalizeModels(models []config.ClaudeModel) []config.ClaudeModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]config.ClaudeModel, 0, len(models))
	for i := range models {
		model := models[i]
		model.Name = strings.TrimSpace(model.Name)
		model.Alias = strings.TrimSpace(model.Alias)
		if model.Name == "" && model.Alias == "" {
			continue
		}
		out = append(out, model)
	}
	return out
}

func viewHasModel(view ItemView, model string) bool {
	for i := range view.Models {
		if strings.EqualFold(view.Models[i].Name, model) || strings.EqualFold(view.Models[i].Alias, model) {
			return true
		}
	}
	return false
}

func viewMatches(view ItemView, q string) bool {
	fields := []string{
		strconv.Itoa(view.Position),
		view.APIKeyPreview,
		view.BaseURL,
		view.ProxyURL,
		strconv.Itoa(view.Priority),
		view.Status,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	for k, v := range view.Headers {
		if strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	for i := range view.Models {
		if strings.Contains(strings.ToLower(view.Models[i].Name), q) || strings.Contains(strings.ToLower(view.Models[i].Alias), q) {
			return true
		}
	}
	return false
}

func PreviewKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 10 {
		return key
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func itemFromClaudeKey(ck config.ClaudeKey) Item {
	baseURL := strings.TrimSpace(ck.BaseURL)
	proxyURL := strings.TrimSpace(ck.ProxyURL)
	priority := ck.Priority
	disableCooling := ck.DisableCooling
	item := Item{
		APIKey:                 strings.TrimSpace(ck.APIKey),
		Headers:                cloneStringMap(ck.Headers),
		Models:                 append([]config.ClaudeModel(nil), ck.Models...),
		ExcludedModels:         append([]string(nil), ck.ExcludedModels...),
		Cloak:                  ck.Cloak,
		ExperimentalCCHSigning: ck.ExperimentalCCHSigning,
	}
	if baseURL != "" {
		item.BaseURL = &baseURL
	}
	if proxyURL != "" {
		item.ProxyURL = &proxyURL
	}
	if priority != 0 {
		item.Priority = &priority
	}
	if disableCooling {
		item.DisableCooling = &disableCooling
	}
	NormalizeItem(&item)
	return item
}

func canonicalItem(item Item) any {
	return struct {
		APIKey                 string               `json:"api-key"`
		BaseURL                *string              `json:"base-url,omitempty"`
		ProxyURL               *string              `json:"proxy-url,omitempty"`
		Priority               *int                 `json:"priority,omitempty"`
		DisableCooling         *bool                `json:"disable-cooling,omitempty"`
		Disabled               bool                 `json:"disabled,omitempty"`
		Headers                map[string]string    `json:"headers,omitempty"`
		Models                 []config.ClaudeModel `json:"models,omitempty"`
		ExcludedModels         []string             `json:"excluded-models,omitempty"`
		Cloak                  *config.CloakConfig  `json:"cloak,omitempty"`
		ExperimentalCCHSigning bool                 `json:"experimental-cch-signing,omitempty"`
	}{
		APIKey:                 item.APIKey,
		BaseURL:                item.BaseURL,
		ProxyURL:               item.ProxyURL,
		Priority:               item.Priority,
		DisableCooling:         item.DisableCooling,
		Disabled:               item.Disabled,
		Headers:                sortedMap(item.Headers),
		Models:                 item.Models,
		ExcludedModels:         item.ExcludedModels,
		Cloak:                  item.Cloak,
		ExperimentalCCHSigning: item.ExperimentalCCHSigning,
	}
}

func sortedMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(in))
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
}
