package resourcepool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const poolAPIKeyPrefix = "sk-cap-"

const permanentPoolAPIKeysMigrationMarker = "account_pool_permanent_api_keys_v8"

var (
	ErrPoolAPIKeyInvalid           = errors.New("invalid account pool api key")
	ErrPoolAPIKeyRevoked           = errors.New("account pool api key is revoked")
	ErrPoolAPIKeySecretUnavailable = errors.New("account pool api key secret is unavailable; rotate the key to make it viewable")
)

// CreatePoolAPIKey creates a permanent high-entropy pool credential.
func (s *Store) CreatePoolAPIKey(ctx context.Context, poolID, name string) (*ClaudeCodePoolAPIKeyCredential, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	pool, err := s.RequireActiveAccountPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	name, err = normalizePoolAPIKeyName(name)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	secret, prefix, hash, err := generatePoolAPIKey(id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_pool_api_keys(id, pool_id, name, key_prefix, key_hash, key_secret, enabled, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, 1, ?, ?)`, id, pool.ID, name, prefix, hash, secret, dbTime(now), dbTime(now)); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code pool api key")
	}
	item, err := s.GetPoolAPIKey(ctx, id)
	if err != nil {
		return nil, err
	}
	invalidatePoolAPIKeyCache(id)
	return &ClaudeCodePoolAPIKeyCredential{Item: *item, Secret: secret}, nil
}

// ListPoolAPIKeys returns safe key metadata, optionally scoped to one pool.
func (s *Store) ListPoolAPIKeys(ctx context.Context, poolID string, includeRevoked bool) ([]ClaudeCodePoolAPIKey, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	poolID = strings.TrimSpace(poolID)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, pool_id, name, key_prefix, CASE WHEN TRIM(key_secret) <> '' THEN 1 ELSE 0 END, enabled, expires_at, revoked_at, last_used_at, created_at, updated_at
FROM claude_code_pool_api_keys
WHERE (? = '' OR pool_id = ?) AND (? = 1 OR revoked_at IS NULL)
ORDER BY revoked_at IS NOT NULL, created_at DESC`, poolID, poolID, boolInt(includeRevoked))
	if err != nil {
		return nil, fmt.Errorf("list account pool api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]ClaudeCodePoolAPIKey, 0)
	for rows.Next() {
		item, errScan := scanPoolAPIKey(rows)
		if errScan != nil {
			return nil, errScan
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account pool api keys: %w", err)
	}
	return items, nil
}

// GetPoolAPIKey returns safe key metadata by ID.
func (s *Store) GetPoolAPIKey(ctx context.Context, id string) (*ClaudeCodePoolAPIKey, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("api key id is required")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, pool_id, name, key_prefix, CASE WHEN TRIM(key_secret) <> '' THEN 1 ELSE 0 END, enabled, expires_at, revoked_at, last_used_at, created_at, updated_at
FROM claude_code_pool_api_keys WHERE id = ?`, id)
	item, err := scanPoolAPIKey(row)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PatchPoolAPIKey updates key metadata without changing the secret.
func (s *Store) PatchPoolAPIKey(ctx context.Context, id string, patch ClaudeCodePoolAPIKeyPatch) (*ClaudeCodePoolAPIKey, error) {
	current, err := s.GetPoolAPIKey(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.RevokedAt != nil {
		return nil, ErrPoolAPIKeyRevoked
	}
	name := current.Name
	enabled := current.Enabled
	if patch.Name != nil {
		name, err = normalizePoolAPIKeyName(*patch.Name)
		if err != nil {
			return nil, err
		}
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET name = ?, enabled = ?, expires_at = NULL, updated_at = ? WHERE id = ?`, name, boolInt(enabled), dbTime(time.Now()), current.ID); err != nil {
		return nil, fmt.Errorf("update account pool api key: %w", err)
	}
	invalidatePoolAPIKeyCache(current.ID)
	return s.GetPoolAPIKey(ctx, current.ID)
}

// RevokePoolAPIKey permanently invalidates one key while retaining usage history.
func (s *Store) RevokePoolAPIKey(ctx context.Context, id string) error {
	item, err := s.GetPoolAPIKey(ctx, id)
	if err != nil {
		return err
	}
	if item.RevokedAt != nil {
		return nil
	}
	now := dbTime(time.Now())
	if _, err := s.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET enabled = 0, key_secret = '', revoked_at = ?, updated_at = ? WHERE id = ?`, now, now, item.ID); err != nil {
		return fmt.Errorf("revoke account pool api key: %w", err)
	}
	invalidatePoolAPIKeyCache(item.ID)
	return nil
}

// RotatePoolAPIKey atomically replaces a key secret and invalidates the previous value.
func (s *Store) RotatePoolAPIKey(ctx context.Context, id string) (*ClaudeCodePoolAPIKeyCredential, error) {
	item, err := s.GetPoolAPIKey(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.RevokedAt != nil {
		return nil, ErrPoolAPIKeyRevoked
	}
	secret, prefix, hash, err := generatePoolAPIKey(item.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET key_prefix = ?, key_hash = ?, key_secret = ?, enabled = 1, expires_at = NULL, updated_at = ? WHERE id = ?`, prefix, hash, secret, dbTime(time.Now()), item.ID); err != nil {
		return nil, fmt.Errorf("rotate account pool api key: %w", err)
	}
	invalidatePoolAPIKeyCache(item.ID)
	updated, err := s.GetPoolAPIKey(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	return &ClaudeCodePoolAPIKeyCredential{Item: *updated, Secret: secret}, nil
}

// GetPoolAPIKeySecret returns a generated key only for an explicit management request.
func (s *Store) GetPoolAPIKeySecret(ctx context.Context, id string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("resource pool store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("api key id is required")
	}
	var secret, hash string
	var revoked sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT key_secret, key_hash, revoked_at FROM claude_code_pool_api_keys WHERE id = ?`, id).Scan(&secret, &hash, &revoked); err != nil {
		return "", err
	}
	if revoked.Valid {
		return "", ErrPoolAPIKeyRevoked
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ErrPoolAPIKeySecretUnavailable
	}
	digest := sha256.Sum256([]byte(secret))
	stored, err := hex.DecodeString(hash)
	if err != nil || len(stored) != len(digest) || subtle.ConstantTimeCompare(stored, digest[:]) != 1 {
		return "", fmt.Errorf("stored account pool api key secret does not match its hash")
	}
	return secret, nil
}

// AuthenticatePoolAPIKey validates a generated key and returns its key and pool metadata.
func (s *Store) AuthenticatePoolAPIKey(ctx context.Context, raw string) (*ClaudeCodePoolAPIKey, *ClaudeCodeAccountPool, error) {
	raw = strings.TrimSpace(raw)
	id, ok := poolAPIKeyID(raw)
	if !ok {
		return nil, nil, ErrPoolAPIKeyInvalid
	}
	var hash string
	row := s.db.QueryRowContext(ctx, `
SELECT k.id, k.pool_id, k.name, k.key_prefix, CASE WHEN TRIM(k.key_secret) <> '' THEN 1 ELSE 0 END, k.key_hash, k.enabled, k.expires_at, k.revoked_at, k.last_used_at, k.created_at, k.updated_at,
       p.id, p.name, p.description, p.enabled, p.is_default, p.archived_at, p.created_at, p.updated_at
FROM claude_code_pool_api_keys k
JOIN claude_code_pools p ON p.id = k.pool_id
WHERE k.id = ?`, id)
	var item ClaudeCodePoolAPIKey
	var pool ClaudeCodeAccountPool
	var secretAvailable, keyEnabled, poolEnabled, isDefault int
	var expires, revoked, lastUsed, poolArchived sql.NullString
	var keyCreated, keyUpdated, poolCreated, poolUpdated string
	if err := row.Scan(&item.ID, &item.PoolID, &item.Name, &item.KeyPrefix, &secretAvailable, &hash, &keyEnabled, &expires, &revoked, &lastUsed, &keyCreated, &keyUpdated,
		&pool.ID, &pool.Name, &pool.Description, &poolEnabled, &isDefault, &poolArchived, &poolCreated, &poolUpdated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrPoolAPIKeyInvalid
		}
		return nil, nil, fmt.Errorf("authenticate account pool api key: %w", err)
	}
	computed := sha256.Sum256([]byte(raw))
	stored, err := hex.DecodeString(hash)
	if err != nil || len(stored) != len(computed) || subtle.ConstantTimeCompare(stored, computed[:]) != 1 {
		return nil, nil, ErrPoolAPIKeyInvalid
	}
	item.SecretAvailable = secretAvailable != 0
	item.Enabled = keyEnabled != 0
	item.ExpiresAt = parseNullTime(expires)
	item.RevokedAt = parseNullTime(revoked)
	item.LastUsedAt = parseNullTime(lastUsed)
	item.CreatedAt = parseDBTime(keyCreated)
	item.UpdatedAt = parseDBTime(keyUpdated)
	pool.Enabled = poolEnabled != 0
	pool.IsDefault = isDefault != 0
	pool.ArchivedAt = parseNullTime(poolArchived)
	pool.CreatedAt = parseDBTime(poolCreated)
	pool.UpdatedAt = parseDBTime(poolUpdated)
	now := time.Now()
	if item.RevokedAt != nil || !item.Enabled {
		return nil, nil, ErrPoolAPIKeyRevoked
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET last_used_at = ?, updated_at = updated_at WHERE id = ?`, dbTime(now), item.ID); err == nil {
		item.LastUsedAt = &now
	}
	return &item, &pool, nil
}

func generatePoolAPIKey(id string) (string, string, string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", "", fmt.Errorf("generate account pool api key: %w", err)
	}
	raw := poolAPIKeyPrefix + id + "." + base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(raw))
	shortID := strings.ReplaceAll(id, "-", "")
	if len(shortID) > 10 {
		shortID = shortID[:10]
	}
	return raw, poolAPIKeyPrefix + shortID + "...", hex.EncodeToString(digest[:]), nil
}

