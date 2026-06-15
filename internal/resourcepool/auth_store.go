package resourcepool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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
		out = append(out, items...)
	}
	items, err := ListStoredAuths(ctx, s.configPath, s.cfg)
	if err != nil {
		return nil, err
	}
	return append(out, items...), nil
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
