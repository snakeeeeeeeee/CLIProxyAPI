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
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
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
	err := row.Scan(&cfg.AccountID, &cfg.BaseRPM, &cfg.ConcurrencyLimit, &cfg.MaxSessions, &cfg.StickyBuffer, &updatedAt)
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
`, cfg.AccountID, cfg.BaseRPM, cfg.ConcurrencyLimit, cfg.MaxSessions, cfg.StickyBuffer, dbTime(now)); err != nil {
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
	if patch.StickyBuffer != nil {
		next.StickyBuffer = *patch.StickyBuffer
	}
	return s.SaveAccountCapacity(ctx, accountID, next)
}

func (s *Store) defaultAccountCapacity(ctx context.Context, accountID string) AccountCapacityConfig {
	baseRPM := 6
	concurrency := 1
	if doc, err := s.GetConfig(ctx); err == nil && doc != nil {
		effective := EffectiveClaudeCodePool(doc.ClaudeCode)
		if effective.Routing.PerAccountRPM > 0 {
			baseRPM = effective.Routing.PerAccountRPM
		}
		if effective.Routing.PerAccountConcurrency > 0 {
			concurrency = effective.Routing.PerAccountConcurrency
		}
	}
	maxSessions := 0
	stickyBuffer := concurrency + maxSessions
	if floor := baseRPM / 5; floor > stickyBuffer {
		stickyBuffer = floor
	}
	if stickyBuffer < 1 {
		stickyBuffer = 1
	}
	return AccountCapacityConfig{
		AccountID:        accountID,
		BaseRPM:          baseRPM,
		ConcurrencyLimit: concurrency,
		MaxSessions:      maxSessions,
		StickyBuffer:     stickyBuffer,
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
	if cfg.StickyBuffer <= 0 {
		cfg.StickyBuffer = cfg.ConcurrencyLimit + cfg.MaxSessions
		if floor := cfg.BaseRPM / 5; floor > cfg.StickyBuffer {
			cfg.StickyBuffer = floor
		}
	}
	if cfg.StickyBuffer < 1 {
		cfg.StickyBuffer = 1
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
	if cfg.StickyBuffer > 1000 {
		cfg.StickyBuffer = 1000
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

// RecordRoutingEvent stores one route decision/result event.
func (s *Store) RecordRoutingEvent(ctx context.Context, event RoutingEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_routing_events(request_id, account_id, auth_id, model, requested_model, proxy_resource_id,
	sticky, session_key, capacity_used, capacity_limit, decision, reason, status_code, error, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, strings.TrimSpace(event.RequestID), strings.TrimSpace(event.AccountID), strings.TrimSpace(event.AuthID),
		strings.TrimSpace(event.Model), strings.TrimSpace(event.RequestedModel), strings.TrimSpace(event.ProxyResourceID),
		boolInt(event.Sticky), strings.TrimSpace(event.SessionKey), event.CapacityUsed, event.CapacityLimit,
		strings.TrimSpace(event.Decision), strings.TrimSpace(event.Reason), event.StatusCode, strings.TrimSpace(event.Error), dbTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("record claude code routing event: %w", err)
	}
	return nil
}

// ListRoutingEvents returns recent route events.
func (s *Store) ListRoutingEvents(ctx context.Context, limit int) ([]RoutingEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, request_id, account_id, auth_id, model, requested_model, proxy_resource_id, sticky, session_key,
       capacity_used, capacity_limit, decision, reason, status_code, error, created_at
FROM claude_code_routing_events
ORDER BY id DESC
LIMIT ?
`, limit)
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

