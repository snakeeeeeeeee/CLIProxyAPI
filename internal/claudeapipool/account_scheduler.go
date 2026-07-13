package claudeapipool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
)

const sessionHashBytes = 8

const (
	QuotaBandUnknown   = "unknown"
	QuotaBandNormal    = "normal"
	QuotaBandDegraded  = "degraded"
	QuotaBandDrainOnly = "drain_only"
	QuotaBandExhausted = "exhausted"
)

// AccountQuotaRoutingState is one fresh, model-aware quota decision projected
// from persisted OAuth usage and Anthropic response headers.
type AccountQuotaRoutingState struct {
	Headroom    float64
	UsedPercent float64
	Band        string
	Window      string
	ResetAt     time.Time
	ExpiresAt   time.Time
}

// AccountRouteCandidate describes an account that has already passed provider,
// proxy, model, and account-level eligibility checks.
type AccountRouteCandidate struct {
	AuthID   string
	ProxyID  string
	Priority int
	Policy   EffectiveRoutingConfig
}

// AccountRouteDescriptor contains account-independent request routing hints.
type AccountRouteDescriptor struct {
	Model             string
	SessionKey        string
	PrefixFingerprint string
}

// AccountRouteSelection describes the atomic account/capacity decision.
type AccountRouteSelection struct {
	AuthID           string
	SessionHash      string
	AffinityMode     string
	PrimaryHit       bool
	BackupLane       bool
	Sticky           bool
	Waited           time.Duration
	SwitchCount      int
	RejectReason     string
	InFlight         int64
	ConcurrencyLimit int
	RPMUsed          int
	RPMLimit         int
	ActiveSessions   int
	MaxSessions      int
	Waiters          int
	QuotaBand        string
	QuotaHeadroom    float64
	QuotaWindow      string
	QuotaResetAt     time.Time
	RetryAfter       time.Duration
}

