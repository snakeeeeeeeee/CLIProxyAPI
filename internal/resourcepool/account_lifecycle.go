package resourcepool

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
)

const (
	quotaFreshnessTTL      = 15 * time.Minute
	quotaDegradedThreshold = 85.0

	quotaConfidenceExact    = "exact"
	quotaConfidenceShared   = "shared"
	quotaConfidenceObserved = "observed"
	quotaConfidenceUnknown  = "unknown"

	quotaFreshnessFresh   = "fresh"
	quotaFreshnessStale   = "stale"
	quotaFreshnessUnknown = "unknown"
)

func nextAccountHealthCheck(accountID string, now time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(accountID) + now.UTC().Format("200601021504")))
	fraction := float64(binary.BigEndian.Uint32(sum[:4])) / float64(^uint32(0))
	jitter := (fraction*0.4 - 0.2) * float64(interval)
	return now.Add(interval + time.Duration(jitter))
}

// AccountHealthUpdate is the single persistent lifecycle transition used by probes and runtime signals.
type AccountHealthUpdate struct {
	Source              string
	Status              string
	Reason              string
	BlockedUntil        *time.Time
	CheckedAt           *time.Time
	NextCheckAt         *time.Time
	AllowManualRecovery bool
}

// UpdateAccountHealth applies one lifecycle transition without allowing ordinary success to clear manual recovery.
func (s *Store) UpdateAccountHealth(ctx context.Context, accountID string, update AccountHealthUpdate) (*ClaudeCodeAccount, error) {
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	status := normalizeAccountHealthStatus(update.Status)
	if account.HealthStatus == AccountHealthManualRecovery && status == AccountHealthHealthy && !update.AllowManualRecovery {
		return account, nil
	}
	checkedAt := update.CheckedAt
	if checkedAt == nil {
		now := time.Now()
		checkedAt = &now
	}
	var blockedUntil any
	if update.BlockedUntil != nil && !update.BlockedUntil.IsZero() {
		blockedUntil = dbTime(*update.BlockedUntil)
	}
	var nextCheck any
	if update.NextCheckAt != nil && !update.NextCheckAt.IsZero() {
		nextCheck = dbTime(*update.NextCheckAt)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_accounts
SET health_status = ?, blocked_until = ?, blocked_reason = ?, last_health_check_at = ?, next_health_check_at = ?, updated_at = ?
WHERE id = ?
`, status, blockedUntil, strings.TrimSpace(update.Reason), dbTime(*checkedAt), nextCheck, dbTime(time.Now()), account.ID); err != nil {
		return nil, fmt.Errorf("update claude code account health: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO pool_events(type, message, data_json, created_at) VALUES(?, ?, json_object('account_id', ?, 'source', ?, 'from', ?, 'to', ?, 'reason', ?), ?)`,
		"account.health", "claude code account health changed", account.ID, strings.TrimSpace(update.Source), account.HealthStatus, status, strings.TrimSpace(update.Reason), dbTime(time.Now()))
	updated, err := s.GetAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	ApplyAccountLifecycleRouting(updated)
	return updated, nil
}

// ApplyAccountLifecycleRouting projects persistent lifecycle state into the account-pool-only router.
func ApplyAccountLifecycleRouting(account *ClaudeCodeAccount) {
	if account == nil || strings.TrimSpace(account.AuthID) == "" {
		return
	}
	scope := AccountRoutingScope(account.PoolID)
	now := time.Now()
	switch account.HealthStatus {
	case AccountHealthChecking, AccountHealthManualRecovery:
		claudeapipool.BlockScopedAccount(scope, account.AuthID, 0, true)
	case AccountHealthTemporarilyBlocked:
		if account.BlockedUntil != nil && account.BlockedUntil.After(now) {
			claudeapipool.BlockScopedAccount(scope, account.AuthID, time.Until(*account.BlockedUntil), false)
		} else {
			claudeapipool.ClearScopedAccountBlock(scope, account.AuthID)
		}
	default:
		claudeapipool.ClearScopedAccountBlock(scope, account.AuthID)
	}
}

