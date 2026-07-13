package resourcepool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// AccountRoutingScope returns the isolated in-memory scheduler namespace for a pool.
func AccountRoutingScope(poolID string) string {
	return coreexecutor.ClaudeAccountPoolRoutingScope(normalizeAccountPoolID(poolID))
}

var (
	ErrDefaultPoolImmutable = errors.New("default account pool is immutable")
	ErrAccountPoolNotEmpty  = errors.New("account pool is not empty")
	ErrAccountPoolArchived  = errors.New("account pool is archived")
	ErrAccountInOtherPool   = errors.New("account belongs to another pool")
	ErrAccountMoveInFlight  = errors.New("account has in-flight requests")
)

// ListAccountPools returns active pools and optionally archived history rows.
func (s *Store) ListAccountPools(ctx context.Context, includeArchived bool) ([]ClaudeCodeAccountPool, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	query := `
	SELECT id, name, description, enabled, is_default, config_json, archived_at, created_at, updated_at
FROM claude_code_pools
WHERE archived_at IS NULL
ORDER BY is_default DESC, lower(name) ASC`
	if includeArchived {
		query = `
	SELECT id, name, description, enabled, is_default, config_json, archived_at, created_at, updated_at
FROM claude_code_pools
ORDER BY archived_at IS NOT NULL, is_default DESC, lower(name) ASC`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list claude code account pools: %w", err)
	}
	defer func() { _ = rows.Close() }()
	pools := make([]ClaudeCodeAccountPool, 0)
	for rows.Next() {
		pool, errScan := scanAccountPool(rows)
		if errScan != nil {
			return nil, errScan
		}
		pools = append(pools, pool)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code account pools: %w", err)
	}
	return pools, nil
}

// GetAccountPool returns one account pool by ID.
func (s *Store) GetAccountPool(ctx context.Context, id string) (*ClaudeCodeAccountPool, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	id = normalizeAccountPoolID(id)
	row := s.db.QueryRowContext(ctx, `
	SELECT id, name, description, enabled, is_default, config_json, archived_at, created_at, updated_at
FROM claude_code_pools
WHERE id = ?`, id)
	pool, err := scanAccountPool(row)
	if err != nil {
		return nil, err
	}
	return &pool, nil
}

// RequireActiveAccountPool resolves an empty ID to default and validates availability.
func (s *Store) RequireActiveAccountPool(ctx context.Context, id string) (*ClaudeCodeAccountPool, error) {
	pool, err := s.GetAccountPool(ctx, normalizeAccountPoolID(id))
	if err != nil {
		return nil, err
	}
	if pool.ArchivedAt != nil {
		return nil, ErrAccountPoolArchived
	}
	return pool, nil
}

// CreateAccountPool creates an active custom pool.
func (s *Store) CreateAccountPool(ctx context.Context, name, description string) (*ClaudeCodeAccountPool, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	name, description, err := normalizeAccountPoolFields(name, description)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(name, DefaultAccountPoolID) {
		return nil, fmt.Errorf("account pool name %q is reserved", DefaultAccountPoolID)
	}
	id := uuid.NewString()
	now := dbTime(time.Now())
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_pools(id, name, description, enabled, is_default, created_at, updated_at)
VALUES(?, ?, ?, 1, 0, ?, ?)`, id, name, description, now, now); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code account pool")
	}
	return s.GetAccountPool(ctx, id)
}

// PatchAccountPool updates a custom pool or the default pool's non-name fields.
func (s *Store) PatchAccountPool(ctx context.Context, id string, patch ClaudeCodeAccountPoolPatch) (*ClaudeCodeAccountPool, error) {
	current, err := s.GetAccountPool(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.ArchivedAt != nil {
		return nil, ErrAccountPoolArchived
	}
	name := current.Name
	description := current.Description
	enabled := current.Enabled
	if patch.Name != nil {
		requested := strings.TrimSpace(*patch.Name)
		if current.IsDefault && !strings.EqualFold(requested, DefaultAccountPoolID) {
			return nil, ErrDefaultPoolImmutable
		}
		name = requested
	}
	if patch.Description != nil {
		description = strings.TrimSpace(*patch.Description)
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	name, description, err = normalizeAccountPoolFields(name, description)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_pools
SET name = ?, description = ?, enabled = ?, updated_at = ?
WHERE id = ?`, name, description, boolInt(enabled), dbTime(time.Now()), current.ID); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code account pool")
	}
	return s.GetAccountPool(ctx, current.ID)
}

