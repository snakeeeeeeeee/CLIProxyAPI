package resourcepool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const modelPricingMigrationMarker = "account_pool_model_pricing_v7"

var activeModelPriceVersionID atomic.Int64

var builtinModelPrices = []ModelPriceUpdate{
	{ModelPattern: "claude-sonnet-4*", InputPerMillion: 3, OutputPerMillion: 15, CacheWrite5mPerMillion: 3.75, CacheWrite1hPerMillion: 6, CacheReadPerMillion: 0.30},
	{ModelPattern: "claude-haiku-4-5*", InputPerMillion: 1, OutputPerMillion: 5, CacheWrite5mPerMillion: 1.25, CacheWrite1hPerMillion: 2, CacheReadPerMillion: 0.10},
	{ModelPattern: "claude-opus-4-5*", InputPerMillion: 5, OutputPerMillion: 25, CacheWrite5mPerMillion: 6.25, CacheWrite1hPerMillion: 10, CacheReadPerMillion: 0.50},
	{ModelPattern: "claude-opus-4-6*", InputPerMillion: 5, OutputPerMillion: 25, CacheWrite5mPerMillion: 6.25, CacheWrite1hPerMillion: 10, CacheReadPerMillion: 0.50},
	{ModelPattern: "claude-opus-4-7*", InputPerMillion: 5, OutputPerMillion: 25, CacheWrite5mPerMillion: 6.25, CacheWrite1hPerMillion: 10, CacheReadPerMillion: 0.50},
	{ModelPattern: "claude-opus-4-8*", InputPerMillion: 5, OutputPerMillion: 25, CacheWrite5mPerMillion: 6.25, CacheWrite1hPerMillion: 10, CacheReadPerMillion: 0.50},
	{ModelPattern: "claude-opus-4-1*", InputPerMillion: 15, OutputPerMillion: 75, CacheWrite5mPerMillion: 18.75, CacheWrite1hPerMillion: 30, CacheReadPerMillion: 1.50},
	{ModelPattern: "claude-opus-4-20250514*", InputPerMillion: 15, OutputPerMillion: 75, CacheWrite5mPerMillion: 18.75, CacheWrite1hPerMillion: 30, CacheReadPerMillion: 1.50},
	{ModelPattern: "claude-3-7-sonnet*", InputPerMillion: 3, OutputPerMillion: 15, CacheWrite5mPerMillion: 3.75, CacheWrite1hPerMillion: 6, CacheReadPerMillion: 0.30},
	{ModelPattern: "claude-3-5-sonnet*", InputPerMillion: 3, OutputPerMillion: 15, CacheWrite5mPerMillion: 3.75, CacheWrite1hPerMillion: 6, CacheReadPerMillion: 0.30},
	{ModelPattern: "claude-3-5-haiku*", InputPerMillion: 0.80, OutputPerMillion: 4, CacheWrite5mPerMillion: 1, CacheWrite1hPerMillion: 1.60, CacheReadPerMillion: 0.08},
}

// ActiveModelPriceVersionID returns the process-local request-start price revision.
func ActiveModelPriceVersionID() int64 {
	return activeModelPriceVersionID.Load()
}

func (s *Store) migrateModelPricingV7(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	var marker string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM pool_config WHERE key = ?`, modelPricingMigrationMarker).Scan(&marker); err == nil {
		return s.refreshActiveModelPriceVersion(ctx)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read model pricing v7 migration marker: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin model pricing v7 migration: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	versionID, err := ensureBuiltinModelPriceVersionTx(ctx, tx)
	if err != nil {
		return err
	}
	if err := backfillUsagePricingTx(ctx, tx, versionID, builtinModelPrices); err != nil {
		return err
	}
	now := dbTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pool_config(key, value, created_at, updated_at)
VALUES(?, '1', ?, ?)
ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = excluded.updated_at
	`, modelPricingMigrationMarker, now, now); err != nil {
		return fmt.Errorf("write model pricing v7 migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit model pricing v7 migration: %w", err)
	}
	activeModelPriceVersionID.Store(versionID)
	return nil
}

func ensureBuiltinModelPriceVersionTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var versionID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM claude_code_model_price_versions ORDER BY revision DESC LIMIT 1`).Scan(&versionID); err == nil {
		return versionID, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read current model price version: %w", err)
	}
	now := dbTime(time.Now())
	result, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_model_price_versions(revision, source, note, created_at)
VALUES(1, 'builtin', 'Anthropic standard pricing baseline', ?)
	`, now)
	if err != nil {
		return 0, fmt.Errorf("create builtin model price version: %w", err)
	}
	versionID, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read builtin model price version id: %w", err)
	}
	for _, price := range builtinModelPrices {
		if err := insertModelPriceTx(ctx, tx, versionID, price, now); err != nil {
			return 0, err
		}
	}
	return versionID, nil
}

