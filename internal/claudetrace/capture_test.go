package claudetrace

import (
	"net/http"
	"strings"
	"testing"
)

func TestCaptureRequestRedactsSecretsAndUserContent(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"system":[{"type":"text","text":"system prompt stays visible"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"secret user text"}]}],
		"metadata":{"user_id":"user_abc_account_def_session_ghi"}
	}`)
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Claude-Code-Session-Id", "session-secret")
	req.Header.Set("X-App", "cli")

	trace := CaptureRequest(req, CaptureOptions{
		Source:            SourceReal,
		RedactUserContent: true,
		RequestBody:       body,
	})
	if got := trace.Headers["Authorization"]; got != "<redacted>" {
		t.Fatalf("Authorization header = %q, want redacted", got)
	}
	if got := trace.Headers["X-Claude-Code-Session-Id"]; got != "<redacted>" {
		t.Fatalf("Session header = %q, want redacted", got)
	}
	rawBody, ok := trace.Body.(map[string]any)
	if !ok {
		t.Fatalf("trace body type = %T, want map", trace.Body)
	}
	system := rawBody["system"].([]any)[0].(map[string]any)["text"]
	if system != "system prompt stays visible" {
		t.Fatalf("system text = %v, want visible", system)
	}
	userText := rawBody["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(map[string]any)
	if userText["redacted"] != true || userText["length"].(int) == 0 {
		t.Fatalf("user text not redacted: %+v", userText)
	}
	metadata := rawBody["metadata"].(map[string]any)["user_id"].(map[string]any)
	if metadata["redacted"] != true || metadata["kind"] != "legacy_user_account_session" {
		t.Fatalf("metadata.user_id not redacted: %+v", metadata)
	}
	if trace.BodyShape.SystemBlockCount != 1 || len(trace.BodyShape.SystemTextHashes) != 1 {
		t.Fatalf("system shape = %+v, want one system hash", trace.BodyShape)
	}
	if trace.BodyShape.MetadataUserIDKind != "legacy_user_account_session" {
		t.Fatalf("metadata user id kind = %q", trace.BodyShape.MetadataUserIDKind)
	}
}

func TestCompareIgnoresUserTextHashChanges(t *testing.T) {
	realTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"system":[{"type":"text","text":"same system"}],
		"messages":[{"role":"user","content":"hello"}]
	}`)
	oursTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"system":[{"type":"text","text":"same system"}],
		"messages":[{"role":"user","content":"different user text"}]
	}`)
	findings := CompareTracePair(realTrace, oursTrace, "trace")
	for _, item := range findings {
		if item.Severity == SeverityFatal {
			t.Fatalf("unexpected fatal finding for user text change: %+v", item)
		}
		if strings.Contains(item.Field, "user_text") {
			t.Fatalf("user text hashes should not be compared: %+v", item)
		}
	}
}

func TestCompareReportsMissingXApp(t *testing.T) {
	realTrace := sampleTraceWithBody(t, `{"model":"claude-opus-4-8","messages":[]}`)
	oursTrace := realTrace
	oursTrace.Headers = cloneStringMap(realTrace.Headers)
	delete(oursTrace.Headers, "X-App")
	findings := CompareTracePair(realTrace, oursTrace, "trace")
	if !hasFinding(findings, SeverityFatal, "headers.x-app") {
		t.Fatalf("findings = %+v, want fatal x-app missing", findings)
	}
}

func TestCompareReportsSessionHeaderMetadataMismatch(t *testing.T) {
	body := `{"model":"claude-opus-4-8","metadata":{"user_id":"{\"device_id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"account_uuid\":\"11111111-2222-4333-8444-555555555555\",\"session_id\":\"11111111-2222-4333-8444-555555555555\"}"},"messages":[]}`
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("X-Claude-Code-Session-Id", "99999999-2222-4333-8444-555555555555")
	trace := CaptureRequest(req, CaptureOptions{RequestBody: []byte(body)})
	if trace.Session.Match {
		t.Fatalf("session invariant = %+v, want mismatch", trace.Session)
	}
	findings := CompareTracePair(trace, trace, "trace")
	if !hasFinding(findings, SeverityFatal, "session") {
		t.Fatalf("findings = %+v, want fatal Session mismatch", findings)
	}
}

func TestCompareReportsToolSchemaDifference(t *testing.T) {
	realTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"messages":[],
		"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	oursTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"messages":[],
		"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}]
	}`)
	realTrace.RequestMode = RequestModeRealClaudeCodePassthrough
	oursTrace.RequestMode = RequestModeRealClaudeCodePassthrough
	findings := CompareTracePair(realTrace, oursTrace, "trace")
	if !hasFinding(findings, SeverityFatal, "tool_schema_hashes") {
		t.Fatalf("findings = %+v, want fatal tool schema diff", findings)
	}
}

