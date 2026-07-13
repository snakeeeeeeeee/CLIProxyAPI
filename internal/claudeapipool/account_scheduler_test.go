package claudeapipool

import (
	"context"
	"testing"
	"time"
)

func accountSchedulerPolicy() EffectiveRoutingConfig {
	return EffectiveRouting(RoutingConfig{
		PerAccountRPM:            10,
		PerAccountConcurrency:    1,
		StickyConcurrencyReserve: 1,
		MaxSessions:              30,
		StickyWaitMS:             50,
		FallbackWaitMS:           50,
		MaxWaitersPerAccount:     20,
		MaxWaitersGlobal:         200,
		SessionAffinityTTLMS:     int(time.Hour / time.Millisecond),
		ActiveSessionIdleTTLMS:   int((5 * time.Minute) / time.Millisecond),
	})
}

func TestAccountSchedulerReleasesActiveCapacityBeforeAffinity(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.MaxSessions = 1
	policy.ActiveSessionIdleTTLMS = 20
	policy.StickyWaitMS = 1
	SetScopedRoutingConfig(scope, policy)
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}

	first, firstSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-1"}, candidates, nil)
	if first == nil {
		t.Fatalf("first selection = %#v", firstSelection)
	}
	first.Release()
	time.Sleep(30 * time.Millisecond)

	second, secondSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-2"}, candidates, nil)
	if second == nil {
		t.Fatalf("expired active capacity was not released: %#v", secondSelection)
	}
	second.Release()
	time.Sleep(30 * time.Millisecond)

	returned, returnedSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "opus", SessionKey: "session-1"}, candidates, nil)
	if returned == nil {
		t.Fatalf("returning session selection = %#v", returnedSelection)
	}
	defer returned.Release()
	if !returnedSelection.PrimaryHit || returnedSelection.AuthID != "auth-a" {
		t.Fatalf("affinity expired with active capacity: %#v", returnedSelection)
	}
}

func TestAccountSchedulerUsesHeadroomWithinPressureBand(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	UpdateScopedAccountHeadroom(scope, "auth-a", map[string]float64{"sonnet": 0.2}, time.Now().Add(time.Minute))
	UpdateScopedAccountHeadroom(scope, "auth-b", map[string]float64{"sonnet": 0.8}, time.Now().Add(time.Minute))

	lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-sonnet"}, []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}, nil)
	if lease == nil {
		t.Fatalf("selection = %#v", selection)
	}
	defer lease.Release()
	if selection.AuthID != "auth-b" {
		t.Fatalf("selected %q, want higher-headroom auth-b", selection.AuthID)
	}
}

func TestAccountSchedulerPrefersHigherModelHeadroomAtEqualPressure(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	expiresAt := time.Now().Add(time.Minute)
	UpdateScopedAccountQuotaRouting(scope, "auth-a", map[string]AccountQuotaRoutingState{
		"sonnet": {Headroom: 0.14, UsedPercent: 86, Band: QuotaBandDegraded, ExpiresAt: expiresAt},
	})
	UpdateScopedAccountQuotaRouting(scope, "auth-b", map[string]AccountQuotaRoutingState{
		"sonnet": {Headroom: 0.7, UsedPercent: 30, Band: QuotaBandNormal, ExpiresAt: expiresAt},
	})
	descriptor := AccountRouteDescriptor{Model: "claude-sonnet", SessionKey: "new-session"}

	lease, selection := AcquireScopedAccountRoute(context.Background(), scope, descriptor, []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}, nil)
	if lease == nil {
		t.Fatalf("selection = %#v", selection)
	}
	defer lease.Release()
	if selection.AuthID != "auth-b" || selection.QuotaHeadroom != 0.7 {
		t.Fatalf("selected lower-headroom account: %#v", selection)
	}
}

