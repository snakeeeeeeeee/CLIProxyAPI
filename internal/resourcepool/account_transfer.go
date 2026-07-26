package resourcepool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Sub2APIDataType    = "sub2api-data"
	Sub2APIDataVersion = 1
)

// Sub2APIDataPayload is the account backup contract shared with sub2api.
type Sub2APIDataPayload struct {
	Type       string               `json:"type,omitempty"`
	Version    int                  `json:"version,omitempty"`
	ExportedAt string               `json:"exported_at"`
	Proxies    []Sub2APIDataProxy   `json:"proxies"`
	Accounts   []Sub2APIDataAccount `json:"accounts"`
}

type Sub2APIDataProxy struct {
	ProxyKey string `json:"proxy_key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Status   string `json:"status"`
}

type Sub2APIDataAccount struct {
	Name               string         `json:"name"`
	Notes              *string        `json:"notes,omitempty"`
	Platform           string         `json:"platform"`
	Type               string         `json:"type"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra,omitempty"`
	ProxyKey           *string        `json:"proxy_key,omitempty"`
	Concurrency        int            `json:"concurrency"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64         `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired,omitempty"`
}

type Sub2APIImportError struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	ProxyKey string `json:"proxy_key,omitempty"`
	Message  string `json:"message"`
}

type Sub2APIImportResult struct {
	ProxyCreated   int                  `json:"proxy_created"`
	ProxyReused    int                  `json:"proxy_reused"`
	ProxyFailed    int                  `json:"proxy_failed"`
	AccountCreated int                  `json:"account_created"`
	AccountUpdated int                  `json:"account_updated"`
	AccountFailed  int                  `json:"account_failed"`
	Errors         []Sub2APIImportError `json:"errors,omitempty"`
}

type accountTransferRow struct {
	ID               string
	PoolID           string
	AuthID           string
	AuthJSON         string
	Email            string
	Enabled          bool
	Priority         int
	Note             string
	LoginSource      string
	ProxyID          string
	ProxyName        string
	ProxyURL         string
	ProxyEnabled     bool
	ConcurrencyLimit int
}

// ExportSub2APIData exports selected accounts, or every account in a pool when ids is empty.
func (s *Store) ExportSub2APIData(ctx context.Context, poolID string, ids []string, includeProxies bool) (Sub2APIDataPayload, error) {
	payload := Sub2APIDataPayload{
		Type:       Sub2APIDataType,
		Version:    Sub2APIDataVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Proxies:    []Sub2APIDataProxy{},
		Accounts:   []Sub2APIDataAccount{},
	}
	if s == nil || s.db == nil {
		return payload, fmt.Errorf("resource pool store is nil")
	}
	poolID = normalizeAccountPoolID(poolID)
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.pool_id, a.auth_id, a.auth_json, a.email, a.enabled, a.priority, a.note,
       COALESCE(c.login_source, 'unknown'), COALESCE(p.id, ''), COALESCE(p.name, ''),
       COALESCE(p.proxy_url, ''), COALESCE(p.enabled, 0), COALESCE(cap.concurrency_limit, 0)
FROM claude_code_accounts a
LEFT JOIN claude_code_account_credentials c ON c.account_id = a.id
LEFT JOIN proxy_resources p ON p.id = a.proxy_resource_id
LEFT JOIN claude_code_account_capacity cap ON cap.account_id = a.id
WHERE a.pool_id = ?
ORDER BY a.updated_at DESC, a.email ASC
`, poolID)
	if err != nil {
		return payload, fmt.Errorf("list accounts for export: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]accountTransferRow, 0)
	for rows.Next() {
		var item accountTransferRow
		var enabled, proxyEnabled int
		if err := rows.Scan(&item.ID, &item.PoolID, &item.AuthID, &item.AuthJSON, &item.Email, &enabled, &item.Priority, &item.Note,
			&item.LoginSource, &item.ProxyID, &item.ProxyName, &item.ProxyURL, &proxyEnabled, &item.ConcurrencyLimit); err != nil {
			return payload, fmt.Errorf("scan account for export: %w", err)
		}
		if len(selected) > 0 {
			if _, ok := selected[item.ID]; !ok {
				continue
			}
		}
		item.Enabled = enabled != 0
		item.ProxyEnabled = proxyEnabled != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return payload, fmt.Errorf("iterate accounts for export: %w", err)
	}
	if len(selected) > 0 && len(items) != len(selected) {
		return payload, fmt.Errorf("one or more selected accounts do not belong to pool %q", poolID)
	}
	proxyKeys := make(map[string]string)
	for _, item := range items {
		credentials, _, errCredentials := exportSub2APICredentials(item.AuthJSON)
		if errCredentials != nil {
			return payload, fmt.Errorf("export account %q: %w", item.ID, errCredentials)
		}
		var proxyKey *string
		if includeProxies && item.ProxyID != "" {
			key, ok := proxyKeys[item.ProxyID]
			if !ok {
				proxy, errProxy := sub2APIProxyFromURL(item.ProxyName, item.ProxyURL, item.ProxyEnabled)
				if errProxy != nil {
					return payload, fmt.Errorf("export account %q proxy: %w", item.ID, errProxy)
				}
				key = proxy.ProxyKey
				proxyKeys[item.ProxyID] = key
				payload.Proxies = append(payload.Proxies, proxy)
			}
			proxyKey = &key
		}
		name := strings.TrimSpace(item.Email)
		if name == "" {
			name = strings.TrimSuffix(strings.TrimSpace(item.AuthID), ".json")
		}
		var notes *string
		if strings.TrimSpace(item.Note) != "" {
			note := strings.TrimSpace(item.Note)
			notes = &note
		}
		concurrency := item.ConcurrencyLimit
		if concurrency <= 0 {
			concurrency = 1
		}
		payload.Accounts = append(payload.Accounts, Sub2APIDataAccount{
			Name: name, Notes: notes, Platform: "anthropic", Type: "oauth", Credentials: credentials,
			Extra: map[string]any{
				"cliproxy_auth_id":      item.AuthID,
				"cliproxy_login_source": normalizeAccountLoginSource(item.LoginSource),
			},
			ProxyKey: proxyKey, Concurrency: concurrency, Priority: max(0, item.Priority),
		})
	}
	sort.Slice(payload.Proxies, func(i, j int) bool { return payload.Proxies[i].ProxyKey < payload.Proxies[j].ProxyKey })
	return payload, nil
}

// ExportSessionKeys returns retained SessionKeys for selected accounts, or all accounts in one pool.
func (s *Store) ExportSessionKeys(ctx context.Context, poolID string, ids []string) ([]string, int, error) {
	if s == nil || s.db == nil {
		return nil, 0, fmt.Errorf("resource pool store is nil")
	}
	poolID = normalizeAccountPoolID(poolID)
	selected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, COALESCE(c.session_key, '')
FROM claude_code_accounts a
LEFT JOIN claude_code_account_credentials c ON c.account_id = a.id
WHERE a.pool_id = ?
ORDER BY a.created_at ASC
`, poolID)
	if err != nil {
		return nil, 0, fmt.Errorf("list SessionKeys for export: %w", err)
	}
	defer func() { _ = rows.Close() }()
	keys := make([]string, 0)
	matched := 0
	unavailable := 0
	for rows.Next() {
		var id, sessionKey string
		if err := rows.Scan(&id, &sessionKey); err != nil {
			return nil, 0, fmt.Errorf("scan SessionKey for export: %w", err)
		}
		if len(selected) > 0 {
			if _, ok := selected[id]; !ok {
				continue
			}
		}
		matched++
		if sessionKey = strings.TrimSpace(sessionKey); sessionKey != "" {
			keys = append(keys, sessionKey)
		} else {
			unavailable++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate SessionKeys for export: %w", err)
	}
	if len(selected) > 0 && matched != len(selected) {
		return nil, 0, fmt.Errorf("one or more selected accounts do not belong to pool %q", poolID)
	}
	return keys, unavailable, nil
}

