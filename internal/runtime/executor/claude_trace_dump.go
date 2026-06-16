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
	traceCfg, ok := e.effectiveClaudeTraceConfig(ctx)
	if !ok || !traceCfg.Enabled {
		return
	}
	errText := ""
	if responseErr != nil {
		errText = responseErr.Error()
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
	})
	if _, err := claudetrace.SaveTrace(traceCfg.DumpDir, trace); err != nil {
		helps.LogWithRequestID(ctx).WithError(err).Warn("claude account-pool trace dump failed")
	}
}

func claudeAccountPoolTraceRequestMode(ctx context.Context, body []byte) string {
	if headers := ginRequestHeaders(ctx); headers != nil {
		if ua := strings.TrimSpace(headers.Get("User-Agent")); strings.HasPrefix(ua, "claude-cli/") {
			return claudetrace.RequestModeRealClaudeCodePassthrough
		}
	}
	shape := claudetrace.BuildBodyShape(body)
	if shape.BillingBlockKind != "" && shape.ToolCount >= 10 {
		return claudetrace.RequestModeRealClaudeCodePassthrough
	}
	return claudetrace.RequestModeAPIMimic
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
