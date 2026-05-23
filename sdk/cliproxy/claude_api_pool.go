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
			s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionDelete, ID: auth.ID})
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
			s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: watcher.AuthUpdateActionDelete, ID: auth.ID})
		}
	}
	for _, auth := range next {
		action := watcher.AuthUpdateActionAdd
		if _, ok := s.coreManager.GetByID(auth.ID); ok {
			action = watcher.AuthUpdateActionModify
		}
		s.emitAuthUpdate(ctx, watcher.AuthUpdate{Action: action, ID: auth.ID, Auth: auth})
	}
	return nil
}
