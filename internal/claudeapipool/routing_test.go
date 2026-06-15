package claudeapipool

import (
	"testing"
	"time"
)

func TestPoolRouterLimitsConcurrencyAndReleases(t *testing.T) {
	router := newPoolRouter()
	policy := EffectiveRouting(RoutingConfig{PerAccountConcurrency: 1})
	lease, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, time.Now())
	if !ok || lease == nil {
		t.Fatal("first acquire should succeed")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, time.Now()); ok {
		t.Fatal("second acquire should be blocked by concurrency")
	}
	status := router.status("auth-1", "claude-opus", policy, time.Now())
	if !status.Unavailable || status.InFlight != 1 {
		t.Fatalf("status while full = %#v", status)
	}
	lease.Release()
	if _, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, time.Now()); !ok {
		t.Fatal("acquire after release should succeed")
	}
}

func TestPoolRouterLimitsRPM(t *testing.T) {
	router := newPoolRouter()
	policy := EffectiveRouting(RoutingConfig{PerAccountRPM: 1})
	now := time.Now()
	lease, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now)
	if !ok || lease == nil {
		t.Fatal("first acquire should succeed")
	}
	lease.Release()
	if _, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now.Add(time.Second)); ok {
		t.Fatal("second acquire should be blocked by rpm")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now.Add(routeWindow+time.Second)); !ok {
		t.Fatal("acquire after rpm window should succeed")
	}
}

func TestPoolRouterCoolsDown529PerAuthModel(t *testing.T) {
	router := newPoolRouter()
	policy := EffectiveRouting(RoutingConfig{OverloadCooldownMS: 2500, OverloadMaxCooldownMS: 2500})
	now := time.Now()
	router.noteResult("auth-1", "claude-opus", StatusOverloaded, nil, policy, now)
	status := router.status("auth-1", "claude-opus", policy, now)
	if !status.Cooling || status.CoolingTo.Sub(now) != 2500*time.Millisecond {
		t.Fatalf("529 status = %#v", status)
	}
	if _, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now.Add(time.Second)); ok {
		t.Fatal("same auth/model should be blocked during 529 cooldown")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, false, now.Add(time.Second)); !ok {
		t.Fatal("different model should not be blocked")
	}
	if _, ok := router.tryAcquire("auth-2", "claude-opus", policy, false, now.Add(time.Second)); !ok {
		t.Fatal("different auth should not be blocked")
	}
}

func TestPoolRouterLimitsRPMAndConcurrencyPerAccount(t *testing.T) {
	router := newPoolRouter()
	policy := EffectiveRouting(RoutingConfig{PerAccountRPM: 1, PerAccountConcurrency: 1})
	now := time.Now()
	lease, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now)
	if !ok || lease == nil {
		t.Fatal("first acquire should succeed")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, false, now.Add(time.Second)); ok {
		t.Fatal("different model should still share account concurrency")
	}
	lease.Release()
	if _, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, false, now.Add(2*time.Second)); ok {
		t.Fatal("different model should still share account rpm")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, false, now.Add(routeWindow+time.Second)); !ok {
		t.Fatal("different model should be available after rpm window")
	}
}

func TestPoolRouterStickyBufferExtendsAccountCapacity(t *testing.T) {
	router := newPoolRouter()
	policy := EffectiveRouting(RoutingConfig{PerAccountRPM: 1, PerAccountConcurrency: 1})
	policy.StickyBuffer = 1
	now := time.Now()

	lease, ok := router.tryAcquire("auth-1", "claude-opus", policy, false, now)
	if !ok || lease == nil {
		t.Fatal("first acquire should succeed")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, false, now.Add(time.Second)); ok {
		t.Fatal("non-sticky acquire should be blocked by base concurrency")
	}
	stickyLease, ok := router.tryAcquire("auth-1", "claude-sonnet", policy, true, now.Add(2*time.Second))
	if !ok || stickyLease == nil {
		t.Fatal("sticky acquire should use buffer capacity")
	}
	if _, ok := router.tryAcquire("auth-1", "claude-haiku", policy, true, now.Add(3*time.Second)); ok {
		t.Fatal("sticky acquire should be blocked after buffer is full")
	}

	status := router.status("auth-1", "claude-opus", policy, now.Add(4*time.Second))
	if status.RPMLimit != 2 || !status.Unavailable {
		t.Fatalf("status with sticky buffer = %#v, want rpm limit 2 and unavailable", status)
	}
	stickyLease.Release()
	lease.Release()
}

func TestCooldownForStatusUsesSeparate429And529Policies(t *testing.T) {
	policy := EffectiveRouting(RoutingConfig{
		RateLimitCooldownMS:    1000,
		RateLimitMaxCooldownMS: 4000,
		OverloadCooldownMS:     5000,
		OverloadMaxCooldownMS:  10000,
	})
	cooldown, nextLevel, ok := cooldownForStatus(StatusTooManyRequests, 2, nil, policy)
	if !ok || cooldown != 4*time.Second || nextLevel != 3 {
		t.Fatalf("429 cooldown = %s/%d/%v", cooldown, nextLevel, ok)
	}
	cooldown, nextLevel, ok = cooldownForStatus(StatusOverloaded, 1, nil, policy)
	if !ok || cooldown != 10*time.Second || nextLevel != 2 {
		t.Fatalf("529 cooldown = %s/%d/%v", cooldown, nextLevel, ok)
	}
}
