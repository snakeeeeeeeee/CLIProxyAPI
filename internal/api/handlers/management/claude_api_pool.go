package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

func (h *Handler) GetClaudeAPIPoolConfig(c *gin.Context) {
	h.mu.Lock()
	enabled := false
	configPath := h.configFilePath
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusOK, gin.H{"enabled": false, "path": claudeapipool.DefaultDBFileName, "storage": "sqlite"})
		return
	}
	enabled = h.cfg.ClaudeAPIPool.Enabled
	cfg := h.cfg
	h.mu.Unlock()
	doc, err := claudeapipool.LoadStore(configPath, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":       enabled,
		"path":          claudeapipool.DefaultDBFileName,
		"import_path":   claudeapipool.DefaultFileName,
		"storage":       "sqlite",
		"virtual-cache": claudeapipool.EffectiveVirtualCache(doc.VirtualCache),
		"routing":       claudeapipool.EffectiveRouting(doc.Routing),
		"reuse-stats":   claudeapipool.VirtualCacheReuseStats(),
	})
}

func (h *Handler) PutClaudeAPIPoolConfig(c *gin.Context) {
	var body struct {
		Enabled      *bool                                      `json:"enabled"`
		VirtualCache *claudeapipool.EffectiveVirtualCacheConfig `json:"virtual-cache"`
		Routing      *claudeapipool.EffectiveRoutingConfig      `json:"routing"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not loaded"})
		return
	}
	if body.Enabled != nil {
		h.cfg.ClaudeAPIPool.Enabled = *body.Enabled
	}
	h.cfg.SanitizeClaudeAPIPool()
	if !h.persistLockedNoResponse(c) {
		h.mu.Unlock()
		return
	}
	configPath := h.configFilePath
	cfg := h.cfg
	h.mu.Unlock()
	if body.VirtualCache != nil || body.Routing != nil {
		doc, err := claudeapipool.LoadStore(configPath, cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
			return
		}
		if body.VirtualCache != nil {
			doc.VirtualCache = claudeapipool.VirtualCacheConfigFromEffective(*body.VirtualCache)
		}
		if body.Routing != nil {
			doc.Routing = claudeapipool.RoutingConfigFromEffective(*body.Routing)
		}
		if err := claudeapipool.SaveStore(configPath, cfg, doc); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
			return
		}
		claudeapipool.SetVirtualCacheConfig(claudeapipool.EffectiveVirtualCache(doc.VirtualCache))
		claudeapipool.SetRoutingConfig(claudeapipool.EffectiveRouting(doc.Routing))
	}
	if err := h.syncClaudeAPIPool(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        "ok",
		"enabled":       cfg.ClaudeAPIPool.Enabled,
		"path":          claudeapipool.DefaultDBFileName,
		"import_path":   claudeapipool.DefaultFileName,
		"storage":       "sqlite",
		"virtual-cache": claudeapipool.CurrentVirtualCacheConfig(),
		"routing":       claudeapipool.CurrentRoutingConfig(),
		"reuse-stats":   claudeapipool.VirtualCacheReuseStats(),
	})
}

func (h *Handler) GetClaudeAPIPoolItems(c *gin.Context) {
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	query := claudeapipool.ListQuery{
		Page:     parsePositiveInt(c.Query("page"), 1),
		PageSize: parsePositiveInt(c.Query("page_size"), 50),
		Q:        c.Query("q"),
		Status:   c.Query("status"),
		Model:    c.Query("model"),
		Runtime:  h.claudeAPIPoolRuntimeStatus(),
		AuthIDs:  h.claudeAPIPoolAuthIDs(),
	}
	c.JSON(http.StatusOK, claudeapipool.List(doc, query))
}

func (h *Handler) ExportClaudeAPIPool(c *gin.Context) {
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "yaml")))
	if format == "json" {
		data, errMarshal := json.MarshalIndent(doc, "", "  ")
		if errMarshal != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal_failed", "message": errMarshal.Error()})
			return
		}
		c.Header("Content-Type", "application/json; charset=utf-8")
		_, _ = c.Writer.Write(data)
		return
	}
	data, errMarshal := yaml.Marshal(doc)
	if errMarshal != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal_failed", "message": errMarshal.Error()})
		return
	}
	c.Header("Content-Type", "application/yaml; charset=utf-8")
	_, _ = c.Writer.Write(data)
}

func (h *Handler) ImportClaudeAPIPool(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
		Replace bool   `json:"replace"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	importDoc, err := claudeapipool.DecodeImportFile([]byte(body.Content))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_import", "message": err.Error()})
		return
	}
	if body.DryRun {
		c.JSON(http.StatusOK, gin.H{
			"count": len(importDoc.Items),
			"items": claudeapipool.List(importDoc, claudeapipool.ListQuery{Page: 1, PageSize: 50}).Items,
		})
		return
	}
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	imported, err := claudeapipool.ApplyImport(doc, []byte(body.Content), body.Replace)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_import", "message": err.Error()})
		return
	}
	if err := h.saveClaudeAPIPool(doc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	if err := h.syncClaudeAPIPool(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "imported": imported})
}

