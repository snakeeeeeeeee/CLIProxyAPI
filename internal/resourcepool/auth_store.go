package resourcepool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// AuthStore routes Claude Code account-pool auth persistence to resource-pools.db
// while delegating all other auth records to the existing auth store.
type AuthStore struct {
	delegate   coreauth.Store
	configPath string
	cfg        *config.Config
}

// NewAuthStore creates a store wrapper for SQLite-backed Claude Code pool auths.
func NewAuthStore(delegate coreauth.Store, configPath string, cfg *config.Config) *AuthStore {
	return &AuthStore{delegate: delegate, configPath: configPath, cfg: cfg}
}

// SetBaseDir forwards file-store directory configuration through the wrapper.
func (s *AuthStore) SetBaseDir(dir string) {
	if s == nil || s.delegate == nil {
		return
	}
	if setter, ok := s.delegate.(interface{ SetBaseDir(string) }); ok {
		setter.SetBaseDir(dir)
	}
}

// List returns delegated auths plus SQLite-backed Claude Code account-pool auths.
func (s *AuthStore) List(ctx context.Context) ([]*coreauth.Auth, error) {
	var out []*coreauth.Auth
	if s == nil {
		return out, nil
	}
	if s.delegate != nil {
		items, err := s.delegate.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, auth := range items {
			if !isLegacyClaudeOAuthFile(auth) {
				out = append(out, auth)
				continue
			}
			migrated, errMigrate := s.migrateLegacyClaudeOAuth(ctx, auth)
			if errMigrate != nil {
				log.WithError(errMigrate).WithField("auth_id", auth.ID).Warn("legacy Claude OAuth migration skipped")
				out = append(out, auth)
				continue
			}
			if !migrated {
				out = append(out, auth)
				continue
			}
			if migrated && s.delegate != nil {
				if errDelete := s.delegate.Delete(ctx, auth.ID); errDelete != nil {
					log.WithError(errDelete).WithField("auth_id", auth.ID).Warn("legacy Claude OAuth migrated but source cleanup failed")
				}
			}
		}
	}
	items, err := ListStoredAuths(ctx, s.configPath, s.cfg)
	if err != nil {
		return nil, err
	}
	return mergeAuthsByID(out, items), nil
}

func (s *AuthStore) migrateLegacyClaudeOAuth(ctx context.Context, auth *coreauth.Auth) (bool, error) {
	if s == nil || auth == nil {
		return false, nil
	}
	store, err := Open(s.configPath, s.cfg)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = store.Close()
	}()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return false, err
	}
	if !EffectiveClaudeCodePool(doc.ClaudeCode).Enabled {
		return false, nil
	}
	migrating := auth.Clone()
	if migrating.Attributes == nil {
		migrating.Attributes = make(map[string]string)
	}
	migrating.Attributes[AttrClaudeOAuthPool] = "true"
	migrating.Attributes[claudeapipool.AttrOAuthPool] = "true"
	account, err := store.RegisterClaudeCodeAccountWithAuth(ctx, migrating.ID, authEmail(migrating), "", migrating)
	if err != nil {
		return false, fmt.Errorf("persist legacy Claude OAuth: %w", err)
	}
	stored, err := GetStoredAuth(ctx, s.configPath, s.cfg, account.ID)
	if err != nil {
		return false, fmt.Errorf("verify migrated Claude OAuth: %w", err)
	}
	if !hasOAuthTokens(stored) {
		return false, fmt.Errorf("verify migrated Claude OAuth: token data is incomplete")
	}
	log.WithFields(log.Fields{
		"auth_id":    migrating.ID,
		"account_id": account.ID,
	}).Info("legacy Claude OAuth migrated to account pool SQLite")
	return true, nil
}

func isLegacyClaudeOAuthFile(auth *coreauth.Auth) bool {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
		return false
	}
	if coreauth.IsPluginVirtualAuth(auth) || isClaudeCodePoolAuth(auth) {
		return false
	}
	if auth.Attributes != nil {
		if strings.EqualFold(strings.TrimSpace(auth.Attributes[claudeapipool.AttrPool]), "true") {
			return false
		}
		if backend := strings.TrimSpace(auth.Attributes[coreauth.AttributeSourceBackend]); backend != coreauth.AuthSourceFile {
			return false
		}
	}
	return hasOAuthTokens(auth)
}

func hasOAuthTokens(auth *coreauth.Auth) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	accessToken, _ := auth.Metadata["access_token"].(string)
	refreshToken, _ := auth.Metadata["refresh_token"].(string)
	return strings.TrimSpace(accessToken) != "" && strings.TrimSpace(refreshToken) != ""
}

func mergeAuthsByID(groups ...[]*coreauth.Auth) []*coreauth.Auth {
	out := make([]*coreauth.Auth, 0)
	index := make(map[string]int)
	for _, group := range groups {
		for _, auth := range group {
			if auth == nil {
				continue
			}
			id := strings.TrimSpace(auth.ID)
			if position, exists := index[id]; id != "" && exists {
				out[position] = auth
				continue
			}
			if id != "" {
				index[id] = len(out)
			}
			out = append(out, auth)
		}
	}
	return out
}

// Save persists pool auths to SQLite and delegates all other auths.
func (s *AuthStore) Save(ctx context.Context, auth *coreauth.Auth) (string, error) {
	if s == nil {
		return "", fmt.Errorf("resource pool auth store unavailable")
	}
	if isClaudeCodePoolAuth(auth) {
		store, err := Open(s.configPath, s.cfg)
		if err != nil {
			return "", err
		}
		defer func() {
			_ = store.Close()
		}()
		if err := store.SaveClaudeCodeAccountAuth(ctx, auth); err != nil {
			if strings.TrimSpace(auth.ID) != "" && isNoRows(err) {
				_, err = store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, authEmail(auth), "", auth)
			}
			if err != nil {
				return "", err
			}
		}
		return "resource-pools.db:" + strings.TrimSpace(auth.ID), nil
	}
	if s.delegate == nil {
		return "", fmt.Errorf("resource pool auth store delegate unavailable")
	}
	return s.delegate.Save(ctx, auth)
}

// Delete removes pool auths from SQLite when possible, otherwise delegates.
func (s *AuthStore) Delete(ctx context.Context, id string) error {
	if s == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	store, err := Open(s.configPath, s.cfg)
	if err == nil && store != nil {
		account, errAccount := store.GetAccountByAuthID(ctx, id)
		if errAccount == nil && account != nil {
			errDelete := store.DeleteAccount(ctx, account.ID)
			_ = store.Close()
			return errDelete
		}
		_ = store.Close()
	}
	if s.delegate == nil {
		return nil
	}
	return s.delegate.Delete(ctx, id)
}

func isClaudeCodePoolAuth(auth *coreauth.Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(auth.Attributes[AttrClaudeOAuthPool]), "true")
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
