package claudeapipool

import (
	"testing"
	"time"
)

func TestRouteMetricsRollingAccountSnapshot(t *testing.T) {
	collector := &routeMetricsCollector{accounts: make(map[string]*accountMetrics)}
	now := time.Unix(1_700_000_000, 0)
	collector.record(RouteUsage{
		AuthID:              "auth-a",
		StatusCode:          200,
		Success:             true,
		Latency:             120 * time.Millisecond,
		InputTokens:         10,
		OutputTokens:        5,
		CacheReadTokens:     90,
		CacheCreationTokens: 10,
	}, now)
	collector.record(RouteUsage{
		AuthID:     "auth-a",
		StatusCode: StatusTooManyRequests,
		Success:    false,
		Latency:    80 * time.Millisecond,
	}, now.Add(10*time.Second))

	got := collector.accountSnapshot("auth-a", now.Add(30*time.Second))
	if got.RequestCount != 2 || got.SuccessCount != 1 || got.FailureCount != 1 {
		t.Fatalf("counts = %#v", got)
	}
	if got.Status429 != 1 {
		t.Fatalf("Status429 = %d, want 1", got.Status429)
	}
	if got.RPM1m != 2 {
		t.Fatalf("RPM1m = %d, want 2", got.RPM1m)
	}
	if got.RealCacheRatio == 0 {
		t.Fatalf("RealCacheRatio = 0, want positive")
	}
	if len(got.History) != metricsBucketCount {
		t.Fatalf("history len = %d, want %d", len(got.History), metricsBucketCount)
	}
	if got.History[len(got.History)-1].State != "red" {
		t.Fatalf("last history state = %q, want red", got.History[len(got.History)-1].State)
	}
}
