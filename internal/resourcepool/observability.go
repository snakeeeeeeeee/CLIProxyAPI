package resourcepool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
)

const defaultUsageWindow = time.Hour
const defaultAvailabilityWindow = 2 * time.Hour

// SaveClaudeCodeProfile persists the Claude Code request-shape profile.
func (s *Store) SaveClaudeCodeProfile(ctx context.Context, profile ClaudeCodeProfile) (*ConfigFile, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	profile.UpdatedAt = &now
	if strings.TrimSpace(profile.UpdatedFrom) == "" {
		profile.UpdatedFrom = "manual"
	}
	doc.Profile = normalizeClaudeCodeProfile(profile)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin save claude code profile: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := savePoolConfigTx(ctx, tx, doc); err != nil {
		return nil, err
	}
	if err := insertEventTx(ctx, tx, "account_pool.profile", "claude code profile updated", nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit save claude code profile: %w", err)
	}
	return s.GetConfig(ctx)
}

// ListUsageCalibrations returns calibration rows for a profile fingerprint.
func (s *Store) ListUsageCalibrations(ctx context.Context, fingerprint string) ([]UsageCalibration, error) {
	if s == nil || s.db == nil {
		return []UsageCalibration{}, nil
	}
	fingerprint = strings.TrimSpace(fingerprint)
	where := ""
	args := []any{}
	if fingerprint != "" {
		where = `WHERE profile_fingerprint = ?`
		args = append(args, fingerprint)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT model, profile_fingerprint, overhead_tokens, status, checked_at, last_error, created_at, updated_at
FROM claude_code_usage_calibrations
`+where+`
ORDER BY updated_at DESC, model ASC
`, args...)
	if err != nil {
		return nil, fmt.Errorf("list claude code usage calibrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]UsageCalibration, 0)
	for rows.Next() {
		calibration, err := scanUsageCalibration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, calibration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code usage calibrations: %w", err)
	}
	return out, nil
}

// GetUsageCalibration returns one calibration row.
func (s *Store) GetUsageCalibration(ctx context.Context, model, fingerprint string) (*UsageCalibration, error) {
	model = strings.TrimSpace(model)
	fingerprint = strings.TrimSpace(fingerprint)
	if model == "" || fingerprint == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `
SELECT model, profile_fingerprint, overhead_tokens, status, checked_at, last_error, created_at, updated_at
FROM claude_code_usage_calibrations
WHERE model = ? AND profile_fingerprint = ?
`, model, fingerprint)
	calibration, err := scanUsageCalibration(row)
	if err != nil {
		return nil, err
	}
	return &calibration, nil
}

// UpsertUsageCalibration stores the latest calibration result.
func (s *Store) UpsertUsageCalibration(ctx context.Context, calibration UsageCalibration) (*UsageCalibration, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	calibration.Model = strings.TrimSpace(calibration.Model)
	calibration.ProfileFingerprint = strings.TrimSpace(calibration.ProfileFingerprint)
	if calibration.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if calibration.ProfileFingerprint == "" {
		return nil, fmt.Errorf("profile fingerprint is required")
	}
	calibration.Status = normalizeUsageCalibrationStatus(calibration.Status)
	if calibration.OverheadTokens < 0 {
		calibration.OverheadTokens = 0
	}
	now := time.Now()
	if calibration.CheckedAt == nil {
		calibration.CheckedAt = &now
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_usage_calibrations(model, profile_fingerprint, overhead_tokens, status, checked_at, last_error, created_at, updated_at)
VALUES(?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)
ON CONFLICT(model, profile_fingerprint) DO UPDATE SET
  overhead_tokens = excluded.overhead_tokens,
  status = excluded.status,
  checked_at = excluded.checked_at,
  last_error = excluded.last_error,
  updated_at = excluded.updated_at
`, calibration.Model, calibration.ProfileFingerprint, calibration.OverheadTokens, calibration.Status,
		dbTime(*calibration.CheckedAt), strings.TrimSpace(calibration.LastError), dbTime(now), dbTime(now))
	if err != nil {
		return nil, fmt.Errorf("save claude code usage calibration: %w", err)
	}
	return s.GetUsageCalibration(ctx, calibration.Model, calibration.ProfileFingerprint)
}

