package resourcepool

import (
	"context"
	"testing"
	"time"
)

func TestAccountPoolInsightsModelCapacityConfidence(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	account := registerInsightAccount(t, store, DefaultAccountPoolID, "capacity-auth")
	known := true
	unknown := false
	if _, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: account.ID,
		Status:    "ok",
		CheckedAt: &now,
		Windows: []QuotaWindow{
			{Key: "five_hour", UsedPercent: 10, RemainPercent: 90, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &now},
			{Key: "seven_day", UsedPercent: 30, RemainPercent: 70, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &now},
			{Key: "seven_day_sonnet", UsedPercent: 40, RemainPercent: 60, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &now},
			{Key: "seven_day_fable", UtilizationKnown: &unknown, Status: "allowed", Remaining: float64Ptr(1), Source: "response_headers", UpdatedAt: &now},
		},
	}); err != nil {
		t.Fatalf("SaveAccountQuota() error = %v", err)
	}

	snapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights() error = %v", err)
	}
	insight := snapshot.Pools[DefaultAccountPoolID]
	sonnet := insight.ModelCapacity.Sonnet
	if sonnet.EligibleCount != 1 || sonnet.RoutableCount != 1 || sonnet.MeasuredCount != 1 || sonnet.ExactCount != 1 {
		loaded, _ := store.GetAccount(ctx, account.ID)
		t.Fatalf("sonnet capacity = %+v; health = %+v; account = %+v", sonnet, insight.Health, loaded)
	}
	if sonnet.AverageHeadroom == nil || *sonnet.AverageHeadroom != 0.6 || sonnet.HeadroomEquivalent != 0.6 || sonnet.Coverage != 1 {
		t.Fatalf("sonnet headroom = %+v", sonnet)
	}
	opus := insight.ModelCapacity.Opus
	if opus.MeasuredCount != 1 || opus.SharedCount != 1 || opus.AverageHeadroom == nil || *opus.AverageHeadroom != 0.7 {
		t.Fatalf("opus capacity = %+v", opus)
	}
	fable := insight.ModelCapacity.Fable
	if fable.ObservedCount != 1 || fable.MeasuredCount != 0 || fable.AverageHeadroom != nil || fable.RoutableCount != 1 {
		t.Fatalf("fable capacity = %+v", fable)
	}
	if insight.Health.Confidence >= 1 {
		t.Fatalf("health confidence = %v, want reduced by missing reliability/Fable percentage", insight.Health.Confidence)
	}
}

func TestAccountPoolInsightsStaleAndExhaustedCapacity(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	staleAt := now.Add(-quotaFreshnessTTL - time.Second)
	known := true
	stale := registerInsightAccount(t, store, DefaultAccountPoolID, "stale-auth")
	if _, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: stale.ID,
		Status:    "ok",
		CheckedAt: &staleAt,
		Windows: []QuotaWindow{
			{Key: "five_hour", UsedPercent: 20, RemainPercent: 80, UtilizationKnown: &known, UpdatedAt: &staleAt},
			{Key: "seven_day", UsedPercent: 20, RemainPercent: 80, UtilizationKnown: &known, UpdatedAt: &staleAt},
			{Key: "seven_day_opus", UsedPercent: 20, RemainPercent: 80, UtilizationKnown: &known, UpdatedAt: &staleAt},
		},
	}); err != nil {
		t.Fatalf("SaveAccountQuota(stale) error = %v", err)
	}
	exhausted := registerInsightAccount(t, store, DefaultAccountPoolID, "exhausted-auth")
	if _, err := store.SaveAccountQuota(ctx, AccountQuota{
		AccountID: exhausted.ID,
		Status:    "ok",
		CheckedAt: &now,
		Windows: []QuotaWindow{
			{Key: "five_hour", UsedPercent: 10, RemainPercent: 90, UtilizationKnown: &known, UpdatedAt: &now},
			{Key: "seven_day", UsedPercent: 10, RemainPercent: 90, UtilizationKnown: &known, UpdatedAt: &now},
			{Key: "seven_day_opus", UsedPercent: 100, RemainPercent: 0, UtilizationKnown: &known, Status: "exhausted", UpdatedAt: &now},
		},
	}); err != nil {
		t.Fatalf("SaveAccountQuota(exhausted) error = %v", err)
	}

	snapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights() error = %v", err)
	}
	opus := snapshot.Pools[DefaultAccountPoolID].ModelCapacity.Opus
	if opus.EligibleCount != 2 || opus.StaleCount != 1 || opus.ExhaustedCount != 1 || opus.MeasuredCount != 1 {
		t.Fatalf("opus capacity = %+v", opus)
	}
	if opus.AverageHeadroom == nil || *opus.AverageHeadroom != 0 || opus.RoutableCount != 1 || opus.Coverage != 0.5 {
		t.Fatalf("opus exhausted headroom = %+v", opus)
	}
}

