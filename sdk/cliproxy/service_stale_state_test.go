package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestServiceApplyCoreAuthAddOrUpdate_DeleteReAddDoesNotInheritStaleRuntimeState(t *testing.T) {
	service := &Service{
		cfg:         &config.Config{},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}

	authID := "service-stale-state-auth"
	modelID := "stale-model"
	lastRefreshedAt := time.Date(2026, time.March, 1, 8, 0, 0, 0, time.UTC)
	nextRefreshAfter := lastRefreshedAt.Add(30 * time.Minute)

	t.Cleanup(func() {
		GlobalModelRegistry().UnregisterClient(authID)
	})

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:               authID,
		Provider:         "claude",
		Status:           coreauth.StatusActive,
		LastRefreshedAt:  lastRefreshedAt,
		NextRefreshAfter: nextRefreshAfter,
		ModelStates: map[string]*coreauth.ModelState{
			modelID: {
				Quota: coreauth.QuotaState{BackoffLevel: 7},
			},
		},
	})

	service.applyCoreAuthRemoval(context.Background(), authID)

	if _, ok := service.coreManager.GetByID(authID); ok {
		t.Fatalf("expected auth %q to be removed from runtime state", authID)
	}

	service.applyCoreAuthAddOrUpdate(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "claude",
		Status:   coreauth.StatusActive,
	})

	updated, ok := service.coreManager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatalf("expected re-added auth to be present")
	}
	if updated.Disabled {
		t.Fatalf("expected re-added auth to be active")
	}
	if !updated.LastRefreshedAt.IsZero() {
		t.Fatalf("expected LastRefreshedAt to reset on delete -> re-add, got %v", updated.LastRefreshedAt)
	}
	if !updated.NextRefreshAfter.IsZero() {
		t.Fatalf("expected NextRefreshAfter to reset on delete -> re-add, got %v", updated.NextRefreshAfter)
	}
	if len(updated.ModelStates) != 0 {
		t.Fatalf("expected ModelStates to reset on delete -> re-add, got %d entries", len(updated.ModelStates))
	}
	if models := registry.GetGlobalRegistry().GetModelsForClient(authID); len(models) == 0 {
		t.Fatalf("expected re-added auth to re-register models in global registry")
	}
}

func TestForceHomeRuntimeConfigEnablesUsageStatistics(t *testing.T) {
	cfg := &config.Config{
		UsageStatisticsEnabled: false,
	}

	forceHomeRuntimeConfig(cfg)

	if !cfg.UsageStatisticsEnabled {
		t.Fatal("expected home runtime config to force usage statistics enabled")
	}
}

func TestApplyHomeOverlayForcesUsageStatisticsEnabled(t *testing.T) {
	baseCfg := &config.Config{}
	baseCfg.Home.Enabled = true
	service := &Service{cfg: baseCfg}

	service.applyHomeOverlay(&config.Config{
		UsageStatisticsEnabled: false,
	})

	if service.cfg == nil || !service.cfg.UsageStatisticsEnabled {
		t.Fatal("expected home overlay to force usage statistics enabled")
	}
	if !service.cfg.Home.Enabled {
		t.Fatal("expected home overlay to preserve local home settings")
	}
}

func TestSyncClaudeAPIPoolAuths_DisabledPoolRemovesRuntimePoolAuths(t *testing.T) {
	service := &Service{
		cfg: &config.Config{
			ClaudeAPIPool: config.ClaudeAPIPoolConfig{Enabled: false},
		},
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	poolAuth := &coreauth.Auth{
		ID:       "pool-auth",
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			claudeapipool.AttrPool: "true",
		},
	}
	legacyAuth := &coreauth.Auth{
		ID:       "legacy-auth",
		Provider: "claude",
		Status:   coreauth.StatusActive,
	}
	if _, err := service.coreManager.Register(context.Background(), poolAuth); err != nil {
		t.Fatalf("register pool auth: %v", err)
	}
	if _, err := service.coreManager.Register(context.Background(), legacyAuth); err != nil {
		t.Fatalf("register legacy auth: %v", err)
	}

	if err := service.SyncClaudeAPIPoolAuths(context.Background()); err != nil {
		t.Fatalf("SyncClaudeAPIPoolAuths() error = %v", err)
	}

	gotPool, ok := service.coreManager.GetByID(poolAuth.ID)
	if !ok || gotPool == nil {
		t.Fatalf("expected pool auth to remain as disabled runtime entry")
	}
	if !gotPool.Disabled || gotPool.Status != coreauth.StatusDisabled {
		t.Fatalf("expected pool auth disabled, got disabled=%v status=%s", gotPool.Disabled, gotPool.Status)
	}
	gotLegacy, ok := service.coreManager.GetByID(legacyAuth.ID)
	if !ok || gotLegacy == nil {
		t.Fatalf("expected legacy auth to remain")
	}
	if gotLegacy.Disabled || gotLegacy.Status == coreauth.StatusDisabled {
		t.Fatalf("legacy auth should not be disabled, got disabled=%v status=%s", gotLegacy.Disabled, gotLegacy.Status)
	}
}