func TestCompareAPIMimicDoesNotFatalMissingClaudeCodeTools(t *testing.T) {
	realTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.178.abc; cc_entrypoint=cli; cch=00000;"}],
		"messages":[],
		"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}]
	}`)
	realTrace.RequestMode = RequestModeRealClaudeCodePassthrough
	oursTrace := sampleTraceWithBody(t, `{
		"model":"claude-opus-4-8",
		"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.178.def; cc_entrypoint=cli; cch=00000;"},{"type":"text","text":"You are Claude Code"}],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	oursTrace.RequestMode = RequestModeAPIMimic

	findings := CompareTracePair(realTrace, oursTrace, "trace")
	for _, item := range findings {
		if item.Severity == SeverityFatal && (strings.Contains(item.Field, "tool_") || strings.Contains(item.Field, "system_") || strings.Contains(item.Field, "has_thinking") || strings.Contains(item.Field, "context_management")) {
			t.Fatalf("api mimic should not fatal on Claude Code-only shape: %+v", item)
		}
	}
	if !hasFinding(findings, SeverityInfo, "tool_count") {
		t.Fatalf("findings = %+v, want info tool_count mismatch", findings)
	}
}

func TestInferRequestKind(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{name: "count tokens", path: "/v1/messages/count_tokens", body: `{}`, want: RequestKindCountTokens},
		{name: "structured helper", path: "/v1/messages", body: `{"output_config":{"format":{"type":"json_schema"}},"messages":[{"role":"user","content":"title"}]}`, want: RequestKindStructuredHelper},
		{name: "tool followup", path: "/v1/messages", body: `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}]}`, want: RequestKindToolFollowup},
		{name: "interactive", path: "/v1/messages", body: `{"messages":[{"role":"user","content":"hi"}]}`, want: RequestKindInteractive},
		{name: "other", path: "/v1/complete", body: `{}`, want: RequestKindOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferRequestKind(tt.path, []byte(tt.body)); got != tt.want {
				t.Fatalf("InferRequestKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTraceGroupingIncludesRequestKind(t *testing.T) {
	realInteractive := sampleTraceWithBody(t, `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`)
	realHelper := sampleTraceWithBody(t, `{"model":"claude-opus-4-8","output_config":{"format":{"type":"json_schema"}},"messages":[{"role":"user","content":"title"}]}`)
	oursInteractive := realInteractive
	oursInteractive.Source = SourceOurs
	oursHelper := realHelper
	oursHelper.Source = SourceOurs
	findings := CompareTraceSets([]Trace{realInteractive, realHelper}, []Trace{oursHelper, oursInteractive})
	for _, item := range findings {
		if item.Severity == SeverityFatal {
			t.Fatalf("request-kind grouping produced fatal mismatch: %+v", item)
		}
	}
}

func sampleTraceWithBody(t *testing.T, raw string) Trace {
	t.Helper()
	body := []byte(raw)
	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("User-Agent", "claude-cli/2.1.178 (external, sdk-cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219")
	return CaptureRequest(req, CaptureOptions{
		Source:            SourceReal,
		RedactUserContent: true,
		RequestBody:       body,
	})
}

func hasFinding(findings []DiffFinding, severity, fieldPart string) bool {
	for _, item := range findings {
		if item.Severity == severity && strings.Contains(item.Field, fieldPart) {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
