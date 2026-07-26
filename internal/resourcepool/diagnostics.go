package resourcepool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

const accountQuotaSchedulerTick = 15 * time.Second

// AccountPoolDiagnostics is a local-only consistency summary for the account-pool runtime.
type AccountPoolDiagnostics struct {
	Status   string                         `json:"status"`
	Issues   []string                       `json:"issues"`
	AsOf     time.Time                      `json:"as_of"`
	Build    AccountPoolBuildDiagnostics    `json:"build"`
	Database AccountPoolDatabaseDiagnostics `json:"database"`
	Profile  AccountPoolProfileDiagnostics  `json:"profile"`
	Quota    AccountPoolQuotaDiagnostics    `json:"quota"`
	Summary  AccountPoolDiagnosticSummary   `json:"summary"`
	Accounts []AccountPoolAccountDiagnostic `json:"accounts"`
}

type AccountPoolBuildDiagnostics struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

type AccountPoolDatabaseDiagnostics struct {
	Path                string `json:"path"`
	InstanceFingerprint string `json:"instance_fingerprint"`
}

type AccountPoolProfileDiagnostics struct {
	Version             string `json:"version"`
	Revision            string `json:"revision"`
	Fingerprint         string `json:"fingerprint"`
	UserAgent           string `json:"user_agent"`
	HeaderCount         int    `json:"header_count"`
	HeaderOrderCount    int    `json:"header_order_count"`
	HeaderConfigHash    string `json:"header_config_hash"`
	TLSProfile          string `json:"tls_profile"`
	TLSJA3              string `json:"tls_ja3"`
	TLSJA4              string `json:"tls_ja4"`
	TLSALPN             string `json:"tls_alpn"`
	AllowClientCacheTTL bool   `json:"allow_client_cache_ttl"`
	Baseline            string `json:"baseline"`
	TimeoutPolicy       string `json:"timeout_policy"`
	CustomOffset        bool   `json:"custom_offset"`
}

type AccountPoolQuotaDiagnostics struct {
	Mode               string     `json:"mode"`
	Interval           string     `json:"interval"`
	Concurrency        int        `json:"concurrency"`
	SchedulerTick      string     `json:"scheduler_tick"`
	LastActiveAt       *time.Time `json:"last_active_at,omitempty"`
	LastActiveCategory string     `json:"last_active_category,omitempty"`
	NextScheduledAt    *time.Time `json:"next_scheduled_at,omitempty"`
}

type AccountPoolDiagnosticSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Attention int `json:"attention"`
	Critical  int `json:"critical"`
}

type AccountPoolAccountDiagnostic struct {
	AccountFingerprint string             `json:"account_fingerprint"`
	DeviceFingerprint  string             `json:"device_fingerprint,omitempty"`
	PoolID             string             `json:"pool_id"`
	Status             string             `json:"status"`
	Issues             []string           `json:"issues"`
	ProxyResourceID    string             `json:"proxy_resource_id,omitempty"`
	LastObservedExitIP string             `json:"last_observed_exit_ip,omitempty"`
	TokenExpiresAt     *time.Time         `json:"token_expires_at,omitempty"`
	LastQuotaAt        *time.Time         `json:"last_quota_at,omitempty"`
	NextQuotaAt        *time.Time         `json:"next_quota_at,omitempty"`
	QuotaTransport     string             `json:"quota_transport,omitempty"`
	NetworkMode        string             `json:"network_mode"`
	Probe              *AccountQuotaProbe `json:"probe,omitempty"`
}

