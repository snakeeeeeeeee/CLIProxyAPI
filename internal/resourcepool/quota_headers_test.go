package resourcepool

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestParseUnifiedRateLimitWindows(t *testing.T) {
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.41")
	headers.Set("anthropic-ratelimit-unified-5h-reset", reset.Format(time.RFC3339))
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "0.87")
	headers.Set("anthropic-ratelimit-unified-overage-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-representative-claim", "seven_day_overage_included")
	headers.Set("anthropic-ratelimit-unified-reset", strconv.FormatInt(reset.Add(time.Hour).UnixMilli(), 10))

	windows := parseUnifiedRateLimitWindows(headers, time.Now())
	byKey := make(map[string]QuotaWindow, len(windows))
	for _, window := range windows {
		byKey[window.Key] = window
	}
	fiveHour := byKey["five_hour"]
	if fiveHour.UsedPercent != 41 || fiveHour.Status != "allowed" || fiveHour.ResetsAt == nil || !fiveHour.ResetsAt.Equal(reset) {
		t.Fatalf("five hour window = %#v", fiveHour)
	}
	if fiveHour.UtilizationKnown == nil || !*fiveHour.UtilizationKnown {
		t.Fatalf("five hour utilization confidence = %#v, want known", fiveHour.UtilizationKnown)
	}
	model := byKey["seven_day_fable"]
	if model.UsedPercent != 87 || model.RepresentativeClaim != "seven_day_overage_included" || model.Source != "response_headers" || model.ResetsAt == nil {
		t.Fatalf("dynamic model window = %#v", model)
	}
	overage := byKey["extra_usage"]
	if overage.Status != "rejected" || overage.Name != "额外用量" {
		t.Fatalf("overage window = %#v", overage)
	}
	overall := evaluateQuotaRouting(windows, timePtr(time.Now()), "*", time.Now())
	if overall.Band == claudeapipool.QuotaBandExhausted {
		t.Fatalf("overage eligibility must not exhaust model quota: %#v", overall)
	}
}

func TestParseUnifiedRateLimitWindowsWithoutUtilizationIsObservedOnly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", reset.Format(time.RFC3339))

	windows := parseUnifiedRateLimitWindows(headers, now)
	if len(windows) != 1 {
		t.Fatalf("windows = %#v, want one observed window", windows)
	}
	window := windows[0]
	if window.UtilizationKnown == nil || *window.UtilizationKnown {
		t.Fatalf("utilization confidence = %#v, want explicitly unknown", window.UtilizationKnown)
	}
	if window.Status != "allowed" || window.ResetsAt == nil || !window.ResetsAt.Equal(reset) {
		t.Fatalf("observed metadata = %#v", window)
	}
	evaluation := evaluateQuotaRouting(windows, &now, "claude-sonnet", now)
	if evaluation.Known || evaluation.Headroom != 0.5 || evaluation.Band != claudeapipool.QuotaBandUnknown {
		t.Fatalf("observed-only routing evaluation = %#v, want neutral", evaluation)
	}
}

func TestEvaluateQuotaRoutingUnknownExhaustionStillBlocks(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	unknown := false
	zero := 0.0
	for name, window := range map[string]QuotaWindow{
		"rejected": {
			Key:              "five_hour",
			UtilizationKnown: &unknown,
			Status:           "rejected",
			ResetsAt:         &reset,
			UpdatedAt:        &now,
		},
		"zero remaining": {
			Key:              "seven_day",
			UtilizationKnown: &unknown,
			Status:           "allowed",
			Remaining:        &zero,
			ResetsAt:         &reset,
			UpdatedAt:        &now,
		},
	} {
		t.Run(name, func(t *testing.T) {
			evaluation := evaluateQuotaRouting([]QuotaWindow{window}, &now, "claude-opus", now)
			if !evaluation.Known || evaluation.Band != claudeapipool.QuotaBandExhausted || evaluation.Headroom != 0 {
				t.Fatalf("routing evaluation = %#v, want exhausted", evaluation)
			}
		})
	}
}

