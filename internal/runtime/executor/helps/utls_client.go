package helps

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	reqclient "github.com/imroc/req/v3"
	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// utlsRoundTripper implements http.RoundTripper using utls with Chrome fingerprint
// to bypass Cloudflare's TLS fingerprinting on Anthropic domains.
type utlsRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*http2.ClientConn
	pending     map[string]*sync.Cond
	dialer      proxy.Dialer
	profile     utlsProfile
}

type utlsProfile string

const (
	utlsProfileChrome         utlsProfile = "chrome"
	utlsProfileClaudeCodeNode utlsProfile = "claude-code-node"
)

type claudeCodeTLSProfileContextKey struct{}

type claudeCodeNodeTransportKey struct {
	accountID string
	proxyURL  string
	revision  string
}

type cachedClaudeCodeNodeTransport struct {
	transport *claudeCodeNodeH1Transport
	lastUsed  time.Time
}

var claudeCodeNodeTransports = struct {
	sync.Mutex
	entries map[claudeCodeNodeTransportKey]cachedClaudeCodeNodeTransport
}{entries: make(map[claudeCodeNodeTransportKey]cachedClaudeCodeNodeTransport)}

const (
	maxClaudeCodeNodeTransports      = 64
	ClaudeCodeNodeProfileRevision    = "2.1.207-r3"
	ClaudeCodeNodeTLSProfileName     = "node-macos-arm64-http1"
	ClaudeCodeNodeTLSJA3             = "44f88fca027f27bab4bb08d4af15f23e"
	ClaudeCodeNodeTLSJA4             = "t13d1714h1_5b57614c22b0_7baf387fc6ff"
	ClaudeCodeNodeTLSALPN            = "http/1.1"
	claudeCodeProfileHeaderOrderAttr = "claude_code_profile_header_order_json"
	claudeCodeNodeTLSJA3Raw          = "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-65037-23-65281-10-11-35-16-5-13-18-51-45-43,29-23-24,0"
)

var claudeCodeNodeDefaultHeaderOrder = []string{
	"Accept",
	"Authorization",
	"x-api-key",
	"Content-Type",
	"User-Agent",
	"X-Claude-Code-Session-Id",
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"anthropic-beta",
	"anthropic-dangerous-direct-browser-access",
	"anthropic-version",
	"x-app",
	"Connection",
	"Host",
	"Accept-Encoding",
	"Content-Length",
}

var claudeCodeNodeR2HeaderOrder = []string{
	"Accept", "Content-Type", "User-Agent", "X-Claude-Code-Session-Id",
	"X-Stainless-Lang", "X-Stainless-Package-Version", "X-Stainless-Os", "X-Stainless-Arch",
	"X-Stainless-Runtime", "X-Stainless-Runtime-Version", "X-Stainless-Retry-Count", "X-Stainless-Timeout",
	"anthropic-dangerous-direct-browser-access", "anthropic-version", "anthropic-beta", "Authorization",
	"x-api-key", "x-app", "Connection", "Host", "Accept-Encoding", "Content-Length",
}

// ClaudeCodeNodeHeaderOrder returns the raw HTTP/1.1 order captured from the
// built-in Claude Code profile. The returned slice can be modified safely.
func ClaudeCodeNodeHeaderOrder() []string {
	return append([]string(nil), claudeCodeNodeDefaultHeaderOrder...)
}

// ClaudeCodeNodeR2HeaderOrder returns the prior built-in order for exact migration matching.
func ClaudeCodeNodeR2HeaderOrder() []string {
	return append([]string(nil), claudeCodeNodeR2HeaderOrder...)
}

// ClaudeCodeNodeTLSJA3RawValue returns the captured JA3 source tuple used by
// fixture tests and trace summaries.
func ClaudeCodeNodeTLSJA3RawValue() string {
	return claudeCodeNodeTLSJA3Raw
}

// WithClaudeCodeTLSProfile marks official Anthropic account-pool requests for
// the Node.js/Claude Code uTLS profile. Other provider paths keep their legacy
// TLS behavior.
func WithClaudeCodeTLSProfile(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, claudeCodeTLSProfileContextKey{}, true)
}

func ClaudeCodeTLSProfileEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(claudeCodeTLSProfileContextKey{}).(bool)
	return enabled
}

