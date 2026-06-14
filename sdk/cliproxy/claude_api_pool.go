package cliproxy

import (
	"context"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher/synthesizer"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// SyncClaudeAPIPoolAuths reloads the Claude API pool store and applies the resulting runtime auths.
func (s *Service) SyncClaudeAPIPoolAuths(ctx context.Context) error {
	if s == nil || s.coreManager == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.cfgMu.RLock()
	cfg := s.cfg
	configPath := s.configPath
	s.cfgMu.RUnlock()
	if cfg == nil || !cfg.ClaudeAPIPool.Enabled {
		for _, auth := range s.coreManager.List() {
			if auth == nil || !claudeapipool.IsAttributesPoolAuth(auth.Attributes) {
				continue
			}
			disabled := auth.Clone()
			disabled.Disabled = true
			disabled.Status = coreauth.StatusDisabled
			disabled.Unavailable = false
			disabled.NextRetryAfter = time.Time{}
			disabled.ModelStates = nil
			claudeapipool.DebugLogf("claude api pool sync disable auth=%s", claudeapipool.DebugAuthRef(auth.ID))
			claudeapipool.UnregisterAuthDebugLabel(auth.ID)
			s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionModify, ID: disabled.ID, Auth: disabled})
		}
		return nil
	}
	doc, err := claudeapipool.LoadStore(configPath, cfg)
	if err != nil {
		return err
	}
	claudeapipool.SetVirtualCacheConfig(claudeapipool.EffectiveVirtualCache(doc.VirtualCache))
	claudeapipool.SetRoutingConfig(claudeapipool.EffectiveRouting(doc.Routing))
	sctx := &synthesizer.SynthesisContext{
		Config:      cfg,
		ConfigPath:  configPath,
		Now:         time.Now(),
		IDGenerator: synthesizer.NewStableIDGenerator(),
	}
	items := claudeapipool.Resolve(doc)
	next := make(map[string]*coreauth.Auth, len(items))
	for i := range items {
		auth := synthesizer.SynthesizeClaudeAPIPoolAuth(sctx, items[i])
		if auth == nil {
			continue
		}
		next[auth.ID] = auth
	}
	for _, auth := range s.coreManager.List() {
		if auth == nil || !claudeapipool.IsAttributesPoolAuth(auth.Attributes) {
			continue
		}
		if _, ok := next[auth.ID]; !ok {
			claudeapipool.DebugLogf("claude api pool sync delete stale auth=%s", claudeapipool.DebugAuthRef(auth.ID))
			claudeapipool.UnregisterAuthDebugLabel(auth.ID)
			s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionDelete, ID: auth.ID})
		}
	}
	for _, auth := range next {
		action := watcher.AuthUpdateActionAdd
		if _, ok := s.coreManager.GetByID(auth.ID); ok {
			action = watcher.AuthUpdateActionModify
		}
		claudeapipool.DebugLogf(
			"claude api pool sync auth action=%s auth=%s status=%s disabled=%t",
			action,
			claudeapipool.DebugAuthRef(auth.ID),
			auth.Status,
			auth.Disabled,
		)
		s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: action, ID: auth.ID, Auth: auth})
	}
	vc := claudeapipool.CurrentVirtualCacheConfig()
	rt := claudeapipool.CurrentRoutingConfig()
	claudeapipool.DebugLogf(
		"claude api pool sync complete items=%d runtime_auths=%d virtual_cache_enabled=%t virtual_cache_mode=%s hit_rate=%.3f target_reuse=%.3f uncached=%d max_creation=%d routing_rpm=%d routing_concurrency=%d max_switches=%d switch_delay_ms=%d affinity_enabled=%t affinity_auto=%t affinity_lanes=%d affinity_max_lanes=%d",
		len(items),
		len(next),
		vc.Enabled,
		vc.Mode,
		vc.HitRate,
		vc.TargetCacheReuseRatio,
		vc.UncachedInputTokens,
		vc.MaxCreationTokens,
		rt.PerAccountRPM,
		rt.PerAccountConcurrency,
		rt.MaxSwitches,
		rt.SwitchDelayMS,
		rt.CacheAffinityEnabled,
		rt.CacheAffinityAuto,
		rt.CacheAffinityLanes,
		rt.CacheAffinityMaxLanes,
	)
	return nil
}
