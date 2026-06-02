package claudeapipool

import (
	"math"
	"strings"
	"sync"
)

const affinityAutoShrinkWindows = 3

// AffinityAutoPlan is the read-only runtime plan used by cache affinity auto mode.
type AffinityAutoPlan struct {
	Enabled           bool    `json:"enabled"`
	EffectiveLanes    int     `json:"effective_lanes"`
	EffectiveMaxLanes int     `json:"effective_max_lanes"`
	PoolSize          int     `json:"pool_size"`
	AvailableAccounts int     `json:"available_accounts"`
	Pressure          float64 `json:"pressure"`
	Reason            string  `json:"reason"`
}

type affinityAutoPlanInput struct {
	Policy            EffectiveRoutingConfig
	PoolSize          int
	AvailableAccounts int
	InFlight          int64
	RPMUsed           int
	RPMLimit          int
	RequestCount      int64
	Status429         int64
	Status529         int64
	Status5xx         int64
}

type affinityAutoPlanner struct {
	mu                 sync.Mutex
	plan               AffinityAutoPlan
	lowPressureWindows int
}

var defaultAffinityAutoPlanner = &affinityAutoPlanner{}

// CurrentAffinityAutoPlan returns the last runtime auto plan.
func CurrentAffinityAutoPlan() AffinityAutoPlan {
	return defaultAffinityAutoPlanner.current()
}

func updateAffinityAutoPlan(stats GlobalRuntimeStats, policy EffectiveRoutingConfig) AffinityAutoPlan {
	return defaultAffinityAutoPlanner.update(affinityAutoPlanInput{
		Policy:            policy,
		PoolSize:          stats.AccountCount,
		AvailableAccounts: stats.AvailableAccounts,
		InFlight:          stats.InFlight,
		RPMUsed:           stats.RPMUsed,
		RPMLimit:          stats.RPMLimit,
		RequestCount:      stats.RequestCount,
		Status429:         stats.Status429,
		Status529:         stats.Status529,
		Status5xx:         stats.Status5xx,
	})
}

