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
	defaultCacheTTL                = 5 * time.Minute
	maxCacheTTL                    = time.Hour
	virtualLedgerMaxKeys           = 100000
	cacheReuseWindow               = 5 * time.Minute
	cacheReuseMinSamples           = 3
	cacheReuseTuningDeadband       = 0.02
	cacheReuseTuningMaxStep        = 0.25
	cacheReuseTunedHitRateMax      = 0.99
	cacheReuseTunedUncachedMinStep = int64(1)
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
	ledger         *virtualCacheLedger
	key            string
	fingerprint    string
	ttl            time.Duration
	policy         EffectiveVirtualCacheConfig
	estimateTokens int64
	hitTokens      int64
	priorObserved  int64
	observedTokens int64
	inputTokens    int64
	creationTokens int64
	resetLedger    bool
	now            time.Time
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
		"claude api pool virtual cache config enabled=%t hit_rate=%.3f target_reuse=%.3f min_cache=%d max_cache=%d uncached=%d shrink_reset=%.3f min_creation=%d max_creation=%d",
		defaultVirtualCachePolicy.Enabled,
		defaultVirtualCachePolicy.HitRate,
		defaultVirtualCachePolicy.TargetCacheReuseRatio,
		defaultVirtualCachePolicy.MinCacheTokens,
		defaultVirtualCachePolicy.MaxCacheTokens,
		defaultVirtualCachePolicy.UncachedInputTokens,
		defaultVirtualCachePolicy.ContextShrinkResetRatio,
		defaultVirtualCachePolicy.MinCreationTokens,
		defaultVirtualCachePolicy.MaxCreationTokens,
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
	fingerprint, ttl, estimateTokens, ok := cachePrefixFingerprint(requestPayload)
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
		"claude api pool virtual cache begin provider=%s model=%s session=%s fingerprint=%s ttl_ms=%d estimate=%d hit_tokens=%d prior_observed=%d reset=%t target_reuse=%.3f hit_rate=%.3f",
		provider,
		model,
		debugShortHash(sessionKey),
		debugShortHash(fingerprint),
		debugDurationMS(ttl),
		estimateTokens,
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
	if tx.observedTokens <= 0 && tx.hitTokens <= 0 && tx.creationTokens <= 0 {
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
	l.capEntryCachedTokensLocked(&entry, maxInt64(0, entry.lastObservedInputTokens-tx.policy.UncachedInputTokens))
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
	if l == nil || !policy.Enabled || policy.TargetCacheReuseRatio <= 0 {
		return policy
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneReuseSamplesLocked(now)
	snapshot := reuseSnapshotFromSamples(l.reuseSamples, policy.TargetCacheReuseRatio)
	return applyTargetCacheReuseTuning(policy, snapshot)
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
	inputTokens := usage.Get("input_tokens").Int()
	outputTokens := usage.Get("output_tokens").Int()
	cacheCreationTokens := usage.Get("cache_creation_input_tokens").Int()
	cacheReadTokens := usage.Get("cache_read_input_tokens").Int()
	cacheableTokens := cacheCreationTokens + cacheReadTokens
	reportedTotalInputTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	if reportedTotalInputTokens > tx.observedTokens {
		tx.observedTokens = reportedTotalInputTokens
	}
	if tx.policy.TargetCacheReuseRatio > 0 {
		return tx.rewriteTargetRatioUsageAtPath(
			payload,
			path,
			usage,
			reportedTotalInputTokens,
			outputTokens,
		)
	}
	if tx.hitTokens <= 0 {
		if cacheableTokens > 0 {
			tx.rememberUsage(inputTokens, cacheReadTokens, cacheCreationTokens)
			tx.hitTokens = maxInt64(tx.hitTokens, cacheReadTokens)
			tx.creationTokens = maxInt64(tx.creationTokens, cacheCreationTokens)
			DebugLogf(
				"claude api pool virtual cache rewrite mode=legacy_passthrough path=%s upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d rewritten_input=%d rewritten_creation=%d rewritten_read=%d output=%d",
				path,
				inputTokens,
				cacheCreationTokens,
				cacheReadTokens,
				reportedTotalInputTokens,
				inputTokens,
				cacheCreationTokens,
				cacheReadTokens,
				outputTokens,
			)
			return payload
		}
		totalInputTokens := inputTokens
		if totalInputTokens <= 0 {
			DebugLogf(
				"claude api pool virtual cache rewrite skip reason=zero_total mode=legacy path=%s upstream_input=%d upstream_creation=%d upstream_read=%d",
				path,
				inputTokens,
				cacheCreationTokens,
				cacheReadTokens,
			)
			return payload
		}
		virtualCreationTokens := tx.configuredCacheTokens(totalInputTokens)
		if virtualCreationTokens <= 0 {
			DebugLogf(
				"claude api pool virtual cache rewrite skip reason=zero_creation mode=legacy path=%s upstream_input=%d upstream_total=%d hit_rate=%.3f",
				path,
				inputTokens,
				totalInputTokens,
				tx.policy.HitRate,
			)
			return payload
		}
		tx.creationTokens = maxInt64(tx.creationTokens, virtualCreationTokens)
		tx.observedTokens = maxInt64(tx.observedTokens, totalInputTokens)
		rewrittenInputTokens := totalInputTokens - virtualCreationTokens
		tx.rememberUsage(rewrittenInputTokens, 0, virtualCreationTokens)
		out := payload
		out, _ = sjson.SetBytes(out, path+".input_tokens", rewrittenInputTokens)
		out, _ = sjson.SetBytes(out, path+".cache_creation_input_tokens", virtualCreationTokens)
		out, _ = sjson.SetBytes(out, path+".cache_read_input_tokens", int64(0))
		out = tx.setCacheCreationTTLBreakdown(out, path, virtualCreationTokens)
		if outputTokens > 0 && !usage.Get("output_tokens").Exists() {
			out, _ = sjson.SetBytes(out, path+".output_tokens", outputTokens)
		}
		DebugLogf(
			"claude api pool virtual cache rewrite mode=legacy_initial path=%s first=%t reset=%t upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d rewritten_input=%d rewritten_creation=%d rewritten_read=%d output=%d hit_rate=%.3f target_reuse=%.3f",
			path,
			true,
			tx.resetLedger,
			inputTokens,
			cacheCreationTokens,
			cacheReadTokens,
			totalInputTokens,
			rewrittenInputTokens,
			virtualCreationTokens,
			int64(0),
			outputTokens,
			tx.policy.HitRate,
			tx.policy.TargetCacheReuseRatio,
		)
		return out
	}
	totalInputTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	if totalInputTokens <= 0 {
		DebugLogf(
			"claude api pool virtual cache rewrite skip reason=zero_total mode=legacy_hit path=%s upstream_input=%d upstream_creation=%d upstream_read=%d",
			path,
			inputTokens,
			cacheCreationTokens,
			cacheReadTokens,
		)
		return payload
	}
	virtualReadTokens := tx.hitTokens
	if cacheableTokens > virtualReadTokens {
		virtualReadTokens = cacheableTokens
	}
	if virtualReadTokens > totalInputTokens {
		virtualReadTokens = totalInputTokens
	}
	virtualReadTokens = tx.targetReadTokens(totalInputTokens, virtualReadTokens)
	if floor := tx.policy.UncachedInputTokens; floor > 0 && totalInputTokens > floor && totalInputTokens-virtualReadTokens < floor {
		virtualReadTokens = totalInputTokens - floor
	}
	virtualReadTokens = tx.configuredReadTokens(virtualReadTokens)
	if virtualReadTokens > totalInputTokens {
		virtualReadTokens = totalInputTokens
	}
	if floor := tx.policy.UncachedInputTokens; floor > 0 && totalInputTokens > floor && totalInputTokens-virtualReadTokens < floor {
		virtualReadTokens = totalInputTokens - floor
	}
	if virtualReadTokens < 0 {
		virtualReadTokens = 0
	}
	virtualCreationTokens := tx.configuredDeltaCreationTokens(totalInputTokens, virtualReadTokens)
	rewrittenInputTokens := totalInputTokens - virtualReadTokens - virtualCreationTokens
	tx.observedTokens = maxInt64(tx.observedTokens, totalInputTokens)
	tx.hitTokens = virtualReadTokens
	tx.creationTokens = maxInt64(tx.creationTokens, virtualCreationTokens)
	tx.rememberUsage(rewrittenInputTokens, virtualReadTokens, virtualCreationTokens)
	out := payload
	out, _ = sjson.SetBytes(out, path+".input_tokens", rewrittenInputTokens)
	out, _ = sjson.SetBytes(out, path+".cache_read_input_tokens", virtualReadTokens)
	if usage.Get("cache_creation_input_tokens").Exists() || cacheCreationTokens > 0 || virtualCreationTokens > 0 {
		out, _ = sjson.SetBytes(out, path+".cache_creation_input_tokens", virtualCreationTokens)
	}
	if usage.Get("cache_creation").Exists() || virtualCreationTokens > 0 {
		out = tx.setCacheCreationTTLBreakdown(out, path, virtualCreationTokens)
	}
	if outputTokens > 0 && !usage.Get("output_tokens").Exists() {
		out, _ = sjson.SetBytes(out, path+".output_tokens", outputTokens)
	}
	DebugLogf(
		"claude api pool virtual cache rewrite mode=legacy_hit path=%s first=%t reset=%t upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d rewritten_input=%d rewritten_creation=%d rewritten_read=%d output=%d hit_tokens=%d prior_observed=%d hit_rate=%.3f target_reuse=%.3f",
		path,
		false,
		tx.resetLedger,
		inputTokens,
		cacheCreationTokens,
		cacheReadTokens,
		totalInputTokens,
		rewrittenInputTokens,
		virtualCreationTokens,
		virtualReadTokens,
		outputTokens,
		tx.hitTokens,
		tx.priorObserved,
		tx.policy.HitRate,
		tx.policy.TargetCacheReuseRatio,
	)
	return out
}

func (tx *VirtualCacheTransaction) rewriteTargetRatioUsageAtPath(payload []byte, path string, usage gjson.Result, totalInputTokens, outputTokens int64) []byte {
	if tx == nil || totalInputTokens <= 0 {
		return payload
	}

	var virtualReadTokens int64
	var virtualCreationTokens int64
	first := tx.hitTokens <= 0
	if tx.hitTokens <= 0 {
		virtualCreationTokens = tx.configuredTargetInitialCreationTokens(totalInputTokens)
	} else {
		virtualReadTokens = tx.configuredTargetReadTokens(totalInputTokens)
		virtualCreationTokens = tx.configuredTargetDeltaCreationTokens(totalInputTokens, virtualReadTokens)
	}
	if virtualReadTokens+virtualCreationTokens > totalInputTokens {
		virtualCreationTokens = totalInputTokens - virtualReadTokens
	}
	if virtualCreationTokens < 0 {
		virtualCreationTokens = 0
	}
	rewrittenInputTokens := totalInputTokens - virtualReadTokens - virtualCreationTokens
	if rewrittenInputTokens < 0 {
		rewrittenInputTokens = 0
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
		"claude api pool virtual cache rewrite mode=target path=%s first=%t reset=%t upstream_input=%d upstream_creation=%d upstream_read=%d upstream_total=%d rewritten_input=%d rewritten_creation=%d rewritten_read=%d output=%d target_reuse=%.3f hit_rate=%.3f uncached=%d max_creation=%d prior_observed=%d estimate=%d",
		path,
		first,
		tx.resetLedger,
		usage.Get("input_tokens").Int(),
		usage.Get("cache_creation_input_tokens").Int(),
		usage.Get("cache_read_input_tokens").Int(),
		totalInputTokens,
		rewrittenInputTokens,
		virtualCreationTokens,
		virtualReadTokens,
		outputTokens,
		tx.policy.TargetCacheReuseRatio,
		tx.policy.HitRate,
		tx.policy.UncachedInputTokens,
		tx.policy.MaxCreationTokens,
		tx.priorObserved,
		tx.estimateTokens,
	)
	return out
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

func (tx *VirtualCacheTransaction) configuredDeltaCreationTokens(totalInputTokens, readTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || readTokens <= 0 {
		return 0
	}
	deltaBase := tx.priorObserved
	if deltaBase <= 0 {
		deltaBase = tx.observedTokens
	}
	delta := totalInputTokens - deltaBase
	if delta <= 0 {
		return 0
	}
	creationTokens := int64(math.Round(float64(delta) * tx.policy.HitRate))
	if tx.policy.MinCreationTokens > 0 && creationTokens < tx.policy.MinCreationTokens {
		creationTokens = tx.policy.MinCreationTokens
	}
	if tx.policy.MaxCreationTokens > 0 && creationTokens > tx.policy.MaxCreationTokens {
		creationTokens = tx.policy.MaxCreationTokens
	}
	maxCreation := totalInputTokens - readTokens
	if floor := tx.policy.UncachedInputTokens; floor > 0 {
		maxCreation = totalInputTokens - readTokens - floor
	}
	if maxCreation < 0 {
		maxCreation = 0
	}
	if creationTokens > maxCreation {
		creationTokens = maxCreation
	}
	if creationTokens < 0 {
		return 0
	}
	return creationTokens
}

func (tx *VirtualCacheTransaction) configuredTargetInitialCreationTokens(totalInputTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || !tx.policy.Enabled || tx.policy.HitRate <= 0 {
		return 0
	}
	if tx.policy.MinCacheTokens > 0 && totalInputTokens < tx.policy.MinCacheTokens {
		return 0
	}
	creationTokens := int64(math.Round(float64(totalInputTokens) * tx.policy.HitRate))
	if tx.policy.MaxCacheTokens > 0 && creationTokens > tx.policy.MaxCacheTokens {
		creationTokens = tx.policy.MaxCacheTokens
	}
	if floor := tx.policy.UncachedInputTokens; floor > 0 {
		maxCreation := totalInputTokens - floor
		if maxCreation < 0 {
			maxCreation = 0
		}
		if creationTokens > maxCreation {
			creationTokens = maxCreation
		}
	}
	if creationTokens < 0 {
		return 0
	}
	return creationTokens
}

func (tx *VirtualCacheTransaction) configuredTargetDeltaCreationTokens(totalInputTokens, readTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || readTokens <= 0 || tx.policy.HitRate <= 0 || tx.policy.MaxCreationTokens <= 0 {
		return 0
	}
	deltaBase := tx.priorObserved
	if deltaBase <= 0 {
		deltaBase = tx.observedTokens
	}
	delta := totalInputTokens - deltaBase
	if delta <= 0 {
		return 0
	}
	creationTokens := int64(math.Round(float64(delta) * tx.policy.HitRate))
	if tx.policy.MinCreationTokens > 0 && creationTokens < tx.policy.MinCreationTokens {
		creationTokens = tx.policy.MinCreationTokens
	}
	if creationTokens > tx.policy.MaxCreationTokens {
		creationTokens = tx.policy.MaxCreationTokens
	}
	maxCreation := totalInputTokens - readTokens
	if floor := tx.policy.UncachedInputTokens; floor > 0 {
		maxCreation = totalInputTokens - readTokens - floor
	}
	if maxCreation < 0 {
		maxCreation = 0
	}
	if creationTokens > maxCreation {
		creationTokens = maxCreation
	}
	if creationTokens < 0 {
		return 0
	}
	return creationTokens
}

func (tx *VirtualCacheTransaction) configuredReadTokens(tokens int64) int64 {
	if tx == nil || tokens <= 0 {
		return 0
	}
	readTokens := tokens
	if tx.policy.ReadScale > 0 {
		readTokens = int64(math.Round(float64(readTokens) * tx.policy.ReadScale))
	}
	if tx.policy.MaxCacheTokens > 0 && readTokens > tx.policy.MaxCacheTokens {
		readTokens = tx.policy.MaxCacheTokens
	}
	if readTokens < 0 {
		return 0
	}
	return readTokens
}

func (tx *VirtualCacheTransaction) configuredTargetReadTokens(totalInputTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || tx.policy.TargetCacheReuseRatio <= 0 {
		return 0
	}
	readTokens := int64(math.Round(float64(totalInputTokens) * tx.policy.TargetCacheReuseRatio))
	if tx.policy.MaxCacheTokens > 0 && readTokens > tx.policy.MaxCacheTokens {
		readTokens = tx.policy.MaxCacheTokens
	}
	if floor := tx.policy.UncachedInputTokens; floor > 0 && totalInputTokens > floor && totalInputTokens-readTokens < floor {
		readTokens = totalInputTokens - floor
	}
	if readTokens > totalInputTokens {
		readTokens = totalInputTokens
	}
	if readTokens < 0 {
		return 0
	}
	return readTokens
}

func (tx *VirtualCacheTransaction) targetReadTokens(totalInputTokens, currentReadTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || currentReadTokens <= 0 || tx.policy.TargetCacheReuseRatio <= 0 {
		return currentReadTokens
	}
	targetReadTokens := int64(math.Round(float64(totalInputTokens) * tx.policy.TargetCacheReuseRatio))
	if targetReadTokens < 0 {
		return 0
	}
	if targetReadTokens > totalInputTokens {
		return totalInputTokens
	}
	return targetReadTokens
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

func (tx *VirtualCacheTransaction) configuredCacheTokens(totalInputTokens int64) int64 {
	if tx == nil || totalInputTokens <= 0 || tx.estimateTokens <= 0 || !tx.policy.Enabled || tx.policy.HitRate <= 0 {
		return 0
	}
	cacheable := tx.estimateTokens
	if cacheable > totalInputTokens {
		cacheable = totalInputTokens
	}
	if tx.policy.MinCacheTokens > 0 && cacheable < tx.policy.MinCacheTokens {
		return 0
	}
	virtualTokens := int64(math.Round(float64(cacheable) * tx.policy.HitRate))
	if tx.policy.MaxCacheTokens > 0 && virtualTokens > tx.policy.MaxCacheTokens {
		virtualTokens = tx.policy.MaxCacheTokens
	}
	if floor := tx.policy.UncachedInputTokens; floor > 0 {
		maxVirtual := totalInputTokens - floor
		if maxVirtual < 0 {
			maxVirtual = 0
		}
		if virtualTokens > maxVirtual {
			virtualTokens = maxVirtual
		}
	}
	if virtualTokens < 0 {
		return 0
	}
	return virtualTokens
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

func cachePrefixFingerprint(payload []byte) (string, time.Duration, int64, bool) {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return "", 0, 0, false
	}
	var b strings.Builder
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
		lastMsg := -1
		messages.ForEach(func(msgIdx, msg gjson.Result) bool {
			content := msg.Get("content")
			if content.IsArray() {
				content.ForEach(func(_, item gjson.Result) bool {
					if cc := item.Get("cache_control"); cc.Exists() {
						lastMsg = int(msgIdx.Int())
						observeCacheControl(cc)
					}
					return true
				})
			} else if cc := content.Get("cache_control"); cc.Exists() {
				lastMsg = int(msgIdx.Int())
				observeCacheControl(cc)
			}
			return true
		})
		if lastMsg >= 0 {
			var section strings.Builder
			messages.ForEach(func(idx, msg gjson.Result) bool {
				if int(idx.Int()) > lastMsg {
					return false
				}
				section.WriteString(msg.Raw)
				section.WriteByte('\n')
				return true
			})
			appendPart("messages", section.String())
		}
	}

	if !found {
		return "", 0, 0, false
	}
	if ttl <= 0 || ttl > maxCacheTTL {
		ttl = defaultCacheTTL
	}
	prefix := b.String()
	sum := sha256.Sum256([]byte(prefix))
	return hex.EncodeToString(sum[:]), ttl, estimateTextTokens(prefix), true
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

func applyTargetCacheReuseTuning(policy EffectiveVirtualCacheConfig, snapshot VirtualCacheReuseSnapshot) EffectiveVirtualCacheConfig {
	if policy.TargetCacheReuseRatio <= 0 ||
		!policy.Enabled ||
		snapshot.SampleCount < cacheReuseMinSamples ||
		snapshot.DenominatorTokens <= 0 {
		return policy
	}
	target := clampRatio(policy.TargetCacheReuseRatio)
	actual := clampRatio(snapshot.ActualRatio)
	errorRatio := target - actual
	if math.Abs(errorRatio) < cacheReuseTuningDeadband {
		return policy
	}
	step := errorRatio
	if step > cacheReuseTuningMaxStep {
		step = cacheReuseTuningMaxStep
	}
	if step < -cacheReuseTuningMaxStep {
		step = -cacheReuseTuningMaxStep
	}
	factor := 1.0 + step
	policy.HitRate = clampFloat(policy.HitRate*factor, 0, cacheReuseTunedHitRateMax)
	policy.ReadScale = factor
	if policy.TargetCacheReuseRatio > 0 && policy.HitRate <= 0 {
		policy.HitRate = minFloat64(policy.TargetCacheReuseRatio, cacheReuseTunedHitRateMax)
	}
	inverseFactor := 1.0 - step
	policy.UncachedInputTokens = scaleInt64(policy.UncachedInputTokens, inverseFactor, cacheReuseTunedUncachedMinStep)
	policy.MinCreationTokens = scaleInt64(policy.MinCreationTokens, factor, 0)
	policy.MaxCreationTokens = scaleInt64(policy.MaxCreationTokens, factor, 0)
	if policy.MinCreationTokens > 0 && policy.MaxCreationTokens > 0 && policy.MinCreationTokens > policy.MaxCreationTokens {
		policy.MinCreationTokens = policy.MaxCreationTokens
	}
	return normalizeEffectiveVirtualCacheConfig(policy)
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

func scaleInt64(value int64, factor float64, minValue int64) int64 {
	if value <= 0 {
		return value
	}
	scaled := math.Round(float64(value) * factor)
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return maxInt64(value, minValue)
	}
	if scaled < float64(minValue) {
		return minValue
	}
	if scaled > float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(scaled)
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

func minFloat64(a, b float64) float64 {
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
