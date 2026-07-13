package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type accountPoolLegacyTestProvider struct{ key string }

func (p accountPoolLegacyTestProvider) Identifier() string { return "legacy-test" }

func (p accountPoolLegacyTestProvider) Authenticate(_ context.Context, req *http.Request) (*sdkaccess.Result, *sdkaccess.AuthError) {
	value := strings.TrimSpace(req.Header.Get("X-Api-Key"))
	if value == "" {
		value = strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	}
	if value == "" {
		return nil, sdkaccess.NewNoCredentialsError()
	}
	if value != p.key {
		return nil, sdkaccess.NewInvalidCredentialError()
	}
	return &sdkaccess.Result{Provider: p.Identifier(), Principal: value}, nil
}

func newAccountPoolAuthTestServer(t *testing.T) (*Server, *resourcepool.Store, *resourcepool.ClaudeCodeAccountPool, *resourcepool.ClaudeCodePoolAPIKeyCredential) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	resourceConfig := filepath.Join(tmpDir, "resource-pools.yaml")
	if err := os.WriteFile(resourceConfig, []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource pool config: %v", err)
	}
	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"legacy-key"}},
		AuthDir:   authDir,
		Debug:     true,
		ResourcePools: proxyconfig.ResourcePoolsConfig{
			Enabled:    true,
			ConfigFile: resourceConfig,
		},
	}
	accessManager := sdkaccess.NewManager()
	accessManager.SetProviders([]sdkaccess.Provider{accountPoolLegacyTestProvider{key: "legacy-key"}})
	server := NewServer(cfg, auth.NewManager(nil, nil, nil), accessManager, filepath.Join(tmpDir, "config.yaml"))
	store, err := resourcepool.Open(server.configFilePath, cfg)
	if err != nil {
		t.Fatalf("open resource pool store: %v", err)
	}
	pool, err := store.CreateAccountPool(context.Background(), "tenant-a", "")
	if err != nil {
		_ = store.Close()
		t.Fatalf("create account pool: %v", err)
	}
	credential, err := store.CreatePoolAPIKey(context.Background(), pool.ID, "tenant-key")
	if err != nil {
		_ = store.Close()
		t.Fatalf("create pool API key: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return server, store, pool, credential
}

func TestClaudeAccountPoolGeneratedAndLegacyKeyIdentity(t *testing.T) {
	server, _, pool, credential := newAccountPoolAuthTestServer(t)
	router := gin.New()
	router.Use(server.claudeAccountPoolAuthMiddleware())
	router.GET("/probe", func(c *gin.Context) {
		metadata, _ := c.Get("accessMetadata")
		billing := coreusage.AccountPoolBillingFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{
			"metadata":         metadata,
			"pool_id":          billing.PoolID,
			"api_key_id":       billing.APIKeyID,
			"price_version_id": billing.PriceVersionID,
			"user_key":         c.GetString("userApiKey"),
		})
	})

	generatedReq := httptest.NewRequest(http.MethodGet, "/probe", nil)
	generatedReq.Header.Set("Authorization", "Bearer "+credential.Secret)
	generatedRec := httptest.NewRecorder()
	router.ServeHTTP(generatedRec, generatedReq)
	if generatedRec.Code != http.StatusOK {
		t.Fatalf("generated key status = %d body=%s", generatedRec.Code, generatedRec.Body.String())
	}
	body := generatedRec.Body.String()
	if !strings.Contains(body, `"pool_id":"`+pool.ID+`"`) || !strings.Contains(body, `"api_key_id":"`+credential.Item.ID+`"`) {
		t.Fatalf("generated key identity body=%s", body)
	}
	if strings.Contains(body, credential.Secret) || !strings.Contains(body, credential.Item.KeyPrefix) {
		t.Fatalf("generated key secret handling body=%s", body)
	}
	if strings.Contains(body, `"price_version_id":0`) {
		t.Fatalf("generated key did not pin pricing revision: %s", body)
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/probe", nil)
	legacyReq.Header.Set("X-Api-Key", "legacy-key")
	legacyRec := httptest.NewRecorder()
	router.ServeHTTP(legacyRec, legacyReq)
	if legacyRec.Code != http.StatusOK || !strings.Contains(legacyRec.Body.String(), `"pool_id":"default"`) || !strings.Contains(legacyRec.Body.String(), `"api_key_id":""`) {
		t.Fatalf("legacy key response status=%d body=%s", legacyRec.Code, legacyRec.Body.String())
	}
}

func TestGeneratedAccountPoolKeyRequiresSupportedHeader(t *testing.T) {
	secret := "sk-cap-test.secret"
	tests := []struct {
		name          string
		authorization string
		xAPIKey       string
		query         string
		want          string
	}{
		{name: "bearer", authorization: "Bearer " + secret, want: secret},
		{name: "case insensitive bearer", authorization: "bearer " + secret, want: secret},
		{name: "x api key", xAPIKey: secret, want: secret},
		{name: "raw authorization rejected", authorization: secret},
		{name: "basic rejected", authorization: "Basic " + secret},
		{name: "query rejected", query: "?api_key=" + secret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/claude-acc-pool/v1/models"+test.query, nil)
			if test.authorization != "" {
				req.Header.Set("Authorization", test.authorization)
			}
			if test.xAPIKey != "" {
				req.Header.Set("X-Api-Key", test.xAPIKey)
			}
			if got := generatedAccountPoolKeyFromRequest(req); got != test.want {
				t.Fatalf("generatedAccountPoolKeyFromRequest() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGeneratedPoolKeyCannotEscapeRouteOrLifecycle(t *testing.T) {
	server, store, pool, credential := newAccountPoolAuthTestServer(t)

	ordinaryReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ordinaryReq.Header.Set("Authorization", "Bearer "+credential.Secret)
	ordinaryRec := httptest.NewRecorder()
	server.engine.ServeHTTP(ordinaryRec, ordinaryReq)
	if ordinaryRec.Code != http.StatusUnauthorized {
		t.Fatalf("generated key ordinary route status=%d body=%s", ordinaryRec.Code, ordinaryRec.Body.String())
	}

	queryReq := httptest.NewRequest(http.MethodGet, "/claude-acc-pool/v1/models?key="+credential.Secret, nil)
	queryRec := httptest.NewRecorder()
	server.engine.ServeHTTP(queryRec, queryReq)
	if queryRec.Code != http.StatusUnauthorized {
		t.Fatalf("generated key query status=%d body=%s", queryRec.Code, queryRec.Body.String())
	}

	enabled := false
	if _, err := store.PatchAccountPool(context.Background(), pool.ID, resourcepool.ClaudeCodeAccountPoolPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	disabledReq := httptest.NewRequest(http.MethodGet, "/claude-acc-pool/v1/models", nil)
	disabledReq.Header.Set("X-Api-Key", credential.Secret)
	disabledRec := httptest.NewRecorder()
	server.engine.ServeHTTP(disabledRec, disabledReq)
	if disabledRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled pool status=%d body=%s", disabledRec.Code, disabledRec.Body.String())
	}

	enabled = true
	if _, err := store.PatchAccountPool(context.Background(), pool.ID, resourcepool.ClaudeCodeAccountPoolPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("enable pool: %v", err)
	}
	if err := store.RevokePoolAPIKey(context.Background(), credential.Item.ID); err != nil {
		t.Fatalf("revoke pool key: %v", err)
	}
	revokedReq := httptest.NewRequest(http.MethodGet, "/claude-acc-pool/v1/models", nil)
	revokedReq.Header.Set("X-Api-Key", credential.Secret)
	revokedRec := httptest.NewRecorder()
	server.engine.ServeHTTP(revokedRec, revokedReq)
	if revokedRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status=%d body=%s", revokedRec.Code, revokedRec.Body.String())
	}
}
