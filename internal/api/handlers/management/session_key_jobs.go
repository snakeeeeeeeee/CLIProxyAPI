package management

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	sessionKeyJobRetention    = 30 * time.Minute
	sessionKeyReservationTTL  = 2 * time.Minute
	sessionKeyReservationTick = 30 * time.Second
	sessionKeyItemTimeout     = 90 * time.Second
	maxSessionKeysPerJob      = 100
	maxSessionKeyLength       = 4096
)

type sessionKeyAuthenticator interface {
	Authenticate(context.Context, string) (*claudeauth.ClaudeAuthBundle, error)
}

type sessionKeyAuthenticatorFactory func(*config.Config, string) sessionKeyAuthenticator

func defaultSessionKeyAuthenticatorFactory(cfg *config.Config, proxyURL string) sessionKeyAuthenticator {
	return claudeauth.NewSessionKeyAuthenticator(cfg, proxyURL)
}

type sessionKeyJobManager struct {
	mu        sync.Mutex
	storageMu sync.Mutex
	active    *sessionKeyJob
	latest    *sessionKeyJob
	jobs      map[string]*sessionKeyJob
}

type sessionKeyJob struct {
	ID          string
	PoolID      string
	Status      string
	Concurrency int
	Items       []*sessionKeyJobItem
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Cancel      context.CancelFunc
	ctx         context.Context
	claimed     map[string]string
}