func TestNormalizeQuotaWindowsInfersLegacyUtilizationConfidence(t *testing.T) {
	windows := normalizeQuotaWindows([]QuotaWindow{
		{Key: "five_hour", Source: "oauth_usage", UsedPercent: 0, RemainPercent: 100},
		{Key: "seven_day", Source: "response_headers", UsedPercent: 35, RemainPercent: 65},
		{Key: "seven_day_opus", Source: "response_headers", Status: "allowed", RemainPercent: 100},
	})
	byKey := make(map[string]QuotaWindow, len(windows))
	for _, window := range windows {
		byKey[window.Key] = window
	}
	if !quotaWindowUtilizationKnown(byKey["five_hour"]) {
		t.Fatal("legacy OAuth utilization should remain known")
	}
	if !quotaWindowUtilizationKnown(byKey["seven_day"]) {
		t.Fatal("legacy nonzero header utilization should remain known")
	}
	if quotaWindowUtilizationKnown(byKey["seven_day_opus"]) {
		t.Fatal("legacy status-only header utilization should remain unknown")
	}
}

func TestMergeQuotaWindowObservedStatusPreservesKnownUtilization(t *testing.T) {
	observedAt := time.Now().UTC().Truncate(time.Second)
	knownAt := observedAt.Add(-time.Minute)
	known := true
	unknown := false
	current := QuotaWindow{
		Key:              "five_hour",
		UsedPercent:      35,
		RemainPercent:    65,
		UtilizationKnown: &known,
		Source:           "oauth_usage",
		UpdatedAt:        &knownAt,
	}
	incoming := QuotaWindow{
		Key:              "five_hour",
		UtilizationKnown: &unknown,
		Status:           "allowed",
		Source:           "response_headers",
		UpdatedAt:        &observedAt,
	}

	merged := mergeQuotaWindow(current, incoming)
	if !quotaWindowUtilizationKnown(merged) || merged.UsedPercent != 35 || merged.RemainPercent != 65 {
		t.Fatalf("merged utilization = %#v, want preserved known percentage", merged)
	}
	if merged.UpdatedAt == nil || !merged.UpdatedAt.Equal(knownAt) || merged.Source != "oauth_usage" {
		t.Fatalf("merged utilization provenance = %#v, want original observation", merged)
	}
	if merged.Status != "allowed" {
		t.Fatalf("merged status = %q, want allowed", merged.Status)
	}
}

func TestMergeQuotaWindowUnknownExhaustionOverridesKnownUtilization(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	known := true
	unknown := false
	current := QuotaWindow{Key: "five_hour", UsedPercent: 35, RemainPercent: 65, UtilizationKnown: &known, Source: "oauth_usage", UpdatedAt: &now}
	incoming := QuotaWindow{Key: "five_hour", UtilizationKnown: &unknown, Status: "rejected", Source: "response_headers", UpdatedAt: &now}

	merged := mergeQuotaWindow(current, incoming)
	if quotaWindowUtilizationKnown(merged) || merged.Status != "rejected" {
		t.Fatalf("merged exhaustion = %#v, want unknown rejected observation", merged)
	}
	evaluation := evaluateQuotaRouting([]QuotaWindow{merged}, &now, "claude-sonnet", now)
	if !evaluation.Known || evaluation.Band != claudeapipool.QuotaBandExhausted {
		t.Fatalf("merged exhaustion routing = %#v", evaluation)
	}
}

func TestQuotaHeadroomUsesBaseAndCurrentModel(t *testing.T) {
	windows := []QuotaWindow{
		{Key: "five_hour", UsedPercent: 20},
		{Key: "seven_day", UsedPercent: 40},
		{Key: "seven_day_sonnet", UsedPercent: 75},
		{Key: "seven_day_opus", UsedPercent: 60},
		{Key: "seven_day_fable", UsedPercent: 88},
	}
	if got := quotaHeadroom(windows, "claude-sonnet"); got != 0.25 {
		t.Fatalf("sonnet headroom = %v, want 0.25", got)
	}
	if got := quotaHeadroom(windows, "claude-opus"); got != 0.4 {
		t.Fatalf("opus headroom = %v, want 0.4", got)
	}
	if got := quotaHeadroom(windows, "claude-fable-5"); got != 0.12 {
		t.Fatalf("fable headroom = %v, want 0.12", got)
	}
}