// UsageOverheadMapJSON returns calibrated overheads for embedding into runtime auth attributes.
func (s *Store) UsageOverheadMapJSON(ctx context.Context, fingerprint string) string {
	rows, err := s.ListUsageCalibrations(ctx, fingerprint)
	if err != nil || len(rows) == 0 {
		return ""
	}
	values := make(map[string]int64, len(rows))
	for _, row := range rows {
		if row.Status != UsageCalibrationCalibrated {
			continue
		}
		model := strings.TrimSpace(row.Model)
		if model == "" || row.ProfileFingerprint != strings.TrimSpace(fingerprint) {
			continue
		}
		values[model] = row.OverheadTokens
	}
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

// GetAccountCapacity returns stored or default capacity settings for one account.
func (s *Store) GetAccountCapacity(ctx context.Context, accountID string) (*AccountCapacityConfig, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT account_id, base_rpm, concurrency_limit, max_sessions, sticky_buffer, updated_at
FROM claude_code_account_capacity
WHERE account_id = ?
`, accountID)
	var cfg AccountCapacityConfig
	var updatedAt string
	err := row.Scan(&cfg.AccountID, &cfg.BaseRPM, &cfg.ConcurrencyLimit, &cfg.MaxSessions, &cfg.StickyConcurrencyReserve, &updatedAt)
	if err == nil {
		cfg.UpdatedAt = parseDBTime(updatedAt)
		return normalizeAccountCapacity(cfg, s.defaultAccountCapacity(ctx, accountID)), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get claude code account capacity: %w", err)
	}
	cfg = s.defaultAccountCapacity(ctx, accountID)
	return &cfg, nil
}

// SaveAccountCapacity persists capacity settings for one account.
func (s *Store) SaveAccountCapacity(ctx context.Context, accountID string, cfg AccountCapacityConfig) (*AccountCapacityConfig, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if _, err := s.GetAccount(ctx, accountID); err != nil {
		return nil, err
	}
	cfg.AccountID = accountID
	cfg = *normalizeAccountCapacity(cfg, s.defaultAccountCapacity(ctx, accountID))
	now := time.Now()
	cfg.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_account_capacity(account_id, base_rpm, concurrency_limit, max_sessions, sticky_buffer, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET
  base_rpm = excluded.base_rpm,
  concurrency_limit = excluded.concurrency_limit,
  max_sessions = excluded.max_sessions,
  sticky_buffer = excluded.sticky_buffer,
  updated_at = excluded.updated_at
`, cfg.AccountID, cfg.BaseRPM, cfg.ConcurrencyLimit, cfg.MaxSessions, cfg.StickyConcurrencyReserve, dbTime(now)); err != nil {
		return nil, fmt.Errorf("save claude code account capacity: %w", err)
	}
	return s.GetAccountCapacity(ctx, accountID)
}

// PatchAccountCapacity applies a partial capacity update.
func (s *Store) PatchAccountCapacity(ctx context.Context, accountID string, patch AccountCapacityPatch) (*AccountCapacityConfig, error) {
	current, err := s.GetAccountCapacity(ctx, accountID)
	if err != nil {
		return nil, err
	}
	next := *current
	if patch.BaseRPM != nil {
		next.BaseRPM = *patch.BaseRPM
	}
	if patch.ConcurrencyLimit != nil {
		next.ConcurrencyLimit = *patch.ConcurrencyLimit
	}
	if patch.MaxSessions != nil {
		next.MaxSessions = *patch.MaxSessions
	}
	if patch.StickyConcurrencyReserve != nil {
		next.StickyConcurrencyReserve = *patch.StickyConcurrencyReserve
	}
	return s.SaveAccountCapacity(ctx, accountID, next)
}

func (s *Store) defaultAccountCapacity(ctx context.Context, accountID string) AccountCapacityConfig {
	effective := EffectiveClaudeCodePool(ClaudeCodePoolConfig{})
	var poolID string
	if s != nil && s.db != nil {
		_ = s.db.QueryRowContext(ctx, `SELECT pool_id FROM claude_code_accounts WHERE id = ?`, strings.TrimSpace(accountID)).Scan(&poolID)
		if scoped, err := s.EffectiveClaudeCodePoolForPool(ctx, poolID); err == nil {
			effective = scoped
		} else if doc, errConfig := s.GetConfig(ctx); errConfig == nil && doc != nil {
			effective = EffectiveClaudeCodePool(doc.ClaudeCode)
		}
	}
	return AccountCapacityConfig{
		AccountID:                accountID,
		BaseRPM:                  effective.Routing.PerAccountRPM,
		ConcurrencyLimit:         effective.Routing.PerAccountConcurrency,
		MaxSessions:              effective.Routing.MaxSessions,
		StickyConcurrencyReserve: effective.Routing.StickyConcurrencyReserve,
	}
}

func normalizeAccountCapacity(cfg AccountCapacityConfig, defaults AccountCapacityConfig) *AccountCapacityConfig {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	if cfg.BaseRPM <= 0 {
		cfg.BaseRPM = defaults.BaseRPM
	}
	if cfg.ConcurrencyLimit <= 0 {
		cfg.ConcurrencyLimit = defaults.ConcurrencyLimit
	}
	if cfg.MaxSessions < 0 {
		cfg.MaxSessions = 0
	}
	if cfg.StickyConcurrencyReserve <= 0 {
		cfg.StickyConcurrencyReserve = 1
	}
	if cfg.BaseRPM > 1000 {
		cfg.BaseRPM = 1000
	}
	if cfg.ConcurrencyLimit > 100 {
		cfg.ConcurrencyLimit = 100
	}
	if cfg.MaxSessions > 1000 {
		cfg.MaxSessions = 1000
	}
	if cfg.StickyConcurrencyReserve > 100 {
		cfg.StickyConcurrencyReserve = 100
	}
	return &cfg
}