type sessionKeyJobItem struct {
	Index        int
	ID           string
	Fingerprint  string
	SessionKey   string
	ProxyID      string
	ProxyName    string
	ProxyExitIP  string
	AccountID    string
	AccountEmail string
	Status       string
	ErrorCode    string
	ErrorMessage string
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

type sessionKeyJobView struct {
	ID          string                  `json:"id"`
	PoolID      string                  `json:"pool_id"`
	Status      string                  `json:"status"`
	Concurrency int                     `json:"concurrency"`
	Total       int                     `json:"total"`
	Queued      int                     `json:"queued"`
	Running     int                     `json:"running"`
	Succeeded   int                     `json:"succeeded"`
	Updated     int                     `json:"updated"`
	Failed      int                     `json:"failed"`
	NoProxy     int                     `json:"no_proxy"`
	Cancelled   int                     `json:"cancelled"`
	Items       []sessionKeyJobItemView `json:"items"`
	CreatedAt   time.Time               `json:"created_at"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

type sessionKeyJobItemView struct {
	Index        int        `json:"index"`
	Fingerprint  string     `json:"fingerprint"`
	ProxyID      string     `json:"proxy_id,omitempty"`
	ProxyName    string     `json:"proxy_name,omitempty"`
	ProxyExitIP  string     `json:"proxy_exit_ip,omitempty"`
	AccountID    string     `json:"account_id,omitempty"`
	AccountEmail string     `json:"account_email,omitempty"`
	Status       string     `json:"status"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

func newSessionKeyJobManager() *sessionKeyJobManager {
	return &sessionKeyJobManager{jobs: make(map[string]*sessionKeyJob)}
}

func (m *sessionKeyJobManager) claim(job *sessionKeyJob) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	if m.active != nil {
		return false
	}
	m.active = job
	m.latest = job
	if m.jobs == nil {
		m.jobs = make(map[string]*sessionKeyJob)
	}
	m.jobs[job.ID] = job
	return true
}

func (m *sessionKeyJobManager) releaseClaim(job *sessionKeyJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == job {
		m.active = nil
	}
	if m.latest == job && job.CompletedAt == nil {
		m.latest = nil
	}
	if job.CompletedAt == nil {
		delete(m.jobs, job.ID)
		m.recomputeLatestLocked()
	}
}

func (m *sessionKeyJobManager) view(id string, current bool) (*sessionKeyJobView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	job := m.latest
	if current && m.active != nil {
		job = m.active
	}
	if !current {
		job = m.jobs[id]
	}
	if job == nil {
		return nil, false
	}
	view := buildSessionKeyJobView(job)
	return &view, true
}

func (m *sessionKeyJobManager) cancel(id string) (*sessionKeyJobView, []string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.ID != id {
		return nil, nil, false
	}
	job := m.active
	cancelledItems := make([]string, 0)
	if job.Status == "queued" || job.Status == "running" {
		job.Status = "cancelling"
		for _, item := range job.Items {
			if item.Status == "queued" {
				completeSessionKeyItem(item, "cancelled", "cancelled", "任务已取消")
				item.SessionKey = ""
				cancelledItems = append(cancelledItems, item.ID)
			}
		}
		if job.Cancel != nil {
			job.Cancel()
		}
	}
	view := buildSessionKeyJobView(job)
	return &view, cancelledItems, true
}

func (m *sessionKeyJobManager) expireLocked(now time.Time) {
	for id, job := range m.jobs {
		if job.CompletedAt != nil && now.Sub(*job.CompletedAt) > sessionKeyJobRetention {
			delete(m.jobs, id)
			if m.latest == job {
				m.latest = nil
			}
		}
	}
	if m.latest == nil {
		m.recomputeLatestLocked()
	}
}

func (m *sessionKeyJobManager) recomputeLatestLocked() {
	for _, job := range m.jobs {
		if m.latest == nil || job.CreatedAt.After(m.latest.CreatedAt) {
			m.latest = job
		}
	}
}

func buildSessionKeyJobView(job *sessionKeyJob) sessionKeyJobView {
	view := sessionKeyJobView{
		ID: job.ID, PoolID: job.PoolID, Status: job.Status, Concurrency: job.Concurrency, Total: len(job.Items),
		Items: make([]sessionKeyJobItemView, 0, len(job.Items)), CreatedAt: job.CreatedAt,
		StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
	}
	for _, item := range job.Items {
		itemView := sessionKeyJobItemView{
			Index: item.Index, Fingerprint: item.Fingerprint, ProxyID: item.ProxyID,
			ProxyName: item.ProxyName, ProxyExitIP: item.ProxyExitIP, AccountID: item.AccountID,
			AccountEmail: item.AccountEmail, Status: item.Status, ErrorCode: item.ErrorCode,
			ErrorMessage: item.ErrorMessage, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt,
		}
		view.Items = append(view.Items, itemView)
		switch item.Status {
		case "queued":
			view.Queued++
		case "running":
			view.Running++
		case "success":
			view.Succeeded++
		case "updated":
			view.Updated++
		case "no_proxy":
			view.NoProxy++
		case "cancelled":
			view.Cancelled++
		default:
			view.Failed++
		}
	}
	return view
}

// CreateSessionKeyJob starts one asynchronous batch login task.
func (h *Handler) CreateSessionKeyJob(c *gin.Context) {
	var body struct {
		SessionKeys []string `json:"session_keys"`
		Concurrency int      `json:"concurrency"`
		PoolID      string   `json:"pool_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "请求格式无效"})
		return
	}
	if len(body.SessionKeys) == 0 || len(body.SessionKeys) > maxSessionKeysPerJob {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_count", "message": "每批需要 1-100 个 SessionKey"})
		return
	}
	if body.Concurrency == 0 {
		body.Concurrency = 2
	}
	if body.Concurrency < 1 || body.Concurrency > 5 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_concurrency", "message": "并发范围为 1-5"})
		return
	}
	body.PoolID = strings.TrimSpace(body.PoolID)
	if body.PoolID == "" {
		body.PoolID = resourcepool.DefaultAccountPoolID
	}
	poolStore, errPoolStore := h.openResourcePoolStoreForJob()
	if errPoolStore != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "open_failed", "message": errPoolStore.Error()})
		return
	}
	_, errPool := poolStore.RequireActiveAccountPool(c.Request.Context(), body.PoolID)
	closeResourcePoolStore(poolStore)
	if errPool != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pool", "message": errPool.Error()})
		return
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	job := &sessionKeyJob{
		ID: uuid.NewString(), PoolID: body.PoolID, Status: "queued", Concurrency: body.Concurrency,
		Items: make([]*sessionKeyJobItem, 0, len(body.SessionKeys)), CreatedAt: time.Now().UTC(),
		Cancel: cancel, ctx: jobCtx, claimed: make(map[string]string),
	}
	seen := make(map[string]struct{}, len(body.SessionKeys))
	itemIDs := make([]string, 0, len(body.SessionKeys))
	for index, raw := range body.SessionKeys {
		key := strings.TrimSpace(raw)
		item := &sessionKeyJobItem{Index: index + 1, ID: fmt.Sprintf("item-%d", index+1), Fingerprint: sessionKeyFingerprint(key), SessionKey: key, Status: "queued"}
		switch {
		case len(key) == 0 || len(key) > maxSessionKeyLength || !strings.HasPrefix(key, "sk-ant-sid"):
			completeSessionKeyItem(item, "invalid_format", "invalid_format", "SessionKey 格式无效")
		case func() bool { _, ok := seen[key]; return ok }():
			completeSessionKeyItem(item, "duplicate_input", "duplicate_input", "批次内重复输入")
		default:
			seen[key] = struct{}{}
			itemIDs = append(itemIDs, item.ID)
		}
		job.Items = append(job.Items, item)
	}
	if h.sessionKeyJobs == nil {
		h.sessionKeyJobs = newSessionKeyJobManager()
	}
	if !h.sessionKeyJobs.claim(job) {
		cancel()
		c.JSON(http.StatusConflict, gin.H{"error": "job_running", "message": "已有 SessionKey 批量任务正在运行"})
		return
	}
	store, err := h.openResourcePoolStoreForJob()
	if err != nil {
		h.sessionKeyJobs.releaseClaim(job)
		cancel()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "open_failed", "message": err.Error()})
		return
	}
	reservations, err := store.ReserveHealthyProxies(c.Request.Context(), job.ID, "session-key-login", itemIDs, sessionKeyReservationTTL)
	if err != nil {
		closeResourcePoolStore(store)
		h.sessionKeyJobs.releaseClaim(job)
		cancel()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reserve_failed", "message": "代理预留失败"})
		return
	}
	reservationByItem := make(map[string]resourcepool.ProxyReservation, len(reservations))
	for _, reservation := range reservations {
		reservationByItem[reservation.ItemID] = reservation
	}
	h.sessionKeyJobs.mu.Lock()
	for _, item := range job.Items {
		if item.Status != "queued" {
			item.SessionKey = ""
			continue
		}
		reservation, ok := reservationByItem[item.ID]
		if !ok {
			completeSessionKeyItem(item, "no_proxy", "no_proxy", "没有健康且空闲的代理")
			continue
		}
		item.ProxyID = reservation.ProxyResourceID
		proxy, errProxy := store.GetProxy(c.Request.Context(), reservation.ProxyResourceID)
		if errProxy == nil && proxy != nil {
			item.ProxyName = proxy.Name
			item.ProxyExitIP = proxy.ExitIP
		}
	}
	view := buildSessionKeyJobView(job)
	h.sessionKeyJobs.mu.Unlock()
	closeResourcePoolStore(store)
	h.writeSessionKeyJobLog(job, nil, "info", "session_key_job_created", "")
	resourcepool.PublishSessionKeyJobChanged(job.ID, "created")
	go h.runSessionKeyJob(job)
	c.JSON(http.StatusAccepted, gin.H{"job": view})
}

func (h *Handler) GetCurrentSessionKeyJob(c *gin.Context) {
	if h.sessionKeyJobs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	view, ok := h.sessionKeyJobs.view("", true)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": view})
}

func (h *Handler) GetSessionKeyJob(c *gin.Context) {
	if h.sessionKeyJobs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	view, ok := h.sessionKeyJobs.view(c.Param("id"), false)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": view})
}

func (h *Handler) CancelSessionKeyJob(c *gin.Context) {
	if h.sessionKeyJobs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	view, cancelledItems, ok := h.sessionKeyJobs.cancel(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	for _, itemID := range cancelledItems {
		h.releaseSessionKeyReservation(view.ID, itemID)
	}
	h.writeSessionKeyJobLog(&sessionKeyJob{ID: view.ID, Status: view.Status}, nil, "warn", "session_key_job_cancelling", "cancelled")
	resourcepool.PublishSessionKeyJobChanged(view.ID, "cancel")
	c.JSON(http.StatusOK, gin.H{"job": view})
}

func (h *Handler) runSessionKeyJob(job *sessionKeyJob) {
	now := time.Now().UTC()
	h.sessionKeyJobs.mu.Lock()
	job.Status = "running"
	job.StartedAt = &now
	queued := make([]int, 0, len(job.Items))
	for index, item := range job.Items {
		if item.Status == "queued" {
			queued = append(queued, index)
		}
	}
	h.sessionKeyJobs.mu.Unlock()
	resourcepool.PublishSessionKeyJobChanged(job.ID, "running")

	renewDone := make(chan struct{})
	go h.renewSessionKeyReservations(job, renewDone)
	queue := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < job.Concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range queue {
				h.processSessionKeyJobItem(job, index)
			}
		}()
	}
	producerDone := false
	for _, index := range queued {
		if job.ctx.Err() != nil {
			break
		}
		select {
		case <-job.ctx.Done():
			producerDone = true
		case queue <- index:
		}
		if producerDone {
			break
		}
	}
	close(queue)
	workers.Wait()
	close(renewDone)

	h.sessionKeyJobs.storageMu.Lock()
	store, err := h.openResourcePoolStoreForJob()
	if err == nil {
		if errRelease := store.ReleaseProxyReservations(context.Background(), job.ID); errRelease != nil {
			log.WithFields(log.Fields{"job_id": job.ID, "error": errRelease}).Warn("failed to release SessionKey job reservations")
		}
		closeResourcePoolStore(store)
	}
	h.sessionKeyJobs.storageMu.Unlock()
	completedAt := time.Now().UTC()
	h.sessionKeyJobs.mu.Lock()
	for _, item := range job.Items {
		if item.Status == "queued" {
			completeSessionKeyItem(item, "cancelled", "cancelled", "任务已取消")
		}
		item.SessionKey = ""
	}
	if job.Status == "cancelling" {
		job.Status = "cancelled"
	} else {
		job.Status = "completed"
	}
	job.CompletedAt = &completedAt
	if h.sessionKeyJobs.active == job {
		h.sessionKeyJobs.active = nil
	}
	h.sessionKeyJobs.mu.Unlock()
	h.triggerConfigReload(context.Background())
	h.writeSessionKeyJobLog(job, nil, "info", "session_key_job_completed", job.Status)
	resourcepool.PublishSessionKeyJobChanged(job.ID, job.Status)
	resourcepool.PublishStatsChanged("session_key_job")
}

func (h *Handler) processSessionKeyJobItem(job *sessionKeyJob, index int) {
	startedAt := time.Now().UTC()
	h.sessionKeyJobs.mu.Lock()
	item := job.Items[index]
	if item.Status != "queued" {
		h.sessionKeyJobs.mu.Unlock()
		return
	}
	item.Status = "running"
	item.StartedAt = &startedAt
	sessionKey := item.SessionKey
	proxyID := item.ProxyID
	h.sessionKeyJobs.mu.Unlock()
	resourcepool.PublishSessionKeyJobChanged(job.ID, "item_running")

	ctx, cancel := context.WithTimeout(context.Background(), sessionKeyItemTimeout)
	defer cancel()
	h.sessionKeyJobs.storageMu.Lock()
	store, err := h.openResourcePoolStoreForJob()
	if err != nil {
		h.sessionKeyJobs.storageMu.Unlock()
		h.failSessionKeyItem(job, item, "persistence_failed", "账号池存储不可用")
		return
	}
	proxy, err := store.GetProxy(ctx, proxyID)
	closeResourcePoolStore(store)
	h.sessionKeyJobs.storageMu.Unlock()
	if err != nil || proxy == nil {
		h.failSessionKeyItem(job, item, "proxy_error", "登录代理不可用")
		return
	}
	h.mu.Lock()
	cfg := h.cfg
	factory := h.sessionKeyAuthFactory
	h.mu.Unlock()
	if factory == nil {
		factory = defaultSessionKeyAuthenticatorFactory
	}
	bundle, err := factory(cfg, proxy.ProxyURL).Authenticate(ctx, sessionKey)
	sessionKey = ""
	if err != nil {
		code := classifySessionKeyAuthError(err)
		h.failSessionKeyItem(job, item, code, sessionKeyErrorMessage(code))
		return
	}
	accountUUID := strings.TrimSpace(bundle.TokenData.AccountUUID)
	h.sessionKeyJobs.storageMu.Lock()
	h.sessionKeyJobs.mu.Lock()
	claimedBy := job.claimed[accountUUID]
	h.sessionKeyJobs.mu.Unlock()
	if claimedBy != "" && claimedBy != item.ID {
		h.sessionKeyJobs.storageMu.Unlock()
		h.sessionKeyJobs.mu.Lock()
		completeSessionKeyItem(item, "duplicate_account", "duplicate_account", "批次内账号重复")
		item.SessionKey = ""
		h.sessionKeyJobs.mu.Unlock()
		h.releaseSessionKeyReservation(job.ID, item.ID)
		resourcepool.PublishSessionKeyJobChanged(job.ID, "item_completed")
		return
	}
	account, updated, err := h.persistSessionKeyAccount(ctx, job.PoolID, job.ID, item.ID, proxyID, bundle)
	if err == nil {
		h.sessionKeyJobs.mu.Lock()
		job.claimed[accountUUID] = item.ID
		h.sessionKeyJobs.mu.Unlock()
	}
	h.sessionKeyJobs.storageMu.Unlock()
	if err != nil {
		if errors.Is(err, resourcepool.ErrAccountInOtherPool) {
			h.failSessionKeyItem(job, item, "account_in_other_pool", "账号已属于其他账号池")
			return
		}
		log.WithFields(log.Fields{"job_id": job.ID, "item_index": item.Index, "proxy_id": item.ProxyID, "error": err}).Warn("failed to persist SessionKey OAuth account")
		h.failSessionKeyItem(job, item, "persistence_failed", "OAuth 已取得，但账号保存失败")
		return
	}
	h.sessionKeyJobs.mu.Lock()
	item.AccountID = account.ID
	item.AccountEmail = account.Email
	status := "success"
	if updated {
		status = "updated"
	}
	completeSessionKeyItem(item, status, "", "")
	item.SessionKey = ""
	h.sessionKeyJobs.mu.Unlock()
	resourcepool.PublishAccountChanged(account.ID, status)
	resourcepool.PublishProxyChanged(proxyID, "bind")
	h.writeSessionKeyJobLog(job, item, "info", "session_key_login_succeeded", "")
	resourcepool.PublishSessionKeyJobChanged(job.ID, "item_completed")
}

func (h *Handler) persistSessionKeyAccount(ctx context.Context, poolID, jobID, itemID, proxyID string, bundle *claudeauth.ClaudeAuthBundle) (*resourcepool.ClaudeCodeAccount, bool, error) {
	storage := &claudeauth.ClaudeTokenStorage{
		AccessToken: bundle.TokenData.AccessToken, RefreshToken: bundle.TokenData.RefreshToken,
		LastRefresh: bundle.LastRefresh, Email: bundle.TokenData.Email,
		OrganizationUUID: bundle.TokenData.OrganizationUUID, AccountUUID: bundle.TokenData.AccountUUID,
		Expire: bundle.TokenData.Expire,
	}
	authID := fmt.Sprintf("claude-%s.json", storage.Email)
	record := &coreauth.Auth{
		ID: authID, Provider: "claude", FileName: authID, Storage: storage,
		Metadata: map[string]any{
			"email": storage.Email, "resource_pool_account": true, "resource_pool_type": "claude-code",
			"resource_pool_id": poolID, "org_uuid": storage.OrganizationUUID, "account_uuid": storage.AccountUUID, "proxy_resource_id": proxyID,
		},
	}
	if h.postAuthHook != nil {
		if err := h.postAuthHook(ctx, record); err != nil {
			return nil, false, fmt.Errorf("post-auth hook: %w", err)
		}
	}
	store, err := h.openResourcePoolStoreForJob()
	if err != nil {
		return nil, false, err
	}
	defer closeResourcePoolStore(store)
	_, errExisting := store.GetAccountByAuthID(ctx, authID)
	updated := errExisting == nil
	if errExisting != nil && !errors.Is(errExisting, sql.ErrNoRows) {
		return nil, false, errExisting
	}
	account, err := store.RegisterClaudeCodeAccountWithAuthReservationInPool(ctx, poolID, authID, storage.Email, proxyID, record, jobID, itemID)
	if err != nil {
		return nil, false, err
	}
	if h.postAuthPersistHook != nil {
		if err := h.postAuthPersistHook(ctx, record); err != nil {
			return nil, updated, fmt.Errorf("post-auth persist hook: %w", err)
		}
	}
	return account, updated, nil
}

func (h *Handler) renewSessionKeyReservations(job *sessionKeyJob, done <-chan struct{}) {
	ticker := time.NewTicker(sessionKeyReservationTick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			h.sessionKeyJobs.storageMu.Lock()
			store, err := h.openResourcePoolStoreForJob()
			if err != nil {
				h.sessionKeyJobs.storageMu.Unlock()
				continue
			}
			err = store.RenewProxyReservations(context.Background(), job.ID, sessionKeyReservationTTL)
			closeResourcePoolStore(store)
			h.sessionKeyJobs.storageMu.Unlock()
			if err != nil {
				log.WithFields(log.Fields{"job_id": job.ID, "error": err}).Warn("failed to renew SessionKey job reservations")
			}
		}
	}
}

func (h *Handler) failSessionKeyItem(job *sessionKeyJob, item *sessionKeyJobItem, code, message string) {
	h.sessionKeyJobs.mu.Lock()
	completeSessionKeyItem(item, "failed", code, message)
	item.SessionKey = ""
	h.sessionKeyJobs.mu.Unlock()
	h.releaseSessionKeyReservation(job.ID, item.ID)
	log.WithFields(log.Fields{"job_id": job.ID, "item_index": item.Index, "fingerprint": item.Fingerprint, "proxy_id": item.ProxyID, "error_code": code}).Warn("SessionKey login item failed")
	h.writeSessionKeyJobLog(job, item, "warn", "session_key_login_failed", code)
	resourcepool.PublishSessionKeyJobChanged(job.ID, "item_completed")
}

func (h *Handler) releaseSessionKeyReservation(jobID, itemID string) {
	h.sessionKeyJobs.storageMu.Lock()
	defer h.sessionKeyJobs.storageMu.Unlock()
	store, err := h.openResourcePoolStoreForJob()
	if err != nil {
		return
	}
	defer closeResourcePoolStore(store)
	_ = store.ReleaseProxyReservation(context.Background(), jobID, itemID)
}

func (h *Handler) openResourcePoolStoreForJob() (*resourcepool.Store, error) {
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return nil, fmt.Errorf("resource pools disabled")
	}
	return resourcepool.Open(configPath, cfg)
}

func (h *Handler) writeSessionKeyJobLog(job *sessionKeyJob, item *sessionKeyJobItem, level, event, code string) {
	if h == nil || job == nil || h.sessionKeyJobs == nil {
		return
	}
	h.mu.Lock()
	cfg := h.cfg
	configPath := h.configFilePath
	h.mu.Unlock()
	entry := resourcepool.AccountPoolLogEntry{
		Level:   level,
		Event:   event,
		Details: map[string]any{},
	}
	h.sessionKeyJobs.mu.Lock()
	jobID := job.ID
	entry.Details["job_id"] = jobID
	entry.Details["status"] = job.Status
	if code != "" {
		entry.Details["error_code"] = code
	}
	if item != nil {
		entry.ProxyResourceID = item.ProxyID
		entry.Details["item_index"] = item.Index
		entry.Details["fingerprint"] = item.Fingerprint
		entry.Details["item_status"] = item.Status
	}
	h.sessionKeyJobs.mu.Unlock()
	h.sessionKeyJobs.storageMu.Lock()
	defer h.sessionKeyJobs.storageMu.Unlock()
	if err := resourcepool.WriteAccountPoolLog(context.Background(), configPath, cfg, entry); err != nil {
		log.WithFields(log.Fields{"job_id": jobID, "event": event, "error": err}).Debug("failed to write SessionKey job account-pool log")
	}
}

func completeSessionKeyItem(item *sessionKeyJobItem, status, code, message string) {
	now := time.Now().UTC()
	item.Status = status
	item.ErrorCode = code
	item.ErrorMessage = message
	item.CompletedAt = &now
}

func sessionKeyFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func classifySessionKeyAuthError(err error) string {
	var authErr *claudeauth.SessionKeyAuthError
	if errors.As(err, &authErr) && authErr.Code != "" {
		return authErr.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "proxy_error"
	}
	return "authorize_failed"
}

func sessionKeyErrorMessage(code string) string {
	switch code {
	case "invalid_session":
		return "SessionKey 已失效或无权授权"
	case "no_organization":
		return "账号没有可用组织"
	case "state_mismatch":
		return "OAuth state 校验失败"
	case "token_exchange_failed":
		return "OAuth token 换取失败"
	case "missing_refresh_token":
		return "OAuth 响应缺少 refresh token"
	case "proxy_error":
		return "代理或网络请求失败"
	case "authorize_failed":
		return "Claude 网页授权失败"
	default:
		return "自动授权失败"
	}
}
