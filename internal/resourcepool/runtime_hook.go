package resourcepool

import (
	"context"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// RuntimeHook records lightweight Claude Code account-pool outcomes.
type RuntimeHook struct {
	ConfigPath string
	Config     *config.Config
}

// OnAuthRegistered implements coreauth.Hook.
func (h RuntimeHook) OnAuthRegistered(context.Context, *coreauth.Auth) {}

// OnAuthUpdated implements coreauth.Hook.
func (h RuntimeHook) OnAuthUpdated(context.Context, *coreauth.Auth) {}

// OnResult implements coreauth.Hook.
func (h RuntimeHook) OnResult(ctx context.Context, result coreauth.Result) {
	if h.Config == nil || !h.Config.ResourcePools.Enabled || !strings.EqualFold(result.Provider, "claude") {
		return
	}
	if strings.TrimSpace(result.AuthID) == "" {
		return
	}
	store, err := Open(h.ConfigPath, h.Config)
	if err != nil {
		return
	}
	defer func() {
		_ = store.Close()
	}()
	account, err := store.GetAccountByAuthID(ctx, result.AuthID)
	if err != nil || account == nil {
		return
	}
	statusCode := http.StatusOK
	message := ""
	if result.Error != nil {
		statusCode = result.Error.StatusCode()
		if statusCode == 0 {
			statusCode = statusCodeFromAccountResult(result.Success, result.Error.Message)
		}
		message = result.Error.Message
	}
	if !result.Success && statusCode == http.StatusOK {
		statusCode = statusCodeFromAccountResult(false, message)
	}
	_ = store.MarkAccountModelResult(ctx, account.ID, result.Model, statusCode, message)
	PublishAccountChanged(account.ID, "result")
	PublishStatsChanged(coreexecutor.PoolScopeClaudeAccountPool)
}

// UsagePlugin records token-bearing Claude Code account-pool usage rows.
type UsagePlugin struct {
	ConfigPath string
	Config     *config.Config
}

// HandleUsage implements coreusage.Plugin.
func (p UsagePlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if p.Config == nil || !p.Config.ResourcePools.Enabled || !strings.EqualFold(strings.TrimSpace(record.Provider), "claude") {
		return
	}
	if strings.TrimSpace(record.AuthID) == "" {
		return
	}
	store, err := Open(p.ConfigPath, p.Config)
	if err != nil {
		return
	}
	defer func() {
		_ = store.Close()
	}()
	account, err := store.GetAccountByAuthID(ctx, record.AuthID)
	if err != nil || account == nil {
		return
	}
	inputTokens := record.Detail.InputTokens
	doc, errConfig := store.GetConfig(ctx)
	if errConfig == nil && doc != nil {
		effective := EffectiveClaudeCodePool(doc.ClaudeCode)
		if effective.Usage.CleanInputTokens {
			overhead := effective.Usage.SystemPromptOverheadTokens
			if calibration, errCalibration := store.GetUsageCalibration(ctx, record.Model, effective.Usage.ProfileFingerprint); errCalibration == nil && calibration != nil && calibration.Status == UsageCalibrationCalibrated {
				overhead = calibration.OverheadTokens
			}
			inputTokens = CleanInputTokens(inputTokens, overhead)
			if floor := CleanInputFloorFromContext(ctx); floor > inputTokens {
				inputTokens = floor
			}
			if inputTokens > record.Detail.InputTokens {
				inputTokens = record.Detail.InputTokens
			}
		}
	}
	statusCode := http.StatusOK
	if record.Failed {
		statusCode = record.Fail.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
	}
	_ = store.RecordUsageLedger(ctx, UsageLedgerEntry{
		APIKeyPreview:       previewAPIKey(record.APIKey),
		AccountID:           account.ID,
		AuthID:              record.AuthID,
		Model:               record.Model,
		RequestedModel:      record.Alias,
		StatusCode:          statusCode,
		InputTokens:         inputTokens,
		OutputTokens:        record.Detail.OutputTokens,
		CacheReadTokens:     record.Detail.CacheReadTokens,
		CacheCreationTokens: record.Detail.CacheCreationTokens,
		Success:             !record.Failed,
		CreatedAt:           record.RequestedAt,
	})
	PublishStatsChanged(coreexecutor.PoolScopeClaudeAccountPool)
}

func previewAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 8 {
		return apiKey
	}
	return apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
}
