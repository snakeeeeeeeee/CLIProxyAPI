package executor

import (
	"net/http"
	"testing"
	"time"
)

func TestStatusErrFromResponsePreservesRetryHeaders(t *testing.T) {
	headers := http.Header{
		"Retry-After":                       []string{"2.5"},
		"Anthropic-Ratelimit-Unified-Reset": []string{time.Now().Add(time.Minute).Format(time.RFC3339)},
	}
	err := statusErrFromResponse(http.StatusTooManyRequests, "rate limited", headers)
	if err.RetryAfter() == nil || *err.RetryAfter() < 2400*time.Millisecond || *err.RetryAfter() > 2600*time.Millisecond {
		t.Fatalf("retry after = %v", err.RetryAfter())
	}
	if got := err.Headers().Get("Anthropic-Ratelimit-Unified-Reset"); got == "" {
		t.Fatal("unified reset header was not preserved")
	}
}

func TestStatusErrFromResponseUsesAnthropicResetWithoutRetryAfter(t *testing.T) {
	now := time.Now()
	headers := http.Header{"Anthropic-Ratelimit-Requests-Reset": []string{now.Add(3 * time.Second).Format(time.RFC3339Nano)}}
	retryAfter := retryAfterFromUpstreamHeaders(headers, now)
	if retryAfter == nil || *retryAfter < 2900*time.Millisecond || *retryAfter > 3100*time.Millisecond {
		t.Fatalf("reset retry after = %v", retryAfter)
	}
}
