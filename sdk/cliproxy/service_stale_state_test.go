package cliproxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
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
		SaveCooldownStatus:     true,
	}

	forceHomeRuntimeConfig(cfg)

	if !cfg.UsageStatisticsEnabled {
		t.Fatal("expected home runtime config to force usage statistics enabled")
	}
	if cfg.SaveCooldownStatus {
		t.Fatal("expected home runtime config to force cooldown status persistence disabled")
	}
}

func TestApplyHomeOverlayForcesUsageStatisticsEnabled(t *testing.T) {
	baseCfg := &config.Config{}
	baseCfg.Home.Enabled = true
	service := &Service{cfg: baseCfg}

	service.applyHomeOverlay(&config.Config{
		UsageStatisticsEnabled: false,
		SaveCooldownStatus:     true,
	})

	if service.cfg == nil || !service.cfg.UsageStatisticsEnabled {
		t.Fatal("expected home overlay to force usage statistics enabled")
	}
	if !service.cfg.Home.Enabled {
		t.Fatal("expected home overlay to preserve local home settings")
	}
	if service.cfg.SaveCooldownStatus {
		t.Fatal("expected home overlay to force cooldown status persistence disabled")
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

func TestSyncResourcePoolAuthsUpdatesCleanInputRuntimeAttributes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initPath := filepath.Join(dir, "resource-pools.yaml")
	if err := os.WriteFile(initPath, []byte("database-path: resource-pools.db\n"), 0o600); err != nil {
		t.Fatalf("write resource-pools.yaml: %v", err)
	}
	cfg := &internalconfig.Config{
		ResourcePools: internalconfig.ResourcePoolsConfig{
			Enabled:    true,
			ConfigFile: "resource-pools.yaml",
		},
	}
	service := &Service{
		cfg:         cfg,
		configPath:  configPath,
		coreManager: coreauth.NewManager(nil, nil, nil),
	}
	ctx := context.Background()
	store, err := resourcepool.Open(configPath, cfg)
	if err != nil {
		t.Fatalf("open resource pool store: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	auth := &coreauth.Auth{
		ID:       "claude-user@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{
			"type":         "claude",
			"email":        "user@example.com",
			"access_token": "access-token",
		},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "user@example.com", "", auth); err != nil {
		t.Fatalf("register account: %v", err)
	}
	cleanDisabled := false
	if _, err := store.SaveClaudeCodePoolConfig(ctx, resourcepool.ClaudeCodePoolConfig{
		Usage: resourcepool.ClaudeCodeUsageConfig{CleanInputTokens: &cleanDisabled},
	}); err != nil {
		t.Fatalf("save disabled config: %v", err)
	}
	if err := service.SyncResourcePoolAuths(ctx); err != nil {
		t.Fatalf("initial SyncResourcePoolAuths() error = %v", err)
	}
	runtimeAuth, ok := service.coreManager.GetByID(auth.ID)
	if !ok || runtimeAuth == nil {
		t.Fatalf("runtime auth %q not found", auth.ID)
	}
	if got := runtimeAuth.Attributes[resourcepool.AttrCleanInputTokens]; got != "false" {
		t.Fatalf("initial clean input attr = %q, want false", got)
	}

	cleanEnabled := true
	if _, err := store.SaveClaudeCodePoolConfig(ctx, resourcepool.ClaudeCodePoolConfig{
		Usage: resourcepool.ClaudeCodeUsageConfig{
			CleanInputTokens:           &cleanEnabled,
			SystemPromptOverheadTokens: 1909,
		},
	}); err != nil {
		t.Fatalf("save enabled config: %v", err)
	}
	if err := service.SyncResourcePoolAuths(ctx); err != nil {
		t.Fatalf("second SyncResourcePoolAuths() error = %v", err)
	}
	runtimeAuth, ok = service.coreManager.GetByID(auth.ID)
	if !ok || runtimeAuth == nil {
		t.Fatalf("runtime auth %q not found after sync", auth.ID)
	}
	if got := runtimeAuth.Attributes[resourcepool.AttrCleanInputTokens]; got != "true" {
		t.Fatalf("clean input attr = %q, want true", got)
	}
	if got := runtimeAuth.Attributes[resourcepool.AttrCleanInputDefaultOverhead]; got != "1909" {
		t.Fatalf("default overhead attr = %q, want 1909", got)
	}
	if got := runtimeAuth.Attributes[resourcepool.AttrProfileFingerprint]; got == "" {
		t.Fatal("profile fingerprint attr is empty")
	}
}