func (account *ClaudeCodeAccount) applyDerivedHealth(now time.Time) {
	if account == nil {
		return
	}
	account.Schedulable = account.Enabled
	account.Enabled = account.Schedulable
	healthAllowsRouting := account.HealthStatus == AccountHealthHealthy
	if account.HealthStatus == AccountHealthTemporarilyBlocked && (account.BlockedUntil == nil || !account.BlockedUntil.After(now)) {
		healthAllowsRouting = true
	}
	proxyAllowsRouting := account.Proxy == nil || (account.Proxy.Enabled && account.Proxy.HealthStatus != HealthUnhealthy)
	account.EffectiveSchedulable = account.Schedulable && account.HasAuthData && healthAllowsRouting && proxyAllowsRouting
	account.QuotaFreshness = "unknown"
	account.QuotaBand = claudeapipool.QuotaBandUnknown
	account.SharedQuotaBand = claudeapipool.QuotaBandUnknown
	account.QuotaWindow = ""
	account.QuotaResetAt = nil
	account.QuotaWindowStates = buildQuotaWindowStates(account.Quota, now)
	if account.Quota != nil && account.Quota.CheckedAt != nil {
		account.QuotaSource = quotaSource(account.Quota)
		overall := evaluateQuotaRouting(account.Quota.Windows, account.Quota.CheckedAt, "*", now)
		shared := evaluateQuotaRouting(account.Quota.Windows, account.Quota.CheckedAt, "", now)
		if overall.Known {
			account.QuotaFreshness = "fresh"
			headroom := overall.Headroom
			account.Headroom = &headroom
			account.QuotaBand = overall.Band
			account.QuotaWindow = overall.Window
			if !overall.ResetAt.IsZero() {
				resetAt := overall.ResetAt
				account.QuotaResetAt = &resetAt
			}
		} else {
			account.QuotaFreshness = "stale"
		}
		if shared.Known {
			account.SharedQuotaBand = shared.Band
			if shared.Band == claudeapipool.QuotaBandExhausted {
				account.EffectiveSchedulable = false
			}
		}
	}
}

func buildQuotaWindowStates(quota *AccountQuota, now time.Time) []QuotaWindowState {
	type windowSpec struct {
		key       string
		name      string
		useShared bool
	}
	specs := []windowSpec{
		{key: "five_hour", name: "5 小时"},
		{key: "seven_day", name: "7 天"},
		{key: "seven_day_sonnet", name: "Sonnet 周额度", useShared: true},
		{key: "seven_day_opus", name: "Opus 周额度", useShared: true},
		{key: "seven_day_fable", name: "Fable 周额度", useShared: true},
	}

	byKey := make(map[string]QuotaWindow)
	var checkedAt *time.Time
	defaultSource := ""
	if quota != nil {
		checkedAt = quota.CheckedAt
		defaultSource = quotaSource(quota)
		for _, window := range normalizeQuotaWindows(quota.Windows) {
			byKey[canonicalQuotaWindowKey(window.Key)] = window
		}
	}
	shared, hasShared := byKey["seven_day"]
	states := make([]QuotaWindowState, 0, len(specs))
	for _, spec := range specs {
		window, found := byKey[spec.key]
		sharedFrom := ""
		confidence := quotaConfidenceUnknown
		if found {
			if quotaWindowUtilizationKnown(window) {
				confidence = quotaConfidenceExact
			} else {
				confidence = quotaConfidenceObserved
			}
		} else if spec.useShared && hasShared {
			window = shared
			found = true
			sharedFrom = "seven_day"
			confidence = quotaConfidenceShared
		}

		state := QuotaWindowState{
			Key:        spec.key,
			Name:       spec.name,
			Confidence: confidence,
			Freshness:  quotaFreshnessUnknown,
			SharedFrom: sharedFrom,
		}
		if !found {
			states = append(states, state)
			continue
		}

		state.Source = strings.TrimSpace(window.Source)
		if state.Source == "" {
			state.Source = defaultSource
		}
		state.ObservedAt = window.UpdatedAt
		if state.ObservedAt == nil {
			state.ObservedAt = checkedAt
		}
		state.Freshness = quotaWindowFreshness(window, state.ObservedAt, now)
		state.UtilizationKnown = quotaWindowUtilizationKnown(window)
		if state.UtilizationKnown {
			used := clampPercent(window.UsedPercent)
			remain := clampPercent(window.RemainPercent)
			state.UsedPercent = &used
			state.RemainPercent = &remain
		}
		state.ResetsAt = window.ResetsAt
		state.Status = strings.ToLower(strings.TrimSpace(window.Status))
		state.Remaining = window.Remaining
		state.Exhausted = quotaWindowSignalsExhausted(window) || (state.UtilizationKnown && window.UsedPercent >= 100)
		states = append(states, state)
	}
	return states
}

