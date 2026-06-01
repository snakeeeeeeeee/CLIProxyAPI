package claudeapipool

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestResolvePathUsesFixedFileBesideConfig(t *testing.T) {
	configPath := filepath.Join("workspace", "config.yaml")
	got := ResolvePath(configPath, &config.Config{})
	want := filepath.Join("workspace", DefaultFileName)
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestResolveInheritsTopLevelDefaultsAndModels(t *testing.T) {
	doc, err := Decode([]byte(`
version: 1
defaults:
  base-url: "https://api.example.test"
  proxy-url: "http://proxy.example.test"
  priority: 7
  disable-cooling: true
  headers:
    anthropic-version: "2023-06-01"
    x-shared: "default"
models:
  - name: "claude-opus"
    alias: "opus"
pure-mode: true
items:
  - api-key: " key-1 "
    headers:
      x-shared: "item"
      x-item: "yes"
`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	items := Resolve(doc)
	if len(items) != 1 {
		t.Fatalf("Resolve() len = %d, want 1", len(items))
	}
	got := items[0]
	if got.Position != 1 {
		t.Fatalf("Position = %d, want 1", got.Position)
	}
	if !got.PureMode {
		t.Fatal("PureMode = false, want true")
	}
	if got.Config.APIKey != "key-1" {
		t.Fatalf("APIKey = %q, want trimmed key", got.Config.APIKey)
	}
	if got.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("BaseURL = %q", got.Config.BaseURL)
	}
	if got.Config.ProxyURL != "http://proxy.example.test" {
		t.Fatalf("ProxyURL = %q", got.Config.ProxyURL)
	}
	if got.Config.Priority != 7 {
		t.Fatalf("Priority = %d, want 7", got.Config.Priority)
	}
	if !got.Config.DisableCooling {
		t.Fatal("DisableCooling = false, want true")
	}
	if len(got.Config.Models) != 1 || got.Config.Models[0].Name != "claude-opus" || got.Config.Models[0].Alias != "opus" {
		t.Fatalf("Models = %#v", got.Config.Models)
	}
	if got.Config.Headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("default header missing: %#v", got.Config.Headers)
	}
	if got.Config.Headers["x-shared"] != "item" {
		t.Fatalf("item header should override default, got %#v", got.Config.Headers)
	}
	if got.Config.Headers["x-item"] != "yes" {
		t.Fatalf("item header missing: %#v", got.Config.Headers)
	}
}

func TestEffectiveVirtualCacheDefaultsAndNormalization(t *testing.T) {
	got := EffectiveVirtualCache(VirtualCacheConfig{})
	if !got.Enabled || got.HitRate != 0.9 {
		t.Fatalf("default virtual cache = %#v, want enabled with 0.9 hit rate", got)
	}
	if got.Mode != VirtualCacheModeNatural {
		t.Fatalf("Mode = %q, want %q", got.Mode, VirtualCacheModeNatural)
	}

	enabled := true
	hitRate := 95.0
	got = EffectiveVirtualCache(VirtualCacheConfig{
		Enabled:               &enabled,
		Mode:                  VirtualCacheModeForced,
		HitRate:               &hitRate,
		TargetCacheReuseRatio: &hitRate,
		MinCacheTokens:        -1,
		MaxCacheTokens:        200,
		UncachedInputTokens:   8,
	})
	if got.HitRate != 0.95 {
		t.Fatalf("HitRate = %v, want 0.95", got.HitRate)
	}
	if got.TargetCacheReuseRatio != 0.95 {
		t.Fatalf("TargetCacheReuseRatio = %v, want 0.95", got.TargetCacheReuseRatio)
	}
	if got.Mode != VirtualCacheModeForced {
		t.Fatalf("Mode = %q, want %q", got.Mode, VirtualCacheModeForced)
	}
	if got.MinCacheTokens != 0 || got.MaxCacheTokens != 200 || got.UncachedInputTokens != 8 {
		t.Fatalf("normalized tokens = %#v", got)
	}
	if got.ContextShrinkResetRatio != 0.7 {
		t.Fatalf("ContextShrinkResetRatio = %v, want 0.7", got.ContextShrinkResetRatio)
	}
}

func TestEffectiveRoutingDefaultsAndNormalization(t *testing.T) {
	got := EffectiveRouting(RoutingConfig{})
	if got.RateLimitCooldownMS != 1000 {
		t.Fatalf("RateLimitCooldownMS = %d, want 1000", got.RateLimitCooldownMS)
	}
	if got.RateLimitMaxCooldownMS != 30*60*1000 {
		t.Fatalf("RateLimitMaxCooldownMS = %d, want 1800000", got.RateLimitMaxCooldownMS)
	}
	if got.OverloadCooldownMS != 10000 || got.OverloadMaxCooldownMS != 60000 {
		t.Fatalf("overload cooldown defaults = %#v", got)
	}
	if got.SameAccountRetryDelayMS != 1500 {
		t.Fatalf("SameAccountRetryDelayMS = %d, want 1500", got.SameAccountRetryDelayMS)
	}

	got = EffectiveRouting(RoutingConfig{
		PerAccountRPM:           -1,
		PerAccountConcurrency:   2,
		MaxSwitches:             3,
		RateLimitCooldownMS:     5000,
		RateLimitMaxCooldownMS:  1000,
		OverloadCooldownMS:      2500,
		OverloadMaxCooldownMS:   1000,
		SameAccountRetry429:     1,
		SameAccountRetry529:     2,
		SameAccountRetryDelayMS: -1,
	})
	if got.PerAccountRPM != 0 || got.PerAccountConcurrency != 2 || got.MaxSwitches != 3 {
		t.Fatalf("normalized routing basics = %#v", got)
	}
	if got.RateLimitMaxCooldownMS != got.RateLimitCooldownMS {
		t.Fatalf("rate max cooldown should be raised to base: %#v", got)
	}
	if got.OverloadMaxCooldownMS != got.OverloadCooldownMS {
		t.Fatalf("overload max cooldown should be raised to base: %#v", got)
	}
	if got.SameAccountRetry429 != 1 || got.SameAccountRetry529 != 2 || got.SameAccountRetryDelayMS != 1500 {
		t.Fatalf("same account retry = %#v", got)
	}

	got = EffectiveRouting(RoutingConfig{
		CacheAffinityEnabled:   true,
		CacheAffinityAuto:      true,
		CacheAffinityMinTokens: -1,
		CacheAffinityLanes:     8,
		CacheAffinityMaxLanes:  2,
		CacheAffinityWaitMS:    -1,
		CacheAffinityTTLMS:     -1,
	})
	if !got.CacheAffinityEnabled || !got.CacheAffinityAuto {
		t.Fatalf("cache affinity flags = %#v", got)
	}
	if got.CacheAffinityMinTokens != 4096 {
		t.Fatalf("CacheAffinityMinTokens = %d, want 4096", got.CacheAffinityMinTokens)
	}
	if got.CacheAffinityLanes != 8 || got.CacheAffinityMaxLanes != 8 {
		t.Fatalf("cache affinity lanes = %#v, want max raised to lanes", got)
	}
	if got.CacheAffinityWaitMS != 250 || got.CacheAffinityTTLMS != 300000 {
		t.Fatalf("cache affinity timing defaults = %#v", got)
	}
}

func TestVirtualCacheConfigFromEffective(t *testing.T) {
	got := VirtualCacheConfigFromEffective(EffectiveVirtualCacheConfig{
		Enabled:                 false,
		Mode:                    VirtualCacheModeForced,
		HitRate:                 0.85,
		TargetCacheReuseRatio:   0.9,
		MinCacheTokens:          100,
		MaxCacheTokens:          1000,
		UncachedInputTokens:     20,
		ContextShrinkResetRatio: 0.65,
		MinCreationTokens:       128,
		MaxCreationTokens:       1200,
	})
	if got.Enabled == nil || *got.Enabled {
		t.Fatalf("Enabled = %#v, want false pointer", got.Enabled)
	}
	if got.Mode != VirtualCacheModeForced {
		t.Fatalf("Mode = %q, want %q", got.Mode, VirtualCacheModeForced)
	}
	if got.HitRate == nil || *got.HitRate != 0.85 {
		t.Fatalf("HitRate = %#v, want 0.85 pointer", got.HitRate)
	}
	if got.TargetCacheReuseRatio == nil || *got.TargetCacheReuseRatio != 0.9 {
		t.Fatalf("TargetCacheReuseRatio = %#v, want 0.9 pointer", got.TargetCacheReuseRatio)
	}
	if got.MinCacheTokens != 100 || got.MaxCacheTokens != 1000 || got.UncachedInputTokens != 20 {
		t.Fatalf("token config = %#v", got)
	}
	if got.ContextShrinkResetRatio == nil || *got.ContextShrinkResetRatio != 0.65 {
		t.Fatalf("ContextShrinkResetRatio = %#v, want 0.65 pointer", got.ContextShrinkResetRatio)
	}
	if got.MinCreationTokens != 128 || got.MaxCreationTokens != 1200 {
		t.Fatalf("creation token config = %#v", got)
	}
}

func TestResolveItemOverridesModelsAndScalars(t *testing.T) {
	baseURL := "https://item.example.test"
	proxyURL := ""
	priority := 0
	disableCooling := false
	doc := &File{
		Version: 1,
		Defaults: Defaults{
			BaseURL:        "https://default.example.test",
			ProxyURL:       "http://proxy.example.test",
			Priority:       9,
			DisableCooling: true,
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items: []Item{
			{
				APIKey:         "key-1",
				BaseURL:        &baseURL,
				ProxyURL:       &proxyURL,
				Priority:       &priority,
				DisableCooling: &disableCooling,
				Models:         []config.ClaudeModel{{Name: "claude-item", Alias: "item"}},
			},
		},
	}

	got := Resolve(doc)[0]
	if got.Config.BaseURL != baseURL {
		t.Fatalf("BaseURL = %q, want %q", got.Config.BaseURL, baseURL)
	}
	if got.Config.ProxyURL != "" {
		t.Fatalf("ProxyURL = %q, want empty override", got.Config.ProxyURL)
	}
	if got.Config.Priority != 0 {
		t.Fatalf("Priority = %d, want zero override", got.Config.Priority)
	}
	if got.Config.DisableCooling {
		t.Fatal("DisableCooling = true, want false override")
	}
	if len(got.Config.Models) != 1 || got.Config.Models[0].Name != "claude-item" {
		t.Fatalf("Models = %#v, want item override", got.Config.Models)
	}
}

func TestPositionHashGuardsMutations(t *testing.T) {
	doc := &File{
		Version: 1,
		Items: []Item{
			{APIKey: "key-1"},
			{APIKey: "key-2"},
		},
	}
	hash := ItemHash(doc.Items[1])

	if _, err := ReplaceItem(doc, 2, "stale", Item{APIKey: "new"}); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("ReplaceItem stale error = %v, want hash mismatch", err)
	}
	resolved, err := ReplaceItem(doc, 2, hash, Item{APIKey: "new"})
	if err != nil {
		t.Fatalf("ReplaceItem() error = %v", err)
	}
	if resolved.Position != 2 || resolved.Config.APIKey != "new" {
		t.Fatalf("ReplaceItem() resolved = %#v", resolved)
	}
	if got := Resolve(doc); len(got) != 2 || got[1].Config.APIKey != "new" {
		t.Fatalf("doc after replace = %#v", got)
	}

	hash = ItemHash(doc.Items[0])
	if err := DeleteItem(doc, 1, hash); err != nil {
		t.Fatalf("DeleteItem() error = %v", err)
	}
	got := Resolve(doc)
	if len(got) != 1 || got[0].Position != 1 || got[0].Config.APIKey != "new" {
		t.Fatalf("doc after delete = %#v", got)
	}
}

func TestSetDisabledBatchGuardsHashes(t *testing.T) {
	doc := &File{
		Version: 1,
		Items: []Item{
			{APIKey: "key-1"},
			{APIKey: "key-2"},
			{APIKey: "key-3"},
		},
	}
	refs := []MutationRef{
		{Position: 1, ItemHash: ItemHash(doc.Items[0])},
		{Position: 3, ItemHash: ItemHash(doc.Items[2])},
	}
	resolved, err := SetDisabledBatch(doc, refs, true)
	if err != nil {
		t.Fatalf("SetDisabledBatch() error = %v", err)
	}
	if len(resolved) != 2 || resolved[0].Position != 1 || resolved[1].Position != 3 {
		t.Fatalf("resolved = %#v", resolved)
	}
	got := Resolve(doc)
	if got[0].Status != StatusDisabled || got[1].Status != StatusEnabled || got[2].Status != StatusDisabled {
		t.Fatalf("statuses after disable = %#v", got)
	}
	if _, err := SetDisabledBatch(doc, []MutationRef{{Position: 1, ItemHash: "stale"}}, false); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("SetDisabledBatch stale error = %v, want hash mismatch", err)
	}
}