// AcquireScopedAccountRoute atomically selects an account and reserves its
// concurrency slot plus the first upstream attempt.
func AcquireScopedAccountRoute(ctx context.Context, scope string, descriptor AccountRouteDescriptor, candidates []AccountRouteCandidate, tried map[string]struct{}) (*RouteLease, AccountRouteSelection) {
	router, basePolicy := scopedRouterAndPolicy(scope)
	if router == nil || len(candidates) == 0 {
		return nil, AccountRouteSelection{RejectReason: "no_candidate"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	descriptor.Model = strings.TrimSpace(descriptor.Model)
	descriptor.SessionKey = strings.TrimSpace(descriptor.SessionKey)
	descriptor.PrefixFingerprint = strings.TrimSpace(descriptor.PrefixFingerprint)
	started := time.Now()
	waitLimit := time.Duration(basePolicy.FallbackWaitMS) * time.Millisecond
	if descriptor.SessionKey != "" {
		waitLimit = time.Duration(basePolicy.StickyWaitMS) * time.Millisecond
	}
	deadline := started.Add(waitLimit)
	queuedAuth := ""
	queued := false
	lastSelection := AccountRouteSelection{}
	defer func() {
		if queued {
			router.removeWaiter(queuedAuth)
		}
	}()

	for {
		now := time.Now()
		router.mu.Lock()
		selection, lease, waitAuth, waitable := router.selectAndAcquireLocked(descriptor, candidates, tried, basePolicy, now)
		selection.SwitchCount = len(tried)
		lastSelection = selection
		if lease != nil {
			if queued {
				router.removeWaiterLocked(queuedAuth)
				queued = false
			}
			selection.Waited = now.Sub(started)
			lease.selection = selection
			router.mu.Unlock()
			return lease, selection
		}
		if !waitable || waitLimit <= 0 || !now.Before(deadline) {
			if queued {
				router.removeWaiterLocked(queuedAuth)
				queued = false
			}
			selection.Waited = now.Sub(started)
			router.mu.Unlock()
			return nil, selection
		}
		if !queued {
			if router.globalWaiters >= basePolicy.MaxWaitersGlobal || router.waiters[waitAuth] >= basePolicy.MaxWaitersPerAccount {
				selection.RejectReason = "queue_full"
				selection.Waited = now.Sub(started)
				router.mu.Unlock()
				return nil, selection
			}
			router.globalWaiters++
			router.waiters[waitAuth]++
			queuedAuth = waitAuth
			queued = true
		}
		changed := router.changed
		router.mu.Unlock()

		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, AccountRouteSelection{RejectReason: "cancelled", Waited: time.Since(started)}
		case <-timer.C:
			lastSelection.Waited = time.Since(started)
			if lastSelection.RejectReason == "" {
				lastSelection.RejectReason = "wait_timeout"
			}
			return nil, lastSelection
		case <-changed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

func (r *poolRouter) selectAndAcquireLocked(descriptor AccountRouteDescriptor, candidates []AccountRouteCandidate, tried map[string]struct{}, basePolicy EffectiveRoutingConfig, now time.Time) (AccountRouteSelection, *RouteLease, string, bool) {
	r.pruneSessionsLocked(now)
	sessionHash := shortSessionHash(descriptor.SessionKey)
	primaryAuth := ""
	if sessionHash != "" {
		if binding := r.sessions[sessionHash]; binding != nil && binding.ExpiresAt.After(now) {
			primaryAuth = binding.AuthID
		}
	}
	for i := range candidates {
		candidates[i].Policy = mergeRoutingOverride(basePolicy, candidates[i].Policy)
	}
	ordered := r.orderCandidatesLocked(descriptor, candidates, primaryAuth, now)
	selection := AccountRouteSelection{SessionHash: sessionHash, AffinityMode: "load"}
	if sessionHash != "" {
		selection.AffinityMode = "session"
	}
	if descriptor.PrefixFingerprint != "" && sessionHash != "" {
		selection.AffinityMode = "session_prefix"
	}
	waitAuth := ""
	waitable := false
	rejectReason := "no_candidate"
	quotaRetryAfter := time.Duration(0)
	for _, candidate := range ordered {
		authID := strings.TrimSpace(candidate.AuthID)
		if authID == "" {
			continue
		}
		if _, used := tried[authID]; used {
			continue
		}
		proxyID := strings.TrimSpace(candidate.ProxyID)
		if coolingUntil := r.proxyCooling[proxyID]; proxyID != "" && coolingUntil.After(now) {
			rejectReason = "proxy_cooling"
			continue
		}
		policy := mergeRoutingOverride(basePolicy, candidate.Policy)
		accountState := r.ensureAccountStateLocked(accountRouteKey(authID))
		modelState := r.ensureStateLocked(routeKey(authID, descriptor.Model))
		r.pruneRecentStartsLocked(accountState, now)
		sticky := primaryAuth != "" && authID == primaryAuth
		selection.AuthID = authID
		selection.PrimaryHit = sticky
		selection.Sticky = sticky
		selection.InFlight = accountState.InFlight
		selection.RPMUsed = len(accountState.RecentStarts)
		selection.RPMLimit = routeRPMLimit(policy, sticky)
		selection.ConcurrencyLimit = routeConcurrencyLimit(policy, sticky)
		selection.ActiveSessions = r.activeSessionsForAuthLocked(authID)
		selection.MaxSessions = policy.MaxSessions
		if accountState.ManualBlocked || accountState.CoolingUntil.After(now) {
			rejectReason = "auth_blocked"
			continue
		}
		if modelState.CoolingUntil.After(now) {
			rejectReason = "cooling"
			continue
		}
		quota, quotaKnown := r.accountQuotaLocked(authID, descriptor.Model, now)
		if quotaKnown {
			selection.QuotaBand = quota.Band
			selection.QuotaHeadroom = quota.Value
			selection.QuotaWindow = quota.Window
			selection.QuotaResetAt = quota.ResetAt
			switch quota.Band {
			case QuotaBandExhausted:
				if retryAfter := quotaRoutingRetryAfter(quota, now); retryAfter > 0 && (quotaRetryAfter == 0 || retryAfter < quotaRetryAfter) {
					quotaRetryAfter = retryAfter
				}
				rejectReason = "quota_exhausted"
				continue
			}
		}
		activeSessions := r.activeSessionsForAuthLocked(authID)
		activeOnCandidate := false
		if active := r.activeSessions[sessionHash]; sessionHash != "" && active != nil && active.ExpiresAt.After(now) {
			activeOnCandidate = active.AuthID == authID
		}
		if sessionHash != "" && !activeOnCandidate && policy.MaxSessions > 0 && activeSessions >= policy.MaxSessions {
			rejectReason = "session_full"
			if waitAuth == "" {
				waitAuth = authID
			}
			waitable = true
			continue
		}
		rpmLimit := routeRPMLimit(policy, sticky)
		if rpmLimit > 0 && len(accountState.RecentStarts) >= rpmLimit {
			rejectReason = "rpm_full"
			if waitAuth == "" {
				waitAuth = authID
			}
			waitable = true
			continue
		}
		concurrencyLimit := routeConcurrencyLimit(policy, sticky)
		if concurrencyLimit > 0 && accountState.InFlight >= int64(concurrencyLimit) {
			rejectReason = "concurrency_full"
			if waitAuth == "" {
				waitAuth = authID
			}
			waitable = true
			continue
		}

		accountState.InFlight++
		accountState.RecentStarts = append(accountState.RecentStarts, now)
		accountState.UpdatedAt = now
		r.lastSelected[authID] = now
		if sessionHash != "" {
			ttl := time.Duration(policy.SessionAffinityTTLMS) * time.Millisecond
			if ttl <= 0 {
				ttl = time.Hour
			}
			if primaryAuth == "" {
				r.sessions[sessionHash] = &sessionBinding{AuthID: authID, ExpiresAt: now.Add(ttl)}
				primaryAuth = authID
				sticky = true
			} else if authID == primaryAuth {
				r.sessions[sessionHash].ExpiresAt = now.Add(ttl)
			}
			activeTTL := time.Duration(policy.ActiveSessionIdleTTLMS) * time.Millisecond
			if activeTTL <= 0 {
				activeTTL = 5 * time.Minute
			}
			r.activeSessions[sessionHash] = &activeSession{AuthID: authID, ExpiresAt: now.Add(activeTTL)}
		}
		selection.AuthID = authID
		selection.PrimaryHit = sessionHash != "" && authID == primaryAuth
		selection.BackupLane = sessionHash != "" && !selection.PrimaryHit
		selection.Sticky = sticky
		selection.InFlight = accountState.InFlight
		selection.ConcurrencyLimit = concurrencyLimit
		selection.RPMUsed = len(accountState.RecentStarts)
		selection.RPMLimit = rpmLimit
		selection.ActiveSessions = r.activeSessionsForAuthLocked(authID)
		selection.MaxSessions = policy.MaxSessions
		selection.Waiters = r.waiters[authID]
		lease := &RouteLease{router: r, key: authID, model: descriptor.Model, policy: policy, selection: selection, attempts: 1}
		return selection, lease, "", false
	}
	selection.RejectReason = rejectReason
	selection.Waiters = r.waiters[waitAuth]
	selection.RetryAfter = quotaRetryAfter
	return selection, nil, waitAuth, waitable
}

func (r *poolRouter) orderCandidatesLocked(descriptor AccountRouteDescriptor, candidates []AccountRouteCandidate, primaryAuth string, now time.Time) []AccountRouteCandidate {
	ordered := append([]AccountRouteCandidate(nil), candidates...)
	prefixKey := ""
	if descriptor.SessionKey != "" && descriptor.PrefixFingerprint != "" {
		prefixKey = shortSessionHash(descriptor.SessionKey + "\x00" + descriptor.Model + "\x00" + descriptor.PrefixFingerprint)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if primaryAuth != "" {
			if a.AuthID == primaryAuth && b.AuthID != primaryAuth {
				return true
			}
			if b.AuthID == primaryAuth && a.AuthID != primaryAuth {
				return false
			}
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		ap := int(math.Floor(r.candidatePressureLocked(a, now) * 4))
		bp := int(math.Floor(r.candidatePressureLocked(b, now) * 4))
		if ap != bp {
			return ap < bp
		}
		ah, ak := r.accountHeadroomLocked(a.AuthID, descriptor.Model, now)
		bh, bk := r.accountHeadroomLocked(b.AuthID, descriptor.Model, now)
		if !ak {
			ah = 0.5
		}
		if !bk {
			bh = 0.5
		}
		if ah != bh {
			return ah > bh
		}
		if prefixKey != "" {
			aScore := hashAffinityScore(prefixKey, a.AuthID)
			bScore := hashAffinityScore(prefixKey, b.AuthID)
			if aScore != bScore {
				return aScore > bScore
			}
		}
		at, bt := r.lastSelected[a.AuthID], r.lastSelected[b.AuthID]
		if !at.Equal(bt) {
			return at.Before(bt)
		}
		return a.AuthID < b.AuthID
	})
	return ordered
}

// UpdateScopedAccountHeadroom keeps compatibility with callers that only have
// a scalar weight. New account-pool code should use UpdateScopedAccountQuotaRouting.
func UpdateScopedAccountHeadroom(scope, authID string, values map[string]float64, expiresAt time.Time) {
	states := make(map[string]AccountQuotaRoutingState, len(values))
	for key, value := range values {
		states[key] = AccountQuotaRoutingState{
			Headroom:    value,
			UsedPercent: (1 - value) * 100,
			Band:        QuotaBandNormal,
			ExpiresAt:   expiresAt,
		}
	}
	UpdateScopedAccountQuotaRouting(scope, authID, states)
}

// UpdateScopedAccountQuotaRouting updates hard exhaustion and headroom ordering.
func UpdateScopedAccountQuotaRouting(scope, authID string, values map[string]AccountQuotaRoutingState) {
	router, _ := scopedRouterAndPolicy(scope)
	authID = strings.TrimSpace(authID)
	if router == nil || authID == "" {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(values) == 0 {
		delete(router.headrooms, authID)
		router.signalChangedLocked()
		return
	}
	now := time.Now()
	snapshots := make(map[string]headroomSnapshot, len(values))
	for key, value := range values {
		if value.ExpiresAt.IsZero() || !value.ExpiresAt.After(now) {
			continue
		}
		if value.Headroom < 0 {
			value.Headroom = 0
		}
		if value.Headroom > 1 {
			value.Headroom = 1
		}
		value.UsedPercent = math.Max(0, math.Min(100, value.UsedPercent))
		value.Band = normalizeQuotaBand(value.Band)
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		incoming := headroomSnapshot{
			Value:       value.Headroom,
			UsedPercent: value.UsedPercent,
			Band:        value.Band,
			Window:      strings.TrimSpace(value.Window),
			ResetAt:     value.ResetAt,
			ExpiresAt:   value.ExpiresAt,
		}
		snapshots[normalizedKey] = incoming
	}
	if len(snapshots) == 0 {
		delete(router.headrooms, authID)
	} else {
		router.headrooms[authID] = snapshots
	}
	router.signalChangedLocked()
}

func (r *poolRouter) accountHeadroomLocked(authID, model string, now time.Time) (float64, bool) {
	snapshot, ok := r.accountQuotaLocked(authID, model, now)
	if !ok {
		return 0, false
	}
	return snapshot.Value, true
}

func (r *poolRouter) accountQuotaLocked(authID, model string, now time.Time) (headroomSnapshot, bool) {
	values := r.headrooms[strings.TrimSpace(authID)]
	if len(values) == 0 {
		return headroomSnapshot{}, false
	}
	keys := []string{""}
	if family := quotaModelFamily(model); family != "" {
		keys = append([]string{family}, keys...)
	}
	for _, key := range keys {
		if snapshot, ok := values[key]; ok && snapshot.ExpiresAt.After(now) {
			return snapshot, true
		}
	}
	return headroomSnapshot{}, false
}

func quotaModelFamily(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, family := range []string{"fable", "sonnet", "opus", "haiku"} {
		if strings.Contains(model, family) {
			return family
		}
	}
	return ""
}

func normalizeQuotaBand(band string) string {
	switch strings.ToLower(strings.TrimSpace(band)) {
	case QuotaBandNormal:
		return QuotaBandNormal
	case QuotaBandDegraded:
		return QuotaBandDegraded
	case QuotaBandDrainOnly:
		return QuotaBandDegraded
	case QuotaBandExhausted:
		return QuotaBandExhausted
	default:
		return QuotaBandUnknown
	}
}

func quotaRoutingRetryAfter(snapshot headroomSnapshot, now time.Time) time.Duration {
	if snapshot.ResetAt.After(now) {
		remaining := snapshot.ResetAt.Sub(now)
		if remaining < time.Second {
			return time.Second
		}
		if remaining > time.Minute {
			return time.Minute
		}
		return remaining
	}
	return 30 * time.Second
}

// ClearScopedAccountQuotaRouting removes persisted quota projections for a deleted account.
func ClearScopedAccountQuotaRouting(scope, authID string) {
	router, _ := scopedRouterAndPolicy(scope)
	authID = strings.TrimSpace(authID)
	if router == nil || authID == "" {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	delete(router.headrooms, authID)
	router.signalChangedLocked()
}

func (r *poolRouter) candidatePressureLocked(candidate AccountRouteCandidate, now time.Time) float64 {
	policy := normalizeEffectiveRoutingConfig(candidate.Policy)
	state := r.accountStates[accountRouteKey(candidate.AuthID)]
	concurrencyPressure := 0.0
	if state != nil && policy.PerAccountConcurrency > 0 {
		concurrencyPressure = float64(state.InFlight) / float64(policy.PerAccountConcurrency)
	}
	rpmPressure := 0.0
	if state != nil {
		r.pruneRecentStartsLocked(state, now)
	}
	if state != nil && policy.PerAccountRPM > 0 {
		rpmPressure = float64(len(state.RecentStarts)) / float64(policy.PerAccountRPM)
	}
	sessionPressure := 0.0
	if policy.MaxSessions > 0 {
		sessionPressure = float64(r.activeSessionsForAuthLocked(candidate.AuthID)) / float64(policy.MaxSessions)
	}
	return math.Max(math.Max(concurrencyPressure, rpmPressure), sessionPressure)
}

func mergeRoutingOverride(base, override EffectiveRoutingConfig) EffectiveRoutingConfig {
	policy := normalizeEffectiveRoutingConfig(base)
	if override.PerAccountRPM > 0 {
		policy.PerAccountRPM = override.PerAccountRPM
	}
	if override.PerAccountConcurrency > 0 {
		policy.PerAccountConcurrency = override.PerAccountConcurrency
	}
	if override.StickyConcurrencyReserve > 0 {
		policy.StickyConcurrencyReserve = override.StickyConcurrencyReserve
	}
	if override.MaxSessions > 0 {
		policy.MaxSessions = override.MaxSessions
	}
	return policy
}

// TryStartAttempt consumes RPM for another real upstream attempt while keeping
// the existing concurrency lease.
func (l *RouteLease) TryStartAttempt() bool {
	if l == nil || l.router == nil || l.key == "" {
		return true
	}
	r := l.router
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.ensureAccountStateLocked(l.key)
	now := time.Now()
	r.pruneRecentStartsLocked(state, now)
	if limit := routeRPMLimit(l.policy, l.selection.Sticky); limit > 0 && len(state.RecentStarts) >= limit {
		return false
	}
	state.RecentStarts = append(state.RecentStarts, now)
	state.UpdatedAt = now
	l.attempts++
	return true
}

// Attempts reports the number of actual upstream attempts charged to this lease.
func (l *RouteLease) Attempts() int {
	if l == nil || l.router == nil {
		return 0
	}
	l.router.mu.Lock()
	defer l.router.mu.Unlock()
	return l.attempts
}

// Promote marks a successful backup lane as the session's new primary.
func (l *RouteLease) Promote() {
	if l == nil || l.router == nil || l.selection.SessionHash == "" || l.key == "" {
		return
	}
	r := l.router
	r.mu.Lock()
	defer r.mu.Unlock()
	ttl := time.Duration(l.policy.SessionAffinityTTLMS) * time.Millisecond
	if ttl <= 0 {
		ttl = time.Hour
	}
	r.sessions[l.selection.SessionHash] = &sessionBinding{AuthID: l.key, ExpiresAt: time.Now().Add(ttl)}
	r.signalChangedLocked()
}

// Selection returns the immutable decision captured when the lease was acquired.
func (l *RouteLease) Selection() AccountRouteSelection {
	if l == nil {
		return AccountRouteSelection{}
	}
	return l.selection
}

// ClearScopedAccountBindings removes every session primary that references an account.
func ClearScopedAccountBindings(scope, authID string) {
	router, _ := scopedRouterAndPolicy(scope)
	if router == nil {
		return
	}
	authID = strings.TrimSpace(authID)
	router.mu.Lock()
	defer router.mu.Unlock()
	for key, binding := range router.sessions {
		if binding == nil || binding.AuthID == authID {
			delete(router.sessions, key)
		}
	}
	for key, session := range router.activeSessions {
		if session == nil || session.AuthID == authID {
			delete(router.activeSessions, key)
		}
	}
	router.signalChangedLocked()
}

// BlockScopedProxy temporarily removes every candidate using a proxy.
func BlockScopedProxy(scope, proxyID string, cooldown time.Duration) {
	router, _ := scopedRouterAndPolicy(scope)
	proxyID = strings.TrimSpace(proxyID)
	if router == nil || proxyID == "" || cooldown <= 0 {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	router.proxyCooling[proxyID] = time.Now().Add(cooldown)
	router.signalChangedLocked()
}

// ClearScopedProxyBlock restores a proxy after manual recovery or a healthy check.
func ClearScopedProxyBlock(scope, proxyID string) {
	router, _ := scopedRouterAndPolicy(scope)
	proxyID = strings.TrimSpace(proxyID)
	if router == nil || proxyID == "" {
		return
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	delete(router.proxyCooling, proxyID)
	router.signalChangedLocked()
}

// ScopedAccountAffinityStats returns active explicit-session bindings for a pool.
func ScopedAccountAffinityStats(scope string) (int, int) {
	router, _ := scopedRouterAndPolicy(scope)
	if router == nil {
		return 0, 0
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	router.pruneSessionsLocked(time.Now())
	active := len(router.sessions)
	return active, active
}

// ScopedAccountBindingCount returns the number of unexpired affinity bindings for one account.
func ScopedAccountBindingCount(scope, authID string) int {
	router, _ := scopedRouterAndPolicy(scope)
	if router == nil {
		return 0
	}
	authID = strings.TrimSpace(authID)
	router.mu.Lock()
	defer router.mu.Unlock()
	router.pruneSessionsLocked(time.Now())
	count := 0
	for _, binding := range router.sessions {
		if binding != nil && binding.AuthID == authID {
			count++
		}
	}
	return count
}

func (r *poolRouter) pruneSessionsLocked(now time.Time) {
	for key, binding := range r.sessions {
		if binding == nil || !binding.ExpiresAt.After(now) {
			delete(r.sessions, key)
		}
	}
	for key, session := range r.activeSessions {
		if session == nil || !session.ExpiresAt.After(now) {
			delete(r.activeSessions, key)
		}
	}
	for proxyID, coolingUntil := range r.proxyCooling {
		if !coolingUntil.After(now) {
			delete(r.proxyCooling, proxyID)
		}
	}
}

func (r *poolRouter) activeSessionsForAuthLocked(authID string) int {
	count := 0
	for _, session := range r.activeSessions {
		if session != nil && session.AuthID == authID {
			count++
		}
	}
	return count
}

func (r *poolRouter) removeWaiter(authID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeWaiterLocked(authID)
}

func (r *poolRouter) removeWaiterLocked(authID string) {
	if r.globalWaiters > 0 {
		r.globalWaiters--
	}
	if r.waiters[authID] > 1 {
		r.waiters[authID]--
	} else {
		delete(r.waiters, authID)
	}
}

func (r *poolRouter) signalChangedLocked() {
	if r.changed != nil {
		close(r.changed)
	}
	r.changed = make(chan struct{})
}

func shortSessionHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:sessionHashBytes])
}