// Diagnostics reads only local configuration and SQLite state. It never probes an external service.
func (s *Store) Diagnostics(ctx context.Context, cfg *config.Config, now time.Time) (*AccountPoolDiagnostics, error) {
	_ = cfg
	if now.IsZero() {
		now = time.Now()
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	instanceFingerprint, err := s.DatabaseInstanceFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	profile := EffectiveClaudeCodeProfile(doc.Profile)
	profileFingerprint := ClaudeCodeProfileFingerprint(profile)
	builtinProfileFingerprint := ClaudeCodeProfileFingerprint(EffectiveClaudeCodeProfile(ClaudeCodeProfile{}))
	profileCustomOffset := profileFingerprint != builtinProfileFingerprint
	poolConfig := EffectiveClaudeCodePool(doc.ClaudeCode)
	quotaConfig := EffectiveAccountQuota(doc.AccountQuota)
	recentExitChanges := s.recentProxyExitChanges(ctx, now.Add(-24*time.Hour))
	diagnostics := &AccountPoolDiagnostics{
		Status: "healthy",
		AsOf:   now,
		Build: AccountPoolBuildDiagnostics{
			Version:   strings.TrimSpace(buildinfo.Version),
			Commit:    strings.TrimSpace(buildinfo.Commit),
			BuildDate: strings.TrimSpace(buildinfo.BuildDate),
			GoVersion: runtime.Version(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
		},
		Database: AccountPoolDatabaseDiagnostics{
			Path:                s.Path(),
			InstanceFingerprint: instanceFingerprint,
		},
		Profile: AccountPoolProfileDiagnostics{
			Version:             profile.Version,
			Revision:            profile.Revision,
			Fingerprint:         profileFingerprint,
			UserAgent:           profile.UserAgent,
			HeaderCount:         len(profile.Headers),
			HeaderOrderCount:    len(profile.HeaderOrder),
			HeaderConfigHash:    accountPoolHeaderConfigHash(profile),
			TLSProfile:          profile.TLSProfile,
			TLSJA3:              profile.TLSJA3,
			TLSJA4:              profile.TLSJA4,
			TLSALPN:             profile.TLSALPN,
			AllowClientCacheTTL: poolConfig.AllowClientCacheTTL,
			Baseline:            "builtin-trace-baseline:2.1.220",
			TimeoutPolicy:       "stream=600,non-stream=300,count_tokens=300,passthrough=preserve-valid",
			CustomOffset:        profileCustomOffset,
		},
		Quota: AccountPoolQuotaDiagnostics{
			Mode:          quotaConfig.Mode,
			Interval:      quotaConfig.Interval,
			Concurrency:   quotaConfig.Concurrency,
			SchedulerTick: accountQuotaSchedulerTick.String(),
		},
		Accounts: make([]AccountPoolAccountDiagnostic, 0, len(accounts)),
	}
	if diagnostics.Build.Commit == "" || strings.EqualFold(diagnostics.Build.Commit, "none") {
		diagnostics.Issues = append(diagnostics.Issues, "build_commit_unknown")
	}
	if profileCustomOffset {
		diagnostics.Issues = append(diagnostics.Issues, "profile_revision_custom")
	}
	if profile.TLSProfile != helps.ClaudeCodeNodeTLSProfileName || profile.TLSALPN != helps.ClaudeCodeNodeTLSALPN {
		diagnostics.Issues = append(diagnostics.Issues, "profile_transport_mismatch")
	}
	if len(accounts) == 0 {
		diagnostics.Issues = append(diagnostics.Issues, "no_accounts")
	}
	for _, account := range accounts {
		item := accountDiagnostic(account, profile.Revision, recentExitChanges, now)
		if item.Probe != nil && (diagnostics.Quota.LastActiveAt == nil || item.Probe.RequestedAt.After(*diagnostics.Quota.LastActiveAt)) {
			requestedAt := item.Probe.RequestedAt
			diagnostics.Quota.LastActiveAt = &requestedAt
			diagnostics.Quota.LastActiveCategory = "oauth-usage"
		}
		if quotaConfig.Mode == AccountQuotaModeScheduled && item.NextQuotaAt != nil &&
			(diagnostics.Quota.NextScheduledAt == nil || item.NextQuotaAt.Before(*diagnostics.Quota.NextScheduledAt)) {
			nextAt := *item.NextQuotaAt
			diagnostics.Quota.NextScheduledAt = &nextAt
		}
		diagnostics.Accounts = append(diagnostics.Accounts, item)
		switch item.Status {
		case "critical":
			diagnostics.Summary.Critical++
		case "attention":
			diagnostics.Summary.Attention++
		default:
			diagnostics.Summary.Healthy++
		}
	}
	diagnostics.Summary.Total = len(diagnostics.Accounts)
	if diagnostics.Summary.Critical > 0 {
		diagnostics.Issues = append(diagnostics.Issues, "accounts_critical")
	} else if diagnostics.Summary.Attention > 0 {
		diagnostics.Issues = append(diagnostics.Issues, "accounts_attention")
	}
	diagnostics.Issues = normalizedDiagnosticIssues(diagnostics.Issues)
	if len(diagnostics.Issues) > 0 {
		diagnostics.Status = "attention"
	}
	if containsDiagnosticIssue(diagnostics.Issues, "profile_transport_mismatch") {
		diagnostics.Status = "critical"
	}
	return diagnostics, nil
}

func accountDiagnostic(account ClaudeCodeAccount, profileRevision string, recentExitChanges map[string]bool, now time.Time) AccountPoolAccountDiagnostic {
	item := AccountPoolAccountDiagnostic{
		AccountFingerprint: shortDiagnosticHash(account.ID),
		DeviceFingerprint:  cloakDeviceFingerprint(account.CloakUserID),
		PoolID:             normalizeAccountPoolID(account.PoolID),
		Status:             "healthy",
		ProxyResourceID:    strings.TrimSpace(account.ProxyResourceID),
		TokenExpiresAt:     account.TokenExpiresAt,
		NextQuotaAt:        account.NextHealthCheckAt,
		NetworkMode:        "direct",
	}
	if account.ProxyResourceID != "" {
		item.NetworkMode = "invalid"
	}
	if account.Proxy != nil {
		item.LastObservedExitIP = strings.TrimSpace(account.Proxy.ExitIP)
		setting, err := proxyutil.Parse(account.Proxy.ProxyURL)
		if err == nil && setting.Mode == proxyutil.ModeProxy && account.Proxy.Enabled {
			item.NetworkMode = "bound_proxy"
		}
	}
	if account.Quota != nil {
		item.LastQuotaAt = account.Quota.CheckedAt
		item.Probe = accountQuotaProbe(account.Quota)
		if item.Probe != nil {
			item.QuotaTransport = strings.SplitN(item.Probe.TransportProfile, "/", 2)[0]
		}
	}
	critical := false
	attention := false
	addCritical := func(code string) {
		critical = true
		item.Issues = append(item.Issues, code)
	}
	addAttention := func(code string) {
		attention = true
		item.Issues = append(item.Issues, code)
	}
	if !account.HasAuthData {
		addCritical("auth_missing")
	}
	if account.TokenExpiresAt != nil {
		if !account.TokenExpiresAt.After(now) {
			addCritical("token_expired")
		} else if account.TokenExpiresAt.Before(now.Add(15 * time.Minute)) {
			addAttention("token_expiring")
		}
	}
	switch account.HealthStatus {
	case AccountHealthManualRecovery:
		addCritical("manual_recovery")
	case AccountHealthTemporarilyBlocked:
		addAttention("temporarily_blocked")
	}
	if account.ProxyResourceID != "" && account.Proxy == nil {
		addCritical("proxy_binding_missing")
	} else if account.Proxy != nil && account.Proxy.HealthStatus == HealthUnhealthy {
		addCritical("proxy_unhealthy")
	}
	if recentExitChanges[strings.TrimSpace(account.ProxyResourceID)] {
		addAttention("proxy_exit_changed")
	}
	if account.Quota == nil || account.Quota.CheckedAt == nil {
		addAttention("quota_never_observed")
	} else if account.QuotaFreshness == quotaFreshnessStale {
		addAttention("quota_stale")
	}
	if account.NextHealthCheckAt != nil && account.NextHealthCheckAt.Before(now.Add(-2*accountQuotaSchedulerTick)) && account.HealthStatus != AccountHealthManualRecovery {
		addAttention("quota_schedule_overdue")
	}
	if item.Probe != nil {
		if item.Probe.ProfileRevision != "" && item.Probe.ProfileRevision != profileRevision {
			addAttention("quota_profile_mismatch")
		}
		if item.Probe.ProxyMode == "invalid" {
			addCritical("quota_proxy_invalid")
		}
		if item.Probe.StatusCode >= 400 || (account.Quota != nil && account.Quota.Status == "error" && item.Probe.StatusCode == 0) {
			addAttention("quota_probe_failed")
		}
	}
	item.Issues = normalizedDiagnosticIssues(item.Issues)
	if critical {
		item.Status = "critical"
	} else if attention {
		item.Status = "attention"
	}
	return item
}

func (s *Store) recentProxyExitChanges(ctx context.Context, since time.Time) map[string]bool {
	out := make(map[string]bool)
	if s == nil || s.db == nil {
		return out
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data_json FROM pool_events WHERE type = 'proxy_exit_changed' AND created_at >= ?`, dbTime(since))
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var data map[string]string
		if json.Unmarshal([]byte(raw), &data) == nil {
			if proxyID := strings.TrimSpace(data["proxy_id"]); proxyID != "" {
				out[proxyID] = true
			}
		}
	}
	return out
}

func accountPoolHeaderConfigHash(profile EffectiveClaudeCodeProfileConfig) string {
	payload := struct {
		Headers     map[string]string `json:"headers"`
		HeaderOrder []string          `json:"header_order"`
	}{Headers: profile.Headers, HeaderOrder: profile.HeaderOrder}
	raw, _ := json.Marshal(payload)
	return shortDiagnosticHash(string(raw))
}

func cloakDeviceFingerprint(cloakUserID string) string {
	value := strings.TrimPrefix(strings.TrimSpace(cloakUserID), "user_")
	index := strings.Index(value, "_account_")
	if index <= 0 {
		return ""
	}
	return shortDiagnosticHash(value[:index])
}

func shortDiagnosticHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func diagnosticProxyMode(raw string) string {
	setting, err := proxyutil.Parse(raw)
	if err != nil || setting.Mode == proxyutil.ModeInvalid {
		return "invalid"
	}
	switch setting.Mode {
	case proxyutil.ModeProxy:
		return "proxy"
	case proxyutil.ModeDirect:
		return "direct"
	default:
		return "inherit"
	}
}

func normalizedDiagnosticIssues(issues []string) []string {
	seen := make(map[string]bool, len(issues))
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" || seen[issue] {
			continue
		}
		seen[issue] = true
		out = append(out, issue)
	}
	sort.Strings(out)
	return out
}

func containsDiagnosticIssue(issues []string, target string) bool {
	for _, issue := range issues {
		if issue == target {
			return true
		}
	}
	return false
}