func TestAccountPoolInsightsReliabilityExcludesClientFaultsAndCancellation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	account := registerInsightAccount(t, store, DefaultAccountPoolID, "reliability-auth")
	for _, entry := range []UsageLedgerEntry{
		{PoolID: DefaultAccountPoolID, AccountID: account.ID, RequestID: "success", StatusCode: 200, Success: true, CreatedAt: now},
		{PoolID: DefaultAccountPoolID, AccountID: account.ID, RequestID: "failure", StatusCode: 529, Success: false, CreatedAt: now},
		{PoolID: DefaultAccountPoolID, AccountID: account.ID, RequestID: "client", StatusCode: 400, Success: false, CreatedAt: now},
	} {
		if err := store.RecordUsageLedger(ctx, entry); err != nil {
			t.Fatalf("RecordUsageLedger(%s) error = %v", entry.RequestID, err)
		}
	}
	for _, event := range []RoutingEvent{
		{PoolID: DefaultAccountPoolID, RequestID: "local", Decision: "rejected", Reason: "no_candidate", CreatedAt: now},
		{PoolID: DefaultAccountPoolID, RequestID: "cancelled", Decision: "rejected", Reason: "context canceled", CreatedAt: now},
	} {
		if err := store.RecordRoutingEvent(ctx, event); err != nil {
			t.Fatalf("RecordRoutingEvent(%s) error = %v", event.RequestID, err)
		}
	}

	snapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights() error = %v", err)
	}
	component := snapshot.Pools[DefaultAccountPoolID].Health.Components["request_reliability"]
	if component.SampleCount != 3 || component.Score == nil || *component.Score != 33.3 {
		t.Fatalf("reliability component = %+v", component)
	}
	if component.Coverage != 0.3 || component.EffectiveWeight != 0.09 {
		t.Fatalf("low-sample reliability weighting = %+v", component)
	}
}

func TestAccountPoolInsightsSpecialHealthStatesAndDistribution(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	emptySnapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights(empty) error = %v", err)
	}
	if health := emptySnapshot.Pools[DefaultAccountPoolID].Health; health.Status != "empty" || health.Score != nil {
		t.Fatalf("empty health = %+v", health)
	}

	account := registerInsightAccount(t, store, DefaultAccountPoolID, "unavailable-auth")
	if _, err := store.db.ExecContext(ctx, `UPDATE claude_code_accounts SET health_status = ? WHERE id = ?`, AccountHealthManualRecovery, account.ID); err != nil {
		t.Fatalf("mark manual recovery: %v", err)
	}
	unavailableSnapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights(unavailable) error = %v", err)
	}
	health := unavailableSnapshot.Pools[DefaultAccountPoolID].Health
	if health.Status != "unavailable" || health.Score == nil || *health.Score != 0 {
		t.Fatalf("unavailable health = %+v", health)
	}

	enabled := false
	if _, err := store.PatchAccountPool(ctx, DefaultAccountPoolID, ClaudeCodeAccountPoolPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("PatchAccountPool(paused) error = %v", err)
	}
	pausedSnapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights(paused) error = %v", err)
	}
	paused := pausedSnapshot.Pools[DefaultAccountPoolID].Health
	if paused.Status != "paused" || paused.Score != nil || pausedSnapshot.Distribution.Paused != 1 {
		t.Fatalf("paused health/distribution = %+v / %+v", paused, pausedSnapshot.Distribution)
	}
}