func TestListPaginationAndFilters(t *testing.T) {
	doc := &File{
		Version: 1,
		Models:  []config.ClaudeModel{{Name: "shared-model"}},
		Items: []Item{
			{APIKey: "key-1", Headers: map[string]string{"x-team": "alpha"}},
			{APIKey: "key-2", Models: []config.ClaudeModel{{Name: "special-model"}}},
			{APIKey: "key-3", Disabled: true},
		},
	}

	page := List(doc, ListQuery{Page: 2, PageSize: 1})
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].Position != 2 {
		t.Fatalf("page = %#v", page)
	}

	byModel := List(doc, ListQuery{Page: 1, PageSize: 50, Model: "special-model"})
	if byModel.Total != 1 || byModel.Items[0].Position != 2 {
		t.Fatalf("model filter = %#v", byModel)
	}

	byStatus := List(doc, ListQuery{Page: 1, PageSize: 50, Status: StatusDisabled})
	if byStatus.Total != 1 || byStatus.Items[0].Position != 3 {
		t.Fatalf("status filter = %#v", byStatus)
	}

	bySearch := List(doc, ListQuery{Page: 1, PageSize: 50, Q: "alpha"})
	if bySearch.Total != 1 || bySearch.Items[0].Position != 1 {
		t.Fatalf("search filter = %#v", bySearch)
	}
}

