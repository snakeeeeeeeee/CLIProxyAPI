package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestImportSub2APIRoundTripPreservesProxyAndPoolIsolation(t *testing.T) {
	h, store := newSessionKeyJobTestHandler(t, 0)
	ctx := context.Background()
	proxyKey := "http|127.0.0.1|19090|user|secret"
	payload := resourcepool.Sub2APIDataPayload{
		Type: resourcepool.Sub2APIDataType, Version: resourcepool.Sub2APIDataVersion, ExportedAt: "2026-07-26T00:00:00Z",
		Proxies: []resourcepool.Sub2APIDataProxy{{
			ProxyKey: proxyKey, Name: "sub2api proxy", Protocol: "http", Host: "127.0.0.1", Port: 19090,
			Username: "user", Password: "secret", Status: "active",
		}},
		Accounts: []resourcepool.Sub2APIDataAccount{{
			Name: "import@example.com", Platform: "anthropic", Type: "oauth", ProxyKey: &proxyKey, Concurrency: 3, Priority: 2,
			Credentials: map[string]any{
				"access_token": "import-access", "refresh_token": "import-refresh", "email": "import@example.com",
				"account_uuid": "account-import", "org_uuid": "org-import", "expires_at": "2026-08-01T00:00:00Z",
			},
		}},
	}
	result := h.importSub2APIData(ctx, store, resourcepool.DefaultAccountPoolID, payload)
	if result.AccountCreated != 1 || result.AccountFailed != 0 || result.ProxyCreated != 1 || result.ProxyFailed != 0 {
		t.Fatalf("import result = %+v", result)
	}
	accounts, err := store.ListAccountsByPool(ctx, resourcepool.DefaultAccountPoolID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ListAccountsByPool() = %d, %v", len(accounts), err)
	}
	account := accounts[0]
	if account.Proxy == nil || !strings.Contains(account.Proxy.ProxyURL, "127.0.0.1:19090") || account.LoginSource != "oauth_import" || account.HasSessionKey {
		t.Fatalf("imported account = %+v", account)
	}
	capacity, err := store.GetAccountCapacity(ctx, account.ID)
	if err != nil || capacity.ConcurrencyLimit != 3 {
		t.Fatalf("capacity = %+v, %v", capacity, err)
	}
	exported, err := store.ExportSub2APIData(ctx, resourcepool.DefaultAccountPoolID, nil, true)
	if err != nil || len(exported.Accounts) != 1 || len(exported.Proxies) != 1 {
		t.Fatalf("round-trip export = %+v, %v", exported, err)
	}
	if exported.Accounts[0].Credentials["refresh_token"] != "import-refresh" || exported.Accounts[0].ProxyKey == nil || *exported.Accounts[0].ProxyKey != exported.Proxies[0].ProxyKey {
		t.Fatalf("round-trip payload = %+v", exported)
	}

	otherPool, err := store.CreateAccountPool(ctx, "other", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	hookCalls := 0
	h.postAuthHook = func(context.Context, *coreauth.Auth) error {
		hookCalls++
		return nil
	}
	conflict := h.importSub2APIData(ctx, store, otherPool.ID, payload)
	if conflict.AccountFailed != 1 || conflict.AccountCreated != 0 || len(conflict.Errors) == 0 || conflict.Errors[0].Message != "账号已属于其他账号池" {
		t.Fatalf("cross-pool conflict result = %+v", conflict)
	}
	if hookCalls != 0 {
		t.Fatalf("post-auth hook calls = %d, want 0 for a rejected cross-pool import", hookCalls)
	}
}

func TestImportSub2APIReusesExistingAccountByEmailAndRetainsSessionKey(t *testing.T) {
	h, store := newSessionKeyJobTestHandler(t, 0)
	ctx := context.Background()
	storage := &claudeauth.ClaudeTokenStorage{
		AccessToken: "old-access", RefreshToken: "old-refresh", Email: "same@example.com",
		AccountUUID: "same-account", Type: "claude",
	}
	record := &coreauth.Auth{ID: "claude-same@example.com.json", Provider: "claude", Storage: storage}
	original, err := store.RegisterClaudeCodeAccountWithCredentialOriginInPool(
		ctx, resourcepool.DefaultAccountPoolID, record.ID, storage.Email, "", record, "", "", "session_key", "sk-ant-sid-original",
	)
	if err != nil {
		t.Fatalf("register original account: %v", err)
	}
	payload := resourcepool.Sub2APIDataPayload{
		Type: resourcepool.Sub2APIDataType, Version: resourcepool.Sub2APIDataVersion,
		Proxies: []resourcepool.Sub2APIDataProxy{},
		Accounts: []resourcepool.Sub2APIDataAccount{{
			Name: "same@example.com", Platform: "anthropic", Type: "oauth", Concurrency: 2,
			Credentials: map[string]any{
				"access_token": "new-access", "refresh_token": "new-refresh",
				"email": "same@example.com", "account_uuid": "same-account",
			},
		}},
	}
	result := h.importSub2APIData(ctx, store, resourcepool.DefaultAccountPoolID, payload)
	if result.AccountUpdated != 1 || result.AccountCreated != 0 || result.AccountFailed != 0 {
		t.Fatalf("import result = %+v", result)
	}
	accounts, err := store.ListAccountsByPool(ctx, resourcepool.DefaultAccountPoolID)
	if err != nil || len(accounts) != 1 || accounts[0].ID != original.ID {
		t.Fatalf("accounts after import = %+v, %v", accounts, err)
	}
	keys, unavailable, err := store.ExportSessionKeys(ctx, resourcepool.DefaultAccountPoolID, []string{original.ID})
	if err != nil || unavailable != 0 || len(keys) != 1 || keys[0] != "sk-ant-sid-original" {
		t.Fatalf("retained SessionKey = %v unavailable=%d err=%v", keys, unavailable, err)
	}
	exported, err := store.ExportSub2APIData(ctx, resourcepool.DefaultAccountPoolID, []string{original.ID}, false)
	if err != nil || exported.Accounts[0].Credentials["access_token"] != "new-access" {
		t.Fatalf("updated OAuth export = %+v, %v", exported, err)
	}
}

func TestAuthFromSub2APIAccountDoesNotTreatAccountExpiryAsTokenExpiry(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Unix()
	record, _, err := authFromSub2APIAccount(resourcepool.Sub2APIDataAccount{
		Name: "expiry@example.com", Platform: "anthropic", Type: "oauth", ExpiresAt: &expiresAt,
		Credentials: map[string]any{"access_token": "access", "refresh_token": "refresh"},
	}, resourcepool.DefaultAccountPoolID, "")
	if err != nil {
		t.Fatalf("authFromSub2APIAccount() error = %v", err)
	}
	storage, ok := record.Storage.(*claudeauth.ClaudeTokenStorage)
	if !ok {
		t.Fatalf("storage type = %T", record.Storage)
	}
	if storage.Expire != "" {
		t.Fatalf("OAuth token expiry = %q, want empty", storage.Expire)
	}
}

func TestAuthFromSub2APIAccountNormalizesNumericTokenExpiry(t *testing.T) {
	record, _, err := authFromSub2APIAccount(resourcepool.Sub2APIDataAccount{
		Name: "numeric-expiry@example.com", Platform: "anthropic", Type: "oauth",
		Credentials: map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_at": float64(1785532800)},
	}, resourcepool.DefaultAccountPoolID, "")
	if err != nil {
		t.Fatalf("authFromSub2APIAccount() error = %v", err)
	}
	storage := record.Storage.(*claudeauth.ClaudeTokenStorage)
	if storage.Expire != "1785532800" {
		t.Fatalf("OAuth token expiry = %q, want unix seconds", storage.Expire)
	}
}

func TestAccountTransferHandlersUseNoStoreAndSeparateSessionKeyExport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, store := newSessionKeyJobTestHandler(t, 0)
	ctx := context.Background()
	storage := &claudeauth.ClaudeTokenStorage{
		AccessToken: "oauth-access", RefreshToken: "oauth-refresh", Email: "export@example.com", Type: "claude",
	}
	record := &coreauth.Auth{ID: "claude-export@example.com.json", Provider: "claude", Storage: storage}
	account, err := store.RegisterClaudeCodeAccountWithCredentialOriginInPool(
		ctx, resourcepool.DefaultAccountPoolID, record.ID, storage.Email, "", record, "", "", "session_key", "sk-ant-sid-export",
	)
	if err != nil {
		t.Fatalf("register account: %v", err)
	}

	oauthBody, _ := json.Marshal(accountTransferRequest{PoolID: resourcepool.DefaultAccountPoolID, IDs: []string{account.ID}})
	oauthRecorder := httptest.NewRecorder()
	oauthContext, _ := gin.CreateTestContext(oauthRecorder)
	oauthContext.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oauthBody))
	oauthContext.Request.Header.Set("Content-Type", "application/json")
	h.ExportClaudeCodeAccounts(oauthContext)
	if oauthRecorder.Code != http.StatusOK || oauthRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("OAuth export status=%d cache=%q body=%s", oauthRecorder.Code, oauthRecorder.Header().Get("Cache-Control"), oauthRecorder.Body.String())
	}
	if strings.Contains(oauthRecorder.Body.String(), "sk-ant-sid-export") || !strings.Contains(oauthRecorder.Body.String(), "oauth-refresh") {
		t.Fatalf("OAuth export credential separation failed: %s", oauthRecorder.Body.String())
	}

	keyRecorder := httptest.NewRecorder()
	keyContext, _ := gin.CreateTestContext(keyRecorder)
	keyContext.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oauthBody))
	keyContext.Request.Header.Set("Content-Type", "application/json")
	h.ExportClaudeCodeSessionKeys(keyContext)
	if keyRecorder.Code != http.StatusOK || keyRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("SessionKey export status=%d cache=%q body=%s", keyRecorder.Code, keyRecorder.Header().Get("Cache-Control"), keyRecorder.Body.String())
	}
	if got := keyRecorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("SessionKey Content-Type = %q", got)
	}
	if keyRecorder.Body.String() != "sk-ant-sid-export\n" {
		t.Fatalf("SessionKey body = %q", keyRecorder.Body.String())
	}
}