func TestAccountSchedulerLegacyDrainOnlyRemainsSchedulable(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.StickyWaitMS = 1
	SetScopedRoutingConfig(scope, policy)
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}
	descriptor := AccountRouteDescriptor{Model: "claude-fable-5", SessionKey: "sticky-session"}

	first, selection := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if first == nil {
		t.Fatalf("initial selection = %#v", selection)
	}
	first.Release()
	resetAt := time.Now().Add(24 * time.Hour)
	UpdateScopedAccountQuotaRouting(scope, "auth-a", map[string]AccountQuotaRoutingState{
		"fable": {Headroom: 0.04, UsedPercent: 96, Band: QuotaBandDrainOnly, Window: "seven_day_fable", ResetAt: resetAt, ExpiresAt: time.Now().Add(time.Minute)},
	})

	sticky, stickySelection := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if sticky == nil || !stickySelection.PrimaryHit {
		t.Fatalf("sticky drain selection = %#v", stickySelection)
	}
	sticky.Release()
	newLease, newSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-fable-5", SessionKey: "new-session"}, candidates, nil)
	if newLease == nil || newSelection.QuotaBand != QuotaBandDegraded {
		t.Fatalf("legacy drain-only blocked a new session: lease=%v selection=%#v", newLease, newSelection)
	}
	newLease.Release()
}

func TestAccountSchedulerFableExhaustionDoesNotBlockSonnet(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	resetAt := time.Now().Add(24 * time.Hour)
	UpdateScopedAccountQuotaRouting(scope, "auth-a", map[string]AccountQuotaRoutingState{
		"":       {Headroom: 0.5, UsedPercent: 50, Band: QuotaBandNormal, Window: "seven_day", ResetAt: resetAt, ExpiresAt: time.Now().Add(time.Minute)},
		"fable":  {Headroom: 0, UsedPercent: 100, Band: QuotaBandExhausted, Window: "seven_day_fable", ResetAt: resetAt, ExpiresAt: time.Now().Add(time.Minute)},
		"sonnet": {Headroom: 0.5, UsedPercent: 50, Band: QuotaBandNormal, Window: "seven_day", ResetAt: resetAt, ExpiresAt: time.Now().Add(time.Minute)},
	})
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}

	fable, fableSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-fable-5", SessionKey: "fable-session"}, candidates, nil)
	if fable != nil || fableSelection.RejectReason != "quota_exhausted" {
		t.Fatalf("fable exhaustion selection = lease:%v selection:%#v", fable, fableSelection)
	}
	sonnet, sonnetSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-sonnet", SessionKey: "sonnet-session"}, candidates, nil)
	if sonnet == nil {
		t.Fatalf("sonnet was blocked by fable quota: %#v", sonnetSelection)
	}
	sonnet.Release()
}

func TestAccountSchedulerRecoversImmediatelyBelowExhaustion(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	resetAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	expiresAt := time.Now().Add(time.Minute)
	UpdateScopedAccountQuotaRouting(scope, "auth-a", map[string]AccountQuotaRoutingState{
		"fable": {Headroom: 0, UsedPercent: 100, Band: QuotaBandExhausted, Window: "seven_day_fable", ResetAt: resetAt, ExpiresAt: expiresAt},
	})
	UpdateScopedAccountQuotaRouting(scope, "auth-a", map[string]AccountQuotaRoutingState{
		"fable": {Headroom: 0.02, UsedPercent: 98, Band: QuotaBandDegraded, Window: "seven_day_fable", ResetAt: resetAt, ExpiresAt: expiresAt},
	})
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}
	recovered, recoveredSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-fable-5", SessionKey: "new-session"}, candidates, nil)
	if recovered == nil {
		t.Fatalf("quota did not recover below true exhaustion: %#v", recoveredSelection)
	}
	recovered.Release()
}

func TestAccountSchedulerIncludesActiveSessionsInPressure(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.PerAccountRPM = 100
	policy.MaxSessions = 4
	SetScopedRoutingConfig(scope, policy)
	for _, session := range []string{"session-a", "session-b"} {
		lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-opus", SessionKey: session}, []AccountRouteCandidate{{AuthID: "auth-a"}}, nil)
		if lease == nil {
			t.Fatalf("seed active session %q: %#v", session, selection)
		}
		lease.Release()
	}

	lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "claude-opus", SessionKey: "new-session"}, []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}, nil)
	if lease == nil {
		t.Fatalf("selection = %#v", selection)
	}
	defer lease.Release()
	if selection.AuthID != "auth-b" {
		t.Fatalf("selected account with more active-session pressure: %#v", selection)
	}
}

