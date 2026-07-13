package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRecordOnlySyntheticResponses(t *testing.T) {
	upstream, _ := url.Parse("https://api.anthropic.com")
	server := &recorderServer{
		upstream:           upstream,
		outDir:             t.TempDir(),
		mode:               "record-only",
		redactUserContent:  true,
		recordOnlyHTTPCode: http.StatusOK,
	}
	tests := []struct {
		name        string
		path        string
		body        string
		contentType string
		contains    string
	}{
		{name: "message", path: "/v1/messages", body: `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`, contentType: "application/json", contains: `"type":"message"`},
		{name: "stream", path: "/v1/messages", body: `{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`, contentType: "text/event-stream", contains: "event: message_stop"},
		{name: "count tokens", path: "/v1/messages/count_tokens", body: `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`, contentType: "application/json", contains: `"input_tokens":1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			resp := recorder.Result()
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), tt.contentType) || !strings.Contains(string(body), tt.contains) {
				t.Fatalf("response status=%d content-type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
			}
		})
	}
}

func TestRawHeaderCaptureStoresOnlyHeaderNamesInWireOrder(t *testing.T) {
	state := &rawHeaderCaptureState{}
	first := "POST /v1/messages HTTP/1.1\r\nAccept: application/json\r\nAuthorization: Bearer secret\r\nContent-Length: 2\r\n\r\n{"
	second := "}POST /v1/messages/count_tokens HTTP/1.1\r\nUser-Agent: claude-cli/test\r\nContent-Length: 0\r\n\r\n"
	state.observe([]byte(first))
	state.observe([]byte(second))
	if got := strings.Join(state.pop(), ","); got != "Accept,Authorization,Content-Length" {
		t.Fatalf("first raw order = %q", got)
	}
	if got := strings.Join(state.pop(), ","); got != "User-Agent,Content-Length" {
		t.Fatalf("second raw order = %q", got)
	}
	if bytes.Contains(state.buffer, []byte("secret")) {
		t.Fatal("capture state retained a header value")
	}
}
