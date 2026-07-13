package claudeapipool

import (
	"math"
	"strings"
	"sync"
	"time"
)

const (
	StatusTooManyRequests = 429
	StatusOverloaded      = 529
	routeWindow           = time.Minute
)

var defaultRouting = newPoolRouter()
var defaultRoutingPolicy = EffectiveRouting(RoutingConfig{})
var scopedRouters = map[string]*poolRouter{}
var scopedRoutingPolicies = map[string]EffectiveRoutingConfig{}

var routingPolicyMu sync.RWMutex

type poolRouter struct {
	mu             sync.Mutex
	states         map[string]*routeState
	accountStates  map[string]*routeState
	sessions       map[string]*sessionBinding
	activeSessions map[string]*activeSession
	lastSelected   map[string]time.Time
	waiters        map[string]int
	proxyCooling   map[string]time.Time
	headrooms      map[string]map[string]headroomSnapshot
	globalWaiters  int
	changed        chan struct{}
}

type sessionBinding struct {
	AuthID    string
	ExpiresAt time.Time
}

type activeSession struct {
	AuthID    string
	ExpiresAt time.Time
}

type headroomSnapshot struct {
	Value       float64
	UsedPercent float64
	Band        string
	Window      string
	ResetAt     time.Time
	ExpiresAt   time.Time
}

type routeState struct {
	InFlight       int64
	RecentStarts   []time.Time
	CoolingUntil   time.Time
	RateLimitLevel int
	OverloadLevel  int
	ManualBlocked  bool
	UpdatedAt      time.Time
}

// RouteLease releases one in-flight pool route slot.
type RouteLease struct {
	router    *poolRouter
	key       string
	model     string
	policy    EffectiveRoutingConfig
	selection AccountRouteSelection
	attempts  int
	once      sync.Once
}

// RouteStatus summarizes local pool route state for one auth/model.
type RouteStatus struct {
	InFlight          int64     `json:"in_flight"`
	RPMUsed           int       `json:"rpm_used"`
	RPMLimit          int       `json:"rpm_limit"`
	ActiveSessions    int       `json:"active_sessions"`
	MaxSessions       int       `json:"max_sessions"`
	Waiters           int       `json:"waiters"`
	Cooling           bool      `json:"cooling"`
	AccountCooling    bool      `json:"account_cooling"`
	ModelCooling      bool      `json:"model_cooling"`
	ModelCoolingCount int       `json:"model_cooling_count"`
	CoolingTo         time.Time `json:"cooling_until,omitempty"`
	Unavailable       bool      `json:"unavailable"`
}

// SetRoutingConfig updates the runtime policy used by future pool routing.
func SetRoutingConfig(cfg EffectiveRoutingConfig) {
	routingPolicyMu.Lock()
	defer routingPolicyMu.Unlock()
	defaultRoutingPolicy = normalizeEffectiveRoutingConfig(cfg)
	DebugLogf(
		"claude api pool routing config rpm=%d concurrency=%d max_switches=%d switch_delay_ms=%d rate_cooldown_ms=%d rate_max_cooldown_ms=%d overload_cooldown_ms=%d overload_max_cooldown_ms=%d same_retry_429=%d same_retry_529=%d same_retry_delay_ms=%d affinity_enabled=%t affinity_auto=%t affinity_lanes=%d affinity_max_lanes=%d affinity_min_tokens=%d affinity_wait_ms=%d affinity_ttl_ms=%d",
		defaultRoutingPolicy.PerAccountRPM,
		defaultRoutingPolicy.PerAccountConcurrency,
		defaultRoutingPolicy.MaxSwitches,
		defaultRoutingPolicy.SwitchDelayMS,
		defaultRoutingPolicy.RateLimitCooldownMS,
		defaultRoutingPolicy.RateLimitMaxCooldownMS,
		defaultRoutingPolicy.OverloadCooldownMS,
		defaultRoutingPolicy.OverloadMaxCooldownMS,
		defaultRoutingPolicy.SameAccountRetry429,
		defaultRoutingPolicy.SameAccountRetry529,
		defaultRoutingPolicy.SameAccountRetryDelayMS,
		defaultRoutingPolicy.CacheAffinityEnabled,
		defaultRoutingPolicy.CacheAffinityAuto,
		defaultRoutingPolicy.CacheAffinityLanes,
		defaultRoutingPolicy.CacheAffinityMaxLanes,
		defaultRoutingPolicy.CacheAffinityMinTokens,
		defaultRoutingPolicy.CacheAffinityWaitMS,
		defaultRoutingPolicy.CacheAffinityTTLMS,
	)
}