func (h *Handler) PatchClaudeAPIPoolItem(c *gin.Context) {
	position, ok := parsePositionParam(c)
	if !ok {
		return
	}
	var body struct {
		ItemHash string              `json:"item_hash"`
		Value    *claudeapipool.Item `json:"value"`
		Disabled *bool               `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	var resolved *claudeapipool.ResolvedItem
	switch {
	case body.Value != nil:
		resolved, err = claudeapipool.ReplaceItem(doc, position, body.ItemHash, *body.Value)
	case body.Disabled != nil:
		resolved, err = claudeapipool.SetDisabled(doc, position, body.ItemHash, *body.Disabled)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing value"})
		return
	}
	if err != nil {
		writePoolMutationError(c, err)
		return
	}
	if err := h.saveClaudeAPIPool(doc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	if err := h.syncClaudeAPIPool(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "item": claudeapipool.ToView(*resolved, h.claudeAPIPoolRuntimeStatus()[resolved.Position])})
}

func (h *Handler) PatchClaudeAPIPoolItemsBatch(c *gin.Context) {
	var body struct {
		Items    []claudeapipool.MutationRef `json:"items"`
		Disabled *bool                       `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Disabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing disabled"})
		return
	}
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	resolved, err := claudeapipool.SetDisabledBatch(doc, body.Items, *body.Disabled)
	if err != nil {
		writePoolMutationError(c, err)
		return
	}
	if err := h.saveClaudeAPIPool(doc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	if err := h.syncClaudeAPIPool(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync_failed", "message": err.Error()})
		return
	}
	runtime := h.claudeAPIPoolRuntimeStatus()
	items := make([]claudeapipool.ItemView, 0, len(resolved))
	for i := range resolved {
		items = append(items, claudeapipool.ToView(resolved[i], runtime[resolved[i].Position]))
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "items": items, "updated": len(items)})
}