func quotaWindowFreshness(window QuotaWindow, observedAt *time.Time, now time.Time) string {
	if observedAt == nil || observedAt.IsZero() {
		return quotaFreshnessUnknown
	}
	if window.ResetsAt != nil && !window.ResetsAt.After(now) {
		return quotaFreshnessStale
	}
	if now.Sub(*observedAt) > quotaFreshnessTTL {
		return quotaFreshnessStale
	}
	return quotaFreshnessFresh
}

func quotaSource(quota *AccountQuota) string {
	if quota == nil {
		return ""
	}
	if strings.TrimSpace(quota.Source) != "" {
		return strings.TrimSpace(quota.Source)
	}
	sources := make(map[string]struct{})
	for _, window := range quota.Windows {
		if source := strings.TrimSpace(window.Source); source != "" {
			sources[source] = struct{}{}
		}
	}
	if len(sources) > 1 {
		return "mixed"
	}
	for source := range sources {
		return source
	}
	return "oauth_usage"
}

func quotaHeadroom(windows []QuotaWindow, model string) float64 {
	maxUsed := 0.0
	for _, window := range windows {
		if quotaWindowApplies(window.Key, model, model == "") && window.UsedPercent > maxUsed {
			maxUsed = window.UsedPercent
		}
	}
	return clampPercent(100-maxUsed) / 100
}

// AccountHeadroom returns fresh model-aware headroom. Unknown or stale snapshots remain neutral.
func (account *ClaudeCodeAccount) AccountHeadroom(model string, now time.Time) (float64, bool) {
	if account == nil || account.Quota == nil || account.Quota.CheckedAt == nil {
		return 0.5, false
	}
	evaluation := evaluateQuotaRouting(account.Quota.Windows, account.Quota.CheckedAt, model, now)
	if !evaluation.Known {
		return 0.5, false
	}
	return evaluation.Headroom, true
}

func applyAccountQuotaRouting(account *ClaudeCodeAccount, now time.Time) {
	if account == nil || strings.TrimSpace(account.AuthID) == "" {
		return
	}
	states := make(map[string]claudeapipool.AccountQuotaRoutingState)
	if account.Quota != nil && account.Quota.CheckedAt != nil {
		for _, family := range []string{"", "sonnet", "opus", "fable", "haiku"} {
			evaluation := evaluateQuotaRouting(account.Quota.Windows, account.Quota.CheckedAt, family, now)
			if !evaluation.Known {
				continue
			}
			states[family] = claudeapipool.AccountQuotaRoutingState{
				Headroom:    evaluation.Headroom,
				UsedPercent: evaluation.UsedPercent,
				Band:        evaluation.Band,
				Window:      evaluation.Window,
				ResetAt:     evaluation.ResetAt,
				ExpiresAt:   evaluation.ExpiresAt,
			}
		}
	}
	claudeapipool.UpdateScopedAccountQuotaRouting(AccountRoutingScope(account.PoolID), account.AuthID, states)
}

type quotaRoutingEvaluation struct {
	Known       bool
	Headroom    float64
	UsedPercent float64
	Band        string
	Window      string
	ResetAt     time.Time
	ExpiresAt   time.Time
}

func evaluateQuotaRouting(windows []QuotaWindow, checkedAt *time.Time, model string, now time.Time) quotaRoutingEvaluation {
	out := quotaRoutingEvaluation{Headroom: 0.5, Band: claudeapipool.QuotaBandUnknown}
	includeAllModels := model == "*"
	for _, window := range normalizeQuotaWindows(windows) {
		if !quotaWindowApplies(window.Key, model, includeAllModels) {
			continue
		}
		if window.ResetsAt != nil && !window.ResetsAt.After(now) {
			continue
		}
		updatedAt := window.UpdatedAt
		if updatedAt == nil {
			updatedAt = checkedAt
		}
		if updatedAt == nil || now.Sub(*updatedAt) > quotaFreshnessTTL {
			continue
		}
		expiresAt := updatedAt.Add(quotaFreshnessTTL)
		used := clampPercent(window.UsedPercent)
		band := quotaBandForWindow(window, used)
		if !quotaWindowUtilizationKnown(window) {
			if band != claudeapipool.QuotaBandExhausted {
				continue
			}
			used = 100
		}
		if !out.Known || quotaBandSeverity(band) > quotaBandSeverity(out.Band) || (quotaBandSeverity(band) == quotaBandSeverity(out.Band) && used > out.UsedPercent) {
			out.Known = true
			out.UsedPercent = used
			out.Headroom = clampPercent(100-used) / 100
			out.Band = band
			out.Window = canonicalQuotaWindowKey(window.Key)
			out.ResetAt = time.Time{}
			if window.ResetsAt != nil {
				out.ResetAt = *window.ResetsAt
			}
		}
		if out.ExpiresAt.IsZero() || expiresAt.Before(out.ExpiresAt) {
			out.ExpiresAt = expiresAt
		}
		if band == claudeapipool.QuotaBandExhausted && window.ResetsAt != nil && window.ResetsAt.After(out.ResetAt) {
			out.ResetAt = *window.ResetsAt
		}
	}
	return out
}