type legacyUsagePricingRow struct {
	id            int64
	model         string
	input         int64
	output        int64
	cacheRead     int64
	cacheCreation int64
}

func backfillUsagePricingTx(ctx context.Context, tx *sql.Tx, versionID int64, prices []ModelPriceUpdate) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens
FROM claude_code_usage_ledger
WHERE price_version_id = 0
	`)
	if err != nil {
		return fmt.Errorf("list legacy usage for pricing: %w", err)
	}
	legacy := make([]legacyUsagePricingRow, 0)
	for rows.Next() {
		var row legacyUsagePricingRow
		if err := rows.Scan(&row.id, &row.model, &row.input, &row.output, &row.cacheRead, &row.cacheCreation); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy usage for pricing: %w", err)
		}
		legacy = append(legacy, row)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy usage pricing rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy usage for pricing: %w", err)
	}
	for _, row := range legacy {
		price, ok := matchModelPriceUpdates(prices, row.model)
		status := "unpriced"
		pattern := ""
		cost := float64(0)
		if ok {
			status = "priced"
			if row.cacheCreation > 0 {
				status = "estimated"
			}
			pattern = normalizeModelPricePattern(price.ModelPattern)
			cost = calculateUsageCost(price, row.input, row.output, row.cacheRead, row.cacheCreation, 0)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE claude_code_usage_ledger
SET cache_creation_5m_tokens = cache_creation_tokens,
    cache_creation_1h_tokens = 0,
    price_version_id = ?, price_model_pattern = ?, pricing_status = ?, estimated_cost = ?
WHERE id = ?
		`, versionID, pattern, status, cost, row.id); err != nil {
			return fmt.Errorf("backfill usage price row %d: %w", row.id, err)
		}
	}
	return nil
}

// CurrentModelPriceVersion returns the latest immutable global pricing snapshot.
func (s *Store) CurrentModelPriceVersion(ctx context.Context) (*ModelPriceVersion, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, revision, source, note, created_at
FROM claude_code_model_price_versions
ORDER BY revision DESC
LIMIT 1
	`)
	version, err := scanModelPriceVersion(row)
	if err != nil {
		return nil, err
	}
	version.Prices, err = s.listModelPrices(ctx, version.ID, version.Revision)
	if err != nil {
		return nil, err
	}
	activeModelPriceVersionID.Store(version.ID)
	return &version, nil
}

// ListModelPriceVersions returns safe pricing revision metadata, newest first.
func (s *Store) ListModelPriceVersions(ctx context.Context, limit int) ([]ModelPriceVersion, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, revision, source, note, created_at
FROM claude_code_model_price_versions
ORDER BY revision DESC
LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list model price versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	versions := make([]ModelPriceVersion, 0)
	for rows.Next() {
		version, errScan := scanModelPriceVersion(rows)
		if errScan != nil {
			return nil, errScan
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model price versions: %w", err)
	}
	return versions, nil
}

// CreateModelPriceVersion applies updates on top of the current revision.
func (s *Store) CreateModelPriceVersion(ctx context.Context, updates []ModelPriceUpdate, note string) (*ModelPriceVersion, error) {
	if len(updates) == 0 {
		return nil, fmt.Errorf("at least one model price update is required")
	}
	for index := range updates {
		normalized, err := normalizeModelPriceUpdate(updates[index])
		if err != nil {
			return nil, err
		}
		updates[index] = normalized
	}
	current, err := s.CurrentModelPriceVersion(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin model price revision: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	now := dbTime(time.Now())
	result, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_model_price_versions(revision, source, note, created_at)
VALUES(?, 'manual', ?, ?)
	`, current.Revision+1, strings.TrimSpace(note), now)
	if err != nil {
		return nil, fmt.Errorf("create model price revision: %w", err)
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read model price revision id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_model_prices(version_id, model_pattern, input_per_million, output_per_million,
    cache_write_5m_per_million, cache_write_1h_per_million, cache_read_per_million, created_at)
SELECT ?, model_pattern, input_per_million, output_per_million,
       cache_write_5m_per_million, cache_write_1h_per_million, cache_read_per_million, ?
FROM claude_code_model_prices
WHERE version_id = ?
	`, versionID, now, current.ID); err != nil {
		return nil, fmt.Errorf("copy model price revision: %w", err)
	}
	for _, update := range updates {
		if update.Remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM claude_code_model_prices WHERE version_id = ? AND model_pattern = ?`, versionID, update.ModelPattern); err != nil {
				return nil, fmt.Errorf("remove model price %s: %w", update.ModelPattern, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM claude_code_model_prices WHERE version_id = ? AND model_pattern = ?`, versionID, update.ModelPattern); err != nil {
			return nil, fmt.Errorf("replace model price %s: %w", update.ModelPattern, err)
		}
		if err := insertModelPriceTx(ctx, tx, versionID, update, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit model price revision: %w", err)
	}
	activeModelPriceVersionID.Store(versionID)
	return s.CurrentModelPriceVersion(ctx)
}

// ResolveModelPrice returns the most-specific exact or trailing-wildcard match.
func (s *Store) ResolveModelPrice(ctx context.Context, versionID int64, model string) (*ModelPrice, error) {
	if versionID <= 0 {
		version, err := s.CurrentModelPriceVersion(ctx)
		if err != nil {
			return nil, err
		}
		versionID = version.ID
	}
	var revision int
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM claude_code_model_price_versions WHERE id = ?`, versionID).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read model price revision: %w", err)
	}
	prices, err := s.listModelPrices(ctx, versionID, revision)
	if err != nil {
		return nil, err
	}
	matched, ok := matchModelPrices(prices, model)
	if !ok {
		return nil, nil
	}
	return &matched, nil
}