func (p *affinityAutoPlanner) current() AffinityAutoPlan {
	if p == nil {
		return AffinityAutoPlan{Reason: "planner_unavailable"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.plan
}

func (p *affinityAutoPlanner) update(input affinityAutoPlanInput) AffinityAutoPlan {
	if p == nil {
		return buildAffinityAutoPlan(input)
	}
	desired := buildAffinityAutoPlan(input)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !desired.Enabled {
		p.plan = desired
		p.lowPressureWindows = 0
		return desired
	}
	previous := p.plan
	if previous.Enabled && affinityPlanShrinks(desired, previous) {
		if desired.Pressure < 0.5 && !affinityPlanHasErrorPressure(input) {
			p.lowPressureWindows++
		} else {
			p.lowPressureWindows = 0
		}
		if p.lowPressureWindows < affinityAutoShrinkWindows {
			held := desired
			held.EffectiveLanes = minInt(maxInt(previous.EffectiveLanes, desired.EffectiveLanes), maxInt(0, desired.AvailableAccounts))
			held.EffectiveMaxLanes = minInt(maxInt(previous.EffectiveMaxLanes, desired.EffectiveMaxLanes), maxInt(held.EffectiveLanes, desired.AvailableAccounts))
			if held.EffectiveMaxLanes < held.EffectiveLanes {
				held.EffectiveMaxLanes = held.EffectiveLanes
			}
			held.Reason = "shrink_debounce"
			p.plan = held
			return held
		}
	}
	p.lowPressureWindows = 0
	p.plan = desired
	return desired
}

func buildAffinityAutoPlan(input affinityAutoPlanInput) AffinityAutoPlan {
	policy := normalizeEffectiveRoutingConfig(input.Policy)
	poolSize := maxInt(0, input.PoolSize)
	available := input.AvailableAccounts
	if available <= 0 && poolSize > 0 {
		available = poolSize
	}
	if available > poolSize && poolSize > 0 {
		available = poolSize
	}
	if available < 0 {
		available = 0
	}
	manualLanes := clampInt(policy.CacheAffinityLanes, 1, maxInt(1, poolSize))
	manualMax := clampInt(maxInt(policy.CacheAffinityMaxLanes, manualLanes), manualLanes, maxInt(manualLanes, poolSize))
	if !policy.CacheAffinityAuto {
		return AffinityAutoPlan{
			Enabled:           false,
			EffectiveLanes:    manualLanes,
			EffectiveMaxLanes: manualMax,
			PoolSize:          poolSize,
			AvailableAccounts: available,
			Pressure:          0,
			Reason:            "auto_disabled",
		}
	}
	plan := AffinityAutoPlan{
		Enabled:           true,
		PoolSize:          poolSize,
		AvailableAccounts: available,
	}
	if poolSize <= 0 || available <= 0 {
		plan.EffectiveLanes = 0
		plan.EffectiveMaxLanes = 0
		plan.Reason = "no_available_accounts"
		return plan
	}
	base := affinityBaseLanes(poolSize)
	pressure, errorBoost := affinityPlanPressure(input, policy, available)
	boost := affinityPressureBoost(pressure, errorBoost)
	hardMax := affinityAutoProfileMaxLanes(policy.CacheAffinityAutoProfile, poolSize, policy.CacheAffinityMaxLanes)
	if hardMax > available {
		hardMax = available
	}
	configLanes := policy.CacheAffinityLanes
	if configLanes <= 0 {
		configLanes = 1
	}
	effective := clampInt(maxInt(configLanes, base+boost), 1, hardMax)
	effectiveMax := clampInt(maxInt(effective, effective*2), effective, hardMax)
	plan.EffectiveLanes = effective
	plan.EffectiveMaxLanes = effectiveMax
	plan.Pressure = pressure
	plan.Reason = affinityPlanReason(poolSize, pressure, errorBoost, boost)
	return plan
}

func affinityBaseLanes(poolSize int) int {
	switch {
	case poolSize <= 0:
		return 0
	case poolSize <= 3:
		return 1
	case poolSize <= 10:
		return 2
	case poolSize <= 30:
		return 3
	case poolSize <= 80:
		return 4
	default:
		return minInt(8, int(math.Sqrt(float64(poolSize))))
	}
}

func affinityAutoProfileMaxLanes(profile string, poolSize, configuredMax int) int {
	if strings.TrimSpace(profile) == "" {
		return configuredMax
	}
	if poolSize <= 0 {
		return maxInt(0, configuredMax)
	}
	root := int(math.Ceil(math.Sqrt(float64(poolSize))))
	if root < 1 {
		root = 1
	}
	profileMax := 0
	switch profile {
	case AffinityAutoProfileCost:
		profileMax = minInt(16, maxInt(2, root))
	case AffinityAutoProfileThroughput:
		profileMax = minInt(64, maxInt(4, root*4))
	default:
		profileMax = minInt(32, maxInt(2, root*2))
	}
	if configuredMax > profileMax {
		return configuredMax
	}
	return profileMax
}

func affinityPlanPressure(input affinityAutoPlanInput, policy EffectiveRoutingConfig, available int) (float64, bool) {
	rpmPressure := 0.0
	if input.RPMLimit > 0 {
		rpmPressure = float64(maxInt(0, input.RPMUsed)) / float64(input.RPMLimit)
	}
	concurrencyPressure := 0.0
	if policy.PerAccountConcurrency > 0 && available > 0 {
		concurrencyLimit := int64(policy.PerAccountConcurrency * available)
		if concurrencyLimit > 0 {
			concurrencyPressure = float64(positivePlanInt64(input.InFlight)) / float64(concurrencyLimit)
		}
	}
	errorEvents := positivePlanInt64(input.Status429) + positivePlanInt64(input.Status529) + positivePlanInt64(input.Status5xx)
	errorPressure := 0.0
	if input.RequestCount > 0 {
		errorPressure = float64(errorEvents) / float64(input.RequestCount)
	}
	errorBoost := input.Status429 > 0 || input.Status529 > 0 || errorPressure >= 0.05
	pressure := math.Max(rpmPressure, math.Max(concurrencyPressure, errorPressure))
	if errorBoost && pressure < 0.9 {
		pressure = 0.9
	}
	return clampFloat64(pressure, 0, 1), errorBoost
}

func affinityPressureBoost(pressure float64, errorBoost bool) int {
	if errorBoost || pressure > 0.9 {
		return 3
	}
	switch {
	case pressure >= 0.75:
		return 2
	case pressure >= 0.5:
		return 1
	default:
		return 0
	}
}

func affinityPlanReason(poolSize int, pressure float64, errorBoost bool, boost int) string {
	if errorBoost {
		return "error_pressure"
	}
	if poolSize <= 3 && boost == 0 {
		return "small_pool_low_pressure"
	}
	switch {
	case pressure >= 0.75:
		return "high_pressure"
	case pressure >= 0.5:
		return "medium_pressure"
	default:
		return "low_pressure"
	}
}

func affinityPlanShrinks(next, previous AffinityAutoPlan) bool {
	return next.EffectiveLanes < previous.EffectiveLanes || next.EffectiveMaxLanes < previous.EffectiveMaxLanes
}

func affinityPlanHasErrorPressure(input affinityAutoPlanInput) bool {
	return input.Status429 > 0 || input.Status529 > 0 || input.Status5xx > 0
}

func affinityLaneBoundsForSelection(policy EffectiveRoutingConfig, ordered []string, unavailable map[string]struct{}) (int, int) {
	policy = normalizeEffectiveRoutingConfig(policy)
	poolSize := len(ordered)
	available := countAvailableAffinityAuths(ordered, unavailable)
	if !policy.CacheAffinityAuto {
		laneTarget := clampInt(policy.CacheAffinityLanes, 1, maxInt(1, poolSize))
		laneMax := clampInt(maxInt(policy.CacheAffinityMaxLanes, laneTarget), laneTarget, maxInt(laneTarget, poolSize))
		return laneTarget, laneMax
	}
	plan := CurrentAffinityAutoPlan()
	if !plan.Enabled || plan.EffectiveLanes <= 0 || plan.PoolSize <= 0 {
		plan = buildAffinityAutoPlan(affinityAutoPlanInput{
			Policy:            policy,
			PoolSize:          poolSize,
			AvailableAccounts: available,
		})
	}
	hardMax := policy.CacheAffinityMaxLanes
	if policy.CacheAffinityAuto {
		hardMax = affinityAutoProfileMaxLanes(policy.CacheAffinityAutoProfile, poolSize, hardMax)
	}
	if hardMax <= 0 {
		hardMax = poolSize
	}
	hardMax = minInt(hardMax, poolSize)
	if available > 0 {
		hardMax = minInt(hardMax, available)
	}
	if hardMax <= 0 {
		return 0, 0
	}
	laneTarget := clampInt(plan.EffectiveLanes, 1, hardMax)
	laneMax := clampInt(maxInt(plan.EffectiveMaxLanes, laneTarget), laneTarget, hardMax)
	return laneTarget, laneMax
}

func countAvailableAffinityAuths(authIDs []string, unavailable map[string]struct{}) int {
	if len(authIDs) == 0 {
		return 0
	}
	if len(unavailable) == 0 {
		return len(authIDs)
	}
	count := 0
	for _, authID := range authIDs {
		authID = strings.TrimSpace(authID)
		if authID == "" {
			continue
		}
		if _, blocked := unavailable[authID]; !blocked {
			count++
		}
	}
	return count
}

func clampInt(value, minValue, maxValue int) int {
	if maxValue < minValue {
		maxValue = minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func clampFloat64(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func positivePlanInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func resetAffinityAutoPlannerForTest() {
	defaultAffinityAutoPlanner = &affinityAutoPlanner{}
}
