package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestClaudeExecutorTraceDumpOnlyForAccountPoolAuth(t *testing.T) {
	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "traces", "ours")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte(`
database-path: resource-pools.db
trace:
  enabled: true
  dump-dir: "`+filepath.ToSlash(dumpDir)+`"
  redact-user-content: true
`), 0o644); err != nil {
		t.Fatalf("write resource-pools.yaml: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req_test")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{
		ResourcePools: config.ResourcePoolsConfig{
			Enabled:    true,
			ConfigFile: initPath,
		},
	})
	payload := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"private user text"}]}`)
	accountPoolAuth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                        "sk-ant-oat-test",
		"base_url":                       server.URL,
		resourcepool.AttrClaudeOAuthPool: "true",
		resourcepool.AttrAccountID:       "account-1",
		"cloak_user_id":                  helps.GenerateFakeUserID(),
	}}
	if _, err := executor.CountTokens(context.Background(), accountPoolAuth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Metadata: map[string]any{
			cliproxyexecutor.PoolScopeMetadataKey: cliproxyexecutor.PoolScopeClaudeAccountPool,
		},
	}); err != nil {
		t.Fatalf("CountTokens(account pool) error: %v", err)
	}

	traces := loadTraceFiles(t, dumpDir)
	if len(traces) != 1 {
		t.Fatalf("trace count after account-pool request = %d, want 1", len(traces))
	}
	trace := traces[0]
	if trace.Source != claudetrace.SourceOurs || trace.Path != "/v1/messages/count_tokens" || trace.StatusCode != http.StatusOK {
		t.Fatalf("trace = %+v, want ours count_tokens 200", trace)
	}
	if got := trace.Headers["Authorization"]; got != "<redacted>" {
		t.Fatalf("Authorization trace header = %q, want redacted", got)
	}
	raw, err := json.Marshal(trace.Body)
	if err != nil {
		t.Fatalf("marshal trace body: %v", err)
	}
	if strings.Contains(string(raw), "private user text") || !strings.Contains(string(raw), `"redacted":true`) {
		t.Fatalf("trace body did not redact user text: %s", raw)
	}

	plainAuth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	if _, err := executor.CountTokens(context.Background(), plainAuth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("CountTokens(plain) error: %v", err)
	}
	traces = loadTraceFiles(t, dumpDir)
	if len(traces) != 1 {
		t.Fatalf("trace count after plain request = %d, want still 1", len(traces))
	}

	if _, err := executor.CountTokens(context.Background(), accountPoolAuth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("CountTokens(account pool without scope) error: %v", err)
	}
	traces = loadTraceFiles(t, dumpDir)
	if len(traces) != 1 {
		t.Fatalf("trace count after unscoped account-pool request = %d, want still 1", len(traces))
	}

	scopedCtx := cliproxyauth.ContextWithClaudePoolScope(context.Background(), cliproxyexecutor.PoolScopeClaudeAccountPool)
	if _, err := executor.CountTokens(scopedCtx, accountPoolAuth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("CountTokens(account pool with context scope) error: %v", err)
	}
	traces = loadTraceFiles(t, dumpDir)
	if len(traces) != 2 {
		t.Fatalf("trace count after context-scoped account-pool request = %d, want 2", len(traces))
	}

	pathScopedMeta := map[string]any{
		cliproxyexecutor.RequestPathMetadataKey: "/claude-acc-pool/v1/messages/count_tokens",
	}
	if _, err := executor.CountTokens(context.Background(), accountPoolAuth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Metadata:     pathScopedMeta,
	}); err != nil {
		t.Fatalf("CountTokens(account pool with request path) error: %v", err)
	}
	traces = loadTraceFiles(t, dumpDir)
	if len(traces) != 3 {
		t.Fatalf("trace count after path-scoped account-pool request = %d, want 3", len(traces))
	}
}

func loadTraceFiles(t *testing.T, dir string) []claudetrace.Trace {
	t.Helper()
	traces, err := claudetrace.LoadTraces(dir)
	if err != nil {
		t.Fatalf("LoadTraces(%s) error: %v", dir, err)
	}
	return traces
}
