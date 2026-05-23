package claudeapipool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS pool_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL DEFAULT 1,
	virtual_cache_json TEXT NOT NULL DEFAULT '{}',
	routing_json TEXT NOT NULL DEFAULT '{}',
	defaults_json TEXT NOT NULL DEFAULT '{}',
	models_json TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pool_items (
	position INTEGER PRIMARY KEY,
	item_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pool_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// ResolveDBPath resolves the fixed SQLite pool store beside the main config file.
func ResolveDBPath(configFilePath string, _ *config.Config) string {
	baseDir := "."
	if configFilePath != "" {
		baseDir = filepath.Dir(configFilePath)
	}
	return filepath.Clean(filepath.Join(baseDir, DefaultDBFileName))
}

// LoadStore reads the SQLite-backed pool store, migrating claude-api-pool.yaml on first use.
func LoadStore(configFilePath string, cfg *config.Config) (*File, error) {
	dbPath := ResolveDBPath(configFilePath, cfg)
	yamlPath := ResolvePath(configFilePath, cfg)
	db, err := openSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = db.Close()
	}()
	if err := migrateYAMLIfEmpty(context.Background(), db, yamlPath); err != nil {
		return nil, err
	}
	return loadSQLiteFile(context.Background(), db)
}

// SaveStore writes the pool document to the SQLite-backed pool store.
func SaveStore(configFilePath string, cfg *config.Config, doc *File) error {
	dbPath := ResolveDBPath(configFilePath, cfg)
	db, err := openSQLiteStore(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()
	return saveSQLiteFile(context.Background(), db, doc)
}

func openSQLiteStore(path string) (*sql.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("claude api pool sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create claude api pool sqlite dir: %w", err)
	}
	absPath, errAbs := filepath.Abs(path)
	if errAbs != nil {
		return nil, fmt.Errorf("resolve claude api pool sqlite path: %w", errAbs)
	}
	dsn := (&url.URL{
		Scheme: "file",
		Path:   absPath,
		RawQuery: url.Values{
			"_pragma": []string{
				"busy_timeout(5000)",
				"journal_mode(WAL)",
				"synchronous(NORMAL)",
			},
		}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open claude api pool sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize claude api pool sqlite: %w", err)
	}
	return db, nil
}

func migrateYAMLIfEmpty(ctx context.Context, db *sql.DB, yamlPath string) error {
	empty, err := sqliteStoreEmpty(ctx, db)
	if err != nil {
		return err
	}
	if !empty {
		return nil
	}
	if strings.TrimSpace(yamlPath) == "" {
		return nil
	}
	if _, err := os.Stat(yamlPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat claude api pool yaml: %w", err)
	}
	doc, err := Load(yamlPath)
	if err != nil {
		return err
	}
	if err := saveSQLiteFile(ctx, db, doc); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO pool_meta(key, value) VALUES('migrated_from_yaml', ?)`, yamlPath); err != nil {
		return fmt.Errorf("record claude api pool yaml migration: %w", err)
	}
	return nil
}

func sqliteStoreEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	var configCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pool_config`).Scan(&configCount); err != nil {
		return false, fmt.Errorf("count claude api pool config rows: %w", err)
	}
	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pool_items`).Scan(&itemCount); err != nil {
		return false, fmt.Errorf("count claude api pool item rows: %w", err)
	}
	return configCount == 0 && itemCount == 0, nil
}

func loadSQLiteFile(ctx context.Context, db *sql.DB) (*File, error) {
	doc := &File{Version: 1}
	var virtualCacheJSON, routingJSON, defaultsJSON, modelsJSON string
	err := db.QueryRowContext(ctx, `SELECT version, virtual_cache_json, routing_json, defaults_json, models_json FROM pool_config WHERE id = 1`).
		Scan(&doc.Version, &virtualCacheJSON, &routingJSON, &defaultsJSON, &modelsJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read claude api pool config from sqlite: %w", err)
	}
	if err == nil {
		if err := decodeSQLiteJSON(virtualCacheJSON, &doc.VirtualCache, "virtual cache"); err != nil {
			return nil, err
		}
		if err := decodeSQLiteJSON(routingJSON, &doc.Routing, "routing"); err != nil {
			return nil, err
		}
		if err := decodeSQLiteJSON(defaultsJSON, &doc.Defaults, "defaults"); err != nil {
			return nil, err
		}
		if err := decodeSQLiteJSON(modelsJSON, &doc.Models, "models"); err != nil {
			return nil, err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT item_json FROM pool_items ORDER BY position ASC`)
	if err != nil {
		return nil, fmt.Errorf("read claude api pool items from sqlite: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var itemJSON string
		if err := rows.Scan(&itemJSON); err != nil {
			return nil, fmt.Errorf("scan claude api pool item from sqlite: %w", err)
		}
		var item Item
		if err := decodeSQLiteJSON(itemJSON, &item, "item"); err != nil {
			return nil, err
		}
		doc.Items = append(doc.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude api pool items from sqlite: %w", err)
	}
	Normalize(doc)
	return doc, nil
}

func saveSQLiteFile(ctx context.Context, db *sql.DB, doc *File) error {
	if doc == nil {
		doc = &File{Version: 1}
	}
	Normalize(doc)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin claude api pool sqlite transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	virtualCacheJSON, err := marshalSQLiteJSON(doc.VirtualCache, "virtual cache")
	if err != nil {
		return err
	}
	routingJSON, err := marshalSQLiteJSON(doc.Routing, "routing")
	if err != nil {
		return err
	}
	defaultsJSON, err := marshalSQLiteJSON(doc.Defaults, "defaults")
	if err != nil {
		return err
	}
	modelsJSON, err := marshalSQLiteJSON(doc.Models, "models")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pool_config(id, version, virtual_cache_json, routing_json, defaults_json, models_json, created_at, updated_at)
VALUES(1, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	version = excluded.version,
	virtual_cache_json = excluded.virtual_cache_json,
	routing_json = excluded.routing_json,
	defaults_json = excluded.defaults_json,
	models_json = excluded.models_json,
	updated_at = excluded.updated_at
`, doc.Version, virtualCacheJSON, routingJSON, defaultsJSON, modelsJSON, now, now); err != nil {
		return fmt.Errorf("write claude api pool config to sqlite: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pool_items`); err != nil {
		return fmt.Errorf("clear claude api pool items in sqlite: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO pool_items(position, item_json, created_at, updated_at) VALUES(?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare claude api pool item insert: %w", err)
	}
	defer func() {
		_ = stmt.Close()
	}()
	for i := range doc.Items {
		itemJSON, err := marshalSQLiteJSON(doc.Items[i], "item")
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, i+1, itemJSON, now, now); err != nil {
			return fmt.Errorf("write claude api pool item %d to sqlite: %w", i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit claude api pool sqlite transaction: %w", err)
	}
	committed = true
	return nil
}

func marshalSQLiteJSON(value any, label string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal claude api pool %s for sqlite: %w", label, err)
	}
	return string(data), nil
}

func decodeSQLiteJSON(raw string, dest any, label string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return fmt.Errorf("decode claude api pool %s from sqlite: %w", label, err)
	}
	return nil
}
