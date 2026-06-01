package synthesizer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher/diff"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ClaudeAPIPoolSynthesizer generates Auth entries from the Claude API pool store.
type ClaudeAPIPoolSynthesizer struct{}

// NewClaudeAPIPoolSynthesizer creates a new ClaudeAPIPoolSynthesizer instance.
func NewClaudeAPIPoolSynthesizer() *ClaudeAPIPoolSynthesizer {
	return &ClaudeAPIPoolSynthesizer{}
}

// Synthesize generates Claude API pool auth entries when the pool is enabled.
func (s *ClaudeAPIPoolSynthesizer) Synthesize(ctx *SynthesisContext) ([]*coreauth.Auth, error) {
	out := make([]*coreauth.Auth, 0)
	if ctx == nil || ctx.Config == nil || !ctx.Config.ClaudeAPIPool.Enabled {
		return out, nil
	}
	doc, err := claudeapipool.LoadStore(ctx.ConfigPath, ctx.Config)
	if err != nil {
		return out, err
	}
	claudeapipool.SetVirtualCacheConfig(claudeapipool.EffectiveVirtualCache(doc.VirtualCache))
	claudeapipool.SetRoutingConfig(claudeapipool.EffectiveRouting(doc.Routing))
	items := claudeapipool.Resolve(doc)
	out = make([]*coreauth.Auth, 0, len(items))
	for i := range items {
		item := items[i]
		auth := SynthesizeClaudeAPIPoolAuth(ctx, item)
		if auth == nil {
			continue
		}
		out = append(out, auth)
	}
	claudeapipool.DebugLogf("claude api pool synthesize complete items=%d runtime_auths=%d", len(items), len(out))
	return out, nil
}

// SynthesizeClaudeAPIPoolAuth builds one runtime auth from a resolved pool item.
func SynthesizeClaudeAPIPoolAuth(ctx *SynthesisContext, item claudeapipool.ResolvedItem) *coreauth.Auth {
	if ctx == nil || ctx.IDGenerator == nil {
		return nil
	}
	ck := item.Config
	key := strings.TrimSpace(ck.APIKey)
	if key == "" {
		return nil
	}
	base := strings.TrimSpace(ck.BaseURL)
	id, token := ctx.IDGenerator.Next("claude-api-pool:apikey", key, base)
	claudeapipool.RegisterAuthDebugLabel(id, fmt.Sprintf("#%d", item.Position))
	attrs := map[string]string{
		"source":                   fmt.Sprintf("config:claude-api-pool[#%d:%s]", item.Position, token),
		"api_key":                  key,
		claudeapipool.AttrPool:     "true",
		claudeapipool.AttrPosition: strconv.Itoa(item.Position),
		claudeapipool.AttrItemHash: item.ItemHash,
	}
	if ck.Priority != 0 {
		attrs["priority"] = strconv.Itoa(ck.Priority)
	}
	if base != "" {
		attrs["base_url"] = base
	}
	if hash := diff.ComputeClaudeModelsHash(ck.Models); hash != "" {
		attrs["models_hash"] = hash
	}
	if modelsJSON := claudeapipool.ModelsToAttribute(ck.Models); modelsJSON != "" {
		attrs[claudeapipool.AttrModelsJSON] = modelsJSON
	}
	if ck.ExperimentalCCHSigning {
		attrs[claudeapipool.AttrCCHSigning] = "true"
	}
	if item.PureMode {
		attrs[claudeapipool.AttrPureMode] = "true"
	}
	addConfigHeadersToAttrs(ck.Headers, attrs)
	metadata := map[string]any{}
	if ck.DisableCooling {
		metadata["disable_cooling"] = true
	}
	status := coreauth.StatusActive
	disabled := item.Raw.Disabled
	if disabled {
		status = coreauth.StatusDisabled
	}
	auth := &coreauth.Auth{
		ID:         id,
		Provider:   "claude",
		Label:      "claude-api-pool",
		Status:     status,
		Disabled:   disabled,
		ProxyURL:   strings.TrimSpace(ck.ProxyURL),
		Attributes: attrs,
		Metadata:   metadata,
		CreatedAt:  ctx.Now,
		UpdatedAt:  ctx.Now,
	}
	ApplyAuthExcludedModelsMeta(auth, ctx.Config, ck.ExcludedModels, "apikey")
	if len(auth.Metadata) == 0 {
		auth.Metadata = nil
	}
	return auth
}
