package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type cachedClaudeTraceConfig struct {
	path    string
	modTime time.Time
	cfg     resourcepool.EffectiveTraceConfig
	loaded  time.Time
}

var claudeTraceConfigCache struct {
	sync.Mutex
	value cachedClaudeTraceConfig
}

func (e *ClaudeExecutor) dumpClaudeAccountPoolTrace(ctx context.Context, auth *cliproxyauth.Auth, meta map[string]any, req *http.Request, body []byte, stream bool, statusCode int, responseHeaders http.Header, responseErr error) {
	if e == nil || !isClaudeCodeAccountPoolTraceAuth(auth) || !isClaudeAccountPoolTraceScope(ctx, meta) {
		return
	}
	e.logClaudeAccountPoolOutboundShape(ctx, auth, req, body, stream, statusCode, responseErr)
	traceCfg, ok := e.effectiveClaudeTraceConfig(ctx)
	if !ok || !traceCfg.Enabled {
		return
	}
	errText := ""
	if responseErr != nil {
		errText = responseErr.Error()
	}
	requestPath := ""
	if req != nil && req.URL != nil {
		requestPath = req.URL.Path
	}
	trace := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{
		Source:            claudetrace.SourceOurs,
		RedactUserContent: traceCfg.RedactUserContent,
		RequestBody:       body,
		StatusCode:        statusCode,
		ResponseHeaders:   responseHeaders,
		ResponseError:     errText,
		Stream:            stream,
		RequestMode:       claudeAccountPoolTraceRequestMode(ctx, body),
		RequestKind:       claudetrace.InferRequestKind(requestPath, body),
		TLSProfile:        claudeAccountPoolTraceTLSProfile(req),
		TLSJA3:            helps.ClaudeCodeNodeTLSJA3,
		TLSJA4:            helps.ClaudeCodeNodeTLSJA4,
		TLSALPN:           helps.ClaudeCodeNodeTLSALPN,
		RawHeaderOrder:    helps.ClaudeCodeNodeHeaderOrderForAuth(auth),
	})
	if _, err := claudetrace.SaveTrace(traceCfg.DumpDir, trace); err != nil {
		helps.LogWithRequestID(ctx).WithError(err).Warn("claude account-pool trace dump failed")
	}
}

func (e *ClaudeExecutor) logClaudeAccountPoolOutboundShape(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request, body []byte, stream bool, statusCode int, responseErr error) {
	if e == nil || e.cfg == nil || !e.cfg.ResourcePools.Enabled || auth == nil {
		return
	}
	configPath := strings.TrimSpace(e.cfg.ResourcePools.ConfigFile)
	if configPath == "" {
		configPath = resourcepool.DefaultConfigFileName
	}
	shape := claudetrace.BuildBodyShape(body)
	session := claudetrace.CaptureRequest(req, claudetrace.CaptureOptions{RequestBody: body}).Session
	mode := claudeAccountPoolTraceRequestMode(ctx, body)
	headers := http.Header{}
	if req != nil {
		headers = req.Header
	}
	errText := ""
	if responseErr != nil {
		errText = responseErr.Error()
	}
	accountID := ""
	proxyID := ""
	if auth.Attributes != nil {
		accountID = auth.Attributes[resourcepool.AttrAccountID]
		proxyID = auth.Attributes[resourcepool.AttrProxyResourceID]
	}
	path := ""
	httpProtocol := ""
	if req != nil && req.URL != nil {
		path = req.URL.Path
		httpProtocol = req.Proto
	}
	entry := resourcepool.AccountPoolLogEntry{
		Level:           "debug",
		Event:           "outbound_shape",
		Path:            path,
		Model:           shape.Model,
		AccountID:       accountID,
		AuthID:          auth.ID,
		ProxyResourceID: proxyID,
		StatusCode:      statusCode,
		Error:           errText,
		Details: map[string]any{
			"mode":                   mode,
			"request_kind":           claudetrace.InferRequestKind(path, body),
			"profile_revision":       claudeCodeAccountPoolProfileFromAuth(auth).Revision,
			"stream":                 stream,
			"user_agent":             headers.Get("User-Agent"),
			"x_app":                  headers.Get("X-App"),
			"anthropic_version":      headers.Get("Anthropic-Version"),
			"anthropic_beta":         headers.Get("Anthropic-Beta"),
			"metadata_user_id_kind":  shape.MetadataUserIDKind,
			"system_block_count":     shape.SystemBlockCount,
			"billing_block_kind":     shape.BillingBlockKind,
			"billing_entrypoint":     shape.BillingEntrypoint,
			"cch_signed":             claudeAccountPoolTraceCCHSigned(body),
			"tls_profile":            claudeAccountPoolTraceTLSProfile(req),
			"tls_ja3":                helps.ClaudeCodeNodeTLSJA3,
			"tls_ja4":                helps.ClaudeCodeNodeTLSJA4,
			"tls_alpn":               helps.ClaudeCodeNodeTLSALPN,
			"session_identity_match": session.Match,
			"http_protocol":          httpProtocol,
			"tool_count":             shape.ToolCount,
			"has_thinking":           shape.HasThinking,
			"has_context_management": shape.HasContextManagement,
			"top_level_keys":         shape.TopLevelKeys,
		},
	}
	if err := resourcepool.WriteAccountPoolLog(ctx, configPath, e.cfg, entry); err != nil {
		helps.LogWithRequestID(ctx).WithError(err).Warn("claude account-pool outbound shape log failed")
	}
}

