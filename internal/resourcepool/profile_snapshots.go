package resourcepool

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
	"golang.org/x/net/html"
)

const (
	profileSnapshotSourcePhistory = "phistory"
	phistoryHomepage              = "https://phistory.cc/"
	phistoryRawBase               = "https://raw.githubusercontent.com/WEIFENG2333/phistory/main/captures/claude-code"
	maxPhistoryFileBytes          = 2 << 20
)

var claudeCodeVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// FetchClaudeCodeProfileSnapshot fetches a Phistory baseline and stores it.
func (s *Store) FetchClaudeCodeProfileSnapshot(ctx context.Context, req ClaudeCodeProfileSnapshotFetchRequest) (*ClaudeCodeProfileSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	source := normalizeProfileSnapshotSource(req.Source)
	if source != profileSnapshotSourcePhistory {
		return nil, fmt.Errorf("unsupported profile snapshot source %q", source)
	}
	version := strings.TrimSpace(req.Version)
	if req.Latest || version == "" || strings.EqualFold(version, "latest") {
		latest, err := fetchLatestPhistoryClaudeCodeVersion(ctx)
		if err != nil {
			return nil, err
		}
		version = latest
	}
	if !claudeCodeVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid Claude Code version %q", version)
	}
	meta, err := fetchPhistoryFile(ctx, version, "meta.json")
	if err != nil {
		return nil, err
	}
	trace, err := fetchPhistoryFile(ctx, version, "trace.jsonl")
	if err != nil {
		return nil, err
	}
	prompt, err := fetchPhistoryFile(ctx, version, "prompt.md")
	if err != nil {
		return nil, err
	}
	staticPrompts, err := fetchPhistoryFile(ctx, version, "static-prompts.md")
	if err != nil {
		return nil, err
	}
	staticPromptsJSON, err := fetchPhistoryFile(ctx, version, "static-prompts.json")
	if err != nil {
		return nil, err
	}
	snapshot, err := BuildClaudeCodeProfileSnapshotArtifacts(source, version, meta, trace, prompt, staticPrompts, staticPromptsJSON)
	if err != nil {
		return nil, err
	}
	current, err := s.GetConfig(ctx)
	if err == nil {
		diff := DiffClaudeCodeProfileSnapshot(EffectiveClaudeCodeProfile(current.Profile), snapshot)
		snapshot.DiffReport = diff.Report
		snapshot.FatalCount = diff.FatalCount
		snapshot.WarnCount = diff.WarnCount
	}
	return s.UpsertClaudeCodeProfileSnapshot(ctx, *snapshot)
}

// BuildClaudeCodeProfileSnapshot normalizes fetched Phistory artifacts.
func BuildClaudeCodeProfileSnapshot(source, version, metaJSON, traceJSONL, promptMD string) (*ClaudeCodeProfileSnapshot, error) {
	return BuildClaudeCodeProfileSnapshotArtifacts(source, version, metaJSON, traceJSONL, promptMD, "", "")
}