// AttachCurrentModelPrices adds current upstream-model prices to model mappings.
func (s *Store) AttachCurrentModelPrices(ctx context.Context, models []ClaudeCodeModel) error {
	version, err := s.CurrentModelPriceVersion(ctx)
	if err != nil {
		return err
	}
	for index := range models {
		price, ok := matchModelPrices(version.Prices, models[index].Name)
		if ok {
			copyPrice := price
			models[index].Price = &copyPrice
		}
	}
	return nil
}

// ApplyUsagePricing resolves an immutable snapshot and calculates raw upstream cost.
func (s *Store) ApplyUsagePricing(ctx context.Context, entry *UsageLedgerEntry) error {
	if entry == nil {
		return nil
	}
	if entry.PriceVersionID <= 0 {
		entry.PriceVersionID = ActiveModelPriceVersionID()
	}
	price, err := s.ResolveModelPrice(ctx, entry.PriceVersionID, entry.Model)
	if err != nil {
		return err
	}
	entry.CacheCreationTokens = nonNegativeInt64(entry.CacheCreationTokens)
	entry.CacheCreation5m = nonNegativeInt64(entry.CacheCreation5m)
	entry.CacheCreation1h = nonNegativeInt64(entry.CacheCreation1h)
	explicitCreation := entry.CacheCreation5m + entry.CacheCreation1h
	estimated := false
	if explicitCreation < entry.CacheCreationTokens {
		entry.CacheCreation5m += entry.CacheCreationTokens - explicitCreation
		estimated = entry.CacheCreationTokens > 0
	} else if explicitCreation > entry.CacheCreationTokens {
		entry.CacheCreationTokens = explicitCreation
	}
	if price == nil {
		entry.PricingStatus = "unpriced"
		entry.PriceModelPattern = ""
		entry.EstimatedCost = 0
		return nil
	}
	entry.PriceVersionID = price.VersionID
	entry.PriceModelPattern = price.ModelPattern
	entry.PricingStatus = "priced"
	if estimated {
		entry.PricingStatus = "estimated"
	}
	entry.EstimatedCost = calculateUsageCost(ModelPriceUpdate{
		InputPerMillion:        price.InputPerMillion,
		OutputPerMillion:       price.OutputPerMillion,
		CacheWrite5mPerMillion: price.CacheWrite5mPerMillion,
		CacheWrite1hPerMillion: price.CacheWrite1hPerMillion,
		CacheReadPerMillion:    price.CacheReadPerMillion,
	}, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheCreation5m, entry.CacheCreation1h)
	return nil
}