// ListRecentRoutingErrors returns recent local or upstream routing failures.
func (s *Store) ListRecentRoutingErrors(ctx context.Context, limit int) ([]RoutingEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, request_id, account_id, auth_id, model, requested_model, proxy_resource_id, sticky, session_key,
       capacity_used, capacity_limit, decision, reason, status_code, error, created_at
FROM claude_code_routing_events
WHERE decision IN ('rejected', 'upstream_error') OR status_code >= 400 OR TRIM(error) <> ''
ORDER BY id DESC
LIMIT ?
`, limit)
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
	if window <= 0 {
		window = defaultUsageWindow
	}
	since := dbTime(time.Now().Add(-window))
	var count int64
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM claude_code_routing_events
WHERE created_at >= ? AND decision = 'rejected'
`, since)
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
	_, err := s.db.ExecContext(ctx, `
INSERT INTO claude_code_usage_ledger(request_id, api_key_preview, account_id, auth_id, model, requested_model,
	status_code, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, raw_input_tokens, raw_total_tokens, estimated_cost, success, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, strings.TrimSpace(entry.RequestID), strings.TrimSpace(entry.APIKeyPreview), strings.TrimSpace(entry.AccountID), strings.TrimSpace(entry.AuthID),
		strings.TrimSpace(entry.Model), strings.TrimSpace(entry.RequestedModel), entry.StatusCode, nonNegativeInt64(entry.InputTokens),
		nonNegativeInt64(entry.OutputTokens), nonNegativeInt64(entry.CacheReadTokens), nonNegativeInt64(entry.CacheCreationTokens),
		nonNegativeInt64(entry.RawInputTokens), nonNegativeInt64(entry.RawTotalTokens), entry.EstimatedCost, boolInt(entry.Success), dbTime(entry.CreatedAt))
	if err != nil {
		return fmt.Errorf("record claude code usage ledger: %w", err)
	}
	return nil
}

// UsageSummary returns recent account/model usage aggregates.
func (s *Store) UsageSummary(ctx context.Context, window time.Duration, limit int) (UsageSummary, error) {
	if window <= 0 {
		window = defaultUsageWindow
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	since := dbTime(time.Now().Add(-window))
	summary := UsageSummary{WindowSeconds: int64(window.Seconds())}
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(success), 0), COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0),
       COALESCE(SUM(CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END), 0),
       COALESCE(SUM(CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END), 0),
       COALESCE(SUM(estimated_cost), 0)
FROM claude_code_usage_ledger
WHERE created_at >= ?
`, since)
	if err := row.Scan(&summary.RequestCount, &summary.SuccessCount, &summary.FailureCount, &summary.InputTokens, &summary.OutputTokens, &summary.CacheReadTokens, &summary.CacheCreationTokens, &summary.RawInputTokens, &summary.RawTotalTokens, &summary.EstimatedCost); err != nil {
		return summary, fmt.Errorf("summarize claude code usage ledger: %w", err)
	}
	summary.SuccessRate = ratioPercent(summary.SuccessCount, summary.RequestCount)
	var err error
	summary.ByAccount, err = s.usageSummaryItems(ctx, since, "account_id")
	if err != nil {
		return summary, err
	}
	summary.ByModel, err = s.usageSummaryItems(ctx, since, "model")
	if err != nil {
		return summary, err
	}
	summary.ByRequestedModel, err = s.usageSummaryItems(ctx, since, "requested_model")
	if err != nil {
		return summary, err
	}
	summary.Recent, err = s.listRecentUsage(ctx, limit)
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

func (s *Store) usageSummaryItems(ctx context.Context, since, column string) ([]UsageSummaryItem, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT %s, COUNT(*), COALESCE(SUM(success), 0), COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0),
       COALESCE(SUM(CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END), 0),
       COALESCE(SUM(CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END), 0),
       COALESCE(SUM(estimated_cost), 0)
FROM claude_code_usage_ledger
WHERE created_at >= ? AND TRIM(%s) <> ''
GROUP BY %s
ORDER BY COUNT(*) DESC, %s ASC
LIMIT 20
`, column, column, column, column), since)
	if err != nil {
		return nil, fmt.Errorf("summarize claude code usage by %s: %w", column, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]UsageSummaryItem, 0)
	for rows.Next() {
		var item UsageSummaryItem
		if err := rows.Scan(&item.Key, &item.RequestCount, &item.SuccessCount, &item.FailureCount, &item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheCreationTokens, &item.RawInputTokens, &item.RawTotalTokens, &item.EstimatedCost); err != nil {
			return nil, fmt.Errorf("scan claude code usage by %s: %w", column, err)
		}
		item.SuccessRate = ratioPercent(item.SuccessCount, item.RequestCount)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code usage by %s: %w", column, err)
	}
	return out, nil
}

func (s *Store) listRecentUsage(ctx context.Context, limit int) ([]UsageLedgerEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, request_id, api_key_preview, account_id, auth_id, model, requested_model, status_code,
       input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
       CASE WHEN raw_input_tokens > 0 THEN raw_input_tokens ELSE input_tokens END,
       CASE WHEN raw_total_tokens > 0 THEN raw_total_tokens ELSE input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens END,
       estimated_cost, success, created_at
FROM claude_code_usage_ledger
ORDER BY id DESC
LIMIT ?
`, limit)
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
	var sticky int
	var createdRaw string
	if err := rows.Scan(
		&event.ID,
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
		&event.Decision,
		&event.Reason,
		&event.StatusCode,
		&event.Error,
		&createdRaw,
	); err != nil {
		return event, fmt.Errorf("scan claude code routing event: %w", err)
	}
	event.Sticky = sticky != 0
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
		&entry.RawInputTokens,
		&entry.RawTotalTokens,
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
		status := claudeapipool.AggregateScopedRouteStatus(coreexecutor.PoolScopeClaudeAccountPool, account.AuthID)
		bufferUsed := 0
		if status.RPMUsed > capacity.BaseRPM {
			bufferUsed = status.RPMUsed - capacity.BaseRPM
		}
		capacityLimit := capacity.ConcurrencyLimit
		capacityUsed := int(status.InFlight)
		runtime := &AccountRuntimeCapacity{
			AccountID:        account.ID,
			BaseRPM:          capacity.BaseRPM,
			ConcurrencyLimit: capacity.ConcurrencyLimit,
			MaxSessions:      capacity.MaxSessions,
			StickyBuffer:     capacity.StickyBuffer,
			CapacityUsed:     capacityUsed,
			CapacityLimit:    capacityLimit,
			InFlight:         status.InFlight,
			RPMUsed:          status.RPMUsed,
			RPMLimit:         status.RPMLimit,
			BufferUsed:       bufferUsed,
			Cooling:          status.Cooling,
			Unavailable:      status.Unavailable,
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
}