// ListAccountModelStatuses returns model-level health rows for one account.
func (s *Store) ListAccountModelStatuses(ctx context.Context, accountID string) ([]AccountModelStatus, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return []AccountModelStatus{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT account_id, model, status, success_count, failure_count, rate_limit_count, overload_count,
       consecutive_failures, cooling_until, last_status_code, last_error, last_test_at, updated_at
FROM claude_code_account_model_status
WHERE account_id = ?
ORDER BY updated_at DESC, model ASC
`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list claude code account model statuses: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]AccountModelStatus, 0)
	for rows.Next() {
		status, err := scanAccountModelStatus(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code account model statuses: %w", err)
	}
	return out, nil
}

// MarkAccountModelResult records model-level success, failure, and cooldown state.
func (s *Store) MarkAccountModelResult(ctx context.Context, accountID, model string, statusCode int, message string) error {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimSpace(model)
	if accountID == "" || model == "" {
		return nil
	}
	now := time.Now()
	status := "healthy"
	successInc := 0
	failureInc := 0
	rateInc := 0
	overloadInc := 0
	resetFailures := true
	var coolingUntil *time.Time
	if statusCode < 200 || statusCode >= 300 {
		status = "unhealthy"
		failureInc = 1
		resetFailures = false
		if statusCode == claudeapipool.StatusTooManyRequests {
			status = "rate_limited"
			rateInc = 1
			if cooldown, _, ok := claudeapipool.CooldownForScopedStatus("claude-acc-pool", statusCode, 0, nil); ok {
				t := now.Add(cooldown)
				coolingUntil = &t
			}
		} else if statusCode == claudeapipool.StatusOverloaded {
			status = "overloaded"
			overloadInc = 1
			if cooldown, _, ok := claudeapipool.CooldownForScopedStatus("claude-acc-pool", statusCode, 0, nil); ok {
				t := now.Add(cooldown)
				coolingUntil = &t
			}
		}
	} else {
		successInc = 1
	}
	coolingText := ""
	if coolingUntil != nil {
		coolingText = dbTime(*coolingUntil)
	}
	if resetFailures {
		_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_account_model_status(account_id, model, status, success_count, failure_count, rate_limit_count,
	overload_count, consecutive_failures, cooling_until, last_status_code, last_error, last_test_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, 0, NULLIF(?, ''), ?, '', ?, ?)
ON CONFLICT(account_id, model) DO UPDATE SET
  status = excluded.status,
  success_count = success_count + excluded.success_count,
  failure_count = failure_count + excluded.failure_count,
  rate_limit_count = rate_limit_count + excluded.rate_limit_count,
  overload_count = overload_count + excluded.overload_count,
  consecutive_failures = 0,
  cooling_until = excluded.cooling_until,
  last_status_code = excluded.last_status_code,
  last_error = '',
  last_test_at = excluded.last_test_at,
  updated_at = excluded.updated_at
`, accountID, model, status, successInc, failureInc, rateInc, overloadInc, coolingText, statusCode, dbTime(now), dbTime(now))
		if err != nil {
			return fmt.Errorf("mark claude code account model result: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_account_model_status(account_id, model, status, success_count, failure_count, rate_limit_count,
	overload_count, consecutive_failures, cooling_until, last_status_code, last_error, last_test_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, 1, NULLIF(?, ''), ?, ?, ?, ?)
ON CONFLICT(account_id, model) DO UPDATE SET
  status = excluded.status,
  success_count = success_count + excluded.success_count,
  failure_count = failure_count + excluded.failure_count,
  rate_limit_count = rate_limit_count + excluded.rate_limit_count,
  overload_count = overload_count + excluded.overload_count,
  consecutive_failures = consecutive_failures + 1,
  cooling_until = COALESCE(excluded.cooling_until, cooling_until),
  last_status_code = excluded.last_status_code,
  last_error = excluded.last_error,
  last_test_at = excluded.last_test_at,
  updated_at = excluded.updated_at
`, accountID, model, status, successInc, failureInc, rateInc, overloadInc, coolingText, statusCode, strings.TrimSpace(message), dbTime(now), dbTime(now))
	if err != nil {
		return fmt.Errorf("mark claude code account model result: %w", err)
	}
	return nil
}

// RecordAccountRuntimeError stores an account-scoped runtime failure without
// attributing it to one model.
func (s *Store) RecordAccountRuntimeError(ctx context.Context, accountID, message string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE claude_code_accounts SET last_error = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(message), dbTime(time.Now()), accountID)
	if err != nil {
		return fmt.Errorf("record claude code account runtime error: %w", err)
	}
	return nil
}

// RecordRoutingEvent stores one route decision/result event.
func (s *Store) RecordRoutingEvent(ctx context.Context, event RoutingEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_routing_events(pool_id, api_key_id, request_id, account_id, auth_id, model, requested_model, proxy_resource_id,
	sticky, session_key, capacity_used, capacity_limit, in_flight, concurrency_limit, rpm_used, rpm_limit,
	attempt, switch_count, wait_ms, affinity_mode, primary_hit, backup_lane, decision, reason, status_code, error, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, normalizeAccountPoolID(event.PoolID), strings.TrimSpace(event.APIKeyID), strings.TrimSpace(event.RequestID), strings.TrimSpace(event.AccountID), strings.TrimSpace(event.AuthID),
		strings.TrimSpace(event.Model), strings.TrimSpace(event.RequestedModel), strings.TrimSpace(event.ProxyResourceID),
		boolInt(event.Sticky), strings.TrimSpace(event.SessionKey), event.CapacityUsed, event.CapacityLimit,
		event.InFlight, event.Concurrency, event.RPMUsed, event.RPMLimit, event.Attempt, event.SwitchCount, event.WaitMS,
		strings.TrimSpace(event.AffinityMode), boolInt(event.PrimaryHit), boolInt(event.BackupLane),
		strings.TrimSpace(event.Decision), strings.TrimSpace(event.Reason), event.StatusCode, strings.TrimSpace(event.Error), dbTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("record claude code routing event: %w", err)
	}
	return nil
}

// ListRoutingEvents returns recent route events.
func (s *Store) ListRoutingEvents(ctx context.Context, limit int) ([]RoutingEvent, error) {
	return s.ListRoutingEventsByPool(ctx, "", limit)
}

// ListRoutingEventsByPool returns recent route events, optionally scoped to one pool.
func (s *Store) ListRoutingEventsByPool(ctx context.Context, poolID string, limit int) ([]RoutingEvent, error) {
	return s.ListRoutingEventsQuery(ctx, UsageQuery{AllTime: true, PoolID: poolID, Limit: limit})
}

// ListRoutingEventsQuery returns route events with the same scope and window semantics as usage queries.
func (s *Store) ListRoutingEventsQuery(ctx context.Context, query UsageQuery) ([]RoutingEvent, error) {
	query = normalizeUsageQuery(query)
	where, args := usageQueryWhere(query)
	args = append(args, query.Limit, query.Offset)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, pool_id, api_key_id, request_id, account_id, auth_id, model, requested_model, proxy_resource_id, sticky, session_key,
       capacity_used, capacity_limit, in_flight, concurrency_limit, rpm_used, rpm_limit,
       attempt, switch_count, wait_ms, affinity_mode, primary_hit, backup_lane, decision, reason, status_code, error, created_at
FROM claude_code_routing_events
`+where+`
ORDER BY id DESC
LIMIT ? OFFSET ?
`, args...)
	if err != nil {
		return nil, fmt.Errorf("list claude code routing events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]RoutingEvent, 0)
	for rows.Next() {
		event, err := scanRoutingEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code routing events: %w", err)
	}
	return out, nil
}

// CountRoutingEventsQuery returns the total number of route events matching a query.
func (s *Store) CountRoutingEventsQuery(ctx context.Context, query UsageQuery) (int64, error) {
	query = normalizeUsageQuery(query)
	where, args := usageQueryWhere(query)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_code_routing_events `+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count claude code routing events: %w", err)
	}
	return total, nil
}

// ListRecentRoutingErrors returns recent local or upstream routing failures.
func (s *Store) ListRecentRoutingErrors(ctx context.Context, limit int) ([]RoutingEvent, error) {
	return s.ListRecentRoutingErrorsByPool(ctx, "", limit)
}

// ListRecentRoutingErrorsByPool returns recent failures, optionally scoped to one pool.
func (s *Store) ListRecentRoutingErrorsByPool(ctx context.Context, poolID string, limit int) ([]RoutingEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	poolID = strings.TrimSpace(poolID)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, pool_id, api_key_id, request_id, account_id, auth_id, model, requested_model, proxy_resource_id, sticky, session_key,
       capacity_used, capacity_limit, in_flight, concurrency_limit, rpm_used, rpm_limit,
       attempt, switch_count, wait_ms, affinity_mode, primary_hit, backup_lane, decision, reason, status_code, error, created_at
FROM claude_code_routing_events
WHERE (? = '' OR pool_id = ?)
  AND (decision IN ('rejected', 'upstream_error') OR status_code >= 400 OR TRIM(error) <> '')
ORDER BY id DESC
LIMIT ?
`, poolID, poolID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent claude code routing errors: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]RoutingEvent, 0)
	for rows.Next() {
		event, err := scanRoutingEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent claude code routing errors: %w", err)
	}
	return out, nil
}

