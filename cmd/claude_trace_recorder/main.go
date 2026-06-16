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
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
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
		recordOnlyHTTPCode: http.StatusAccepted,
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: recorder,
	}
	fmt.Printf("Claude trace recorder listening on http://%s\n", listenAddr)
	fmt.Printf("Mode: %s, upstream: %s, out: %s\n", mode, upstream.String(), outDir)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "error: recorder failed: %v\n", err)
		os.Exit(1)
	}
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

	if s.mode == "record-only" {
		trace := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{
			Source:            claudetrace.SourceReal,
			RedactUserContent: s.redactUserContent,
			RequestBody:       body,
			StatusCode:        s.recordOnlyHTTPCode,
		})
		s.saveTrace(trace)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.recordOnlyHTTPCode)
		_, _ = w.Write([]byte(`{"ok":true,"mode":"record-only"}` + "\n"))
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
	})
	s.saveTrace(trace)
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to copy upstream response: %v\n", err)
	}
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