// BuildClaudeCodeProfileSnapshotArtifacts normalizes full and static Phistory artifacts.
func BuildClaudeCodeProfileSnapshotArtifacts(source, version, metaJSON, traceJSONL, promptMD, staticPromptsMD, staticPromptsJSON string) (*ClaudeCodeProfileSnapshot, error) {
	source = normalizeProfileSnapshotSource(source)
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, fmt.Errorf("version is required")
	}
	profilePrompt := staticPromptsMD
	if strings.TrimSpace(profilePrompt) == "" {
		profilePrompt = promptMD
	}
	profile := normalizeProfileFromTrace(version, traceJSONL, profilePrompt)
	normalizedRaw, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("encode normalized profile: %w", err)
	}
	now := time.Now()
	promptHash := sha256Hex([]byte(promptMD))
	staticPromptHash := ""
	if strings.TrimSpace(staticPromptsMD) != "" {
		staticPromptHash = sha256Hex([]byte(staticPromptsMD))
	}
	traceHash := sha256Hex([]byte(traceJSONL))
	requestKindSummary := summarizeTraceRequestKinds(traceJSONL)
	return &ClaudeCodeProfileSnapshot{
		ID:                    uuid.NewString(),
		Source:                source,
		Version:               version,
		Status:                "fetched",
		MetaJSON:              strings.TrimSpace(metaJSON),
		TraceJSONL:            strings.TrimSpace(traceJSONL),
		PromptMD:              promptMD,
		StaticPromptsMD:       staticPromptsMD,
		StaticPromptsJSON:     strings.TrimSpace(staticPromptsJSON),
		NormalizedProfileJSON: string(normalizedRaw),
		NormalizedProfile:     &profile,
		PromptHash:            promptHash,
		StaticPromptHash:      staticPromptHash,
		StaticPromptLength:    len(staticPromptsMD),
		FullPromptHash:        promptHash,
		FullPromptLength:      len(promptMD),
		RequestKindSummary:    requestKindSummary,
		TraceHash:             traceHash,
		FetchedAt:             &now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

// ListClaudeCodeProfileSnapshots returns profile baselines without large raw fields.
func (s *Store) ListClaudeCodeProfileSnapshots(ctx context.Context) ([]ClaudeCodeProfileSnapshot, error) {
	if s == nil || s.db == nil {
		return []ClaudeCodeProfileSnapshot{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, source, version, status, normalized_profile_json, prompt_hash,
       static_prompt_hash, static_prompt_length, full_prompt_hash, full_prompt_length,
       request_kind_summary_json, trace_hash,
       diff_report, fatal_count, warn_count, promoted, last_error, fetched_at,
       promoted_at, created_at, updated_at
FROM claude_code_profile_snapshots
ORDER BY promoted DESC, updated_at DESC
`)
	if err != nil {
		return nil, fmt.Errorf("list claude code profile snapshots: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	out := make([]ClaudeCodeProfileSnapshot, 0)
	for rows.Next() {
		snapshot, err := scanProfileSnapshot(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claude code profile snapshots: %w", err)
	}
	return out, nil
}

// GetClaudeCodeProfileSnapshot returns one full snapshot by id.
func (s *Store) GetClaudeCodeProfileSnapshot(ctx context.Context, id string) (*ClaudeCodeProfileSnapshot, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, source, version, status, meta_json, trace_jsonl, prompt_md,
       static_prompts_md, static_prompts_json, normalized_profile_json, prompt_hash,
       static_prompt_hash, static_prompt_length, full_prompt_hash, full_prompt_length,
       request_kind_summary_json, trace_hash, diff_report,
       fatal_count, warn_count, promoted, last_error, fetched_at, promoted_at,
       created_at, updated_at
FROM claude_code_profile_snapshots
WHERE id = ?
`, id)
	snapshot, err := scanProfileSnapshot(row, true)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// UpsertClaudeCodeProfileSnapshot stores a snapshot by source/version.
func (s *Store) UpsertClaudeCodeProfileSnapshot(ctx context.Context, snapshot ClaudeCodeProfileSnapshot) (*ClaudeCodeProfileSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	snapshot.Source = normalizeProfileSnapshotSource(snapshot.Source)
	snapshot.Version = strings.TrimSpace(snapshot.Version)
	if snapshot.Version == "" {
		return nil, fmt.Errorf("version is required")
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		snapshot.ID = uuid.NewString()
	}
	now := time.Now()
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	snapshot.UpdatedAt = now
	if snapshot.FetchedAt == nil {
		snapshot.FetchedAt = &now
	}
	if snapshot.NormalizedProfile != nil && strings.TrimSpace(snapshot.NormalizedProfileJSON) == "" {
		raw, err := json.Marshal(snapshot.NormalizedProfile)
		if err != nil {
			return nil, fmt.Errorf("encode normalized profile: %w", err)
		}
		snapshot.NormalizedProfileJSON = string(raw)
	}
	requestKindSummaryJSON, err := json.Marshal(snapshot.RequestKindSummary)
	if err != nil {
		return nil, fmt.Errorf("encode request-kind summary: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO claude_code_profile_snapshots(
  id, source, version, status, meta_json, trace_jsonl, prompt_md, static_prompts_md, static_prompts_json,
  normalized_profile_json, prompt_hash, static_prompt_hash, static_prompt_length,
  full_prompt_hash, full_prompt_length, request_kind_summary_json, trace_hash, diff_report,
  fatal_count, warn_count, promoted, last_error, fetched_at, promoted_at,
  created_at, updated_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
ON CONFLICT(source, version) DO UPDATE SET
  status = excluded.status,
  meta_json = excluded.meta_json,
  trace_jsonl = excluded.trace_jsonl,
  prompt_md = excluded.prompt_md,
	static_prompts_md = excluded.static_prompts_md,
	static_prompts_json = excluded.static_prompts_json,
  normalized_profile_json = excluded.normalized_profile_json,
  prompt_hash = excluded.prompt_hash,
	static_prompt_hash = excluded.static_prompt_hash,
	static_prompt_length = excluded.static_prompt_length,
	full_prompt_hash = excluded.full_prompt_hash,
	full_prompt_length = excluded.full_prompt_length,
	request_kind_summary_json = excluded.request_kind_summary_json,
  trace_hash = excluded.trace_hash,
  diff_report = excluded.diff_report,
  fatal_count = excluded.fatal_count,
  warn_count = excluded.warn_count,
  last_error = excluded.last_error,
  fetched_at = excluded.fetched_at,
  updated_at = excluded.updated_at
`, snapshot.ID, snapshot.Source, snapshot.Version, strings.TrimSpace(snapshot.Status),
		nonEmptyJSON(snapshot.MetaJSON), snapshot.TraceJSONL, snapshot.PromptMD, snapshot.StaticPromptsMD, nonEmptyJSON(snapshot.StaticPromptsJSON),
		nonEmptyJSON(snapshot.NormalizedProfileJSON), snapshot.PromptHash, snapshot.StaticPromptHash, snapshot.StaticPromptLength,
		snapshot.FullPromptHash, snapshot.FullPromptLength, nonEmptyJSON(string(requestKindSummaryJSON)), snapshot.TraceHash, snapshot.DiffReport,
		snapshot.FatalCount, snapshot.WarnCount, boolInt(snapshot.Promoted), strings.TrimSpace(snapshot.LastError),
		timePtrText(snapshot.FetchedAt), timePtrText(snapshot.PromotedAt), dbTime(snapshot.CreatedAt), dbTime(snapshot.UpdatedAt))
	if err != nil {
		return nil, fmt.Errorf("save claude code profile snapshot: %w", err)
	}
	return s.GetClaudeCodeProfileSnapshotBySourceVersion(ctx, snapshot.Source, snapshot.Version)
}

// GetClaudeCodeProfileSnapshotBySourceVersion returns a full snapshot by source/version.
func (s *Store) GetClaudeCodeProfileSnapshotBySourceVersion(ctx context.Context, source, version string) (*ClaudeCodeProfileSnapshot, error) {
	source = normalizeProfileSnapshotSource(source)
	version = strings.TrimSpace(version)
	row := s.db.QueryRowContext(ctx, `
SELECT id, source, version, status, meta_json, trace_jsonl, prompt_md,
       static_prompts_md, static_prompts_json, normalized_profile_json, prompt_hash,
       static_prompt_hash, static_prompt_length, full_prompt_hash, full_prompt_length,
       request_kind_summary_json, trace_hash, diff_report,
       fatal_count, warn_count, promoted, last_error, fetched_at, promoted_at,
       created_at, updated_at
FROM claude_code_profile_snapshots
WHERE source = ? AND version = ?
`, source, version)
	snapshot, err := scanProfileSnapshot(row, true)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// DiffClaudeCodeProfileSnapshot compares a snapshot with the current profile.
func DiffClaudeCodeProfileSnapshot(current EffectiveClaudeCodeProfileConfig, snapshot *ClaudeCodeProfileSnapshot) ClaudeCodeProfileSnapshotDiff {
	diff := ClaudeCodeProfileSnapshotDiff{}
	if snapshot == nil {
		diff.FatalCount = 1
		diff.Report = "snapshot is missing"
		diff.Issues = []string{diff.Report}
		return diff
	}
	diff.SnapshotID = snapshot.ID
	diff.Version = snapshot.Version
	diff.CurrentVersion = current.Version
	diff.ProfileFingerprint = ClaudeCodeProfileFingerprint(current)
	profile := snapshot.NormalizedProfile
	if profile == nil && strings.TrimSpace(snapshot.NormalizedProfileJSON) != "" {
		var decoded ClaudeCodeProfile
		if err := json.Unmarshal([]byte(snapshot.NormalizedProfileJSON), &decoded); err == nil {
			profile = &decoded
		}
	}
	if profile == nil {
		diff.FatalCount = 1
		diff.Report = "snapshot normalized profile is missing"
		diff.Issues = []string{diff.Report}
		return diff
	}
	effectiveSnapshot := EffectiveClaudeCodeProfile(*profile)
	addIssue := func(level, message string) {
		line := level + ": " + message
		diff.Issues = append(diff.Issues, line)
		if level == "fatal" {
			diff.FatalCount++
		} else if level == "warn" {
			diff.WarnCount++
		}
	}
	if strings.TrimSpace(current.Version) != strings.TrimSpace(effectiveSnapshot.Version) {
		addIssue("warn", fmt.Sprintf("version current=%s snapshot=%s", current.Version, effectiveSnapshot.Version))
	}
	if strings.TrimSpace(current.UserAgent) != strings.TrimSpace(effectiveSnapshot.UserAgent) {
		addIssue("warn", fmt.Sprintf("user-agent current=%q snapshot=%q", current.UserAgent, effectiveSnapshot.UserAgent))
	}
	compareHeaderMap("header", current.Headers, effectiveSnapshot.Headers, addIssue)
	compareStringSet("beta", current.Betas, effectiveSnapshot.Betas, addIssue)
	currentPromptHash := sha256Hex([]byte(strings.TrimSpace(current.SystemPrompt)))
	snapshotPromptHash := snapshot.StaticPromptHash
	if snapshotPromptHash == "" {
		snapshotPromptHash = snapshot.PromptHash
	}
	if snapshotPromptHash != "" && currentPromptHash != snapshotPromptHash {
		addIssue("warn", fmt.Sprintf("stable prompt hash current=%s snapshot=%s", shortHash(currentPromptHash), shortHash(snapshotPromptHash)))
	}
	if len(diff.Issues) == 0 {
		diff.Report = "ok: 当前 profile 与快照摘要一致"
	} else {
		diff.Report = strings.Join(diff.Issues, "\n")
	}
	return diff
}

// RefreshClaudeCodeProfileSnapshotDiff recalculates and stores the diff report.
func (s *Store) RefreshClaudeCodeProfileSnapshotDiff(ctx context.Context, id string) (*ClaudeCodeProfileSnapshotDiff, error) {
	snapshot, err := s.GetClaudeCodeProfileSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	doc, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	diff := DiffClaudeCodeProfileSnapshot(EffectiveClaudeCodeProfile(doc.Profile), snapshot)
	if _, err := s.db.ExecContext(ctx, `
UPDATE claude_code_profile_snapshots
SET diff_report = ?, fatal_count = ?, warn_count = ?
WHERE id = ?
`, diff.Report, diff.FatalCount, diff.WarnCount, snapshot.ID); err != nil {
		return nil, fmt.Errorf("save claude code profile snapshot diff: %w", err)
	}
	return &diff, nil
}

// PromoteClaudeCodeProfileSnapshot is intentionally disabled. Phistory snapshots
// are reference baselines for trace comparison only and must not be applied to
// the runtime Claude Code profile.
func (s *Store) PromoteClaudeCodeProfileSnapshot(ctx context.Context, id string) (*ConfigFile, *ClaudeCodeProfileSnapshot, error) {
	snapshot, err := s.GetClaudeCodeProfileSnapshot(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return nil, snapshot, fmt.Errorf("profile snapshots are reference-only and cannot be applied to runtime profile")
}

func fetchLatestPhistoryClaudeCodeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, phistoryHomepage, nil)
	if err != nil {
		return "", err
	}
	body, err := doProfileSnapshotHTTPRequest(req, maxPhistoryFileBytes)
	if err != nil {
		return "", fmt.Errorf("fetch Phistory versions: %w", err)
	}
	manifestRaw, err := phistoryManifestJSON(body)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Agents []struct {
			ID     string `json:"id"`
			Latest struct {
				Version string `json:"version"`
			} `json:"latest"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return "", fmt.Errorf("decode Phistory manifest: %w", err)
	}
	for _, agent := range manifest.Agents {
		if agent.ID == "claude-code" && claudeCodeVersionPattern.MatchString(strings.TrimSpace(agent.Latest.Version)) {
			return strings.TrimSpace(agent.Latest.Version), nil
		}
	}
	return "", fmt.Errorf("Claude Code latest version missing from Phistory manifest")
}

func phistoryManifestJSON(page []byte) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("parse Phistory homepage: %w", err)
	}
	var visit func(*html.Node) string
	visit = func(node *html.Node) string {
		if node.Type == html.ElementNode && node.Data == "script" {
			for _, attr := range node.Attr {
				if attr.Key == "id" && attr.Val == "manifest" && node.FirstChild != nil {
					return node.FirstChild.Data
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if value := visit(child); value != "" {
				return value
			}
		}
		return ""
	}
	raw := strings.TrimSpace(visit(doc))
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("Phistory manifest script is missing or invalid")
	}
	return []byte(raw), nil
}

func summarizeTraceRequestKinds(traceJSONL string) map[string]int {
	summary := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(traceJSONL))
	scanner.Buffer(make([]byte, 0, 64*1024), maxPhistoryFileBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Request struct {
				Path string          `json:"path"`
				Body json.RawMessage `json:"body"`
			} `json:"request"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		path := entry.Request.Path
		if index := strings.IndexByte(path, '?'); index >= 0 {
			path = path[:index]
		}
		kind := claudetrace.InferRequestKind(path, entry.Request.Body)
		summary[kind]++
	}
	return summary
}

func fetchPhistoryFile(ctx context.Context, version, name string) (string, error) {
	url := phistoryRawBase + "/" + strings.TrimSpace(version) + "/" + strings.TrimSpace(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	body, err := doProfileSnapshotHTTPRequest(req, maxPhistoryFileBytes)
	if err != nil {
		return "", fmt.Errorf("fetch Phistory %s: %w", name, err)
	}
	return string(body), nil
}

func doProfileSnapshotHTTPRequest(req *http.Request, maxBytes int64) ([]byte, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(limited)))
	}
	reader := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response too large")
	}
	return body, nil
}

func normalizeProfileFromTrace(version, traceJSONL, promptMD string) ClaudeCodeProfile {
	profile := defaultClaudeCodeProfile()
	profile.Version = strings.TrimSpace(version)
	profile.UserAgent = "claude-cli/" + strings.TrimSpace(version) + " (external, sdk-cli)"
	profile.UpdatedFrom = "phistory:" + strings.TrimSpace(version)
	profile.SystemPrompt = strings.TrimSpace(promptMD)
	profile.SystemPromptMode = "phistory_prompt"
	profile.Locked = true
	if strings.TrimSpace(traceJSONL) == "" {
		return profile
	}
	scanner := bufio.NewScanner(strings.NewReader(traceJSONL))
	scanner.Buffer(make([]byte, 0, 64*1024), maxPhistoryFileBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Request struct {
				Headers map[string]any `json:"headers"`
				Body    struct {
					System []struct {
						Text string `json:"text"`
					} `json:"system"`
					Tools []any `json:"tools"`
				} `json:"body"`
			} `json:"request"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if len(entry.Request.Headers) > 0 {
			applyTraceHeadersToProfile(&profile, entry.Request.Headers)
		}
		if len(entry.Request.Body.System) > 0 && strings.TrimSpace(promptMD) == "" {
			parts := make([]string, 0, len(entry.Request.Body.System))
			for _, block := range entry.Request.Body.System {
				if text := strings.TrimSpace(block.Text); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				profile.SystemPrompt = strings.Join(parts, "\n\n")
			}
		}
		break
	}
	return profile
}

func applyTraceHeadersToProfile(profile *ClaudeCodeProfile, headers map[string]any) {
	if profile == nil {
		return
	}
	profileHeaders := make(map[string]string, len(profile.Headers)+8)
	for key, value := range profile.Headers {
		profileHeaders[key] = value
	}
	for key, value := range headers {
		headerName := strings.TrimSpace(key)
		headerValue := strings.TrimSpace(fmt.Sprint(value))
		if headerName == "" || headerValue == "" || headerValue == "***" {
			continue
		}
		switch strings.ToLower(headerName) {
		case "user-agent":
			profile.UserAgent = headerValue
		case "anthropic-beta":
			profile.Betas = parseCommaList(headerValue)
		case "anthropic-version", "x-app", "x-stainless-runtime", "x-stainless-lang", "x-stainless-retry-count", "x-stainless-timeout", "x-stainless-package-version", "x-stainless-runtime-version", "x-stainless-os", "x-stainless-arch":
			profileHeaders[canonicalTraceHeaderName(headerName)] = headerValue
		}
	}
	profile.Headers = profileHeaders
}

func canonicalTraceHeaderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic-version":
		return "Anthropic-Version"
	case "x-app":
		return "X-App"
	case "x-stainless-runtime":
		return "X-Stainless-Runtime"
	case "x-stainless-lang":
		return "X-Stainless-Lang"
	case "x-stainless-retry-count":
		return "X-Stainless-Retry-Count"
	case "x-stainless-timeout":
		return "X-Stainless-Timeout"
	case "x-stainless-package-version":
		return "X-Stainless-Package-Version"
	case "x-stainless-runtime-version":
		return "X-Stainless-Runtime-Version"
	case "x-stainless-os":
		return "X-Stainless-OS"
	case "x-stainless-arch":
		return "X-Stainless-Arch"
	default:
		return strings.TrimSpace(name)
	}
}

