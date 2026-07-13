package claudetrace

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/sjson"
)

func TestGoldenTraceRegressionScenarios(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "real")
	oursDir := filepath.Join(t.TempDir(), "ours")
	scenarios := []struct {
		name   string
		path   string
		model  string
		stream bool
		body   string
	}{
		{
			name:  "ordinary-message-opus",
			path:  "/v1/messages",
			model: "claude-opus-4-8",
			body:  `{"model":"claude-opus-4-8","system":[{"type":"text","text":"system"}],"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:   "stream-message-sonnet",
			path:   "/v1/messages",
			model:  "claude-sonnet-4-6",
			stream: true,
			body:   `{"model":"claude-sonnet-4-6","stream":true,"system":[{"type":"text","text":"system"}],"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:  "tool-use-haiku",
			path:  "/v1/messages",
			model: "claude-haiku-4-5-20251001",
			body:  `{"model":"claude-haiku-4-5-20251001","system":[{"type":"text","text":"system"}],"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}],"messages":[{"role":"user","content":"read file"}]}`,
		},
		{
			name:  "tool-result-sonnet",
			path:  "/v1/messages",
			model: "claude-sonnet-4-6",
			body:  `{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"system"}],"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"a.go"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}]}`,
		},
		{
			name:  "count-tokens-opus",
			path:  "/v1/messages/count_tokens",
			model: "claude-opus-4-8",
			body:  `{"model":"claude-opus-4-8","system":[{"type":"text","text":"system"}],"messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:  "cache-control-haiku",
			path:  "/v1/messages",
			model: "claude-haiku-4-5-20251001",
			body:  `{"model":"claude-haiku-4-5-20251001","system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`,
		},
	}
	for _, scenario := range scenarios {
		realTrace := goldenScenarioTrace(t, SourceReal, scenario.path, scenario.body, scenario.stream)
		oursTrace := goldenScenarioTrace(t, SourceOurs, scenario.path, scenario.body, scenario.stream)
		oursTrace.Headers["X-Claude-Code-Session-Id"] = "session-ours-" + scenario.name
		if _, err := SaveTrace(realDir, realTrace); err != nil {
			t.Fatalf("SaveTrace(real %s) error: %v", scenario.name, err)
		}
		if _, err := SaveTrace(oursDir, oursTrace); err != nil {
			t.Fatalf("SaveTrace(ours %s) error: %v", scenario.name, err)
		}
	}
	realTraces, err := LoadTraces(realDir)
	if err != nil {
		t.Fatalf("LoadTraces(real) error: %v", err)
	}
	oursTraces, err := LoadTraces(oursDir)
	if err != nil {
		t.Fatalf("LoadTraces(ours) error: %v", err)
	}
	findings := CompareTraceSets(realTraces, oursTraces)
	for _, item := range findings {
		if item.Severity == SeverityFatal || item.Severity == SeverityWarn {
			t.Fatalf("unexpected golden finding: %+v", item)
		}
	}
}

func goldenScenarioTrace(t *testing.T, source, path, body string, stream bool) Trace {
	t.Helper()
	metadata := `{"device_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","account_uuid":"11111111-2222-4333-8444-555555555555","session_id":"session-real"}`
	bodyWithMetadata, err := sjson.Set(body, "metadata.user_id", metadata)
	if err != nil {
		t.Fatalf("set metadata: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com"+path+"?beta=true", strings.NewReader(bodyWithMetadata))
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	req.Header.Set("User-Agent", "claude-cli/2.1.178 (external, sdk-cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,context-management-2025-06-27")
	req.Header.Set("X-Claude-Code-Session-Id", "session-real")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return CaptureRequest(req, CaptureOptions{
		Source:            source,
		RedactUserContent: true,
		RequestBody:       []byte(bodyWithMetadata),
		StatusCode:        http.StatusOK,
		Stream:            stream,
	})
}