func (h *Handler) DeleteClaudeAPIPoolItem(c *gin.Context) {
	position, ok := parsePositionParam(c)
	if !ok {
		return
	}
	itemHash := strings.TrimSpace(c.Query("item_hash"))
	if itemHash == "" {
		var body struct {
			ItemHash string `json:"item_hash"`
		}
		_ = c.ShouldBindJSON(&body)
		itemHash = strings.TrimSpace(body.ItemHash)
	}
	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	if err := claudeapipool.DeleteItem(doc, position, itemHash); err != nil {
		writePoolMutationError(c, err)
		return
	}
	if err := h.saveClaudeAPIPool(doc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	if err := h.syncClaudeAPIPool(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type claudeAPIPoolTestRequest struct {
	ItemHash string `json:"item_hash"`
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
}

type claudeAPIPoolTestResponse struct {
	Status     string            `json:"status"`
	StatusCode int               `json:"status_code"`
	Model      string            `json:"model"`
	Prompt     string            `json:"prompt"`
	Message    string            `json:"message,omitempty"`
	Body       string            `json:"body,omitempty"`
	DurationMS int64             `json:"duration_ms"`
	Headers    map[string]string `json:"headers,omitempty"`
}

func (h *Handler) TestClaudeAPIPoolItem(c *gin.Context) {
	position, ok := parsePositionParam(c)
	if !ok {
		return
	}
	var body claudeAPIPoolTestRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	doc, err := h.loadClaudeAPIPool()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	resolved, err := claudeAPIPoolResolveForMutation(doc, position, body.ItemHash)
	if err != nil {
		writePoolMutationError(c, err)
		return
	}

	result, err := h.testClaudeAPIPoolResolvedItem(c.Request.Context(), *resolved, body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "test_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) ResetClaudeAPIPoolCooling(c *gin.Context) {
	positionRaw := strings.TrimSpace(c.Param("position"))
	if positionRaw == "" {
		positionRaw = strings.TrimSpace(c.Query("position"))
	}
	position, _ := strconv.Atoi(positionRaw)
	if h.authManager == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	auths := h.authManager.List()
	for _, auth := range auths {
		if auth == nil || !claudeapipool.IsAttributesPoolAuth(auth.Attributes) {
			continue
		}
		if position > 0 && poolPosition(auth) != position {
			continue
		}
		resetAuthCooling(auth)
		claudeapipool.ResetRouteCooling(auth.ID)
		_, _ = h.authManager.Update(coreauth.WithSkipPersist(c.Request.Context()), auth)
	}
	if position <= 0 {
		claudeapipool.ResetRouteCooling("")
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) ClearClaudeAPIPoolLedger(c *gin.Context) {
	claudeapipool.ClearVirtualCacheLedger()
	c.JSON(http.StatusOK, gin.H{"status": "ok", "entries": 0})
}

func (h *Handler) loadClaudeAPIPool() (*claudeapipool.File, error) {
	cfg, configPath := h.claudeAPIPoolConfigContext()
	return claudeapipool.LoadStore(configPath, cfg)
}

func (h *Handler) saveClaudeAPIPool(doc *claudeapipool.File) error {
	cfg, configPath := h.claudeAPIPoolConfigContext()
	return claudeapipool.SaveStore(configPath, cfg, doc)
}

func (h *Handler) claudeAPIPoolConfigContext() (*config.Config, string) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil {
		cfg = &config.Config{}
	}
	return cfg, configPath
}

func claudeAPIPoolResolveForMutation(doc *claudeapipool.File, position int, expectedHash string) (*claudeapipool.ResolvedItem, error) {
	if doc == nil {
		return nil, fmt.Errorf("pool document is nil")
	}
	index := position - 1
	if index < 0 || index >= len(doc.Items) {
		return nil, fmt.Errorf("item not found")
	}
	expectedHash = strings.TrimSpace(expectedHash)
	if expectedHash == "" || !strings.EqualFold(claudeapipool.ItemHash(doc.Items[index]), expectedHash) {
		return nil, fmt.Errorf("item hash mismatch")
	}
	resolved := claudeapipool.ResolveOne(doc, index)
	if resolved == nil {
		return nil, fmt.Errorf("item not found")
	}
	return resolved, nil
}

func (h *Handler) testClaudeAPIPoolResolvedItem(ctx context.Context, item claudeapipool.ResolvedItem, body claudeAPIPoolTestRequest) (claudeAPIPoolTestResponse, error) {
	start := time.Now()
	model := claudeAPIPoolTestModel(item.Config.Models, body.Model)
	if model == "" {
		return claudeAPIPoolTestResponse{}, fmt.Errorf("model is required")
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "hi"
	}
	if item.Config.APIKey == "" {
		return claudeAPIPoolTestResponse{}, fmt.Errorf("api key is required")
	}
	endpoint, err := claudeMessagesEndpoint(item.Config.BaseURL)
	if err != nil {
		return claudeAPIPoolTestResponse{}, err
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": 16,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	payloadBytes, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return claudeAPIPoolTestResponse{}, errMarshal
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if errReq != nil {
		return claudeAPIPoolTestResponse{}, errReq
	}
	applyClaudeAPIPoolTestHeaders(req, item.Config.APIKey, item.Config.Headers)

	auth := &coreauth.Auth{
		Provider: "claude",
		ProxyURL: strings.TrimSpace(item.Config.ProxyURL),
		Attributes: map[string]string{
			"api_key":  item.Config.APIKey,
			"base_url": item.Config.BaseURL,
		},
	}
	client := &http.Client{
		Timeout:   defaultAPICallTimeout,
		Transport: h.apiCallTransport(auth),
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		return claudeAPIPoolTestResponse{}, errDo
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("claude api pool test response body close error: %v", errClose)
		}
	}()
	respBody, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return claudeAPIPoolTestResponse{}, errRead
	}

	status := "ok"
	message := extractClaudeAPIPoolTestMessage(respBody)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		status = "error"
		if message == "" {
			message = strings.TrimSpace(string(respBody))
		}
	}
	return claudeAPIPoolTestResponse{
		Status:     status,
		StatusCode: resp.StatusCode,
		Model:      model,
		Prompt:     prompt,
		Message:    message,
		Body:       string(respBody),
		DurationMS: time.Since(start).Milliseconds(),
		Headers:    claudeAPIPoolTestResponseHeaders(resp.Header),
	}, nil
}

func (h *Handler) syncClaudeAPIPool(ctx context.Context) error {
	if h == nil || h.claudeAPIPoolSync == nil {
		return nil
	}
	return h.claudeAPIPoolSync(ctx)
}

func (h *Handler) claudeAPIPoolAuthIDs() map[int]string {
	out := make(map[int]string)
	if h == nil || h.authManager == nil {
		return out
	}
	for _, auth := range h.authManager.List() {
		if auth == nil || !claudeapipool.IsAttributesPoolAuth(auth.Attributes) {
			continue
		}
		position := poolPosition(auth)
		if position <= 0 {
			continue
		}
		out[position] = auth.ID
	}
	return out
}

func (h *Handler) claudeAPIPoolRuntimeStatus() map[int]claudeapipool.RuntimeStatus {
	out := make(map[int]claudeapipool.RuntimeStatus)
	if h == nil || h.authManager == nil {
		return out
	}
	now := time.Now()
	for _, auth := range h.authManager.List() {
		if auth == nil || !claudeapipool.IsAttributesPoolAuth(auth.Attributes) {
			continue
		}
		position := poolPosition(auth)
		if position <= 0 {
			continue
		}
		status := claudeapipool.RuntimeStatus{
			AuthID:   auth.ID,
			Disabled: auth.Disabled || auth.Status == coreauth.StatusDisabled,
		}
		routeStatus := claudeapipool.AggregateRouteStatus(auth.ID)
		status.InFlight = routeStatus.InFlight
		status.RPMUsed = routeStatus.RPMUsed
		status.RPMLimit = routeStatus.RPMLimit
		if routeStatus.Cooling {
			status.Cooling = true
			status.CoolingTo = routeStatus.CoolingTo
		}
		for _, state := range auth.ModelStates {
			if state == nil {
				continue
			}
			next := state.NextRetryAfter
			if !state.Quota.NextRecoverAt.IsZero() && state.Quota.NextRecoverAt.After(next) {
				next = state.Quota.NextRecoverAt
			}
			if state.Unavailable && next.After(now) {
				status.Cooling = true
				if status.CoolingTo.IsZero() || next.After(status.CoolingTo) {
					status.CoolingTo = next
				}
			}
		}
		out[position] = status
	}
	return out
}

func parsePositiveInt(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parsePositionParam(c *gin.Context) (int, bool) {
	position, err := strconv.Atoi(strings.TrimSpace(c.Param("position")))
	if err != nil || position <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid position"})
		return 0, false
	}
	return position, true
}

func writePoolMutationError(c *gin.Context, err error) {
	msg := err.Error()
	if strings.Contains(msg, "hash mismatch") {
		c.JSON(http.StatusConflict, gin.H{"error": "item_hash_mismatch", "message": msg})
		return
	}
	if strings.Contains(msg, "not found") {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": msg})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": msg})
}

func poolPosition(auth *coreauth.Auth) int {
	if auth == nil || len(auth.Attributes) == 0 {
		return 0
	}
	position, _ := strconv.Atoi(strings.TrimSpace(auth.Attributes[claudeapipool.AttrPosition]))
	return position
}

func resetAuthCooling(auth *coreauth.Auth) {
	if auth == nil {
		return
	}
	auth.Unavailable = false
	auth.NextRetryAfter = time.Time{}
	auth.Quota = coreauth.QuotaState{}
	auth.Status = coreauth.StatusActive
	auth.StatusMessage = ""
	auth.LastError = nil
	for _, state := range auth.ModelStates {
		if state == nil {
			continue
		}
		state.Unavailable = false
		state.NextRetryAfter = time.Time{}
		state.Quota = coreauth.QuotaState{}
		state.Status = coreauth.StatusActive
		state.StatusMessage = ""
		state.LastError = nil
		state.UpdatedAt = time.Now()
	}
}

func (h *Handler) persistLockedNoResponse(c *gin.Context) bool {
	if err := config.SaveConfigPreserveComments(h.configFilePath, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return false
	}
	return true
}

func claudeAPIPoolTestModel(models []config.ClaudeModel, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for i := range models {
			name := strings.TrimSpace(models[i].Name)
			alias := strings.TrimSpace(models[i].Alias)
			if alias != "" && strings.EqualFold(alias, requested) {
				if name != "" {
					return name
				}
				return alias
			}
			if name != "" && strings.EqualFold(name, requested) {
				return name
			}
		}
		return requested
	}
	for i := range models {
		if name := strings.TrimSpace(models[i].Name); name != "" {
			return name
		}
		if alias := strings.TrimSpace(models[i].Alias); alias != "" {
			return alias
		}
	}
	return ""
}

func claudeMessagesEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	parsed, errParse := url.Parse(baseURL)
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base url")
	}
	switch {
	case strings.HasSuffix(baseURL, "/v1/messages"):
		return baseURL, nil
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/messages", nil
	default:
		return baseURL + "/v1/messages", nil
	}
}

func applyClaudeAPIPoolTestHeaders(req *http.Request, apiKey string, customHeaders map[string]string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range customHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if strings.EqualFold(key, "host") {
			req.Host = value
		}
		req.Header.Set(key, value)
	}
	if req.Header.Get("Anthropic-Version") == "" {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "https") && strings.EqualFold(req.URL.Host, "api.anthropic.com") {
		req.Header.Del("Authorization")
		req.Header.Set("x-api-key", apiKey)
		return
	}
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func extractClaudeAPIPoolTestMessage(body []byte) string {
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if parsed.Error.Message != "" {
		if parsed.Error.Type != "" {
			return parsed.Error.Type + ": " + parsed.Error.Message
		}
		return parsed.Error.Message
	}
	if parsed.Message != "" {
		return parsed.Message
	}
	for _, block := range parsed.Content {
		if strings.EqualFold(block.Type, "text") && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text)
		}
	}
	return ""
}

func claudeAPIPoolTestResponseHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, 3)
	for _, key := range []string{"request-id", "x-request-id", "anthropic-ratelimit-requests-reset"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
