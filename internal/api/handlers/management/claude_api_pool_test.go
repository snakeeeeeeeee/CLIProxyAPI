package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestClaudeAPIPoolItemTestUsesSelectedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotAuth string
	var gotHeader string
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotHeader = r.Header.Get("X-Account")
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = payload.Model
		if len(payload.Messages) != 1 || payload.Messages[0].Content != "hi" {
			t.Fatalf("messages = %#v, want hi", payload.Messages)
		}
		w.Header().Set("request-id", "req-1")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	poolPath := filepath.Join(dir, claudeapipool.DefaultFileName)
	doc := &claudeapipool.File{
		Version: 1,
		Defaults: claudeapipool.Defaults{
			BaseURL: upstream.URL,
			Headers: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items: []claudeapipool.Item{
			{APIKey: "key-1", Headers: map[string]string{"X-Account": "one"}},
			{APIKey: "key-2", Headers: map[string]string{"X-Account": "two"}, Models: []config.ClaudeModel{{Name: "claude-two", Alias: "two-alias"}}},
		},
	}
	if err := claudeapipool.Save(poolPath, doc); err != nil {
		t.Fatalf("save pool: %v", err)
	}

	body := map[string]string{
		"item_hash": claudeapipool.ItemHash(doc.Items[1]),
		"model":     "",
		"prompt":    "hi",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v0/management/claude-api-pool/items/2/test", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "position", Value: "2"}}
	ctx.Request = req

	h := NewHandler(&config.Config{}, configPath, nil)
	h.TestClaudeAPIPoolItem(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer key-2" {
		t.Fatalf("Authorization = %q, want Bearer key-2", gotAuth)
	}
	if gotHeader != "two" {
		t.Fatalf("X-Account = %q, want two", gotHeader)
	}
	if gotModel != "claude-two" {
		t.Fatalf("model = %q, want claude-two", gotModel)
	}
	var response claudeAPIPoolTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "ok" || response.StatusCode != http.StatusOK || response.Message != "ok" {
		t.Fatalf("response = %#v", response)
	}
	if response.Headers["request-id"] != "req-1" {
		t.Fatalf("headers = %#v, want request-id", response.Headers)
	}
}

func TestClaudeAPIPoolItemTestRejectsStaleHash(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	poolPath := filepath.Join(dir, claudeapipool.DefaultFileName)
	doc := &claudeapipool.File{
		Version: 1,
		Defaults: claudeapipool.Defaults{
			BaseURL: "https://api.example.test",
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items:  []claudeapipool.Item{{APIKey: "key-1"}},
	}
	if err := claudeapipool.Save(poolPath, doc); err != nil {
		t.Fatalf("save pool: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v0/management/claude-api-pool/items/1/test", bytes.NewReader([]byte(`{"item_hash":"stale"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "position", Value: "1"}}
	ctx.Request = req

	h := NewHandler(&config.Config{}, configPath, nil)
	h.TestClaudeAPIPoolItem(ctx)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
