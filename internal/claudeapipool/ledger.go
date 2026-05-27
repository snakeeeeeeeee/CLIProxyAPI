package claudeapipool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	defaultCacheTTL      = 5 * time.Minute
	maxCacheTTL          = time.Hour
	virtualLedgerMaxKeys = 100000
	cacheReuseWindow     = 5 * time.Minute
)

var defaultLedger = newVirtualCacheLedger(virtualLedgerMaxKeys)
var defaultVirtualCachePolicy = EffectiveVirtualCache(VirtualCacheConfig{})

var virtualCachePolicyMu sync.RWMutex

type virtualCacheLedger struct {
	mu           sync.Mutex
	entries      map[string]virtualCacheEntry
	reuseSamples []virtualCacheReuseSample
	maxKeys      int
}

type virtualCacheEntry struct {
	prefixFingerprint       string
	cached5mTokens          int64
	cached5mExpiresAt       time.Time
	cached1hTokens          int64
	cached1hExpiresAt       time.Time
	lastEstimateTokens      int64
	lastObservedInputTokens int64
	updatedAt               time.Time
}

type virtualCacheReuseSample struct {
	recordedAt               time.Time
	inputTokens              int64
	cacheReadInputTokens     int64
	cacheCreationInputTokens int64
}

// VirtualCacheReuseSnapshot summarizes recent rewritten cache usage.
type VirtualCacheReuseSnapshot struct {
	Enabled                  bool    `json:"enabled"`
	WindowSeconds            int64   `json:"window_seconds"`
	TargetRatio              float64 `json:"target_ratio"`
	ActualRatio              float64 `json:"actual_ratio"`
	InputTokens              int64   `json:"input_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	DenominatorTokens        int64   `json:"denominator_tokens"`
	SampleCount              int     `json:"sample_count"`
}

// VirtualCacheTransaction rewrites Claude usage for one request based on a
// ledger lookup taken before the upstream request completed.
type VirtualCacheTransaction struct {
	ledger                *virtualCacheLedger
	key                   string
	fingerprint           string
	ttl                   time.Duration
	policy                EffectiveVirtualCacheConfig
	estimateTokens        int64
	deltaTokens           int64
	hitTokens             int64
	priorObserved         int64
	observedTokens        int64
	inputTokens           int64
	creationTokens        int64
	cacheableBudgetTokens int64
	resetLedger           bool
	now                   time.Time
}

func newVirtualCacheLedger(maxKeys int) *virtualCacheLedger {
	if maxKeys <= 0 {
		maxKeys = virtualLedgerMaxKeys
	}
	return &virtualCacheLedger{
		entries: make(map[string]virtualCacheEntry),
		maxKeys: maxKeys,
	}
}

// BeginVirtualCache starts a local virtual cache accounting transaction.
func BeginVirtualCache(provider, model, sessionKey string, requestPayload []byte) *VirtualCacheTransaction {
	return defaultLedger.begin(provider, model, sessionKey, requestPayload, time.Now())
}

// SetVirtualCacheConfig updates the runtime policy used by future transactions.
func SetVirtualCacheConfig(cfg EffectiveVirtualCacheConfig) {
	virtualCachePolicyMu.Lock()
	defer virtualCachePolicyMu.Unlock()
	defaultVirtualCachePolicy = normalizeEffectiveVirtualCacheConfig(cfg)
	DebugLogf(
		"claude api pool virtual cache config enabled=%t mode=%s hit_rate=%.3f target_reuse=%.3f min_cache=%d max_cache=%d shrink_reset=%.3f",
		defaultVirtualCachePolicy.Enabled,
		defaultVirtualCachePolicy.Mode,
		defaultVirtualCachePolicy.HitRate,
		defaultVirtualCachePolicy.TargetCacheReuseRatio,
		defaultVirtualCachePolicy.MinCacheTokens,
		defaultVirtualCachePolicy.MaxCacheTokens,
		defaultVirtualCachePolicy.ContextShrinkResetRatio,
	)
}

// CurrentVirtualCacheConfig returns the runtime policy used by future transactions.
func CurrentVirtualCacheConfig() EffectiveVirtualCacheConfig {
	virtualCachePolicyMu.RLock()
	defer virtualCachePolicyMu.RUnlock()
	return defaultVirtualCachePolicy
}

// ClearVirtualCacheLedger removes all virtual cache accounting entries.
func ClearVirtualCacheLedger() {
	defaultLedger.clear()
}

// VirtualCacheReuseStats returns the recent reuse window used for target tuning.
func VirtualCacheReuseStats() VirtualCacheReuseSnapshot {
	policy := CurrentVirtualCacheConfig()
	return defaultLedger.reuseSnapshot(policy.TargetCacheReuseRatio, time.Now())
}

// VirtualCacheLedgerSize returns the current number of accounting entries.
func VirtualCacheLedgerSize() int {
	return defaultLedger.size(time.Now())
}

func (l *virtualCacheLedger) begin(provider, model, sessionKey string, requestPayload []byte, now time.Time) *VirtualCacheTransaction {
	if l == nil {
		return nil
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	sessionKey = strings.TrimSpace(sessionKey)
	policy := l.effectivePolicy(CurrentVirtualCacheConfig(), now)
	fingerprint, ttl, estimateTokens, deltaTokens, ok := cachePrefixFingerprint(requestPayload)
	if provider == "" || model == "" || sessionKey == "" || !ok || !policy.Enabled || policy.HitRate <= 0 {
		reason := "unknown"
		switch {
		case provider == "":
			reason = "empty_provider"
		case model == "":
			reason = "empty_model"
		case sessionKey == "":
			reason = "empty_session"
		case !ok:
			reason = "no_cache_prefix"
		case !policy.Enabled:
			reason = "disabled"
		case policy.HitRate <= 0:
			reason = "hit_rate_zero"
		}
		DebugLogf(
			"claude api pool virtual cache skip reason=%s provider=%s model=%s session=%s enabled=%t hit_rate=%.3f target_reuse=%.3f",
			reason,
			provider,
			model,
			debugShortHash(sessionKey),
			policy.Enabled,
			policy.HitRate,
			policy.TargetCacheReuseRatio,
		)
		return nil
	}
	if policy.MinCacheTokens > 0 && estimateTokens < policy.MinCacheTokens {
		DebugLogf(
			"claude api pool virtual cache skip reason=below_min_cache provider=%s model=%s session=%s estimate=%d min_cache=%d",
			provider,
			model,
			debugShortHash(sessionKey),
			estimateTokens,
			policy.MinCacheTokens,
		)
		return nil
	}
	key := strings.Join([]string{provider, model, sessionKey}, "\x00")
	tx := &VirtualCacheTransaction{
		ledger:         l,
		key:            key,
		fingerprint:    fingerprint,
		ttl:            ttl,
		policy:         policy,
		estimateTokens: estimateTokens,
		deltaTokens:    deltaTokens,
		now:            now,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
	if entry, okEntry := l.entries[key]; okEntry {
		l.expireEntryLocked(&entry, now)
		tx.resetLedger = shouldResetVirtualCacheEntry(entry, estimateTokens, policy)
		if !tx.resetLedger {
			tx.hitTokens = entry.cached5mTokens + entry.cached1hTokens
			tx.priorObserved = entry.lastObservedInputTokens
			tx.observedTokens = entry.lastObservedInputTokens
		}
	}
	DebugLogf(
		"claude api pool virtual cache begin provider=%s model=%s session=%s fingerprint=%s ttl_ms=%d estimate=%d delta=%d hit_tokens=%d prior_observed=%d reset=%t target_reuse=%.3f hit_rate=%.3f",
		provider,
		model,
		debugShortHash(sessionKey),
		debugShortHash(fingerprint),
		debugDurationMS(ttl),
		estimateTokens,
		deltaTokens,
		tx.hitTokens,
		tx.priorObserved,
		tx.resetLedger,
		policy.TargetCacheReuseRatio,
		policy.HitRate,
	)
	return tx
}

func (l *virtualCacheLedger) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.entries)
	l.reuseSamples = nil
}

func (l *virtualCacheLedger) size(now time.Time) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
	return len(l.entries)
}

func (l *virtualCacheLedger) commit(tx *VirtualCacheTransaction) {
	if l == nil || tx == nil || tx.key == "" {
		return
	}
	if tx.observedTokens <= 0 && tx.hitTokens <= 0 && tx.creationTokens <= 0 && tx.cacheableBudgetTokens <= 0 {
		return
	}
	now := time.Now()
	expiresAt := now.Add(tx.ttl)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
	if tx.inputTokens > 0 || tx.observedTokens > 0 || tx.creationTokens > 0 {
		l.recordReuseSampleLocked(tx.rewrittenInputTokens(), tx.hitTokens, tx.creationTokens, now)
	}
	if len(l.entries) >= l.maxKeys {
		l.dropOldestLocked()
	}
	entry := l.entries[tx.key]
	if tx.resetLedger {
		entry = virtualCacheEntry{}
	} else {
		l.expireEntryLocked(&entry, now)
	}
	entry.prefixFingerprint = tx.fingerprint
	entry.lastEstimateTokens = tx.estimateTokens
	entry.lastObservedInputTokens = tx.observedTokens
	if entry.lastObservedInputTokens < tx.hitTokens+tx.creationTokens {
		entry.lastObservedInputTokens = tx.hitTokens + tx.creationTokens
	}
	if entry.lastObservedInputTokens <= 0 {
		entry.lastObservedInputTokens = tx.estimateTokens
	}
	if tx.creationTokens > 0 {
		if tx.ttl == time.Hour {
			entry.cached1hTokens += tx.creationTokens
			entry.cached1hExpiresAt = expiresAt
		} else {
			entry.cached5mTokens += tx.creationTokens
			entry.cached5mExpiresAt = expiresAt
		}
	} else if tx.hitTokens > 0 {
		if !entry.cached5mExpiresAt.IsZero() {
			entry.cached5mExpiresAt = now.Add(defaultCacheTTL)
		}
		if !entry.cached1hExpiresAt.IsZero() {
			entry.cached1hExpiresAt = now.Add(maxCacheTTL)
		}
	}
	l.capEntryCachedTokensLocked(&entry, tx.cacheableBudgetTokens)
	entry.updatedAt = now
	if entry.cached5mTokens <= 0 && entry.cached1hTokens <= 0 && entry.lastObservedInputTokens <= 0 {
		delete(l.entries, tx.key)
		DebugLogf(
			"claude api pool virtual cache commit action=delete key=%s fingerprint=%s observed=%d input=%d read=%d creation=%d reset=%t",
			debugShortHash(tx.key),
			debugShortHash(tx.fingerprint),
			tx.observedTokens,
			tx.rewrittenInputTokens(),
			tx.hitTokens,
			tx.creationTokens,
			tx.resetLedger,
		)
		return
	}
	l.entries[tx.key] = entry
	DebugLogf(
		"claude api pool virtual cache commit key=%s fingerprint=%s ttl_ms=%d estimate=%d observed=%d input=%d read=%d creation=%d reset=%t cached_5m=%d cached_1h=%d expires_5m=%s expires_1h=%s samples=%d",
		debugShortHash(tx.key),
		debugShortHash(tx.fingerprint),
		debugDurationMS(tx.ttl),
		tx.estimateTokens,
		tx.observedTokens,
		tx.rewrittenInputTokens(),
		tx.hitTokens,
		tx.creationTokens,
		tx.resetLedger,
		entry.cached5mTokens,
		entry.cached1hTokens,
		debugTime(entry.cached5mExpiresAt),
		debugTime(entry.cached1hExpiresAt),
		len(l.reuseSamples),
	)
}

func (l *virtualCacheLedger) effectivePolicy(policy EffectiveVirtualCacheConfig, now time.Time) EffectiveVirtualCacheConfig {
	policy = normalizeEffectiveVirtualCacheConfig(policy)
	if l == nil {
		return policy
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneReuseSamplesLocked(now)
	return policy
}

func (l *virtualCacheLedger) reuseSnapshot(targetRatio float64, now time.Time) VirtualCacheReuseSnapshot {
	if l == nil {
		return reuseSnapshotFromSamples(nil, targetRatio)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneReuseSamplesLocked(now)
	return reuseSnapshotFromSamples(l.reuseSamples, targetRatio)
}

func (l *virtualCacheLedger) recordReuseSampleLocked(inputTokens, cacheReadTokens, cacheCreationTokens int64, now time.Time) {
	if l == nil {
		return
	}
	inputTokens = maxInt64(0, inputTokens)
	cacheReadTokens = maxInt64(0, cacheReadTokens)
	cacheCreationTokens = maxInt64(0, cacheCreationTokens)
	denominator := inputTokens + cacheReadTokens + cacheCreationTokens
	if denominator <= 0 || (cacheReadTokens <= 0 && cacheCreationTokens <= 0) {
		return
	}
	l.pruneReuseSamplesLocked(now)
	l.reuseSamples = append(l.reuseSamples, virtualCacheReuseSample{
		recordedAt:               now,
		inputTokens:              inputTokens,
		cacheReadInputTokens:     cacheReadTokens,
		cacheCreationInputTokens: cacheCreationTokens,
	})
}

func (l *virtualCacheLedger) pruneReuseSamplesLocked(now time.Time) {
	if l == nil || len(l.reuseSamples) == 0 {
		return
	}
	cutoff := now.Add(-cacheReuseWindow)
	firstKept := 0
	for firstKept < len(l.reuseSamples) && l.reuseSamples[firstKept].recordedAt.Before(cutoff) {
		firstKept++
	}
	if firstKept == 0 {
		return
	}
	if firstKept >= len(l.reuseSamples) {
		l.reuseSamples = nil
		return
	}
	copy(l.reuseSamples, l.reuseSamples[firstKept:])
	l.reuseSamples = l.reuseSamples[:len(l.reuseSamples)-firstKept]
}

func reuseSnapshotFromSamples(samples []virtualCacheReuseSample, targetRatio float64) VirtualCacheReuseSnapshot {
	var inputTokens int64
	var cacheReadTokens int64
	var cacheCreationTokens int64
	for _, sample := range samples {
		inputTokens += maxInt64(0, sample.inputTokens)
		cacheReadTokens += maxInt64(0, sample.cacheReadInputTokens)
		cacheCreationTokens += maxInt64(0, sample.cacheCreationInputTokens)
	}
	denominator := inputTokens + cacheReadTokens + cacheCreationTokens
	actualRatio := 0.0
	if denominator > 0 {
		actualRatio = float64(cacheReadTokens) / float64(denominator)
	}
	return VirtualCacheReuseSnapshot{
		Enabled:                  targetRatio > 0,
		WindowSeconds:            int64(cacheReuseWindow / time.Second),
		TargetRatio:              clampRatio(targetRatio),
		ActualRatio:              actualRatio,
		InputTokens:              inputTokens,
		CacheReadInputTokens:     cacheReadTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		DenominatorTokens:        denominator,
		SampleCount:              len(samples),
	}
}

func (l *virtualCacheLedger) pruneExpiredLocked(now time.Time) {
	for key, entry := range l.entries {
		l.expireEntryLocked(&entry, now)
		if entry.cached5mTokens <= 0 && entry.cached1hTokens <= 0 {
			delete(l.entries, key)
			continue
		}
		l.entries[key] = entry
	}
}

func (l *virtualCacheLedger) expireEntryLocked(entry *virtualCacheEntry, now time.Time) {
	if entry == nil {
		return
	}
	if !entry.cached5mExpiresAt.IsZero() && !entry.cached5mExpiresAt.After(now) {
		entry.cached5mTokens = 0
		entry.cached5mExpiresAt = time.Time{}
	}
	if !entry.cached1hExpiresAt.IsZero() && !entry.cached1hExpiresAt.After(now) {
		entry.cached1hTokens = 0
		entry.cached1hExpiresAt = time.Time{}
	}
	if entry.cached5mTokens <= 0 && entry.cached1hTokens <= 0 {
		entry.lastEstimateTokens = 0
		entry.lastObservedInputTokens = 0
	}
}

func (l *virtualCacheLedger) capEntryCachedTokensLocked(entry *virtualCacheEntry, maxTokens int64) {
	if entry == nil {
		return
	}
	if maxTokens < 0 {
		maxTokens = 0
	}
	total := entry.cached5mTokens + entry.cached1hTokens
	if total <= maxTokens {
		return
	}
	excess := total - maxTokens
	trim5m := minInt64(entry.cached5mTokens, excess)
	entry.cached5mTokens -= trim5m
	excess -= trim5m
	if entry.cached5mTokens <= 0 {
		entry.cached5mTokens = 0
		entry.cached5mExpiresAt = time.Time{}
	}
	if excess > 0 {
		trim1h := minInt64(entry.cached1hTokens, excess)
		entry.cached1hTokens -= trim1h
		if entry.cached1hTokens <= 0 {
			entry.cached1hTokens = 0
			entry.cached1hExpiresAt = time.Time{}
		}
	}
}

func (l *virtualCacheLedger) dropOldestLocked() {
	oldestKey := ""
	oldest := time.Time{}
	for key, entry := range l.entries {
		candidate := entry.updatedAt
		if candidate.IsZero() {
			candidate = entry.cached5mExpiresAt
			if candidate.IsZero() || (!entry.cached1hExpiresAt.IsZero() && entry.cached1hExpiresAt.Before(candidate)) {
				candidate = entry.cached1hExpiresAt
			}
		}
		if oldestKey == "" || candidate.Before(oldest) {
			oldestKey = key
			oldest = candidate
		}
	}
	if oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}

// RewriteClaudeResponseUsage rewrites non-stream Claude usage fields.
func (tx *VirtualCacheTransaction) RewriteClaudeResponseUsage(payload []byte) []byte {
	if tx == nil || !gjson.ValidBytes(payload) {
		return payload
	}
	return tx.rewriteUsageAtPath(payload, "usage")
}

// RewriteClaudeStreamLine rewrites Claude SSE usage fields while preserving the
// data: prefix used by Anthropic streams.
func (tx *VirtualCacheTransaction) RewriteClaudeStreamLine(line []byte) []byte {
	if tx == nil {
		return line
	}
	prefix, payload, ok := splitSSEDataLine(line)
	if !ok || !gjson.ValidBytes(payload) {
		return line
	}
	rewritten := tx.rewriteUsageAtPath(payload, "message.usage")
	rewritten = tx.rewriteUsageAtPath(rewritten, "usage")
	if bytes.Equal(rewritten, payload) {
		return line
	}
	out := make([]byte, 0, len(prefix)+len(rewritten))
	out = append(out, prefix...)
	out = append(out, rewritten...)
	return out
}

// Commit records the observed cacheable tokens for later requests.
func (tx *VirtualCacheTransaction) Commit() {
	if tx == nil || tx.ledger == nil {
		return
	}
	tx.ledger.commit(tx)
}

func (tx *VirtualCacheTransaction) rewriteUsageAtPath(payload []byte, path string) []byte {
	usage := gjson.GetBytes(payload, path)
	if !usage.Exists() || !usage.IsObject() {
		return payload
	}
	outputTokens := usage.Get("output_tokens").Int()
	inputTokens := usage.Get("input_tokens").Int()
	cacheCreationTokens := usage.Get("cache_creation_input_tokens").Int()
	cacheReadTokens := usage.Get("cache_read_input_tokens").Int()
	reportedTotalInputTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	totalInputTokens := tx.anchorTotalInputTokens(reportedTotalInputTokens)
	if totalInputTokens <= 0 {
		DebugLogf(
			"claude api pool virtual cache rewrite skip reason=zero_total mode=anchored path=%s upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d estimate=%d delta=%d hit_rate=%.3f",
			path,
			inputTokens,
			cacheCreationTokens,
			cacheReadTokens,
			reportedTotalInputTokens,
			tx.estimateTokens,
			tx.deltaTokens,
			tx.policy.HitRate,
		)
		return payload
	}
	tx.observedTokens = maxInt64(tx.observedTokens, totalInputTokens)
	return tx.rewriteLocalLedgerUsageAtPath(payload, path, usage, totalInputTokens, reportedTotalInputTokens, outputTokens)
}

func (tx *VirtualCacheTransaction) rewriteLocalLedgerUsageAtPath(payload []byte, path string, usage gjson.Result, totalInputTokens, reportedTotalInputTokens, outputTokens int64) []byte {
	if tx == nil || totalInputTokens <= 0 {
		return payload
	}

	upstreamInputTokens := usage.Get("input_tokens").Int()
	upstreamCreationTokens := usage.Get("cache_creation_input_tokens").Int()
	upstreamReadTokens := usage.Get("cache_read_input_tokens").Int()

	var virtualReadTokens int64
	var virtualCreationTokens int64
	rewrittenInputTokens := tx.anchoredInputTokens(totalInputTokens, upstreamInputTokens, upstreamCreationTokens, upstreamReadTokens)
	availableHitTokens := tx.availableHitTokens()
	first := availableHitTokens <= 0
	if rewrittenInputTokens < 0 {
		rewrittenInputTokens = 0
	}
	if rewrittenInputTokens > totalInputTokens {
		rewrittenInputTokens = totalInputTokens
	}
	cacheableBudget := totalInputTokens - rewrittenInputTokens
	tx.cacheableBudgetTokens = maxInt64(tx.cacheableBudgetTokens, cacheableBudget)
	if first {
		virtualCreationTokens = cacheableBudget
	} else {
		virtualReadTokens = tx.localReadTokens(cacheableBudget)
		virtualCreationTokens = cacheableBudget - virtualReadTokens
	}
	if virtualReadTokens+virtualCreationTokens > totalInputTokens {
		virtualCreationTokens = totalInputTokens - virtualReadTokens
	}
	if virtualCreationTokens < 0 {
		virtualCreationTokens = 0
	}
	if rewrittenInputTokens < 0 {
		rewrittenInputTokens = 0
	}
	if maxInput := totalInputTokens - virtualReadTokens - virtualCreationTokens; rewrittenInputTokens > maxInput {
		rewrittenInputTokens = maxInt64(0, maxInput)
	}

	tx.observedTokens = maxInt64(tx.observedTokens, totalInputTokens)
	tx.hitTokens = virtualReadTokens
	tx.creationTokens = maxInt64(tx.creationTokens, virtualCreationTokens)
	tx.rememberUsage(rewrittenInputTokens, virtualReadTokens, virtualCreationTokens)

	out := payload
	out, _ = sjson.SetBytes(out, path+".input_tokens", rewrittenInputTokens)
	out, _ = sjson.SetBytes(out, path+".cache_read_input_tokens", virtualReadTokens)
	out, _ = sjson.SetBytes(out, path+".cache_creation_input_tokens", virtualCreationTokens)
	if usage.Get("cache_creation").Exists() || virtualCreationTokens > 0 {
		out = tx.setCacheCreationTTLBreakdown(out, path, virtualCreationTokens)
	}
	if outputTokens > 0 && !usage.Get("output_tokens").Exists() {
		out, _ = sjson.SetBytes(out, path+".output_tokens", outputTokens)
	}
	DebugLogf(
		"claude api pool virtual cache rewrite mode=anchored path=%s first=%t reset=%t upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d anchored_total=%d rewritten_input=%d cacheable_budget=%d rewritten_creation=%d rewritten_read=%d output=%d target_reuse=%.3f hit_rate=%.3f available_hit=%d prior_observed=%d estimate=%d delta=%d total_multiplier=%.3f",
		path,
		first,
		tx.resetLedger,
		upstreamInputTokens,
		upstreamCreationTokens,
		upstreamReadTokens,
		reportedTotalInputTokens,
		totalInputTokens,
		rewrittenInputTokens,
		cacheableBudget,
		virtualCreationTokens,
		virtualReadTokens,
		outputTokens,
		tx.policy.TargetCacheReuseRatio,
		tx.policy.HitRate,
		availableHitTokens,
		tx.priorObserved,
		tx.estimateTokens,
		tx.deltaTokens,
		tokenTotalMultiplier(totalInputTokens, reportedTotalInputTokens),
	)
	return out
}

func (tx *VirtualCacheTransaction) availableHitTokens() int64 {
	if tx == nil {
		return 0
	}
	if tx.hitTokens < 0 {
		return 0
	}
	return tx.hitTokens
}

func (tx *VirtualCacheTransaction) localCacheableBudgetTokens() int64 {
	if tx == nil || tx.estimateTokens <= 0 || tx.policy.HitRate <= 0 {
		return 0
	}
	tokens := int64(math.Round(float64(tx.estimateTokens) * tx.policy.HitRate))
	if tx.policy.MaxCacheTokens > 0 && tokens > tx.policy.MaxCacheTokens {
		tokens = tx.policy.MaxCacheTokens
	}
	if tokens < 0 {
		return 0
	}
	return tokens
}

func (tx *VirtualCacheTransaction) localTotalInputTokens() int64 {
	if tx == nil {
		return 0
	}
	inputTokens := tx.deltaTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	return inputTokens + tx.localCacheableBudgetTokens()
}

func (tx *VirtualCacheTransaction) anchorTotalInputTokens(reportedTotalInputTokens int64) int64 {
	if reportedTotalInputTokens > 0 {
		return reportedTotalInputTokens
	}
	if tx == nil {
		return 0
	}
	return tx.localTotalInputTokens()
}

func (tx *VirtualCacheTransaction) anchoredInputTokens(totalInputTokens, upstreamInputTokens, upstreamCreationTokens, upstreamReadTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 {
		return 0
	}
	inputTokens := tx.deltaTokens
	if upstreamCreationTokens > 0 || upstreamReadTokens > 0 {
		inputTokens = upstreamInputTokens
	}
	if tx.policy.Mode == VirtualCacheModeForced && totalInputTokens > 0 && inputTokens < 1 {
		inputTokens = 1
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if floor := tx.policy.UncachedInputTokens; floor > 0 && totalInputTokens > floor && inputTokens < floor {
		inputTokens = floor
	}
	if maxCacheTokens := tx.policy.MaxCacheTokens; maxCacheTokens > 0 && totalInputTokens-inputTokens > maxCacheTokens {
		inputTokens = totalInputTokens - maxCacheTokens
	}
	if inputTokens > totalInputTokens {
		inputTokens = totalInputTokens
	}
	return inputTokens
}

func (tx *VirtualCacheTransaction) localReadTokens(cacheableBudget int64) int64 {
	if tx == nil || cacheableBudget <= 0 {
		return 0
	}
	if tx.policy.Mode == VirtualCacheModeForced && tx.policy.TargetCacheReuseRatio > 0 {
		return tx.forcedReadTokens(cacheableBudget)
	}
	readTokens := minInt64(tx.availableHitTokens(), cacheableBudget)
	if readTokens > cacheableBudget {
		readTokens = cacheableBudget
	}
	if readTokens < 0 {
		return 0
	}
	return readTokens
}

func (tx *VirtualCacheTransaction) forcedReadTokens(cacheableBudget int64) int64 {
	if tx == nil || cacheableBudget <= 0 {
		return 0
	}
	ratio := clampRatio(tx.policy.TargetCacheReuseRatio)
	readTokens := int64(math.Round(float64(cacheableBudget) * ratio))
	maxReadTokens := cacheableBudget
	if cacheableBudget > 1 {
		maxReadTokens = cacheableBudget - 1
	}
	if readTokens < 0 {
		return 0
	}
	if readTokens > maxReadTokens {
		return maxReadTokens
	}
	return readTokens
}

func (tx *VirtualCacheTransaction) rememberUsage(inputTokens, cacheReadTokens, cacheCreationTokens int64) {
	if tx == nil {
		return
	}
	tx.inputTokens = maxInt64(tx.inputTokens, inputTokens)
	tx.hitTokens = maxInt64(tx.hitTokens, cacheReadTokens)
	tx.creationTokens = maxInt64(tx.creationTokens, cacheCreationTokens)
}

func (tx *VirtualCacheTransaction) rewrittenInputTokens() int64 {
	if tx == nil {
		return 0
	}
	if tx.inputTokens > 0 {
		return tx.inputTokens
	}
	if tx.observedTokens > 0 {
		return maxInt64(0, tx.observedTokens-tx.hitTokens-tx.creationTokens)
	}
	return 0
}

func tokenTotalMultiplier(rewrittenTotalInputTokens, reportedTotalInputTokens int64) float64 {
	if reportedTotalInputTokens <= 0 {
		return 0
	}
	return float64(rewrittenTotalInputTokens) / float64(reportedTotalInputTokens)
}

func (tx *VirtualCacheTransaction) setCacheCreationTTLBreakdown(payload []byte, path string, tokens int64) []byte {
	if tx != nil && tx.ttl == time.Hour {
		payload, _ = sjson.SetBytes(payload, path+".cache_creation.ephemeral_5m_input_tokens", int64(0))
		payload, _ = sjson.SetBytes(payload, path+".cache_creation.ephemeral_1h_input_tokens", tokens)
		return payload
	}
	payload, _ = sjson.SetBytes(payload, path+".cache_creation.ephemeral_5m_input_tokens", tokens)
	payload, _ = sjson.SetBytes(payload, path+".cache_creation.ephemeral_1h_input_tokens", int64(0))
	return payload
}

func splitSSEDataLine(line []byte) ([]byte, []byte, bool) {
	trimmedLeft := bytes.TrimLeft(line, " \t")
	leading := len(line) - len(trimmedLeft)
	if !bytes.HasPrefix(trimmedLeft, []byte("data:")) {
		return nil, nil, false
	}
	dataStart := leading + len("data:")
	payloadStart := dataStart
	for payloadStart < len(line) && (line[payloadStart] == ' ' || line[payloadStart] == '\t') {
		payloadStart++
	}
	return line[:payloadStart], line[payloadStart:], true
}

func cachePrefixFingerprint(payload []byte) (string, time.Duration, int64, int64, bool) {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return "", 0, 0, 0, false
	}
	var b strings.Builder
	var delta strings.Builder
	ttl := maxCacheTTL
	found := false
	appendPart := func(label string, raw string) {
		if raw == "" {
			return
		}
		b.WriteString(label)
		b.WriteByte('=')
		b.WriteString(raw)
		b.WriteByte('\n')
	}
	observeCacheControl := func(cc gjson.Result) {
		if !cc.Exists() {
			return
		}
		found = true
		if parsed := parseCacheControlTTL(cc); parsed < ttl {
			ttl = parsed
		}
	}

	tools := gjson.GetBytes(payload, "tools")
	if tools.Exists() && tools.IsArray() {
		last := -1
		tools.ForEach(func(idx, item gjson.Result) bool {
			if cc := item.Get("cache_control"); cc.Exists() {
				last = int(idx.Int())
				observeCacheControl(cc)
			}
			return true
		})
		if last >= 0 {
			var section strings.Builder
			tools.ForEach(func(idx, item gjson.Result) bool {
				if int(idx.Int()) > last {
					return false
				}
				section.WriteString(item.Raw)
				section.WriteByte('\n')
				return true
			})
			appendPart("tools", section.String())
		}
	}

	system := gjson.GetBytes(payload, "system")
	if system.Exists() && system.IsArray() {
		last := -1
		system.ForEach(func(idx, item gjson.Result) bool {
			if cc := item.Get("cache_control"); cc.Exists() {
				last = int(idx.Int())
				observeCacheControl(cc)
			}
			return true
		})
		if last >= 0 {
			var section strings.Builder
			system.ForEach(func(idx, item gjson.Result) bool {
				if int(idx.Int()) > last {
					return false
				}
				section.WriteString(item.Raw)
				section.WriteByte('\n')
				return true
			})
			appendPart("system", section.String())
		}
	}

	messages := gjson.GetBytes(payload, "messages")
	if messages.Exists() && messages.IsArray() {
		type messageCacheInfo struct {
			raw                   string
			lastCacheContentIndex int
		}
		lastMsg := -1
		messageInfos := make([]messageCacheInfo, 0)
		messages.ForEach(func(msgIdx, msg gjson.Result) bool {
			info := messageCacheInfo{
				raw:                   msg.Raw,
				lastCacheContentIndex: -1,
			}
			content := msg.Get("content")
			if content.IsArray() {
				content.ForEach(func(contentIdx, item gjson.Result) bool {
					if cc := item.Get("cache_control"); cc.Exists() {
						lastMsg = int(msgIdx.Int())
						info.lastCacheContentIndex = int(contentIdx.Int())
						observeCacheControl(cc)
					}
					return true
				})
			} else if cc := content.Get("cache_control"); cc.Exists() {
				lastMsg = int(msgIdx.Int())
				observeCacheControl(cc)
			}
			messageInfos = append(messageInfos, info)
			return true
		})
		if lastMsg >= 0 {
			var section strings.Builder
			for idx, info := range messageInfos {
				switch {
				case idx > lastMsg:
					break
				case idx < lastMsg || info.lastCacheContentIndex < 0:
					section.WriteString(info.raw)
					section.WriteByte('\n')
				default:
					msg := gjson.Parse(info.raw)
					section.WriteString("role=")
					section.WriteString(msg.Get("role").Raw)
					section.WriteByte('\n')
					content := msg.Get("content")
					content.ForEach(func(contentIdx, item gjson.Result) bool {
						if int(contentIdx.Int()) > info.lastCacheContentIndex {
							delta.WriteString(item.Raw)
							delta.WriteByte('\n')
							return true
						}
						section.WriteString(item.Raw)
						section.WriteByte('\n')
						return true
					})
				}
			}
			appendPart("messages", section.String())
			for idx := lastMsg + 1; idx < len(messageInfos); idx++ {
				delta.WriteString(messageInfos[idx].raw)
				delta.WriteByte('\n')
			}
		} else if found {
			for _, info := range messageInfos {
				delta.WriteString(info.raw)
				delta.WriteByte('\n')
			}
		}
	}

	if !found {
		return "", 0, 0, 0, false
	}
	if ttl <= 0 || ttl > maxCacheTTL {
		ttl = defaultCacheTTL
	}
	prefix := b.String()
	sum := sha256.Sum256([]byte(prefix))
	return hex.EncodeToString(sum[:]), ttl, estimateTextTokens(prefix), estimateTextTokens(delta.String()), true
}

func parseCacheControlTTL(cc gjson.Result) time.Duration {
	raw := strings.TrimSpace(cc.Get("ttl").String())
	if raw == "" {
		return defaultCacheTTL
	}
	switch strings.ToLower(raw) {
	case "5m":
		return defaultCacheTTL
	case "1h":
		return time.Hour
	}
	if parsed, err := time.ParseDuration(raw); err == nil {
		if parsed <= 0 || parsed > maxCacheTTL {
			return defaultCacheTTL
		}
		return parsed
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		parsed := time.Duration(seconds) * time.Second
		if parsed > maxCacheTTL {
			return defaultCacheTTL
		}
		return parsed
	}
	return defaultCacheTTL
}

func normalizeEffectiveVirtualCacheConfig(cfg EffectiveVirtualCacheConfig) EffectiveVirtualCacheConfig {
	cfg.Mode = normalizeVirtualCacheMode(cfg.Mode)
	if cfg.HitRate > 1 {
		cfg.HitRate = cfg.HitRate / 100
	}
	if cfg.HitRate < 0 {
		cfg.HitRate = 0
	}
	if cfg.HitRate > 1 {
		cfg.HitRate = 1
	}
	cfg.TargetCacheReuseRatio = clampRatio(cfg.TargetCacheReuseRatio)
	if cfg.ReadScale < 0 || math.IsNaN(cfg.ReadScale) || math.IsInf(cfg.ReadScale, 0) {
		cfg.ReadScale = 0
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
	if cfg.ContextShrinkResetRatio > 1 {
		cfg.ContextShrinkResetRatio = cfg.ContextShrinkResetRatio / 100
	}
	if cfg.ContextShrinkResetRatio < 0 {
		cfg.ContextShrinkResetRatio = 0
	}
	if cfg.ContextShrinkResetRatio > 1 {
		cfg.ContextShrinkResetRatio = 1
	}
	if cfg.MinCreationTokens < 0 {
		cfg.MinCreationTokens = 0
	}
	if cfg.MaxCreationTokens < 0 {
		cfg.MaxCreationTokens = 0
	}
	return cfg
}

func clampRatio(value float64) float64 {
	if value > 1 {
		value = value / 100
	}
	return clampFloat(value, 0, 1)
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func estimateTextTokens(text string) int64 {
	if text == "" {
		return 0
	}
	var units float64
	for _, r := range text {
		if isNonWesternRune(r) {
			units += 4
		} else {
			units++
		}
	}
	tokens := units / 4
	switch {
	case tokens < 100:
		tokens *= 1.5
	case tokens < 200:
		tokens *= 1.3
	case tokens < 300:
		tokens *= 1.25
	case tokens < 800:
		tokens *= 1.2
	}
	return int64(math.Ceil(tokens))
}

func isNonWesternRune(r rune) bool {
	return !((r >= '\u0000' && r <= '\u007F') ||
		(r >= '\u0080' && r <= '\u00FF') ||
		(r >= '\u0100' && r <= '\u024F') ||
		(r >= '\u1E00' && r <= '\u1EFF') ||
		(r >= '\u2C60' && r <= '\u2C7F') ||
		(r >= '\uA720' && r <= '\uA7FF') ||
		(r >= '\uAB30' && r <= '\uAB6F'))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func shouldResetVirtualCacheEntry(entry virtualCacheEntry, estimateTokens int64, policy EffectiveVirtualCacheConfig) bool {
	ratio := policy.ContextShrinkResetRatio
	if ratio <= 0 || estimateTokens <= 0 || entry.lastEstimateTokens <= 0 {
		return false
	}
	threshold := int64(math.Floor(float64(entry.lastEstimateTokens) * ratio))
	return threshold > 0 && estimateTokens < threshold
}

func (tx *VirtualCacheTransaction) String() string {
	if tx == nil {
		return ""
	}
	return fmt.Sprintf("virtual-cache hit_tokens=%d creation_tokens=%d observed_tokens=%d estimate_tokens=%d ttl=%s", tx.hitTokens, tx.creationTokens, tx.observedTokens, tx.estimateTokens, tx.ttl)
}