func TestAccountSchedulerKeepsExplicitSessionAcrossModels(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}

	first, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-1"}, candidates, nil)
	if first == nil || selection.AuthID == "" {
		t.Fatalf("first selection = %#v", selection)
	}
	first.Promote()
	first.Release()

	second, next := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "opus", SessionKey: "session-1"}, candidates, nil)
	if second == nil {
		t.Fatalf("second selection = %#v", next)
	}
	defer second.Release()
	if next.AuthID != selection.AuthID || !next.PrimaryHit {
		t.Fatalf("cross-model selection = %#v, want primary %q", next, selection.AuthID)
	}
}

func TestAccountSchedulerKeepsSameSessionIndependentAcrossPools(t *testing.T) {
	poolA := t.Name() + "/pool-a"
	poolB := t.Name() + "/pool-b"
	SetScopedRoutingConfig(poolA, accountSchedulerPolicy())
	SetScopedRoutingConfig(poolB, accountSchedulerPolicy())
	descriptor := AccountRouteDescriptor{Model: "claude-sonnet", SessionKey: "shared-session"}

	leaseA, selectionA := AcquireScopedAccountRoute(context.Background(), poolA, descriptor, []AccountRouteCandidate{{AuthID: "auth-a"}}, nil)
	if leaseA == nil {
		t.Fatalf("pool A selection = %#v", selectionA)
	}
	leaseA.Promote()
	leaseA.Release()
	leaseB, selectionB := AcquireScopedAccountRoute(context.Background(), poolB, descriptor, []AccountRouteCandidate{{AuthID: "auth-b"}}, nil)
	if leaseB == nil {
		t.Fatalf("pool B selection = %#v", selectionB)
	}
	leaseB.Promote()
	leaseB.Release()

	returnA, nextA := AcquireScopedAccountRoute(context.Background(), poolA, descriptor, []AccountRouteCandidate{{AuthID: "auth-a"}}, nil)
	if returnA == nil || nextA.AuthID != "auth-a" || !nextA.PrimaryHit {
		t.Fatalf("pool A binding leaked or disappeared: %#v", nextA)
	}
	returnA.Release()
	returnB, nextB := AcquireScopedAccountRoute(context.Background(), poolB, descriptor, []AccountRouteCandidate{{AuthID: "auth-b"}}, nil)
	if returnB == nil || nextB.AuthID != "auth-b" || !nextB.PrimaryHit {
		t.Fatalf("pool B binding leaked or disappeared: %#v", nextB)
	}
	returnB.Release()
}

func TestAccountSchedulerDoesNotBindRequestsWithoutSession(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}

	first, firstSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, candidates, nil)
	if first == nil {
		t.Fatalf("first selection = %#v", firstSelection)
	}
	first.Release()
	second, secondSelection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, candidates, nil)
	if second == nil {
		t.Fatalf("second selection = %#v", secondSelection)
	}
	defer second.Release()
	if firstSelection.SessionHash != "" || secondSelection.SessionHash != "" {
		t.Fatalf("requests without session unexpectedly bound: %#v %#v", firstSelection, secondSelection)
	}
	if firstSelection.AuthID == secondSelection.AuthID {
		t.Fatalf("load scheduler did not rotate after release: %#v %#v", firstSelection, secondSelection)
	}
}

func TestAccountSchedulerPromotesSuccessfulBackup(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}, {AuthID: "auth-b"}}
	descriptor := AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-1", PrefixFingerprint: "prefix-1"}

	primary, first := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if primary == nil {
		t.Fatalf("primary selection = %#v", first)
	}
	primary.Release()

	backup, second := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, map[string]struct{}{first.AuthID: {}})
	if backup == nil || !second.BackupLane || second.AuthID == first.AuthID {
		t.Fatalf("backup selection = %#v", second)
	}
	backup.Promote()
	backup.Release()

	nextLease, next := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if nextLease == nil {
		t.Fatalf("promoted selection = %#v", next)
	}
	defer nextLease.Release()
	if next.AuthID != second.AuthID || !next.PrimaryHit {
		t.Fatalf("promoted primary = %#v, want %q", next, second.AuthID)
	}
}