// CurrentRoutingConfig returns the runtime pool routing policy.
func CurrentRoutingConfig() EffectiveRoutingConfig {
	routingPolicyMu.RLock()
	defer routingPolicyMu.RUnlock()
	return defaultRoutingPolicy
}

// SetScopedRoutingConfig updates a named pool routing policy without affecting the default pool.
func SetScopedRoutingConfig(scope string, cfg EffectiveRoutingConfig) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		SetRoutingConfig(cfg)
		return
	}
	routingPolicyMu.Lock()
	defer routingPolicyMu.Unlock()
	if scopedRouters == nil {
		scopedRouters = make(map[string]*poolRouter)
	}
	if scopedRouters[scope] == nil {
		scopedRouters[scope] = newPoolRouter()
	}
	if scopedRoutingPolicies == nil {
		scopedRoutingPolicies = make(map[string]EffectiveRoutingConfig)
	}
	scopedRoutingPolicies[scope] = normalizeEffectiveRoutingConfig(cfg)
	for childScope := range scopedRoutingPolicies {
		if strings.HasPrefix(childScope, scope+"/") {
			scopedRoutingPolicies[childScope] = scopedRoutingPolicies[scope]
			if childRouter := scopedRouters[childScope]; childRouter != nil {
				childRouter.mu.Lock()
				childRouter.signalChangedLocked()
				childRouter.mu.Unlock()
			}
		}
	}
	router := scopedRouters[scope]
	router.mu.Lock()
	router.signalChangedLocked()
	router.mu.Unlock()
}

// CurrentScopedRoutingConfig returns a named pool policy, falling back to the default pool policy.
func CurrentScopedRoutingConfig(scope string) EffectiveRoutingConfig {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return CurrentRoutingConfig()
	}
	routingPolicyMu.RLock()
	defer routingPolicyMu.RUnlock()
	if scopedRoutingPolicies != nil {
		if cfg, ok := scopedRoutingPolicies[scope]; ok {
			return cfg
		}
		for parent := parentRoutingScope(scope); parent != ""; parent = parentRoutingScope(parent) {
			if cfg, ok := scopedRoutingPolicies[parent]; ok {
				return cfg
			}
		}
	}
	return defaultRoutingPolicy
}

// RemoveScopedRoutingScope drops an archived pool's policy and in-memory routing state.
// Existing leases retain their router pointer and can still release normally.
func RemoveScopedRoutingScope(scope string) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return
	}
	routingPolicyMu.Lock()
	defer routingPolicyMu.Unlock()
	delete(scopedRoutingPolicies, scope)
	delete(scopedRouters, scope)
	for childScope := range scopedRoutingPolicies {
		if strings.HasPrefix(childScope, scope+"/") {
			delete(scopedRoutingPolicies, childScope)
			delete(scopedRouters, childScope)
		}
	}
}

func scopedRouterAndPolicy(scope string) (*poolRouter, EffectiveRoutingConfig) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return defaultRouting, CurrentRoutingConfig()
	}
	routingPolicyMu.RLock()
	router := scopedRouters[scope]
	policy, ok := scopedRoutingPolicies[scope]
	routingPolicyMu.RUnlock()
	if router != nil && ok {
		return router, policy
	}
	routingPolicyMu.Lock()
	defer routingPolicyMu.Unlock()
	if router = scopedRouters[scope]; router == nil {
		router = newPoolRouter()
		scopedRouters[scope] = router
	}
	if policy, ok = scopedRoutingPolicies[scope]; !ok {
		policy = defaultRoutingPolicy
		for parent := parentRoutingScope(scope); parent != ""; parent = parentRoutingScope(parent) {
			if parentPolicy, exists := scopedRoutingPolicies[parent]; exists {
				policy = parentPolicy
				break
			}
		}
		scopedRoutingPolicies[scope] = policy
	}
	return router, policy
}

