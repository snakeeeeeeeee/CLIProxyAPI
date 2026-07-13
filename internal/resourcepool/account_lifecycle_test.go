package resourcepool

import (
	"testing"
	"time"
)

func TestBuildQuotaWindowStatesDistinguishesConfidenceAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	checkedAt := now.Add(-2 * time.Minute)
	staleAt := now.Add(-quotaFreshnessTTL - time.Second)
	resetAt := now.Add(24 * time.Hour)
	known := true
	unknown := false
	remaining := 0.0
	quota := &AccountQuota{
		CheckedAt: &checkedAt,
		Windows: []QuotaWindow{
			{Key: "five_hour", UsedPercent: 20, RemainPercent: 80, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &checkedAt, ResetsAt: &resetAt},
			{Key: "seven_day", UsedPercent: 35, RemainPercent: 65, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &checkedAt, ResetsAt: &resetAt},
			{Key: "seven_day_opus", UtilizationKnown: &unknown, Status: "allowed", Source: "response_headers", UpdatedAt: &checkedAt, ResetsAt: &resetAt},
			{Key: "seven_day_fable", UtilizationKnown: &unknown, Status: "rejected", Remaining: &remaining, Source: "response_headers", UpdatedAt: &staleAt, ResetsAt: &resetAt},
		},
	}

	states := buildQuotaWindowStates(quota, now)
	if len(states) != 5 {
		t.Fatalf("states len = %d, want 5: %#v", len(states), states)
	}
	byKey := make(map[string]QuotaWindowState, len(states))
	for _, state := range states {
		byKey[state.Key] = state
	}

	fiveHour := byKey["five_hour"]
	if fiveHour.Confidence != quotaConfidenceExact || fiveHour.Freshness != quotaFreshnessFresh || fiveHour.Source != "oauth_usage" || fiveHour.ObservedAt == nil || !fiveHour.ObservedAt.Equal(checkedAt) {
		t.Fatalf("five hour state = %#v", fiveHour)
	}
	if fiveHour.RemainPercent == nil || *fiveHour.RemainPercent != 80 {
		t.Fatalf("five hour percentage = %#v", fiveHour.RemainPercent)
	}

	sonnet := byKey["seven_day_sonnet"]
	if sonnet.Confidence != quotaConfidenceShared || sonnet.SharedFrom != "seven_day" || sonnet.Freshness != quotaFreshnessFresh || sonnet.RemainPercent == nil || *sonnet.RemainPercent != 65 {
		t.Fatalf("sonnet shared state = %#v", sonnet)
	}

	opus := byKey["seven_day_opus"]
	if opus.Confidence != quotaConfidenceObserved || opus.Freshness != quotaFreshnessFresh || opus.UtilizationKnown || opus.Source != "response_headers" {
		t.Fatalf("opus observed state = %#v", opus)
	}

	fable := byKey["seven_day_fable"]
	if fable.Confidence != quotaConfidenceObserved || fable.Freshness != quotaFreshnessStale || !fable.Exhausted {
		t.Fatalf("fable stale state = %#v", fable)
	}
}

func TestBuildQuotaWindowStatesMarksMissingAndElapsedWindows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedAt := now.Add(-time.Minute)
	resetAt := now.Add(-time.Second)
	known := true
	states := buildQuotaWindowStates(&AccountQuota{
		CheckedAt: &observedAt,
		Windows: []QuotaWindow{{
			Key:              "five_hour",
			UsedPercent:      10,
			RemainPercent:    90,
			UtilizationKnown: &known,
			Source:           "oauth_usage",
			UpdatedAt:        &observedAt,
			ResetsAt:         &resetAt,
		}},
	}, now)

	byKey := make(map[string]QuotaWindowState, len(states))
	for _, state := range states {
		byKey[state.Key] = state
	}
	if got := byKey["five_hour"].Freshness; got != quotaFreshnessStale {
		t.Fatalf("elapsed five hour freshness = %q, want stale", got)
	}
	for _, key := range []string{"seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_fable"} {
		state := byKey[key]
		if state.Confidence != quotaConfidenceUnknown || state.Freshness != quotaFreshnessUnknown || state.Source != "" || state.ObservedAt != nil {
			t.Fatalf("missing state %s = %#v", key, state)
		}
	}
}