func (s *Store) refreshActiveModelPriceVersion(ctx context.Context) error {
	var versionID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM claude_code_model_price_versions ORDER BY revision DESC LIMIT 1`).Scan(&versionID); err != nil {
		return fmt.Errorf("refresh active model price version: %w", err)
	}
	activeModelPriceVersionID.Store(versionID)
	return nil
}

func (s *Store) listModelPrices(ctx context.Context, versionID int64, revision int) ([]ModelPrice, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT version_id, model_pattern, input_per_million, output_per_million,
       cache_write_5m_per_million, cache_write_1h_per_million, cache_read_per_million, created_at
FROM claude_code_model_prices
WHERE version_id = ?
ORDER BY lower(model_pattern) ASC
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("list model prices: %w", err)
	}
	defer func() { _ = rows.Close() }()
	prices := make([]ModelPrice, 0)
	for rows.Next() {
		var price ModelPrice
		var createdRaw string
		if err := rows.Scan(&price.VersionID, &price.ModelPattern, &price.InputPerMillion, &price.OutputPerMillion,
			&price.CacheWrite5mPerMillion, &price.CacheWrite1hPerMillion, &price.CacheReadPerMillion, &createdRaw); err != nil {
			return nil, fmt.Errorf("scan model price: %w", err)
		}
		price.Revision = revision
		price.CreatedAt = parseDBTime(createdRaw)
		prices = append(prices, price)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model prices: %w", err)
	}
	return prices, nil
}

func insertModelPriceTx(ctx context.Context, tx *sql.Tx, versionID int64, price ModelPriceUpdate, createdAt string) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO claude_code_model_prices(version_id, model_pattern, input_per_million, output_per_million,
    cache_write_5m_per_million, cache_write_1h_per_million, cache_read_per_million, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, versionID, normalizeModelPricePattern(price.ModelPattern), price.InputPerMillion, price.OutputPerMillion,
		price.CacheWrite5mPerMillion, price.CacheWrite1hPerMillion, price.CacheReadPerMillion, createdAt); err != nil {
		return fmt.Errorf("insert model price %s: %w", price.ModelPattern, err)
	}
	return nil
}

func scanModelPriceVersion(row interface{ Scan(...interface{}) error }) (ModelPriceVersion, error) {
	var version ModelPriceVersion
	var createdRaw string
	if err := row.Scan(&version.ID, &version.Revision, &version.Source, &version.Note, &createdRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return version, sql.ErrNoRows
		}
		return version, fmt.Errorf("scan model price version: %w", err)
	}
	version.CreatedAt = parseDBTime(createdRaw)
	return version, nil
}

func normalizeModelPriceUpdate(update ModelPriceUpdate) (ModelPriceUpdate, error) {
	update.ModelPattern = normalizeModelPricePattern(update.ModelPattern)
	if update.ModelPattern == "" {
		return update, fmt.Errorf("model price pattern is required")
	}
	if strings.Count(update.ModelPattern, "*") > 1 || (strings.Contains(update.ModelPattern, "*") && !strings.HasSuffix(update.ModelPattern, "*")) {
		return update, fmt.Errorf("model price pattern only supports one trailing wildcard")
	}
	values := []float64{update.InputPerMillion, update.OutputPerMillion, update.CacheWrite5mPerMillion, update.CacheWrite1hPerMillion, update.CacheReadPerMillion}
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return update, fmt.Errorf("model prices must be finite and non-negative")
		}
	}
	return update, nil
}

func normalizeModelPricePattern(pattern string) string {
	return strings.ToLower(strings.TrimSpace(pattern))
}

func matchModelPrices(prices []ModelPrice, model string) (ModelPrice, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	bestLength := -1
	var best ModelPrice
	for _, price := range prices {
		pattern := normalizeModelPricePattern(price.ModelPattern)
		prefix := strings.TrimSuffix(pattern, "*")
		matches := pattern == model || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, prefix))
		if matches && len(prefix) > bestLength {
			best = price
			bestLength = len(prefix)
		}
	}
	return best, bestLength >= 0
}

func matchModelPriceUpdates(prices []ModelPriceUpdate, model string) (ModelPriceUpdate, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	sorted := append([]ModelPriceUpdate(nil), prices...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(strings.TrimSuffix(sorted[i].ModelPattern, "*")) > len(strings.TrimSuffix(sorted[j].ModelPattern, "*"))
	})
	for _, price := range sorted {
		pattern := normalizeModelPricePattern(price.ModelPattern)
		if pattern == model || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))) {
			return price, true
		}
	}
	return ModelPriceUpdate{}, false
}

func calculateUsageCost(price ModelPriceUpdate, input, output, cacheRead, cacheWrite5m, cacheWrite1h int64) float64 {
	const perMillion = 1_000_000
	return float64(nonNegativeInt64(input))*price.InputPerMillion/perMillion +
		float64(nonNegativeInt64(output))*price.OutputPerMillion/perMillion +
		float64(nonNegativeInt64(cacheRead))*price.CacheReadPerMillion/perMillion +
		float64(nonNegativeInt64(cacheWrite5m))*price.CacheWrite5mPerMillion/perMillion +
		float64(nonNegativeInt64(cacheWrite1h))*price.CacheWrite1hPerMillion/perMillion
}