func parentRoutingScope(scope string) string {
	scope = strings.Trim(strings.TrimSpace(scope), "/")
	if index := strings.LastIndex(scope, "/"); index > 0 {
		return scope[:index]
	}
	return ""
}

// TryAcquireRoute reserves local capacity for one upstream pool request.
func TryAcquireRoute(authID, model string) (*RouteLease, bool) {
	return defaultRouting.tryAcquire(authID, model, CurrentRoutingConfig(), false, time.Now())
}

// TryAcquireScopedRoute reserves local capacity for a named pool request.
func TryAcquireScopedRoute(scope, authID, model string) (*RouteLease, bool) {
	router, policy := scopedRouterAndPolicy(scope)
	return router.tryAcquire(authID, model, policy, false, time.Now())
}

// TryAcquireScopedRouteWithPolicy reserves local capacity using a per-auth policy override.
func TryAcquireScopedRouteWithPolicy(scope, authID, model string, override EffectiveRoutingConfig) (*RouteLease, bool) {
	return TryAcquireScopedRouteWithPolicyOptions(scope, authID, model, override, false)
}

// TryAcquireScopedRouteWithPolicyOptions reserves local capacity with optional sticky buffer usage.
func TryAcquireScopedRouteWithPolicyOptions(scope, authID, model string, override EffectiveRoutingConfig, sticky bool) (*RouteLease, bool) {
	router, policy := scopedRouterAndPolicy(scope)
	if override.PerAccountRPM > 0 {
		policy.PerAccountRPM = override.PerAccountRPM
	}
	if override.PerAccountConcurrency > 0 {
		policy.PerAccountConcurrency = override.PerAccountConcurrency
	}
	if override.StickyConcurrencyReserve > 0 {
		policy.StickyConcurrencyReserve = override.StickyConcurrencyReserve
	}
	return router.tryAcquire(authID, model, policy, sticky, time.Now())
}

// NoteRouteResult updates local pool routing state from an upstream status.
func NoteRouteResult(authID, model string, statusCode int, retryAfter *time.Duration) {
	defaultRouting.noteResult(authID, model, statusCode, retryAfter, CurrentRoutingConfig(), time.Now())
}

// NoteScopedRouteResult updates local route state for a named pool.
func NoteScopedRouteResult(scope, authID, model string, statusCode int, retryAfter *time.Duration) {
	router, policy := scopedRouterAndPolicy(scope)
	router.noteResult(authID, model, statusCode, retryAfter, policy, time.Now())
}

// RouteStatusFor returns local route pressure for one auth/model.
func RouteStatusFor(authID, model string) RouteStatus {
	return defaultRouting.status(authID, model, CurrentRoutingConfig(), time.Now())
}

// ScopedRouteStatusFor returns local route pressure for one auth/model in a named pool.
func ScopedRouteStatusFor(scope, authID, model string) RouteStatus {
	router, policy := scopedRouterAndPolicy(scope)
	return router.status(authID, model, policy, time.Now())
}

// ScopedRouteStatusForWithPolicy returns model-aware pressure using a per-auth capacity override.
func ScopedRouteStatusForWithPolicy(scope, authID, model string, override EffectiveRoutingConfig) RouteStatus {
	router, policy := scopedRouterAndPolicy(scope)
	return router.status(authID, model, mergeRoutingOverride(policy, override), time.Now())
}

// AggregateRouteStatus returns local route pressure across all models for one auth.
func AggregateRouteStatus(authID string) RouteStatus {
	return defaultRouting.aggregateStatus(authID, CurrentRoutingConfig(), time.Now())
}

// AggregateScopedRouteStatus returns local route pressure for a named pool.
func AggregateScopedRouteStatus(scope, authID string) RouteStatus {
	router, policy := scopedRouterAndPolicy(scope)
	return router.aggregateStatus(authID, policy, time.Now())
}

