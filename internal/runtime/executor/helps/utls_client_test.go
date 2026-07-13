package helps

import (
	"bufio"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	tls "github.com/refraction-networking/utls"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type utlsClientRoundTripFunc func(*http.Request) (*http.Response, error)

func (f utlsClientRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClaudeCodeNodeClientHelloUsesHTTP11Only(t *testing.T) {
	spec := claudeCodeNodeClientHelloSpec()
	var protocols []string
	for _, extension := range spec.Extensions {
		if alpn, ok := extension.(*tls.ALPNExtension); ok {
			protocols = append(protocols, alpn.AlpnProtocols...)
		}
	}
	if strings.Join(protocols, ",") != "http/1.1" {
		t.Fatalf("ALPN protocols = %v, want only http/1.1", protocols)
	}
}

func TestClaudeCodeNodeTransportDisablesHTTP2(t *testing.T) {
	transport, err := newClaudeCodeNodeH1Transport("", nil)
	if err != nil {
		t.Fatalf("newClaudeCodeNodeH1Transport() error = %v", err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	if transport.base == nil {
		t.Fatal("base transport is nil")
	}
	if transport.base.DialTLSContext == nil {
		t.Fatal("DialTLSContext is nil")
	}
}

func TestClaudeCodeNodeTLSFingerprintFixture(t *testing.T) {
	sum := md5.Sum([]byte(ClaudeCodeNodeTLSJA3RawValue()))
	if got := fmt.Sprintf("%x", sum); got != ClaudeCodeNodeTLSJA3 {
		t.Fatalf("JA3 hash = %q, want %q", got, ClaudeCodeNodeTLSJA3)
	}
	if ClaudeCodeNodeTLSJA4 != "t13d1714h1_5b57614c22b0_7baf387fc6ff" {
		t.Fatalf("JA4 = %q", ClaudeCodeNodeTLSJA4)
	}
}

func TestClaudeCodeNodeTransportWritesCapturedHTTP11HeaderOrder(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	headersCh := make(chan []string, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, errAccept := listener.Accept()
		if errAccept != nil {
			errCh <- errAccept
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		if _, errRead := reader.ReadString('\n'); errRead != nil {
			errCh <- errRead
			return
		}
		var lines []string
		contentLength := 0
		for {
			line, errRead := reader.ReadString('\n')
			if errRead != nil {
				errCh <- errRead
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if line == "" {
				break
			}
			lines = append(lines, line)
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				contentLength, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		if contentLength > 0 {
			if _, errRead := io.CopyN(io.Discard, reader, int64(contentLength)); errRead != nil {
				errCh <- errRead
				return
			}
		}
		headersCh <- lines
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
	}()

	transport, err := newClaudeCodeNodeH1Transport("", nil)
	if err != nil {
		t.Fatalf("newClaudeCodeNodeH1Transport() error = %v", err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/messages?beta=true", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	for key, value := range map[string]string{
		"Accept":                                    "application/json",
		"Content-Type":                              "application/json",
		"User-Agent":                                "claude-cli/2.1.207 (external, sdk-cli)",
		"X-Claude-Code-Session-Id":                  "11111111-2222-4333-8444-555555555555",
		"X-Stainless-Lang":                          "js",
		"X-Stainless-Package-Version":               "0.94.0",
		"X-Stainless-Os":                            "MacOS",
		"X-Stainless-Arch":                          "arm64",
		"X-Stainless-Runtime":                       "node",
		"X-Stainless-Runtime-Version":               "v26.3.0",
		"X-Stainless-Retry-Count":                   "0",
		"X-Stainless-Timeout":                       "600",
		"Anthropic-Dangerous-Direct-Browser-Access": "true",
		"Anthropic-Version":                         "2023-06-01",
		"Anthropic-Beta":                            "claude-code-20250219",
		"Authorization":                             "Bearer test",
		"X-App":                                     "cli",
		"Connection":                                "keep-alive",
		"Accept-Encoding":                           "gzip, deflate, br, zstd",
	} {
		req.Header.Set(key, value)
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp.Body.Close()

	select {
	case errCapture := <-errCh:
		t.Fatalf("capture error = %v", errCapture)
	case lines := <-headersCh:
		got := make([]string, 0, len(lines))
		values := make(map[string]string, len(lines))
		for _, line := range lines {
			name, value, _ := strings.Cut(line, ":")
			got = append(got, name)
			values[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
		want := []string{
			"Accept", "Content-Type", "User-Agent", "X-Claude-Code-Session-Id",
			"X-Stainless-Lang", "X-Stainless-Package-Version", "X-Stainless-OS", "X-Stainless-Arch",
			"X-Stainless-Runtime", "X-Stainless-Runtime-Version", "X-Stainless-Retry-Count", "X-Stainless-Timeout",
			"anthropic-dangerous-direct-browser-access", "anthropic-version", "anthropic-beta", "Authorization",
			"x-app", "Connection", "Host", "Accept-Encoding", "Content-Length",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("header order =\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if values["authorization"] != "Bearer test" {
			t.Fatalf("Authorization wire value = %q, want Bearer credential", values["authorization"])
		}
	}
}

func TestClaudeCodeNodeTransportCacheReplacesProxyForAccount(t *testing.T) {
	resetClaudeCodeNodeTransportCacheForTest()
	t.Cleanup(resetClaudeCodeNodeTransportCacheForTest)
	auth := &cliproxyauth.Auth{ID: "auth-a", Attributes: map[string]string{
		"claude_code_account_id":       "account-a",
		"claude_code_profile_revision": ClaudeCodeNodeProfileRevision,
	}}
	first, err := cachedClaudeCodeNodeTransportFor(auth, "direct")
	if err != nil {
		t.Fatalf("first cached transport error = %v", err)
	}
	second, err := cachedClaudeCodeNodeTransportFor(auth, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("second cached transport error = %v", err)
	}
	if first == second {
		t.Fatal("proxy change reused the previous transport")
	}
	claudeCodeNodeTransports.Lock()
	entryCount := len(claudeCodeNodeTransports.entries)
	claudeCodeNodeTransports.Unlock()
	if entryCount != 1 {
		t.Fatalf("cache entries = %d, want one active account transport", entryCount)
	}
}

func resetClaudeCodeNodeTransportCacheForTest() {
	claudeCodeNodeTransports.Lock()
	defer claudeCodeNodeTransports.Unlock()
	for key, cached := range claudeCodeNodeTransports.entries {
		cached.transport.CloseIdleConnections()
		delete(claudeCodeNodeTransports.entries, key)
	}
}

func TestNewUtlsHTTPClientUsesContextRoundTripperForProtectedHost(t *testing.T) {
	t.Parallel()

	called := false
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", utlsClientRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if req.URL.Hostname() != "chatgpt.com" {
			t.Fatalf("hostname = %q, want chatgpt.com", req.URL.Hostname())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    req,
		}, nil
	}))

	client := NewUtlsHTTPClient(ctx, nil, nil, 0)
	resp, err := client.Get("https://chatgpt.com/backend-api/codex/responses")
	if err != nil {
		t.Fatalf("client.Get returned error: %v", err)
	}
	if errClose := resp.Body.Close(); errClose != nil {
		t.Fatalf("response body close returned error: %v", errClose)
	}
	if !called {
		t.Fatal("expected context RoundTripper to handle protected host request")
	}
}
