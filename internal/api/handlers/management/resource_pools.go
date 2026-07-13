package management

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) openResourcePoolStore(c *gin.Context) (*resourcepool.Store, bool) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil || !cfg.ResourcePools.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource pools disabled"})
		return nil, false
	}
	store, err := resourcepool.Open(configPath, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "open_failed", "message": err.Error()})
		return nil, false
	}
	return store, true
}

func (h *Handler) GetResourcePoolConfig(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	summary, err := store.Summary(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summary_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":      true,
		"storage":      "sqlite",
		"path":         store.Path(),
		"proxy_health": resourcepool.EffectiveProxyHealth(doc.ProxyHealth),
		"claude_code":  resourcepool.EffectiveClaudeCodePool(doc.ClaudeCode),
		"summary":      summary,
	})
}

func (h *Handler) ListClaudeCodeAccountPools(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	items, err := store.ListAccountPools(c.Request.Context(), strings.EqualFold(strings.TrimSpace(c.Query("include_archived")), "true"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	insights, err := store.AccountPoolInsights(c.Request.Context(), time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insights_failed", "message": err.Error()})
		return
	}
	baseQuery := summaryUsageQuery(parseUsageQuery(c))
	usage, err := store.UsageSummaryQuery(c.Request.Context(), baseQuery)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summary_failed", "message": err.Error()})
		return
	}
	usageByPool := make(map[string]resourcepool.UsageSummaryItem, len(usage.ByPool))
	for _, item := range usage.ByPool {
		usageByPool[item.Key] = item
	}
	for index := range items {
		attachAccountPoolSummary(&items[index], insights.Pools[items[index].ID], usageByPool[items[index].ID])
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetClaudeCodeAccountPool(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.GetAccountPool(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "get_failed", "message": err.Error()})
		return
	}
	insights, err := store.AccountPoolInsights(c.Request.Context(), time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insights_failed", "message": err.Error()})
		return
	}
	query := summaryUsageQuery(parseUsageQuery(c))
	query.PoolID = item.ID
	usage, err := store.UsageSummaryQuery(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summary_failed", "message": err.Error()})
		return
	}
	attachAccountPoolSummary(item, insights.Pools[item.ID], usageSummaryItem(usage))
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func attachAccountPoolSummary(pool *resourcepool.ClaudeCodeAccountPool, insights resourcepool.AccountPoolOperationalInsights, usage resourcepool.UsageSummaryItem) {
	if pool == nil {
		return
	}
	summary := &resourcepool.AccountPoolSummary{
		AccountCount:         insights.AccountCount,
		HealthyAccountCount:  insights.HealthyAccountCount,
		APIKeyCount:          insights.APIKeyCount,
		RequestCount:         usage.RequestCount,
		AttemptCount:         usage.AttemptCount,
		SuccessRate:          usage.SuccessRate,
		RawTotalTokens:       usage.RawTotalTokens,
		EstimatedCost:        usage.EstimatedCost,
		UnpricedRequestCount: usage.UnpricedRequestCount,
		PricingCoverage:      usage.PricingCoverage,
		Health:               insights.Health,
		ModelCapacity:        insights.ModelCapacity,
	}
	if summary.RequestCount == 0 {
		summary.PricingCoverage = 100
	}
	pool.Summary = summary
}

func summaryUsageQuery(query resourcepool.UsageQuery) resourcepool.UsageQuery {
	query.PoolID = ""
	query.AccountID = ""
	query.APIKeyID = ""
	query.Model = ""
	query.Limit = 500
	query.Offset = 0
	return query
}

func usageSummaryItem(summary resourcepool.UsageSummary) resourcepool.UsageSummaryItem {
	return resourcepool.UsageSummaryItem{
		RequestCount:         summary.RequestCount,
		AttemptCount:         summary.AttemptCount,
		SuccessCount:         summary.SuccessCount,
		FailureCount:         summary.FailureCount,
		SuccessRate:          summary.SuccessRate,
		InputTokens:          summary.InputTokens,
		OutputTokens:         summary.OutputTokens,
		CacheReadTokens:      summary.CacheReadTokens,
		CacheCreationTokens:  summary.CacheCreationTokens,
		CacheCreation5m:      summary.CacheCreation5m,
		CacheCreation1h:      summary.CacheCreation1h,
		RawInputTokens:       summary.RawInputTokens,
		RawTotalTokens:       summary.RawTotalTokens,
		EstimatedCost:        summary.EstimatedCost,
		UnpricedRequestCount: summary.UnpricedRequestCount,
		PricingCoverage:      summary.PricingCoverage,
	}
}

func (h *Handler) CreateClaudeCodeAccountPool(c *gin.Context) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "invalid request body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.CreateAccountPool(c.Request.Context(), body.Name, body.Description)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "create_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishPoolChanged(item.ID, "create")
	resourcepool.PublishStatsChanged("account_pool")
	c.JSON(http.StatusCreated, gin.H{"item": item})
}

func (h *Handler) PatchClaudeCodeAccountPool(c *gin.Context) {
	var body resourcepool.ClaudeCodeAccountPoolPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "invalid request body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.PatchAccountPool(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "update_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishPoolChanged(item.ID, "update")
	resourcepool.PublishStatsChanged("account_pool")
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) DeleteClaudeCodeAccountPool(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	id := strings.TrimSpace(c.Param("id"))
	if err := store.ArchiveAccountPool(c.Request.Context(), id); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		} else if errors.Is(err, resourcepool.ErrAccountPoolNotEmpty) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": "archive_failed", "message": err.Error()})
		return
	}
	claudeapipool.RemoveScopedRoutingScope(resourcepool.AccountRoutingScope(id))
	resourcepool.PublishPoolChanged(id, "archive")
	resourcepool.PublishStatsChanged("account_pool")
	c.JSON(http.StatusOK, gin.H{"status": "archived"})
}