// AggregateScopedRouteStatusWithPolicy returns route pressure using a per-auth policy override.
func AggregateScopedRouteStatusWithPolicy(scope, authID string, override EffectiveRoutingConfig) RouteStatus {
	router, policy := scopedRouterAndPolicy(scope)
	return router.aggregateStatus(authID, mergeRoutingOverride(policy, override), time.Now())
}

// ResetRouteCooling clears local pool limiter/cooldown state.
func ResetRouteCooling(authID string) {
	defaultRouting.reset(authID)
}

// ResetScopedRouteCooling clears limiter and cooldown state in one named pool.
func ResetScopedRouteCooling(scope, authID string) {
	router, _ := scopedRouterAndPolicy(scope)
	router.reset(authID)
}

// BlockScopedAccount removes an account from all model routes until recovery.
func BlockScopedAccount(scope, authID string, cooldown time.Duration, manual bool) {
	router, _ := scopedRouterAndPolicy(scope)
	if router == nil || strings.TrimSpace(authID) == "" {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	state := router.ensureAccountStateLocked(accountRouteKey(authID))
	state.ManualBlocked = manual
	if manual {
		state.CoolingUntil = time.Time{}
	} else if cooldown > 0 {
		state.CoolingUntil = time.Now().Add(cooldown)
	}
	state.UpdatedAt = time.Now()
	router.signalChangedLocked()
}

// ClearScopedAccountBlock restores an account after refresh or manual recovery.
func ClearScopedAccountBlock(scope, authID string) {
	router, _ := scopedRouterAndPolicy(scope)
	if router == nil || strings.TrimSpace(authID) == "" {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	state := router.ensureAccountStateLocked(accountRouteKey(authID))
	state.ManualBlocked = false
	state.CoolingUntil = time.Time{}
	state.UpdatedAt = time.Now()
	router.signalChangedLocked()
}

// CooldownForStatus returns the pool-specific cooldown for 429/529.
func CooldownForStatus(statusCode int, backoffLevel int, retryAfter *time.Duration) (time.Duration, int, bool) {
	return cooldownForStatus(statusCode, backoffLevel, retryAfter, CurrentRoutingConfig())
}

// CooldownForScopedStatus returns pool-specific cooldown for a named pool.
func CooldownForScopedStatus(scope string, statusCode int, backoffLevel int, retryAfter *time.Duration) (time.Duration, int, bool) {
	_, policy := scopedRouterAndPolicy(scope)
	return cooldownForStatus(statusCode, backoffLevel, retryAfter, policy)
}

// CrossAccountAttempts returns the max credential attempts implied by max-switches.
func CrossAccountAttempts() int {
	cfg := CurrentRoutingConfig()
	if cfg.MaxSwitches <= 0 {
		return 0
	}
	return cfg.MaxSwitches + 1
}

// ScopedCrossAccountAttempts returns cross-account attempts for a named pool.
func ScopedCrossAccountAttempts(scope string) int {
	cfg := CurrentScopedRoutingConfig(scope)
	if cfg.MaxSwitches <= 0 {
		return 0
	}
	return cfg.MaxSwitches + 1
}

// SameAccountRetry returns same-auth retry settings for a status code.
func SameAccountRetry(statusCode int) (int, time.Duration) {
	cfg := CurrentRoutingConfig()
	attempts := 0
	switch statusCode {
	case StatusTooManyRequests:
		attempts = cfg.SameAccountRetry429
	case StatusOverloaded:
		attempts = cfg.SameAccountRetry529
	}
	if attempts <= 0 {
		return 0, 0
	}
	return attempts, time.Duration(cfg.SameAccountRetryDelayMS) * time.Millisecond
}

// ScopedSameAccountRetry returns same-account retry settings for a named pool.
func ScopedSameAccountRetry(scope string, statusCode int) (int, time.Duration) {
	cfg := CurrentScopedRoutingConfig(scope)
	attempts := 0
	switch statusCode {
	case StatusTooManyRequests:
		attempts = cfg.SameAccountRetry429
	case StatusOverloaded:
		attempts = cfg.SameAccountRetry529
	}
	if attempts <= 0 {
		return 0, 0
	}
	return attempts, time.Duration(cfg.SameAccountRetryDelayMS) * time.Millisecond
}

// SwitchDelay returns the configured delay before trying another pool account.
func SwitchDelay() time.Duration {
	cfg := CurrentRoutingConfig()
	if cfg.SwitchDelayMS <= 0 {
		return 0
	}
	return time.Duration(cfg.SwitchDelayMS) * time.Millisecond
}

// ScopedSwitchDelay returns switch delay for a named pool.
func ScopedSwitchDelay(scope string) time.Duration {
	cfg := CurrentScopedRoutingConfig(scope)
	if cfg.SwitchDelayMS <= 0 {
		return 0
	}
	return time.Duration(cfg.SwitchDelayMS) * time.Millisecond
}

func newPoolRouter() *poolRouter {
	return &poolRouter{
		states:         make(map[string]*routeState),
		accountStates:  make(map[string]*routeState),
		sessions:       make(map[string]*sessionBinding),
		activeSessions: make(map[string]*activeSession),
		lastSelected:   make(map[string]time.Time),
		waiters:        make(map[string]int),
		proxyCooling:   make(map[string]time.Time),
		headrooms:      make(map[string]map[string]headroomSnapshot),
		changed:        make(chan struct{}),
	}
}

func (r *poolRouter) tryAcquire(authID, model string, policy EffectiveRoutingConfig, sticky bool, now time.Time) (*RouteLease, bool) {
	if r == nil {
		return nil, true
	}
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil, true
	}
	policy = normalizeEffectiveRoutingConfig(policy)
	accountKey := accountRouteKey(authID)
	modelKey := routeKey(authID, model)
	r.mu.Lock()
	defer r.mu.Unlock()
	accountState := r.ensureAccountStateLocked(accountKey)
	modelState := r.ensureStateLocked(modelKey)
	r.pruneRecentStartsLocked(accountState, now)
	if modelState.CoolingUntil.After(now) {
		DebugLogf(
			"claude api pool route denied auth=%s model=%s reason=cooling cooling_until=%s cooling_ms=%d inflight=%d rpm_used=%d rpm_limit=%d",
			debugAuthRef(authID),
			model,
			debugTime(modelState.CoolingUntil),
			debugUntilMS(now, modelState.CoolingUntil),
			accountState.InFlight,
			len(accountState.RecentStarts),
			policy.PerAccountRPM,
		)
		return nil, false
	}
	concurrencyLimit := routeConcurrencyLimit(policy, sticky)
	if concurrencyLimit > 0 && accountState.InFlight >= int64(concurrencyLimit) {
		DebugLogf(
			"claude api pool route denied auth=%s model=%s reason=concurrency sticky=%t inflight=%d concurrency_limit=%d rpm_used=%d rpm_limit=%d",
			debugAuthRef(authID),
			model,
			sticky,
			accountState.InFlight,
			concurrencyLimit,
			len(accountState.RecentStarts),
			routeRPMLimit(policy, sticky),
		)
		return nil, false
	}
	rpmLimit := routeRPMLimit(policy, sticky)
	if rpmLimit > 0 && len(accountState.RecentStarts) >= rpmLimit {
		DebugLogf(
			"claude api pool route denied auth=%s model=%s reason=rpm sticky=%t inflight=%d rpm_used=%d rpm_limit=%d",
			debugAuthRef(authID),
			model,
			sticky,
			accountState.InFlight,
			len(accountState.RecentStarts),
			rpmLimit,
		)
		return nil, false
	}
	accountState.InFlight++
	accountState.RecentStarts = append(accountState.RecentStarts, now)
	accountState.UpdatedAt = now
	DebugLogf(
		"claude api pool route acquired auth=%s model=%s sticky=%t inflight=%d concurrency_limit=%d rpm_used=%d rpm_limit=%d",
		debugAuthRef(authID),
		model,
		sticky,
		accountState.InFlight,
		concurrencyLimit,
		len(accountState.RecentStarts),
		rpmLimit,
	)
	return &RouteLease{router: r, key: accountKey, model: strings.TrimSpace(model), policy: policy, attempts: 1}, true
}

func (r *poolRouter) noteResult(authID, model string, statusCode int, retryAfter *time.Duration, policy EffectiveRoutingConfig, now time.Time) {
	if r == nil || strings.TrimSpace(authID) == "" {
		return
	}
	key := routeKey(authID, model)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.ensureStateLocked(key)
	r.pruneRecentStartsLocked(state, now)
	state.UpdatedAt = now
	switch statusCode {
	case StatusTooManyRequests:
		cooldown, nextLevel, ok := cooldownForStatus(statusCode, state.RateLimitLevel, retryAfter, policy)
		if !ok {
			DebugLogf(
				"claude api pool route result auth=%s model=%s status=%d action=no_cooldown rate_level=%d overload_level=%d",
				debugAuthRef(authID),
				model,
				statusCode,
				state.RateLimitLevel,
				state.OverloadLevel,
			)
			return
		}
		state.RateLimitLevel = nextLevel
		state.CoolingUntil = laterTime(state.CoolingUntil, now.Add(cooldown))
		DebugLogf(
			"claude api pool route result auth=%s model=%s status=%d cooldown_ms=%d rate_level=%d overload_level=%d cooling_until=%s retry_after_ms=%d",
			debugAuthRef(authID),
			model,
			statusCode,
			debugDurationMS(cooldown),
			state.RateLimitLevel,
			state.OverloadLevel,
			debugTime(state.CoolingUntil),
			debugRetryAfterMS(retryAfter),
		)
	case StatusOverloaded:
		cooldown, nextLevel, ok := cooldownForStatus(statusCode, state.OverloadLevel, retryAfter, policy)
		if !ok {
			DebugLogf(
				"claude api pool route result auth=%s model=%s status=%d action=no_cooldown rate_level=%d overload_level=%d",
				debugAuthRef(authID),
				model,
				statusCode,
				state.RateLimitLevel,
				state.OverloadLevel,
			)
			return
		}
		state.OverloadLevel = nextLevel
		state.CoolingUntil = laterTime(state.CoolingUntil, now.Add(cooldown))
		DebugLogf(
			"claude api pool route result auth=%s model=%s status=%d cooldown_ms=%d rate_level=%d overload_level=%d cooling_until=%s retry_after_ms=%d",
			debugAuthRef(authID),
			model,
			statusCode,
			debugDurationMS(cooldown),
			state.RateLimitLevel,
			state.OverloadLevel,
			debugTime(state.CoolingUntil),
			debugRetryAfterMS(retryAfter),
		)
	default:
		if statusCode >= 200 && statusCode < 300 {
			state.RateLimitLevel = 0
			state.OverloadLevel = 0
		}
		DebugLogf(
			"claude api pool route result auth=%s model=%s status=%d rate_level=%d overload_level=%d cooling_until=%s",
			debugAuthRef(authID),
			model,
			statusCode,
			state.RateLimitLevel,
			state.OverloadLevel,
			debugTime(state.CoolingUntil),
		)
	}
}

func (r *poolRouter) status(authID, model string, policy EffectiveRoutingConfig, now time.Time) RouteStatus {
	if r == nil || strings.TrimSpace(authID) == "" {
		return RouteStatus{}
	}
	key := routeKey(authID, model)
	accountKey := accountRouteKey(authID)
	r.mu.Lock()
	defer r.mu.Unlock()
	modelState := r.states[key]
	accountState := r.accountStates[accountKey]
	policy = normalizeEffectiveRoutingConfig(policy)
	if accountState != nil {
		r.pruneRecentStartsLocked(accountState, now)
	}
	r.pruneSessionsLocked(now)
	status := statusFromRouteStates(accountState, modelState, policy, now)
	status.ActiveSessions = r.activeSessionsForAuthLocked(authID)
	status.Waiters = r.waiters[authID]
	return status
}

func (r *poolRouter) aggregateStatus(authID string, policy EffectiveRoutingConfig, now time.Time) RouteStatus {
	authID = strings.TrimSpace(authID)
	if r == nil || authID == "" {
		return RouteStatus{}
	}
	prefix := authID + "\x00"
	policy = normalizeEffectiveRoutingConfig(policy)
	r.mu.Lock()
	defer r.mu.Unlock()
	accountState := r.accountStates[accountRouteKey(authID)]
	if accountState != nil {
		r.pruneRecentStartsLocked(accountState, now)
	}
	out := statusFromRouteStates(accountState, nil, policy, now)
	r.pruneSessionsLocked(now)
	out.ActiveSessions = r.activeSessionsForAuthLocked(authID)
	out.MaxSessions = policy.MaxSessions
	out.Waiters = r.waiters[authID]
	for key, state := range r.states {
		if state == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		if state.CoolingUntil.After(now) {
			out.Cooling = true
			out.ModelCooling = true
			out.ModelCoolingCount++
			out.CoolingTo = laterTime(out.CoolingTo, state.CoolingUntil)
		}
	}
	return out
}

// AggregateRouteStatuses returns route pressure for a set of auth IDs.
func AggregateRouteStatuses(authIDs []string) []RouteStatus {
	out := make([]RouteStatus, 0, len(authIDs))
	for _, authID := range authIDs {
		out = append(out, AggregateRouteStatus(authID))
	}
	return out
}

func (r *poolRouter) reset(authID string) {
	if r == nil {
		return
	}
	authID = strings.TrimSpace(authID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if authID == "" {
		clear(r.states)
		clear(r.accountStates)
		clear(r.sessions)
		clear(r.activeSessions)
		clear(r.lastSelected)
		r.signalChangedLocked()
		return
	}
	prefix := authID + "\x00"
	if state := r.accountStates[accountRouteKey(authID)]; state != nil {
		state.CoolingUntil = time.Time{}
		state.ManualBlocked = false
		state.RateLimitLevel = 0
		state.OverloadLevel = 0
		state.RecentStarts = nil
		state.UpdatedAt = time.Now()
	}
	for key, state := range r.states {
		if strings.HasPrefix(key, prefix) {
			if state == nil {
				delete(r.states, key)
				continue
			}
			state.CoolingUntil = time.Time{}
			state.RateLimitLevel = 0
			state.OverloadLevel = 0
			state.RecentStarts = nil
			state.UpdatedAt = time.Now()
		}
	}
	r.signalChangedLocked()
}

func (l *RouteLease) Release() {
	if l == nil || l.router == nil || l.key == "" {
		return
	}
	l.once.Do(func() {
		l.router.mu.Lock()
		defer l.router.mu.Unlock()
		state := l.router.accountStates[l.key]
		if state == nil {
			return
		}
		if state.InFlight > 0 {
			state.InFlight--
		}
		state.UpdatedAt = time.Now()
		l.router.signalChangedLocked()
		DebugLogf("claude api pool route released key=%s inflight=%d", debugAuthRef(l.key), state.InFlight)
	})
}

func debugRetryAfterMS(retryAfter *time.Duration) int64 {
	if retryAfter == nil {
		return 0
	}
	return debugDurationMS(*retryAfter)
}

func (r *poolRouter) ensureStateLocked(key string) *routeState {
	state := r.states[key]
	if state == nil {
		state = &routeState{}
		r.states[key] = state
	}
	return state
}

func (r *poolRouter) ensureAccountStateLocked(key string) *routeState {
	state := r.accountStates[key]
	if state == nil {
		state = &routeState{}
		r.accountStates[key] = state
	}
	return state
}

func (r *poolRouter) pruneRecentStartsLocked(state *routeState, now time.Time) {
	if state == nil || len(state.RecentStarts) == 0 {
		return
	}
	cutoff := now.Add(-routeWindow)
	firstKept := 0
	for firstKept < len(state.RecentStarts) && !state.RecentStarts[firstKept].After(cutoff) {
		firstKept++
	}
	if firstKept == 0 {
		return
	}
	if firstKept >= len(state.RecentStarts) {
		state.RecentStarts = nil
		return
	}
	copy(state.RecentStarts, state.RecentStarts[firstKept:])
	state.RecentStarts = state.RecentStarts[:len(state.RecentStarts)-firstKept]
}

func statusFromRouteStates(accountState, modelState *routeState, policy EffectiveRoutingConfig, now time.Time) RouteStatus {
	status := RouteStatus{
		RPMLimit:    routeRPMLimit(policy, true),
		MaxSessions: policy.MaxSessions,
	}
	if accountState != nil {
		status.InFlight = accountState.InFlight
		status.RPMUsed = len(accountState.RecentStarts)
		if limit := routeConcurrencyLimit(policy, false); limit > 0 && accountState.InFlight >= int64(limit) {
			status.Unavailable = true
		}
		if status.RPMLimit > 0 && len(accountState.RecentStarts) >= status.RPMLimit {
			status.Unavailable = true
		}
		if accountState.ManualBlocked || accountState.CoolingUntil.After(now) {
			status.Cooling = true
			status.AccountCooling = true
			status.Unavailable = true
			status.CoolingTo = accountState.CoolingUntil
		}
	}
	if modelState != nil && modelState.CoolingUntil.After(now) {
		status.Cooling = true
		status.ModelCooling = true
		status.ModelCoolingCount = 1
		status.Unavailable = true
		status.CoolingTo = modelState.CoolingUntil
	}
	return status
}

func cooldownForStatus(statusCode int, backoffLevel int, retryAfter *time.Duration, policy EffectiveRoutingConfig) (time.Duration, int, bool) {
	policy = normalizeEffectiveRoutingConfig(policy)
	if backoffLevel < 0 {
		backoffLevel = 0
	}
	if retryAfter != nil && *retryAfter > 0 {
		return *retryAfter, backoffLevel, true
	}
	var base time.Duration
	var maxValue time.Duration
	switch statusCode {
	case StatusTooManyRequests:
		base = time.Duration(policy.RateLimitCooldownMS) * time.Millisecond
		maxValue = time.Duration(policy.RateLimitMaxCooldownMS) * time.Millisecond
	case StatusOverloaded:
		base = time.Duration(policy.OverloadCooldownMS) * time.Millisecond
		maxValue = time.Duration(policy.OverloadMaxCooldownMS) * time.Millisecond
	default:
		return 0, backoffLevel, false
	}
	if base <= 0 {
		return 0, backoffLevel, false
	}
	if maxValue < base {
		maxValue = base
	}
	multiplier := math.Pow(2, float64(backoffLevel))
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 1 {
		multiplier = 1
	}
	cooldown := time.Duration(float64(base) * multiplier)
	if cooldown <= 0 || cooldown > maxValue {
		return maxValue, backoffLevel, true
	}
	return cooldown, backoffLevel + 1, true
}

func normalizeEffectiveRoutingConfig(cfg EffectiveRoutingConfig) EffectiveRoutingConfig {
	normalized := EffectiveRouting(RoutingConfigFromEffective(cfg))
	if cfg.StickyConcurrencyReserve > 0 {
		normalized.StickyConcurrencyReserve = cfg.StickyConcurrencyReserve
	}
	return normalized
}

func routeConcurrencyLimit(policy EffectiveRoutingConfig, sticky bool) int {
	limit := policy.PerAccountConcurrency
	if sticky && policy.StickyConcurrencyReserve > 0 {
		limit += policy.StickyConcurrencyReserve
	}
	return limit
}

func routeRPMLimit(policy EffectiveRoutingConfig, sticky bool) int {
	return policy.PerAccountRPM
}

func routeKey(authID, model string) string {
	return strings.TrimSpace(authID) + "\x00" + strings.TrimSpace(model)
}

func accountRouteKey(authID string) string {
	return strings.TrimSpace(authID)
}

func laterTime(a, b time.Time) time.Time {
	if a.IsZero() || b.After(a) {
		return b
	}
	return a
}