// ArchiveAccountPool archives an empty custom pool while preserving history.
func (s *Store) ArchiveAccountPool(ctx context.Context, id string) error {
	pool, err := s.GetAccountPool(ctx, id)
	if err != nil {
		return err
	}
	if pool.IsDefault || pool.ID == DefaultAccountPoolID {
		return ErrDefaultPoolImmutable
	}
	if pool.ArchivedAt != nil {
		return nil
	}
	var accounts int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_code_accounts WHERE pool_id = ?`, pool.ID).Scan(&accounts); err != nil {
		return fmt.Errorf("count account pool members: %w", err)
	}
	if accounts > 0 {
		return ErrAccountPoolNotEmpty
	}
	var activeKeys int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_code_pool_api_keys WHERE pool_id = ? AND revoked_at IS NULL`, pool.ID).Scan(&activeKeys); err != nil {
		return fmt.Errorf("count account pool api keys: %w", err)
	}
	if activeKeys > 0 {
		return ErrAccountPoolNotEmpty
	}
	now := dbTime(time.Now())
	res, err := s.db.ExecContext(ctx, `UPDATE claude_code_pools SET enabled = 0, archived_at = ?, updated_at = ? WHERE id = ?`, now, now, pool.ID)
	if err != nil {
		return fmt.Errorf("archive claude code account pool: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MoveAccountToPool moves an idle account and clears source-pool runtime state.
func (s *Store) MoveAccountToPool(ctx context.Context, accountID, targetPoolID string) (*ClaudeCodeAccount, error) {
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	target, err := s.RequireActiveAccountPool(ctx, targetPoolID)
	if err != nil {
		return nil, err
	}
	if account.PoolID == target.ID {
		return account, nil
	}
	if account.RuntimeCapacity != nil && account.RuntimeCapacity.InFlight > 0 {
		return nil, ErrAccountMoveInFlight
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin move account pool member: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE claude_code_accounts SET pool_id = ?, updated_at = ? WHERE id = ?`, target.ID, dbTime(time.Now()), account.ID); err != nil {
		return nil, fmt.Errorf("move account pool member: %w", err)
	}
	if err := insertEventTx(ctx, tx, "account.move", "claude code account moved", map[string]string{"account_id": account.ID, "from_pool_id": account.PoolID, "pool_id": target.ID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit move account pool member: %w", err)
	}
	oldScope := AccountRoutingScope(account.PoolID)
	claudeapipool.ResetScopedRouteCooling(oldScope, account.AuthID)
	claudeapipool.ClearScopedAccountBindings(oldScope, account.AuthID)
	claudeapipool.ClearScopedAccountQuotaRouting(oldScope, account.AuthID)
	updated, err := s.GetAccount(ctx, account.ID)
	if err == nil {
		ApplyAccountLifecycleRouting(updated)
	}
	return updated, err
}

func normalizeAccountPoolID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultAccountPoolID
	}
	return id
}

func normalizeAccountPoolFields(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return "", "", fmt.Errorf("account pool name is required")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", "", fmt.Errorf("account pool name exceeds 64 characters")
	}
	if utf8.RuneCountInString(description) > 500 {
		return "", "", fmt.Errorf("account pool description exceeds 500 characters")
	}
	return name, description, nil
}

func scanAccountPool(row interface {
	Scan(dest ...interface{}) error
}) (ClaudeCodeAccountPool, error) {
	var pool ClaudeCodeAccountPool
	var enabled, isDefault int
	var archived sql.NullString
	var createdRaw, updatedRaw string
	if err := row.Scan(&pool.ID, &pool.Name, &pool.Description, &enabled, &isDefault, &pool.configJSON, &archived, &createdRaw, &updatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return pool, sql.ErrNoRows
		}
		return pool, fmt.Errorf("scan claude code account pool: %w", err)
	}
	pool.Enabled = enabled != 0
	pool.IsDefault = isDefault != 0
	if overrides, err := decodeAccountPoolConfigOverrides(pool.configJSON); err == nil {
		pool.ConfigOverrideCount = countAccountPoolConfigOverrides(overrides)
		pool.HasConfigOverride = pool.ConfigOverrideCount > 0
	}
	pool.ArchivedAt = parseNullTime(archived)
	pool.CreatedAt = parseDBTime(createdRaw)
	pool.UpdatedAt = parseDBTime(updatedRaw)
	return pool, nil
}