func TestAccountSchedulerEnforcesMaxSessionsForNewBindings(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.MaxSessions = 1
	policy.StickyWaitMS = 1
	SetScopedRoutingConfig(scope, policy)
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}

	first, _ := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-1"}, candidates, nil)
	if first == nil {
		t.Fatal("first session should acquire")
	}
	first.Release()
	second, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-2"}, candidates, nil)
	if second != nil || selection.RejectReason != "session_full" {
		t.Fatalf("second session = lease:%v selection:%#v", second, selection)
	}
}

func TestAccountSchedulerStickyReserveDoesNotExpandRPM(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.PerAccountRPM = 2
	SetScopedRoutingConfig(scope, policy)
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}
	descriptor := AccountRouteDescriptor{Model: "sonnet", SessionKey: "session-1"}

	first, _ := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if first == nil {
		t.Fatal("first sticky request should acquire")
	}
	defer first.Release()
	second, secondSelection := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if second == nil || secondSelection.ConcurrencyLimit != 2 {
		t.Fatalf("sticky reserve selection = %#v", secondSelection)
	}
	defer second.Release()
	third, thirdSelection := AcquireScopedAccountRoute(context.Background(), scope, descriptor, candidates, nil)
	if third != nil || thirdSelection.RejectReason != "rpm_full" || thirdSelection.RPMLimit != 2 {
		t.Fatalf("third selection = lease:%v selection:%#v", third, thirdSelection)
	}
}

func TestAccountSchedulerWaitsForReleaseSignal(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.FallbackWaitMS = 500
	SetScopedRoutingConfig(scope, policy)
	candidates := []AccountRouteCandidate{{AuthID: "auth-a"}}
	first, _ := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, candidates, nil)
	if first == nil {
		t.Fatal("first request should acquire")
	}

	type result struct {
		lease     *RouteLease
		selection AccountRouteSelection
	}
	resultCh := make(chan result, 1)
	go func() {
		lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, candidates, nil)
		resultCh <- result{lease: lease, selection: selection}
	}()
	time.Sleep(25 * time.Millisecond)
	first.Release()
	got := <-resultCh
	if got.lease == nil || got.selection.Waited < 20*time.Millisecond {
		t.Fatalf("waited selection = %#v", got.selection)
	}
	got.lease.Release()
}

func TestRouteLeaseCountsEveryAttemptAgainstRPM(t *testing.T) {
	scope := t.Name()
	policy := accountSchedulerPolicy()
	policy.PerAccountRPM = 3
	SetScopedRoutingConfig(scope, policy)
	lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, []AccountRouteCandidate{{AuthID: "auth-a"}}, nil)
	if lease == nil {
		t.Fatalf("selection = %#v", selection)
	}
	defer lease.Release()
	if !lease.TryStartAttempt() || !lease.TryStartAttempt() {
		t.Fatal("second and third attempts should fit RPM")
	}
	if lease.TryStartAttempt() {
		t.Fatal("fourth attempt should exceed RPM")
	}
}

func TestAccountSchedulerSkipsTemporarilyBlockedProxy(t *testing.T) {
	scope := t.Name()
	SetScopedRoutingConfig(scope, accountSchedulerPolicy())
	BlockScopedProxy(scope, "proxy-a", time.Minute)
	lease, selection := AcquireScopedAccountRoute(context.Background(), scope, AccountRouteDescriptor{Model: "sonnet"}, []AccountRouteCandidate{
		{AuthID: "auth-a", ProxyID: "proxy-a"},
		{AuthID: "auth-b", ProxyID: "proxy-b"},
	}, nil)
	if lease == nil {
		t.Fatalf("selection = %#v", selection)
	}
	defer lease.Release()
	if selection.AuthID != "auth-b" {
		t.Fatalf("selected blocked proxy account: %#v", selection)
	}
}