func TestDecodeImportAcceptsPoolDocumentAndItemArray(t *testing.T) {
	items, err := DecodeImport([]byte(`
version: 1
items:
  - api-key: key-1
  - api-key: key-2
`))
	if err != nil {
		t.Fatalf("DecodeImport(pool doc) error = %v", err)
	}
	if len(items) != 2 || items[0].APIKey != "key-1" || items[1].APIKey != "key-2" {
		t.Fatalf("DecodeImport(pool doc) = %#v", items)
	}

	items, err = DecodeImport([]byte(`[{"api-key":"json-key"}]`))
	if err != nil {
		t.Fatalf("DecodeImport(array) error = %v", err)
	}
	if len(items) != 1 || items[0].APIKey != "json-key" {
		t.Fatalf("DecodeImport(array) = %#v", items)
	}
}

func TestDecodeImportAcceptsSimpleAPIKeyWorkspaceLines(t *testing.T) {
	doc, err := DecodeImportFile([]byte(`
# apiKey-----workspaceId
key-1-----workspace-a
key-2 ----- workspace-b
`))
	if err != nil {
		t.Fatalf("DecodeImportFile(simple lines) error = %v", err)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(doc.Items))
	}
	if doc.Items[0].APIKey != "key-1" || doc.Items[0].Headers[SimpleImportWorkspaceHeader] != "workspace-a" {
		t.Fatalf("first item = %#v", doc.Items[0])
	}
	if doc.Items[1].APIKey != "key-2" || doc.Items[1].Headers[SimpleImportWorkspaceHeader] != "workspace-b" {
		t.Fatalf("second item = %#v", doc.Items[1])
	}
	if doc.Items[0].Headers["anthropic-workspace-id"] != "workspace-a" {
		t.Fatalf("workspace header key = %#v", doc.Items[0].Headers)
	}
}

