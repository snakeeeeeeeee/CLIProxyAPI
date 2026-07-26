package resourcepool

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestApplyAuthOverlayEnforcesStrictAccountProxyMode(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	cfg := &config.Config{
		SDKConfig:     config.SDKConfig{ProxyURL: "http://global-proxy.invalid:8080"},
		ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"},
	}

	unboundStored := &coreauth.Auth{
		ID:       "claude-unbound@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{"email": "unbound@example.com", "access_token": "access"},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, unboundStored.ID, "unbound@example.com", "", unboundStored); err != nil {
		t.Fatalf("register unbound account: %v", err)
	}
	unboundRuntime := &coreauth.Auth{
		ID:       unboundStored.ID,
		Provider: "claude",
		ProxyURL: cfg.ProxyURL,
		Metadata: map[string]any{"email": "unbound@example.com"},
	}
	if err := ApplyAuthOverlay(ctx, store.initPath, cfg, unboundRuntime); err != nil {
		t.Fatalf("ApplyAuthOverlay(unbound) error = %v", err)
	}
	if unboundRuntime.ProxyURL != "direct" {
		t.Fatalf("unbound ProxyURL = %q, want direct", unboundRuntime.ProxyURL)
	}
	if unboundRuntime.Attributes["proxy_resource_bound"] != "false" {
		t.Fatalf("unbound attributes = %+v", unboundRuntime.Attributes)
	}

	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	boundStored := &coreauth.Auth{
		ID:       "claude-bound@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{"email": "bound@example.com", "access_token": "access"},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, boundStored.ID, "bound@example.com", proxy.ID, boundStored); err != nil {
		t.Fatalf("register bound account: %v", err)
	}
	boundRuntime := &coreauth.Auth{
		ID:       boundStored.ID,
		Provider: "claude",
		Metadata: map[string]any{"email": "bound@example.com"},
	}
	if err := ApplyAuthOverlay(ctx, store.initPath, cfg, boundRuntime); err != nil {
		t.Fatalf("ApplyAuthOverlay(bound) error = %v", err)
	}
	if boundRuntime.ProxyURL != proxy.ProxyURL || boundRuntime.Attributes["proxy_resource_bound"] != "true" {
		t.Fatalf("bound runtime = %+v", boundRuntime)
	}

	disabled := false
	if _, err := store.UpdateProxy(ctx, proxy.ID, ProxyPatch{Enabled: &disabled}); err != nil {
		t.Fatalf("disable proxy: %v", err)
	}
	disabledRuntime := &coreauth.Auth{
		ID:       boundStored.ID,
		Provider: "claude",
		Metadata: map[string]any{"email": "bound@example.com"},
	}
	if err := ApplyAuthOverlay(ctx, store.initPath, cfg, disabledRuntime); err != nil {
		t.Fatalf("ApplyAuthOverlay(disabled proxy) error = %v", err)
	}
	if disabledRuntime.ProxyURL != "invalid://bound-proxy" {
		t.Fatalf("disabled bound ProxyURL = %q, want fail-closed marker", disabledRuntime.ProxyURL)
	}
	if disabledRuntime.Attributes["proxy_resource_enabled"] != "false" {
		t.Fatalf("disabled bound attributes = %+v", disabledRuntime.Attributes)
	}
}

func TestListStoredAuthsRejectsUnavailableBoundProxy(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{ProxyURL: "http://127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	auth := &coreauth.Auth{
		ID:       "claude-disabled-proxy@example.com.json",
		Provider: "claude",
		Metadata: map[string]any{"email": "disabled-proxy@example.com", "access_token": "access"},
	}
	if _, err := store.RegisterClaudeCodeAccountWithAuth(ctx, auth.ID, "disabled-proxy@example.com", proxy.ID, auth); err != nil {
		t.Fatalf("register bound account: %v", err)
	}
	disabled := false
	if _, err := store.UpdateProxy(ctx, proxy.ID, ProxyPatch{Enabled: &disabled}); err != nil {
		t.Fatalf("disable proxy: %v", err)
	}

	auths, err := ListStoredAuths(ctx, store.initPath, &config.Config{
		SDKConfig:     config.SDKConfig{ProxyURL: "http://global-proxy.invalid:8080"},
		ResourcePools: config.ResourcePoolsConfig{Enabled: true, ConfigFile: "resource-pools.yaml"},
	})
	if err != nil {
		t.Fatalf("ListStoredAuths() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("stored auth count = %d, want 1", len(auths))
	}
	if auths[0].ProxyURL != "invalid://bound-proxy" {
		t.Fatalf("stored auth ProxyURL = %q, want fail-closed marker", auths[0].ProxyURL)
	}
}

func TestStrictAccountProxyURL(t *testing.T) {
	for _, test := range []struct {
		name    string
		bound   bool
		present bool
		enabled bool
		health  string
		rawURL  string
		want    string
	}{
		{name: "unbound ignores inherited proxy", rawURL: "http://global.invalid:8080", want: "direct"},
		{name: "missing binding", bound: true, enabled: true, want: "invalid://bound-proxy"},
		{name: "disabled binding", bound: true, present: true, rawURL: "http://127.0.0.1:18080", want: "invalid://bound-proxy"},
		{name: "unhealthy binding", bound: true, present: true, enabled: true, health: HealthUnhealthy, rawURL: "http://127.0.0.1:18080", want: "invalid://bound-proxy"},
		{name: "malformed binding", bound: true, present: true, enabled: true, rawURL: "ftp://invalid.example.com", want: "invalid://bound-proxy"},
		{name: "healthy binding", bound: true, present: true, enabled: true, health: HealthHealthy, rawURL: "http://127.0.0.1:18080", want: "http://127.0.0.1:18080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := strictAccountProxyURL(test.bound, test.present, test.enabled, test.health, test.rawURL); got != test.want {
				t.Fatalf("strictAccountProxyURL() = %q, want %q", got, test.want)
			}
		})
	}
}
