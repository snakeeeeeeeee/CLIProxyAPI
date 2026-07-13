package resourcepool

import (
	"context"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
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

// OnRouteEvent implements coreauth.Hook.
func (h RuntimeHook) OnRouteEvent(ctx context.Context, event coreauth.RouteEvent) {
	if h.Config == nil || !h.Config.ResourcePools.Enabled || !strings.EqualFold(event.Provider, "claude") {
		return
	}
	if strings.TrimSpace(event.Scope) != coreexecutor.PoolScopeClaudeAccountPool {
		return
	}
	if strings.TrimSpace(event.AuthID) == "" {
		return
	}
	store, err := Open(h.ConfigPath, h.Config)
	if err != nil {
		return
	}
	defer func() {
		_ = store.Close()
	}()
	account, err := store.GetAccountByAuthID(ctx, event.AuthID)
	if err != nil || account == nil {
		return
	}
	routingEvent := RoutingEvent{
		PoolID:          account.PoolID,
		APIKeyID:        event.APIKeyID,
		RequestID:       event.RequestID,
		AccountID:       account.ID,
		AuthID:          event.AuthID,
		Model:           event.Model,
		RequestedModel:  event.RequestedModel,
		ProxyResourceID: event.ProxyResourceID,
		Sticky:          event.Sticky,
		SessionKey:      event.SessionKey,
		CapacityUsed:    event.CapacityUsed,
		CapacityLimit:   event.CapacityLimit,
		InFlight:        event.InFlight,
		Concurrency:     event.Concurrency,
		RPMUsed:         event.RPMUsed,
		RPMLimit:        event.RPMLimit,
		Attempt:         event.Attempt,
		SwitchCount:     event.SwitchCount,
		WaitMS:          event.WaitMS,
		AffinityMode:    event.AffinityMode,
		PrimaryHit:      event.PrimaryHit,
		BackupLane:      event.BackupLane,
		Decision:        event.Decision,
		Reason:          event.Reason,
		StatusCode:      event.StatusCode,
		Error:           event.Error,
		CreatedAt:       event.CreatedAt,
	}
	_ = store.RecordRoutingEvent(ctx, routingEvent)
	level := "info"
	switch event.Decision {
	case "rejected":
		level = "warn"
	case "upstream_error":
		level = "error"
	case "selected":
		level = "debug"
	}
	_ = WriteAccountPoolLog(ctx, h.ConfigPath, h.Config, AccountPoolLogEntry{
		Time:            event.CreatedAt,
		Level:           level,
		Event:           "route_" + strings.TrimSpace(event.Decision),
		RequestID:       event.RequestID,
		Model:           event.Model,
		RequestedModel:  event.RequestedModel,
		AccountID:       account.ID,
		AuthID:          event.AuthID,
		ProxyResourceID: event.ProxyResourceID,
		Sticky:          event.Sticky,
		SessionKey:      event.SessionKey,
		InFlight:        event.InFlight,
		Concurrency:     event.Concurrency,
		RPMUsed:         event.RPMUsed,
		RPMLimit:        event.RPMLimit,
		Attempt:         event.Attempt,
		SwitchCount:     event.SwitchCount,
		WaitMS:          event.WaitMS,
		AffinityMode:    event.AffinityMode,
		PrimaryHit:      event.PrimaryHit,
		BackupLane:      event.BackupLane,
		Decision:        event.Decision,
		Reason:          event.Reason,
		StatusCode:      event.StatusCode,
		Error:           event.Error,
	})
	PublishAccountChanged(account.ID, "route")
	PublishStatsChanged(coreexecutor.PoolScopeClaudeAccountPool)
}

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
	store.applyRuntimeHealthResult(ctx, account, statusCode, result.Success, message)
	if result.Success {
		_ = store.RecordAccountRuntimeError(ctx, account.ID, "")
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusPaymentRequired || statusCode == http.StatusForbidden || strings.TrimSpace(result.Model) == "" {
		_ = store.RecordAccountRuntimeError(ctx, account.ID, message)
	} else {
		_ = store.MarkAccountModelResult(ctx, account.ID, result.Model, statusCode, message)
	}
	if !result.Success {
		_ = WriteAccountPoolLog(ctx, h.ConfigPath, h.Config, AccountPoolLogEntry{
			Level:      "warn",
			Event:      "account_result",
			Model:      result.Model,
			AccountID:  account.ID,
			AuthID:     result.AuthID,
			StatusCode: statusCode,
			Decision:   "result",
			Reason:     "mark_result",
			Error:      message,
		})
	}
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
	if record.Failed && (record.Fail.StatusCode == http.StatusBadRequest || record.Fail.StatusCode == http.StatusUnprocessableEntity) {
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
	if len(record.ResponseHeaders) > 0 {
		if updated, errMerge := store.MergeAccountRateLimitHeaders(ctx, account.ID, record.ResponseHeaders); errMerge == nil && updated != nil {
			account = updated
		}
	}
	statusCode := http.StatusOK
	if record.Failed {
		statusCode = record.Fail.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
	}
	rawTotal := record.Detail.TotalTokens
	if rawTotal <= 0 {
		rawTotal = record.Detail.InputTokens + record.Detail.OutputTokens + record.Detail.CacheReadTokens + record.Detail.CacheCreationTokens
	}
	billing := coreusage.AccountPoolBillingFromContext(ctx)
	apiKeyPreview := previewAPIKey(record.APIKey)
	if billing.APIKeyID != "" {
		if key, errKey := store.GetPoolAPIKey(ctx, billing.APIKeyID); errKey == nil && key != nil {
			apiKeyPreview = key.KeyPrefix
		}
	}
	entry := UsageLedgerEntry{
		PoolID:              account.PoolID,
		APIKeyID:            billing.APIKeyID,
		RequestID:           logging.GetRequestID(ctx),
		APIKeyPreview:       apiKeyPreview,
		AccountID:           account.ID,
		AuthID:              record.AuthID,
		Model:               record.Model,
		RequestedModel:      record.Alias,
		StatusCode:          statusCode,
		InputTokens:         record.Detail.InputTokens,
		OutputTokens:        record.Detail.OutputTokens,
		CacheReadTokens:     record.Detail.CacheReadTokens,
		CacheCreationTokens: record.Detail.CacheCreationTokens,
		CacheCreation5m:     record.Detail.CacheCreation5mTokens,
		CacheCreation1h:     record.Detail.CacheCreation1hTokens,
		RawInputTokens:      record.Detail.InputTokens,
		RawTotalTokens:      rawTotal,
		PriceVersionID:      billing.PriceVersionID,
		Success:             !record.Failed,
		CreatedAt:           record.RequestedAt,
	}
	if errPrice := store.ApplyUsagePricing(ctx, &entry); errPrice != nil {
		entry.PricingStatus = "unpriced"
	}
	_ = store.RecordUsageLedger(ctx, entry)
	level := "info"
	event := "usage_success"
	errorMessage := ""
	if record.Failed {
		level = "error"
		event = "usage_failure"
		errorMessage = strings.TrimSpace(record.Fail.Body)
	}
	_ = WriteAccountPoolLog(ctx, p.ConfigPath, p.Config, AccountPoolLogEntry{
		Time:            record.RequestedAt,
		Level:           level,
		Event:           event,
		RequestID:       logging.GetRequestID(ctx),
		Model:           record.Model,
		RequestedModel:  record.Alias,
		AccountID:       account.ID,
		AuthID:          record.AuthID,
		StatusCode:      statusCode,
		LatencyMS:       record.Latency.Milliseconds(),
		InputTokens:     record.Detail.InputTokens,
		OutputTokens:    record.Detail.OutputTokens,
		CacheReadTokens: record.Detail.CacheReadTokens,
		CacheCreate:     record.Detail.CacheCreationTokens,
		TotalTokens:     rawTotal,
		Error:           errorMessage,
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
