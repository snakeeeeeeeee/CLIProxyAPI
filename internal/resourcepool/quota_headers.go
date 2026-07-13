package resourcepool

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const unifiedRateLimitPrefix = "anthropic-ratelimit-unified-"

// MergeAccountRateLimitHeaders merges dynamic Anthropic quota windows without replacing usage-endpoint data.
func (s *Store) MergeAccountRateLimitHeaders(ctx context.Context, accountID string, headers http.Header) (*ClaudeCodeAccount, error) {
	if len(headers) == 0 {
		return s.GetAccount(ctx, accountID)
	}
	now := accountQuotaNow()
	parsed := parseUnifiedRateLimitWindows(headers, now)
	if len(parsed) == 0 {
		return s.GetAccount(ctx, accountID)
	}
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]QuotaWindow)
	if account.Quota != nil {
		for _, window := range normalizeQuotaWindows(account.Quota.Windows) {
			byKey[window.Key] = window
		}
	}
	for _, incoming := range normalizeQuotaWindows(parsed) {
		byKey[incoming.Key] = mergeQuotaWindow(byKey[incoming.Key], incoming)
	}
	windows := make([]QuotaWindow, 0, len(byKey))
	for _, window := range byKey {
		windows = append(windows, window)
	}
	rawJSON := ""
	if account.Quota != nil {
		rawJSON = account.Quota.RawJSON
	}
	saved, err := s.SaveAccountQuota(ctx, AccountQuota{
		AccountID: account.ID,
		Status:    "ok",
		Windows:   windows,
		CheckedAt: &now,
		RawJSON:   rawJSON,
		Source:    "response_headers",
		Probe:     accountQuotaProbe(account.Quota),
	})
	if err != nil {
		return nil, err
	}
	interval, errInterval := s.accountQuotaInterval(ctx)
	if errInterval != nil {
		interval = quotaDefaultInterval
	}
	next := nextAccountHealthCheck(account.ID, now, interval)
	_, _ = s.db.ExecContext(ctx, `UPDATE claude_code_accounts SET next_health_check_at = ? WHERE id = ?`, dbTime(next), account.ID)
	return saved, nil
}

func accountQuotaProbe(quota *AccountQuota) *AccountQuotaProbe {
	if quota == nil || quota.Probe == nil {
		return nil
	}
	copy := *quota.Probe
	return &copy
}

func parseUnifiedRateLimitWindows(headers http.Header, now time.Time) []QuotaWindow {
	type values struct {
		utilization *float64
		reset       *time.Time
		status      string
		remaining   *float64
	}
	byWindow := make(map[string]*values)
	for name, rawValues := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if !strings.HasPrefix(lower, unifiedRateLimitPrefix) || len(rawValues) == 0 {
			continue
		}
		remainder := strings.TrimPrefix(lower, unifiedRateLimitPrefix)
		windowKey, field := splitUnifiedWindowField(remainder)
		if windowKey == "" || field == "" {
			continue
		}
		entry := byWindow[windowKey]
		if entry == nil {
			entry = &values{}
			byWindow[windowKey] = entry
		}
		value := strings.TrimSpace(rawValues[0])
		switch field {
		case "utilization":
			if number, err := strconv.ParseFloat(value, 64); err == nil {
				percent := clampPercent(number * 100)
				entry.utilization = &percent
			}
		case "reset":
			if parsed := parseRateLimitReset(value); parsed != nil {
				entry.reset = parsed
			}
		case "status":
			entry.status = value
		case "remaining":
			if number, err := strconv.ParseFloat(value, 64); err == nil {
				entry.remaining = &number
			}
		case "surpassed-threshold":
			if strings.EqualFold(value, "true") || value == "1" || value == "1.0" {
				percent := 100.0
				entry.utilization = &percent
			}
		}
	}
	claim := strings.TrimSpace(headers.Get("anthropic-ratelimit-unified-representative-claim"))
	claimKey := canonicalQuotaClaimKey(claim)
	aggregateReset := parseRateLimitReset(headers.Get("anthropic-ratelimit-unified-reset"))
	out := make([]QuotaWindow, 0, len(byWindow))
	for key, entry := range byWindow {
		if entry == nil || (entry.utilization == nil && entry.reset == nil && entry.status == "" && entry.remaining == nil) {
			continue
		}
		used := 0.0
		if entry.utilization != nil {
			used = *entry.utilization
		}
		updated := now
		canonicalKey := normalizeHeaderWindowKey(key)
		reset := entry.reset
		if reset == nil && canonicalKey == claimKey {
			reset = aggregateReset
		}
		out = append(out, QuotaWindow{
			Key:                 canonicalKey,
			Name:                quotaWindowDisplayName(key),
			UsedPercent:         used,
			RemainPercent:       clampPercent(100 - used),
			UtilizationKnown:    boolPtr(entry.utilization != nil),
			ResetsAt:            reset,
			Status:              strings.ToLower(strings.TrimSpace(entry.status)),
			Remaining:           entry.remaining,
			RepresentativeClaim: claim,
			Source:              "response_headers",
			UpdatedAt:           &updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func splitUnifiedWindowField(value string) (string, string) {
	for _, field := range []string{"surpassed-threshold", "representative-claim", "utilization", "remaining", "status", "reset"} {
		suffix := "-" + field
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(value, suffix)), field
		}
	}
	return "", ""
}

func parseRateLimitReset(value string) *time.Time {
	if unix, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && unix > 0 {
		if unix > 1e11 {
			unix /= 1000
		}
		parsed := time.Unix(unix, 0)
		return &parsed
	}
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
		return &parsed
	}
	return nil
}

func normalizeHeaderWindowKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "5h":
		return "five_hour"
	case "7d":
		return "seven_day"
	case "7d_oi":
		return "seven_day_fable"
	case "overage":
		return "extra_usage"
	default:
		return "model_" + key
	}
}

func quotaWindowDisplayName(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "5h":
		return "5 小时"
	case "7d":
		return "7 天"
	case "7d_oi":
		return "Fable 周额度"
	case "overage":
		return "额外用量"
	default:
		return strings.ToUpper(strings.TrimSpace(key)) + " 窗口"
	}
}

func canonicalQuotaClaimKey(claim string) string {
	switch strings.ToLower(strings.TrimSpace(claim)) {
	case "five_hour":
		return "five_hour"
	case "seven_day":
		return "seven_day"
	case "seven_day_sonnet":
		return "seven_day_sonnet"
	case "seven_day_opus":
		return "seven_day_opus"
	case "seven_day_overage_included", "seven_day_fable":
		return "seven_day_fable"
	default:
		return ""
	}
}

func canonicalQuotaWindowKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "5h", "five_hour":
		return "five_hour"
	case "7d", "seven_day":
		return "seven_day"
	case "seven_day_sonnet":
		return "seven_day_sonnet"
	case "seven_day_opus":
		return "seven_day_opus"
	case "7d_oi", "model_7d_oi", "seven_day_overage_included", "seven_day_fable":
		return "seven_day_fable"
	case "overage", "model_overage", "extra_usage":
		return "extra_usage"
	default:
		return key
	}
}

func quotaWindowName(key, fallback string) string {
	switch canonicalQuotaWindowKey(key) {
	case "five_hour":
		return "5 小时"
	case "seven_day":
		return "7 天"
	case "seven_day_sonnet":
		return "Sonnet 周额度"
	case "seven_day_opus":
		return "Opus 周额度"
	case "seven_day_fable":
		return "Fable 周额度"
	case "extra_usage":
		return "额外用量"
	default:
		return strings.TrimSpace(fallback)
	}
}

func normalizeQuotaWindows(windows []QuotaWindow) []QuotaWindow {
	byKey := make(map[string]QuotaWindow, len(windows))
	for _, window := range windows {
		window.Key = canonicalQuotaWindowKey(window.Key)
		if window.Key == "" {
			continue
		}
		window.Name = quotaWindowName(window.Key, window.Name)
		window.UsedPercent = clampPercent(window.UsedPercent)
		window.RemainPercent = clampPercent(100 - window.UsedPercent)
		if window.UtilizationKnown == nil {
			window.UtilizationKnown = boolPtr(inferLegacyQuotaUtilizationKnown(window))
		}
		window.Status = strings.ToLower(strings.TrimSpace(window.Status))
		current, exists := byKey[window.Key]
		if !exists || quotaWindowNewer(window, current) {
			byKey[window.Key] = mergeQuotaWindow(current, window)
		} else {
			byKey[window.Key] = mergeQuotaWindow(window, current)
		}
	}
	out := make([]QuotaWindow, 0, len(byKey))
	for _, window := range byKey {
		out = append(out, window)
	}
	sort.Slice(out, func(i, j int) bool {
		return quotaWindowSortKey(out[i].Key) < quotaWindowSortKey(out[j].Key)
	})
	return out
}

