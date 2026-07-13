package resourcepool

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS proxy_resources (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	proxy_url TEXT NOT NULL UNIQUE,
	exit_ip TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	health_status TEXT NOT NULL DEFAULT 'unknown',
	latency_ms INTEGER NOT NULL DEFAULT 0,
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	last_checked_at TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	tags_json TEXT NOT NULL DEFAULT '[]',
	note TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS claude_code_pools (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	is_default INTEGER NOT NULL DEFAULT 0,
	config_json TEXT NOT NULL DEFAULT '{}',
	archived_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS claude_code_pool_api_keys (
	id TEXT PRIMARY KEY,
	pool_id TEXT NOT NULL,
	name TEXT NOT NULL,
	key_prefix TEXT NOT NULL,
	key_hash TEXT NOT NULL UNIQUE,
	key_secret TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	expires_at TEXT,
	revoked_at TEXT,
	last_used_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(pool_id) REFERENCES claude_code_pools(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_claude_code_pool_api_keys_pool ON claude_code_pool_api_keys(pool_id, revoked_at, created_at DESC);

CREATE TABLE IF NOT EXISTS claude_code_model_price_versions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	revision INTEGER NOT NULL UNIQUE,
	source TEXT NOT NULL DEFAULT 'manual',
	note TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS claude_code_model_prices (
	version_id INTEGER NOT NULL,
	model_pattern TEXT NOT NULL,
	input_per_million REAL NOT NULL DEFAULT 0,
	output_per_million REAL NOT NULL DEFAULT 0,
	cache_write_5m_per_million REAL NOT NULL DEFAULT 0,
	cache_write_1h_per_million REAL NOT NULL DEFAULT 0,
	cache_read_per_million REAL NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	PRIMARY KEY(version_id, model_pattern),
	FOREIGN KEY(version_id) REFERENCES claude_code_model_price_versions(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_claude_code_model_prices_version ON claude_code_model_prices(version_id, model_pattern);

CREATE TABLE IF NOT EXISTS claude_code_accounts (
	id TEXT PRIMARY KEY,
	pool_id TEXT NOT NULL DEFAULT 'default',
	auth_id TEXT NOT NULL UNIQUE,
	cloak_user_id TEXT NOT NULL DEFAULT '',
	auth_json TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	health_status TEXT NOT NULL DEFAULT 'healthy',
	blocked_until TEXT,
	blocked_reason TEXT NOT NULL DEFAULT '',
	last_health_check_at TEXT,
	next_health_check_at TEXT,
	priority INTEGER NOT NULL DEFAULT 0,
	proxy_resource_id TEXT UNIQUE,
	note TEXT NOT NULL DEFAULT '',
	excluded_models_json TEXT NOT NULL DEFAULT '[]',
	test_status TEXT NOT NULL DEFAULT 'unknown',
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	last_test_at TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(proxy_resource_id) REFERENCES proxy_resources(id) ON DELETE SET NULL,
	FOREIGN KEY(pool_id) REFERENCES claude_code_pools(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS proxy_reservations (
	proxy_resource_id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	item_id TEXT NOT NULL,
	purpose TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(proxy_resource_id) REFERENCES proxy_resources(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_reservations_owner_item ON proxy_reservations(owner_id, item_id);
CREATE INDEX IF NOT EXISTS idx_proxy_reservations_expiry ON proxy_reservations(expires_at);

CREATE TABLE IF NOT EXISTS claude_code_account_capacity (
	account_id TEXT PRIMARY KEY,
	base_rpm INTEGER NOT NULL DEFAULT 6,
	concurrency_limit INTEGER NOT NULL DEFAULT 1,
	max_sessions INTEGER NOT NULL DEFAULT 0,
	sticky_buffer INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS claude_code_account_model_status (
	account_id TEXT NOT NULL,
	model TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'unknown',
	success_count INTEGER NOT NULL DEFAULT 0,
	failure_count INTEGER NOT NULL DEFAULT 0,
	rate_limit_count INTEGER NOT NULL DEFAULT 0,
	overload_count INTEGER NOT NULL DEFAULT 0,
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	cooling_until TEXT,
	last_status_code INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	last_test_at TEXT,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(account_id, model),
	FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
);

	CREATE TABLE IF NOT EXISTS claude_code_models (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		alias TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	source TEXT NOT NULL DEFAULT 'manual',
	note TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_claude_code_models_alias ON claude_code_models(lower(alias));
	CREATE INDEX IF NOT EXISTS idx_claude_code_models_name ON claude_code_models(lower(name));

	CREATE TABLE IF NOT EXISTS claude_code_account_quota (
		account_id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'unknown',
		windows_json TEXT NOT NULL DEFAULT '[]',
		raw_json TEXT NOT NULL DEFAULT '',
		probe_json TEXT NOT NULL DEFAULT '{}',
		last_error TEXT NOT NULL DEFAULT '',
		checked_at TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
	);

CREATE TABLE IF NOT EXISTS pool_config (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pool_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	message TEXT NOT NULL,
	data_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS claude_code_routing_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pool_id TEXT NOT NULL DEFAULT 'default',
	api_key_id TEXT NOT NULL DEFAULT '',
	request_id TEXT NOT NULL DEFAULT '',
	account_id TEXT NOT NULL DEFAULT '',
	auth_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	requested_model TEXT NOT NULL DEFAULT '',
	proxy_resource_id TEXT NOT NULL DEFAULT '',
	sticky INTEGER NOT NULL DEFAULT 0,
	session_key TEXT NOT NULL DEFAULT '',
	capacity_used INTEGER NOT NULL DEFAULT 0,
	capacity_limit INTEGER NOT NULL DEFAULT 0,
	decision TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	status_code INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_claude_code_routing_events_created ON claude_code_routing_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_code_routing_events_account ON claude_code_routing_events(account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS claude_code_usage_ledger (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pool_id TEXT NOT NULL DEFAULT 'default',
	api_key_id TEXT NOT NULL DEFAULT '',
	request_id TEXT NOT NULL DEFAULT '',
	api_key_preview TEXT NOT NULL DEFAULT '',
	account_id TEXT NOT NULL DEFAULT '',
	auth_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	requested_model TEXT NOT NULL DEFAULT '',
	status_code INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0,
	raw_input_tokens INTEGER NOT NULL DEFAULT 0,
	raw_total_tokens INTEGER NOT NULL DEFAULT 0,
	price_version_id INTEGER NOT NULL DEFAULT 0,
	price_model_pattern TEXT NOT NULL DEFAULT '',
	pricing_status TEXT NOT NULL DEFAULT 'unpriced',
	estimated_cost REAL NOT NULL DEFAULT 0,
	success INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_created ON claude_code_usage_ledger(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_account ON claude_code_usage_ledger(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_model ON claude_code_usage_ledger(model, created_at DESC);

CREATE TABLE IF NOT EXISTS claude_code_usage_calibrations (
	model TEXT NOT NULL,
	profile_fingerprint TEXT NOT NULL,
	overhead_tokens INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'estimated',
	checked_at TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(model, profile_fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_claude_code_usage_calibrations_updated ON claude_code_usage_calibrations(updated_at DESC);

CREATE TABLE IF NOT EXISTS claude_code_profile_snapshots (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT 'phistory',
	version TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'fetched',
	meta_json TEXT NOT NULL DEFAULT '{}',
	trace_jsonl TEXT NOT NULL DEFAULT '',
	prompt_md TEXT NOT NULL DEFAULT '',
	static_prompts_md TEXT NOT NULL DEFAULT '',
	static_prompts_json TEXT NOT NULL DEFAULT '',
	normalized_profile_json TEXT NOT NULL DEFAULT '{}',
	prompt_hash TEXT NOT NULL DEFAULT '',
	static_prompt_hash TEXT NOT NULL DEFAULT '',
	static_prompt_length INTEGER NOT NULL DEFAULT 0,
	full_prompt_hash TEXT NOT NULL DEFAULT '',
	full_prompt_length INTEGER NOT NULL DEFAULT 0,
	request_kind_summary_json TEXT NOT NULL DEFAULT '{}',
	trace_hash TEXT NOT NULL DEFAULT '',
	diff_report TEXT NOT NULL DEFAULT '',
	fatal_count INTEGER NOT NULL DEFAULT 0,
	warn_count INTEGER NOT NULL DEFAULT 0,
	promoted INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	fetched_at TEXT,
	promoted_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_claude_code_profile_snapshots_source_version ON claude_code_profile_snapshots(source, version);
CREATE INDEX IF NOT EXISTS idx_claude_code_profile_snapshots_updated ON claude_code_profile_snapshots(updated_at DESC);
`

// Store wraps the SQLite resource pool database.
type Store struct {
	db       *sql.DB
	path     string
	initPath string
}

const databaseInstanceIDConfigKey = "database_instance_id"

var storeInitializationMu sync.Mutex

// Open opens the SQLite resource pool store and performs first-run YAML import.
func Open(configFilePath string, cfg *config.Config) (*Store, error) {
	initPath := ResolveConfigPath(configFilePath, cfg)
	initDoc, err := LoadConfigFile(initPath)
	if err != nil {
		return nil, err
	}
	dbPath := strings.TrimSpace(initDoc.DatabasePath)
	if dbPath == "" {
		dbPath = DefaultDBFileName
	}
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(initPath), dbPath)
	}
	storeInitializationMu.Lock()
	defer storeInitializationMu.Unlock()
	db, err := openSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, path: filepath.Clean(dbPath), initPath: initPath}
	if err := store.importYAMLIfEmpty(context.Background(), initDoc); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateAccountPoolRoutingV2(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateAccountPoolConfigV3(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateAccountPoolPureUsageV4(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateClaudeCodeProfileRevision(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateAccountLifecycleV5(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateMultiPoolV6(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateModelPricingV7(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migratePermanentPoolAPIKeysV8(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateAccountPoolConfigInheritanceV9(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.ensureDatabaseInstanceID(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) ensureDatabaseInstanceID(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("resource pool store is nil")
	}
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, databaseInstanceIDConfigKey).Scan(&existing)
	if err == nil && strings.TrimSpace(existing) != "" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read database instance id: %w", err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("generate database instance id: %w", err)
	}
	now := dbTime(time.Now())
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO pool_config(key, value, created_at, updated_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(key) DO NOTHING
`, databaseInstanceIDConfigKey, hex.EncodeToString(random), now, now); err != nil {
		return fmt.Errorf("persist database instance id: %w", err)
	}
	return nil
}

// DatabaseInstanceFingerprint returns a short non-reversible identity for this SQLite instance.
func (s *Store) DatabaseInstanceFingerprint(ctx context.Context) (string, error) {
	if err := s.ensureDatabaseInstanceID(ctx); err != nil {
		return "", err
	}
	var instanceID string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, databaseInstanceIDConfigKey).Scan(&instanceID); err != nil {
		return "", fmt.Errorf("read database instance fingerprint source: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(instanceID)))
	return hex.EncodeToString(sum[:6]), nil
}

func (s *Store) migrateMultiPoolV6(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "account_pool_multi_pool_v6"
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&value); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read multi-pool v6 migration marker: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin multi-pool v6 migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_pools(id, name, description, enabled, is_default, created_at, updated_at)
VALUES(?, ?, '', 1, 1, ?, ?)
ON CONFLICT(id) DO UPDATE SET name = excluded.name, enabled = 1, is_default = 1, archived_at = NULL, updated_at = excluded.updated_at
	`, DefaultAccountPoolID, DefaultAccountPoolID, now, now); err != nil {
		return fmt.Errorf("ensure default account pool: %w", err)
	}
	for _, table := range []string{"claude_code_accounts", "claude_code_routing_events", "claude_code_usage_ledger"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET pool_id = ? WHERE TRIM(pool_id) = ''`, DefaultAccountPoolID); err != nil {
			return fmt.Errorf("backfill %s pool id: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write multi-pool v6 migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit multi-pool v6 migration: %w", err)
	}
	return nil
}

func (s *Store) migrateAccountLifecycleV5(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "account_pool_lifecycle_v5"
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&value); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read account lifecycle v5 migration marker: %w", err)
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if doc.ClaudeCode.Routing.ActiveSessionIdleTTLMS <= 0 {
		doc.ClaudeCode.Routing.ActiveSessionIdleTTLMS = int((5 * time.Minute) / time.Millisecond)
	}
	if doc.ClaudeCode.Routing.MaxWaitersPerAccount == 20 {
		doc.ClaudeCode.Routing.MaxWaitersPerAccount = 5
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account lifecycle v5 migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return err
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write account lifecycle v5 migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account lifecycle v5 migration: %w", err)
	}
	return nil
}

func (s *Store) migrateAccountPoolConfigV3(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "account_pool_config_v3"
	var markerValue string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&markerValue); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read account-pool config v3 migration marker: %w", err)
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account-pool config v3 migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return err
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write account-pool config v3 migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account-pool config v3 migration: %w", err)
	}
	return nil
}

func (s *Store) migrateAccountPoolPureUsageV4(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "account_pool_pure_usage_v4"
	var markerValue string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&markerValue); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read account-pool pure usage v4 migration marker: %w", err)
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	// normalizeConfigFile makes pure-mode authoritative and synchronizes the
	// legacy clean-input field before the config is persisted.
	normalizeConfigFile(doc)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account-pool pure usage v4 migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return err
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write account-pool pure usage v4 migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account-pool pure usage v4 migration: %w", err)
	}
	return nil
}

func (s *Store) migrateClaudeCodeProfileRevision(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "claude_code_profile_2_1_207_r3"
	var markerValue string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&markerValue); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read claude code profile migration marker: %w", err)
	}
	var rawProfile string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = 'claude_code_profile_json'`).Scan(&rawProfile); err != nil {
		return fmt.Errorf("read claude code profile for migration: %w", err)
	}
	var storedProfile ClaudeCodeProfile
	if err := json.Unmarshal([]byte(rawProfile), &storedProfile); err != nil {
		return fmt.Errorf("decode claude code profile for migration: %w", err)
	}
	isR2Baseline := isExactBuiltinClaudeCodeProfileR2(storedProfile)
	requiresUpgrade := shouldMigrateBuiltinClaudeCodeProfile(storedProfile) || isR2Baseline
	oldProfile := EffectiveClaudeCodeProfile(builtinClaudeCodeProfileR2())
	oldFingerprint := ClaudeCodeProfileFingerprint(oldProfile)
	oldDefaultOverhead := ClaudeCodeProfileInjectedOverheadTokens(oldProfile)
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if requiresUpgrade {
		doc.Profile = defaultClaudeCodeProfile()
		if doc.ClaudeCode.Usage.SystemPromptOverheadTokens == 1909 || doc.ClaudeCode.Usage.SystemPromptOverheadTokens == oldDefaultOverhead {
			doc.ClaudeCode.Usage.SystemPromptOverheadTokens = DefaultCleanInputOverheadTokens
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin claude code profile migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if requiresUpgrade {
		if err := savePoolConfigTx(ctx, tx, doc); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE claude_code_usage_calibrations SET status = ?, updated_at = ? WHERE profile_fingerprint = ? AND status = ?`, UsageCalibrationStale, dbTime(time.Now()), oldFingerprint, UsageCalibrationCalibrated); err != nil {
			return fmt.Errorf("mark old Claude Code usage calibrations stale: %w", err)
		}
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write claude code profile migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit claude code profile migration: %w", err)
	}
	return nil
}

func (s *Store) migrateAccountPoolRoutingV2(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const marker = "account_pool_routing_v2"
	var markerValue string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, marker).Scan(&markerValue); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read account-pool routing migration marker: %w", err)
	}
	var rawConfig string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = 'claude_code_pool_json'`).Scan(&rawConfig); err != nil {
		return fmt.Errorf("read account-pool routing config for migration: %w", err)
	}
	requiresReset := !strings.Contains(rawConfig, `"session-affinity-ttl-ms"`)
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if requiresReset {
		doc.ClaudeCode.Routing = defaultClaudeCodeRoutingConfig()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account-pool routing migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if requiresReset {
		if err := savePoolConfigTx(ctx, tx, doc); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE claude_code_account_capacity SET base_rpm = 6, concurrency_limit = 1, max_sessions = 30, sticky_buffer = 1, updated_at = ?`, dbTime(time.Now())); err != nil {
			return fmt.Errorf("reset account-pool capacity defaults: %w", err)
		}
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_config(key, value, created_at, updated_at) VALUES(?, '1', ?, ?) ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at`, marker, now, now); err != nil {
		return fmt.Errorf("write account-pool routing migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account-pool routing migration: %w", err)
	}
	return nil
}

// Path returns the database path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close closes the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func openSQLiteStore(path string) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("resource pool sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create resource pool sqlite dir: %w", err)
	}
	absPath, errAbs := filepath.Abs(path)
	if errAbs != nil {
		return nil, fmt.Errorf("resolve resource pool sqlite path: %w", errAbs)
	}
	dsn := (&url.URL{
		Scheme: "file",
		Path:   absPath,
		RawQuery: url.Values{
			"_pragma": []string{
				"busy_timeout(5000)",
				"journal_mode(WAL)",
				"synchronous(NORMAL)",
				"foreign_keys(ON)",
			},
		}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open resource pool sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable resource pool foreign keys: %w", err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize resource pool sqlite: %w", err)
	}
	if err := migrateSQLiteStore(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrateSQLiteStore(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("resource pool sqlite is nil")
	}
	if err := ensureColumn(db, "claude_code_pool_api_keys", "key_secret", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_pools", "config_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "auth_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "cloak_user_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE claude_code_accounts SET cloak_user_id = 'user_' || lower(hex(randomblob(32))) || '_account_' || lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(6))) || '_session_' || lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(6))) WHERE TRIM(cloak_user_id) = '' OR cloak_user_id NOT GLOB 'user_*_account_*_session_*'`); err != nil {
		return fmt.Errorf("backfill claude code account cloak user id: %w", err)
	}
	if err := ensureColumn(db, "claude_code_accounts", "test_status", "TEXT NOT NULL DEFAULT 'unknown'"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "consecutive_failures", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "last_test_at", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "last_error", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "health_status", "TEXT NOT NULL DEFAULT 'healthy'"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "blocked_until", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "blocked_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "last_health_check_at", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "next_health_check_at", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_account_quota", "probe_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_accounts", "pool_id", "TEXT NOT NULL DEFAULT 'default'"); err != nil {
		return err
	}
	if _, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS claude_code_account_quota (
		account_id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'unknown',
		windows_json TEXT NOT NULL DEFAULT '[]',
		raw_json TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		checked_at TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
	)
	`); err != nil {
		return fmt.Errorf("migrate claude code account quota table: %w", err)
	}
	if _, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS proxy_reservations (
		proxy_resource_id TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		purpose TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(proxy_resource_id) REFERENCES proxy_resources(id) ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_reservations_owner_item ON proxy_reservations(owner_id, item_id);
	CREATE INDEX IF NOT EXISTS idx_proxy_reservations_expiry ON proxy_reservations(expires_at);

	CREATE TABLE IF NOT EXISTS claude_code_account_capacity (
		account_id TEXT PRIMARY KEY,
		base_rpm INTEGER NOT NULL DEFAULT 6,
		concurrency_limit INTEGER NOT NULL DEFAULT 1,
		max_sessions INTEGER NOT NULL DEFAULT 0,
		sticky_buffer INTEGER NOT NULL DEFAULT 1,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS claude_code_account_model_status (
		account_id TEXT NOT NULL,
		model TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'unknown',
		success_count INTEGER NOT NULL DEFAULT 0,
		failure_count INTEGER NOT NULL DEFAULT 0,
		rate_limit_count INTEGER NOT NULL DEFAULT 0,
		overload_count INTEGER NOT NULL DEFAULT 0,
		consecutive_failures INTEGER NOT NULL DEFAULT 0,
		cooling_until TEXT,
		last_status_code INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		last_test_at TEXT,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(account_id, model),
		FOREIGN KEY(account_id) REFERENCES claude_code_accounts(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS claude_code_routing_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT NOT NULL DEFAULT '',
		account_id TEXT NOT NULL DEFAULT '',
		auth_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		requested_model TEXT NOT NULL DEFAULT '',
		proxy_resource_id TEXT NOT NULL DEFAULT '',
		sticky INTEGER NOT NULL DEFAULT 0,
		session_key TEXT NOT NULL DEFAULT '',
		capacity_used INTEGER NOT NULL DEFAULT 0,
		capacity_limit INTEGER NOT NULL DEFAULT 0,
		decision TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_claude_code_routing_events_created ON claude_code_routing_events(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_claude_code_routing_events_account ON claude_code_routing_events(account_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS claude_code_usage_ledger (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id TEXT NOT NULL DEFAULT '',
		api_key_preview TEXT NOT NULL DEFAULT '',
		account_id TEXT NOT NULL DEFAULT '',
		auth_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		requested_model TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0,
		raw_input_tokens INTEGER NOT NULL DEFAULT 0,
		raw_total_tokens INTEGER NOT NULL DEFAULT 0,
		price_version_id INTEGER NOT NULL DEFAULT 0,
		price_model_pattern TEXT NOT NULL DEFAULT '',
		pricing_status TEXT NOT NULL DEFAULT 'unpriced',
		estimated_cost REAL NOT NULL DEFAULT 0,
		success INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_created ON claude_code_usage_ledger(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_account ON claude_code_usage_ledger(account_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_model ON claude_code_usage_ledger(model, created_at DESC);

	CREATE TABLE IF NOT EXISTS claude_code_model_price_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		revision INTEGER NOT NULL UNIQUE,
		source TEXT NOT NULL DEFAULT 'manual',
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS claude_code_model_prices (
		version_id INTEGER NOT NULL,
		model_pattern TEXT NOT NULL,
		input_per_million REAL NOT NULL DEFAULT 0,
		output_per_million REAL NOT NULL DEFAULT 0,
		cache_write_5m_per_million REAL NOT NULL DEFAULT 0,
		cache_write_1h_per_million REAL NOT NULL DEFAULT 0,
		cache_read_per_million REAL NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		PRIMARY KEY(version_id, model_pattern),
		FOREIGN KEY(version_id) REFERENCES claude_code_model_price_versions(id) ON DELETE RESTRICT
	);
	CREATE INDEX IF NOT EXISTS idx_claude_code_model_prices_version ON claude_code_model_prices(version_id, model_pattern);

	CREATE TABLE IF NOT EXISTS claude_code_usage_calibrations (
		model TEXT NOT NULL,
		profile_fingerprint TEXT NOT NULL,
		overhead_tokens INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'estimated',
		checked_at TEXT,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(model, profile_fingerprint)
	);
	CREATE INDEX IF NOT EXISTS idx_claude_code_usage_calibrations_updated ON claude_code_usage_calibrations(updated_at DESC);

	CREATE TABLE IF NOT EXISTS claude_code_profile_snapshots (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT 'phistory',
		version TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'fetched',
		meta_json TEXT NOT NULL DEFAULT '{}',
		trace_jsonl TEXT NOT NULL DEFAULT '',
		prompt_md TEXT NOT NULL DEFAULT '',
		static_prompts_md TEXT NOT NULL DEFAULT '',
		static_prompts_json TEXT NOT NULL DEFAULT '',
		normalized_profile_json TEXT NOT NULL DEFAULT '{}',
		prompt_hash TEXT NOT NULL DEFAULT '',
		static_prompt_hash TEXT NOT NULL DEFAULT '',
		static_prompt_length INTEGER NOT NULL DEFAULT 0,
		full_prompt_hash TEXT NOT NULL DEFAULT '',
		full_prompt_length INTEGER NOT NULL DEFAULT 0,
		request_kind_summary_json TEXT NOT NULL DEFAULT '{}',
		trace_hash TEXT NOT NULL DEFAULT '',
		diff_report TEXT NOT NULL DEFAULT '',
		fatal_count INTEGER NOT NULL DEFAULT 0,
		warn_count INTEGER NOT NULL DEFAULT 0,
		promoted INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		fetched_at TEXT,
		promoted_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_claude_code_profile_snapshots_source_version ON claude_code_profile_snapshots(source, version);
	CREATE INDEX IF NOT EXISTS idx_claude_code_profile_snapshots_updated ON claude_code_profile_snapshots(updated_at DESC);
	`); err != nil {
		return fmt.Errorf("migrate claude code account observability tables: %w", err)
	}
	if err := ensureColumn(db, "claude_code_usage_ledger", "raw_input_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(db, "claude_code_usage_ledger", "raw_total_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	for column, definition := range map[string]string{
		"cache_creation_5m_tokens": "INTEGER NOT NULL DEFAULT 0",
		"cache_creation_1h_tokens": "INTEGER NOT NULL DEFAULT 0",
		"price_version_id":         "INTEGER NOT NULL DEFAULT 0",
		"price_model_pattern":      "TEXT NOT NULL DEFAULT ''",
		"pricing_status":           "TEXT NOT NULL DEFAULT 'unpriced'",
	} {
		if err := ensureColumn(db, "claude_code_usage_ledger", column, definition); err != nil {
			return err
		}
	}
	for column, definition := range map[string]string{
		"static_prompts_md":         "TEXT NOT NULL DEFAULT ''",
		"static_prompts_json":       "TEXT NOT NULL DEFAULT ''",
		"static_prompt_hash":        "TEXT NOT NULL DEFAULT ''",
		"static_prompt_length":      "INTEGER NOT NULL DEFAULT 0",
		"full_prompt_hash":          "TEXT NOT NULL DEFAULT ''",
		"full_prompt_length":        "INTEGER NOT NULL DEFAULT 0",
		"request_kind_summary_json": "TEXT NOT NULL DEFAULT '{}'",
	} {
		if err := ensureColumn(db, "claude_code_profile_snapshots", column, definition); err != nil {
			return err
		}
	}
	for column, definition := range map[string]string{
		"in_flight":         "INTEGER NOT NULL DEFAULT 0",
		"concurrency_limit": "INTEGER NOT NULL DEFAULT 0",
		"rpm_used":          "INTEGER NOT NULL DEFAULT 0",
		"rpm_limit":         "INTEGER NOT NULL DEFAULT 0",
		"attempt":           "INTEGER NOT NULL DEFAULT 0",
		"switch_count":      "INTEGER NOT NULL DEFAULT 0",
		"wait_ms":           "INTEGER NOT NULL DEFAULT 0",
		"affinity_mode":     "TEXT NOT NULL DEFAULT ''",
		"primary_hit":       "INTEGER NOT NULL DEFAULT 0",
		"backup_lane":       "INTEGER NOT NULL DEFAULT 0",
	} {
		if err := ensureColumn(db, "claude_code_routing_events", column, definition); err != nil {
			return err
		}
	}
	for table, columns := range map[string]map[string]string{
		"claude_code_routing_events": {
			"pool_id":    "TEXT NOT NULL DEFAULT 'default'",
			"api_key_id": "TEXT NOT NULL DEFAULT ''",
		},
		"claude_code_usage_ledger": {
			"pool_id":    "TEXT NOT NULL DEFAULT 'default'",
			"api_key_id": "TEXT NOT NULL DEFAULT ''",
		},
	} {
		for column, definition := range columns {
			if err := ensureColumn(db, table, column, definition); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(`
CREATE INDEX IF NOT EXISTS idx_claude_code_accounts_pool ON claude_code_accounts(pool_id, enabled, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_code_routing_events_pool ON claude_code_routing_events(pool_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_code_usage_ledger_pool ON claude_code_usage_ledger(pool_id, created_at DESC);
	`); err != nil {
		return fmt.Errorf("create multi-pool indexes: %w", err)
	}
	return nil
}

func ensureColumn(db *sql.DB, tableName, columnName, columnDef string) error {
	rows, err := db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return fmt.Errorf("inspect resource pool table %s: %w", tableName, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan resource pool table %s: %w", tableName, err)
		}
		if strings.EqualFold(name, columnName) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate resource pool table %s: %w", tableName, err)
	}
	if _, err := db.Exec(`ALTER TABLE ` + tableName + ` ADD COLUMN ` + columnName + ` ` + columnDef); err != nil {
		return fmt.Errorf("migrate resource pool table %s add %s: %w", tableName, columnName, err)
	}
	return nil
}

func (s *Store) importYAMLIfEmpty(ctx context.Context, doc *ConfigFile) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("resource pool store is nil")
	}
	empty, err := s.empty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		return nil
	}
	if doc == nil {
		doc = defaultConfigFile()
	}
	normalizeConfigFile(doc)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin resource pool yaml import: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return err
	}
	for i, seed := range doc.Proxies {
		if strings.TrimSpace(seed.ProxyURL) == "" {
			continue
		}
		if _, err := insertProxyTx(ctx, tx, seed); err != nil {
			return fmt.Errorf("import proxy %d: %w", i+1, err)
		}
	}
	if err := insertEventTx(ctx, tx, "init", "resource pools initialized from yaml", map[string]string{"path": s.initPath}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit resource pool yaml import: %w", err)
	}
	return nil
}

func (s *Store) empty(ctx context.Context) (bool, error) {
	var proxyCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_resources`).Scan(&proxyCount); err != nil {
		return false, fmt.Errorf("count proxy resources: %w", err)
	}
	var accountCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_code_accounts`).Scan(&accountCount); err != nil {
		return false, fmt.Errorf("count claude code accounts: %w", err)
	}
	var configCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pool_config`).Scan(&configCount); err != nil {
		return false, fmt.Errorf("count resource pool config: %w", err)
	}
	return proxyCount == 0 && accountCount == 0 && configCount == 0, nil
}

// GetConfig loads the SQLite-backed runtime config.
func (s *Store) GetConfig(ctx context.Context) (*ConfigFile, error) {
	doc := defaultConfigFile()
	if s == nil || s.db == nil {
		return doc, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM pool_config`)
	if err != nil {
		return nil, fmt.Errorf("read resource pool config: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan resource pool config: %w", err)
		}
		switch key {
		case "database_path":
			doc.DatabasePath = value
		case "proxy_health_json":
			if err := json.Unmarshal([]byte(value), &doc.ProxyHealth); err != nil {
				return nil, fmt.Errorf("decode proxy health config: %w", err)
			}
		case "account_quota_json":
			if err := json.Unmarshal([]byte(value), &doc.AccountQuota); err != nil {
				return nil, fmt.Errorf("decode account quota config: %w", err)
			}
		case "trace_json":
			if err := json.Unmarshal([]byte(value), &doc.Trace); err != nil {
				return nil, fmt.Errorf("decode trace config: %w", err)
			}
		case "claude_code_pool_json":
			if err := json.Unmarshal([]byte(value), &doc.ClaudeCode); err != nil {
				return nil, fmt.Errorf("decode claude code pool config: %w", err)
			}
		case "claude_code_profile_json":
			if err := json.Unmarshal([]byte(value), &doc.Profile); err != nil {
				return nil, fmt.Errorf("decode claude code profile: %w", err)
			}
		case "pool_config_json":
			if err := json.Unmarshal([]byte(value), &doc.PoolConfig); err != nil {
				return nil, fmt.Errorf("decode resource pool config json: %w", err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource pool config: %w", err)
	}
	normalizeConfigFile(doc)
	return doc, nil
}

func savePoolConfigTx(ctx context.Context, tx *sql.Tx, doc *ConfigFile) error {
	if doc == nil {
		doc = defaultConfigFile()
	}
	normalizeConfigFile(doc)
	now := dbTime(time.Now())
	values := map[string]interface{}{
		"database_path":            doc.DatabasePath,
		"proxy_health_json":        doc.ProxyHealth,
		"account_quota_json":       doc.AccountQuota,
		"trace_json":               doc.Trace,
		"claude_code_pool_json":    doc.ClaudeCode,
		"claude_code_profile_json": doc.Profile,
		"pool_config_json":         doc.PoolConfig,
	}
	for key, value := range values {
		var encoded string
		switch v := value.(type) {
		case string:
			encoded = v
		default:
			raw, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("encode resource pool config %s: %w", key, err)
			}
			encoded = string(raw)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pool_config(key, value, created_at, updated_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
`, key, encoded, now, now); err != nil {
			return fmt.Errorf("save resource pool config %s: %w", key, err)
		}
	}
	return nil
}

// SaveClaudeCodePoolConfig persists Claude Code account-pool settings in SQLite.
func (s *Store) SaveClaudeCodePoolConfig(ctx context.Context, cfg ClaudeCodePoolConfig) (*ConfigFile, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	doc.ClaudeCode = cfg
	normalizeConfigFile(doc)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin save claude code pool config: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return nil, err
	}
	if err := insertEventTx(ctx, tx, "account_pool.config", "claude code account pool config updated", nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit save claude code pool config: %w", err)
	}
	return s.GetConfig(ctx)
}

// SaveClaudeCodePoolLogConfig persists only the account-pool log settings.
func (s *Store) SaveClaudeCodePoolLogConfig(ctx context.Context, cfg AccountPoolLogConfig) (*ConfigFile, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	doc.ClaudeCode.Log = cfg
	normalizeConfigFile(doc)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin save claude code pool log config: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return nil, err
	}
	if err := insertEventTx(ctx, tx, "account_pool.log_config", "claude code account pool log config updated", nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit save claude code pool log config: %w", err)
	}
	return s.GetConfig(ctx)
}

// CreateProxy inserts one proxy resource.
func (s *Store) CreateProxy(ctx context.Context, seed ProxyResourceSeed) (*ProxyResource, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create proxy: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	proxy, err := insertProxyTx(ctx, tx, seed)
	if err != nil {
		return nil, err
	}
	if err := insertEventTx(ctx, tx, "proxy.create", "proxy resource created", map[string]string{"proxy_id": proxy.ID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create proxy: %w", err)
	}
	return s.GetProxy(ctx, proxy.ID)
}

func insertProxyTx(ctx context.Context, tx *sql.Tx, seed ProxyResourceSeed) (*ProxyResource, error) {
	proxyURL, err := validateProxyURL(seed.ProxyURL)
	if err != nil {
		return nil, err
	}
	enabled := true
	if seed.Enabled != nil {
		enabled = *seed.Enabled
	}
	tags := normalizeStringList(seed.Tags)
	tagsJSON, err := encodeStringList(tags)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	name := strings.TrimSpace(seed.Name)
	if name == "" {
		name = defaultProxyName(proxyURL)
	}
	health := normalizeHealthStatus(HealthUnknown, enabled)
	proxy := &ProxyResource{
		ID:           uuid.NewString(),
		Name:         name,
		ProxyURL:     proxyURL,
		ExitIP:       strings.TrimSpace(seed.ExitIP),
		Enabled:      enabled,
		HealthStatus: health,
		Tags:         tags,
		Note:         strings.TrimSpace(seed.Note),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO proxy_resources(id, name, proxy_url, exit_ip, enabled, health_status, tags_json, note, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, proxy.ID, proxy.Name, proxy.ProxyURL, proxy.ExitIP, boolInt(proxy.Enabled), proxy.HealthStatus, tagsJSON, proxy.Note, dbTime(proxy.CreatedAt), dbTime(proxy.UpdatedAt)); err != nil {
		return nil, mapSQLiteConstraintError(err, "proxy resource")
	}
	return proxy, nil
}

// ImportProxies inserts proxy seeds, skipping duplicate proxy URLs.
func (s *Store) ImportProxies(ctx context.Context, seeds []ProxyResourceSeed) (ImportResult, error) {
	result := ImportResult{}
	if len(seeds) == 0 {
		return result, nil
	}
	for i, seed := range seeds {
		_, err := s.CreateProxy(ctx, seed)
		if err == nil {
			result.Created++
			continue
		}
		if isUniqueConstraint(err) {
			result.Skipped++
			continue
		}
		result.Errors = append(result.Errors, fmt.Sprintf("第 %d 行: %v", i+1, err))
	}
	return result, nil
}

// ParseProxyImport parses newline import text. Each line can be either a proxy URL
// or name|proxy-url|exit-ip|tag1,tag2|note.
func ParseProxyImport(text string) []ProxyResourceSeed {
	lines := strings.Split(text, "\n")
	out := make([]ProxyResourceSeed, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "|") {
			parts := strings.Split(line, "|")
			seed := ProxyResourceSeed{}
			if len(parts) > 0 {
				seed.Name = strings.TrimSpace(parts[0])
			}
			if len(parts) > 1 {
				seed.ProxyURL = strings.TrimSpace(parts[1])
			}
			if len(parts) > 2 {
				seed.ExitIP = strings.TrimSpace(parts[2])
			}
			if len(parts) > 3 {
				seed.Tags = splitTags(parts[3])
			}
			if len(parts) > 4 {
				seed.Note = strings.TrimSpace(strings.Join(parts[4:], "|"))
			}
			out = append(out, seed)
			continue
		}
		out = append(out, ProxyResourceSeed{ProxyURL: line})
	}
	return out
}

// ListProxies returns all proxy resources grouped by health-friendly ordering.
func (s *Store) ListProxies(ctx context.Context) ([]ProxyResource, error) {
	return s.listProxies(ctx, "")
}

// ListAvailableProxies returns enabled, unbound, healthy-or-unknown proxies.
func (s *Store) ListAvailableProxies(ctx context.Context) ([]ProxyResource, error) {
	return s.listProxies(ctx, "available")
}

// ListHealthyAvailableProxies returns proxies eligible for SessionKey login.
func (s *Store) ListHealthyAvailableProxies(ctx context.Context) ([]ProxyResource, error) {
	return s.listProxies(ctx, "healthy_available")
}

// ListEnabledProxiesForHealth returns all enabled proxies for health checking.
func (s *Store) ListEnabledProxiesForHealth(ctx context.Context) ([]ProxyResource, error) {
	return s.listProxies(ctx, "enabled")
}

func (s *Store) listProxies(ctx context.Context, mode string) ([]ProxyResource, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	where := ""
	switch mode {
	case "available":
		where = `WHERE p.enabled = 1 AND (p.health_status = 'healthy' OR p.health_status = 'unknown') AND a.id IS NULL AND r.proxy_resource_id IS NULL`
	case "healthy_available":
		where = `WHERE p.enabled = 1 AND p.health_status = 'healthy' AND a.id IS NULL AND r.proxy_resource_id IS NULL`
	case "enabled":
		where = `WHERE p.enabled = 1`
	}
	now := dbTime(time.Now())
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at, COALESCE(a.id, ''), COALESCE(a.email, ''),
	   CASE WHEN r.proxy_resource_id IS NULL THEN 0 ELSE 1 END, r.expires_at
FROM proxy_resources p
LEFT JOIN claude_code_accounts a ON a.proxy_resource_id = p.id
LEFT JOIN proxy_reservations r ON r.proxy_resource_id = p.id AND r.expires_at > ?
`+where+`
ORDER BY
  CASE
    WHEN p.enabled = 0 THEN 4
    WHEN p.health_status = 'healthy' THEN 1
    WHEN p.health_status = 'unknown' THEN 2
    WHEN p.health_status = 'unhealthy' THEN 3
    ELSE 2
  END,
  p.updated_at DESC,
  p.name ASC
`, now)
	if err != nil {
		return nil, fmt.Errorf("list proxy resources: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]ProxyResource, 0)
	for rows.Next() {
		proxy, err := scanProxy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, proxy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy resources: %w", err)
	}
	return out, nil
}

// GetProxy returns one proxy resource.
func (s *Store) GetProxy(ctx context.Context, id string) (*ProxyResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("proxy id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at, COALESCE(a.id, ''), COALESCE(a.email, ''),
	   CASE WHEN r.proxy_resource_id IS NULL THEN 0 ELSE 1 END, r.expires_at
FROM proxy_resources p
LEFT JOIN claude_code_accounts a ON a.proxy_resource_id = p.id
LEFT JOIN proxy_reservations r ON r.proxy_resource_id = p.id AND r.expires_at > ?
WHERE p.id = ?
`, dbTime(time.Now()), id)
	if err != nil {
		return nil, fmt.Errorf("get proxy resource: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate proxy resource: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	proxy, err := scanProxy(rows)
	if err != nil {
		return nil, err
	}
	return &proxy, nil
}

// UpdateProxy patches one proxy resource.
func (s *Store) UpdateProxy(ctx context.Context, id string, patch ProxyPatch) (*ProxyResource, error) {
	current, err := s.GetProxy(ctx, id)
	if err != nil {
		return nil, err
	}
	name := current.Name
	proxyURL := current.ProxyURL
	exitIP := current.ExitIP
	enabled := current.Enabled
	tags := current.Tags
	note := current.Note
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
	}
	if patch.ProxyURL != nil {
		proxyURL, err = validateProxyURL(*patch.ProxyURL)
		if err != nil {
			return nil, err
		}
	}
	if patch.ExitIP != nil {
		exitIP = strings.TrimSpace(*patch.ExitIP)
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if patch.Tags != nil {
		tags = normalizeStringList(*patch.Tags)
	}
	if patch.Note != nil {
		note = strings.TrimSpace(*patch.Note)
	}
	if name == "" {
		name = defaultProxyName(proxyURL)
	}
	health := current.HealthStatus
	if !enabled {
		health = HealthDisabled
	} else if health == HealthDisabled {
		health = HealthUnknown
	}
	tagsJSON, err := encodeStringList(tags)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE proxy_resources
SET name = ?, proxy_url = ?, exit_ip = ?, enabled = ?, health_status = ?, tags_json = ?, note = ?, updated_at = ?
WHERE id = ?
`, name, proxyURL, exitIP, boolInt(enabled), health, tagsJSON, note, dbTime(time.Now()), current.ID); err != nil {
		return nil, mapSQLiteConstraintError(err, "proxy resource")
	}
	return s.GetProxy(ctx, current.ID)
}

// DeleteProxy deletes a proxy resource and unbinds any account that referenced it.
func (s *Store) DeleteProxy(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("proxy id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete proxy: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	var reserved int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_reservations WHERE proxy_resource_id = ? AND expires_at > ?`, id, dbTime(time.Now())).Scan(&reserved); err != nil {
		return fmt.Errorf("check proxy reservation before delete: %w", err)
	}
	if reserved > 0 {
		return fmt.Errorf("proxy resource is reserved")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE claude_code_accounts SET proxy_resource_id = NULL, updated_at = ? WHERE proxy_resource_id = ?`, dbTime(time.Now()), id); err != nil {
		return fmt.Errorf("unbind proxy before delete: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM proxy_resources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete proxy resource: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	if err := insertEventTx(ctx, tx, "proxy.delete", "proxy resource deleted", map[string]string{"proxy_id": id}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete proxy: %w", err)
	}
	return nil
}

// UnbindProxy clears the account binding for a proxy.
func (s *Store) UnbindProxy(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("proxy id is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE claude_code_accounts SET proxy_resource_id = NULL, updated_at = ? WHERE proxy_resource_id = ?`, dbTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("unbind proxy: %w", err)
	}
	return nil
}

// UpdateProxyHealth stores the result of one proxy health check.
func (s *Store) UpdateProxyHealth(ctx context.Context, id string, ok bool, latency time.Duration, checkErr error, failureThreshold int) (*HealthResult, error) {
	proxy, err := s.GetProxy(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	status := proxy.HealthStatus
	failures := proxy.ConsecutiveFailures
	lastError := ""
	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	if !proxy.Enabled {
		status = HealthDisabled
	} else if ok {
		status = HealthHealthy
		failures = 0
	} else {
		failures++
		if failureThreshold <= 0 {
			failureThreshold = 1
		}
		if failures >= failureThreshold {
			status = HealthUnhealthy
		} else {
			status = HealthUnknown
		}
		if checkErr != nil {
			lastError = checkErr.Error()
		}
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE proxy_resources
SET health_status = ?, latency_ms = ?, consecutive_failures = ?, last_checked_at = ?, last_error = ?, updated_at = ?
WHERE id = ?
`, status, latencyMS, failures, dbTime(now), lastError, dbTime(now), proxy.ID); err != nil {
		return nil, fmt.Errorf("update proxy health: %w", err)
	}
	return &HealthResult{
		ID:                  proxy.ID,
		HealthStatus:        status,
		LatencyMS:           latencyMS,
		ConsecutiveFailures: failures,
		LastCheckedAt:       &now,
		LastError:           lastError,
	}, nil
}

// RegisterClaudeCodeAccount creates or updates a Claude Code OAuth account row.
func (s *Store) RegisterClaudeCodeAccount(ctx context.Context, authID, email, proxyResourceID string) (*ClaudeCodeAccount, error) {
	return s.RegisterClaudeCodeAccountInPool(ctx, DefaultAccountPoolID, authID, email, proxyResourceID)
}

// RegisterClaudeCodeAccountInPool creates or updates an account in one explicit pool.
func (s *Store) RegisterClaudeCodeAccountInPool(ctx context.Context, poolID, authID, email, proxyResourceID string) (*ClaudeCodeAccount, error) {
	return s.RegisterClaudeCodeAccountWithAuthInPool(ctx, poolID, authID, email, proxyResourceID, nil)
}

// RegisterClaudeCodeAccountWithAuth creates or updates a Claude Code OAuth account row
// and optionally persists the complete flattened auth JSON in SQLite.
func (s *Store) RegisterClaudeCodeAccountWithAuth(ctx context.Context, authID, email, proxyResourceID string, auth *coreauth.Auth) (*ClaudeCodeAccount, error) {
	return s.RegisterClaudeCodeAccountWithAuthInPool(ctx, DefaultAccountPoolID, authID, email, proxyResourceID, auth)
}

// RegisterClaudeCodeAccountWithAuthInPool persists an auth in one explicit pool.
func (s *Store) RegisterClaudeCodeAccountWithAuthInPool(ctx context.Context, poolID, authID, email, proxyResourceID string, auth *coreauth.Auth) (*ClaudeCodeAccount, error) {
	return s.RegisterClaudeCodeAccountWithAuthReservationInPool(ctx, poolID, authID, email, proxyResourceID, auth, "", "")
}

// RegisterClaudeCodeAccountWithAuthReservation registers an account and consumes its matching proxy reservation.
func (s *Store) RegisterClaudeCodeAccountWithAuthReservation(ctx context.Context, authID, email, proxyResourceID string, auth *coreauth.Auth, reservationOwner, reservationItem string) (*ClaudeCodeAccount, error) {
	return s.RegisterClaudeCodeAccountWithAuthReservationInPool(ctx, DefaultAccountPoolID, authID, email, proxyResourceID, auth, reservationOwner, reservationItem)
}

// RegisterClaudeCodeAccountWithAuthReservationInPool registers an account in one pool and consumes its matching proxy reservation.
func (s *Store) RegisterClaudeCodeAccountWithAuthReservationInPool(ctx context.Context, poolID, authID, email, proxyResourceID string, auth *coreauth.Auth, reservationOwner, reservationItem string) (*ClaudeCodeAccount, error) {
	poolID = normalizeAccountPoolID(poolID)
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil, fmt.Errorf("auth id is required")
	}
	email = strings.TrimSpace(email)
	proxyResourceID = strings.TrimSpace(proxyResourceID)
	authJSON, errAuthJSON := encodeAuthJSON(auth)
	if errAuthJSON != nil {
		return nil, errAuthJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin register claude code account: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	var poolArchived sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT archived_at FROM claude_code_pools WHERE id = ?`, poolID).Scan(&poolArchived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("account pool %q not found", poolID)
		}
		return nil, fmt.Errorf("read claude code account pool: %w", err)
	}
	if poolArchived.Valid {
		return nil, ErrAccountPoolArchived
	}
	accountID := ""
	existingPoolID := ""
	err = tx.QueryRowContext(ctx, `SELECT id, pool_id FROM claude_code_accounts WHERE auth_id = ?`, authID).Scan(&accountID, &existingPoolID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read claude code account by auth: %w", err)
	}
	if err == nil && normalizeAccountPoolID(existingPoolID) != poolID {
		return nil, fmt.Errorf("%w: account is in pool %q", ErrAccountInOtherPool, normalizeAccountPoolID(existingPoolID))
	}
	now := time.Now()
	if errors.Is(err, sql.ErrNoRows) {
		accountID = uuid.NewString()
		if proxyResourceID != "" {
			if err := assertProxyBindableTx(ctx, tx, accountID, proxyResourceID, reservationOwner, reservationItem); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_accounts(id, pool_id, auth_id, cloak_user_id, auth_json, email, enabled, health_status, next_health_check_at, priority, proxy_resource_id, excluded_models_json, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, 1, ?, ?, 0, NULLIF(?, ''), '[]', ?, ?)
`, accountID, poolID, authID, generateClaudeCodeCloakUserID(), authJSON, email, AccountHealthChecking, dbTime(now), proxyResourceID, dbTime(now), dbTime(now)); err != nil {
			return nil, mapSQLiteConstraintError(err, "claude code account")
		}
	} else {
		if proxyResourceID != "" {
			if err := assertProxyBindableTx(ctx, tx, accountID, proxyResourceID, reservationOwner, reservationItem); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE claude_code_accounts
SET email = CASE WHEN ? = '' THEN email ELSE ? END,
    auth_json = CASE WHEN ? = '' THEN auth_json ELSE ? END,
    enabled = 1,
    health_status = ?,
    blocked_until = NULL,
    blocked_reason = '',
    next_health_check_at = ?,
    proxy_resource_id = CASE WHEN ? = '' THEN proxy_resource_id ELSE ? END,
    updated_at = ?
WHERE id = ?
`, email, email, authJSON, authJSON, AccountHealthChecking, dbTime(now), proxyResourceID, proxyResourceID, dbTime(now), accountID); err != nil {
			return nil, mapSQLiteConstraintError(err, "claude code account")
		}
	}
	if proxyResourceID != "" && reservationOwner != "" {
		res, errDelete := tx.ExecContext(ctx, `DELETE FROM proxy_reservations WHERE proxy_resource_id = ? AND owner_id = ? AND item_id = ?`, proxyResourceID, reservationOwner, reservationItem)
		if errDelete != nil {
			return nil, fmt.Errorf("consume proxy reservation: %w", errDelete)
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return nil, fmt.Errorf("proxy reservation expired or does not match")
		}
	}
	if err := insertEventTx(ctx, tx, "account.register", "claude code account registered", map[string]string{"account_id": accountID, "auth_id": authID, "pool_id": poolID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit register claude code account: %w", err)
	}
	registered, err := s.GetAccount(ctx, accountID)
	if err == nil {
		ApplyAccountLifecycleRouting(registered)
	}
	return registered, err
}

// SaveClaudeCodeAccountAuth updates the flattened auth JSON for a pool account.
func (s *Store) SaveClaudeCodeAccountAuth(ctx context.Context, auth *coreauth.Auth) error {
	if s == nil || s.db == nil || auth == nil {
		return nil
	}
	authID := strings.TrimSpace(auth.ID)
	if authID == "" {
		return fmt.Errorf("auth id is required")
	}
	authJSON, err := encodeAuthJSON(auth)
	if err != nil {
		return err
	}
	email := authEmail(auth)
	res, err := s.db.ExecContext(ctx, `
UPDATE claude_code_accounts
SET auth_json = ?,
    email = CASE WHEN ? = '' THEN email ELSE ? END,
    updated_at = ?
WHERE auth_id = ?
`, authJSON, email, email, dbTime(time.Now()), authID)
	if err != nil {
		return fmt.Errorf("save claude code account auth: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func encodeAuthJSON(auth *coreauth.Auth) (string, error) {
	if auth == nil {
		return "", nil
	}
	var payload map[string]any
	var err error
	if auth.Storage != nil {
		payload, err = misc.MergeMetadata(auth.Storage, auth.Metadata)
		if err != nil {
			return "", fmt.Errorf("encode auth storage json: %w", err)
		}
	} else if auth.Metadata != nil {
		payload, err = misc.MergeMetadata(auth.Metadata, nil)
		if err != nil {
			return "", fmt.Errorf("encode auth metadata json: %w", err)
		}
	} else {
		payload = map[string]any{}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = "claude"
	payload["disabled"] = auth.Disabled
	if strings.TrimSpace(auth.ProxyURL) != "" {
		payload["proxy_url"] = strings.TrimSpace(auth.ProxyURL)
	}
	if strings.TrimSpace(auth.Prefix) != "" {
		payload["prefix"] = strings.TrimSpace(auth.Prefix)
	}
	for key, value := range auth.Attributes {
		switch key {
		case "priority":
			if strings.TrimSpace(value) != "" {
				payload["priority"] = strings.TrimSpace(value)
			}
		case "note":
			if strings.TrimSpace(value) != "" {
				payload["note"] = strings.TrimSpace(value)
			}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal auth json: %w", err)
	}
	return string(raw), nil
}

func authEmail(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		if email, _ := auth.Metadata["email"].(string); strings.TrimSpace(email) != "" {
			return strings.TrimSpace(email)
		}
	}
	if auth.Attributes != nil {
		if email := strings.TrimSpace(auth.Attributes["email"]); email != "" {
			return email
		}
	}
	return ""
}

func generateClaudeCodeCloakUserID() string {
	device := make([]byte, 32)
	if _, err := rand.Read(device); err != nil {
		return uuid.NewString()
	}
	return "user_" + hex.EncodeToString(device) + "_account_" + uuid.NewString() + "_session_" + uuid.NewString()
}

// ListAccounts returns all Claude Code account rows.
func (s *Store) ListAccounts(ctx context.Context) ([]ClaudeCodeAccount, error) {
	return s.ListAccountsByPool(ctx, "")
}

// ListAccountsByPool returns all accounts or only members of one pool.
func (s *Store) ListAccountsByPool(ctx context.Context, poolID string) ([]ClaudeCodeAccount, error) {
	poolID = strings.TrimSpace(poolID)
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, a.cloak_user_id, CASE WHEN TRIM(a.auth_json) <> '' THEN 1 ELSE 0 END, a.auth_json,
       a.email, a.enabled, a.health_status, a.blocked_until, a.blocked_reason, a.last_health_check_at, a.next_health_check_at,
       a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.test_status, a.consecutive_failures,
       a.last_test_at, a.last_error, a.created_at, a.updated_at,
	       q.status, q.windows_json, q.raw_json, q.probe_json, q.last_error, q.checked_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN claude_code_account_quota q ON q.account_id = a.id
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE (? = '' OR a.pool_id = ?)
ORDER BY a.enabled DESC, a.updated_at DESC, a.email ASC
	`, poolID, poolID)
	if err != nil {
		return nil, fmt.Errorf("list claude code accounts: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]ClaudeCodeAccount, 0)
	for rows.Next() {
		account, err := scanAccountWithProxy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code accounts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close claude code accounts rows: %w", err)
	}
	for i := range out {
		s.hydrateAccountRuntime(ctx, &out[i])
	}
	return out, nil
}

// GetAccount returns one Claude Code account.
func (s *Store) GetAccount(ctx context.Context, id string) (*ClaudeCodeAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("account id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, a.cloak_user_id, CASE WHEN TRIM(a.auth_json) <> '' THEN 1 ELSE 0 END, a.auth_json,
       a.email, a.enabled, a.health_status, a.blocked_until, a.blocked_reason, a.last_health_check_at, a.next_health_check_at,
       a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.test_status, a.consecutive_failures,
       a.last_test_at, a.last_error, a.created_at, a.updated_at,
	       q.status, q.windows_json, q.raw_json, q.probe_json, q.last_error, q.checked_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN claude_code_account_quota q ON q.account_id = a.id
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE a.id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("get claude code account: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate claude code account: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	account, err := scanAccountWithProxy(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close claude code account rows: %w", err)
	}
	s.hydrateAccountRuntime(ctx, &account)
	return &account, nil
}

// GetAccountByAuthID returns one Claude Code account by runtime auth ID.
func (s *Store) GetAccountByAuthID(ctx context.Context, authID string) (*ClaudeCodeAccount, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil, fmt.Errorf("auth id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, a.cloak_user_id, CASE WHEN TRIM(a.auth_json) <> '' THEN 1 ELSE 0 END, a.auth_json,
       a.email, a.enabled, a.health_status, a.blocked_until, a.blocked_reason, a.last_health_check_at, a.next_health_check_at,
       a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.test_status, a.consecutive_failures,
       a.last_test_at, a.last_error, a.created_at, a.updated_at,
	       q.status, q.windows_json, q.raw_json, q.probe_json, q.last_error, q.checked_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN claude_code_account_quota q ON q.account_id = a.id
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE a.auth_id = ?
	`, authID)
	if err != nil {
		return nil, fmt.Errorf("get claude code account by auth id: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate claude code account by auth id: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	account, err := scanAccountWithProxy(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close claude code account by auth id rows: %w", err)
	}
	s.hydrateAccountRuntime(ctx, &account)
	return &account, nil
}

// PatchAccount updates one Claude Code account.
func (s *Store) PatchAccount(ctx context.Context, id string, patch AccountPatch) (*ClaudeCodeAccount, error) {
	current, err := s.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	email := current.Email
	enabled := current.Enabled
	if patch.Schedulable != nil {
		enabled = *patch.Schedulable
	}
	priority := current.Priority
	note := current.Note
	excluded := current.ExcludedModels
	proxyID := current.ProxyResourceID
	if patch.Email != nil {
		email = strings.TrimSpace(*patch.Email)
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if patch.Priority != nil {
		priority = *patch.Priority
	}
	if patch.Note != nil {
		note = strings.TrimSpace(*patch.Note)
	}
	if patch.ExcludedModels != nil {
		excluded = normalizeStringList(*patch.ExcludedModels)
	}
	if patch.ProxyResourceID != nil {
		proxyID = strings.TrimSpace(*patch.ProxyResourceID)
	}
	excludedJSON, err := encodeStringList(excluded)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin patch claude code account: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if patch.ProxyResourceID != nil && proxyID != "" {
		if err := assertProxyBindableTx(ctx, tx, current.ID, proxyID, "", ""); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE claude_code_accounts
SET email = ?, enabled = ?, priority = ?, proxy_resource_id = NULLIF(?, ''), note = ?, excluded_models_json = ?, updated_at = ?
WHERE id = ?
`, email, boolInt(enabled), priority, proxyID, note, excludedJSON, dbTime(time.Now()), current.ID); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code account")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit patch claude code account: %w", err)
	}
	updated, err := s.GetAccount(ctx, current.ID)
	if err == nil {
		ApplyAccountLifecycleRouting(updated)
	}
	return updated, err
}

// BindAccountProxy binds one proxy to one Claude Code account.
func (s *Store) BindAccountProxy(ctx context.Context, accountID, proxyID string) (*ClaudeCodeAccount, error) {
	proxyID = strings.TrimSpace(proxyID)
	if proxyID == "" {
		return nil, fmt.Errorf("proxy id is required")
	}
	return s.PatchAccount(ctx, accountID, AccountPatch{ProxyResourceID: &proxyID})
}

// UnbindAccountProxy clears the proxy binding for one Claude Code account.
func (s *Store) UnbindAccountProxy(ctx context.Context, accountID string) (*ClaudeCodeAccount, error) {
	empty := ""
	return s.PatchAccount(ctx, accountID, AccountPatch{ProxyResourceID: &empty})
}

// DeleteAccount removes one Claude Code account row and releases its proxy binding.
func (s *Store) DeleteAccount(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("account id is required")
	}
	account, _ := s.GetAccount(ctx, accountID)
	res, err := s.db.ExecContext(ctx, `DELETE FROM claude_code_accounts WHERE id = ?`, accountID)
	if err != nil {
		return fmt.Errorf("delete claude code account: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	if account != nil && strings.TrimSpace(account.AuthID) != "" {
		routingScope := AccountRoutingScope(account.PoolID)
		claudeapipool.ClearScopedAccountQuotaRouting(routingScope, account.AuthID)
		claudeapipool.ClearScopedAccountBindings(routingScope, account.AuthID)
	}
	return nil
}

// MarkAccountTestResult updates one account's health/test fields.
func (s *Store) MarkAccountTestResult(ctx context.Context, accountID string, ok bool, message string) (*ClaudeCodeAccount, error) {
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	status := "healthy"
	failures := 0
	lastError := ""
	if !ok {
		status = "unhealthy"
		failures = account.ConsecutiveFailures + 1
		lastError = strings.TrimSpace(message)
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_accounts
SET test_status = ?, consecutive_failures = ?, last_test_at = ?, last_error = ?, updated_at = ?
WHERE id = ?
`, status, failures, dbTime(now), lastError, dbTime(now), account.ID); err != nil {
		return nil, fmt.Errorf("mark claude code account test result: %w", err)
	}
	return s.GetAccount(ctx, account.ID)
}

// SaveAccountQuota stores the latest Claude OAuth usage snapshot for one account.
func (s *Store) SaveAccountQuota(ctx context.Context, quota AccountQuota) (*ClaudeCodeAccount, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	accountID := strings.TrimSpace(quota.AccountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if _, err := s.GetAccount(ctx, accountID); err != nil {
		return nil, err
	}
	status := normalizeQuotaStatus(quota.Status)
	windowsJSON, err := encodeQuotaWindows(normalizeQuotaWindows(quota.Windows))
	if err != nil {
		return nil, err
	}
	checkedAt := quota.CheckedAt
	if checkedAt == nil {
		now := time.Now()
		checkedAt = &now
	}
	nowText := dbTime(time.Now())
	if _, err := s.db.ExecContext(ctx, `
	INSERT INTO claude_code_account_quota(account_id, status, windows_json, raw_json, probe_json, last_error, checked_at, updated_at)
	VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(account_id) DO UPDATE SET
	  status = excluded.status,
	  windows_json = excluded.windows_json,
	  raw_json = excluded.raw_json,
	  probe_json = excluded.probe_json,
	  last_error = excluded.last_error,
	  checked_at = excluded.checked_at,
	  updated_at = excluded.updated_at
		`, accountID, status, windowsJSON, strings.TrimSpace(quota.RawJSON), encodeAccountQuotaProbe(quota.Probe), strings.TrimSpace(quota.LastError), dbTime(*checkedAt), nowText); err != nil {
		return nil, fmt.Errorf("save claude code account quota: %w", err)
	}
	return s.GetAccount(ctx, accountID)
}

// ListModels returns Claude Code account-pool model mappings.
func (s *Store) ListModels(ctx context.Context, enabledOnly bool) ([]ClaudeCodeModel, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	where := ""
	if enabledOnly {
		where = "WHERE enabled = 1"
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, alias, enabled, source, note, created_at, updated_at
FROM claude_code_models
`+where+`
ORDER BY enabled DESC, updated_at DESC, alias ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list claude code models: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]ClaudeCodeModel, 0)
	for rows.Next() {
		model, err := scanClaudeCodeModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code models: %w", err)
	}
	return out, nil
}

// UpsertModel creates or updates a model mapping by alias.
func (s *Store) UpsertModel(ctx context.Context, model ClaudeCodeModel) (*ClaudeCodeModel, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	name := strings.TrimSpace(model.Name)
	if name == "" {
		return nil, fmt.Errorf("model name is required")
	}
	alias := strings.TrimSpace(model.Alias)
	if alias == "" {
		alias = name
	}
	source := strings.TrimSpace(model.Source)
	if source == "" {
		source = "manual"
	}
	enabled := model.Enabled
	if !model.Enabled && strings.TrimSpace(model.ID) == "" {
		enabled = true
	}
	existing, errExisting := s.GetModelByAlias(ctx, alias)
	if errExisting != nil && !errors.Is(errExisting, sql.ErrNoRows) {
		return nil, errExisting
	}
	now := dbTime(time.Now())
	if existing != nil {
		if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_models
SET name = ?, enabled = ?, source = ?, note = ?, updated_at = ?
WHERE id = ?
`, name, boolInt(enabled), source, strings.TrimSpace(model.Note), now, existing.ID); err != nil {
			return nil, mapSQLiteConstraintError(err, "claude code model")
		}
		return s.GetModel(ctx, existing.ID)
	}
	id := strings.TrimSpace(model.ID)
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_models(id, name, alias, enabled, source, note, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
`, id, name, alias, boolInt(enabled), source, strings.TrimSpace(model.Note), now, now); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code model")
	}
	return s.GetModelByAlias(ctx, alias)
}

// PatchModel updates one model mapping by id.
func (s *Store) PatchModel(ctx context.Context, id string, patch ClaudeCodeModelPatch) (*ClaudeCodeModel, error) {
	current, err := s.GetModel(ctx, id)
	if err != nil {
		return nil, err
	}
	name := current.Name
	alias := current.Alias
	enabled := current.Enabled
	source := current.Source
	note := current.Note
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
	}
	if patch.Alias != nil {
		alias = strings.TrimSpace(*patch.Alias)
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if patch.Source != nil {
		source = strings.TrimSpace(*patch.Source)
	}
	if patch.Note != nil {
		note = strings.TrimSpace(*patch.Note)
	}
	if name == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if alias == "" {
		alias = name
	}
	if source == "" {
		source = "manual"
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_models
SET name = ?, alias = ?, enabled = ?, source = ?, note = ?, updated_at = ?
WHERE id = ?
`, name, alias, boolInt(enabled), source, note, dbTime(time.Now()), current.ID); err != nil {
		return nil, mapSQLiteConstraintError(err, "claude code model")
	}
	return s.GetModel(ctx, current.ID)
}

// DeleteModel removes one model mapping.
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("model id is required")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM claude_code_models WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete claude code model: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetModel returns one model mapping by id.
func (s *Store) GetModel(ctx context.Context, id string) (*ClaudeCodeModel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("model id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, alias, enabled, source, note, created_at, updated_at
FROM claude_code_models
WHERE id = ?
`, id)
	if err != nil {
		return nil, fmt.Errorf("get claude code model: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate claude code model: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	model, err := scanClaudeCodeModel(rows)
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// GetModelByAlias returns one model mapping by external alias.
func (s *Store) GetModelByAlias(ctx context.Context, alias string) (*ClaudeCodeModel, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("model alias is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, alias, enabled, source, note, created_at, updated_at
FROM claude_code_models
WHERE lower(alias) = lower(?)
`, alias)
	if err != nil {
		return nil, fmt.Errorf("get claude code model by alias: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate claude code model by alias: %w", err)
		}
		return nil, sql.ErrNoRows
	}
	model, err := scanClaudeCodeModel(rows)
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// ResolveModelAlias resolves an external model alias for dedicated public API calls.
func (s *Store) ResolveModelAlias(ctx context.Context, requested string) (string, bool, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", false, fmt.Errorf("model is required")
	}
	model, err := s.GetModelByAlias(ctx, requested)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !model.Enabled {
		return "", false, nil
	}
	return model.Name, true, nil
}

// FindAccountOverlay finds pool metadata for a runtime Claude OAuth auth.
func (s *Store) FindAccountOverlay(ctx context.Context, authID, email string) (*AccountOverlay, bool, error) {
	authID = strings.TrimSpace(authID)
	email = strings.TrimSpace(email)
	if authID == "" && email == "" {
		return nil, false, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, a.cloak_user_id, CASE WHEN TRIM(a.auth_json) <> '' THEN 1 ELSE 0 END, a.auth_json,
	       a.email, a.enabled, a.health_status, a.blocked_until, a.blocked_reason, a.last_health_check_at, a.next_health_check_at,
	       a.priority, COALESCE(a.proxy_resource_id, ''),
       a.note, a.excluded_models_json, a.test_status, a.consecutive_failures,
       a.last_test_at, a.last_error, a.created_at, a.updated_at,
	       q.status, q.windows_json, q.raw_json, q.probe_json, q.last_error, q.checked_at,
       p.id, p.name, p.proxy_url, p.exit_ip, p.enabled, p.health_status, p.latency_ms,
       p.consecutive_failures, p.last_checked_at, p.last_error, p.tags_json, p.note,
       p.created_at, p.updated_at
FROM claude_code_accounts a
LEFT JOIN claude_code_account_quota q ON q.account_id = a.id
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
WHERE (? <> '' AND a.auth_id = ?) OR (? <> '' AND lower(a.email) = lower(?))
ORDER BY CASE WHEN a.auth_id = ? THEN 0 ELSE 1 END
LIMIT 1
`, authID, authID, email, email, authID)
	if err != nil {
		return nil, false, fmt.Errorf("find claude code account overlay: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, fmt.Errorf("iterate claude code account overlay: %w", err)
		}
		return nil, false, nil
	}
	account, err := scanAccountWithProxy(rows)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close claude code account overlay rows: %w", err)
	}
	s.hydrateAccountRuntime(ctx, &account)
	return &AccountOverlay{Account: account, Proxy: account.Proxy}, true, nil
}

// Summary returns compact counts for the console.
func (s *Store) Summary(ctx context.Context) (ConsoleSummary, error) {
	var summary ConsoleSummary
	proxies, err := s.ListProxies(ctx)
	if err != nil {
		return summary, err
	}
	for _, proxy := range proxies {
		summary.ProxyTotal++
		if proxy.BoundAccountID != "" {
			summary.ProxyBound++
		}
		switch normalizeHealthStatus(proxy.HealthStatus, proxy.Enabled) {
		case HealthHealthy:
			summary.ProxyHealthy++
		case HealthUnhealthy:
			summary.ProxyUnhealthy++
		case HealthDisabled:
			summary.ProxyDisabled++
		default:
			summary.ProxyUnknown++
		}
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		return summary, err
	}
	for _, account := range accounts {
		summary.AccountTotal++
		if account.Enabled {
			summary.AccountEnabled++
		}
		if account.ProxyResourceID != "" {
			summary.AccountBound++
		}
	}
	return summary, nil
}

func assertProxyBindableTx(ctx context.Context, tx *sql.Tx, accountID, proxyID, reservationOwner, reservationItem string) error {
	var enabled int
	var health string
	if err := tx.QueryRowContext(ctx, `SELECT enabled, health_status FROM proxy_resources WHERE id = ?`, proxyID).Scan(&enabled, &health); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("proxy resource not found")
		}
		return fmt.Errorf("read proxy before bind: %w", err)
	}
	if enabled == 0 {
		return fmt.Errorf("proxy resource is disabled")
	}
	if normalizeHealthStatus(health, true) == HealthUnhealthy {
		return fmt.Errorf("proxy resource is unhealthy")
	}
	var reservedOwner, reservedItem string
	errReservation := tx.QueryRowContext(ctx, `SELECT owner_id, item_id FROM proxy_reservations WHERE proxy_resource_id = ? AND expires_at > ?`, proxyID, dbTime(time.Now())).Scan(&reservedOwner, &reservedItem)
	if errReservation != nil && !errors.Is(errReservation, sql.ErrNoRows) {
		return fmt.Errorf("check proxy reservation: %w", errReservation)
	}
	if errReservation == nil && (reservationOwner == "" || reservedOwner != reservationOwner || reservedItem != reservationItem) {
		return fmt.Errorf("proxy resource is reserved")
	}
	var boundAccount string
	err := tx.QueryRowContext(ctx, `SELECT id FROM claude_code_accounts WHERE proxy_resource_id = ? AND id <> ? LIMIT 1`, proxyID, accountID).Scan(&boundAccount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check proxy binding: %w", err)
	}
	if boundAccount != "" {
		return fmt.Errorf("proxy resource is already bound")
	}
	return nil
}

func scanProxy(rows interface {
	Scan(dest ...interface{}) error
}) (ProxyResource, error) {
	var proxy ProxyResource
	var enabled int
	var lastChecked sql.NullString
	var tagsJSON string
	var createdRaw, updatedRaw string
	var reserved int
	var reservedUntil sql.NullString
	if err := rows.Scan(
		&proxy.ID,
		&proxy.Name,
		&proxy.ProxyURL,
		&proxy.ExitIP,
		&enabled,
		&proxy.HealthStatus,
		&proxy.LatencyMS,
		&proxy.ConsecutiveFailures,
		&lastChecked,
		&proxy.LastError,
		&tagsJSON,
		&proxy.Note,
		&createdRaw,
		&updatedRaw,
		&proxy.BoundAccountID,
		&proxy.BoundAccountEmail,
		&reserved,
		&reservedUntil,
	); err != nil {
		return proxy, fmt.Errorf("scan proxy resource: %w", err)
	}
	proxy.Enabled = enabled != 0
	proxy.Reserved = reserved != 0
	proxy.ReservedUntil = parseNullTime(reservedUntil)
	proxy.HealthStatus = normalizeHealthStatus(proxy.HealthStatus, proxy.Enabled)
	proxy.LastCheckedAt = parseNullTime(lastChecked)
	proxy.CreatedAt = parseDBTime(createdRaw)
	proxy.UpdatedAt = parseDBTime(updatedRaw)
	proxy.Tags = decodeStringList(tagsJSON)
	proxy.ProxyURLPreview = proxyutil.Redact(proxy.ProxyURL)
	return proxy, nil
}

func scanAccountWithProxy(rows interface {
	Scan(dest ...interface{}) error
}) (ClaudeCodeAccount, error) {
	var account ClaudeCodeAccount
	var enabled, hasAuthData int
	var authJSON, excludedJSON string
	var accountCreatedRaw, accountUpdatedRaw string
	var accountLastTestRaw, blockedUntilRaw, lastHealthCheckRaw, nextHealthCheckRaw sql.NullString
	var quotaStatus, quotaWindowsJSON, quotaRawJSON, quotaProbeJSON, quotaLastError, quotaCheckedRaw sql.NullString
	var proxyID sql.NullString
	var proxyName, proxyURL, proxyExitIP, proxyHealth, proxyLastError, proxyTagsJSON, proxyNote sql.NullString
	var proxyEnabled, proxyLatencyMS, proxyFailures sql.NullInt64
	var proxyLastChecked, proxyCreatedRaw, proxyUpdatedRaw sql.NullString
	if err := rows.Scan(
		&account.ID,
		&account.PoolID,
		&account.AuthID,
		&account.CloakUserID,
		&hasAuthData,
		&authJSON,
		&account.Email,
		&enabled,
		&account.HealthStatus,
		&blockedUntilRaw,
		&account.BlockedReason,
		&lastHealthCheckRaw,
		&nextHealthCheckRaw,
		&account.Priority,
		&account.ProxyResourceID,
		&account.Note,
		&excludedJSON,
		&account.TestStatus,
		&account.ConsecutiveFailures,
		&accountLastTestRaw,
		&account.LastError,
		&accountCreatedRaw,
		&accountUpdatedRaw,
		&quotaStatus,
		&quotaWindowsJSON,
		&quotaRawJSON,
		&quotaProbeJSON,
		&quotaLastError,
		&quotaCheckedRaw,
		&proxyID,
		&proxyName,
		&proxyURL,
		&proxyExitIP,
		&proxyEnabled,
		&proxyHealth,
		&proxyLatencyMS,
		&proxyFailures,
		&proxyLastChecked,
		&proxyLastError,
		&proxyTagsJSON,
		&proxyNote,
		&proxyCreatedRaw,
		&proxyUpdatedRaw,
	); err != nil {
		return account, fmt.Errorf("scan claude code account: %w", err)
	}
	account.Enabled = enabled != 0
	account.Schedulable = account.Enabled
	account.HealthStatus = normalizeAccountHealthStatus(account.HealthStatus)
	account.BlockedUntil = parseNullTime(blockedUntilRaw)
	account.LastHealthCheckAt = parseNullTime(lastHealthCheckRaw)
	account.NextHealthCheckAt = parseNullTime(nextHealthCheckRaw)
	account.HasAuthData = hasAuthData != 0
	account.TokenExpiresAt = tokenExpiresAtFromAuthJSON(authJSON)
	account.TestStatus = normalizeAccountTestStatus(account.TestStatus)
	account.LastTestAt = parseNullTime(accountLastTestRaw)
	account.CreatedAt = parseDBTime(accountCreatedRaw)
	account.UpdatedAt = parseDBTime(accountUpdatedRaw)
	account.ExcludedModels = decodeStringList(excludedJSON)
	if quotaStatus.Valid || quotaCheckedRaw.Valid || quotaLastError.Valid {
		quota := &AccountQuota{
			AccountID: account.ID,
			Status:    normalizeQuotaStatus(nullString(quotaStatus)),
			Windows:   normalizeQuotaWindows(decodeQuotaWindows(nullString(quotaWindowsJSON))),
			CheckedAt: parseNullTime(quotaCheckedRaw),
			LastError: nullString(quotaLastError),
			RawJSON:   nullString(quotaRawJSON),
			Probe:     decodeAccountQuotaProbe(nullString(quotaProbeJSON)),
		}
		account.Quota = quota
	}
	if proxyID.Valid && proxyID.String != "" {
		proxy := &ProxyResource{
			ID:                  proxyID.String,
			Name:                nullString(proxyName),
			ProxyURL:            nullString(proxyURL),
			ExitIP:              nullString(proxyExitIP),
			Enabled:             proxyEnabled.Valid && proxyEnabled.Int64 != 0,
			HealthStatus:        nullString(proxyHealth),
			LatencyMS:           nullInt(proxyLatencyMS),
			ConsecutiveFailures: int(nullInt(proxyFailures)),
			LastCheckedAt:       parseNullTime(proxyLastChecked),
			LastError:           nullString(proxyLastError),
			Tags:                decodeStringList(nullString(proxyTagsJSON)),
			Note:                nullString(proxyNote),
			CreatedAt:           parseDBTime(nullString(proxyCreatedRaw)),
			UpdatedAt:           parseDBTime(nullString(proxyUpdatedRaw)),
		}
		proxy.HealthStatus = normalizeHealthStatus(proxy.HealthStatus, proxy.Enabled)
		proxy.ProxyURLPreview = proxyutil.Redact(proxy.ProxyURL)
		proxy.BoundAccountID = account.ID
		proxy.BoundAccountEmail = account.Email
		account.Proxy = proxy
	}
	account.applyDerivedHealth(time.Now())
	return account, nil
}

func scanClaudeCodeModel(rows interface {
	Scan(dest ...interface{}) error
}) (ClaudeCodeModel, error) {
	var model ClaudeCodeModel
	var enabled int
	var createdRaw, updatedRaw string
	if err := rows.Scan(
		&model.ID,
		&model.Name,
		&model.Alias,
		&enabled,
		&model.Source,
		&model.Note,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return model, fmt.Errorf("scan claude code model: %w", err)
	}
	model.Enabled = enabled != 0
	model.CreatedAt = parseDBTime(createdRaw)
	model.UpdatedAt = parseDBTime(updatedRaw)
	return model, nil
}

func normalizeAccountTestStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "healthy", "unhealthy", "testing":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unknown"
	}
}

func normalizeAccountHealthStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case AccountHealthChecking, AccountHealthHealthy, AccountHealthTemporarilyBlocked, AccountHealthManualRecovery:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return AccountHealthHealthy
	}
}

func validateProxyURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("proxy url is required")
	}
	setting, err := proxyutil.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if setting.Mode != proxyutil.ModeProxy {
		return "", fmt.Errorf("proxy url must use http, https, socks5, or socks5h")
	}
	return trimmed, nil
}

func defaultProxyName(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		return proxyutil.Redact(proxyURL)
	}
	return parsed.Host
}

