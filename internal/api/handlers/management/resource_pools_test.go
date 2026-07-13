package management

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestClaudeCodeAccountPoolConfigHandlersExposeInheritance(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	store, err := resourcepool.Open(configPath, cfg)
	if err != nil {
		t.Fatalf("resourcepool.Open() error = %v", err)
	}
	globalPure := false
	if _, err := store.SaveClaudeCodePoolConfig(context.Background(), resourcepool.ClaudeCodePoolConfig{
		PureMode: &globalPure,
		Routing:  claudeapipool.RoutingConfig{PerAccountRPM: 11, PerAccountConcurrency: 2},
	}); err != nil {
		_ = store.Close()
		t.Fatalf("SaveClaudeCodePoolConfig() error = %v", err)
	}
	pool, err := store.CreateAccountPool(context.Background(), "API Config", "")
	if err != nil {
		_ = store.Close()
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close resource pool store: %v", err)
	}

	gin.SetMode(gin.TestMode)
	handler := &Handler{cfg: cfg, configFilePath: configPath}
	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/account-pools/"+pool.ID+"/config", nil)
	getContext.Params = gin.Params{{Key: "id", Value: pool.ID}}
	handler.GetClaudeCodeAccountPoolConfig(getContext)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial resourcepool.ClaudeCodeAccountPoolConfigView
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if initial.Effective.Routing.PerAccountRPM != 11 || initial.Sources["routing.per_account_rpm"] != "global" {
		t.Fatalf("GET config view = %+v", initial)
	}

	patchRecorder := httptest.NewRecorder()
	patchContext, _ := gin.CreateTestContext(patchRecorder)
	patchContext.Request = httptest.NewRequest(http.MethodPatch, "/account-pools/"+pool.ID+"/config", strings.NewReader(`{"pure_mode":true,"routing":{"per_account_rpm":7}}`))
	patchContext.Params = gin.Params{{Key: "id", Value: pool.ID}}
	handler.PatchClaudeCodeAccountPoolConfig(patchContext)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	var overridden resourcepool.ClaudeCodeAccountPoolConfigView
	if err := json.Unmarshal(patchRecorder.Body.Bytes(), &overridden); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if !overridden.Effective.PureMode || overridden.Effective.Routing.PerAccountRPM != 7 || overridden.Sources["pure_mode"] != "pool" || overridden.Sources["routing.per_account_rpm"] != "pool" {
		t.Fatalf("PATCH config view = %+v", overridden)
	}

	resetRecorder := httptest.NewRecorder()
	resetContext, _ := gin.CreateTestContext(resetRecorder)
	resetContext.Request = httptest.NewRequest(http.MethodPatch, "/account-pools/"+pool.ID+"/config", strings.NewReader(`{"pure_mode":null,"routing":{"per_account_rpm":null}}`))
	resetContext.Params = gin.Params{{Key: "id", Value: pool.ID}}
	handler.PatchClaudeCodeAccountPoolConfig(resetContext)
	if resetRecorder.Code != http.StatusOK {
		t.Fatalf("reset PATCH status = %d, body=%s", resetRecorder.Code, resetRecorder.Body.String())
	}
	var reset resourcepool.ClaudeCodeAccountPoolConfigView
	if err := json.Unmarshal(resetRecorder.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset PATCH response: %v", err)
	}
	if reset.Effective.PureMode || reset.Effective.Routing.PerAccountRPM != 11 || reset.Sources["pure_mode"] != "global" || reset.Sources["routing.per_account_rpm"] != "global" {
		t.Fatalf("reset config view = %+v", reset)
	}
}