// CountLocalRoutingRejects counts recent account-pool local rejections.
func (s *Store) CountLocalRoutingRejects(ctx context.Context, window time.Duration) (int64, error) {
	return s.CountLocalRoutingRejectsByPool(ctx, window, "")
}

// CountLocalRoutingRejectsByPool counts local rejections in one optional pool.
func (s *Store) CountLocalRoutingRejectsByPool(ctx context.Context, window time.Duration, poolID string) (int64, error) {
	return s.CountLocalRoutingRejectsQuery(ctx, window, false, poolID)
}

// CountLocalRoutingRejectsQuery counts scoped local rejections for a time range.
func (s *Store) CountLocalRoutingRejectsQuery(ctx context.Context, window time.Duration, allTime bool, poolID string) (int64, error) {
	if window <= 0 {
		window = defaultUsageWindow
	}
	poolID = strings.TrimSpace(poolID)
	var count int64
	query := `
SELECT COUNT(*)
FROM claude_code_routing_events
	WHERE decision = 'rejected' AND (? = '' OR pool_id = ?)`
	args := []interface{}{poolID, poolID}
	if !allTime {
		query += ` AND created_at >= ?`
		args = append(args, dbTime(time.Now().Add(-window)))
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count claude code local routing rejects: %w", err)
	}
	return count, nil
}