func quotaWindowApplies(key, model string, includeAllModels bool) bool {
	key = canonicalQuotaWindowKey(key)
	if key == "extra_usage" || key == "" {
		return false
	}
	if key == "five_hour" || key == "seven_day" {
		return true
	}
	if includeAllModels {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(model, "fable"):
		return key == "seven_day_fable" || strings.Contains(key, "fable") || strings.Contains(key, "7d_oi")
	case strings.Contains(model, "sonnet"):
		return key == "seven_day_sonnet" || strings.Contains(key, "sonnet")
	case strings.Contains(model, "opus"):
		return key == "seven_day_opus" || strings.Contains(key, "opus")
	case strings.Contains(model, "haiku"):
		return strings.Contains(key, "haiku")
	default:
		return false
	}
}

func quotaBandForWindow(window QuotaWindow, used float64) string {
	status := strings.ToLower(strings.TrimSpace(window.Status))
	if status == "rejected" || status == "exhausted" || used >= 100 || (window.Remaining != nil && *window.Remaining <= 0) {
		return claudeapipool.QuotaBandExhausted
	}
	if used >= quotaDegradedThreshold {
		return claudeapipool.QuotaBandDegraded
	}
	return claudeapipool.QuotaBandNormal
}

func quotaBandSeverity(band string) int {
	switch band {
	case claudeapipool.QuotaBandExhausted:
		return 3
	case claudeapipool.QuotaBandDrainOnly, claudeapipool.QuotaBandDegraded:
		return 1
	default:
		return 0
	}
}

func (s *Store) applyRuntimeHealthResult(ctx context.Context, account *ClaudeCodeAccount, statusCode int, success bool, message string) {
	if s == nil || account == nil {
		return
	}
	now := time.Now()
	if success {
		interval, errInterval := s.accountQuotaInterval(ctx)
		if errInterval != nil {
			interval = quotaDefaultInterval
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE claude_code_accounts SET consecutive_failures = 0 WHERE id = ?`, account.ID)
		_, _ = s.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{
			Source:      "inference",
			Status:      AccountHealthHealthy,
			CheckedAt:   &now,
			NextCheckAt: timePtr(nextAccountHealthCheck(account.ID, now, interval)),
		})
		return
	}
	if statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity || statusCode == http.StatusTooManyRequests || statusCode == claudeapipool.StatusOverloaded {
		return
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusPaymentRequired || statusCode == http.StatusForbidden {
		status, reason, blockedUntil := classifyQuotaProbeFailure(statusCode, message, now)
		_, _ = s.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{Source: "inference", Status: status, Reason: reason, BlockedUntil: blockedUntil, CheckedAt: &now, NextCheckAt: blockedUntil})
		return
	}
	if statusCode == 0 || statusCode >= 500 {
		failures := account.ConsecutiveFailures + 1
		_, _ = s.db.ExecContext(ctx, `UPDATE claude_code_accounts SET consecutive_failures = ?, last_error = ?, updated_at = ? WHERE id = ?`, failures, strings.TrimSpace(message), dbTime(now), account.ID)
		if failures >= 3 {
			until := now.Add(2 * time.Minute)
			_, _ = s.UpdateAccountHealth(ctx, account.ID, AccountHealthUpdate{Source: "inference", Status: AccountHealthTemporarilyBlocked, Reason: "连续网络或代理错误", BlockedUntil: &until, CheckedAt: &now, NextCheckAt: &until})
		}
	}
}
