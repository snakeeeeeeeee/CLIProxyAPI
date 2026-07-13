// Command claude_trace_recorder records redacted Claude Code CLI request traces
// and optionally forwards requests to Anthropic.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
	"github.com/tidwall/gjson"
)

var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

func main() {
	var listenAddr string
	var upstreamRaw string
	var outDir string
	var mode string
	var redactUserContent bool

	flag.StringVar(&listenAddr, "listen", "127.0.0.1:39001", "Listen address")
	flag.StringVar(&upstreamRaw, "upstream", "https://api.anthropic.com", "Upstream Anthropic base URL")
	flag.StringVar(&outDir, "out", "traces/real", "Output trace directory")
	flag.StringVar(&mode, "mode", "forward", "Mode: forward or record-only")
	flag.BoolVar(&redactUserContent, "redact-user-content", true, "Redact user message text in trace files")
	flag.Parse()

	upstream, err := url.Parse(strings.TrimSpace(upstreamRaw))
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		fmt.Fprintf(os.Stderr, "error: invalid upstream %q\n", upstreamRaw)
		os.Exit(1)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "forward"
	}
	if mode != "forward" && mode != "record-only" {
		fmt.Fprintf(os.Stderr, "error: invalid mode %q, want forward or record-only\n", mode)
		os.Exit(1)
	}
	if host, _, err := net.SplitHostPort(listenAddr); err == nil {
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			fmt.Fprintf(os.Stderr, "warning: recorder listen address %s is not loopback\n", listenAddr)
		}
	}

	recorder := &recorderServer{
		upstream:           upstream,
		outDir:             outDir,
		mode:               mode,
		redactUserContent:  redactUserContent,
		forwardHTTPClient:  http.DefaultClient,
		recordOnlyHTTPCode: http.StatusOK,
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: recorder,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if captured, ok := conn.(*rawHeaderCaptureConn); ok {
				return context.WithValue(ctx, rawHeaderCaptureContextKey{}, captured.state)
			}
			return ctx
		},
	}
	fmt.Printf("Claude trace recorder listening on http://%s\n", listenAddr)
	fmt.Printf("Mode: %s, upstream: %s, out: %s\n", mode, upstream.String(), outDir)
	listener, errListen := net.Listen("tcp", listenAddr)
	if errListen != nil {
		fmt.Fprintf(os.Stderr, "error: recorder listen failed: %v\n", errListen)
		os.Exit(1)
	}
	if err := server.Serve(rawHeaderCaptureListener{Listener: listener}); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "error: recorder failed: %v\n", err)
		os.Exit(1)
	}
}

type rawHeaderCaptureContextKey struct{}

type rawHeaderCaptureListener struct {
	net.Listener
}

func (l rawHeaderCaptureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &rawHeaderCaptureConn{Conn: conn, state: &rawHeaderCaptureState{}}, nil
}

type rawHeaderCaptureConn struct {
	net.Conn
	state *rawHeaderCaptureState
}

func (c *rawHeaderCaptureConn) Read(buffer []byte) (int, error) {
	n, err := c.Conn.Read(buffer)
	if n > 0 && c.state != nil {
		c.state.observe(buffer[:n])
	}
	return n, err
}

type rawHeaderCaptureState struct {
	mu            sync.Mutex
	buffer        []byte
	bodyRemaining int
	orders        [][]string
	unsupported   bool
}

func (s *rawHeaderCaptureState) observe(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsupported || len(data) == 0 {
		return
	}
	s.buffer = append(s.buffer, data...)
	for {
		if s.bodyRemaining > 0 {
			consume := s.bodyRemaining
			if consume > len(s.buffer) {
				consume = len(s.buffer)
			}
			s.buffer = s.buffer[consume:]
			s.bodyRemaining -= consume
			if s.bodyRemaining > 0 {
				return
			}
		}
		end := bytes.Index(s.buffer, []byte("\r\n\r\n"))
		if end < 0 {
			if len(s.buffer) > 1<<20 {
				s.buffer = nil
				s.unsupported = true
			}
			return
		}
		headerBlock := append([]byte(nil), s.buffer[:end]...)
		s.buffer = s.buffer[end+4:]
		order, contentLength, chunked := parseRawRequestHeaderBlock(headerBlock)
		if len(order) > 0 {
			s.orders = append(s.orders, order)
		}
		if chunked {
			s.buffer = nil
			s.unsupported = true
			return
		}
		s.bodyRemaining = contentLength
		if len(s.buffer) == 0 && s.bodyRemaining == 0 {
			return
		}
	}
}

