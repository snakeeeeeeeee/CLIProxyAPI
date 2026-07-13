package management

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestListAuthFilesHidesClaudeCodeAccountPoolAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{
		{
			ID:       "pool-account",
			Provider: "claude",
			Attributes: map[string]string{
				claudeapipool.AttrOAuthPool: "true",
				coreauth.AttributePath:      "/tmp/pool-account.json",
			},
		},
		{
			ID:       "ordinary-account",
			Provider: "claude",
			Attributes: map[string]string{
				coreauth.AttributePath: "/tmp/ordinary-account.json",
			},
		},
	} {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth: %v", err)
		}
	}
	handler := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/v0/management/auth-files", nil)

	handler.ListAuthFiles(ctx)

	var body struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Files) != 1 || body.Files[0]["id"] != "ordinary-account" {
		t.Fatalf("files = %+v", body.Files)
	}
}

func TestAccountRuntimeEntriesIncludeSQLiteBackedPoolAuth(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID:       "sqlite-pool-account",
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			claudeapipool.AttrOAuthPool: "true",
		},
		Success: 7,
		Failed:  1,
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	handler := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)

	entries := handler.authEntryByID()
	entry, ok := entries[auth.ID]
	if !ok {
		t.Fatalf("SQLite-backed account-pool auth missing from runtime entries: %+v", entries)
	}
	if got := entry["success"]; got != int64(7) {
		t.Fatalf("success = %v, want 7", got)
	}
	if got := entry["failed"]; got != int64(1) {
		t.Fatalf("failed = %v, want 1", got)
	}
}
