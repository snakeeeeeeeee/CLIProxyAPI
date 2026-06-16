package auth

import (
	"context"
	"strings"
)

// ContextWithClaudePoolScope records the Claude pool scope selected for execution.
func ContextWithClaudePoolScope(ctx context.Context, scope string) context.Context {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, claudePoolScopeContextKey{}, scope)
}

// ClaudePoolScopeFromContext returns the Claude pool scope selected for execution.
func ClaudePoolScopeFromContext(ctx context.Context) string {
	return claudePoolScopeFromContext(ctx)
}