func encodeStringList(values []string) (string, error) {
	values = normalizeStringList(values)
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode string list: %w", err)
	}
	return string(raw), nil
}

func decodeStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return normalizeStringList(values)
}

func encodeQuotaWindows(values []QuotaWindow) (string, error) {
	if values == nil {
		values = []QuotaWindow{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode quota windows: %w", err)
	}
	return string(raw), nil
}

func decodeQuotaWindows(raw string) []QuotaWindow {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []QuotaWindow{}
	}
	var values []QuotaWindow
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []QuotaWindow{}
	}
	for i := range values {
		values[i].Key = strings.TrimSpace(values[i].Key)
		values[i].Name = strings.TrimSpace(values[i].Name)
		values[i].RemainPercent = clampPercent(values[i].RemainPercent)
		values[i].UsedPercent = clampPercent(values[i].UsedPercent)
	}
	if values == nil {
		return []QuotaWindow{}
	}
	return values
}

func encodeAccountQuotaProbe(probe *AccountQuotaProbe) string {
	if probe == nil {
		return "{}"
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodeAccountQuotaProbe(raw string) *AccountQuotaProbe {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	var probe AccountQuotaProbe
	if err := json.Unmarshal([]byte(raw), &probe); err != nil || probe.RequestedAt.IsZero() {
		return nil
	}
	probe.ProfileRevision = strings.TrimSpace(probe.ProfileRevision)
	probe.TransportProfile = strings.TrimSpace(probe.TransportProfile)
	probe.ProxyMode = strings.TrimSpace(probe.ProxyMode)
	probe.ProxyResourceID = strings.TrimSpace(probe.ProxyResourceID)
	return &probe
}

func normalizeQuotaStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "error", "checking":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unknown"
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func splitTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return normalizeStringList(strings.Split(raw, ","))
}

func insertEventTx(ctx context.Context, tx *sql.Tx, typ, message string, data interface{}) error {
	if tx == nil {
		return nil
	}
	if data == nil {
		data = map[string]string{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode resource pool event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pool_events(type, message, data_json, created_at) VALUES(?, ?, ?, ?)`, strings.TrimSpace(typ), strings.TrimSpace(message), string(raw), dbTime(time.Now())); err != nil {
		return fmt.Errorf("insert resource pool event: %w", err)
	}
	return nil
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func dbTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseDBTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseNullTime(raw sql.NullString) *time.Time {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	t := parseDBTime(raw.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func tokenExpiresAtFromAuthJSON(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	for _, meta := range []map[string]any{payload, nestedAuthMetadata(payload, "metadata"), nestedAuthMetadata(payload, "token")} {
		for _, key := range []string{"expired", "expire", "expires_at", "expiresAt", "expiry", "expires"} {
			if ts, ok := parseAuthTokenExpiry(meta[key]); ok && !ts.IsZero() {
				return &ts
			}
		}
	}
	return nil
}

func nestedAuthMetadata(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	if nested, ok := payload[key].(map[string]any); ok {
		return nested
	}
	if raw, ok := payload[key].(string); ok && strings.TrimSpace(raw) != "" {
		var nested map[string]any
		if err := json.Unmarshal([]byte(raw), &nested); err == nil {
			return nested
		}
	}
	if nested, ok := payload[key].(json.RawMessage); ok && len(nested) > 0 {
		var meta map[string]any
		if err := json.Unmarshal(nested, &meta); err == nil {
			return meta
		}
	}
	return nil
}

func parseAuthTokenExpiry(value any) (time.Time, bool) {
	switch v := value.(type) {
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if ts, err := time.Parse(layout, raw); err == nil {
				return ts, true
			}
		}
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return normaliseAuthUnixTime(unix), true
		}
	case float64:
		return normaliseAuthUnixTime(int64(v)), true
	case int64:
		return normaliseAuthUnixTime(v), true
	case json.Number:
		if unix, err := v.Int64(); err == nil {
			return normaliseAuthUnixTime(unix), true
		}
	}
	return time.Time{}, false
}

func normaliseAuthUnixTime(raw int64) time.Time {
	if raw <= 0 {
		return time.Time{}
	}
	if raw > 1_000_000_000_000 {
		return time.UnixMilli(raw)
	}
	return time.Unix(raw, 0)
}

func nullInt(v sql.NullInt64) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

func mapSQLiteConstraintError(err error, subject string) error {
	if err == nil {
		return nil
	}
	if isUniqueConstraint(err) {
		return fmt.Errorf("%s already exists: %w", subject, err)
	}
	return err
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "constraint") &&
		strings.Contains(strings.ToLower(err.Error()), "unique")
}