func TestAccountPoolInsightsMissingTrafficAndQuotaReduceConfidenceInsteadOfScore(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	registerInsightAccount(t, store, DefaultAccountPoolID, "no-samples-auth")

	snapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights() error = %v", err)
	}
	health := snapshot.Pools[DefaultAccountPoolID].Health
	if health.Status != "healthy" || health.Score == nil || *health.Score != 100 {
		t.Fatalf("health without traffic/quota = %+v", health)
	}
	if health.Confidence != 0.5 {
		t.Fatalf("health confidence = %v, want readiness+load weight 0.5", health.Confidence)
	}
	if health.Components["request_reliability"].Score != nil || health.Components["quota_resilience"].Score != nil {
		t.Fatalf("missing components should remain unknown: %+v", health.Components)
	}
	foundSinglePoint := false
	for _, issue := range health.Issues {
		if issue.Code == "single_point_risk" {
			foundSinglePoint = true
		}
	}
	if !foundSinglePoint {
		t.Fatalf("health issues = %+v, want single-point warning", health.Issues)
	}
}

func TestAccountPoolInsightsGlobalHealthAggregatesAccounts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	registerInsightAccount(t, store, DefaultAccountPoolID, "global-ready")
	unavailablePool, err := store.CreateAccountPool(ctx, "Unavailable", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	for index := 0; index < 3; index++ {
		account := registerInsightAccount(t, store, unavailablePool.ID, "global-unavailable-"+string(rune('a'+index)))
		if _, err := store.db.ExecContext(ctx, `UPDATE claude_code_accounts SET health_status = ? WHERE id = ?`, AccountHealthManualRecovery, account.ID); err != nil {
			t.Fatalf("mark account unavailable: %v", err)
		}
	}

	snapshot, err := store.AccountPoolInsights(ctx, now)
	if err != nil {
		t.Fatalf("AccountPoolInsights() error = %v", err)
	}
	readiness := snapshot.Global.Health.Components["account_readiness"]
	if readiness.Score == nil || *readiness.Score != 25 || readiness.SampleCount != 4 {
		t.Fatalf("global readiness = %+v, want 1/4 accounts", readiness)
	}
	if snapshot.Global.Health.Score == nil || *snapshot.Global.Health.Score != 40 || snapshot.Global.Health.Status != "critical" {
		t.Fatalf("global health = %+v, want account-weighted score 40", snapshot.Global.Health)
	}
	if snapshot.Distribution.Healthy != 1 || snapshot.Distribution.Unavailable != 1 {
		t.Fatalf("health distribution = %+v", snapshot.Distribution)
	}
}

func registerInsightAccount(t *testing.T, store *Store, poolID, authID string) *ClaudeCodeAccount {
	t.Helper()
	ctx := context.Background()
	account, err := store.RegisterClaudeCodeAccountInPool(ctx, poolID, authID, authID+"@example.com", "")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountInPool(%s) error = %v", authID, err)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE claude_code_accounts
SET auth_json = '{}'
WHERE id = ?
	`, account.ID); err != nil {
		t.Fatalf("prepare insight account %s: %v", authID, err)
	}
	now := time.Now()
	if _, err := store.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{
		Source:              "test",
		Status:              AccountHealthHealthy,
		CheckedAt:           &now,
		AllowManualRecovery: true,
	}); err != nil {
		t.Fatalf("mark insight account %s healthy: %v", authID, err)
	}
	account, err = store.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount(%s) error = %v", authID, err)
	}
	return account
}
