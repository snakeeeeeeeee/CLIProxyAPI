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

var routingPolicyMu sync.RWMutex

type poolRouter struct {
	mu            sync.Mutex
	states        map[string]*routeState
	accountStates map[string]*routeState
}

type routeState struct {
	InFlight       int64
	RecentStarts   []time.Time
	CoolingUntil   time.Time
	RateLimitLevel int
	OverloadLevel  int
	UpdatedAt      time.Time
}

// RouteLease releases one in-flight pool route slot.
type RouteLease struct {
	router *poolRouter
	key    string
	once   sync.Once
}

// RouteStatus summarizes local pool route state for one auth/model.
type RouteStatus struct {
	InFlight    int64     `json:"in_flight"`
	RPMUsed     int       `json:"rpm_used"`
	RPMLimit    int       `json:"rpm_limit"`
	Cooling     bool      `json:"cooling"`
	CoolingTo   time.Time `json:"cooling_until,omitempty"`
	Unavailable bool      `json:"unavailable"`
}

// SetRoutingConfig updates the runtime policy used by future pool routing.
func SetRoutingConfig(cfg EffectiveRoutingConfig) {
	routingPolicyMu.Lock()
	defer routingPolicyMu.Unlock()
	defaultRoutingPolicy = normalizeEffectiveRoutingConfig(cfg)
	DebugLogf(
		"claude api pool routing config rpm=%d concurrency=%d max_switches=%d switch_delay_ms=%d rate_cooldown_ms=%d rate_max_cooldown_ms=%d overload_cooldown_ms=%d overload_max_cooldown_ms=%d same_retry_429=%d same_retry_529=%d same_retry_delay_ms=%d",
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
	)
}

// CurrentRoutingConfig returns the runtime pool routing policy.
func CurrentRoutingConfig() EffectiveRoutingConfig {
	routingPolicyMu.RLock()
	defer routingPolicyMu.RUnlock()
	return defaultRoutingPolicy
}

// TryAcquireRoute reserves local capacity for one upstream pool request.
func TryAcquireRoute(authID, model string) (*RouteLease, bool) {
	return defaultRouting.tryAcquire(authID, model, CurrentRoutingConfig(), time.Now())
}

// NoteRouteResult updates local pool routing state from an upstream status.
func NoteRouteResult(authID, model string, statusCode int, retryAfter *time.Duration) {
	defaultRouting.noteResult(authID, model, statusCode, retryAfter, CurrentRoutingConfig(), time.Now())
}

// RouteStatusFor returns local route pressure for one auth/model.
func RouteStatusFor(authID, model string) RouteStatus {
	return defaultRouting.status(authID, model, CurrentRoutingConfig(), time.Now())
}

// AggregateRouteStatus returns local route pressure across all models for one auth.
func AggregateRouteStatus(authID string) RouteStatus {
	return defaultRouting.aggregateStatus(authID, CurrentRoutingConfig(), time.Now())
}

// ResetRouteCooling clears local pool limiter/cooldown state.
func ResetRouteCooling(authID string) {
	defaultRouting.reset(authID)
}

// CooldownForStatus returns the pool-specific cooldown for 429/529.
func CooldownForStatus(statusCode int, backoffLevel int, retryAfter *time.Duration) (time.Duration, int, bool) {
	return cooldownForStatus(statusCode, backoffLevel, retryAfter, CurrentRoutingConfig())
}

// CrossAccountAttempts returns the max credential attempts implied by max-switches.
func CrossAccountAttempts() int {
	cfg := CurrentRoutingConfig()
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

// SwitchDelay returns the configured delay before trying another pool account.
func SwitchDelay() time.Duration {
	cfg := CurrentRoutingConfig()
	if cfg.SwitchDelayMS <= 0 {
		return 0
	}
	return time.Duration(cfg.SwitchDelayMS) * time.Millisecond
}

func newPoolRouter() *poolRouter {
	return &poolRouter{
		states:        make(map[string]*routeState),
		accountStates: make(map[string]*routeState),
	}
}

func (r *poolRouter) tryAcquire(authID, model string, policy EffectiveRoutingConfig, now time.Time) (*RouteLease, bool) {
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
	if policy.PerAccountConcurrency > 0 && accountState.InFlight >= int64(policy.PerAccountConcurrency) {
		DebugLogf(
			"claude api pool route denied auth=%s model=%s reason=concurrency inflight=%d concurrency_limit=%d rpm_used=%d rpm_limit=%d",
			debugAuthRef(authID),
			model,
			accountState.InFlight,
			policy.PerAccountConcurrency,
			len(accountState.RecentStarts),
			policy.PerAccountRPM,
		)
		return nil, false
	}
	if policy.PerAccountRPM > 0 && len(accountState.RecentStarts) >= policy.PerAccountRPM {
		DebugLogf(
			"claude api pool route denied auth=%s model=%s reason=rpm inflight=%d rpm_used=%d rpm_limit=%d",
			debugAuthRef(authID),
			model,
			accountState.InFlight,
			len(accountState.RecentStarts),
			policy.PerAccountRPM,
		)
		return nil, false
	}
	accountState.InFlight++
	accountState.RecentStarts = append(accountState.RecentStarts, now)
	accountState.UpdatedAt = now
	DebugLogf(
		"claude api pool route acquired auth=%s model=%s inflight=%d concurrency_limit=%d rpm_used=%d rpm_limit=%d",
		debugAuthRef(authID),
		model,
		accountState.InFlight,
		policy.PerAccountConcurrency,
		len(accountState.RecentStarts),
		policy.PerAccountRPM,
	)
	return &RouteLease{router: r, key: accountKey}, true
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
	return statusFromRouteStates(accountState, modelState, policy, now)
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
	for key, state := range r.states {
		if state == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		if state.CoolingUntil.After(now) {
			out.Cooling = true
			out.Unavailable = true
			out.CoolingTo = laterTime(out.CoolingTo, state.CoolingUntil)
		}
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
		return
	}
	prefix := authID + "\x00"
	if state := r.accountStates[accountRouteKey(authID)]; state != nil {
		state.CoolingUntil = time.Time{}
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
		RPMLimit: policy.PerAccountRPM,
	}
	if accountState != nil {
		status.InFlight = accountState.InFlight
		status.RPMUsed = len(accountState.RecentStarts)
		if policy.PerAccountConcurrency > 0 && accountState.InFlight >= int64(policy.PerAccountConcurrency) {
			status.Unavailable = true
		}
		if policy.PerAccountRPM > 0 && len(accountState.RecentStarts) >= policy.PerAccountRPM {
			status.Unavailable = true
		}
	}
	if modelState != nil && modelState.CoolingUntil.After(now) {
		status.Cooling = true
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
	return EffectiveRouting(RoutingConfigFromEffective(cfg))
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