// RecordUsageLedger stores a lightweight request accounting row.
func (s *Store) RecordUsageLedger(ctx context.Context, entry UsageLedgerEntry) error {
	if s == nil || s.db == nil {
		return nil
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if strings.TrimSpace(entry.PricingStatus) == "" || entry.PriceVersionID <= 0 {
		if err := s.ApplyUsagePricing(ctx, &entry); err != nil {
			entry.PricingStatus = "unpriced"
		}
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_usage_ledger(pool_id, api_key_id, request_id, api_key_preview, account_id, auth_id, model, requested_model,
	status_code, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens,
	raw_input_tokens, raw_total_tokens, price_version_id, price_model_pattern, pricing_status, estimated_cost, success, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, normalizeAccountPoolID(entry.PoolID), strings.TrimSpace(entry.APIKeyID), strings.TrimSpace(entry.RequestID), strings.TrimSpace(entry.APIKeyPreview), strings.TrimSpace(entry.AccountID), strings.TrimSpace(entry.AuthID),
		strings.TrimSpace(entry.Model), strings.TrimSpace(entry.RequestedModel), entry.StatusCode, nonNegativeInt64(entry.InputTokens),
		nonNegativeInt64(entry.OutputTokens), nonNegativeInt64(entry.CacheReadTokens), nonNegativeInt64(entry.CacheCreationTokens),
		nonNegativeInt64(entry.CacheCreation5m), nonNegativeInt64(entry.CacheCreation1h), nonNegativeInt64(entry.RawInputTokens),
		nonNegativeInt64(entry.RawTotalTokens), entry.PriceVersionID, strings.TrimSpace(entry.PriceModelPattern), normalizePricingStatus(entry.PricingStatus),
		entry.EstimatedCost, boolInt(entry.Success), dbTime(entry.CreatedAt))
	if err != nil {
		return fmt.Errorf("record claude code usage ledger: %w", err)
	}
	return nil
}

// UsageSummary returns recent account/model usage aggregates.
func (s *Store) UsageSummary(ctx context.Context, window time.Duration, limit int) (UsageSummary, error) {
	return s.UsageSummaryQuery(ctx, UsageQuery{Window: window, Limit: limit})
}

// UsageSummaryQuery returns scoped request and upstream-attempt aggregates.
func (s *Store) UsageSummaryQuery(ctx context.Context, query UsageQuery) (UsageSummary, error) {
	query = normalizeUsageQuery(query)
	where, args := usageQueryWhere(query)
	summary := UsageSummary{}
	if !query.AllTime {
		summary.WindowSeconds = int64(query.Window.Seconds())
	}
	row := s.db.QueryRowContext(ctx, `
WITH filtered AS (
    SELECT *, CASE WHEN TRIM(request_id) <> '' THEN request_id ELSE 'ledger:' || id END AS request_key
    FROM claude_code_usage_ledger
    `+where+`
), request_rollup AS (
    SELECT request_key, MAX(success) AS success,
           MAX(CASE WHEN pricing_status = 'unpriced' THEN 1 ELSE 0 END) AS unpriced
    FROM filtered
    GROUP BY request_key
), request_stats AS (
    SELECT COUNT(*) AS request_count, COALESCE(SUM(success), 0) AS success_count,
           COALESCE(SUM(unpriced), 0) AS unpriced_request_count
    FROM request_rollup
), attempt_stats AS (
    SELECT COUNT(*) AS attempt_count,
           COALESCE(SUM(input_tokens), 0) AS input_tokens,
           COALESCE(SUM(output_tokens), 0) AS output_tokens,
           COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(cache_creation_5m_tokens), 0) AS cache_creation_5m_tokens,
           COALESCE(SUM(cache_creation_1h_tokens), 0) AS cache_creation_1h_tokens,
           COALESCE(SUM(CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END), 0) AS raw_input_tokens,
           COALESCE(SUM(CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END), 0) AS raw_total_tokens,
           COALESCE(SUM(estimated_cost), 0) AS estimated_cost
    FROM filtered
)
SELECT request_stats.request_count, attempt_stats.attempt_count, request_stats.success_count,
       request_stats.request_count - request_stats.success_count,
       attempt_stats.input_tokens, attempt_stats.output_tokens, attempt_stats.cache_read_tokens,
       attempt_stats.cache_creation_tokens, attempt_stats.cache_creation_5m_tokens, attempt_stats.cache_creation_1h_tokens,
       attempt_stats.raw_input_tokens, attempt_stats.raw_total_tokens, attempt_stats.estimated_cost,
       request_stats.unpriced_request_count
FROM request_stats CROSS JOIN attempt_stats
	`, args...)
	if err := row.Scan(&summary.RequestCount, &summary.AttemptCount, &summary.SuccessCount, &summary.FailureCount,
		&summary.InputTokens, &summary.OutputTokens, &summary.CacheReadTokens, &summary.CacheCreationTokens,
		&summary.CacheCreation5m, &summary.CacheCreation1h, &summary.RawInputTokens, &summary.RawTotalTokens,
		&summary.EstimatedCost, &summary.UnpricedRequestCount); err != nil {
		return summary, fmt.Errorf("summarize claude code usage ledger: %w", err)
	}
	summary.SuccessRate = ratioPercent(summary.SuccessCount, summary.RequestCount)
	summary.PricingCoverage = pricingCoverage(summary.RequestCount, summary.UnpricedRequestCount)
	var err error
	for column, target := range map[string]*[]UsageSummaryItem{
		"pool_id":         &summary.ByPool,
		"account_id":      &summary.ByAccount,
		"api_key_id":      &summary.ByAPIKey,
		"model":           &summary.ByModel,
		"requested_model": &summary.ByRequestedModel,
	} {
		*target, err = s.usageSummaryItemsQuery(ctx, query, column)
		if err != nil {
			return summary, err
		}
	}
	summary.Recent, err = s.listRecentUsageQuery(ctx, query)
	if err != nil {
		return summary, err
	}
	return summary, nil
}

// AccountAvailability summarizes the latest per-minute success history for one account.
func (s *Store) AccountAvailability(ctx context.Context, accountID string, window time.Duration) (*AccountAvailabilitySummary, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if window <= 0 {
		window = defaultAvailabilityWindow
	}
	minutes := int(window / time.Minute)
	if minutes <= 0 {
		minutes = int(defaultAvailabilityWindow / time.Minute)
	}
	if minutes > 24*60 {
		minutes = 24 * 60
	}
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-time.Duration(minutes-1) * time.Minute)
	summary := &AccountAvailabilitySummary{
		WindowMinutes: minutes,
		Status:        "none",
		Buckets:       make([]AccountAvailabilityBucket, minutes),
	}
	bucketByMinute := make(map[string]*AccountAvailabilityBucket, minutes)
	for i := 0; i < minutes; i++ {
		startedAt := start.Add(time.Duration(i) * time.Minute)
		summary.Buckets[i] = AccountAvailabilityBucket{
			StartedAt: startedAt,
			Status:    "none",
		}
		bucketByMinute[startedAt.Format(time.RFC3339)] = &summary.Buckets[i]
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT created_at, success
FROM claude_code_usage_ledger
WHERE account_id = ? AND created_at >= ?
ORDER BY created_at ASC
`, accountID, dbTime(start))
	if err != nil {
		return nil, fmt.Errorf("list account availability ledger: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var createdRaw string
		var success int
		if err := rows.Scan(&createdRaw, &success); err != nil {
			return nil, fmt.Errorf("scan account availability ledger: %w", err)
		}
		createdAt := parseDBTime(createdRaw).UTC().Truncate(time.Minute)
		if createdAt.Before(start) || createdAt.After(end) {
			continue
		}
		bucket := bucketByMinute[createdAt.Format(time.RFC3339)]
		if bucket == nil {
			continue
		}
		bucket.RequestCount++
		if success != 0 {
			bucket.SuccessCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account availability ledger: %w", err)
	}
	for i := range summary.Buckets {
		bucket := &summary.Buckets[i]
		bucket.SuccessRate = ratioPercent(bucket.SuccessCount, bucket.RequestCount)
		bucket.Status = availabilityStatus(bucket.RequestCount, bucket.SuccessCount)
		summary.RequestCount += bucket.RequestCount
		summary.SuccessCount += bucket.SuccessCount
	}
	summary.FailureCount = summary.RequestCount - summary.SuccessCount
	summary.SuccessRate = ratioPercent(summary.SuccessCount, summary.RequestCount)
	summary.Status = availabilityStatus(summary.RequestCount, summary.SuccessCount)
	return summary, nil
}

func (s *Store) usageSummaryItemsQuery(ctx context.Context, query UsageQuery, column string) ([]UsageSummaryItem, error) {
	if !validUsageDimension(column) {
		return nil, fmt.Errorf("invalid usage summary dimension %q", column)
	}
	where, args := usageQueryWhere(query)
	args = append(args, 500)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
WITH filtered AS (
    SELECT *, CASE WHEN TRIM(request_id) <> '' THEN request_id ELSE 'ledger:' || id END AS request_key
    FROM claude_code_usage_ledger
    %s
), request_rollup AS (
    SELECT %s AS key, request_key, MAX(success) AS success,
           MAX(CASE WHEN pricing_status = 'unpriced' THEN 1 ELSE 0 END) AS unpriced
    FROM filtered
    WHERE TRIM(%s) <> ''
    GROUP BY %s, request_key
), request_stats AS (
    SELECT key, COUNT(*) AS request_count, COALESCE(SUM(success), 0) AS success_count,
           COALESCE(SUM(unpriced), 0) AS unpriced_request_count
    FROM request_rollup
    GROUP BY key
), attempt_stats AS (
    SELECT %s AS key, COUNT(*) AS attempt_count,
           COALESCE(SUM(input_tokens), 0) AS input_tokens,
           COALESCE(SUM(output_tokens), 0) AS output_tokens,
           COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
           COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
           COALESCE(SUM(cache_creation_5m_tokens), 0) AS cache_creation_5m_tokens,
           COALESCE(SUM(cache_creation_1h_tokens), 0) AS cache_creation_1h_tokens,
           COALESCE(SUM(CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END), 0) AS raw_input_tokens,
           COALESCE(SUM(CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END), 0) AS raw_total_tokens,
           COALESCE(SUM(estimated_cost), 0) AS estimated_cost
    FROM filtered
    WHERE TRIM(%s) <> ''
    GROUP BY %s
)
SELECT request_stats.key, request_stats.request_count, attempt_stats.attempt_count,
       request_stats.success_count, request_stats.request_count - request_stats.success_count,
       attempt_stats.input_tokens, attempt_stats.output_tokens, attempt_stats.cache_read_tokens,
       attempt_stats.cache_creation_tokens, attempt_stats.cache_creation_5m_tokens, attempt_stats.cache_creation_1h_tokens,
       attempt_stats.raw_input_tokens, attempt_stats.raw_total_tokens, attempt_stats.estimated_cost,
       request_stats.unpriced_request_count
FROM request_stats JOIN attempt_stats ON attempt_stats.key = request_stats.key
ORDER BY request_stats.request_count DESC, request_stats.key ASC
LIMIT ?
	`, where, column, column, column, column, column, column), args...)
	if err != nil {
		return nil, fmt.Errorf("summarize claude code usage by %s: %w", column, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]UsageSummaryItem, 0)
	for rows.Next() {
		var item UsageSummaryItem
		if err := rows.Scan(&item.Key, &item.RequestCount, &item.AttemptCount, &item.SuccessCount, &item.FailureCount,
			&item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheCreationTokens,
			&item.CacheCreation5m, &item.CacheCreation1h, &item.RawInputTokens, &item.RawTotalTokens,
			&item.EstimatedCost, &item.UnpricedRequestCount); err != nil {
			return nil, fmt.Errorf("scan claude code usage by %s: %w", column, err)
		}
		item.SuccessRate = ratioPercent(item.SuccessCount, item.RequestCount)
		item.PricingCoverage = pricingCoverage(item.RequestCount, item.UnpricedRequestCount)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code usage by %s: %w", column, err)
	}
	return out, nil
}