func exportSub2APICredentials(authJSON string) (map[string]any, *int64, error) {
	metadata := map[string]any{}
	if strings.TrimSpace(authJSON) == "" {
		return nil, nil, fmt.Errorf("stored OAuth credentials are missing")
	}
	if err := json.Unmarshal([]byte(authJSON), &metadata); err != nil {
		return nil, nil, fmt.Errorf("decode stored OAuth credentials: %w", err)
	}
	accessToken := mapString(metadata, "access_token")
	refreshToken := mapString(metadata, "refresh_token")
	if accessToken == "" || refreshToken == "" {
		return nil, nil, fmt.Errorf("stored OAuth access_token or refresh_token is missing")
	}
	credentials := map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	}
	for _, key := range []string{"id_token", "email", "org_uuid", "account_uuid", "last_refresh"} {
		if value := mapString(metadata, key); value != "" {
			credentials[key] = value
		}
	}
	expiresRaw := mapString(metadata, "expires_at")
	if expiresRaw == "" {
		expiresRaw = mapString(metadata, "expired")
	}
	var expiresAt *int64
	if expiresRaw != "" {
		credentials["expires_at"] = expiresRaw
		credentials["expired"] = expiresRaw
		if parsed, ok := parseTransferExpiry(expiresRaw); ok {
			unix := parsed.Unix()
			expiresAt = &unix
		}
	}
	return credentials, expiresAt, nil
}

func parseTransferExpiry(raw string) (time.Time, bool) {
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
		return parsed, true
	}
	if unix, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		return time.Unix(unix, 0).UTC(), true
	}
	return time.Time{}, false
}

func sub2APIProxyFromURL(name, rawURL string, enabled bool) (Sub2APIDataProxy, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return Sub2APIDataProxy{}, fmt.Errorf("invalid proxy URL")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 || port > 65535 {
		return Sub2APIDataProxy{}, fmt.Errorf("proxy port is invalid")
	}
	protocol := strings.ToLower(parsed.Scheme)
	switch protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return Sub2APIDataProxy{}, fmt.Errorf("proxy protocol %q is unsupported", protocol)
	}
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	status := "inactive"
	if enabled {
		status = "active"
	}
	if strings.TrimSpace(name) == "" {
		name = parsed.Host
	}
	return Sub2APIDataProxy{
		ProxyKey: buildSub2APIProxyKey(protocol, parsed.Hostname(), port, username, password),
		Name:     strings.TrimSpace(name), Protocol: protocol, Host: parsed.Hostname(), Port: port,
		Username: username, Password: password, Status: status,
	}, nil
}

func buildSub2APIProxyKey(protocol, host string, port int, username, password string) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", strings.TrimSpace(protocol), strings.TrimSpace(host), port, strings.TrimSpace(username), strings.TrimSpace(password))
}

func mapString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
