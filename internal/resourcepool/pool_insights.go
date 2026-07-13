package resourcepool

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
)

const poolHealthWindow = time.Hour

var poolInsightModelFamilies = []struct {
	name  string
	model string
	key   string
}{
	{name: "sonnet", model: "claude-sonnet", key: "seven_day_sonnet"},
	{name: "opus", model: "claude-opus", key: "seven_day_opus"},
	{name: "fable", model: "claude-fable", key: "seven_day_fable"},
}

type poolInsightAccount struct {
	account  ClaudeCodeAccount
	capacity AccountCapacityConfig
}

type modelCapacityAccumulator struct {
	item        ModelCapacityItem
	headroomSum float64
}

type poolInsightAccumulator struct {
	pool              ClaudeCodeAccountPool
	insight           AccountPoolOperationalInsights
	models            map[string]*modelCapacityAccumulator
	adminAllowed      int
	readyAccounts     int
	reliabilityTotal  int64
	reliabilityOK     int64
	concurrencyLimit  int64
	rpmLimit          int
	sessionLimit      int
	authRecoveryCount int
	checkingCount     int
	proxyIssueCount   int
}

// AccountPoolInsights computes one batched operational snapshot without upstream requests.
func (s *Store) AccountPoolInsights(ctx context.Context, now time.Time) (AccountPoolInsightsSnapshot, error) {
	if s == nil || s.db == nil {
		return AccountPoolInsightsSnapshot{}, fmt.Errorf("resource pool store is nil")
	}
	if now.IsZero() {
		now = time.Now()
	}
	pools, err := s.ListAccountPools(ctx, true)
	if err != nil {
		return AccountPoolInsightsSnapshot{}, err
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return AccountPoolInsightsSnapshot{}, err
	}
	globalConfig := EffectiveClaudeCodePool(doc.ClaudeCode)
	poolConfigs := make(map[string]EffectiveClaudeCodePoolConfig, len(pools))
	accumulators := make(map[string]*poolInsightAccumulator, len(pools))
	poolByID := make(map[string]ClaudeCodeAccountPool, len(pools))
	for _, pool := range pools {
		overrides, errDecode := decodeAccountPoolConfigOverrides(pool.configJSON)
		if errDecode != nil {
			return AccountPoolInsightsSnapshot{}, fmt.Errorf("decode account pool %s config: %w", pool.ID, errDecode)
		}
		poolConfigs[pool.ID] = effectiveClaudeCodePoolWithOverrides(globalConfig, overrides)
		poolByID[pool.ID] = pool
		accumulators[pool.ID] = newPoolInsightAccumulator(pool)
	}
	globalAccumulator := newPoolInsightAccumulator(ClaudeCodeAccountPool{ID: "global", Name: "global", Enabled: true})

	accounts, err := s.loadPoolInsightAccounts(ctx, poolConfigs, now)
	if err != nil {
		return AccountPoolInsightsSnapshot{}, err
	}
	for _, row := range accounts {
		accumulator := accumulators[row.account.PoolID]
		if accumulator == nil {
			pool := ClaudeCodeAccountPool{ID: row.account.PoolID, Name: row.account.PoolID, Enabled: true}
			accumulator = newPoolInsightAccumulator(pool)
			accumulators[row.account.PoolID] = accumulator
			poolByID[row.account.PoolID] = pool
		}
		accumulator.addAccount(row, now)
		pool := poolByID[row.account.PoolID]
		if pool.Enabled && pool.ArchivedAt == nil {
			globalAccumulator.addAccount(row, now)
		}
	}

	keyCounts, err := s.loadPoolAPIKeyCounts(ctx)
	if err != nil {
		return AccountPoolInsightsSnapshot{}, err
	}
	for poolID, count := range keyCounts {
		if accumulator := accumulators[poolID]; accumulator != nil {
			accumulator.insight.APIKeyCount = count
		}
		if pool := poolByID[poolID]; pool.Enabled && pool.ArchivedAt == nil {
			globalAccumulator.insight.APIKeyCount += count
		}
	}

	reliability, err := s.loadPoolReliability(ctx, now.Add(-poolHealthWindow))
	if err != nil {
		return AccountPoolInsightsSnapshot{}, err
	}
	for poolID, sample := range reliability {
		if accumulator := accumulators[poolID]; accumulator != nil {
			accumulator.reliabilityTotal = sample.total
			accumulator.reliabilityOK = sample.success
		}
		if pool := poolByID[poolID]; pool.Enabled && pool.ArchivedAt == nil {
			globalAccumulator.reliabilityTotal += sample.total
			globalAccumulator.reliabilityOK += sample.success
		}
	}

	snapshot := AccountPoolInsightsSnapshot{Pools: make(map[string]AccountPoolOperationalInsights, len(accumulators))}
	for _, pool := range pools {
		accumulator := accumulators[pool.ID]
		if accumulator == nil {
			continue
		}
		insight := accumulator.finalize(now)
		snapshot.Pools[pool.ID] = insight
		if pool.ArchivedAt == nil {
			incrementPoolHealthDistribution(&snapshot.Distribution, insight.Health.Status)
		}
	}
	snapshot.Global = globalAccumulator.finalize(now)
	return snapshot, nil
}

