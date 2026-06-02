package claudeapipool

import "testing"

func TestAffinityAutoPlannerLowPressureByPoolSize(t *testing.T) {
	t.Cleanup(resetAffinityAutoPlannerForTest)
	resetAffinityAutoPlannerForTest()
	cases := []struct {
		name       string
		poolSize   int
		wantLanes  int
		wantMax    int
		configMax  int
		configLane int
	}{
		{name: "two accounts", poolSize: 2, wantLanes: 1, wantMax: 2, configMax: 8, configLane: 1},
		{name: "ten accounts", poolSize: 10, wantLanes: 2, wantMax: 4, configMax: 8, configLane: 1},
		{name: "fifty accounts", poolSize: 50, wantLanes: 4, wantMax: 8, configMax: 8, configLane: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planner := &affinityAutoPlanner{}
			got := planner.update(affinityAutoPlanInput{
				Policy: EffectiveRouting(RoutingConfig{
					CacheAffinityAuto:     true,
					CacheAffinityLanes:    tc.configLane,
					CacheAffinityMaxLanes: tc.configMax,
				}),
				PoolSize:          tc.poolSize,
				AvailableAccounts: tc.poolSize,
			})
			if got.EffectiveLanes != tc.wantLanes || got.EffectiveMaxLanes != tc.wantMax {
				t.Fatalf("plan = %#v, want lanes=%d max=%d", got, tc.wantLanes, tc.wantMax)
			}
		})
	}
}

func TestAffinityAutoPlannerProfilesDeriveMaxLanes(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		wantMax int
	}{
		{name: "legacy blank", profile: "", wantMax: 7},
		{name: "cost", profile: AffinityAutoProfileCost, wantMax: 10},
		{name: "balanced", profile: AffinityAutoProfileBalanced, wantMax: 20},
		{name: "throughput", profile: AffinityAutoProfileThroughput, wantMax: 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := affinityAutoProfileMaxLanes(tc.profile, 100, 7)
			if got != tc.wantMax {
				t.Fatalf("affinityAutoProfileMaxLanes() = %d, want %d", got, tc.wantMax)
			}
		})
	}
}

func TestAffinityAutoPlannerPressureAndAvailabilityClamp(t *testing.T) {
	t.Cleanup(resetAffinityAutoPlannerForTest)
	planner := &affinityAutoPlanner{}
	got := planner.update(affinityAutoPlanInput{
		Policy: EffectiveRouting(RoutingConfig{
			PerAccountConcurrency: 2,
			CacheAffinityAuto:     true,
			CacheAffinityLanes:    1,
			CacheAffinityMaxLanes: 10,
		}),
		PoolSize:          10,
		AvailableAccounts: 3,
		InFlight:          6,
		RPMUsed:           90,
		RPMLimit:          100,
		RequestCount:      20,
	})
	if got.EffectiveLanes != 3 || got.EffectiveMaxLanes != 3 {
		t.Fatalf("plan = %#v, want clamped 3/3", got)
	}
	if got.Pressure < 0.9 {
		t.Fatalf("pressure = %.2f, want high pressure", got.Pressure)
	}
}

func TestAffinityAutoPlannerErrorsForceHighBoost(t *testing.T) {
	t.Cleanup(resetAffinityAutoPlannerForTest)
	planner := &affinityAutoPlanner{}
	got := planner.update(affinityAutoPlanInput{
		Policy: EffectiveRouting(RoutingConfig{
			CacheAffinityAuto:     true,
			CacheAffinityLanes:    1,
			CacheAffinityMaxLanes: 8,
		}),
		PoolSize:          10,
		AvailableAccounts: 10,
		RequestCount:      20,
		Status429:         1,
	})
	if got.EffectiveLanes != 5 || got.EffectiveMaxLanes != 8 {
		t.Fatalf("plan = %#v, want error-boosted 5/8", got)
	}
	if got.Reason != "error_pressure" {
		t.Fatalf("reason = %q, want error_pressure", got.Reason)
	}
}

func TestAffinityAutoPlannerShrinkDebounce(t *testing.T) {
	t.Cleanup(resetAffinityAutoPlannerForTest)
	planner := &affinityAutoPlanner{}
	high := affinityAutoPlanInput{
		Policy: EffectiveRouting(RoutingConfig{
			CacheAffinityAuto:     true,
			CacheAffinityLanes:    1,
			CacheAffinityMaxLanes: 8,
		}),
		PoolSize:          10,
		AvailableAccounts: 10,
		InFlight:          10,
		RPMUsed:           95,
		RPMLimit:          100,
		RequestCount:      10,
	}
	got := planner.update(high)
	if got.EffectiveLanes != 5 || got.EffectiveMaxLanes != 8 {
		t.Fatalf("high plan = %#v, want 5/8", got)
	}
	low := high
	low.InFlight = 0
	low.RPMUsed = 0
	got = planner.update(low)
	if got.EffectiveLanes != 5 || got.EffectiveMaxLanes != 8 || got.Reason != "shrink_debounce" {
		t.Fatalf("first low plan = %#v, want held 5/8", got)
	}
	_ = planner.update(low)
	got = planner.update(low)
	if got.EffectiveLanes != 2 || got.EffectiveMaxLanes != 4 {
		t.Fatalf("third low plan = %#v, want shrunk 2/4", got)
	}
}

func TestRuntimeStatsIncludesAffinityAutoPlan(t *testing.T) {
	t.Cleanup(func() {
		defaultRouteMetrics = &routeMetricsCollector{accounts: make(map[string]*accountMetrics)}
		resetAffinityAutoPlannerForTest()
		SetRoutingConfig(EffectiveRouting(RoutingConfig{}))
	})
	defaultRouteMetrics = &routeMetricsCollector{accounts: make(map[string]*accountMetrics)}
	resetAffinityAutoPlannerForTest()
	SetRoutingConfig(EffectiveRouting(RoutingConfig{
		CacheAffinityAuto:     true,
		CacheAffinityLanes:    1,
		CacheAffinityMaxLanes: 4,
	}))
	stats := RuntimeStats([]string{"auth-a", "auth-b"}, []RouteStatus{
		{RPMLimit: 20},
		{RPMLimit: 20},
	})
	if !stats.AffinityAutoPlan.Enabled {
		t.Fatalf("affinity auto plan disabled: %#v", stats.AffinityAutoPlan)
	}
	if stats.AffinityAutoPlan.EffectiveLanes != 1 || stats.AffinityAutoPlan.EffectiveMaxLanes != 2 {
		t.Fatalf("affinity auto plan = %#v, want 1/2", stats.AffinityAutoPlan)
	}
}