func TestEvaluateQuotaRoutingUsesSharedAndModelBands(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(24 * time.Hour)
	windows := []QuotaWindow{
		{Key: "five_hour", UsedPercent: 40, ResetsAt: &reset, UpdatedAt: &now},
		{Key: "seven_day_fable", UsedPercent: 96, ResetsAt: &reset, UpdatedAt: &now},
	}
	shared := evaluateQuotaRouting(windows, &now, "", now)
	if !shared.Known || shared.Band != "normal" || shared.Headroom != 0.6 {
		t.Fatalf("shared evaluation = %#v", shared)
	}
	fable := evaluateQuotaRouting(windows, &now, "claude-fable-5", now)
	if !fable.Known || fable.Band != "degraded" || fable.Headroom != 0.04 || fable.Window != "seven_day_fable" {
		t.Fatalf("fable evaluation = %#v", fable)
	}
	sonnet := evaluateQuotaRouting(windows, &now, "claude-sonnet", now)
	if !sonnet.Known || sonnet.Band != "normal" || sonnet.Headroom != 0.6 {
		t.Fatalf("sonnet evaluation = %#v", sonnet)
	}
}

func TestMergeRateLimitHeadersBlocksOnlyFableFamily(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{ID: "fable-header-auth", Provider: "claude", Metadata: map[string]any{"access_token": "token", "refresh_token": "refresh"}, Attributes: map[string]string{AttrClaudeOAuthPool: "true"}}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "fable@example.com", "", auth)
	if err != nil {
		t.Fatalf("register account: %v", err)
	}
	routingScope := AccountRoutingScope(DefaultAccountPoolID)
	claudeapipool.ClearScopedAccountBlock(routingScope, auth.ID)
	t.Cleanup(func() {
		claudeapipool.ClearScopedAccountQuotaRouting(routingScope, auth.ID)
	})
	reset := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.40")
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "0.50")
	headers.Set("anthropic-ratelimit-unified-7d-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(reset.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(reset.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-representative-claim", "seven_day_overage_included")
	if _, err := store.MergeAccountRateLimitHeaders(ctx, account.ID, headers); err != nil {
		t.Fatalf("merge headers: %v", err)
	}

	candidates := []claudeapipool.AccountRouteCandidate{{AuthID: auth.ID}}
	fable, fableSelection := claudeapipool.AcquireScopedAccountRoute(ctx, routingScope, claudeapipool.AccountRouteDescriptor{Model: "claude-fable-5", SessionKey: "fable-session"}, candidates, nil)
	if fable != nil || fableSelection.RejectReason != "quota_exhausted" {
		t.Fatalf("fable route = lease:%v selection:%#v", fable, fableSelection)
	}
	sonnet, sonnetSelection := claudeapipool.AcquireScopedAccountRoute(ctx, routingScope, claudeapipool.AccountRouteDescriptor{Model: "claude-sonnet", SessionKey: "sonnet-session"}, candidates, nil)
	if sonnet == nil {
		t.Fatalf("sonnet was blocked by fable quota: %#v", sonnetSelection)
	}
	sonnet.Release()
}

func TestMergeRateLimitHeadersSharedExhaustionBlocksEveryModel(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	auth := &coreauth.Auth{ID: "shared-header-auth", Provider: "claude", Metadata: map[string]any{"access_token": "token", "refresh_token": "refresh"}, Attributes: map[string]string{AttrClaudeOAuthPool: "true"}}
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "shared@example.com", "", auth)
	if err != nil {
		t.Fatalf("register account: %v", err)
	}
	routingScope := AccountRoutingScope(DefaultAccountPoolID)
	claudeapipool.ClearScopedAccountBlock(routingScope, auth.ID)
	t.Cleanup(func() {
		claudeapipool.ClearScopedAccountQuotaRouting(routingScope, auth.ID)
	})
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset.Unix(), 10))
	if _, err := store.MergeAccountRateLimitHeaders(ctx, account.ID, headers); err != nil {
		t.Fatalf("merge headers: %v", err)
	}

	for _, model := range []string{"claude-sonnet", "claude-opus", "claude-fable-5"} {
		lease, selection := claudeapipool.AcquireScopedAccountRoute(ctx, routingScope, claudeapipool.AccountRouteDescriptor{Model: model, SessionKey: model}, []claudeapipool.AccountRouteCandidate{{AuthID: auth.ID}}, nil)
		if lease != nil || selection.RejectReason != "quota_exhausted" {
			t.Fatalf("model %s route = lease:%v selection:%#v", model, lease, selection)
		}
	}
}
