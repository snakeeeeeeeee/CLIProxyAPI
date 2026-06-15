package resourcepool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
)

const (
	UsageCalibrationCalibrated = "calibrated"
	UsageCalibrationEstimated  = "estimated"
	UsageCalibrationStale      = "stale"
	UsageCalibrationFailed     = "failed"
)

// ClaudeCodeProfileFingerprint returns the stable fingerprint for the built-in
// request profile that affects Claude Code prompt overhead.
func ClaudeCodeProfileFingerprint(profile EffectiveClaudeCodeProfileConfig) string {
	headers := make(map[string]string, len(profile.Headers))
	for key, value := range profile.Headers {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		headers[key] = value
	}
	betas := normalizeFingerprintList(profile.Betas)
	payload := struct {
		Version             string            `json:"version"`
		UserAgent           string            `json:"user_agent"`
		Headers             map[string]string `json:"headers"`
		Betas               []string          `json:"betas"`
		SystemPrompt        string            `json:"system_prompt"`
		StaticPrompt        string            `json:"static_prompt"`
		BillingBlockEnabled bool              `json:"billing_block_enabled"`
		BillingTemplate     string            `json:"billing_template"`
		MetadataUserIDMode  string            `json:"metadata_user_id_mode"`
		SystemPromptMode    string            `json:"system_prompt_mode"`
		SigningMode         string            `json:"signing_mode"`
	}{
		Version:             strings.TrimSpace(profile.Version),
		UserAgent:           strings.TrimSpace(profile.UserAgent),
		Headers:             headers,
		Betas:               betas,
		SystemPrompt:        strings.TrimSpace(profile.SystemPrompt),
		StaticPrompt:        helps.ClaudeCodeStaticPrompt(),
		BillingBlockEnabled: profile.BillingBlockEnabled,
		BillingTemplate:     "x-anthropic-billing-header: cc_version=<version>.<fp>; cc_entrypoint=cli; cch=<cch>;",
		MetadataUserIDMode:  strings.TrimSpace(profile.MetadataUserIDMode),
		SystemPromptMode:    strings.TrimSpace(profile.SystemPromptMode),
		SigningMode:         "experimental-cch-signing+oauth",
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeFingerprintList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

// CleanInputTokens subtracts the calibrated/estimated Claude Code profile
// overhead from a downstream-visible input token count.
func CleanInputTokens(inputTokens, overheadTokens int64) int64 {
	if inputTokens <= 0 {
		return inputTokens
	}
	cleaned := inputTokens - overheadTokens
	if cleaned < 1 {
		return 1
	}
	return cleaned
}

type cleanInputFloorContextKey struct{}

// WithCleanInputFloor stores the downstream-visible user input token estimate
// for clean-input usage rewriting and usage ledger rows.
func WithCleanInputFloor(ctx context.Context, floor int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if floor <= 0 {
		return ctx
	}
	return context.WithValue(ctx, cleanInputFloorContextKey{}, floor)
}

// CleanInputFloorFromContext returns the user-visible input token floor stored
// on the request context.
func CleanInputFloorFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	switch value := ctx.Value(cleanInputFloorContextKey{}).(type) {
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func normalizeUsageCalibrationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case UsageCalibrationCalibrated:
		return UsageCalibrationCalibrated
	case UsageCalibrationStale:
		return UsageCalibrationStale
	case UsageCalibrationFailed:
		return UsageCalibrationFailed
	default:
		return UsageCalibrationEstimated
	}
}

// UnmarshalJSON accepts both the API's snake_case fields and the YAML-style
// kebab-case aliases used by older resource console builds.
func (cfg *ClaudeCodeUsageConfig) UnmarshalJSON(data []byte) error {
	if cfg == nil {
		return nil
	}
	var raw struct {
		CleanInputTokensSnake           *bool `json:"clean_input_tokens"`
		CleanInputTokensKebab           *bool `json:"clean-input-tokens"`
		SystemPromptOverheadTokensSnake int64 `json:"system_prompt_overhead_tokens"`
		SystemPromptOverheadTokensKebab int64 `json:"system-prompt-overhead-tokens"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.CleanInputTokens = raw.CleanInputTokensSnake
	if cfg.CleanInputTokens == nil {
		cfg.CleanInputTokens = raw.CleanInputTokensKebab
	}
	cfg.SystemPromptOverheadTokens = raw.SystemPromptOverheadTokensSnake
	if cfg.SystemPromptOverheadTokens == 0 {
		cfg.SystemPromptOverheadTokens = raw.SystemPromptOverheadTokensKebab
	}
	return nil
}
