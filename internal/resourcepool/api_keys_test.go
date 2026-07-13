package resourcepool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPoolAPIKeyLifecyclePersistsRevealableSecret(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	credential, err := store.CreatePoolAPIKey(ctx, DefaultAccountPoolID, "client-a")
	if err != nil {
		t.Fatalf("CreatePoolAPIKey() error = %v", err)
	}
	if !strings.HasPrefix(credential.Secret, poolAPIKeyPrefix) || strings.Contains(credential.Item.KeyPrefix, credential.Secret) {
		t.Fatalf("credential = %+v", credential)
	}
	if !credential.Item.SecretAvailable {
		t.Fatal("created key should report its secret as available")
	}
	var storedHash, storedSecret string
	if err := store.db.QueryRowContext(ctx, `SELECT key_hash, key_secret FROM claude_code_pool_api_keys WHERE id = ?`, credential.Item.ID).Scan(&storedHash, &storedSecret); err != nil {
		t.Fatalf("read stored key: %v", err)
	}
	if strings.Contains(storedHash, credential.Secret) || len(storedHash) != 64 {
		t.Fatalf("stored hash = %q", storedHash)
	}
	if storedSecret != credential.Secret {
		t.Fatalf("stored secret = %q, want generated secret", storedSecret)
	}
	revealed, err := store.GetPoolAPIKeySecret(ctx, credential.Item.ID)
	if err != nil || revealed != credential.Secret {
		t.Fatalf("GetPoolAPIKeySecret() = %q, %v", revealed, err)
	}
	encoded, err := json.Marshal(credential.Item)
	if err != nil {
		t.Fatalf("marshal management item: %v", err)
	}
	if strings.Contains(string(encoded), credential.Secret) {
		t.Fatalf("management item leaked secret: %s", encoded)
	}
	item, pool, err := store.AuthenticatePoolAPIKey(ctx, credential.Secret)
	if err != nil {
		t.Fatalf("AuthenticatePoolAPIKey() error = %v", err)
	}
	if item.ID != credential.Item.ID || pool.ID != DefaultAccountPoolID || item.LastUsedAt == nil {
		t.Fatalf("authenticated item=%+v pool=%+v", item, pool)
	}
	rotated, err := store.RotatePoolAPIKey(ctx, item.ID)
	if err != nil {
		t.Fatalf("RotatePoolAPIKey() error = %v", err)
	}
	if rotated.Secret == credential.Secret {
		t.Fatal("rotated secret did not change")
	}
	revealed, err = store.GetPoolAPIKeySecret(ctx, item.ID)
	if err != nil || revealed != rotated.Secret {
		t.Fatalf("GetPoolAPIKeySecret() after rotate = %q, %v", revealed, err)
	}
	if _, _, err := store.AuthenticatePoolAPIKey(ctx, credential.Secret); !errors.Is(err, ErrPoolAPIKeyInvalid) {
		t.Fatalf("old secret auth error = %v", err)
	}
	if err := store.RevokePoolAPIKey(ctx, item.ID); err != nil {
		t.Fatalf("RevokePoolAPIKey() error = %v", err)
	}
	if _, _, err := store.AuthenticatePoolAPIKey(ctx, rotated.Secret); !errors.Is(err, ErrPoolAPIKeyRevoked) {
		t.Fatalf("revoked secret auth error = %v", err)
	}
	if _, err := store.GetPoolAPIKeySecret(ctx, item.ID); !errors.Is(err, ErrPoolAPIKeyRevoked) {
		t.Fatalf("revoked secret reveal error = %v", err)
	}
	var revokedSecret string
	if err := store.db.QueryRowContext(ctx, `SELECT key_secret FROM claude_code_pool_api_keys WHERE id = ?`, item.ID).Scan(&revokedSecret); err != nil {
		t.Fatalf("read revoked secret: %v", err)
	}
	if revokedSecret != "" {
		t.Fatal("revocation should clear the persisted secret")
	}
}

func TestPoolAPIKeyLegacySecretRequiresRotation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	credential, err := store.CreatePoolAPIKey(ctx, DefaultAccountPoolID, "legacy")
	if err != nil {
		t.Fatalf("CreatePoolAPIKey() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET key_secret = '' WHERE id = ?`, credential.Item.ID); err != nil {
		t.Fatalf("clear stored secret: %v", err)
	}
	if _, err := store.GetPoolAPIKeySecret(ctx, credential.Item.ID); !errors.Is(err, ErrPoolAPIKeySecretUnavailable) {
		t.Fatalf("legacy reveal error = %v", err)
	}
	items, err := store.ListPoolAPIKeys(ctx, DefaultAccountPoolID, false)
	if err != nil {
		t.Fatalf("ListPoolAPIKeys() error = %v", err)
	}
	if len(items) != 1 || items[0].SecretAvailable {
		t.Fatalf("legacy item = %+v", items)
	}
	rotated, err := store.RotatePoolAPIKey(ctx, credential.Item.ID)
	if err != nil {
		t.Fatalf("RotatePoolAPIKey() error = %v", err)
	}
	if !rotated.Item.SecretAvailable {
		t.Fatal("rotated legacy key should become viewable")
	}
}

func TestPoolAPIKeysArePermanentAndPoolArchiveProtection(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	pool, err := store.CreateAccountPool(ctx, "Team Key", "")
	if err != nil {
		t.Fatalf("CreateAccountPool() error = %v", err)
	}
	credential, err := store.CreatePoolAPIKey(ctx, pool.ID, "permanent")
	if err != nil {
		t.Fatalf("CreatePoolAPIKey() error = %v", err)
	}
	if err := store.ArchiveAccountPool(ctx, pool.ID); !errors.Is(err, ErrAccountPoolNotEmpty) {
		t.Fatalf("ArchiveAccountPool(active key) error = %v", err)
	}
	past := dbTime(time.Now().Add(-time.Minute))
	if _, err := store.db.ExecContext(ctx, `UPDATE claude_code_pool_api_keys SET expires_at = ? WHERE id = ?`, past, credential.Item.ID); err != nil {
		t.Fatalf("expire key: %v", err)
	}
	if _, _, err := store.AuthenticatePoolAPIKey(ctx, credential.Secret); err != nil {
		t.Fatalf("legacy expired key should remain permanent: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM pool_config WHERE key = ?`, permanentPoolAPIKeysMigrationMarker); err != nil {
		t.Fatalf("delete permanent key migration marker: %v", err)
	}
	if err := store.migratePermanentPoolAPIKeysV8(ctx); err != nil {
		t.Fatalf("migratePermanentPoolAPIKeysV8() error = %v", err)
	}
	var expiryCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_code_pool_api_keys WHERE id = ? AND expires_at IS NOT NULL`, credential.Item.ID).Scan(&expiryCount); err != nil {
		t.Fatalf("read migrated key expiry: %v", err)
	}
	if expiryCount != 0 {
		t.Fatalf("legacy key expiry count = %d, want 0", expiryCount)
	}
	if err := store.RevokePoolAPIKey(ctx, credential.Item.ID); err != nil {
		t.Fatalf("RevokePoolAPIKey() error = %v", err)
	}
	if err := store.ArchiveAccountPool(ctx, pool.ID); err != nil {
		t.Fatalf("ArchiveAccountPool() error = %v", err)
	}
}