func newUtlsRoundTripper(proxyURL string, profile utlsProfile) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
			dialer = proxyErrorDialer{err: fmt.Errorf("configure outbound proxy: %w", errBuild)}
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
			dialer = proxyDialer
		}
	}
	return &utlsRoundTripper{
		connections: make(map[string]*http2.ClientConn),
		pending:     make(map[string]*sync.Cond),
		dialer:      dialer,
		profile:     profile,
	}
}

type proxyErrorDialer struct {
	err error
}

func (d proxyErrorDialer) Dial(string, string) (net.Conn, error) {
	return nil, d.err
}

func (t *utlsRoundTripper) getOrCreateConnection(host, addr string) (*http2.ClientConn, error) {
	t.mu.Lock()

	if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
		t.mu.Unlock()
		return h2Conn, nil
	}

	if cond, ok := t.pending[host]; ok {
		cond.Wait()
		if h2Conn, ok := t.connections[host]; ok && h2Conn.CanTakeNewRequest() {
			t.mu.Unlock()
			return h2Conn, nil
		}
	}

	cond := sync.NewCond(&t.mu)
	t.pending[host] = cond
	t.mu.Unlock()

	h2Conn, err := t.createConnection(host, addr)

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pending, host)
	cond.Broadcast()

	if err != nil {
		return nil, err
	}

	t.connections[host] = h2Conn
	return h2Conn, nil
}

func (t *utlsRoundTripper) createConnection(host, addr string) (*http2.ClientConn, error) {
	conn, err := t.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{ServerName: host}
	tlsConn := tls.UClient(conn, tlsConfig, tls.HelloChrome_Auto)
	if t.profile == utlsProfileClaudeCodeNode {
		tlsConn = tls.UClient(conn, tlsConfig, tls.HelloCustom)
		if err := tlsConn.ApplyPreset(claudeCodeNodeClientHelloSpec()); err != nil {
			conn.Close()
			return nil, fmt.Errorf("apply claude code node tls preset: %w", err)
		}
	}

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	tr := &http2.Transport{}
	h2Conn, err := tr.NewClientConn(tlsConn)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}

	return h2Conn, nil
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	hostname := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(hostname, port)

	h2Conn, err := t.getOrCreateConnection(hostname, addr)
	if err != nil {
		return nil, err
	}

	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		t.mu.Lock()
		if cached, ok := t.connections[hostname]; ok && cached == h2Conn {
			delete(t.connections, hostname)
		}
		t.mu.Unlock()
		return nil, err
	}

	return resp, nil
}

func claudeCodeNodeClientHelloSpec() *tls.ClientHelloSpec {
	return &tls.ClientHelloSpec{
		CipherSuites: []uint16{
			0x1301, 0x1302, 0x1303,
			0xc02b, 0xc02f, 0xc02c, 0xc030,
			0xcca9, 0xcca8,
			0xc009, 0xc013, 0xc00a, 0xc014,
			0x009c, 0x009d,
			0x002f, 0x0035,
		},
		CompressionMethods: []byte{0},
		TLSVersMax:         tls.VersionTLS13,
		TLSVersMin:         tls.VersionTLS10,
		Extensions: []tls.TLSExtension{
			&tls.SNIExtension{},
			&tls.GREASEEncryptedClientHelloExtension{},
			&tls.ExtendedMasterSecretExtension{},
			&tls.RenegotiationInfoExtension{},
			&tls.SupportedCurvesExtension{Curves: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}},
			&tls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&tls.SessionTicketExtension{},
			&tls.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},
			&tls.StatusRequestExtension{},
			&tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tls.SignatureScheme{
				0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601, 0x0201,
			}},
			&tls.SCTExtension{},
			&tls.KeyShareExtension{KeyShares: []tls.KeyShare{{Group: tls.X25519}}},
			&tls.PSKKeyExchangeModesExtension{Modes: []uint8{uint8(tls.PskModeDHE)}},
			&tls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}},
		},
	}
}

// utlsProtectedHosts contains the hosts that should use utls Chrome TLS fingerprint
// to bypass Cloudflare's TLS fingerprinting.
var utlsProtectedHosts = map[string]struct{}{
	"api.anthropic.com": {},
	"chatgpt.com":       {},
}

// fallbackRoundTripper uses utls for protected HTTPS hosts and falls back to
// standard transport for all other requests.
type fallbackRoundTripper struct {
	utls     http.RoundTripper
	fallback http.RoundTripper
}

func (f *fallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		if _, ok := utlsProtectedHosts[strings.ToLower(req.URL.Hostname())]; ok {
			return f.utls.RoundTrip(req)
		}
	}
	return f.fallback.RoundTrip(req)
}