func (s *rawHeaderCaptureState) pop() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.orders) == 0 {
		return nil
	}
	order := append([]string(nil), s.orders[0]...)
	s.orders = s.orders[1:]
	return order
}

func parseRawRequestHeaderBlock(block []byte) (order []string, contentLength int, chunked bool) {
	lines := bytes.Split(block, []byte("\r\n"))
	for _, line := range lines[1:] {
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		headerName := strings.TrimSpace(string(name))
		if headerName == "" {
			continue
		}
		order = append(order, headerName)
		switch strings.ToLower(headerName) {
		case "content-length":
			contentLength, _ = strconv.Atoi(strings.TrimSpace(string(value)))
		case "transfer-encoding":
			chunked = strings.Contains(strings.ToLower(string(value)), "chunked")
		}
	}
	return order, contentLength, chunked
}

func rawHeaderOrderFromRequest(req *http.Request) []string {
	if req == nil {
		return nil
	}
	state, _ := req.Context().Value(rawHeaderCaptureContextKey{}).(*rawHeaderCaptureState)
	return state.pop()
}

type recorderServer struct {
	upstream           *url.URL
	outDir             string
	mode               string
	redactUserContent  bool
	forwardHTTPClient  *http.Client
	recordOnlyHTTPCode int
}

func (s *recorderServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	rawHeaderOrder := rawHeaderOrderFromRequest(req)

	if s.mode == "record-only" {
		trace := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{
			Source:            claudetrace.SourceReal,
			RedactUserContent: s.redactUserContent,
			RequestBody:       body,
			StatusCode:        s.recordOnlyHTTPCode,
			RawHeaderOrder:    rawHeaderOrder,
		})
		s.saveTrace(trace)
		s.writeSyntheticAnthropicResponse(w, req, body)
		return
	}

	upstreamReq, err := s.buildUpstreamRequest(req.Context(), req, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := s.forwardHTTPClient.Do(upstreamReq)
	if err != nil {
		trace := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{
			Source:            claudetrace.SourceReal,
			RedactUserContent: s.redactUserContent,
			RequestBody:       body,
			ResponseError:     err.Error(),
			RawHeaderOrder:    rawHeaderOrder,
		})
		s.saveTrace(trace)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	trace := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{
		Source:            claudetrace.SourceReal,
		RedactUserContent: s.redactUserContent,
		RequestBody:       body,
		StatusCode:        resp.StatusCode,
		ResponseHeaders:   resp.Header.Clone(),
		RawHeaderOrder:    rawHeaderOrder,
	})
	s.saveTrace(trace)
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to copy upstream response: %v\n", err)
	}
}

func (s *recorderServer) writeSyntheticAnthropicResponse(w http.ResponseWriter, req *http.Request, body []byte) {
	statusCode := s.recordOnlyHTTPCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	requestID := "req_trace_" + fmt.Sprintf("%d", time.Now().UnixNano())
	w.Header().Set("request-id", requestID)
	if req != nil && strings.HasSuffix(req.URL.Path, "/messages/count_tokens") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(`{"input_tokens":1}`))
		return
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	if gjson.GetBytes(body, "stream").Bool() {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(statusCode)
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_trace\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n", model)
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `{"id":"msg_trace","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`, model)
}

func (s *recorderServer) buildUpstreamRequest(ctx context.Context, req *http.Request, body []byte) (*http.Request, error) {
	target := *s.upstream
	target.Path = singleJoiningSlash(s.upstream.Path, req.URL.Path)
	target.RawQuery = req.URL.RawQuery
	target.Fragment = ""
	upstreamReq, err := http.NewRequestWithContext(ctx, req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}
	copyForwardHeaders(upstreamReq.Header, req.Header)
	upstreamReq.Host = s.upstream.Host
	return upstreamReq, nil
}

func (s *recorderServer) saveTrace(trace claudetrace.Trace) {
	path, err := claudetrace.SaveTrace(s.outDir, trace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save trace: %v\n", err)
		return
	}
	fmt.Printf("saved trace: %s\n", path)
}

func copyForwardHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopByHopHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopByHopHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}