func scanProfileSnapshot(rows interface {
	Scan(dest ...interface{}) error
}, full bool) (ClaudeCodeProfileSnapshot, error) {
	var snapshot ClaudeCodeProfileSnapshot
	var promoted int
	var requestKindSummaryJSON string
	var fetchedRaw, promotedRaw sql.NullString
	var createdRaw, updatedRaw string
	if full {
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.Source,
			&snapshot.Version,
			&snapshot.Status,
			&snapshot.MetaJSON,
			&snapshot.TraceJSONL,
			&snapshot.PromptMD,
			&snapshot.StaticPromptsMD,
			&snapshot.StaticPromptsJSON,
			&snapshot.NormalizedProfileJSON,
			&snapshot.PromptHash,
			&snapshot.StaticPromptHash,
			&snapshot.StaticPromptLength,
			&snapshot.FullPromptHash,
			&snapshot.FullPromptLength,
			&requestKindSummaryJSON,
			&snapshot.TraceHash,
			&snapshot.DiffReport,
			&snapshot.FatalCount,
			&snapshot.WarnCount,
			&promoted,
			&snapshot.LastError,
			&fetchedRaw,
			&promotedRaw,
			&createdRaw,
			&updatedRaw,
		); err != nil {
			return snapshot, fmt.Errorf("scan claude code profile snapshot: %w", err)
		}
	} else {
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.Source,
			&snapshot.Version,
			&snapshot.Status,
			&snapshot.NormalizedProfileJSON,
			&snapshot.PromptHash,
			&snapshot.StaticPromptHash,
			&snapshot.StaticPromptLength,
			&snapshot.FullPromptHash,
			&snapshot.FullPromptLength,
			&requestKindSummaryJSON,
			&snapshot.TraceHash,
			&snapshot.DiffReport,
			&snapshot.FatalCount,
			&snapshot.WarnCount,
			&promoted,
			&snapshot.LastError,
			&fetchedRaw,
			&promotedRaw,
			&createdRaw,
			&updatedRaw,
		); err != nil {
			return snapshot, fmt.Errorf("scan claude code profile snapshot: %w", err)
		}
	}
	snapshot.Promoted = promoted != 0
	_ = json.Unmarshal([]byte(requestKindSummaryJSON), &snapshot.RequestKindSummary)
	snapshot.FetchedAt = parseNullTime(fetchedRaw)
	snapshot.PromotedAt = parseNullTime(promotedRaw)
	snapshot.CreatedAt = parseDBTime(createdRaw)
	snapshot.UpdatedAt = parseDBTime(updatedRaw)
	if strings.TrimSpace(snapshot.NormalizedProfileJSON) != "" {
		var profile ClaudeCodeProfile
		if err := json.Unmarshal([]byte(snapshot.NormalizedProfileJSON), &profile); err == nil {
			snapshot.NormalizedProfile = &profile
		}
	}
	return snapshot, nil
}

func normalizeProfileSnapshotSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", profileSnapshotSourcePhistory:
		return profileSnapshotSourcePhistory
	default:
		return strings.ToLower(strings.TrimSpace(source))
	}
}

func nonEmptyJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

func timePtrText(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return dbTime(*t)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func parseCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func compareHeaderMap(label string, current, snapshot map[string]string, addIssue func(string, string)) {
	currentNorm := normalizeCompareMap(current)
	snapshotNorm := normalizeCompareMap(snapshot)
	snapshotKeys := sortedMapKeys(snapshotNorm)
	currentKeys := sortedMapKeys(currentNorm)
	for _, key := range snapshotKeys {
		snapshotValue := snapshotNorm[key]
		if currentValue, ok := currentNorm[key]; !ok {
			addIssue("warn", fmt.Sprintf("%s missing current key=%s snapshot=%q", label, key, snapshotValue))
		} else if currentValue != snapshotValue {
			addIssue("warn", fmt.Sprintf("%s mismatch key=%s current=%q snapshot=%q", label, key, currentValue, snapshotValue))
		}
	}
	for _, key := range currentKeys {
		if _, ok := snapshotNorm[key]; !ok {
			addIssue("info", fmt.Sprintf("%s extra current key=%s", label, key))
		}
	}
}

func normalizeCompareMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func compareStringSet(label string, current, snapshot []string, addIssue func(string, string)) {
	currentSet := listSet(current)
	snapshotSet := listSet(snapshot)
	snapshotValues := sortedBoolMapKeys(snapshotSet)
	currentValues := sortedBoolMapKeys(currentSet)
	for _, value := range snapshotValues {
		if !currentSet[value] {
			addIssue("warn", fmt.Sprintf("%s missing current value=%s", label, value))
		}
	}
	for _, value := range currentValues {
		if !snapshotSet[value] {
			addIssue("info", fmt.Sprintf("%s extra current value=%s", label, value))
		}
	}
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func listSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = true
	}
	return out
}

func compareSemver(a, b string) int {
	ap := semverParts(a)
	bp := semverParts(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func semverParts(version string) [3]int {
	var out [3]int
	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		value, err := strconv.Atoi(parts[i])
		if err == nil {
			out[i] = value
		}
	}
	return out
}

func IsProfileSnapshotNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