func quotaWindowNewer(a, b QuotaWindow) bool {
	if a.UpdatedAt == nil {
		return b.UpdatedAt == nil
	}
	return b.UpdatedAt == nil || a.UpdatedAt.After(*b.UpdatedAt)
}

func mergeQuotaWindow(current, incoming QuotaWindow) QuotaWindow {
	if current.Key == "" {
		return incoming
	}
	incoming.Key = canonicalQuotaWindowKey(incoming.Key)
	if !quotaWindowUtilizationKnown(incoming) && quotaWindowUtilizationKnown(current) && !quotaWindowSignalsExhausted(incoming) {
		incoming.UsedPercent = current.UsedPercent
		incoming.RemainPercent = current.RemainPercent
		incoming.UtilizationKnown = current.UtilizationKnown
		incoming.UpdatedAt = current.UpdatedAt
		incoming.Source = current.Source
	}
	if incoming.Name == "" {
		incoming.Name = current.Name
	}
	if incoming.ResetsAt == nil {
		incoming.ResetsAt = current.ResetsAt
	}
	if incoming.UtilizationKnown == nil {
		incoming.UtilizationKnown = current.UtilizationKnown
	}
	if incoming.Status == "" {
		incoming.Status = current.Status
	}
	if incoming.Remaining == nil {
		incoming.Remaining = current.Remaining
	}
	if incoming.RepresentativeClaim == "" {
		incoming.RepresentativeClaim = current.RepresentativeClaim
	}
	if incoming.Source == "" {
		incoming.Source = current.Source
	}
	if incoming.UpdatedAt == nil {
		incoming.UpdatedAt = current.UpdatedAt
	}
	return incoming
}

func quotaWindowSignalsExhausted(window QuotaWindow) bool {
	status := strings.ToLower(strings.TrimSpace(window.Status))
	return status == "rejected" || status == "exhausted" || (window.Remaining != nil && *window.Remaining <= 0)
}

func quotaWindowUtilizationKnown(window QuotaWindow) bool {
	if window.UtilizationKnown != nil {
		return *window.UtilizationKnown
	}
	return inferLegacyQuotaUtilizationKnown(window)
}

func inferLegacyQuotaUtilizationKnown(window QuotaWindow) bool {
	if strings.EqualFold(strings.TrimSpace(window.Source), "oauth_usage") {
		return true
	}
	return window.UsedPercent > 0 || (window.RemainPercent > 0 && window.RemainPercent < 100)
}

func mergeOAuthUsageWindows(existing *AccountQuota, active []QuotaWindow, now time.Time) []QuotaWindow {
	active = normalizeQuotaWindows(active)
	hasFable := false
	for _, window := range active {
		if window.Key == "seven_day_fable" {
			hasFable = true
			break
		}
	}
	if hasFable || existing == nil {
		return active
	}
	for _, window := range normalizeQuotaWindows(existing.Windows) {
		if window.Key != "seven_day_fable" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(window.Source), "response_headers") {
			continue
		}
		if window.UpdatedAt == nil || now.Sub(*window.UpdatedAt) > quotaFreshnessTTL {
			continue
		}
		if window.ResetsAt != nil && !window.ResetsAt.After(now) {
			continue
		}
		active = append(active, window)
		break
	}
	return normalizeQuotaWindows(active)
}

func quotaWindowSortKey(key string) string {
	switch canonicalQuotaWindowKey(key) {
	case "five_hour":
		return "01"
	case "seven_day":
		return "02"
	case "seven_day_sonnet":
		return "03"
	case "seven_day_opus":
		return "04"
	case "seven_day_fable":
		return "05"
	case "extra_usage":
		return "99"
	default:
		return "50:" + key
	}
}