func TestApplyImportReplacePreservesImportedDefaultsAndModels(t *testing.T) {
	doc := &File{
		Version: 1,
		Items:   []Item{{APIKey: "old-key"}},
	}
	count, err := ApplyImport(doc, []byte(`
version: 1
defaults:
  base-url: "https://api.example.test"
  headers:
    anthropic-version: "2023-06-01"
models:
  - name: "claude-opus"
items:
  - api-key: "new-key"
`), true)
	if err != nil {
		t.Fatalf("ApplyImport(replace) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ApplyImport count = %d, want 1", count)
	}
	if doc.Defaults.BaseURL != "https://api.example.test" {
		t.Fatalf("Defaults.BaseURL = %q", doc.Defaults.BaseURL)
	}
	if len(doc.Models) != 1 || doc.Models[0].Name != "claude-opus" {
		t.Fatalf("Models = %#v", doc.Models)
	}
	resolved := Resolve(doc)
	if len(resolved) != 1 || resolved[0].Config.BaseURL != "https://api.example.test" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestApplyImportAppendMaterializesImportedDefaultsAndModels(t *testing.T) {
	doc := &File{
		Version: 1,
		Defaults: Defaults{
			BaseURL: "https://existing.example.test",
		},
		Items: []Item{{APIKey: "old-key"}},
	}
	count, err := ApplyImport(doc, []byte(`
version: 1
defaults:
  base-url: "https://import.example.test"
  headers:
    x-import: "yes"
models:
  - name: "claude-import"
items:
  - api-key: "new-key"
`), false)
	if err != nil {
		t.Fatalf("ApplyImport(append) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ApplyImport count = %d, want 1", count)
	}
	if doc.Defaults.BaseURL != "https://existing.example.test" {
		t.Fatalf("existing defaults changed: %#v", doc.Defaults)
	}
	resolved := Resolve(doc)
	if len(resolved) != 2 {
		t.Fatalf("resolved len = %d", len(resolved))
	}
	if resolved[1].Config.BaseURL != "https://import.example.test" {
		t.Fatalf("imported BaseURL = %q", resolved[1].Config.BaseURL)
	}
	if resolved[1].Config.Headers["x-import"] != "yes" {
		t.Fatalf("imported headers = %#v", resolved[1].Config.Headers)
	}
	if len(resolved[1].Config.Models) != 1 || resolved[1].Config.Models[0].Name != "claude-import" {
		t.Fatalf("imported models = %#v", resolved[1].Config.Models)
	}
}

func TestApplyImportAppendSimpleLinesUsesExistingDefaults(t *testing.T) {
	doc := &File{
		Version: 1,
		Defaults: Defaults{
			BaseURL: "https://existing.example.test",
			Headers: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
	}
	count, err := ApplyImport(doc, []byte("new-key-----workspace-a\n"), false)
	if err != nil {
		t.Fatalf("ApplyImport(simple append) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ApplyImport count = %d, want 1", count)
	}
	resolved := Resolve(doc)
	if len(resolved) != 1 {
		t.Fatalf("resolved len = %d, want 1", len(resolved))
	}
	if resolved[0].Config.BaseURL != "https://existing.example.test" {
		t.Fatalf("BaseURL = %q", resolved[0].Config.BaseURL)
	}
	if resolved[0].Config.Headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("default headers missing: %#v", resolved[0].Config.Headers)
	}
	if resolved[0].Config.Headers[SimpleImportWorkspaceHeader] != "workspace-a" {
		t.Fatalf("workspace header missing: %#v", resolved[0].Config.Headers)
	}
	if len(resolved[0].Config.Models) != 1 || resolved[0].Config.Models[0].Name != "claude-default" {
		t.Fatalf("models = %#v", resolved[0].Config.Models)
	}
}

func TestApplyImportReplaceSimpleLinesPreservesExistingPoolConfig(t *testing.T) {
	doc := &File{
		Version: 1,
		Defaults: Defaults{
			BaseURL: "https://existing.example.test",
			Headers: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items:  []Item{{APIKey: "old-key"}},
	}
	count, err := ApplyImport(doc, []byte("new-key-----workspace-a\n"), true)
	if err != nil {
		t.Fatalf("ApplyImport(simple replace) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ApplyImport count = %d, want 1", count)
	}
	if len(doc.Items) != 1 || doc.Items[0].APIKey != "new-key" {
		t.Fatalf("items = %#v", doc.Items)
	}
	if doc.Defaults.BaseURL != "https://existing.example.test" {
		t.Fatalf("Defaults.BaseURL = %q", doc.Defaults.BaseURL)
	}
	if len(doc.Models) != 1 || doc.Models[0].Name != "claude-default" {
		t.Fatalf("Models = %#v", doc.Models)
	}
	resolved := Resolve(doc)
	if resolved[0].Config.Headers[SimpleImportWorkspaceHeader] != "workspace-a" ||
		resolved[0].Config.Headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("headers = %#v", resolved[0].Config.Headers)
	}
}
