package management

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestReadClaudeCodeManagementResponseBodyDecodesGzip(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	if _, err := gzipWriter.Write([]byte(`{"type":"error","error":{"message":"rate limited"}}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	body, err := readClaudeCodeManagementResponseBody(resp, 1024)
	if err != nil {
		t.Fatalf("readClaudeCodeManagementResponseBody() error = %v", err)
	}
	if got, want := string(body), `{"type":"error","error":{"message":"rate limited"}}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReadClaudeCodeManagementResponseBodyDetectsGzipMagicBytes(t *testing.T) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	if _, err := gzipWriter.Write([]byte(`{"input_tokens":1910}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	body, err := readClaudeCodeManagementResponseBody(resp, 1024)
	if err != nil {
		t.Fatalf("readClaudeCodeManagementResponseBody() error = %v", err)
	}
	if got, want := string(body), `{"input_tokens":1910}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestBuildClaudeCodeUsageCalibrationBodyStripsCountTokensUnsupportedFields(t *testing.T) {
	testBody, err := buildClaudeCodeAccountTestBody("claude-opus-4-8", "hi", "user_device_account_account_session_session")
	if err != nil {
		t.Fatalf("buildClaudeCodeAccountTestBody() error = %v", err)
	}
	var testPayload map[string]any
	if err := json.Unmarshal(testBody, &testPayload); err != nil {
		t.Fatalf("unmarshal test body: %v", err)
	}
	if _, ok := testPayload["metadata"]; !ok {
		t.Fatal("test body metadata missing, want normal account test requests to keep metadata")
	}
	if got, ok := testPayload["max_tokens"].(float64); !ok || int(got) != claudeCodeAccountTestMaxTokens {
		t.Fatalf("test body max_tokens = %#v, want %d", testPayload["max_tokens"], claudeCodeAccountTestMaxTokens)
	}

	calibrationBody, err := buildClaudeCodeUsageCalibrationBody("claude-opus-4-8", "user_device_account_account_session_session")
	if err != nil {
		t.Fatalf("buildClaudeCodeUsageCalibrationBody() error = %v", err)
	}
	var calibrationPayload map[string]any
	if err := json.Unmarshal(calibrationBody, &calibrationPayload); err != nil {
		t.Fatalf("unmarshal calibration body: %v", err)
	}
	if _, ok := calibrationPayload["metadata"]; ok {
		t.Fatalf("calibration payload includes metadata: %#v", calibrationPayload["metadata"])
	}
	if _, ok := calibrationPayload["max_tokens"]; ok {
		t.Fatalf("calibration payload includes max_tokens: %#v", calibrationPayload["max_tokens"])
	}
	if _, ok := calibrationPayload["system"]; !ok {
		t.Fatal("calibration payload missing system prompt")
	}
}