func TestImportHandlerRejectsMissingPoolBeforeCreatingProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, store := newSessionKeyJobTestHandler(t, 0)
	payload := resourcepool.Sub2APIDataPayload{
		Type: resourcepool.Sub2APIDataType, Version: resourcepool.Sub2APIDataVersion,
		Proxies: []resourcepool.Sub2APIDataProxy{{
			ProxyKey: "http|127.0.0.1|19090||", Name: "should-not-exist", Protocol: "http", Host: "127.0.0.1", Port: 19090, Status: "active",
		}},
		Accounts: []resourcepool.Sub2APIDataAccount{},
	}
	body, _ := json.Marshal(accountTransferImportRequest{Data: payload})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.ImportClaudeCodeAccounts(c)
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
	proxies, err := store.ListProxies(context.Background())
	if err != nil || len(proxies) != 0 {
		t.Fatalf("proxies after rejected import = %+v, %v", proxies, err)
	}
}

func TestImportSub2APIExpiredProxyIsDisabled(t *testing.T) {
	h, store := newSessionKeyJobTestHandler(t, 0)
	result := h.importSub2APIData(context.Background(), store, resourcepool.DefaultAccountPoolID, resourcepool.Sub2APIDataPayload{
		Proxies: []resourcepool.Sub2APIDataProxy{{
			ProxyKey: "socks5|127.0.0.1|19091||", Name: "expired", Protocol: "socks5", Host: "127.0.0.1", Port: 19091, Status: "expired",
		}},
		Accounts: []resourcepool.Sub2APIDataAccount{},
	})
	if result.ProxyCreated != 1 || result.ProxyFailed != 0 {
		t.Fatalf("import result = %+v", result)
	}
	proxies, err := store.ListProxies(context.Background())
	if err != nil || len(proxies) != 1 || proxies[0].Enabled {
		t.Fatalf("imported proxies = %+v, %v", proxies, err)
	}
}