func newPoolInsightAccumulator(pool ClaudeCodeAccountPool) *poolInsightAccumulator {
	return &poolInsightAccumulator{
		pool: pool,
		models: map[string]*modelCapacityAccumulator{
			"sonnet": {},
			"opus":   {},
			"fable":  {},
		},
	}
}

func (s *Store) loadPoolInsightAccounts(ctx context.Context, poolConfigs map[string]EffectiveClaudeCodePoolConfig, now time.Time) ([]poolInsightAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, CASE WHEN TRIM(a.auth_json) <> '' THEN 1 ELSE 0 END,
       a.enabled, a.health_status, a.blocked_until, a.blocked_reason, a.excluded_models_json,
       COALESCE(a.proxy_resource_id, ''), p.enabled, p.health_status,
       q.status, q.windows_json, q.checked_at,
       c.account_id, c.base_rpm, c.concurrency_limit, c.max_sessions, c.sticky_buffer, c.updated_at
FROM claude_code_accounts a
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
LEFT JOIN claude_code_account_quota q ON q.account_id = a.id
LEFT JOIN claude_code_account_capacity c ON c.account_id = a.id
ORDER BY a.pool_id, a.id
	`)
	if err != nil {
		return nil, fmt.Errorf("load account-pool insight accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]poolInsightAccount, 0)
	for rows.Next() {
		var row poolInsightAccount
		var hasAuth, enabled int
		var blockedUntil sql.NullString
		var excludedJSON string
		var proxyID string
		var proxyEnabled sql.NullInt64
		var proxyHealth sql.NullString
		var quotaStatus, quotaWindows, quotaChecked sql.NullString
		var capacityAccount, capacityUpdated sql.NullString
		var capacityRPM, capacityConcurrency, capacitySessions, capacityReserve sql.NullInt64
		if err := rows.Scan(
			&row.account.ID,
			&row.account.PoolID,
			&row.account.AuthID,
			&hasAuth,
			&enabled,
			&row.account.HealthStatus,
			&blockedUntil,
			&row.account.BlockedReason,
			&excludedJSON,
			&proxyID,
			&proxyEnabled,
			&proxyHealth,
			&quotaStatus,
			&quotaWindows,
			&quotaChecked,
			&capacityAccount,
			&capacityRPM,
			&capacityConcurrency,
			&capacitySessions,
			&capacityReserve,
			&capacityUpdated,
		); err != nil {
			return nil, fmt.Errorf("scan account-pool insight account: %w", err)
		}
		row.account.Enabled = enabled != 0
		row.account.Schedulable = row.account.Enabled
		row.account.HasAuthData = hasAuth != 0
		row.account.HealthStatus = normalizeAccountHealthStatus(row.account.HealthStatus)
		row.account.BlockedUntil = parseNullTime(blockedUntil)
		row.account.ExcludedModels = decodeStringList(excludedJSON)
		if proxyID != "" {
			row.account.ProxyResourceID = proxyID
			row.account.Proxy = &ProxyResource{
				ID:           proxyID,
				Enabled:      proxyEnabled.Valid && proxyEnabled.Int64 != 0,
				HealthStatus: normalizeHealthStatus(proxyHealth.String, proxyEnabled.Valid && proxyEnabled.Int64 != 0),
			}
		}
		if quotaStatus.Valid || quotaWindows.Valid || quotaChecked.Valid {
			row.account.Quota = &AccountQuota{
				AccountID: row.account.ID,
				Status:    normalizeQuotaStatus(quotaStatus.String),
				Windows:   normalizeQuotaWindows(decodeQuotaWindows(quotaWindows.String)),
				CheckedAt: parseNullTime(quotaChecked),
			}
		}
		row.account.applyDerivedHealth(now)

		effective := poolConfigs[row.account.PoolID]
		defaults := AccountCapacityConfig{
			AccountID:                row.account.ID,
			BaseRPM:                  effective.Routing.PerAccountRPM,
			ConcurrencyLimit:         effective.Routing.PerAccountConcurrency,
			MaxSessions:              effective.Routing.MaxSessions,
			StickyConcurrencyReserve: effective.Routing.StickyConcurrencyReserve,
		}
		row.capacity = defaults
		if capacityAccount.Valid {
			row.capacity = AccountCapacityConfig{
				AccountID:                row.account.ID,
				BaseRPM:                  int(capacityRPM.Int64),
				ConcurrencyLimit:         int(capacityConcurrency.Int64),
				MaxSessions:              int(capacitySessions.Int64),
				StickyConcurrencyReserve: int(capacityReserve.Int64),
				UpdatedAt:                parseDBTime(capacityUpdated.String),
			}
			row.capacity = *normalizeAccountCapacity(row.capacity, defaults)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account-pool insight accounts: %w", err)
	}
	return out, nil
}

func (s *Store) loadPoolAPIKeyCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pool_id, COUNT(*)
FROM claude_code_pool_api_keys
WHERE revoked_at IS NULL
GROUP BY pool_id
	`)
	if err != nil {
		return nil, fmt.Errorf("load account-pool api key counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int)
	for rows.Next() {
		var poolID string
		var count int
		if err := rows.Scan(&poolID, &count); err != nil {
			return nil, fmt.Errorf("scan account-pool api key count: %w", err)
		}
		out[normalizeAccountPoolID(poolID)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account-pool api key counts: %w", err)
	}
	return out, nil
}

type poolReliabilitySample struct {
	total   int64
	success int64
}

func (s *Store) loadPoolReliability(ctx context.Context, cutoff time.Time) (map[string]poolReliabilitySample, error) {
	cutoffText := dbTime(cutoff)
	rows, err := s.db.QueryContext(ctx, `
WITH ledger_rollup AS (
    SELECT pool_id,
           CASE WHEN TRIM(request_id) <> '' THEN request_id ELSE 'ledger:' || id END AS request_key,
           MAX(success) AS success,
           MAX(CASE WHEN status_code IN (400, 422, 499) THEN 1 ELSE 0 END) AS client_fault
    FROM claude_code_usage_ledger
    WHERE created_at >= ?
    GROUP BY pool_id, request_key
), routing_rollup AS (
    SELECT e.pool_id,
           CASE WHEN TRIM(e.request_id) <> '' THEN e.request_id ELSE 'event:' || e.id END AS request_key,
           0 AS success,
           MAX(CASE WHEN e.status_code IN (400, 422, 499)
                     OR lower(e.reason) LIKE '%cancel%'
                     OR lower(e.error) LIKE '%cancel%'
                    THEN 1 ELSE 0 END) AS client_fault
    FROM claude_code_routing_events e
    WHERE e.created_at >= ?
      AND e.decision = 'rejected'
      AND (TRIM(e.request_id) = '' OR NOT EXISTS (
          SELECT 1 FROM claude_code_usage_ledger l
          WHERE l.created_at >= ? AND l.pool_id = e.pool_id AND l.request_id = e.request_id
      ))
    GROUP BY e.pool_id, request_key
), final_requests AS (
    SELECT pool_id, request_key, success FROM ledger_rollup WHERE success = 1 OR client_fault = 0
    UNION ALL
    SELECT pool_id, request_key, success FROM routing_rollup WHERE client_fault = 0
)
SELECT pool_id, COUNT(*), COALESCE(SUM(success), 0)
FROM final_requests
GROUP BY pool_id
	`, cutoffText, cutoffText, cutoffText)
	if err != nil {
		return nil, fmt.Errorf("load account-pool reliability: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]poolReliabilitySample)
	for rows.Next() {
		var poolID string
		var sample poolReliabilitySample
		if err := rows.Scan(&poolID, &sample.total, &sample.success); err != nil {
			return nil, fmt.Errorf("scan account-pool reliability: %w", err)
		}
		out[normalizeAccountPoolID(poolID)] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account-pool reliability: %w", err)
	}
	return out, nil
}

func (a *poolInsightAccumulator) addAccount(row poolInsightAccount, now time.Time) {
	if a == nil {
		return
	}
	account := row.account
	a.insight.AccountCount++
	for _, family := range poolInsightModelFamilies {
		a.models[family.name].item.AccountCount++
	}
	if account.Schedulable {
		a.adminAllowed++
	}

	override := claudeapipool.EffectiveRoutingConfig{
		PerAccountRPM:            row.capacity.BaseRPM,
		PerAccountConcurrency:    row.capacity.ConcurrencyLimit,
		StickyConcurrencyReserve: row.capacity.StickyConcurrencyReserve,
		MaxSessions:              row.capacity.MaxSessions,
	}
	scope := AccountRoutingScope(account.PoolID)
	status := claudeapipool.AggregateScopedRouteStatusWithPolicy(scope, account.AuthID, override)
	ready := account.EffectiveSchedulable && !status.AccountCooling
	if ready {
		a.readyAccounts++
		a.insight.HealthyAccountCount++
		if !status.Unavailable {
			a.insight.AvailableAccounts++
		}
		a.insight.InFlight += status.InFlight
		a.insight.RPMUsed += status.RPMUsed
		a.insight.RPMLimit += status.RPMLimit
		a.insight.ActiveSessions += status.ActiveSessions
		a.insight.MaxSessions += status.MaxSessions
		a.concurrencyLimit += int64(row.capacity.ConcurrencyLimit)
		a.rpmLimit += status.RPMLimit
		a.sessionLimit += status.MaxSessions
	}
	if status.Cooling {
		a.insight.CoolingAccounts++
	}
	if account.Schedulable {
		switch account.HealthStatus {
		case AccountHealthManualRecovery:
			a.authRecoveryCount++
		case AccountHealthChecking:
			a.checkingCount++
		}
		if account.Proxy != nil && (!account.Proxy.Enabled || account.Proxy.HealthStatus == HealthUnhealthy) {
			a.proxyIssueCount++
		}
	}

	states := make(map[string]QuotaWindowState, len(account.QuotaWindowStates))
	for _, state := range account.QuotaWindowStates {
		states[state.Key] = state
	}
	for _, family := range poolInsightModelFamilies {
		modelStatus := claudeapipool.ScopedRouteStatusForWithPolicy(scope, account.AuthID, family.model, override)
		a.addModelAccount(family, account, states[family.key], ready, modelStatus, now)
	}
}

func (a *poolInsightAccumulator) addModelAccount(family struct {
	name  string
	model string
	key   string
}, account ClaudeCodeAccount, state QuotaWindowState, baseEligible bool, routeStatus claudeapipool.RouteStatus, now time.Time) {
	model := a.models[family.name]
	if model == nil || !baseEligible || accountExcludesModelFamily(account.ExcludedModels, family.name) {
		return
	}
	model.item.EligibleCount++
	if state.ObservedAt != nil && (model.item.LatestObservationTime == nil || state.ObservedAt.After(*model.item.LatestObservationTime)) {
		observed := *state.ObservedAt
		model.item.LatestObservationTime = &observed
	}
	switch state.Confidence {
	case quotaConfidenceExact:
		model.item.ExactCount++
	case quotaConfidenceShared:
		model.item.SharedCount++
	case quotaConfidenceObserved:
		model.item.ObservedCount++
	}

	exhausted := state.Freshness == quotaFreshnessFresh && state.Exhausted
	if state.Freshness == quotaFreshnessStale {
		model.item.StaleCount++
	} else if state.Confidence == quotaConfidenceUnknown || state.Freshness == quotaFreshnessUnknown {
		model.item.UnknownCount++
	} else if exhausted {
		model.item.ExhaustedCount++
		model.item.MeasuredCount++
	} else if state.Freshness == quotaFreshnessFresh && state.UtilizationKnown && state.Confidence != quotaConfidenceObserved {
		evaluation := evaluateQuotaRouting(account.Quota.Windows, account.Quota.CheckedAt, family.model, now)
		if evaluation.Known {
			model.item.MeasuredCount++
			model.headroomSum += evaluation.Headroom
		} else {
			model.item.UnknownCount++
		}
	}
	if a.pool.Enabled && a.pool.ArchivedAt == nil && !exhausted && !routeStatus.Unavailable {
		model.item.RoutableCount++
	}
}

func (a *poolInsightAccumulator) finalize(now time.Time) AccountPoolOperationalInsights {
	if a == nil {
		return AccountPoolOperationalInsights{}
	}
	for name, model := range a.models {
		if model.item.MeasuredCount > 0 {
			average := model.headroomSum / float64(model.item.MeasuredCount)
			model.item.AverageHeadroom = float64Ptr(roundFloat(average, 4))
		}
		model.item.HeadroomEquivalent = roundFloat(model.headroomSum, 4)
		if model.item.EligibleCount > 0 {
			model.item.Coverage = roundFloat(float64(model.item.MeasuredCount)/float64(model.item.EligibleCount), 4)
		}
		switch name {
		case "sonnet":
			a.insight.ModelCapacity.Sonnet = model.item
		case "opus":
			a.insight.ModelCapacity.Opus = model.item
		case "fable":
			a.insight.ModelCapacity.Fable = model.item
		}
	}
	a.insight.Health = a.calculateHealth(now)
	return a.insight
}

func (a *poolInsightAccumulator) calculateHealth(now time.Time) PoolHealthSummary {
	health := PoolHealthSummary{
		Status:     "empty",
		Components: make(map[string]PoolHealthComponent, 4),
		Issues:     []PoolHealthIssue{},
		AsOf:       now,
	}
	readiness := PoolHealthComponent{BaseWeight: 0.4, SampleCount: int64(a.adminAllowed)}
	if a.adminAllowed > 0 {
		score := 100 * float64(a.readyAccounts) / float64(a.adminAllowed)
		readiness.Score = float64Ptr(roundFloat(score, 1))
		readiness.Coverage = 1
		readiness.EffectiveWeight = readiness.BaseWeight
	}
	health.Components["account_readiness"] = readiness

	reliability := PoolHealthComponent{BaseWeight: 0.3, SampleCount: a.reliabilityTotal}
	if a.reliabilityTotal > 0 {
		score := 100 * float64(a.reliabilityOK) / float64(a.reliabilityTotal)
		reliability.Score = float64Ptr(roundFloat(score, 1))
		reliability.Coverage = math.Min(1, float64(a.reliabilityTotal)/10)
		reliability.EffectiveWeight = reliability.BaseWeight * reliability.Coverage
	}
	health.Components["request_reliability"] = reliability

	quota := PoolHealthComponent{BaseWeight: 0.2}
	capacityItems := []ModelCapacityItem{a.insight.ModelCapacity.Sonnet, a.insight.ModelCapacity.Opus, a.insight.ModelCapacity.Fable}
	eligibleTotal := 0
	measuredTotal := 0
	weakest := 1.0
	hasQuotaScore := false
	for _, item := range capacityItems {
		eligibleTotal += item.EligibleCount
		measuredTotal += item.MeasuredCount
		if item.AverageHeadroom != nil {
			hasQuotaScore = true
			if *item.AverageHeadroom < weakest {
				weakest = *item.AverageHeadroom
			}
		}
	}
	quota.SampleCount = int64(measuredTotal)
	if hasQuotaScore && eligibleTotal > 0 {
		quota.Score = float64Ptr(roundFloat(weakest*100, 1))
		quota.Coverage = math.Min(1, float64(measuredTotal)/float64(eligibleTotal))
		quota.EffectiveWeight = quota.BaseWeight * quota.Coverage
	}
	health.Components["quota_resilience"] = quota

	load := PoolHealthComponent{BaseWeight: 0.1, SampleCount: int64(a.readyAccounts)}
	if a.readyAccounts > 0 {
		concurrencyPressure := ratioClamped(float64(a.insight.InFlight), float64(a.concurrencyLimit))
		rpmPressure := ratioClamped(float64(a.insight.RPMUsed), float64(a.rpmLimit))
		sessionPressure := ratioClamped(float64(a.insight.ActiveSessions), float64(a.sessionLimit))
		pressure := math.Max(concurrencyPressure, math.Max(rpmPressure, sessionPressure))
		score := 100 * (1 - pressure)
		load.Score = float64Ptr(roundFloat(score, 1))
		load.Coverage = 1
		load.EffectiveWeight = load.BaseWeight
	}
	health.Components["load_headroom"] = load

	if !a.pool.Enabled || a.pool.ArchivedAt != nil {
		health.Status = "paused"
		health.Issues = a.healthIssues(health)
		return health
	}
	if a.insight.AccountCount == 0 {
		health.Status = "empty"
		return health
	}
	if a.readyAccounts == 0 {
		score := 0.0
		health.Score = &score
		health.Status = "unavailable"
		health.Confidence = roundFloat(sumEffectiveWeights(health.Components), 3)
		health.Issues = a.healthIssues(health)
		return health
	}

	weightedScore := 0.0
	effectiveWeight := 0.0
	for _, component := range health.Components {
		if component.Score == nil || component.EffectiveWeight <= 0 {
			continue
		}
		weightedScore += *component.Score * component.EffectiveWeight
		effectiveWeight += component.EffectiveWeight
	}
	if effectiveWeight > 0 {
		score := roundFloat(weightedScore/effectiveWeight, 1)
		health.Score = &score
		health.Confidence = roundFloat(math.Min(1, effectiveWeight), 3)
		switch {
		case score >= 85:
			health.Status = "healthy"
		case score >= 65:
			health.Status = "attention"
		default:
			health.Status = "critical"
		}
	}
	health.Issues = a.healthIssues(health)
	return health
}

func (a *poolInsightAccumulator) healthIssues(health PoolHealthSummary) []PoolHealthIssue {
	issues := make([]PoolHealthIssue, 0, 8)
	if a.authRecoveryCount > 0 {
		issues = append(issues, PoolHealthIssue{Code: "auth_recovery", Severity: "critical", Message: "存在需要重新认证的账号", Count: a.authRecoveryCount})
	}
	if a.proxyIssueCount > 0 {
		issues = append(issues, PoolHealthIssue{Code: "proxy_unhealthy", Severity: "critical", Message: "存在不可用的绑定代理", Count: a.proxyIssueCount})
	}
	if a.checkingCount > 0 {
		issues = append(issues, PoolHealthIssue{Code: "account_checking", Severity: "warning", Message: "账号仍在检查中", Count: a.checkingCount})
	}
	for _, family := range poolInsightModelFamilies {
		item := modelCapacityByName(a.insight.ModelCapacity, family.name)
		if item.ExhaustedCount > 0 {
			severity := "warning"
			if item.EligibleCount > 0 && item.ExhaustedCount >= item.EligibleCount {
				severity = "critical"
			}
			issues = append(issues, PoolHealthIssue{Code: "model_exhausted", Severity: severity, Message: "模型额度已耗尽", Count: item.ExhaustedCount, Model: family.name})
		}
		if item.StaleCount > 0 {
			issues = append(issues, PoolHealthIssue{Code: "quota_stale", Severity: "warning", Message: "额度数据已过期", Count: item.StaleCount, Model: family.name})
		}
	}
	if component := health.Components["request_reliability"]; component.Score != nil && *component.Score < 90 {
		severity := "warning"
		if *component.Score < 65 && component.SampleCount >= 10 {
			severity = "critical"
		}
		issues = append(issues, PoolHealthIssue{Code: "low_success_rate", Severity: severity, Message: "最近 1 小时请求成功率偏低", Count: int(component.SampleCount)})
	}
	if component := health.Components["load_headroom"]; component.Score != nil && *component.Score < 30 {
		severity := "warning"
		if *component.Score < 10 {
			severity = "critical"
		}
		issues = append(issues, PoolHealthIssue{Code: "high_load", Severity: severity, Message: "账号池当前负载较高"})
	}
	if a.readyAccounts == 1 {
		issues = append(issues, PoolHealthIssue{Code: "single_point_risk", Severity: "warning", Message: "仅有 1 个有效可调度账号"})
	}
	sort.SliceStable(issues, func(i, j int) bool {
		left := healthIssueSeverity(issues[i].Severity)
		right := healthIssueSeverity(issues[j].Severity)
		if left != right {
			return left > right
		}
		if issues[i].Count != issues[j].Count {
			return issues[i].Count > issues[j].Count
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].Model < issues[j].Model
	})
	return issues
}

func accountExcludesModelFamily(excluded []string, family string) bool {
	family = strings.ToLower(strings.TrimSpace(family))
	for _, pattern := range excluded {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "*" || pattern == family || strings.Contains(pattern, family) {
			return true
		}
	}
	return false
}

func modelCapacityByName(summary ModelCapacitySummary, name string) ModelCapacityItem {
	switch name {
	case "sonnet":
		return summary.Sonnet
	case "opus":
		return summary.Opus
	case "fable":
		return summary.Fable
	default:
		return ModelCapacityItem{}
	}
}

func incrementPoolHealthDistribution(distribution *PoolHealthDistribution, status string) {
	if distribution == nil {
		return
	}
	switch status {
	case "healthy":
		distribution.Healthy++
	case "attention":
		distribution.Attention++
	case "critical":
		distribution.Critical++
	case "unavailable":
		distribution.Unavailable++
	case "paused":
		distribution.Paused++
	case "empty":
		distribution.Empty++
	}
}

func sumEffectiveWeights(components map[string]PoolHealthComponent) float64 {
	total := 0.0
	for _, component := range components {
		total += component.EffectiveWeight
	}
	return math.Min(1, total)
}

func ratioClamped(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Max(0, math.Min(1, numerator/denominator))
}

func roundFloat(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func float64Ptr(value float64) *float64 {
	return &value
}

func healthIssueSeverity(severity string) int {
	switch severity {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}
