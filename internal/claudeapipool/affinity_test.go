package claudeapipool

import (
	"testing"
	"time"
)

func TestAffinityRouterStableSelectionForSameKey(t *testing.T) {
	router := &affinityRouter{entries: make(map[string]*affinityEntry)}
	policy := EffectiveRouting(RoutingConfig{
		CacheAffinityEnabled: true,
		CacheAffinityLanes:   2,
	})
	req := AffinityRequest{
		Provider:          "claude",
		Model:             "claude-opus",
		SessionKey:        "session-a",
		PrefixFingerprint: "prefix-a",
		EstimateTokens:    8000,
		TTL:               time.Minute,
	}
	auths := []string{"auth-a", "auth-b", "auth-c"}
	first := router.selectAuth(req, auths, nil, policy, time.Now())
	second := router.selectAuth(req, auths, nil, policy, time.Now().Add(time.Second))
	if first.AuthID == "" || first.AuthID != second.AuthID {
		t.Fatalf("selection not stable: first=%#v second=%#v", first, second)
	}
	active, lanes := router.stats(time.Now())
	if active != 1 || lanes != 2 {
		t.Fatalf("stats = %d/%d, want 1/2", active, lanes)
	}
}

func TestAffinityRouterFallsBackWithinOrderedPool(t *testing.T) {
	router := &affinityRouter{entries: make(map[string]*affinityEntry)}
	policy := EffectiveRouting(RoutingConfig{
		CacheAffinityEnabled:  true,
		CacheAffinityAuto:     true,
		CacheAffinityLanes:    1,
		CacheAffinityMaxLanes: 3,
	})
	req := AffinityRequest{
		Provider:          "claude",
		Model:             "claude-opus",
		SessionKey:        "session-a",
		PrefixFingerprint: "prefix-a",
		EstimateTokens:    8000,
		TTL:               time.Minute,
	}
	auths := []string{"auth-a", "auth-b", "auth-c"}
	first := router.selectAuth(req, auths, nil, policy, time.Now())
	blocked := map[string]struct{}{first.AuthID: {}}
	second := router.selectAuth(req, auths, blocked, policy, time.Now().Add(time.Second))
	if second.AuthID == "" || second.AuthID == first.AuthID {
		t.Fatalf("fallback selection = %#v first=%#v", second, first)
	}
}