func TestGetClaudeCodeAccountPoolDiagnostics(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/claude-code-account-pool/diagnostics", nil)
	handler := &Handler{cfg: cfg, configFilePath: configPath}
	handler.GetClaudeCodeAccountPoolDiagnostics(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var diagnostics resourcepool.AccountPoolDiagnostics
	if err := json.Unmarshal(recorder.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if got := len(diagnostics.Database.InstanceFingerprint); got != 12 {
		t.Fatalf("database instance fingerprint length = %d, want 12", got)
	}
	if diagnostics.Profile.Revision == "" || diagnostics.Quota.SchedulerTick == "" {
		t.Fatalf("diagnostics missing runtime summaries: %+v", diagnostics)
	}
}

func TestGetClaudeCodePoolAPIKeySecret(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	store, err := resourcepool.Open(configPath, cfg)
	if err != nil {
		t.Fatalf("resourcepool.Open() error = %v", err)
	}
	credential, err := store.CreatePoolAPIKey(context.Background(), resourcepool.DefaultAccountPoolID, "client-a")
	if err != nil {
		_ = store.Close()
		t.Fatalf("CreatePoolAPIKey() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close resource pool store: %v", err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api-keys/"+credential.Item.ID+"/secret", nil)
	ctx.Params = gin.Params{{Key: "id", Value: credential.Item.ID}}
	handler := &Handler{cfg: cfg, configFilePath: configPath}
	handler.GetClaudeCodePoolAPIKeySecret(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var response struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode reveal response: %v", err)
	}
	if response.Secret != credential.Secret {
		t.Fatalf("secret = %q, want generated secret", response.Secret)
	}
}

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
	system, _ := testPayload["system"].([]any)
	if len(system) != 2 {
		t.Fatalf("test body system blocks = %d, want 2", len(system))
	}
	if got := system[1].(map[string]any)["cache_control"].(map[string]any)["ttl"]; got != "1h" {
		t.Fatalf("test body cache TTL = %#v, want 1h", got)
	}
	billing, _ := system[0].(map[string]any)["text"].(string)
	if !strings.Contains(billing, "cc_entrypoint=sdk-cli;") || strings.Contains(billing, "cch=") {
		t.Fatalf("billing = %q, want sdk-cli without CCH", billing)
	}
	if got, ok := testPayload["max_tokens"].(float64); !ok || int(got) != claudeCodeAccountTestMaxTokens {
		t.Fatalf("test body max_tokens = %#v, want %d", testPayload["max_tokens"], claudeCodeAccountTestMaxTokens)
	}
	testBodyLower := strings.ToLower(string(testBody))
	for _, sensitive := range []string{"denial-of-service", "supply-chain compromise", "malicious detection evasion"} {
		if strings.Contains(testBodyLower, sensitive) {
			t.Fatalf("account connectivity test includes security-sensitive static prompt text %q", sensitive)
		}
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
	calibrationSystem, _ := calibrationPayload["system"].([]any)
	if len(calibrationSystem) != 3 {
		t.Fatalf("calibration payload system blocks = %d, want 3", len(calibrationSystem))
	}
	for _, index := range []int{1, 2} {
		if got := calibrationSystem[index].(map[string]any)["cache_control"].(map[string]any)["ttl"]; got != "1h" {
			t.Fatalf("calibration system[%d] cache TTL = %#v, want 1h", index, got)
		}
	}
	calibrationBodyLower := strings.ToLower(string(calibrationBody))
	if !strings.Contains(calibrationBodyLower, "denial-of-service") {
		t.Fatal("calibration payload missing production static prompt")
	}
}

func TestClaudeCodeManagementBetasDoNotForceLongContext(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-8", "claude-haiku-4-5-20251001"} {
		t.Run(model, func(t *testing.T) {
			betas := strings.Join(claudeCodeManagementBetasForModel(model), ",")
			if strings.Contains(betas, "context-1m-2025-08-07") {
				t.Fatalf("management beta %q should not include long-context beta by default", betas)
			}
			if strings.Contains(betas, "advisor-tool-2026-03-01") || strings.Contains(betas, "effort-2025-11-24") {
				t.Fatalf("management beta %q includes unsupported default", betas)
			}
		})
	}
}

func TestClaudeCodeManagementOAuthBetasIncludeCredentialMarker(t *testing.T) {
	auth := &coreauth.Auth{Attributes: map[string]string{"auth_kind": "oauth"}}
	betas := claudeCodeManagementBetasForAuth(auth, "claude-sonnet-4-6")
	joined := strings.Join(betas, ",")
	if !strings.Contains(joined, "claude-code-20250219,oauth-2025-04-20") {
		t.Fatalf("OAuth betas = %q, want credential marker after claude-code", joined)
	}

	apiKeyAuth := &coreauth.Auth{Attributes: map[string]string{"auth_kind": "api_key"}}
	if joinedAPIKey := strings.Join(claudeCodeManagementBetasForAuth(apiKeyAuth, "claude-sonnet-4-6"), ","); strings.Contains(joinedAPIKey, "oauth-2025-04-20") {
		t.Fatalf("API key betas = %q, should not contain OAuth credential marker", joinedAPIKey)
	}
}

func TestExtractClaudeMessageTextSupportsJSONAndSSE(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "message JSON", body: `{"type":"message","content":[{"type":"text","text":"hello"}]}`, want: "hello"},
		{name: "nested message", body: `{"type":"message_start","message":{"content":[{"type":"text","text":"nested"}]}}`, want: "nested"},
		{name: "SSE deltas", body: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n", want: "hello"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractClaudeMessageText([]byte(test.body)); got != test.want {
				t.Fatalf("extractClaudeMessageText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSummarizeClaudeMessageResponse(t *testing.T) {
	body := []byte(`{"type":"message","stop_reason":"end_turn","content":[{"type":"thinking"}]}`)
	if got := summarizeClaudeMessageResponse(body); got != "type=message · stop_reason=end_turn · content=thinking" {
		t.Fatalf("summarizeClaudeMessageResponse() = %q", got)
	}
}

func TestRecordClaudeCodeAccountTestUsageUpdatesAvailability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	store, err := resourcepool.Open(filepath.Join(dir, "config.yaml"), cfg)
	if err != nil {
		t.Fatalf("resourcepool.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	account, err := store.RegisterClaudeCodeAccount(context.Background(), "test-auth", "test@example.com", "")
	if err != nil {
		t.Fatalf("register account: %v", err)
	}
	body := []byte(`{"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":11,"cache_creation_input_tokens":5}}`)
	recordClaudeCodeAccountTestUsage(context.Background(), store, account, "claude-opus-4-8", http.StatusOK, true, body, time.Now())

	availability, err := store.AccountAvailability(context.Background(), account.ID, time.Hour)
	if err != nil {
		t.Fatalf("AccountAvailability() error = %v", err)
	}
	if availability.RequestCount != 1 || availability.SuccessCount != 1 || availability.Status != "healthy" {
		t.Fatalf("availability = %#v, want one successful request", availability)
	}
	usage, err := store.UsageSummary(context.Background(), time.Hour, 10)
	if err != nil {
		t.Fatalf("UsageSummary() error = %v", err)
	}
	if usage.RequestCount != 1 || usage.InputTokens != 7 || usage.OutputTokens != 3 || usage.CacheReadTokens != 11 || usage.CacheCreationTokens != 5 || usage.RawTotalTokens != 26 {
		t.Fatalf("usage = %#v", usage)
	}
}
