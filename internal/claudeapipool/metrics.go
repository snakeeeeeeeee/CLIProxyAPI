package claudeapipool

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	cliproxyusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const (
	metricsBucketSeconds int64 = 60
	metricsBucketCount         = 60
)

type routeMetricsCollector struct {
	mu       sync.Mutex
	accounts map[string]*accountMetrics
}

type accountMetrics struct {
	buckets [metricsBucketCount]accountMetricBucket
}

type accountMetricBucket struct {
	bucketID            int64
	requests            int64
	success             int64
	failures            int64
	status429           int64
	status529           int64
	status5xx           int64
	latencyMS           int64
	latencyCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheReadTokens     int64
	cacheCreationTokens int64
}

// RouteUsage records the route/account outcome for a Claude API pool request.
type RouteUsage struct {
	AuthID              string
	StatusCode          int
	Success             bool
	Latency             time.Duration
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// AccountMetricsSnapshot is returned in management item views.
type AccountMetricsSnapshot struct {
	WindowSeconds       int64                      `json:"window_seconds"`
	RequestCount        int64                      `json:"request_count"`
	SuccessCount        int64                      `json:"success_count"`
	FailureCount        int64                      `json:"failure_count"`
	SuccessRate         float64                    `json:"success_rate"`
	RPM1m               int64                      `json:"rpm_1m"`
	Status429           int64                      `json:"status_429"`
	Status529           int64                      `json:"status_529"`
	Status5xx           int64                      `json:"status_5xx"`
	AverageLatencyMS    int64                      `json:"avg_latency_ms"`
	InputTokens         int64                      `json:"input_tokens"`
	OutputTokens        int64                      `json:"output_tokens"`
	CacheReadTokens     int64                      `json:"cache_read_tokens"`
	CacheCreationTokens int64                      `json:"cache_creation_tokens"`
	RealCacheRatio      float64                    `json:"real_cache_ratio"`
	History             []AccountMetricsBucketView `json:"history"`
}

// AccountMetricsBucketView is one minute of per-account traffic history.
type AccountMetricsBucketView struct {
	Time        string  `json:"time"`
	Requests    int64   `json:"requests"`
	Success     int64   `json:"success"`
	Failures    int64   `json:"failures"`
	Status429   int64   `json:"status_429"`
	Status529   int64   `json:"status_529"`
	Status5xx   int64   `json:"status_5xx"`
	SuccessRate float64 `json:"success_rate"`
	State       string  `json:"state"`
}

// GlobalRuntimeStats summarizes pool-wide runtime state for management UI.
type GlobalRuntimeStats struct {
	WindowSeconds       int64            `json:"window_seconds"`
	AccountCount        int              `json:"account_count"`
	AvailableAccounts   int              `json:"available_accounts"`
	CoolingAccounts     int              `json:"cooling_accounts"`
	InFlight            int64            `json:"in_flight"`
	RPMUsed             int              `json:"rpm_used"`
	RPMLimit            int              `json:"rpm_limit"`
	ActiveAffinityKeys  int              `json:"active_affinity_keys"`
	WarmLanes           int              `json:"warm_lanes"`
	RequestCount        int64            `json:"request_count"`
	SuccessCount        int64            `json:"success_count"`
	FailureCount        int64            `json:"failure_count"`
	Status429           int64            `json:"status_429"`
	Status529           int64            `json:"status_529"`
	Status5xx           int64            `json:"status_5xx"`
	SuccessRate         float64          `json:"success_rate"`
	RealCacheRatio      float64          `json:"real_cache_ratio"`
	CacheReadTokens     int64            `json:"cache_read_tokens"`
	CacheCreationTokens int64            `json:"cache_creation_tokens"`
	InputTokens         int64            `json:"input_tokens"`
	OutputTokens        int64            `json:"output_tokens"`
	AffinityAutoPlan    AffinityAutoPlan `json:"affinity_auto_plan"`
}

var defaultRouteMetrics = &routeMetricsCollector{accounts: make(map[string]*accountMetrics)}

// RecordRouteUsage records one Claude API pool request outcome.
func RecordRouteUsage(usage RouteUsage) {
	defaultRouteMetrics.record(usage, time.Now())
}

// AccountMetrics returns the recent metrics for one auth.
func AccountMetrics(authID string) AccountMetricsSnapshot {
	return defaultRouteMetrics.accountSnapshot(authID, time.Now())
}

// RuntimeStats returns pool-wide metrics blended with current limiter state.
func RuntimeStats(authIDs []string, statuses []RouteStatus) GlobalRuntimeStats {
	return runtimeStats(authIDs, statuses, true)
}

// RefreshAffinityAutoPlan refreshes the runtime affinity auto plan without requiring a management UI poll.
func RefreshAffinityAutoPlan(authIDs []string, statuses []RouteStatus) AffinityAutoPlan {
	return runtimeStats(authIDs, statuses, false).AffinityAutoPlan
}

func runtimeStats(authIDs []string, statuses []RouteStatus, includeAffinityStats bool) GlobalRuntimeStats {
	stats := defaultRouteMetrics.globalSnapshot(authIDs, time.Now())
	for _, status := range statuses {
		stats.AccountCount++
		stats.InFlight += status.InFlight
		stats.RPMUsed += status.RPMUsed
		stats.RPMLimit += status.RPMLimit
		if status.Cooling || status.Unavailable {
			stats.CoolingAccounts++
		} else {
			stats.AvailableAccounts++
		}
	}
	if includeAffinityStats {
		activeKeys, warmLanes := AffinityStats()
		stats.ActiveAffinityKeys = activeKeys
		stats.WarmLanes = warmLanes
	}
	stats.AffinityAutoPlan = updateAffinityAutoPlan(stats, CurrentRoutingConfig())
	return stats
}

func (c *routeMetricsCollector) record(usage RouteUsage, now time.Time) {
	if c == nil {
		return
	}
	authID := strings.TrimSpace(usage.AuthID)
	if authID == "" {
		return
	}
	bucketID := metricBucketID(now)
	idx := metricBucketIndex(bucketID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accounts == nil {
		c.accounts = make(map[string]*accountMetrics)
	}
	metrics := c.accounts[authID]
	if metrics == nil {
		metrics = &accountMetrics{}
		c.accounts[authID] = metrics
	}
	bucket := &metrics.buckets[idx]
	if bucket.bucketID != bucketID {
		*bucket = accountMetricBucket{bucketID: bucketID}
	}
	bucket.requests++
	if usage.Success {
		bucket.success++
	} else {
		bucket.failures++
	}
	switch {
	case usage.StatusCode == StatusTooManyRequests:
		bucket.status429++
	case usage.StatusCode == StatusOverloaded:
		bucket.status529++
	case usage.StatusCode >= http.StatusInternalServerError:
		bucket.status5xx++
	}
	if usage.Latency > 0 {
		bucket.latencyMS += usage.Latency.Milliseconds()
		bucket.latencyCount++
	}
	bucket.inputTokens += positiveInt64(usage.InputTokens)
	bucket.outputTokens += positiveInt64(usage.OutputTokens)
	bucket.cacheReadTokens += positiveInt64(usage.CacheReadTokens)
	bucket.cacheCreationTokens += positiveInt64(usage.CacheCreationTokens)
}

func (c *routeMetricsCollector) accountSnapshot(authID string, now time.Time) AccountMetricsSnapshot {
	authID = strings.TrimSpace(authID)
	snapshot := AccountMetricsSnapshot{
		WindowSeconds: metricsBucketSeconds * int64(metricsBucketCount),
		History:       emptyMetricHistory(now),
	}
	if c == nil || authID == "" {
		return snapshot
	}
	c.mu.Lock()
	metrics := c.accounts[authID]
	if metrics == nil {
		c.mu.Unlock()
		return snapshot
	}
	buckets := metrics.buckets
	c.mu.Unlock()
	snapshot.History = buildMetricHistory(buckets, now)
	for _, bucket := range buckets {
		if !metricBucketInWindow(bucket, now) {
			continue
		}
		addBucketToSnapshot(&snapshot, bucket)
	}
	finalizeAccountSnapshot(&snapshot)
	return snapshot
}

func (c *routeMetricsCollector) globalSnapshot(authIDs []string, now time.Time) GlobalRuntimeStats {
	stats := GlobalRuntimeStats{WindowSeconds: metricsBucketSeconds * int64(metricsBucketCount)}
	if c == nil {
		return stats
	}
	allowed := make(map[string]struct{}, len(authIDs))
	for _, authID := range authIDs {
		authID = strings.TrimSpace(authID)
		if authID != "" {
			allowed[authID] = struct{}{}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for authID, metrics := range c.accounts {
		if metrics == nil {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[authID]; !ok {
				continue
			}
		}
		for _, bucket := range metrics.buckets {
			if !metricBucketInWindow(bucket, now) {
				continue
			}
			stats.RequestCount += bucket.requests
			stats.SuccessCount += bucket.success
			stats.FailureCount += bucket.failures
			stats.Status429 += bucket.status429
			stats.Status529 += bucket.status529
			stats.Status5xx += bucket.status5xx
			stats.InputTokens += bucket.inputTokens
			stats.OutputTokens += bucket.outputTokens
			stats.CacheReadTokens += bucket.cacheReadTokens
			stats.CacheCreationTokens += bucket.cacheCreationTokens
		}
	}
	if stats.RequestCount > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.RequestCount)
	}
	stats.RealCacheRatio = realCacheRatio(stats.CacheReadTokens, stats.CacheCreationTokens, stats.InputTokens)
	return stats
}

func metricBucketID(now time.Time) int64 {
	if now.IsZero() {
		return 0
	}
	return now.Unix() / metricsBucketSeconds
}

func metricBucketIndex(bucketID int64) int {
	mod := bucketID % int64(metricsBucketCount)
	if mod < 0 {
		mod += int64(metricsBucketCount)
	}
	return int(mod)
}

func metricBucketInWindow(bucket accountMetricBucket, now time.Time) bool {
	if bucket.bucketID == 0 {
		return false
	}
	current := metricBucketID(now)
	return bucket.bucketID <= current && bucket.bucketID > current-int64(metricsBucketCount)
}

func emptyMetricHistory(now time.Time) []AccountMetricsBucketView {
	current := metricBucketID(now)
	out := make([]AccountMetricsBucketView, 0, metricsBucketCount)
	for i := metricsBucketCount - 1; i >= 0; i-- {
		bucketID := current - int64(i)
		out = append(out, AccountMetricsBucketView{
			Time:  formatMetricBucketLabel(bucketID),
			State: "empty",
		})
	}
	return out
}

func buildMetricHistory(buckets [metricsBucketCount]accountMetricBucket, now time.Time) []AccountMetricsBucketView {
	current := metricBucketID(now)
	out := make([]AccountMetricsBucketView, 0, metricsBucketCount)
	for i := metricsBucketCount - 1; i >= 0; i-- {
		bucketID := current - int64(i)
		bucket := buckets[metricBucketIndex(bucketID)]
		view := AccountMetricsBucketView{
			Time:  formatMetricBucketLabel(bucketID),
			State: "empty",
		}
		if bucket.bucketID == bucketID {
			view.Requests = bucket.requests
			view.Success = bucket.success
			view.Failures = bucket.failures
			view.Status429 = bucket.status429
			view.Status529 = bucket.status529
			view.Status5xx = bucket.status5xx
			if bucket.requests > 0 {
				view.SuccessRate = float64(bucket.success) / float64(bucket.requests)
			}
			view.State = metricBucketState(bucket)
		}
		out = append(out, view)
	}
	return out
}

func addBucketToSnapshot(snapshot *AccountMetricsSnapshot, bucket accountMetricBucket) {
	snapshot.RequestCount += bucket.requests
	snapshot.SuccessCount += bucket.success
	snapshot.FailureCount += bucket.failures
	snapshot.Status429 += bucket.status429
	snapshot.Status529 += bucket.status529
	snapshot.Status5xx += bucket.status5xx
	snapshot.InputTokens += bucket.inputTokens
	snapshot.OutputTokens += bucket.outputTokens
	snapshot.CacheReadTokens += bucket.cacheReadTokens
	snapshot.CacheCreationTokens += bucket.cacheCreationTokens
	if bucket.latencyCount > 0 {
		currentTotal := snapshot.AverageLatencyMS * (snapshot.RequestCount - bucket.requests)
		currentTotal += bucket.latencyMS
		count := snapshot.RequestCount
		if count > 0 {
			snapshot.AverageLatencyMS = currentTotal / count
		}
	}
}

func finalizeAccountSnapshot(snapshot *AccountMetricsSnapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.RequestCount > 0 {
		snapshot.SuccessRate = float64(snapshot.SuccessCount) / float64(snapshot.RequestCount)
	}
	if len(snapshot.History) > 0 {
		last := snapshot.History[len(snapshot.History)-1]
		snapshot.RPM1m = last.Requests
	}
	snapshot.RealCacheRatio = realCacheRatio(snapshot.CacheReadTokens, snapshot.CacheCreationTokens, snapshot.InputTokens)
}

func metricBucketState(bucket accountMetricBucket) string {
	if bucket.requests <= 0 {
		return "empty"
	}
	if bucket.status429 > 0 || bucket.status529 > 0 || bucket.status5xx > 0 || bucket.failures > bucket.success {
		return "red"
	}
	if bucket.failures > 0 {
		return "yellow"
	}
	return "green"
}

func formatMetricBucketLabel(bucketID int64) string {
	start := time.Unix(bucketID*metricsBucketSeconds, 0).In(time.Local)
	return start.Format("15:04")
}

func realCacheRatio(readTokens, creationTokens, inputTokens int64) float64 {
	denominator := readTokens + creationTokens + inputTokens
	if denominator <= 0 {
		return 0
	}
	return float64(readTokens) / float64(denominator)
}

func positiveInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

type usageMetricsPlugin struct{}

func (usageMetricsPlugin) HandleUsage(_ context.Context, record cliproxyusage.Record) {
	if strings.TrimSpace(record.Provider) != "claude" || strings.TrimSpace(record.AuthID) == "" {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(record.Source), "config:claude-api-pool[") {
		return
	}
	statusCode := record.Fail.StatusCode
	RecordRouteUsage(RouteUsage{
		AuthID:              record.AuthID,
		StatusCode:          statusCode,
		Success:             !record.Failed,
		Latency:             record.Latency,
		InputTokens:         record.Detail.InputTokens,
		OutputTokens:        record.Detail.OutputTokens,
		CacheReadTokens:     record.Detail.CacheReadTokens,
		CacheCreationTokens: record.Detail.CacheCreationTokens,
	})
}

func init() {
	cliproxyusage.RegisterPlugin(usageMetricsPlugin{})
}
