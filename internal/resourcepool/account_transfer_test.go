package resourcepool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestSub2APIExportIncludesOAuthProxyAndRetainedSessionKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	enabled := true
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{
		Name: "bound", ProxyURL: "socks5://user:secret@127.0.0.1:19080", Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	expiresAt := time.Now().UTC().Add(8 * time.Hour).Truncate(time.Second)
	storage := &claudeauth.ClaudeTokenStorage{
		IDToken: "id-token", AccessToken: "access-token", RefreshToken: "refresh-token",
		LastRefresh: time.Now().UTC().Format(time.RFC3339), Email: "owner@example.com",
		OrganizationUUID: "org-id", AccountUUID: "account-id", Type: "claude", Expire: expiresAt.Format(time.RFC3339),
	}
	auth := &coreauth.Auth{ID: "claude-owner@example.com.json", Provider: "claude", Storage: storage, Metadata: map[string]any{"email": storage.Email}}
	account, err := store.RegisterClaudeCodeAccountWithCredentialOriginInPool(ctx, DefaultAccountPoolID, auth.ID, storage.Email, proxy.ID, auth, "", "", "session_key", "sk-ant-sid-retained")
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithCredentialOriginInPool() error = %v", err)
	}
	priority := 7
	note := "migration note"
	if _, err = store.PatchAccount(ctx, account.ID, AccountPatch{Priority: &priority, Note: &note}); err != nil {
		t.Fatalf("PatchAccount() error = %v", err)
	}
	capacity, err := store.GetAccountCapacity(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccountCapacity() error = %v", err)
	}
	capacity.ConcurrencyLimit = 4
	if _, err = store.SaveAccountCapacity(ctx, account.ID, *capacity); err != nil {
		t.Fatalf("SaveAccountCapacity() error = %v", err)
	}

	payload, err := store.ExportSub2APIData(ctx, DefaultAccountPoolID, []string{account.ID}, true)
	if err != nil {
		t.Fatalf("ExportSub2APIData() error = %v", err)
	}
	if payload.Type != Sub2APIDataType || payload.Version != Sub2APIDataVersion || len(payload.Accounts) != 1 || len(payload.Proxies) != 1 {
		t.Fatalf("unexpected export payload: %+v", payload)
	}
	gotAccount := payload.Accounts[0]
	if gotAccount.Platform != "anthropic" || gotAccount.Type != "oauth" || gotAccount.Concurrency != 4 || gotAccount.Priority != priority {
		t.Fatalf("exported account = %+v", gotAccount)
	}
	if gotAccount.Credentials["access_token"] != "access-token" || gotAccount.Credentials["refresh_token"] != "refresh-token" {
		t.Fatalf("exported credentials = %#v", gotAccount.Credentials)
	}
	if gotAccount.Credentials["expires_at"] != expiresAt.Format(time.RFC3339) || gotAccount.Credentials["expired"] != expiresAt.Format(time.RFC3339) {
		t.Fatalf("exported expiry = %#v", gotAccount.Credentials)
	}
	if gotAccount.ExpiresAt != nil {
		t.Fatalf("account expires_at = %v, want nil because OAuth expiry belongs in credentials", *gotAccount.ExpiresAt)
	}
	gotProxy := payload.Proxies[0]
	if gotProxy.Protocol != "socks5" || gotProxy.Username != "user" || gotProxy.Password != "secret" || gotAccount.ProxyKey == nil || *gotAccount.ProxyKey != gotProxy.ProxyKey {
		t.Fatalf("exported proxy/account link = %+v / %+v", gotProxy, gotAccount.ProxyKey)
	}
	keys, unavailable, err := store.ExportSessionKeys(ctx, DefaultAccountPoolID, []string{account.ID})
	if err != nil || unavailable != 0 || len(keys) != 1 || keys[0] != "sk-ant-sid-retained" {
		t.Fatalf("ExportSessionKeys() = %v, %d, %v", keys, unavailable, err)
	}
	stored, err := store.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if stored.LoginSource != "session_key" || !stored.HasSessionKey {
		t.Fatalf("account origin = %q/%v", stored.LoginSource, stored.HasSessionKey)
	}
	normalJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("Marshal(account) error = %v", err)
	}
	if strings.Contains(string(normalJSON), "sk-ant-sid-retained") {
		t.Fatal("normal account JSON exposed retained SessionKey")
	}
}

func TestSessionKeyExportReportsLegacyOAuthAccountUnavailable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	account, err := store.RegisterClaudeCodeAccountWithAuthInPool(ctx, DefaultAccountPoolID, "claude-legacy.json", "legacy@example.com", "", &coreauth.Auth{
		ID: "claude-legacy.json", Provider: "claude", Storage: &claudeauth.ClaudeTokenStorage{AccessToken: "access", RefreshToken: "refresh"},
	})
	if err != nil {
		t.Fatalf("RegisterClaudeCodeAccountWithAuthInPool() error = %v", err)
	}
	keys, unavailable, err := store.ExportSessionKeys(ctx, DefaultAccountPoolID, []string{account.ID})
	if err != nil || len(keys) != 0 || unavailable != 1 {
		t.Fatalf("ExportSessionKeys() = %v, %d, %v", keys, unavailable, err)
	}
}
