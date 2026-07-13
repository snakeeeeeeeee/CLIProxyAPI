package resourcepool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type recordingAuthStore struct {
	items   []*coreauth.Auth
	deleted []string
	baseDir string
}

func (s *recordingAuthStore) List(context.Context) ([]*coreauth.Auth, error) {
	return append([]*coreauth.Auth(nil), s.items...), nil
}

func (s *recordingAuthStore) Save(context.Context, *coreauth.Auth) (string, error) {
	return "", nil
}

func (s *recordingAuthStore) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *recordingAuthStore) SetBaseDir(dir string) {
	s.baseDir = dir
}

func TestAuthStoreForwardsBaseDir(t *testing.T) {
	delegate := &recordingAuthStore{}
	store := NewAuthStore(delegate, "", nil)
	store.SetBaseDir("/tmp/auths")
	if delegate.baseDir != "/tmp/auths" {
		t.Fatalf("base dir = %q", delegate.baseDir)
	}
}

func TestAuthStoreListMigratesLegacyClaudeOAuth(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write init config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	legacy := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Attributes: map[string]string{
			coreauth.AttributeSourceBackend: coreauth.AuthSourceFile,
		},
		Metadata: map[string]any{
			"type":          "claude",
			"email":         "user@example.com",
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}
	delegate := &recordingAuthStore{items: []*coreauth.Auth{legacy}}
	store := NewAuthStore(delegate, configPath, cfg)

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || !isClaudeCodePoolAuth(items[0]) || !hasOAuthTokens(items[0]) {
		t.Fatalf("migrated auths = %+v", items)
	}
	if len(delegate.deleted) != 1 || delegate.deleted[0] != legacy.ID {
		t.Fatalf("deleted = %v, want %q", delegate.deleted, legacy.ID)
	}
	db, err := Open(configPath, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	accounts, err := db.ListAccounts(context.Background())
	if err != nil || len(accounts) != 1 || !accounts[0].HasAuthData {
		t.Fatalf("accounts = %+v, err = %v", accounts, err)
	}
}

func TestAuthStoreListDoesNotMigrateNonOAuthClaudeAuth(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write init config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	apiKey := &coreauth.Auth{
		ID:       "claude-api-key.json",
		Provider: "claude",
		Attributes: map[string]string{
			coreauth.AttributeSourceBackend: coreauth.AuthSourceFile,
		},
		Metadata: map[string]any{"type": "claude", "api_key": "secret"},
	}
	delegate := &recordingAuthStore{items: []*coreauth.Auth{apiKey}}
	items, err := NewAuthStore(delegate, configPath, cfg).List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0] != apiKey || len(delegate.deleted) != 0 {
		t.Fatalf("items = %+v, deleted = %v", items, delegate.deleted)
	}
}

func TestAuthStoreListKeepsLegacyOAuthWhenAccountPoolDisabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(filepath.Join(dir, "resource-pools.yaml"), []byte("database-path: resource-pools.db\nclaude-code-pool:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatalf("write init config: %v", err)
	}
	cfg := &config.Config{ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"}}
	legacy := &coreauth.Auth{
		ID:       "claude-disabled-pool.json",
		Provider: "claude",
		Attributes: map[string]string{
			coreauth.AttributeSourceBackend: coreauth.AuthSourceFile,
		},
		Metadata: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}
	delegate := &recordingAuthStore{items: []*coreauth.Auth{legacy}}
	items, err := NewAuthStore(delegate, configPath, cfg).List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0] != legacy || len(delegate.deleted) != 0 {
		t.Fatalf("items = %+v, deleted = %v", items, delegate.deleted)
	}
}
