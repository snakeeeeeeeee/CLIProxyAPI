package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
)

type fakeSessionKeyAuthenticator struct {
	tracker *sessionKeyConcurrencyTracker
}

type sessionKeyConcurrencyTracker struct {
	mu      sync.Mutex
	active  int
	maximum int
}

type fixedAccountSessionKeyAuthenticator struct{}

func (fixedAccountSessionKeyAuthenticator) Authenticate(_ context.Context, _ string) (*claudeauth.ClaudeAuthBundle, error) {
	return &claudeauth.ClaudeAuthBundle{
		TokenData: claudeauth.ClaudeTokenData{
			AccessToken: "access", RefreshToken: "refresh", Email: "same@example.com",
			OrganizationUUID: "org", AccountUUID: "same-account", Expire: time.Now().Add(time.Hour).Format(time.RFC3339),
		},
		LastRefresh: time.Now().Format(time.RFC3339),
	}, nil
}

func (a fakeSessionKeyAuthenticator) Authenticate(_ context.Context, key string) (*claudeauth.ClaudeAuthBundle, error) {
	a.tracker.mu.Lock()
	a.tracker.active++
	if a.tracker.active > a.tracker.maximum {
		a.tracker.maximum = a.tracker.active
	}
	a.tracker.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	a.tracker.mu.Lock()
	a.tracker.active--
	a.tracker.mu.Unlock()
	suffix := key[len(key)-1:]
	return &claudeauth.ClaudeAuthBundle{
		TokenData: claudeauth.ClaudeTokenData{
			AccessToken: "access-" + suffix, RefreshToken: "refresh-" + suffix,
			Email: "owner-" + suffix + "@example.com", OrganizationUUID: "org-" + suffix,
			AccountUUID: "account-" + suffix, Expire: time.Now().Add(time.Hour).Format(time.RFC3339),
		},
		LastRefresh: time.Now().Format(time.RFC3339),
	}, nil
}

func TestSessionKeyJobLimitsConcurrencyAndDoesNotExposeSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, store := newSessionKeyJobTestHandler(t, 2)
	tracker := &sessionKeyConcurrencyTracker{}
	h.sessionKeyAuthFactory = func(*config.Config, string) sessionKeyAuthenticator {
		return fakeSessionKeyAuthenticator{tracker: tracker}
	}
	keys := []string{"sk-ant-sid-secret-a", "sk-ant-sid-secret-b", "sk-ant-sid-secret-c", "sk-ant-sid-secret-a"}
	body, _ := json.Marshal(map[string]any{"session_keys": keys, "concurrency": 2})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateSessionKeyJob(c)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("CreateSessionKeyJob status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, key := range keys {
		if strings.Contains(recorder.Body.String(), key) {
			t.Fatalf("response contains SessionKey %q", key)
		}
	}
	var response struct {
		Job sessionKeyJobView `json:"job"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	job := waitForSessionKeyJob(t, h, response.Job.ID)
	if job.Succeeded != 2 || job.NoProxy != 1 || job.Failed != 1 {
		t.Fatalf("job summary = %+v, want success=2 no_proxy=1 duplicate failure=1", job)
	}
	tracker.mu.Lock()
	maximum := tracker.maximum
	tracker.mu.Unlock()
	if maximum > 2 {
		t.Fatalf("maximum concurrency = %d, want <= 2", maximum)
	}
	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("account count = %d, want 2", len(accounts))
	}
	for _, item := range h.sessionKeyJobs.latest.Items {
		if item.SessionKey != "" {
			t.Fatalf("completed item %d retained SessionKey", item.Index)
		}
	}
	logPath := filepath.Join(filepath.Dir(store.Path()), "acc-pool-logs", "account-pool.log")
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read account pool log: %v", err)
	}
	for _, key := range keys {
		if bytes.Contains(logBody, []byte(key)) {
			t.Fatalf("account pool log contains SessionKey %q", key)
		}
	}
}

func TestSessionKeyJobRejectsSecondActiveJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newSessionKeyJobTestHandler(t, 1)
	tracker := &sessionKeyConcurrencyTracker{}
	h.sessionKeyAuthFactory = func(*config.Config, string) sessionKeyAuthenticator {
		return fakeSessionKeyAuthenticator{tracker: tracker}
	}
	first := createSessionKeyJobRequest(t, h, []string{"sk-ant-sid-first-a"}, 1)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	second := createSessionKeyJobRequest(t, h, []string{"sk-ant-sid-second-b"}, 1)
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s, want 409", second.Code, second.Body.String())
	}
	var payload struct {
		Job sessionKeyJobView `json:"job"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	_ = waitForSessionKeyJob(t, h, payload.Job.ID)
}