func (s *Store) listRecentUsageQuery(ctx context.Context, query UsageQuery) ([]UsageLedgerEntry, error) {
	where, args := usageQueryWhere(query)
	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, pool_id, api_key_id, request_id, api_key_preview, account_id, auth_id, model, requested_model, status_code,
       input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens,
       CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END,
       CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END,
       price_version_id, price_model_pattern, pricing_status, estimated_cost, success, created_at
FROM claude_code_usage_ledger
`+where+`
ORDER BY id DESC
LIMIT ?
`, args...)
	if err != nil {
		return nil, fmt.Errorf("list recent claude code usage: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]UsageLedgerEntry, 0)
	for rows.Next() {
		entry, err := scanUsageLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent claude code usage: %w", err)
	}
	return out, nil
}

func normalizeUsageQuery(query UsageQuery) UsageQuery {
	if !query.AllTime && query.Window <= 0 {
		query.Window = defaultUsageWindow
	}
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	query.PoolID = strings.TrimSpace(query.PoolID)
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.APIKeyID = strings.TrimSpace(query.APIKeyID)
	query.Model = strings.TrimSpace(query.Model)
	return query
}

func usageQueryWhere(query UsageQuery) (string, []interface{}) {
	clauses := make([]string, 0, 5)
	args := make([]interface{}, 0, 5)
	if !query.AllTime {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, dbTime(time.Now().Add(-query.Window)))
	}
	for _, filter := range []struct{ column, value string }{
		{"pool_id", query.PoolID},
		{"account_id", query.AccountID},
		{"api_key_id", query.APIKeyID},
	} {
		if filter.value != "" {
			clauses = append(clauses, filter.column+" = ?")
			args = append(args, filter.value)
		}
	}
	if query.Model != "" {
		clauses = append(clauses, "(lower(model) = lower(?) OR lower(requested_model) = lower(?))")
		args = append(args, query.Model, query.Model)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func validUsageDimension(column string) bool {
	switch column {
	case "pool_id", "account_id", "api_key_id", "model", "requested_model":
		return true
	default:
		return false
	}
}

func pricingCoverage(requestCount, unpricedCount int64) float64 {
	if requestCount <= 0 {
		return 100
	}
	priced := requestCount - unpricedCount
	if priced < 0 {
		priced = 0
	}
	return ratioPercent(priced, requestCount)
}

func normalizePricingStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "priced", "estimated":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unpriced"
	}
}

func scanUsageCalibration(rows interface {
	Scan(dest ...interface{}) error
}) (UsageCalibration, error) {
	var calibration UsageCalibration
	var checkedRaw sql.NullString
	var createdRaw, updatedRaw string
	if err := rows.Scan(
		&calibration.Model,
		&calibration.ProfileFingerprint,
		&calibration.OverheadTokens,
		&calibration.Status,
		&checkedRaw,
		&calibration.LastError,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return calibration, err
	}
	calibration.Status = normalizeUsageCalibrationStatus(calibration.Status)
	calibration.CheckedAt = parseNullTime(checkedRaw)
	calibration.CreatedAt = parseDBTime(createdRaw)
	calibration.UpdatedAt = parseDBTime(updatedRaw)
	return calibration, nil
}

func scanAccountModelStatus(rows interface {
	Scan(dest ...interface{}) error
}) (AccountModelStatus, error) {
	var status AccountModelStatus
	var coolingRaw, testRaw sql.NullString
	var updatedRaw string
	if err := rows.Scan(
		&status.AccountID,
		&status.Model,
		&status.Status,
		&status.SuccessCount,
		&status.FailureCount,
		&status.RateLimitCount,
		&status.OverloadCount,
		&status.ConsecutiveFailures,
		&coolingRaw,
		&status.LastStatusCode,
		&status.LastError,
		&testRaw,
		&updatedRaw,
	); err != nil {
		return status, fmt.Errorf("scan claude code account model status: %w", err)
	}
	status.CoolingUntil = parseNullTime(coolingRaw)
	status.LastTestAt = parseNullTime(testRaw)
	status.UpdatedAt = parseDBTime(updatedRaw)
	return status, nil
}

func scanRoutingEvent(rows interface {
	Scan(dest ...interface{}) error
}) (RoutingEvent, error) {
	var event RoutingEvent
	var sticky, primaryHit, backupLane int
	var createdRaw string
	if err := rows.Scan(
		&event.ID,
		&event.PoolID,
		&event.APIKeyID,
		&event.RequestID,
		&event.AccountID,
		&event.AuthID,
		&event.Model,
		&event.RequestedModel,
		&event.ProxyResourceID,
		&sticky,
		&event.SessionKey,
		&event.CapacityUsed,
		&event.CapacityLimit,
		&event.InFlight,
		&event.Concurrency,
		&event.RPMUsed,
		&event.RPMLimit,
		&event.Attempt,
		&event.SwitchCount,
		&event.WaitMS,
		&event.AffinityMode,
		&primaryHit,
		&backupLane,
		&event.Decision,
		&event.Reason,
		&event.StatusCode,
		&event.Error,
		&createdRaw,
	); err != nil {
		return event, fmt.Errorf("scan claude code routing event: %w", err)
	}
	event.Sticky = sticky != 0
	event.PrimaryHit = primaryHit != 0
	event.BackupLane = backupLane != 0
	event.CreatedAt = parseDBTime(createdRaw)
	return event, nil
}

func scanUsageLedgerEntry(rows interface {
	Scan(dest ...interface{}) error
}) (UsageLedgerEntry, error) {
	var entry UsageLedgerEntry
	var success int
	var createdRaw string
	if err := rows.Scan(
		&entry.ID,
		&entry.PoolID,
		&entry.APIKeyID,
		&entry.RequestID,
		&entry.APIKeyPreview,
		&entry.AccountID,
		&entry.AuthID,
		&entry.Model,
		&entry.RequestedModel,
		&entry.StatusCode,
		&entry.InputTokens,
		&entry.OutputTokens,
		&entry.CacheReadTokens,
		&entry.CacheCreationTokens,
		&entry.CacheCreation5m,
		&entry.CacheCreation1h,
		&entry.RawInputTokens,
		&entry.RawTotalTokens,
		&entry.PriceVersionID,
		&entry.PriceModelPattern,
		&entry.PricingStatus,
		&entry.EstimatedCost,
		&success,
		&createdRaw,
	); err != nil {
		return entry, fmt.Errorf("scan claude code usage ledger: %w", err)
	}
	entry.Success = success != 0
	entry.CreatedAt = parseDBTime(createdRaw)
	return entry, nil
}

func ratioPercent(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func availabilityStatus(requestCount, successCount int64) string {
	if requestCount <= 0 {
		return "none"
	}
	rate := ratioPercent(successCount, requestCount)
	if rate >= 90 {
		return "healthy"
	}
	if rate >= 10 {
		return "degraded"
	}
	return "unhealthy"
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func statusCodeFromAccountResult(success bool, message string) int {
	if success {
		return http.StatusOK
	}
	if strings.Contains(message, "429") || strings.Contains(strings.ToLower(message), "rate_limit") {
		return claudeapipool.StatusTooManyRequests
	}
	if strings.Contains(message, "529") || strings.Contains(strings.ToLower(message), "overload") {
		return claudeapipool.StatusOverloaded
	}
	return http.StatusBadGateway
}

func (s *Store) hydrateAccountRuntime(ctx context.Context, account *ClaudeCodeAccount) {
	if s == nil || account == nil {
		return
	}
	if capacity, err := s.GetAccountCapacity(ctx, account.ID); err == nil {
		account.Capacity = capacity
		routingScope := AccountRoutingScope(account.PoolID)
		status := claudeapipool.AggregateScopedRouteStatusWithPolicy(routingScope, account.AuthID, claudeapipool.EffectiveRoutingConfig{
			PerAccountRPM:            capacity.BaseRPM,
			PerAccountConcurrency:    capacity.ConcurrencyLimit,
			StickyConcurrencyReserve: capacity.StickyConcurrencyReserve,
			MaxSessions:              capacity.MaxSessions,
		})
		reserveUsed := 0
		if status.InFlight > int64(capacity.ConcurrencyLimit) {
			reserveUsed = int(status.InFlight) - capacity.ConcurrencyLimit
		}
		capacityLimit := capacity.ConcurrencyLimit
		capacityUsed := int(status.InFlight)
		runtime := &AccountRuntimeCapacity{
			AccountID:                account.ID,
			BaseRPM:                  capacity.BaseRPM,
			ConcurrencyLimit:         capacity.ConcurrencyLimit,
			MaxSessions:              capacity.MaxSessions,
			StickyConcurrencyReserve: capacity.StickyConcurrencyReserve,
			CapacityUsed:             capacityUsed,
			CapacityLimit:            capacityLimit,
			InFlight:                 status.InFlight,
			RPMUsed:                  status.RPMUsed,
			RPMLimit:                 status.RPMLimit,
			ReserveUsed:              reserveUsed,
			ActiveSessions:           status.ActiveSessions,
			Waiters:                  status.Waiters,
			Cooling:                  status.Cooling,
			AccountCooling:           status.AccountCooling,
			ModelCoolingCount:        status.ModelCoolingCount,
			Unavailable:              status.Unavailable,
		}
		if !status.CoolingTo.IsZero() {
			runtime.CoolingUntil = status.CoolingTo.Format(time.RFC3339Nano)
		}
		account.RuntimeCapacity = runtime
	}
	if statuses, err := s.ListAccountModelStatuses(ctx, account.ID); err == nil {
		account.ModelStatuses = statuses
	}
	if availability, err := s.AccountAvailability(ctx, account.ID, defaultAvailabilityWindow); err == nil {
		account.Availability = availability
	}
	account.AffinityBindings = claudeapipool.ScopedAccountBindingCount(AccountRoutingScope(account.PoolID), account.AuthID)
	account.applyDerivedHealth(time.Now())
	if account.RuntimeCapacity != nil && account.RuntimeCapacity.AccountCooling {
		account.EffectiveSchedulable = false
	}
	applyAccountQuotaRouting(account, time.Now())
}