func poolAPIKeyID(raw string) (string, bool) {
	if !strings.HasPrefix(raw, poolAPIKeyPrefix) {
		return "", false
	}
	remainder := strings.TrimPrefix(raw, poolAPIKeyPrefix)
	parts := strings.SplitN(remainder, ".", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func normalizePoolAPIKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("api key name is required")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", fmt.Errorf("api key name exceeds 64 characters")
	}
	return name, nil
}

func (s *Store) migratePermanentPoolAPIKeysV8(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	var marker string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, permanentPoolAPIKeysMigrationMarker).Scan(&marker); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read permanent api key migration marker: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin permanent api key migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET expires_at = NULL WHERE expires_at IS NOT NULL`); err != nil {
		return fmt.Errorf("clear legacy api key expiry: %w", err)
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pool_config(key, value, created_at, updated_at)
VALUES(?, '1', ?, ?)
ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at
	`, permanentPoolAPIKeysMigrationMarker, now, now); err != nil {
		return fmt.Errorf("write permanent api key migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit permanent api key migration: %w", err)
	}
	return nil
}

func scanPoolAPIKey(row interface {
	Scan(dest ...interface{}) error
}) (ClaudeCodePoolAPIKey, error) {
	var item ClaudeCodePoolAPIKey
	var secretAvailable, enabled int
	var expires, revoked, lastUsed sql.NullString
	var createdRaw, updatedRaw string
	if err := row.Scan(&item.ID, &item.PoolID, &item.Name, &item.KeyPrefix, &secretAvailable, &enabled, &expires, &revoked, &lastUsed, &createdRaw, &updatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return item, sql.ErrNoRows
		}
		return item, fmt.Errorf("scan account pool api key: %w", err)
	}
	item.SecretAvailable = secretAvailable != 0
	item.Enabled = enabled != 0
	item.ExpiresAt = parseNullTime(expires)
	item.RevokedAt = parseNullTime(revoked)
	item.LastUsedAt = parseNullTime(lastUsed)
	item.CreatedAt = parseDBTime(createdRaw)
	item.UpdatedAt = parseDBTime(updatedRaw)
	return item, nil
}

// Cache hooks are no-ops until the route-local authenticator cache is enabled.
func invalidatePoolAPIKeyCache(string) {}