func TestSessionKeyJobCancelImmediatelyClearsQueuedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := newSessionKeyJobManager()
	job := &sessionKeyJob{
		ID: "job-cancel", Status: "running", ctx: ctx, Cancel: cancel,
		Items: []*sessionKeyJobItem{
			{ID: "item-1", Status: "running", SessionKey: "sk-ant-sid-running"},
			{ID: "item-2", Status: "queued", SessionKey: "sk-ant-sid-queued"},
		},
	}
	if !manager.claim(job) {
		t.Fatal("claim() = false")
	}
	view, cancelledItems, ok := manager.cancel(job.ID)
	if !ok || view.Status != "cancelling" {
		t.Fatalf("cancel() = %+v, %v, want cancelling", view, ok)
	}
	if len(cancelledItems) != 1 || cancelledItems[0] != "item-2" {
		t.Fatalf("cancelled items = %v, want item-2", cancelledItems)
	}
	if job.Items[1].Status != "cancelled" || job.Items[1].SessionKey != "" {
		t.Fatalf("queued item after cancel = %+v", job.Items[1])
	}
	if job.Items[0].Status != "running" || job.Items[0].SessionKey == "" {
		t.Fatalf("running item changed during cancel = %+v", job.Items[0])
	}
}

func TestSessionKeyJobOnlyPersistsFirstSuccessfulDuplicateAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, store := newSessionKeyJobTestHandler(t, 2)
	h.sessionKeyAuthFactory = func(*config.Config, string) sessionKeyAuthenticator {
		return fixedAccountSessionKeyAuthenticator{}
	}
	response := createSessionKeyJobRequest(t, h, []string{"sk-ant-sid-same-a", "sk-ant-sid-same-b"}, 2)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Job sessionKeyJobView `json:"job"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	job := waitForSessionKeyJob(t, h, payload.Job.ID)
	if job.Succeeded != 1 || job.Failed != 1 {
		t.Fatalf("job summary = %+v, want success=1 failed=1", job)
	}
	duplicateCount := 0
	for _, item := range job.Items {
		if item.Status == "duplicate_account" {
			duplicateCount++
		}
	}
	if duplicateCount != 1 {
		t.Fatalf("duplicate account count = %d, want 1", duplicateCount)
	}
	accounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("account count = %d, want 1", len(accounts))
	}
}

func TestSessionKeyJobManagerRetainsCompletedJobsByID(t *testing.T) {
	manager := newSessionKeyJobManager()
	now := time.Now().UTC()
	first := &sessionKeyJob{ID: "first", Status: "completed", CreatedAt: now.Add(-time.Minute), CompletedAt: &now}
	if !manager.claim(first) {
		t.Fatal("claim(first) = false")
	}
	manager.active = nil
	secondCompleted := now.Add(time.Second)
	second := &sessionKeyJob{ID: "second", Status: "completed", CreatedAt: now, CompletedAt: &secondCompleted}
	if !manager.claim(second) {
		t.Fatal("claim(second) = false")
	}
	manager.active = nil
	if _, ok := manager.view(first.ID, false); !ok {
		t.Fatal("view(first) not retained")
	}
	current, ok := manager.view("", true)
	if !ok || current.ID != second.ID {
		t.Fatalf("current = %+v, %v, want second", current, ok)
	}
}

func newSessionKeyJobTestHandler(t *testing.T, proxyCount int) (*Handler, *resourcepool.Store) {
	t.Helper()
	dir := t.TempDir()
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	h := NewHandler(cfg, filepath.Join(dir, "config.yaml"), nil)
	store, err := resourcepool.Open(filepath.Join(dir, "config.yaml"), cfg)
	if err != nil {
		t.Fatalf("resourcepool.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for index := 0; index < proxyCount; index++ {
		proxy, err := store.CreateProxy(context.Background(), resourcepool.ProxyResourceSeed{ProxyURL: fmt.Sprintf("http://127.0.0.1:%d", 19000+index)})
		if err != nil {
			t.Fatalf("CreateProxy() error = %v", err)
		}
		if _, err := store.UpdateProxyHealth(context.Background(), proxy.ID, true, time.Millisecond, nil, 1); err != nil {
			t.Fatalf("UpdateProxyHealth() error = %v", err)
		}
	}
	return h, store
}

func createSessionKeyJobRequest(t *testing.T, h *Handler, keys []string, concurrency int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"session_keys": keys, "concurrency": concurrency})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.CreateSessionKeyJob(c)
	return recorder
}

func waitForSessionKeyJob(t *testing.T, h *Handler, id string) sessionKeyJobView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, ok := h.sessionKeyJobs.view(id, false)
		if ok && (view.Status == "completed" || view.Status == "cancelled") {
			return *view
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for SessionKey job")
	return sessionKeyJobView{}
}