// NewUtlsHTTPClient creates an HTTP client using utls Chrome TLS fingerprint.
// Use this for provider requests that need a Chrome-like TLS fingerprint.
// Falls back to standard transport for non-HTTPS requests.
func NewUtlsHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	var ctxRoundTripper http.RoundTripper
	if ctx != nil {
		ctxRoundTripper, _ = ctx.Value("cliproxy.roundtripper").(http.RoundTripper)
	}
	if ClaudeCodeTLSProfileEnabled(ctx) {
		if proxyURL == "" && ctxRoundTripper != nil {
			return &http.Client{Transport: ctxRoundTripper, Timeout: timeout}
		}
		nodeTransport, errTransport := cachedClaudeCodeNodeTransportFor(auth, proxyURL)
		if errTransport != nil {
			return &http.Client{Transport: errorRoundTripper{err: errTransport}, Timeout: timeout}
		}
		return &http.Client{Transport: nodeTransport, Timeout: timeout}
	}

	profile := utlsProfileChrome
	var utlsRT http.RoundTripper = newUtlsRoundTripper(proxyURL, profile)
	var standardTransport http.RoundTripper = http.DefaultTransport
	if proxyURL != "" {
		if transport := buildProxyTransport(proxyURL); transport != nil {
			standardTransport = transport
		}
	} else if ctxRoundTripper != nil {
		utlsRT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	}

	client := &http.Client{
		Transport: &fallbackRoundTripper{
			utls:     utlsRT,
			fallback: standardTransport,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

type errorRoundTripper struct {
	err error
}

func (r errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, r.err
}

func cachedClaudeCodeNodeTransportFor(auth *cliproxyauth.Auth, proxyURL string) (*claudeCodeNodeH1Transport, error) {
	key := claudeCodeNodeTransportKey{
		accountID: claudeCodeNodeAccountKey(auth),
		proxyURL:  strings.TrimSpace(proxyURL),
		revision:  claudeCodeNodeProfileRevision(auth),
	}
	now := time.Now()
	claudeCodeNodeTransports.Lock()
	defer claudeCodeNodeTransports.Unlock()
	if cached, ok := claudeCodeNodeTransports.entries[key]; ok {
		cached.lastUsed = now
		claudeCodeNodeTransports.entries[key] = cached
		return cached.transport, nil
	}
	transport, err := newClaudeCodeNodeH1Transport(key.proxyURL, ClaudeCodeNodeHeaderOrderForAuth(auth))
	if err != nil {
		return nil, err
	}
	for existingKey, cached := range claudeCodeNodeTransports.entries {
		if key.accountID != "" && existingKey.accountID == key.accountID && existingKey != key {
			cached.transport.CloseIdleConnections()
			delete(claudeCodeNodeTransports.entries, existingKey)
		}
	}
	if len(claudeCodeNodeTransports.entries) >= maxClaudeCodeNodeTransports {
		var oldestKey claudeCodeNodeTransportKey
		var oldestTime time.Time
		for existingKey, cached := range claudeCodeNodeTransports.entries {
			if oldestTime.IsZero() || cached.lastUsed.Before(oldestTime) {
				oldestKey = existingKey
				oldestTime = cached.lastUsed
			}
		}
		if cached, ok := claudeCodeNodeTransports.entries[oldestKey]; ok {
			cached.transport.CloseIdleConnections()
			delete(claudeCodeNodeTransports.entries, oldestKey)
		}
	}
	claudeCodeNodeTransports.entries[key] = cachedClaudeCodeNodeTransport{transport: transport, lastUsed: now}
	return transport, nil
}

func claudeCodeNodeAccountKey(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Attributes != nil {
		if accountID := strings.TrimSpace(auth.Attributes["claude_code_account_id"]); accountID != "" {
			return accountID
		}
	}
	return strings.TrimSpace(auth.ID)
}

func claudeCodeNodeProfileRevision(auth *cliproxyauth.Auth) string {
	if auth != nil && auth.Attributes != nil {
		if revision := strings.TrimSpace(auth.Attributes["claude_code_profile_revision"]); revision != "" {
			return revision
		}
	}
	return ClaudeCodeNodeProfileRevision
}

func ClaudeCodeNodeHeaderOrderForAuth(auth *cliproxyauth.Auth) []string {
	if auth != nil && auth.Attributes != nil {
		if raw := strings.TrimSpace(auth.Attributes[claudeCodeProfileHeaderOrderAttr]); raw != "" {
			var order []string
			if err := json.Unmarshal([]byte(raw), &order); err == nil {
				normalized := make([]string, 0, len(order))
				seen := make(map[string]bool, len(order))
				for _, key := range order {
					key = strings.TrimSpace(key)
					canonical := strings.ToLower(key)
					if key == "" || seen[canonical] {
						continue
					}
					seen[canonical] = true
					normalized = append(normalized, key)
				}
				if len(normalized) > 0 {
					return normalized
				}
			}
		}
	}
	return ClaudeCodeNodeHeaderOrder()
}

type claudeCodeNodeH1Transport struct {
	base        *reqclient.Transport
	headerOrder []string
}

func (t *claudeCodeNodeH1Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, fmt.Errorf("claude code node transport is not initialized")
	}
	if req == nil {
		return nil, fmt.Errorf("claude code node request is nil")
	}
	outReq := req.Clone(req.Context())
	applyClaudeCodeNodeWireHeaderCase(outReq.Header)
	outReq.Header[reqclient.HeaderOderKey] = append([]string(nil), t.headerOrder...)
	resp, err := t.base.RoundTrip(outReq)
	if resp != nil {
		resp.Request = req
	}
	return resp, err
}

func (t *claudeCodeNodeH1Transport) CloseIdleConnections() {
	if t != nil && t.base != nil {
		t.base.CloseIdleConnections()
	}
}

func applyClaudeCodeNodeWireHeaderCase(headers http.Header) {
	for canonical, wire := range map[string]string{
		"Anthropic-Dangerous-Direct-Browser-Access": "anthropic-dangerous-direct-browser-access",
		"Anthropic-Version":                         "anthropic-version",
		"Anthropic-Beta":                            "anthropic-beta",
		"X-Api-Key":                                 "x-api-key",
		"X-App":                                     "x-app",
		"X-Stainless-Os":                            "X-Stainless-OS",
	} {
		moveHTTPHeaderCase(headers, canonical, wire)
	}
}

func moveHTTPHeaderCase(headers http.Header, canonical, wire string) {
	if headers == nil || canonical == "" || wire == "" {
		return
	}
	var values []string
	for key, current := range headers {
		if strings.EqualFold(key, canonical) {
			values = append(values, current...)
			delete(headers, key)
		}
	}
	if len(values) > 0 {
		headers[wire] = values
	}
}

func newClaudeCodeNodeH1Transport(proxyURL string, headerOrder []string) (*claudeCodeNodeH1Transport, error) {
	var dialContext func(context.Context, string, string) (net.Conn, error)
	if strings.TrimSpace(proxyURL) == "" {
		dialContext = (&net.Dialer{}).DialContext
	} else {
		dialer, mode, errDialer := proxyutil.BuildDialer(proxyURL)
		if errDialer != nil {
			return nil, fmt.Errorf("configure claude code node proxy: %w", errDialer)
		}
		if mode == proxyutil.ModeInherit || dialer == nil {
			dialContext = (&net.Dialer{}).DialContext
		} else {
			if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
				dialContext = contextDialer.DialContext
			} else {
				dialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				}
			}
		}
	}
	transport := reqclient.NewTransport()
	transport.EnableForceHTTP1()
	transport.Proxy = nil
	transport.DialContext = dialContext
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		rawConn, errDial := dialContext(ctx, network, addr)
		if errDial != nil {
			return nil, errDial
		}
		host, _, errHost := net.SplitHostPort(addr)
		if errHost != nil {
			host = addr
		}
		tlsConn := tls.UClient(rawConn, &tls.Config{ServerName: host}, tls.HelloCustom)
		if errPreset := tlsConn.ApplyPreset(claudeCodeNodeClientHelloSpec()); errPreset != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("apply claude code node tls preset: %w", errPreset)
		}
		if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("claude code node tls handshake: %w", errHandshake)
		}
		if protocol := tlsConn.ConnectionState().NegotiatedProtocol; protocol != "" && protocol != "http/1.1" {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("claude code node tls negotiated unsupported protocol %q", protocol)
		}
		return tlsConn, nil
	}
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 4
	transport.MaxConnsPerHost = 8
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 0
	transport.ResponseHeaderTimeout = 0
	transport.ExpectContinueTimeout = 0
	transport.DisableCompression = true
	if len(headerOrder) == 0 {
		headerOrder = ClaudeCodeNodeHeaderOrder()
	}
	return &claudeCodeNodeH1Transport{
		base:        transport,
		headerOrder: append([]string(nil), headerOrder...),
	}, nil
}
