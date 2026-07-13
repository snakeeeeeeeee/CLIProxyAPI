package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func registerAccountPoolAuth(t *testing.T, manager *Manager, id string) {
	t.Helper()
	_, err := manager.Register(context.Background(), &Auth{
		ID:       id,
		Provider: "claude",
		Attributes: map[string]string{
			claudeapipool.AttrOAuthPool: "true",
		},
	})
	if err != nil {
		t.Fatalf("register account-pool auth: %v", err)
	}
}

func accountPoolResultContext() context.Context {
	return ContextWithClaudePoolScope(context.Background(), cliproxyexecutor.PoolScopeClaudeAccountPool)
}

func TestAccountPoolAuthIsIsolatedFromOrdinaryClaudeRoutes(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	accountPoolAuth := &Auth{
		ID:       "account-pool-only",
		Provider: "claude",
		Attributes: map[string]string{
			claudeapipool.AttrOAuthPool: "true",
		},
	}
	ordinaryAuth := &Auth{ID: "ordinary", Provider: "claude"}
	if manager.authAllowedForProviderRouteWithOptions("claude", accountPoolAuth, cliproxyexecutor.Options{}) {
		t.Fatal("account-pool auth was allowed on an ordinary Claude route")
	}
	poolOptions := cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.PoolScopeMetadataKey: cliproxyexecutor.PoolScopeClaudeAccountPool,
	}}
	if !manager.authAllowedForProviderRouteWithOptions("claude", accountPoolAuth, poolOptions) {
		t.Fatal("account-pool auth was rejected on the account-pool route")
	}
	if !manager.authAllowedForProviderRouteWithOptions("claude", ordinaryAuth, cliproxyexecutor.Options{}) {
		t.Fatal("ordinary Claude auth was rejected on an ordinary route")
	}
	if manager.authAllowedForProviderRouteWithOptions("claude", ordinaryAuth, poolOptions) {
		t.Fatal("ordinary Claude auth was allowed on the account-pool route")
	}
}

func TestAccountPoolAuthMembershipIsStrict(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	poolA := &Auth{ID: "pool-a-auth", Provider: "claude", Attributes: map[string]string{
		claudeapipool.AttrOAuthPool:                "true",
		cliproxyexecutor.AccountPoolIDAttributeKey: "pool-a",
	}}
	poolB := &Auth{ID: "pool-b-auth", Provider: "claude", Attributes: map[string]string{
		claudeapipool.AttrOAuthPool:                "true",
		cliproxyexecutor.AccountPoolIDAttributeKey: "pool-b",
	}}
	optionsA := cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.PoolScopeMetadataKey:     cliproxyexecutor.PoolScopeClaudeAccountPool,
		cliproxyexecutor.AccountPoolIDMetadataKey: "pool-a",
	}}
	if !manager.authAllowedForProviderRouteWithOptions("claude", poolA, optionsA) {
		t.Fatal("pool A auth was rejected from pool A")
	}
	if manager.authAllowedForProviderRouteWithOptions("claude", poolB, optionsA) {
		t.Fatal("pool B auth was allowed into pool A")
	}
}

func TestAccountPoolSemanticErrorDoesNotPenalizeAccount(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	registerAccountPoolAuth(t, manager, "semantic-account")
	manager.MarkResult(accountPoolResultContext(), Result{
		AuthID:   "semantic-account",
		Provider: "claude",
		Model:    "claude-sonnet",
		Error:    &Error{HTTPStatus: http.StatusBadRequest, Message: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens must be an integer"}}`},
	})
	auth, _ := manager.GetByID("semantic-account")
	if auth.Failed != 0 || len(auth.ModelStates) != 0 || auth.Unavailable {
		t.Fatalf("semantic error changed account health: %#v", auth)
	}
}

func TestAccountPoolUnauthorizedUsesAccountCooldown(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	registerAccountPoolAuth(t, manager, "unauthorized-account")
	before := time.Now()
	manager.MarkResult(accountPoolResultContext(), Result{
		AuthID:   "unauthorized-account",
		Provider: "claude",
		Model:    "claude-sonnet",
		Error:    &Error{HTTPStatus: http.StatusUnauthorized, Message: "invalid authentication credentials"},
	})
	auth, _ := manager.GetByID("unauthorized-account")
	if !auth.Unavailable || auth.NextRetryAfter.Before(before.Add(29*time.Minute)) || auth.NextRetryAfter.After(before.Add(31*time.Minute)) {
		t.Fatalf("unauthorized account cooldown = %#v", auth)
	}
	if len(auth.ModelStates) != 0 {
		t.Fatalf("401 created model-level state: %#v", auth.ModelStates)
	}
}

func TestAccountPoolUnknownForbiddenEscalatesToManualRecovery(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	registerAccountPoolAuth(t, manager, "forbidden-account")
	for range 3 {
		manager.MarkResult(accountPoolResultContext(), Result{
			AuthID:   "forbidden-account",
			Provider: "claude",
			Model:    "claude-sonnet",
			Error:    &Error{HTTPStatus: http.StatusForbidden, Message: "forbidden by upstream"},
		})
	}
	auth, _ := manager.GetByID("forbidden-account")
	if !auth.Unavailable || !auth.NextRetryAfter.IsZero() || !strings.Contains(auth.StatusMessage, "manual_recovery") {
		t.Fatalf("forbidden escalation = %#v", auth)
	}
}

func TestAccountPoolModelCooldownDoesNotDisableWholeAccount(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	registerAccountPoolAuth(t, manager, "rate-account")
	manager.MarkResult(accountPoolResultContext(), Result{
		AuthID:     "rate-account",
		Provider:   "claude",
		Model:      "claude-sonnet",
		RetryAfter: durationPointer(time.Minute),
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate_limit_error"},
	})
	auth, _ := manager.GetByID("rate-account")
	if auth.Unavailable {
		t.Fatalf("model cooldown disabled account: %#v", auth)
	}
	state := auth.ModelStates["claude-sonnet"]
	if state == nil || !state.Unavailable || state.NextRetryAfter.IsZero() {
		t.Fatalf("missing model cooldown: %#v", state)
	}
}

func durationPointer(value time.Duration) *time.Duration { return &value }

type routingTestError struct {
	status     int
	message    string
	retryAfter time.Duration
	headers    http.Header
}

func (e routingTestError) Error() string              { return e.message }
func (e routingTestError) StatusCode() int            { return e.status }
func (e routingTestError) RetryAfter() *time.Duration { return &e.retryAfter }
func (e routingTestError) Headers() http.Header       { return e.headers.Clone() }

func TestClassifyClaudeAccountPoolRoutingErrorPreservesDecisionData(t *testing.T) {
	err := routingTestError{
		status:     http.StatusTooManyRequests,
		message:    `{"type":"error","error":{"type":"rate_limit_error","message":"limited"}}`,
		retryAfter: 3 * time.Second,
		headers:    http.Header{"Anthropic-Ratelimit-Unified-Reset": []string{"reset"}},
	}
	classified := classifyClaudeAccountPoolRoutingError(err)
	if classified.Owner != RoutingOwnerModel || classified.Action != RoutingActionCooldown || classified.Reason != "rate_limited" {
		t.Fatalf("classification = %#v", classified)
	}
	if classified.RetryAfter() == nil || *classified.RetryAfter() != 3*time.Second {
		t.Fatalf("retry after = %v", classified.RetryAfter())
	}
	if classified.Headers().Get("Anthropic-Ratelimit-Unified-Reset") != "reset" {
		t.Fatalf("headers = %#v", classified.Headers())
	}
}