func claudeAccountPoolTraceCCHSigned(body []byte) bool {
	text := strings.TrimSpace(gjson.GetBytes(body, "system.0.text").String())
	return strings.HasPrefix(text, "x-anthropic-billing-header:") && strings.Contains(text, "cch=") && !strings.Contains(text, "cch=00000;")
}

func claudeAccountPoolTraceTLSProfile(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	if strings.EqualFold(req.URL.Scheme, "https") && strings.EqualFold(req.URL.Host, "api.anthropic.com") {
		return claudeAccountPoolTLSProfileNodeJS
	}
	return "default"
}

func claudeAccountPoolTraceRequestMode(ctx context.Context, body []byte) string {
	switch claudeCodeAccountPoolRequestMode(ctx, body) {
	case claudeAccountPoolModePassthrough:
		return claudetrace.RequestModeRealClaudeCodePassthrough
	default:
		return claudetrace.RequestModeAPIMimic
	}
}

func isClaudeCodeAccountPoolTraceAuth(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	return resourcepool.IsClaudeCodeAccountPoolAuth(auth.Attributes) ||
		strings.EqualFold(strings.TrimSpace(auth.Attributes[claudeapipool.AttrOAuthPool]), "true")
}

func isClaudeAccountPoolTraceScope(ctx context.Context, meta map[string]any) bool {
	if cliproxyauth.ClaudePoolScopeFromContext(ctx) == cliproxyexecutor.PoolScopeClaudeAccountPool {
		return true
	}
	if strings.HasPrefix(traceRequestPathFromContext(ctx), "/claude-acc-pool/v1") {
		return true
	}
	if strings.HasPrefix(traceMetadataString(meta, cliproxyexecutor.RequestPathMetadataKey), "/claude-acc-pool/v1") {
		return true
	}
	if meta == nil {
		return false
	}
	value, ok := meta[cliproxyexecutor.PoolScopeMetadataKey]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == cliproxyexecutor.PoolScopeClaudeAccountPool
	default:
		return strings.TrimSpace(strings.Trim(fmt.Sprint(typed), `"`)) == cliproxyexecutor.PoolScopeClaudeAccountPool
	}
}

func traceRequestPathFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil {
		if path := strings.TrimSpace(ginCtx.FullPath()); path != "" {
			return path
		}
		if ginCtx.Request != nil && ginCtx.Request.URL != nil {
			return strings.TrimSpace(ginCtx.Request.URL.Path)
		}
	}
	return ""
}

func traceMetadataString(meta map[string]any, key string) string {
	if meta == nil || key == "" {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func (e *ClaudeExecutor) effectiveClaudeTraceConfig(ctx context.Context) (resourcepool.EffectiveTraceConfig, bool) {
	var empty resourcepool.EffectiveTraceConfig
	if e == nil || e.cfg == nil || !e.cfg.ResourcePools.Enabled {
		return empty, false
	}
	configPath := strings.TrimSpace(e.cfg.ResourcePools.ConfigFile)
	if configPath == "" {
		configPath = resourcepool.DefaultConfigFileName
	}
	configPath = filepath.Clean(configPath)
	info, statErr := os.Stat(configPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		log.WithError(statErr).Warn("stat resource pools trace config failed")
	}

	claudeTraceConfigCache.Lock()
	defer claudeTraceConfigCache.Unlock()
	cached := claudeTraceConfigCache.value
	if cached.path == configPath && !cached.loaded.IsZero() {
		if errors.Is(statErr, os.ErrNotExist) && cached.modTime.IsZero() {
			return cached.cfg, true
		}
		if statErr == nil && cached.modTime.Equal(info.ModTime()) {
			return cached.cfg, true
		}
	}

	doc, err := resourcepool.LoadConfigFile(configPath)
	if err != nil {
		helps.LogWithRequestID(ctx).WithError(err).Warn("load resource pools trace config failed")
		return empty, false
	}
	traceCfg := resourcepool.EffectiveTrace(doc.Trace)
	if traceCfg.DumpDir != "" && !filepath.IsAbs(traceCfg.DumpDir) {
		traceCfg.DumpDir = filepath.Clean(traceCfg.DumpDir)
	}
	modTime := time.Time{}
	if statErr == nil {
		modTime = info.ModTime()
	}
	claudeTraceConfigCache.value = cachedClaudeTraceConfig{
		path:    configPath,
		modTime: modTime,
		cfg:     traceCfg,
		loaded:  time.Now(),
	}
	return traceCfg, true
}
