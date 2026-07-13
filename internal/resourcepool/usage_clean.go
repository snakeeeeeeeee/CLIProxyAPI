package resourcepool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/tiktoken-go/tokenizer"
)

const (
	UsageCalibrationCalibrated = "calibrated"
	UsageCalibrationEstimated  = "estimated"
	UsageCalibrationStale      = "stale"
	UsageCalibrationFailed     = "failed"
)

var DefaultCleanInputOverheadTokens = ClaudeCodeProfileInjectedOverheadTokens(EffectiveClaudeCodeProfileConfig{
	Revision:     DefaultClaudeCodeProfileRevision,
	Version:      DefaultClaudeCodeProfileVersion,
	SystemPrompt: helps.ClaudeCodeStaticPrompt(),
})

// ClaudeCodeProfileInjectedOverheadTokens estimates only the blocks added by
// account-pool mimic mode. Client-provided system text remains visible usage.
func ClaudeCodeProfileInjectedOverheadTokens(profile EffectiveClaudeCodeProfileConfig) int64 {
	version := strings.TrimSpace(profile.Version)
	if version == "" {
		version = DefaultClaudeCodeProfileVersion
	}
	staticPrompt := strings.TrimSpace(profile.SystemPrompt)
	if staticPrompt == "" {
		staticPrompt = helps.ClaudeCodeStaticPrompt()
	}
	text := strings.Join([]string{
		"x-anthropic-billing-header: cc_version=" + version + ".000; cc_entrypoint=sdk-cli;",
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.",
		staticPrompt,
	}, "\n")
	if enc, err := tokenizer.Get(tokenizer.Cl100kBase); err == nil && enc != nil {
		if count, errCount := enc.Count(text); errCount == nil && count > 0 {
			return int64(count)
		}
	}
	count := int64((len([]rune(text)) + 3) / 4)
	if count < 1 {
		return 1
	}
	return count
}

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
		Revision            string            `json:"revision"`
		Version             string            `json:"version"`
		UserAgent           string            `json:"user_agent"`
		Headers             map[string]string `json:"headers"`
		HeaderOrder         []string          `json:"header_order"`
		Betas               []string          `json:"betas"`
		SystemPrompt        string            `json:"system_prompt"`
		StaticPrompt        string            `json:"static_prompt"`
		BillingBlockEnabled bool              `json:"billing_block_enabled"`
		BillingTemplate     string            `json:"billing_template"`
		MetadataUserIDMode  string            `json:"metadata_user_id_mode"`
		SystemPromptMode    string            `json:"system_prompt_mode"`
		SigningMode         string            `json:"signing_mode"`
		TLSProfile          string            `json:"tls_profile"`
		TLSJA3              string            `json:"tls_ja3"`
		TLSJA4              string            `json:"tls_ja4"`
		TLSALPN             string            `json:"tls_alpn"`
	}{
		Revision:            strings.TrimSpace(profile.Revision),
		Version:             strings.TrimSpace(profile.Version),
		UserAgent:           strings.TrimSpace(profile.UserAgent),
		Headers:             headers,
		HeaderOrder:         append([]string(nil), profile.HeaderOrder...),
		Betas:               betas,
		SystemPrompt:        strings.TrimSpace(profile.SystemPrompt),
		StaticPrompt:        helps.ClaudeCodeStaticPrompt(),
		BillingBlockEnabled: profile.BillingBlockEnabled,
		BillingTemplate:     "x-anthropic-billing-header: cc_version=<version>.<fp>; cc_entrypoint=sdk-cli;",
		MetadataUserIDMode:  strings.TrimSpace(profile.MetadataUserIDMode),
		SystemPromptMode:    strings.TrimSpace(profile.SystemPromptMode),
		SigningMode:         "none",
		TLSProfile:          strings.TrimSpace(profile.TLSProfile),
		TLSJA3:              strings.TrimSpace(profile.TLSJA3),
		TLSJA4:              strings.TrimSpace(profile.TLSJA4),
		TLSALPN:             strings.TrimSpace(profile.TLSALPN),
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
