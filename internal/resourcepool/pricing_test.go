package resourcepool

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestBuiltinModelPricingAndImmutableRevision(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	current, err := store.CurrentModelPriceVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentModelPriceVersion() error = %v", err)
	}
	if current.Revision != 1 || current.ID <= 0 || len(current.Prices) == 0 {
		t.Fatalf("current price version = %+v", current)
	}
	sonnet, err := store.ResolveModelPrice(ctx, current.ID, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ResolveModelPrice(sonnet) error = %v", err)
	}
	if sonnet == nil || sonnet.InputPerMillion != 3 || sonnet.OutputPerMillion != 15 {
		t.Fatalf("sonnet price = %+v", sonnet)
	}
	fable, err := store.ResolveModelPrice(ctx, current.ID, "claude-fable-5")
	if err != nil {
		t.Fatalf("ResolveModelPrice(fable) error = %v", err)
	}
	if fable != nil {
		t.Fatalf("Fable must remain unpriced until configured: %+v", fable)
	}

	next, err := store.CreateModelPriceVersion(ctx, []ModelPriceUpdate{{
		ModelPattern:           "claude-sonnet-4-6",
		InputPerMillion:        4,
		OutputPerMillion:       20,
		CacheWrite5mPerMillion: 5,
		CacheWrite1hPerMillion: 8,
		CacheReadPerMillion:    0.4,
	}}, "test revision")
	if err != nil {
		t.Fatalf("CreateModelPriceVersion() error = %v", err)
	}
	if next.Revision != 2 || next.ID == current.ID {
		t.Fatalf("next price version = %+v", next)
	}
	oldSonnet, err := store.ResolveModelPrice(ctx, current.ID, "claude-sonnet-4-6")
	if err != nil || oldSonnet == nil || oldSonnet.InputPerMillion != 3 {
		t.Fatalf("old immutable price = %+v, err=%v", oldSonnet, err)
	}
	newSonnet, err := store.ResolveModelPrice(ctx, next.ID, "claude-sonnet-4-6")
	if err != nil || newSonnet == nil || newSonnet.InputPerMillion != 4 {
		t.Fatalf("new exact price = %+v, err=%v", newSonnet, err)
	}
}

func TestApplyUsagePricingUsesCacheDurations(t *testing.T) {
	store := openTestStore(t)
	entry := UsageLedgerEntry{
		Model:               "claude-sonnet-4-6",
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheReadTokens:     1_000_000,
		CacheCreationTokens: 2_000_000,
		CacheCreation5m:     1_000_000,
		CacheCreation1h:     1_000_000,
	}
	if err := store.ApplyUsagePricing(context.Background(), &entry); err != nil {
		t.Fatalf("ApplyUsagePricing() error = %v", err)
	}
	if entry.PricingStatus != "priced" || entry.PriceVersionID <= 0 {
		t.Fatalf("priced entry = %+v", entry)
	}
	if math.Abs(entry.EstimatedCost-28.05) > 0.000001 {
		t.Fatalf("cost = %.8f, want 28.05", entry.EstimatedCost)
	}

	estimated := UsageLedgerEntry{Model: "claude-sonnet-4-6", CacheCreationTokens: 1_000_000}
	if err := store.ApplyUsagePricing(context.Background(), &estimated); err != nil {
		t.Fatalf("ApplyUsagePricing(estimated) error = %v", err)
	}
	if estimated.PricingStatus != "estimated" || estimated.CacheCreation5m != 1_000_000 || estimated.EstimatedCost != 3.75 {
		t.Fatalf("estimated cache price = %+v", estimated)
	}
}

func TestPricingMigrationBackfillsKnownAndUnknownUsage(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := dbTime(time.Now())
	for _, model := range []string{"claude-opus-4-8", "claude-fable-5"} {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO claude_code_usage_ledger(model, input_tokens, output_tokens, cache_creation_tokens, created_at)
VALUES(?, 1000000, 1000000, 1000000, ?)
		`, model, now); err != nil {
			t.Fatalf("insert legacy usage %s: %v", model, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = ?`, modelPricingMigrationMarker); err != nil {
		t.Fatalf("delete migration marker: %v", err)
	}
	if err := store.migrateModelPricingV7(ctx); err != nil {
		t.Fatalf("migrateModelPricingV7() error = %v", err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT model, pricing_status, estimated_cost FROM claude_code_usage_ledger ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("query backfilled usage: %v", err)
	}
	defer func() { _ = rows.Close() }()
	statuses := map[string]string{}
	costs := map[string]float64{}
	for rows.Next() {
		var model, status string
		var cost float64
		if err := rows.Scan(&model, &status, &cost); err != nil {
			t.Fatalf("scan backfilled usage: %v", err)
		}
		statuses[model] = status
		costs[model] = cost
	}
	if statuses["claude-opus-4-8"] != "estimated" || costs["claude-opus-4-8"] != 36.25 {
		t.Fatalf("Opus backfill status=%q cost=%v", statuses["claude-opus-4-8"], costs["claude-opus-4-8"])
	}
	if statuses["claude-fable-5"] != "unpriced" || costs["claude-fable-5"] != 0 {
		t.Fatalf("Fable backfill status=%q cost=%v", statuses["claude-fable-5"], costs["claude-fable-5"])
	}
}

func TestUsageSummarySeparatesRequestsAttemptsAndPools(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	pool, err := store.CreateAccountPool(ctx, "isolated", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	entries := []UsageLedgerEntry{
		{PoolID: DefaultAccountPoolID, RequestID: "request-a", Model: "claude-sonnet-4-6", InputTokens: 10, Success: false},
		{PoolID: DefaultAccountPoolID, RequestID: "request-a", Model: "claude-sonnet-4-6", InputTokens: 20, Success: true},
		{PoolID: pool.ID, RequestID: "request-b", Model: "claude-fable-5", InputTokens: 30, Success: false},
	}
	for _, entry := range entries {
		if err := store.RecordUsageLedger(ctx, entry); err != nil {
			t.Fatalf("RecordUsageLedger() error = %v", err)
		}
	}
	global, err := store.UsageSummaryQuery(ctx, UsageQuery{Window: time.Hour})
	if err != nil {
		t.Fatalf("UsageSummaryQuery(global) error = %v", err)
	}
	if global.RequestCount != 2 || global.AttemptCount != 3 || global.SuccessCount != 1 || global.FailureCount != 1 || global.UnpricedRequestCount != 1 {
		t.Fatalf("global usage = %+v", global)
	}
	if global.InputTokens != 60 || global.PricingCoverage != 50 {
		t.Fatalf("global totals = %+v", global)
	}
	defaultOnly, err := store.UsageSummaryQuery(ctx, UsageQuery{Window: time.Hour, PoolID: DefaultAccountPoolID})
	if err != nil {
		t.Fatalf("UsageSummaryQuery(default) error = %v", err)
	}
	if defaultOnly.RequestCount != 1 || defaultOnly.AttemptCount != 2 || defaultOnly.SuccessCount != 1 || defaultOnly.InputTokens != 30 || defaultOnly.PricingCoverage != 100 {
		t.Fatalf("default usage = %+v", defaultOnly)
	}
}