func (h *Handler) ListClaudeCodePoolAPIKeys(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	items, err := store.ListPoolAPIKeys(c.Request.Context(), strings.TrimSpace(c.Query("pool_id")), strings.EqualFold(strings.TrimSpace(c.Query("include_revoked")), "true"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	usageQuery := parseUsageQuery(c)
	usageQuery.PoolID = strings.TrimSpace(c.Query("pool_id"))
	usageQuery.Limit = 1
	usage, err := store.UsageSummaryQuery(c.Request.Context(), usageQuery)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage_failed", "message": err.Error()})
		return
	}
	usageByKey := make(map[string]*resourcepool.UsageSummaryItem, len(usage.ByAPIKey))
	for index := range usage.ByAPIKey {
		item := usage.ByAPIKey[index]
		usageByKey[item.Key] = &item
	}
	for index := range items {
		items[index].Usage = usageByKey[items[index].ID]
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) CreateClaudeCodePoolAPIKey(c *gin.Context) {
	var body struct {
		PoolID string `json:"pool_id"`
		Name   string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "invalid request body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	credential, err := store.CreatePoolAPIKey(c.Request.Context(), body.PoolID, body.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "create_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishAPIKeyChanged(credential.Item.ID, "create")
	resourcepool.PublishStatsChanged("api_key")
	c.JSON(http.StatusCreated, credential)
}

func (h *Handler) PatchClaudeCodePoolAPIKey(c *gin.Context) {
	var body resourcepool.ClaudeCodePoolAPIKeyPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "invalid request body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.PatchPoolAPIKey(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "update_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishAPIKeyChanged(item.ID, "update")
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) GetClaudeCodePoolAPIKeySecret(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	secret, err := store.GetPoolAPIKeySecret(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		code := "secret_failed"
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, resourcepool.ErrPoolAPIKeyRevoked):
			status = http.StatusConflict
			code = "key_revoked"
		case errors.Is(err, resourcepool.ErrPoolAPIKeySecretUnavailable):
			status = http.StatusConflict
			code = "secret_unavailable"
		}
		c.JSON(status, gin.H{"error": code, "message": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, gin.H{"secret": secret})
}

func (h *Handler) DeleteClaudeCodePoolAPIKey(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	id := strings.TrimSpace(c.Param("id"))
	if err := store.RevokePoolAPIKey(c.Request.Context(), id); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "revoke_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishAPIKeyChanged(id, "revoke")
	resourcepool.PublishStatsChanged("api_key")
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

func (h *Handler) RotateClaudeCodePoolAPIKey(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	credential, err := store.RotatePoolAPIKey(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "rotate_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishAPIKeyChanged(credential.Item.ID, "rotate")
	c.JSON(http.StatusOK, credential)
}

func (h *Handler) GetClaudeCodePoolConfig(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	effective := resourcepool.EffectiveClaudeCodePool(doc.ClaudeCode)
	c.JSON(http.StatusOK, gin.H{
		"raw":       doc.ClaudeCode,
		"effective": effective,
		"storage":   "sqlite",
		"path":      store.Path(),
	})
}

func (h *Handler) GetClaudeCodeAccountPoolDiagnostics(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	h.mu.Lock()
	cfg := h.cfg
	h.mu.Unlock()
	diagnostics, err := store.Diagnostics(c.Request.Context(), cfg, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "diagnostics_failed", "message": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, diagnostics)
}

func (h *Handler) PutClaudeCodePoolConfig(c *gin.Context) {
	var body resourcepool.ClaudeCodePoolConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.SaveClaudeCodePoolConfig(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	effective := resourcepool.EffectiveClaudeCodePool(doc.ClaudeCode)
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishConfigChanged("save")
	resourcepool.PublishStatsChanged("config")
	c.JSON(http.StatusOK, gin.H{"raw": doc.ClaudeCode, "effective": effective})
}

func (h *Handler) GetClaudeCodeAccountPoolConfig(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	view, err := store.GetAccountPoolConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handler) PatchClaudeCodeAccountPoolConfig(c *gin.Context) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "account-pool config patch is required"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	view, err := store.PatchAccountPoolConfig(c.Request.Context(), c.Param("id"), json.RawMessage(raw))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishPoolChanged(c.Param("id"), "config")
	resourcepool.PublishStatsChanged("pool_config")
	c.JSON(http.StatusOK, view)
}

func (h *Handler) GetClaudeCodeProfile(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"raw":       doc.Profile,
		"effective": resourcepool.EffectiveClaudeCodeProfile(doc.Profile),
	})
}

func (h *Handler) PutClaudeCodeProfile(c *gin.Context) {
	c.JSON(http.StatusMethodNotAllowed, gin.H{
		"error":     "profile_locked",
		"message":   "Claude Code request profile is built in and not editable at runtime",
		"effective": resourcepool.EffectiveClaudeCodeProfile(resourcepool.ClaudeCodeProfile{}),
	})
}

func (h *Handler) ListClaudeCodeProfileSnapshots(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	items, err := store.ListClaudeCodeProfileSnapshots(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetClaudeCodeProfileSnapshot(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.GetClaudeCodeProfileSnapshot(c.Request.Context(), c.Param("id"))
	if err != nil {
		if resourcepool.IsProfileSnapshotNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) FetchClaudeCodeProfileSnapshot(c *gin.Context) {
	var body resourcepool.ClaudeCodeProfileSnapshotFetchRequest
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	item, err := store.FetchClaudeCodeProfileSnapshot(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "fetch_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishConfigChanged("profile_snapshot_fetch")
	c.JSON(http.StatusOK, gin.H{"item": item})
}

func (h *Handler) DiffClaudeCodeProfileSnapshot(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	diff, err := store.RefreshClaudeCodeProfileSnapshotDiff(c.Request.Context(), c.Param("id"))
	if err != nil {
		if resourcepool.IsProfileSnapshotNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "diff_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishConfigChanged("profile_snapshot_diff")
	c.JSON(http.StatusOK, gin.H{"diff": diff})
}

func (h *Handler) PromoteClaudeCodeProfileSnapshot(c *gin.Context) {
	c.JSON(http.StatusMethodNotAllowed, gin.H{
		"error":   "profile_snapshot_reference_only",
		"message": "Profile snapshots are reference-only and cannot be applied to production traffic",
	})
}

func (h *Handler) ListProxyResources(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	proxies, err := store.ListProxies(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": proxies})
}

func (h *Handler) ListAvailableProxyResources(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	proxies, err := store.ListAvailableProxies(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": proxies})
}

func (h *Handler) CreateProxyResource(c *gin.Context) {
	var body resourcepool.ProxyResourceSeed
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	proxy, err := store.CreateProxy(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "create_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishProxyChanged(proxy.ID, "create")
	c.JSON(http.StatusOK, gin.H{"item": proxy})
}

func (h *Handler) ImportProxyResources(c *gin.Context) {
	var body struct {
		Text  string                           `json:"text"`
		Items []resourcepool.ProxyResourceSeed `json:"items"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	seeds := body.Items
	if strings.TrimSpace(body.Text) != "" {
		seeds = append(seeds, resourcepool.ParseProxyImport(body.Text)...)
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	result, err := store.ImportProxies(c.Request.Context(), seeds)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "import_failed", "message": err.Error()})
		return
	}
	if result.Created > 0 {
		resourcepool.PublishProxyChanged("", "import")
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) BatchProxyResources(c *gin.Context) {
	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	if action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action is required"})
		return
	}
	ids := dedupeTrimmedStrings(body.IDs)
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids are required"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, errConfig := store.GetConfig(c.Request.Context())
	healthCfg := resourcepool.EffectiveProxyHealthConfig{}
	if errConfig == nil {
		healthCfg = resourcepool.EffectiveProxyHealth(doc.ProxyHealth)
	}
	result := gin.H{
		"action": action,
		"total":  len(ids),
		"ok":     0,
		"failed": 0,
		"errors": []gin.H{},
	}
	errorsOut := make([]gin.H, 0)
	successCount := 0
	for _, id := range ids {
		if err := h.applyProxyBatchAction(c.Request.Context(), store, action, id, healthCfg); err != nil {
			errorsOut = append(errorsOut, gin.H{"id": id, "message": err.Error()})
			continue
		}
		successCount++
	}
	result["ok"] = successCount
	result["failed"] = len(ids) - successCount
	result["errors"] = errorsOut
	if action == "enable" || action == "disable" || action == "unbind" || action == "delete" {
		h.triggerConfigReload(c.Request.Context())
	}
	if successCount > 0 {
		resourcepool.PublishProxyChanged("", "batch_"+action)
		if action == "unbind" || action == "delete" {
			resourcepool.PublishAccountChanged("", "proxy_"+action)
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) applyProxyBatchAction(ctx context.Context, store *resourcepool.Store, action, id string, healthCfg resourcepool.EffectiveProxyHealthConfig) error {
	switch action {
	case "test":
		_, err := resourcepool.TestProxyAndStore(ctx, store, id, healthCfg)
		return err
	case "enable":
		enabled := true
		_, err := store.UpdateProxy(ctx, id, resourcepool.ProxyPatch{Enabled: &enabled})
		return err
	case "disable":
		enabled := false
		_, err := store.UpdateProxy(ctx, id, resourcepool.ProxyPatch{Enabled: &enabled})
		return err
	case "unbind":
		return store.UnbindProxy(ctx, id)
	case "delete":
		return store.DeleteProxy(ctx, id)
	default:
		return errors.New("unsupported batch action")
	}
}

func dedupeTrimmedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func (h *Handler) PatchProxyResource(c *gin.Context) {
	var body resourcepool.ProxyPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	proxy, err := store.UpdateProxy(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "update_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishProxyChanged(proxy.ID, "update")
	c.JSON(http.StatusOK, gin.H{"item": proxy})
}

func (h *Handler) DeleteProxyResource(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	if err := store.DeleteProxy(c.Request.Context(), c.Param("id")); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "delete_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishProxyChanged(c.Param("id"), "delete")
	resourcepool.PublishAccountChanged("", "proxy_delete")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) TestProxyResource(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_failed", "message": err.Error()})
		return
	}
	result, err := resourcepool.TestProxyAndStore(c.Request.Context(), store, c.Param("id"), resourcepool.EffectiveProxyHealth(doc.ProxyHealth))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"item": result, "warning": err.Error()})
		return
	}
	resourcepool.PublishProxyChanged(c.Param("id"), "test")
	c.JSON(http.StatusOK, gin.H{"item": result})
}

func (h *Handler) UnbindProxyResource(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	if err := store.UnbindProxy(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unbind_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishProxyChanged(c.Param("id"), "unbind")
	resourcepool.PublishAccountChanged("", "proxy_unbind")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) ListClaudeCodeAccounts(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	accounts, err := store.ListAccountsByPool(c.Request.Context(), strings.TrimSpace(c.Query("pool_id")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	usageQuery := parseUsageQuery(c)
	usageQuery.PoolID = strings.TrimSpace(c.Query("pool_id"))
	usageQuery.Limit = 1
	usage, err := store.UsageSummaryQuery(c.Request.Context(), usageQuery)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage_failed", "message": err.Error()})
		return
	}
	usageByAccount := make(map[string]*resourcepool.UsageSummaryItem, len(usage.ByAccount))
	for index := range usage.ByAccount {
		item := usage.ByAccount[index]
		usageByAccount[item.Key] = &item
	}
	authEntries := h.authEntryByID()
	items := make([]gin.H, 0, len(accounts))
	for _, account := range accounts {
		account.Usage = usageByAccount[account.ID]
		entry := gin.H{"account": account}
		if authEntry, ok := authEntries[account.AuthID]; ok {
			entry["runtime"] = authEntry
		}
		items = append(items, entry)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) GetClaudeCodePoolStats(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	usageQuery := parseUsageQuery(c)
	insights, err := store.AccountPoolInsights(c.Request.Context(), time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insights_failed", "message": err.Error()})
		return
	}
	operational := insights.Global
	if usageQuery.PoolID != "" {
		operational = insights.Pools[usageQuery.PoolID]
	}
	stats := resourcepool.AccountPoolStats{
		AccountCount:      operational.AccountCount,
		AvailableAccounts: operational.AvailableAccounts,
		CoolingAccounts:   operational.CoolingAccounts,
		InFlight:          operational.InFlight,
		RPMUsed:           operational.RPMUsed,
		RPMLimit:          operational.RPMLimit,
		Health:            operational.Health,
		ModelCapacity:     operational.ModelCapacity,
	}
	if usageQuery.PoolID == "" {
		distribution := insights.Distribution
		stats.PoolHealthDistribution = &distribution
	}
	if !usageQuery.AllTime {
		stats.WindowSeconds = int64(usageQuery.Window.Seconds())
	}
	if usageQuery.PoolID != "" {
		activeKeys, warmLanes := claudeapipool.ScopedAccountAffinityStats(resourcepool.AccountRoutingScope(usageQuery.PoolID))
		stats.ActiveAffinityKeys = activeKeys
		stats.WarmLanes = warmLanes
	} else {
		pools, errPools := store.ListAccountPools(c.Request.Context(), false)
		if errPools != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": errPools.Error()})
			return
		}
		for _, pool := range pools {
			activeKeys, warmLanes := claudeapipool.ScopedAccountAffinityStats(resourcepool.AccountRoutingScope(pool.ID))
			stats.ActiveAffinityKeys += activeKeys
			stats.WarmLanes += warmLanes
		}
	}
	usageQuery.Limit = 20
	usage, errUsage := store.UsageSummaryQuery(c.Request.Context(), usageQuery)
	if errUsage != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage_failed", "message": errUsage.Error()})
		return
	}
	stats.RequestCount = usage.RequestCount
	stats.AttemptCount = usage.AttemptCount
	stats.SuccessCount = usage.SuccessCount
	stats.FailureCount = usage.FailureCount
	stats.SuccessRate = usage.SuccessRate
	stats.InputTokens = usage.InputTokens
	stats.OutputTokens = usage.OutputTokens
	stats.CacheReadTokens = usage.CacheReadTokens
	stats.CacheCreationTokens = usage.CacheCreationTokens
	stats.RawInputTokens = usage.RawInputTokens
	stats.RawTotalTokens = usage.RawTotalTokens
	stats.EstimatedCost = usage.EstimatedCost
	stats.UnpricedRequestCount = usage.UnpricedRequestCount
	stats.PricingCoverage = usage.PricingCoverage
	if totalCache := usage.CacheReadTokens + usage.CacheCreationTokens; totalCache > 0 {
		stats.RealCacheRatio = float64(usage.CacheReadTokens) * 100 / float64(totalCache)
	}
	rejects, errRejects := store.CountLocalRoutingRejectsQuery(c.Request.Context(), usageQuery.Window, usageQuery.AllTime, usageQuery.PoolID)
	if errRejects != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "routing_failed", "message": errRejects.Error()})
		return
	}
	stats.LocalRejectCount = rejects
	recentErrors, errErrors := store.ListRecentRoutingErrorsByPool(c.Request.Context(), usageQuery.PoolID, 10)
	if errErrors != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "routing_failed", "message": errErrors.Error()})
		return
	}
	stats.RecentErrors = recentErrors
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (h *Handler) GetClaudeCodePoolLogConfig(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	effective := resourcepool.EffectiveAccountPoolLog(doc.ClaudeCode.Log)
	c.JSON(http.StatusOK, gin.H{"raw": doc.ClaudeCode.Log, "effective": effective})
}

func (h *Handler) PutClaudeCodePoolLogConfig(c *gin.Context) {
	var body resourcepool.AccountPoolLogConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.SaveClaudeCodePoolLogConfig(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishConfigChanged("account_pool_log")
	resourcepool.PublishStatsChanged("account_pool_log")
	c.JSON(http.StatusOK, gin.H{"raw": doc.ClaudeCode.Log, "effective": resourcepool.EffectiveAccountPoolLog(doc.ClaudeCode.Log)})
}

func (h *Handler) ListClaudeCodePoolLogs(c *gin.Context) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil || !cfg.ResourcePools.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource pools disabled"})
		return
	}
	logs, err := resourcepool.ReadAccountPoolLogs(c.Request.Context(), configPath, cfg, parsePositiveQueryInt(c, "limit", 200))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": logs})
}

func (h *Handler) ClearClaudeCodePoolLogs(c *gin.Context) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil || !cfg.ResourcePools.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource pools disabled"})
		return
	}
	if err := resourcepool.ClearAccountPoolLogs(c.Request.Context(), configPath, cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "clear_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishStatsChanged("account_pool_logs")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) DownloadClaudeCodePoolLog(c *gin.Context) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil || !cfg.ResourcePools.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource pools disabled"})
		return
	}
	path, err := resourcepool.AccountPoolLogFilePath(c.Request.Context(), configPath, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "path_failed", "message": err.Error()})
		return
	}
	if strings.TrimSpace(path) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "log_not_configured"})
		return
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "log_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stat_failed", "message": err.Error()})
		return
	}
	c.FileAttachment(path, "account-pool.log")
}

func (h *Handler) ListClaudeCodeRoutingEvents(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	query := parseUsageQuery(c)
	events, err := store.ListRoutingEventsQuery(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	total, err := store.CountRoutingEventsQuery(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "count_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": events, "total": total, "limit": query.Limit, "offset": query.Offset})
}

func (h *Handler) GetClaudeCodeUsageSummary(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	summary, err := store.UsageSummaryQuery(c.Request.Context(), parseUsageQuery(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "usage_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

func (h *Handler) ListClaudeCodeUsageCalibrations(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	doc, err := store.GetConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_failed", "message": err.Error()})
		return
	}
	effective := resourcepool.EffectiveClaudeCodePool(doc.ClaudeCode)
	fingerprint := effective.Usage.ProfileFingerprint
	rows, err := store.ListUsageCalibrations(c.Request.Context(), fingerprint)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	byModel := map[string]resourcepool.UsageCalibration{}
	for _, row := range rows {
		byModel[strings.TrimSpace(row.Model)] = row
	}
	models, _ := store.ListModels(c.Request.Context(), true)
	items := make([]resourcepool.UsageCalibrationView, 0, len(models)+len(rows))
	seen := map[string]bool{}
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		seen[name] = true
		if calibration, ok := byModel[name]; ok && calibration.Status == resourcepool.UsageCalibrationCalibrated {
			items = append(items, resourcepool.UsageCalibrationView{
				UsageCalibration:        calibration,
				EffectiveOverheadTokens: calibration.OverheadTokens,
				Estimated:               false,
			})
			continue
		}
		view := resourcepool.UsageCalibrationView{
			UsageCalibration: resourcepool.UsageCalibration{
				Model:              name,
				ProfileFingerprint: fingerprint,
				OverheadTokens:     effective.Usage.SystemPromptOverheadTokens,
				Status:             resourcepool.UsageCalibrationEstimated,
			},
			EffectiveOverheadTokens: effective.Usage.SystemPromptOverheadTokens,
			Estimated:               true,
		}
		if calibration, ok := byModel[name]; ok {
			view.UsageCalibration = calibration
			view.EffectiveOverheadTokens = effective.Usage.SystemPromptOverheadTokens
			view.Estimated = calibration.Status != resourcepool.UsageCalibrationCalibrated
		}
		items = append(items, view)
	}
	for _, row := range rows {
		if seen[strings.TrimSpace(row.Model)] {
			continue
		}
		items = append(items, resourcepool.UsageCalibrationView{
			UsageCalibration:        row,
			EffectiveOverheadTokens: row.OverheadTokens,
			Estimated:               row.Status != resourcepool.UsageCalibrationCalibrated,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"profile_fingerprint": fingerprint,
		"default_overhead":    effective.Usage.SystemPromptOverheadTokens,
		"pure_mode":           effective.PureMode,
		"clean_input_tokens":  effective.Usage.CleanInputTokens,
		"items":               items,
	})
}

func (h *Handler) CalibrateClaudeCodeUsage(c *gin.Context) {
	var body struct {
		Model     string `json:"model"`
		AccountID string `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	calibration, err := h.calibrateClaudeCodeUsage(c.Request.Context(), store, model, body.AccountID)
	if err != nil {
		resourcepool.PublishStatsChanged("usage_calibration")
		c.JSON(http.StatusOK, gin.H{"item": calibration, "warning": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishStatsChanged("usage_calibration")
	c.JSON(http.StatusOK, gin.H{"item": calibration})
}

func (h *Handler) calibrateClaudeCodeUsage(ctx context.Context, store *resourcepool.Store, model, accountID string) (*resourcepool.UsageCalibration, error) {
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	effective := resourcepool.EffectiveClaudeCodePool(doc.ClaudeCode)
	fingerprint := effective.Usage.ProfileFingerprint
	account, err := h.pickClaudeCodeCalibrationAccount(ctx, store, accountID)
	if err != nil {
		failed, saveErr := store.UpsertUsageCalibration(ctx, resourcepool.UsageCalibration{
			Model:              model,
			ProfileFingerprint: fingerprint,
			Status:             resourcepool.UsageCalibrationFailed,
			OverheadTokens:     effective.Usage.SystemPromptOverheadTokens,
			LastError:          err.Error(),
		})
		if saveErr != nil {
			return failed, saveErr
		}
		return failed, err
	}
	auth, err := h.storedClaudeCodeAuth(ctx, account.ID)
	if err != nil {
		return h.saveFailedUsageCalibration(ctx, store, model, fingerprint, effective.Usage.SystemPromptOverheadTokens, err)
	}
	if err := h.ensureClaudeCodeManagementAccessToken(ctx, store, auth); err != nil {
		return h.saveFailedUsageCalibration(ctx, store, model, fingerprint, effective.Usage.SystemPromptOverheadTokens, err)
	}
	count, err := h.countClaudeCodeUsageCalibrationTokens(ctx, auth, model)
	if err != nil {
		return h.saveFailedUsageCalibration(ctx, store, model, fingerprint, effective.Usage.SystemPromptOverheadTokens, err)
	}
	overhead := count - 1
	if overhead < 0 {
		overhead = 0
	}
	return store.UpsertUsageCalibration(ctx, resourcepool.UsageCalibration{
		Model:              model,
		ProfileFingerprint: fingerprint,
		OverheadTokens:     overhead,
		Status:             resourcepool.UsageCalibrationCalibrated,
	})
}

func (h *Handler) pickClaudeCodeCalibrationAccount(ctx context.Context, store *resourcepool.Store, accountID string) (*resourcepool.ClaudeCodeAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID != "" {
		account, err := store.GetAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if !account.Enabled || !account.HasAuthData {
			return nil, errors.New("selected account is disabled or missing auth data")
		}
		return account, nil
	}
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].Enabled && accounts[i].HasAuthData {
			return &accounts[i], nil
		}
	}
	return nil, errors.New("no enabled claude code account available for calibration")
}

func (h *Handler) countClaudeCodeUsageCalibrationTokens(ctx context.Context, auth *coreauth.Auth, model string) (int64, error) {
	userID := claudeCodeManagementUserID(auth)
	body, err := buildClaudeCodeUsageCalibrationBody(model, userID)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages/count_tokens?beta=true", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	applyClaudeCodeManagementHeaders(req, auth, false, userID, model)
	resp, err := h.authManager.HttpRequest(ctx, auth, req)
	if err != nil {
		return 0, err
	}
	responseBody, err := readClaudeCodeManagementResponseBody(resp, 2<<20)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, errors.New(strings.TrimSpace(string(responseBody)))
	}
	var payloadOut struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := json.Unmarshal(responseBody, &payloadOut); err != nil {
		return 0, err
	}
	if payloadOut.InputTokens <= 0 {
		return 0, errors.New("count_tokens response missing input_tokens")
	}
	return payloadOut.InputTokens, nil
}

func (h *Handler) saveFailedUsageCalibration(ctx context.Context, store *resourcepool.Store, model, fingerprint string, defaultOverhead int64, err error) (*resourcepool.UsageCalibration, error) {
	message := "calibration failed"
	if err != nil {
		message = err.Error()
	}
	item, saveErr := store.UpsertUsageCalibration(ctx, resourcepool.UsageCalibration{
		Model:              model,
		ProfileFingerprint: fingerprint,
		OverheadTokens:     defaultOverhead,
		Status:             resourcepool.UsageCalibrationFailed,
		LastError:          message,
	})
	if saveErr != nil {
		return item, saveErr
	}
	return item, errors.New(message)
}

func parsePositiveQueryInt(c *gin.Context, key string, fallback int) int {
	if c == nil {
		return fallback
	}
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err == nil && value > 0 {
		return value
	}
	return fallback
}

func parseNonNegativeQueryInt(c *gin.Context, key string, fallback int) int {
	if c == nil {
		return fallback
	}
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err == nil && value >= 0 {
		return value
	}
	return fallback
}

func parseUsageQuery(c *gin.Context) resourcepool.UsageQuery {
	query := resourcepool.UsageQuery{
		Window:    30 * 24 * time.Hour,
		PoolID:    strings.TrimSpace(c.Query("pool_id")),
		AccountID: strings.TrimSpace(c.Query("account_id")),
		APIKeyID:  strings.TrimSpace(c.Query("api_key_id")),
		Model:     strings.TrimSpace(c.Query("model")),
		Limit:     parsePositiveQueryInt(c, "limit", 100),
		Offset:    parseNonNegativeQueryInt(c, "offset", 0),
	}
	raw := strings.ToLower(strings.TrimSpace(c.Query("window")))
	switch raw {
	case "", "30d":
	case "24h", "1d":
		query.Window = 24 * time.Hour
	case "7d":
		query.Window = 7 * 24 * time.Hour
	case "all", "all-time":
		query.AllTime = true
		query.Window = 0
	default:
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			query.Window = parsed
		}
	}
	return query
}

func (h *Handler) ListClaudeCodePoolModels(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	models, err := store.ListModels(c.Request.Context(), false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	if err := store.AttachCurrentModelPrices(c.Request.Context(), models); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pricing_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": models})
}

// GetClaudeCodeModelPrices returns the current immutable revision and history metadata.
func (h *Handler) GetClaudeCodeModelPrices(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	current, err := store.CurrentModelPriceVersion(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pricing_failed", "message": err.Error()})
		return
	}
	versions, err := store.ListModelPriceVersions(c.Request.Context(), parsePositiveQueryInt(c, "limit", 20))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pricing_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"current": current, "versions": versions})
}

// PutClaudeCodeModelPrices creates a new immutable global price revision.
func (h *Handler) PutClaudeCodeModelPrices(c *gin.Context) {
	var body struct {
		Note    string                          `json:"note"`
		Updates []resourcepool.ModelPriceUpdate `json:"updates"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	version, err := store.CreateModelPriceVersion(c.Request.Context(), body.Updates, body.Note)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pricing_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishPricingChanged(strconv.FormatInt(version.ID, 10), "update")
	c.JSON(http.StatusOK, gin.H{"current": version})
}

func (h *Handler) CreateClaudeCodePoolModel(c *gin.Context) {
	var body resourcepool.ClaudeCodeModel
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	if strings.TrimSpace(body.Source) == "" {
		body.Source = "manual"
	}
	model, err := store.UpsertModel(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishModelChanged(model.ID, "create")
	c.JSON(http.StatusOK, gin.H{"item": model})
}

func (h *Handler) PatchClaudeCodePoolModel(c *gin.Context) {
	var body resourcepool.ClaudeCodeModelPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	model, err := store.PatchModel(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "update_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishModelChanged(model.ID, "update")
	c.JSON(http.StatusOK, gin.H{"item": model})
}

func (h *Handler) DeleteClaudeCodePoolModel(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	if err := store.DeleteModel(c.Request.Context(), c.Param("id")); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "delete_failed", "message": err.Error()})
		return
	}
	resourcepool.PublishModelChanged(c.Param("id"), "delete")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) FetchClaudeCodePoolModels(c *gin.Context) {
	var body struct {
		AccountID string `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	modelNames, err := h.fetchModelsFromClaudeCodeAccount(c.Request.Context(), store, body.AccountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fetch_failed", "message": err.Error()})
		return
	}
	items := make([]*resourcepool.ClaudeCodeModel, 0, len(modelNames))
	for _, name := range modelNames {
		model, err := store.UpsertModel(c.Request.Context(), resourcepool.ClaudeCodeModel{
			Name:    name,
			Alias:   name,
			Enabled: true,
			Source:  "account_fetch",
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "save_failed", "message": err.Error()})
			return
		}
		items = append(items, model)
	}
	if len(items) > 0 {
		resourcepool.PublishModelChanged("", "fetch")
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) fetchModelsFromClaudeCodeAccount(ctx context.Context, store *resourcepool.Store, accountID string) ([]string, error) {
	account, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if h.authManager == nil {
		return nil, errors.New("auth manager unavailable")
	}
	auth, errAuth := h.storedClaudeCodeAuth(ctx, account.ID)
	if errAuth != nil {
		return nil, errAuth
	}
	if err := h.ensureClaudeCodeManagementAccessToken(ctx, store, auth); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, err
	}
	applyClaudeCodeManagementHeaders(req, auth, false, claudeCodeManagementUserID(auth), "")
	resp, err := h.authManager.HttpRequest(ctx, auth, req)
	if err != nil {
		return nil, err
	}
	body, err := readClaudeCodeManagementResponseBody(resp, 2<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New(strings.TrimSpace(string(body)))
	}
	return parseClaudeModelNames(body), nil
}

func parseClaudeModelNames(body []byte) []string {
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		name := strings.TrimSpace(item.ID)
		if name == "" {
			name = strings.TrimSpace(item.Name)
		}
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

type claudeCodeAccountTestOptions struct {
	Model   string `json:"model"`
	Message string `json:"message"`
}

func (h *Handler) testClaudeCodeAccount(ctx context.Context, store *resourcepool.Store, accountID string, opts claudeCodeAccountTestOptions) (*resourcepool.ClaudeCodeAccount, string, error) {
	account, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, "", err
	}
	if h == nil || h.authManager == nil {
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, "auth manager unavailable")
		return account, "", errors.New("auth manager unavailable")
	}
	if strings.TrimSpace(account.AuthID) == "" {
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, "runtime auth is empty")
		return account, "", errors.New("runtime auth is empty")
	}
	auth, errAuth := h.storedClaudeCodeAuth(ctx, account.ID)
	if errAuth != nil {
		message := errAuth.Error()
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	if err := h.ensureClaudeCodeManagementAccessToken(ctx, store, auth); err != nil {
		message := err.Error()
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	model, errModel := resolveClaudeCodeAccountTestModel(ctx, store, opts.Model)
	if errModel != nil {
		message := errModel.Error()
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	testMessage := strings.TrimSpace(opts.Message)
	if testMessage == "" {
		testMessage = "hi"
	}
	userID := claudeCodeManagementUserID(auth)
	body, err := buildClaudeCodeAccountTestBody(model, testMessage, userID)
	if err != nil {
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, err.Error())
		return account, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(string(body)))
	if err != nil {
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, err.Error())
		return account, "", err
	}
	applyClaudeCodeManagementHeaders(req, auth, false, userID, model)
	attemptedAt := time.Now()
	resp, err := h.authManager.HttpRequest(ctx, auth, req)
	if err != nil {
		message := err.Error()
		recordClaudeCodeAccountTestUsage(ctx, store, account, model, http.StatusBadGateway, false, nil, attemptedAt)
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	responseBody, readErr := readClaudeCodeManagementResponseBody(resp, 2<<20)
	if readErr != nil {
		message := readErr.Error()
		recordClaudeCodeAccountTestUsage(ctx, store, account, model, resp.StatusCode, false, nil, attemptedAt)
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	testSucceeded := resp.StatusCode >= 200 && resp.StatusCode < 300
	recordClaudeCodeAccountTestUsage(ctx, store, account, model, resp.StatusCode, testSucceeded, responseBody, attemptedAt)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = resp.Status
		}
		_ = store.MarkAccountModelResult(ctx, account.ID, model, resp.StatusCode, message)
		account, _ = store.MarkAccountTestResult(ctx, account.ID, false, message)
		return account, "", errors.New(message)
	}
	reply := extractClaudeMessageText(responseBody)
	_, _ = store.MergeAccountRateLimitHeaders(ctx, account.ID, resp.Header)
	_ = store.MarkAccountModelResult(ctx, account.ID, model, http.StatusOK, "")
	account, err = store.MarkAccountTestResult(ctx, account.ID, true, "")
	if err != nil {
		return nil, "", err
	}
	if reply == "" {
		return account, "", fmt.Errorf("上游返回 HTTP 200，但没有可显示文本（%s）", summarizeClaudeMessageResponse(responseBody))
	}
	return account, reply, nil
}

func recordClaudeCodeAccountTestUsage(ctx context.Context, store *resourcepool.Store, account *resourcepool.ClaudeCodeAccount, model string, statusCode int, success bool, body []byte, attemptedAt time.Time) {
	if store == nil || account == nil {
		return
	}
	var payload struct {
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheCreation            struct {
				Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &payload)
	rawTotal := payload.Usage.InputTokens + payload.Usage.OutputTokens + payload.Usage.CacheReadInputTokens + payload.Usage.CacheCreationInputTokens
	if attemptedAt.IsZero() {
		attemptedAt = time.Now()
	}
	if statusCode == 0 {
		statusCode = http.StatusBadGateway
	}
	if err := store.RecordUsageLedger(ctx, resourcepool.UsageLedgerEntry{
		PoolID:              account.PoolID,
		RequestID:           "management-test",
		AccountID:           account.ID,
		AuthID:              account.AuthID,
		Model:               strings.TrimSpace(model),
		RequestedModel:      strings.TrimSpace(model),
		StatusCode:          statusCode,
		InputTokens:         payload.Usage.InputTokens,
		OutputTokens:        payload.Usage.OutputTokens,
		CacheReadTokens:     payload.Usage.CacheReadInputTokens,
		CacheCreationTokens: payload.Usage.CacheCreationInputTokens,
		CacheCreation5m:     payload.Usage.CacheCreation.Ephemeral5mInputTokens,
		CacheCreation1h:     payload.Usage.CacheCreation.Ephemeral1hInputTokens,
		RawInputTokens:      payload.Usage.InputTokens,
		RawTotalTokens:      rawTotal,
		Success:             success,
		CreatedAt:           attemptedAt,
	}); err != nil {
		log.WithError(err).WithField("account_id", account.ID).Warn("failed to record Claude Code account test usage")
	}
}

func resolveClaudeCodeAccountTestModel(ctx context.Context, store *resourcepool.Store, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if resolved, ok, err := store.ResolveModelAlias(ctx, requested); err != nil {
			return "", err
		} else if ok {
			return resolved, nil
		}
		return requested, nil
	}
	models, err := store.ListModels(ctx, true)
	if err != nil {
		return "", err
	}
	for _, model := range models {
		if strings.TrimSpace(model.Name) != "" {
			return strings.TrimSpace(model.Name), nil
		}
	}
	return "claude-3-5-haiku-latest", nil
}

const (
	claudeCodeManagementVersion         = resourcepool.DefaultClaudeCodeProfileVersion
	claudeCodeManagementUserAgent       = "claude-cli/" + claudeCodeManagementVersion + " (external, sdk-cli)"
	claudeCodeManagementIdentityPrompt  = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
	claudeCodeManagementFingerprintSalt = "59cf53e54c78"
	claudeCodeManagementOAuthBeta       = "oauth-2025-04-20"
	claudeCodeAccountTestMaxTokens      = 1024
)

func buildClaudeCodeAccountTestBody(model, message, userID string) ([]byte, error) {
	payload := buildClaudeCodeAccountTestPayload(model, message, userID, false)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func buildClaudeCodeUsageCalibrationBody(model, userID string) ([]byte, error) {
	payload := buildClaudeCodeAccountTestPayload(model, "hi", userID, true)
	delete(payload, "max_tokens")
	delete(payload, "metadata")
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func buildClaudeCodeAccountTestPayload(model, message, userID string, includeStaticPrompt bool) map[string]any {
	if strings.TrimSpace(userID) == "" {
		userID = helps.GenerateFakeUserID()
	}
	system := []map[string]any{
		{
			"type": "text",
			"text": claudeCodeManagementBillingHeader(message),
		},
		{
			"type":          "text",
			"text":          claudeCodeManagementIdentityPrompt,
			"cache_control": map[string]string{"type": "ephemeral", "ttl": "1h"},
		},
	}
	if includeStaticPrompt {
		staticPrompt := strings.Join([]string{
			helps.ClaudeCodeIntro,
			helps.ClaudeCodeSystem,
			helps.ClaudeCodeDoingTasks,
			helps.ClaudeCodeToneAndStyle,
			helps.ClaudeCodeOutputEfficiency,
		}, "\n\n")
		system = append(system, map[string]any{
			"type":          "text",
			"text":          staticPrompt,
			"cache_control": map[string]string{"type": "ephemeral", "ttl": "1h"},
		})
	}
	return map[string]any{
		"model":      model,
		"max_tokens": claudeCodeAccountTestMaxTokens,
		"system":     system,
		"metadata": map[string]string{
			"user_id": userID,
		},
		"messages": []map[string]string{
			{"role": "user", "content": message},
		},
	}
}

func claudeCodeManagementBillingHeader(messageText string) string {
	indices := [3]int{4, 7, 20}
	runes := []rune(messageText)
	var sb strings.Builder
	for _, idx := range indices {
		if idx < len(runes) {
			sb.WriteRune(runes[idx])
		} else {
			sb.WriteRune('0')
		}
	}
	input := claudeCodeManagementFingerprintSalt + sb.String() + claudeCodeManagementVersion
	h := sha256.Sum256([]byte(input))
	buildHash := hex.EncodeToString(h[:])[:3]
	return fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=sdk-cli;", claudeCodeManagementVersion, buildHash)
}

func applyClaudeCodeManagementHeaders(req *http.Request, auth *coreauth.Auth, stream bool, userID string, model string) {
	if req == nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-encoding", "gzip, deflate, br, zstd")
	req.Header.Set("user-agent", claudeCodeManagementUserAgent)
	req.Header.Set("x-app", "cli")
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", strings.Join(claudeCodeManagementBetasForAuth(auth, model), ","))
	req.Header.Set("x-stainless-runtime", "node")
	req.Header.Set("x-stainless-runtime-version", "v26.3.0")
	req.Header.Set("x-stainless-package-version", "0.94.0")
	req.Header.Set("x-stainless-lang", "js")
	req.Header.Set("x-stainless-retry-count", "0")
	req.Header.Set("x-stainless-timeout", "600")
	req.Header.Set("x-claude-code-session-id", claudeCodeManagementSessionID(auth, userID))
}

func claudeCodeManagementBetasForAuth(auth *coreauth.Auth, model string) []string {
	betas := claudeCodeManagementBetasForModel(model)
	if auth == nil || auth.AuthKind() != coreauth.AuthKindOAuth {
		return betas
	}
	for _, beta := range betas {
		if strings.EqualFold(beta, claudeCodeManagementOAuthBeta) {
			return betas
		}
	}
	insertAt := 0
	for index, beta := range betas {
		if strings.EqualFold(beta, "claude-code-20250219") {
			insertAt = index + 1
			break
		}
	}
	betas = append(betas, "")
	copy(betas[insertAt+1:], betas[insertAt:])
	betas[insertAt] = claudeCodeManagementOAuthBeta
	return betas
}

func claudeCodeManagementBetasForModel(model string) []string {
	modelLower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(modelLower, "haiku"):
		return []string{
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
		}
	case strings.Contains(modelLower, "opus"):
		return []string{
			"claude-code-20250219",
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
			"mid-conversation-system-2026-04-07",
		}
	default:
		return []string{
			"claude-code-20250219",
			"interleaved-thinking-2025-05-14",
			"thinking-token-count-2026-05-13",
			"context-management-2025-06-27",
			"prompt-caching-scope-2026-01-05",
		}
	}
}

func claudeCodeManagementUserID(auth *coreauth.Auth) string {
	if auth != nil && auth.Attributes != nil {
		if userID := strings.TrimSpace(auth.Attributes["cloak_user_id"]); userID != "" && helps.IsValidUserID(userID) {
			return userID
		}
	}
	accountUUID := ""
	if auth != nil && auth.Metadata != nil {
		if raw, _ := auth.Metadata["account_uuid"].(string); strings.TrimSpace(raw) != "" {
			accountUUID = strings.TrimSpace(raw)
		}
	}
	generated := helps.GenerateFakeUserID()
	if accountUUID == "" {
		return generated
	}
	parsed := parseClaudeCodeLegacyUserID(generated)
	if parsed.deviceID == "" || parsed.sessionID == "" {
		return generated
	}
	return "user_" + parsed.deviceID + "_account_" + accountUUID + "_session_" + parsed.sessionID
}

type claudeCodeLegacyUserID struct {
	deviceID  string
	sessionID string
}

func parseClaudeCodeLegacyUserID(userID string) claudeCodeLegacyUserID {
	parts := strings.Split(strings.TrimSpace(userID), "_")
	if len(parts) != 6 || parts[0] != "user" || parts[2] != "account" || parts[4] != "session" {
		return claudeCodeLegacyUserID{}
	}
	return claudeCodeLegacyUserID{deviceID: parts[1], sessionID: parts[5]}
}

func claudeCodeManagementSessionID(auth *coreauth.Auth, userID string) string {
	parts := strings.Split(strings.TrimSpace(userID), "_")
	if len(parts) == 6 && parts[4] == "session" {
		return parts[5]
	}
	return helps.CachedSessionID(claudeCodeManagementAuthKey(auth))
}

func claudeCodeManagementAuthKey(auth *coreauth.Auth) string {
	if auth == nil {
		return "claude-code-management"
	}
	if strings.TrimSpace(auth.ID) != "" {
		return strings.TrimSpace(auth.ID)
	}
	if auth.Metadata != nil {
		if token, _ := auth.Metadata["access_token"].(string); strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}
	return "claude-code-management"
}

func (h *Handler) ensureClaudeCodeManagementAccessToken(ctx context.Context, store *resourcepool.Store, auth *coreauth.Auth) error {
	if auth == nil {
		return errors.New("auth is nil")
	}
	accessToken, _ := auth.Metadata["access_token"].(string)
	expiresAt, hasExpiry := auth.ExpirationTime()
	if strings.TrimSpace(accessToken) != "" && (!hasExpiry || time.Now().Add(2*time.Minute).Before(expiresAt)) {
		return nil
	}
	return h.refreshClaudeCodeManagementAccessToken(ctx, store, auth)
}

func (h *Handler) refreshClaudeCodeManagementAccessToken(ctx context.Context, store *resourcepool.Store, auth *coreauth.Auth) error {
	if auth == nil {
		return errors.New("auth is nil")
	}
	refreshToken, _ := auth.Metadata["refresh_token"].(string)
	if strings.TrimSpace(refreshToken) == "" {
		return errors.New("missing refresh_token")
	}
	h.mu.Lock()
	cfg := h.cfg
	h.mu.Unlock()
	service := claudeauth.NewClaudeAuthWithProxyURL(cfg, auth.ProxyURL)
	tokenData, err := service.RefreshClaudeCodeTokensWithRetry(ctx, refreshToken, 3)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokenData.AccessToken) == "" {
		return errors.New("refresh response did not include access_token")
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata["access_token"] = strings.TrimSpace(tokenData.AccessToken)
	if strings.TrimSpace(tokenData.RefreshToken) != "" {
		auth.Metadata["refresh_token"] = strings.TrimSpace(tokenData.RefreshToken)
	}
	if strings.TrimSpace(tokenData.Email) != "" {
		auth.Metadata["email"] = strings.TrimSpace(tokenData.Email)
	}
	if strings.TrimSpace(tokenData.OrganizationUUID) != "" {
		auth.Metadata["org_uuid"] = strings.TrimSpace(tokenData.OrganizationUUID)
	}
	if strings.TrimSpace(tokenData.AccountUUID) != "" {
		auth.Metadata["account_uuid"] = strings.TrimSpace(tokenData.AccountUUID)
	}
	if strings.TrimSpace(tokenData.Expire) != "" {
		auth.Metadata["expired"] = strings.TrimSpace(tokenData.Expire)
	}
	auth.Metadata["type"] = "claude"
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)
	if h.authManager != nil {
		if _, errUpdate := h.authManager.Update(ctx, auth); errUpdate != nil {
			return errUpdate
		}
	}
	if store != nil {
		if errSave := store.SaveClaudeCodeAccountAuth(ctx, auth); errSave != nil {
			return errSave
		}
	}
	return nil
}

func extractClaudeMessageText(body []byte) string {
	if text := extractClaudeJSONMessageText(body); text != "" {
		return text
	}
	var reply strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if text := extractClaudeJSONMessageText([]byte(data)); text != "" {
			reply.WriteString(text)
		}
	}
	return strings.TrimSpace(reply.String())
}

type claudeMessageContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeMessageEnvelope struct {
	Type         string                      `json:"type"`
	StopReason   string                      `json:"stop_reason"`
	Content      []claudeMessageContentBlock `json:"content"`
	ContentBlock claudeMessageContentBlock   `json:"content_block"`
	Delta        claudeMessageContentBlock   `json:"delta"`
	Completion   string                      `json:"completion"`
	OutputText   string                      `json:"output_text"`
	Message      *struct {
		Content []claudeMessageContentBlock `json:"content"`
	} `json:"message"`
}

func extractClaudeJSONMessageText(body []byte) string {
	var payload claudeMessageEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	parts := make([]string, 0, len(payload.Content)+3)
	appendBlocks := func(blocks []claudeMessageContentBlock) {
		for _, block := range blocks {
			if block.Type != "" && block.Type != "text" && block.Type != "text_delta" {
				continue
			}
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	appendBlocks(payload.Content)
	if payload.Message != nil {
		appendBlocks(payload.Message.Content)
	}
	for _, text := range []string{payload.ContentBlock.Text, payload.Delta.Text, payload.Completion, payload.OutputText} {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n")
}

func summarizeClaudeMessageResponse(body []byte) string {
	var payload claudeMessageEnvelope
	if err := json.Unmarshal(body, &payload); err == nil {
		contentTypes := make([]string, 0, len(payload.Content))
		for _, block := range payload.Content {
			if block.Type != "" {
				contentTypes = append(contentTypes, block.Type)
			}
		}
		parts := []string{}
		if payload.Type != "" {
			parts = append(parts, "type="+payload.Type)
		}
		if payload.StopReason != "" {
			parts = append(parts, "stop_reason="+payload.StopReason)
		}
		if len(contentTypes) > 0 {
			parts = append(parts, "content="+strings.Join(contentTypes, ","))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " · ")
		}
	}
	events := make([]string, 0, 4)
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "event:") {
			continue
		}
		event := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		if event != "" && !seen[event] {
			seen[event] = true
			events = append(events, event)
		}
	}
	if len(events) > 0 {
		return "SSE=" + strings.Join(events, ",")
	}
	return "无法识别的响应结构"
}

type managementCompositeReadCloser struct {
	io.Reader
	closers []func() error
}

func (c *managementCompositeReadCloser) Close() error {
	var firstErr error
	for _, closeFn := range c.closers {
		if closeFn == nil {
			continue
		}
		if err := closeFn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type managementPeekableBody struct {
	*bufio.Reader
	closer io.Closer
}

func (p *managementPeekableBody) Close() error {
	return p.closer.Close()
}

func readClaudeCodeManagementResponseBody(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("response body is nil")
	}
	decodedBody, err := decodeClaudeCodeManagementResponseBody(resp.Body, resp.Header.Get("Content-Encoding"))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = decodedBody.Close()
	}()
	if limit <= 0 {
		limit = 2 << 20
	}
	return io.ReadAll(io.LimitReader(decodedBody, limit))
}

func decodeClaudeCodeManagementResponseBody(body io.ReadCloser, contentEncoding string) (io.ReadCloser, error) {
	if body == nil {
		return nil, errors.New("response body is nil")
	}
	if strings.TrimSpace(contentEncoding) == "" {
		peekable := &managementPeekableBody{Reader: bufio.NewReader(body), closer: body}
		magic, peekErr := peekable.Peek(4)
		if peekErr == nil || (peekErr == io.EOF && len(magic) >= 2) {
			switch {
			case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
				gzipReader, err := gzip.NewReader(peekable)
				if err != nil {
					_ = peekable.Close()
					return nil, fmt.Errorf("magic-byte gzip: failed to create reader: %w", err)
				}
				return &managementCompositeReadCloser{
					Reader: gzipReader,
					closers: []func() error{
						gzipReader.Close,
						peekable.Close,
					},
				}, nil
			case len(magic) >= 4 && magic[0] == 0x28 && magic[1] == 0xb5 && magic[2] == 0x2f && magic[3] == 0xfd:
				decoder, err := zstd.NewReader(peekable)
				if err != nil {
					_ = peekable.Close()
					return nil, fmt.Errorf("magic-byte zstd: failed to create reader: %w", err)
				}
				return &managementCompositeReadCloser{
					Reader: decoder,
					closers: []func() error{
						func() error { decoder.Close(); return nil },
						peekable.Close,
					},
				}, nil
			}
		}
		return peekable, nil
	}
	for _, raw := range strings.Split(contentEncoding, ",") {
		encoding := strings.TrimSpace(strings.ToLower(raw))
		switch encoding {
		case "", "identity":
			continue
		case "gzip":
			gzipReader, err := gzip.NewReader(body)
			if err != nil {
				_ = body.Close()
				return nil, fmt.Errorf("failed to create gzip reader: %w", err)
			}
			return &managementCompositeReadCloser{
				Reader: gzipReader,
				closers: []func() error{
					gzipReader.Close,
					body.Close,
				},
			}, nil
		case "deflate":
			deflateReader := flate.NewReader(body)
			return &managementCompositeReadCloser{
				Reader: deflateReader,
				closers: []func() error{
					deflateReader.Close,
					body.Close,
				},
			}, nil
		case "br":
			return &managementCompositeReadCloser{
				Reader: brotli.NewReader(body),
				closers: []func() error{
					body.Close,
				},
			}, nil
		case "zstd":
			decoder, err := zstd.NewReader(body)
			if err != nil {
				_ = body.Close()
				return nil, fmt.Errorf("failed to create zstd reader: %w", err)
			}
			return &managementCompositeReadCloser{
				Reader: decoder,
				closers: []func() error{
					func() error { decoder.Close(); return nil },
					body.Close,
				},
			}, nil
		default:
			continue
		}
	}
	return body, nil
}

func (h *Handler) storedClaudeCodeAuth(ctx context.Context, accountID string) (*coreauth.Auth, error) {
	if h == nil || h.authManager == nil {
		return nil, errors.New("auth manager unavailable")
	}
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	auth, err := resourcepool.GetStoredAuth(ctx, configPath, cfg, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("stored auth not found")
		}
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("stored auth not found")
	}
	return auth, nil
}

func (h *Handler) ImportClaudeAuthToAccountPool(c *gin.Context) {
	var body struct {
		AuthID          string `json:"auth_id"`
		Email           string `json:"email"`
		ProxyResourceID string `json:"proxy_resource_id"`
		PoolID          string `json:"pool_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	authID := strings.TrimSpace(body.AuthID)
	email := strings.TrimSpace(body.Email)
	if authID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_id is required"})
		return
	}
	if h.authManager != nil {
		if auth, ok := h.authManager.GetByID(authID); ok && auth != nil {
			if !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "only claude auth can be imported"})
				return
			}
			if email == "" {
				email = authEmail(auth)
			}
		}
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.RegisterClaudeCodeAccountInPool(c.Request.Context(), body.PoolID, authID, email, body.ProxyResourceID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "import_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "import")
	if account.ProxyResourceID != "" {
		resourcepool.PublishProxyChanged(account.ProxyResourceID, "bind")
	}
	resourcepool.PublishStatsChanged("account")
	h.startClaudeCodeAccountInitialProbe(account.ID)
	c.JSON(http.StatusOK, gin.H{"account": account})
}

// RecheckClaudeCodeAccount runs an explicit usage probe that may clear manual recovery.
func (h *Handler) RecheckClaudeCodeAccount(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	account, err := resourcepool.RecheckStoredAccountQuota(c.Request.Context(), configPath, cfg, store, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "recheck_failed", "message": err.Error(), "account": account})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "recovered")
	resourcepool.PublishStatsChanged("account_health")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) PatchClaudeCodeAccount(c *gin.Context) {
	var body resourcepool.AccountPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.PatchAccount(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "update_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "update")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) MoveClaudeCodeAccount(c *gin.Context) {
	var body struct {
		PoolID string `json:"pool_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.PoolID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pool_id is required"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.MoveAccountToPool(c.Request.Context(), c.Param("id"), body.PoolID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		} else if errors.Is(err, resourcepool.ErrAccountMoveInFlight) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": "move_failed", "message": err.Error()})
		return
	}
	if h.authManager != nil {
		if runtimeAuth, okAuth := h.authManager.GetByID(account.AuthID); okAuth && runtimeAuth != nil {
			if runtimeAuth.Attributes == nil {
				runtimeAuth.Attributes = map[string]string{}
			}
			runtimeAuth.Attributes[resourcepool.AttrAccountPoolID] = account.PoolID
			_, _ = h.authManager.Update(coreauth.WithSkipPersist(c.Request.Context()), runtimeAuth)
		}
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "move")
	resourcepool.PublishPoolChanged(account.PoolID, "account_move")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) PatchClaudeCodeAccountCapacity(c *gin.Context) {
	var body resourcepool.AccountCapacityPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	capacity, err := store.PatchAccountCapacity(c.Request.Context(), c.Param("id"), body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(c.Param("id"), "capacity")
	resourcepool.PublishStatsChanged("capacity")
	c.JSON(http.StatusOK, gin.H{"capacity": capacity})
}

func (h *Handler) ListClaudeCodeAccountModelStatus(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	statuses, err := store.ListAccountModelStatuses(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": statuses})
}

func (h *Handler) BindClaudeCodeAccountProxy(c *gin.Context) {
	var body struct {
		ProxyResourceID string `json:"proxy_resource_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.BindAccountProxy(c.Request.Context(), c.Param("id"), body.ProxyResourceID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bind_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "bind_proxy")
	resourcepool.PublishProxyChanged(account.ProxyResourceID, "bind")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) UnbindClaudeCodeAccountProxy(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.UnbindAccountProxy(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unbind_failed", "message": err.Error()})
		return
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "unbind_proxy")
	resourcepool.PublishProxyChanged("", "unbind")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) ResetClaudeCodeAccountCooling(c *gin.Context) {
	accountID := strings.TrimSpace(c.Param("id"))
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	account, err := store.GetAccount(c.Request.Context(), accountID)
	closeResourcePoolStore(store)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account_not_found"})
		return
	}
	if h.authManager != nil {
		if auth, ok := h.authManager.GetByID(account.AuthID); ok && auth != nil {
			resetAuthCooling(auth)
			_, _ = h.authManager.Update(coreauth.WithSkipPersist(c.Request.Context()), auth)
		}
	}
	routingScope := resourcepool.AccountRoutingScope(account.PoolID)
	claudeapipool.ResetScopedRouteCooling(routingScope, account.AuthID)
	claudeapipool.ClearScopedProxyBlock(routingScope, account.ProxyResourceID)
	resourcepool.PublishAccountChanged(account.ID, "reset_cooling")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) DeleteClaudeCodeAccount(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.GetAccount(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account_not_found"})
		return
	}
	if err := store.DeleteAccount(c.Request.Context(), account.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "delete_failed", "message": err.Error()})
		return
	}
	if h.authManager != nil {
		h.authManager.Remove(coreauth.WithSkipPersist(c.Request.Context()), account.AuthID)
	}
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "delete")
	if account.ProxyResourceID != "" {
		resourcepool.PublishProxyChanged(account.ProxyResourceID, "unbind")
	}
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) TestClaudeCodeAccount(c *gin.Context) {
	var body claudeCodeAccountTestOptions
	if c.Request.Body != nil {
		if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, reply, err := h.testClaudeCodeAccount(c.Request.Context(), store, c.Param("id"), body)
	if err != nil {
		if account != nil {
			resourcepool.PublishAccountChanged(account.ID, "test")
			resourcepool.PublishStatsChanged("account")
		}
		c.JSON(http.StatusOK, gin.H{"account": account, "warning": err.Error()})
		return
	}
	if account != nil {
		resourcepool.PublishAccountChanged(account.ID, "test")
		resourcepool.PublishStatsChanged("account")
	}
	c.JSON(http.StatusOK, gin.H{"account": account, "reply": reply})
}

func (h *Handler) RefreshClaudeCodeAccountQuota(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := h.refreshClaudeCodeAccountQuota(c.Request.Context(), store, c.Param("id"))
	if account != nil {
		resourcepool.PublishAccountChanged(account.ID, "quota")
		resourcepool.PublishStatsChanged("account")
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"account": account, "warning": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) RefreshClaudeCodeAccountToken(c *gin.Context) {
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	account, err := store.GetAccount(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account_not_found", "message": err.Error()})
		return
	}
	auth, errAuth := h.storedClaudeCodeAuth(c.Request.Context(), account.ID)
	if errAuth != nil {
		c.JSON(http.StatusOK, gin.H{"account": account, "warning": errAuth.Error()})
		return
	}
	if err := h.refreshClaudeCodeManagementAccessToken(c.Request.Context(), store, auth); err != nil {
		if refreshed, errGet := store.GetAccount(c.Request.Context(), account.ID); errGet == nil {
			account = refreshed
		}
		resourcepool.PublishAccountChanged(account.ID, "token")
		resourcepool.PublishStatsChanged("account")
		c.JSON(http.StatusOK, gin.H{"account": account, "warning": err.Error()})
		return
	}
	if refreshed, errGet := store.GetAccount(c.Request.Context(), account.ID); errGet == nil {
		account = refreshed
	}
	now := time.Now()
	if recovered, errHealth := store.UpdateAccountHealth(c.Request.Context(), account.ID, resourcepool.AccountHealthUpdate{
		Source:              "manual_token_refresh",
		Status:              resourcepool.AccountHealthHealthy,
		CheckedAt:           &now,
		AllowManualRecovery: true,
	}); errHealth == nil {
		account = recovered
	}
	claudeapipool.ClearScopedAccountBlock(resourcepool.AccountRoutingScope(account.PoolID), account.AuthID)
	h.triggerConfigReload(c.Request.Context())
	resourcepool.PublishAccountChanged(account.ID, "token")
	resourcepool.PublishStatsChanged("account")
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) refreshClaudeCodeAccountQuota(ctx context.Context, store *resourcepool.Store, accountID string) (*resourcepool.ClaudeCodeAccount, error) {
	account, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	auth, errAuth := resourcepool.GetStoredAuth(ctx, configPath, cfg, account.ID)
	if errAuth != nil {
		nowAccount, errSave := store.SaveAccountQuota(ctx, resourcepool.AccountQuota{
			AccountID: account.ID,
			Status:    "error",
			Windows:   []resourcepool.QuotaWindow{},
			LastError: errAuth.Error(),
		})
		if errSave != nil {
			return nil, errSave
		}
		return nowAccount, errAuth
	}
	return resourcepool.RefreshAccountQuota(ctx, cfg, store, account.ID, auth, func(updated *coreauth.Auth) error {
		if h.authManager != nil {
			if _, err := h.authManager.Update(ctx, updated); err != nil {
				return err
			}
		}
		return store.SaveClaudeCodeAccountAuth(ctx, updated)
	})
}

func (h *Handler) BatchClaudeCodeAccounts(c *gin.Context) {
	var body struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	ids := dedupeTrimmedStrings(body.IDs)
	if action == "" || len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action and ids are required"})
		return
	}
	store, ok := h.openResourcePoolStore(c)
	if !ok {
		return
	}
	defer closeResourcePoolStore(store)
	result := gin.H{
		"action": action,
		"total":  len(ids),
		"ok":     0,
		"failed": 0,
		"errors": []gin.H{},
	}
	errorsOut := make([]gin.H, 0)
	successCount := 0
	for _, id := range ids {
		if err := h.applyClaudeCodeAccountBatchAction(c.Request.Context(), store, action, id); err != nil {
			errorsOut = append(errorsOut, gin.H{"id": id, "message": err.Error()})
			continue
		}
		successCount++
	}
	result["ok"] = successCount
	result["failed"] = len(ids) - successCount
	result["errors"] = errorsOut
	if action != "test" {
		h.triggerConfigReload(c.Request.Context())
	}
	if successCount > 0 {
		resourcepool.PublishAccountChanged("", "batch_"+action)
		if action == "unbind" || action == "delete" {
			resourcepool.PublishProxyChanged("", "account_"+action)
		}
		resourcepool.PublishStatsChanged("account")
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) applyClaudeCodeAccountBatchAction(ctx context.Context, store *resourcepool.Store, action, id string) error {
	switch action {
	case "enable":
		enabled := true
		_, err := store.PatchAccount(ctx, id, resourcepool.AccountPatch{Enabled: &enabled})
		return err
	case "disable":
		enabled := false
		_, err := store.PatchAccount(ctx, id, resourcepool.AccountPatch{Enabled: &enabled})
		return err
	case "delete":
		account, err := store.GetAccount(ctx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteAccount(ctx, account.ID); err != nil {
			return err
		}
		if h.authManager != nil {
			h.authManager.Remove(coreauth.WithSkipPersist(ctx), account.AuthID)
		}
		return nil
	case "test":
		account, _, err := h.testClaudeCodeAccount(ctx, store, id, claudeCodeAccountTestOptions{})
		if account != nil {
			resourcepool.PublishAccountChanged(account.ID, "test")
			resourcepool.PublishStatsChanged("account")
		}
		return err
	case "refresh-quota":
		account, err := h.refreshClaudeCodeAccountQuota(ctx, store, id)
		if account != nil {
			resourcepool.PublishAccountChanged(account.ID, "quota")
			resourcepool.PublishStatsChanged("account")
		}
		return err
	case "unbind":
		_, err := store.UnbindAccountProxy(ctx, id)
		return err
	case "reset-cooling":
		account, err := store.GetAccount(ctx, id)
		if err != nil {
			return err
		}
		if h.authManager != nil {
			if auth, ok := h.authManager.GetByID(account.AuthID); ok && auth != nil {
				resetAuthCooling(auth)
				_, _ = h.authManager.Update(coreauth.WithSkipPersist(ctx), auth)
			}
		}
		return nil
	default:
		return errors.New("unsupported batch action")
	}
}

func (h *Handler) RequestClaudeCodeAccountPoolAuthURL(c *gin.Context) {
	q := c.Request.URL.Query()
	q.Set("pool", "claude-code")
	if strings.TrimSpace(q.Get("pool_id")) == "" {
		q.Set("pool_id", resourcepool.DefaultAccountPoolID)
	}
	if q.Get("proxy_resource_id") == "" && q.Get("login_proxy_resource_id") != "" {
		q.Set("proxy_resource_id", q.Get("login_proxy_resource_id"))
	}
	c.Request.URL.RawQuery = q.Encode()
	h.RequestAnthropicToken(c)
}

func (h *Handler) authEntryByID() map[string]gin.H {
	out := map[string]gin.H{}
	if h == nil || h.authManager == nil {
		return out
	}
	auths := h.authManager.List()
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
			continue
		}
		entry := h.buildAuthFileEntry(auth)
		if entry == nil {
			entry = gin.H{
				"status":          auth.Status,
				"status_message":  auth.StatusMessage,
				"success":         auth.Success,
				"failed":          auth.Failed,
				"recent_requests": auth.RecentRequestsSnapshot(time.Now()),
			}
			if !auth.NextRetryAfter.IsZero() {
				entry["next_retry_after"] = auth.NextRetryAfter
			}
		}
		out[auth.ID] = accountRuntimeEntry(auth, entry)
	}
	return out
}

func accountRuntimeEntry(auth *coreauth.Auth, entry gin.H) gin.H {
	success, _ := entry["success"].(int64)
	failed, _ := entry["failed"].(int64)
	total := success + failed
	successRate := 0.0
	if total > 0 {
		successRate = float64(success) / float64(total)
	}
	health := 100
	if total > 0 {
		health = int(successRate * 100)
	}
	if auth != nil && (auth.Disabled || auth.Status == coreauth.StatusDisabled || auth.Unavailable) {
		health = 0
	}
	out := gin.H{
		"status":          entry["status"],
		"status_message":  entry["status_message"],
		"success":         entry["success"],
		"failed":          entry["failed"],
		"success_rate":    successRate,
		"health":          health,
		"recent_requests": entry["recent_requests"],
		"cooling_until":   entry["next_retry_after"],
		"last_error":      "",
	}
	if auth != nil && auth.LastError != nil {
		out["last_error"] = auth.LastError.Message
	}
	return out
}

func (h *Handler) triggerConfigReload(ctx context.Context) {
	if h == nil {
		return
	}
	h.mu.Lock()
	cfg := h.cfg
	hook := h.configReloadHook
	host := h.pluginHost
	resourcePoolSync := h.resourcePoolSync
	h.mu.Unlock()
	if cfg == nil {
		return
	}
	if hook != nil {
		hook(ctx, cfg)
	} else if host != nil {
		host.ApplyConfig(ctx, cfg)
	}
	if resourcePoolSync != nil {
		if err := resourcePoolSync(ctx); err != nil {
			log.Warnf("resource pool runtime sync failed: %v", err)
		}
	}
}

func closeResourcePoolStore(store *resourcepool.Store) {
	if store != nil {
		_ = store.Close()
	}
}
