package resourcepool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

const accountPoolLogFileName = "account-pool.log"

var accountPoolLogLevels = map[string]int{
	"debug": 10,
	"info":  20,
	"warn":  30,
	"error": 40,
}

// AccountPoolLogEntry is one JSONL diagnostic line.
type AccountPoolLogEntry struct {
	Time            time.Time `json:"ts"`
	Level           string    `json:"level"`
	Event           string    `json:"event"`
	RequestID       string    `json:"request_id,omitempty"`
	Path            string    `json:"path,omitempty"`
	Model           string    `json:"model,omitempty"`
	RequestedModel  string    `json:"requested_model,omitempty"`
	AccountID       string    `json:"account_id,omitempty"`
	AuthID          string    `json:"auth_id,omitempty"`
	ProxyResourceID string    `json:"proxy_resource_id,omitempty"`
	Sticky          bool      `json:"sticky,omitempty"`
	SessionKey      string    `json:"session_key,omitempty"`
	InFlight        int64     `json:"in_flight,omitempty"`
	Concurrency     int       `json:"concurrency_limit,omitempty"`
	RPMUsed         int       `json:"rpm_used,omitempty"`
	RPMLimit        int       `json:"rpm_limit,omitempty"`
	Decision        string    `json:"decision,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	LatencyMS       int64     `json:"latency_ms,omitempty"`
	InputTokens     int64     `json:"input_tokens,omitempty"`
	OutputTokens    int64     `json:"output_tokens,omitempty"`
	CacheReadTokens int64     `json:"cache_read_tokens,omitempty"`
	CacheCreate     int64     `json:"cache_creation_tokens,omitempty"`
	TotalTokens     int64     `json:"total_tokens,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// AccountPoolLogView is returned to management clients.
type AccountPoolLogView struct {
	Line  string               `json:"line"`
	Entry *AccountPoolLogEntry `json:"entry,omitempty"`
}

func resolveAccountPoolLogConfig(ctx context.Context, configFilePath string, cfg *config.Config) (EffectiveAccountPoolLogConfig, string, error) {
	if cfg == nil || !cfg.ResourcePools.Enabled {
		return EffectiveAccountPoolLog(defaultConfigFile().ClaudeCode.Log), "", nil
	}
	store, err := Open(configFilePath, cfg)
	if err != nil {
		return EffectiveAccountPoolLog(defaultConfigFile().ClaudeCode.Log), "", err
	}
	defer func() {
		_ = store.Close()
	}()
	doc, err := store.GetConfig(ctx)
	if err != nil {
		return EffectiveAccountPoolLog(defaultConfigFile().ClaudeCode.Log), "", err
	}
	effective := EffectiveAccountPoolLog(doc.ClaudeCode.Log)
	dir := effective.Dir
	if !filepath.IsAbs(dir) {
		base := filepath.Dir(store.initPath)
		if base == "." || base == "" {
			base = filepath.Dir(store.Path())
		}
		dir = filepath.Join(base, dir)
	}
	return effective, filepath.Clean(dir), nil
}

func accountPoolLogPath(ctx context.Context, configFilePath string, cfg *config.Config) (EffectiveAccountPoolLogConfig, string, error) {
	effective, dir, err := resolveAccountPoolLogConfig(ctx, configFilePath, cfg)
	if err != nil {
		return effective, "", err
	}
	if dir == "" {
		return effective, "", nil
	}
	return effective, filepath.Join(dir, accountPoolLogFileName), nil
}

// AccountPoolLogFilePath returns the active account-pool JSONL log path.
func AccountPoolLogFilePath(ctx context.Context, configFilePath string, cfg *config.Config) (string, error) {
	_, path, err := accountPoolLogPath(ctx, configFilePath, cfg)
	return path, err
}

// WriteAccountPoolLog writes one account-pool diagnostic line.
func WriteAccountPoolLog(ctx context.Context, configFilePath string, cfg *config.Config, entry AccountPoolLogEntry) error {
	effective, path, err := accountPoolLogPath(ctx, configFilePath, cfg)
	if err != nil || path == "" {
		return err
	}
	if !effective.Enabled {
		return nil
	}
	entry.Level = normalizeLogLevel(entry.Level)
	if !logLevelAllowed(entry.Level, effective.Level) {
		return nil
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	if effective.Redact {
		entry.AuthID = redactID(entry.AuthID)
		entry.AccountID = redactID(entry.AccountID)
		entry.SessionKey = redactID(entry.SessionKey)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create account pool log dir: %w", err)
	}
	writer := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    effective.MaxSizeMB,
		MaxBackups: effective.MaxBackups,
		Compress:   false,
	}
	defer func() {
		_ = writer.Close()
	}()
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal account pool log: %w", err)
	}
	if _, err := writer.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write account pool log: %w", err)
	}
	return nil
}

// ReadAccountPoolLogs returns the last N JSONL lines.
func ReadAccountPoolLogs(ctx context.Context, configFilePath string, cfg *config.Config, limit int) ([]AccountPoolLogView, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	_, path, err := accountPoolLogPath(ctx, configFilePath, cfg)
	if err != nil || path == "" {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AccountPoolLogView{}, nil
		}
		return nil, fmt.Errorf("open account pool log: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	ring := make([]string, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(ring) >= limit {
			copy(ring, ring[1:])
			ring[len(ring)-1] = line
		} else {
			ring = append(ring, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read account pool log: %w", err)
	}
	out := make([]AccountPoolLogView, 0, len(ring))
	for i := len(ring) - 1; i >= 0; i-- {
		view := AccountPoolLogView{Line: ring[i]}
		var entry AccountPoolLogEntry
		if err := json.Unmarshal([]byte(ring[i]), &entry); err == nil {
			view.Entry = &entry
		}
		out = append(out, view)
	}
	return out, nil
}

// ClearAccountPoolLogs removes account-pool log files.
func ClearAccountPoolLogs(ctx context.Context, configFilePath string, cfg *config.Config) error {
	_, path, err := accountPoolLogPath(ctx, configFilePath, cfg)
	if err != nil || path == "" {
		return err
	}
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read account pool log dir: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == accountPoolLogFileName || strings.HasPrefix(name, accountPoolLogFileName+".") {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove account pool log %s: %w", name, err)
			}
		}
	}
	return nil
}

func normalizeLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if _, ok := accountPoolLogLevels[level]; ok {
		return level
	}
	return "info"
}

func logLevelAllowed(entryLevel, configured string) bool {
	entryRank := accountPoolLogLevels[normalizeLogLevel(entryLevel)]
	configuredRank := accountPoolLogLevels[normalizeLogLevel(configured)]
	return entryRank >= configuredRank
}

func redactID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:6] + "..." + value[len(value)-4:]
}
