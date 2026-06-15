package synthesizer

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ResourcePoolSynthesizer generates Auth entries from resource-pools.db.
type ResourcePoolSynthesizer struct{}

// NewResourcePoolSynthesizer creates a new ResourcePoolSynthesizer instance.
func NewResourcePoolSynthesizer() *ResourcePoolSynthesizer {
	return &ResourcePoolSynthesizer{}
}

// Synthesize generates SQLite-backed Claude Code account-pool auth entries.
func (s *ResourcePoolSynthesizer) Synthesize(ctx *SynthesisContext) ([]*coreauth.Auth, error) {
	if ctx == nil {
		return nil, nil
	}
	return resourcepool.ListStoredAuths(context.Background(), ctx.ConfigPath, ctx.Config)
}
