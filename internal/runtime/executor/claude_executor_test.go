package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	xxHash64 "github.com/pierrec/xxHash/xxHash64"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func resetClaudeDeviceProfileCache() {
	helps.ResetClaudeDeviceProfileCache()
}

func newClaudeHeaderTestRequest(t *testing.T, incoming http.Header) *http.Request {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginReq := httptest.NewRequest(http.MethodPost, "http://localhost/v1/messages", nil)
	ginReq.Header = incoming.Clone()
	ginCtx.Request = ginReq

	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	return req.WithContext(context.WithValue(req.Context(), "gin", ginCtx))
}

func assertClaudeFingerprint(t *testing.T, headers http.Header, userAgent, pkgVersion, runtimeVersion, osName, arch string) {
	t.Helper()

	if got := headers.Get("User-Agent"); got != userAgent {
		t.Fatalf("User-Agent = %q, want %q", got, userAgent)
	}
	if got := headers.Get("X-Stainless-Package-Version"); got != pkgVersion {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, pkgVersion)
	}
	if got := headers.Get("X-Stainless-Runtime-Version"); got != runtimeVersion {
		t.Fatalf("X-Stainless-Runtime-Version = %q, want %q", got, runtimeVersion)
	}
	if got := headers.Get("X-Stainless-Os"); got != osName {
		t.Fatalf("X-Stainless-Os = %q, want %q", got, osName)
	}
	if got := headers.Get("X-Stainless-Arch"); got != arch {
		t.Fatalf("X-Stainless-Arch = %q, want %q", got, arch)
	}
}

func TestApplyClaudeHeaders_UsesConfiguredBaselineFingerprint(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			Timeout:                "900",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline",
		Attributes: map[string]string{
			"api_key":                            "key-baseline",
			"header:User-Agent":                  "evil-client/9.9",
			"header:X-Stainless-Os":              "Linux",
			"header:X-Stainless-Arch":            "x64",
			"header:X-Stainless-Package-Version": "9.9.9",
		},
	}
	incoming := http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	}

	req := newClaudeHeaderTestRequest(t, incoming)
	applyClaudeHeaders(req, auth, "key-baseline", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "evil-client/9.9", "9.9.9", "v24.5.0", "Linux", "x64")
	if got := req.Header.Get("X-Stainless-Timeout"); got != "900" {
		t.Fatalf("X-Stainless-Timeout = %q, want %q", got, "900")
	}
}

func TestApplyClaudeHeaders_TracksHighestClaudeCLIFingerprint(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-upgrade",
		Attributes: map[string]string{
			"api_key": "key-upgrade",
		},
	}

	firstReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(firstReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, firstReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "MacOS", "arm64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"lobe-chat/1.0"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "MacOS", "arm64")

	higherReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.63 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.75.0"},
		"X-Stainless-Runtime-Version": []string{"v24.4.0"},
		"X-Stainless-Os":              []string{"MacOS"},
		"X-Stainless-Arch":            []string{"arm64"},
	})
	applyClaudeHeaders(higherReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, higherReq.Header, "claude-cli/2.1.63 (external, cli)", "0.75.0", "v24.4.0", "MacOS", "arm64")

	lowerReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.61 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.73.0"},
		"X-Stainless-Runtime-Version": []string{"v24.2.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(lowerReq, auth, "key-upgrade", false, nil, cfg)
	assertClaudeFingerprint(t, lowerReq.Header, "claude-cli/2.1.63 (external, cli)", "0.75.0", "v24.4.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_DoesNotDowngradeConfiguredBaselineOnFirstClaudeClient(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline-floor",
		Attributes: map[string]string{
			"api_key": "key-baseline-floor",
		},
	}

	olderClaudeReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(olderClaudeReq, auth, "key-baseline-floor", false, nil, cfg)
	assertClaudeFingerprint(t, olderClaudeReq.Header, "claude-cli/2.1.70 (external, cli)", "0.80.0", "v24.5.0", "MacOS", "arm64")

	newerClaudeReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.71 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.81.0"},
		"X-Stainless-Runtime-Version": []string{"v24.6.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(newerClaudeReq, auth, "key-baseline-floor", false, nil, cfg)
	assertClaudeFingerprint(t, newerClaudeReq.Header, "claude-cli/2.1.71 (external, cli)", "0.81.0", "v24.6.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_UpgradesCachedSoftwareFingerprintWhenBaselineAdvances(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	oldCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	newCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.77 (external, cli)",
			PackageVersion:         "0.87.0",
			RuntimeVersion:         "v24.8.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-baseline-reload",
		Attributes: map[string]string{
			"api_key": "key-baseline-reload",
		},
	}

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.71 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.81.0"},
		"X-Stainless-Runtime-Version": []string{"v24.6.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-baseline-reload", false, nil, oldCfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.71 (external, cli)", "0.81.0", "v24.6.0", "MacOS", "arm64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-baseline-reload", false, nil, newCfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_LearnsOfficialFingerprintAfterCustomBaselineFallback(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "my-gateway/1.0",
			PackageVersion:         "custom-pkg",
			RuntimeVersion:         "custom-runtime",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-custom-baseline-learning",
		Attributes: map[string]string{
			"api_key": "key-custom-baseline-learning",
		},
	}

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "my-gateway/1.0", "custom-pkg", "custom-runtime", "MacOS", "arm64")

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.77 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.87.0"},
		"X-Stainless-Runtime-Version": []string{"v24.8.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")

	postLearningThirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(postLearningThirdPartyReq, auth, "key-custom-baseline-learning", false, nil, cfg)
	assertClaudeFingerprint(t, postLearningThirdPartyReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestResolveClaudeDeviceProfile_RechecksCacheBeforeStoringCandidate(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-racy-upgrade",
		Attributes: map[string]string{
			"api_key": "key-racy-upgrade",
		},
	}

	lowPaused := make(chan struct{})
	releaseLow := make(chan struct{})
	var pauseOnce sync.Once
	var releaseOnce sync.Once

	helps.ClaudeDeviceProfileBeforeCandidateStore = func(candidate helps.ClaudeDeviceProfile) {
		if candidate.UserAgent != "claude-cli/2.1.62 (external, cli)" {
			return
		}
		pauseOnce.Do(func() { close(lowPaused) })
		<-releaseLow
	}
	t.Cleanup(func() {
		helps.ClaudeDeviceProfileBeforeCandidateStore = nil
		releaseOnce.Do(func() { close(releaseLow) })
	})

	lowResultCh := make(chan helps.ClaudeDeviceProfile, 1)
	go func() {
		lowResultCh <- helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
			"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
			"X-Stainless-Package-Version": []string{"0.74.0"},
			"X-Stainless-Runtime-Version": []string{"v24.3.0"},
			"X-Stainless-Os":              []string{"Linux"},
			"X-Stainless-Arch":            []string{"x64"},
		}, cfg)
	}()

	select {
	case <-lowPaused:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lower candidate to pause before storing")
	}

	highResult := helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.63 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.75.0"},
		"X-Stainless-Runtime-Version": []string{"v24.4.0"},
		"X-Stainless-Os":              []string{"MacOS"},
		"X-Stainless-Arch":            []string{"arm64"},
	}, cfg)
	releaseOnce.Do(func() { close(releaseLow) })

	select {
	case lowResult := <-lowResultCh:
		if lowResult.UserAgent != "claude-cli/2.1.63 (external, cli)" {
			t.Fatalf("lowResult.UserAgent = %q, want %q", lowResult.UserAgent, "claude-cli/2.1.63 (external, cli)")
		}
		if lowResult.PackageVersion != "0.75.0" {
			t.Fatalf("lowResult.PackageVersion = %q, want %q", lowResult.PackageVersion, "0.75.0")
		}
		if lowResult.OS != "MacOS" || lowResult.Arch != "arm64" {
			t.Fatalf("lowResult platform = %s/%s, want %s/%s", lowResult.OS, lowResult.Arch, "MacOS", "arm64")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lower candidate result")
	}

	if highResult.UserAgent != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("highResult.UserAgent = %q, want %q", highResult.UserAgent, "claude-cli/2.1.63 (external, cli)")
	}
	if highResult.OS != "MacOS" || highResult.Arch != "arm64" {
		t.Fatalf("highResult platform = %s/%s, want %s/%s", highResult.OS, highResult.Arch, "MacOS", "arm64")
	}

	cached := helps.ResolveClaudeDeviceProfile(auth, "key-racy-upgrade", http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	}, cfg)
	if cached.UserAgent != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("cached.UserAgent = %q, want %q", cached.UserAgent, "claude-cli/2.1.63 (external, cli)")
	}
	if cached.PackageVersion != "0.75.0" {
		t.Fatalf("cached.PackageVersion = %q, want %q", cached.PackageVersion, "0.75.0")
	}
	if cached.OS != "MacOS" || cached.Arch != "arm64" {
		t.Fatalf("cached platform = %s/%s, want %s/%s", cached.OS, cached.Arch, "MacOS", "arm64")
	}
}

func TestApplyClaudeHeaders_ThirdPartyBaselineThenOfficialUpgradeKeepsPinnedPlatform(t *testing.T) {
	resetClaudeDeviceProfileCache()
	stabilize := true

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.70 (external, cli)",
			PackageVersion:         "0.80.0",
			RuntimeVersion:         "v24.5.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-third-party-then-official",
		Attributes: map[string]string{
			"api_key": "key-third-party-then-official",
		},
	}

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"curl/8.7.1"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-third-party-then-official", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.70 (external, cli)", "0.80.0", "v24.5.0", "MacOS", "arm64")

	officialReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.77 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.87.0"},
		"X-Stainless-Runtime-Version": []string{"v24.8.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(officialReq, auth, "key-third-party-then-official", false, nil, cfg)
	assertClaudeFingerprint(t, officialReq.Header, "claude-cli/2.1.77 (external, cli)", "0.87.0", "v24.8.0", "MacOS", "arm64")
}

func TestApplyClaudeHeaders_DisableDeviceProfileStabilization(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-disable-stability",
		Attributes: map[string]string{
			"api_key": "key-disable-stability",
		},
	}

	firstReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(firstReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, firstReq.Header, "claude-cli/2.1.62 (external, cli)", "0.74.0", "v24.3.0", "Linux", "x64")

	thirdPartyReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"lobe-chat/1.0"},
		"X-Stainless-Package-Version": []string{"0.10.0"},
		"X-Stainless-Runtime-Version": []string{"v18.0.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(thirdPartyReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, thirdPartyReq.Header, "claude-cli/2.1.60 (external, cli)", "0.10.0", "v18.0.0", "Windows", "x64")

	lowerReq := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.61 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.73.0"},
		"X-Stainless-Runtime-Version": []string{"v24.2.0"},
		"X-Stainless-Os":              []string{"Windows"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(lowerReq, auth, "key-disable-stability", false, nil, cfg)
	assertClaudeFingerprint(t, lowerReq.Header, "claude-cli/2.1.61 (external, cli)", "0.73.0", "v24.2.0", "Windows", "x64")
}

func TestApplyClaudeHeaders_LegacyModePreservesConfiguredUserAgentOverrideForClaudeClients(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-legacy-ua-override",
		Attributes: map[string]string{
			"api_key":           "key-legacy-ua-override",
			"header:User-Agent": "config-ua/1.0",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.62 (external, cli)"},
		"X-Stainless-Package-Version": []string{"0.74.0"},
		"X-Stainless-Runtime-Version": []string{"v24.3.0"},
		"X-Stainless-Os":              []string{"Linux"},
		"X-Stainless-Arch":            []string{"x64"},
	})
	applyClaudeHeaders(req, auth, "key-legacy-ua-override", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "config-ua/1.0", "0.74.0", "v24.3.0", "Linux", "x64")
}

func TestApplyClaudeHeaders_LegacyModeFallsBackToRuntimeOSArchWhenMissing(t *testing.T) {
	resetClaudeDeviceProfileCache()

	stabilize := false
	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:              "claude-cli/2.1.60 (external, cli)",
			PackageVersion:         "0.70.0",
			RuntimeVersion:         "v22.0.0",
			OS:                     "MacOS",
			Arch:                   "arm64",
			StabilizeDeviceProfile: &stabilize,
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-legacy-runtime-os-arch",
		Attributes: map[string]string{
			"api_key": "key-legacy-runtime-os-arch",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})
	applyClaudeHeaders(req, auth, "key-legacy-runtime-os-arch", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "claude-cli/2.1.60 (external, cli)", "0.70.0", "v22.0.0", helps.MapStainlessOS(), helps.MapStainlessArch())
}

func TestApplyClaudeHeaders_UnsetStabilizationAlsoUsesLegacyRuntimeOSArchFallback(t *testing.T) {
	resetClaudeDeviceProfileCache()

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent:      "claude-cli/2.1.60 (external, cli)",
			PackageVersion: "0.70.0",
			RuntimeVersion: "v22.0.0",
			OS:             "MacOS",
			Arch:           "arm64",
		},
	}
	auth := &cliproxyauth.Auth{
		ID: "auth-unset-runtime-os-arch",
		Attributes: map[string]string{
			"api_key": "key-unset-runtime-os-arch",
		},
	}

	req := newClaudeHeaderTestRequest(t, http.Header{
		"User-Agent": []string{"curl/8.7.1"},
	})
	applyClaudeHeaders(req, auth, "key-unset-runtime-os-arch", false, nil, cfg)

	assertClaudeFingerprint(t, req.Header, "claude-cli/2.1.60 (external, cli)", "0.70.0", "v22.0.0", helps.MapStainlessOS(), helps.MapStainlessArch())
}

func TestClaudeDeviceProfileStabilizationEnabled_DefaultFalse(t *testing.T) {
	if helps.ClaudeDeviceProfileStabilizationEnabled(nil) {
		t.Fatal("expected nil config to default to disabled stabilization")
	}
	if helps.ClaudeDeviceProfileStabilizationEnabled(&config.Config{}) {
		t.Fatal("expected unset stabilize-device-profile to default to disabled stabilization")
	}
}

func TestApplyClaudeToolPrefix(t *testing.T) {
	input := []byte(`{"tools":[{"name":"alpha"},{"name":"proxy_bravo"}],"tool_choice":{"type":"tool","name":"charlie"},"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"delta","id":"t1","input":{}}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_alpha" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_alpha")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_bravo" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_bravo")
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "proxy_charlie" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "proxy_charlie")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "proxy_delta" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "proxy_delta")
	}
}

func TestApplyClaudeToolPrefix_WithToolReference(t *testing.T) {
	input := []byte(`{"tools":[{"name":"alpha"}],"messages":[{"role":"user","content":[{"type":"tool_reference","tool_name":"beta"},{"type":"tool_reference","tool_name":"proxy_gamma"}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "messages.0.content.0.tool_name").String(); got != "proxy_beta" {
		t.Fatalf("messages.0.content.0.tool_name = %q, want %q", got, "proxy_beta")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.tool_name").String(); got != "proxy_gamma" {
		t.Fatalf("messages.0.content.1.tool_name = %q, want %q", got, "proxy_gamma")
	}
}

func TestApplyClaudeToolPrefix_SkipsBuiltinTools(t *testing.T) {
	input := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"my_custom_tool","input_schema":{"type":"object"}}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "web_search" {
		t.Fatalf("built-in tool name should not be prefixed: tools.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_my_custom_tool" {
		t.Fatalf("custom tool should be prefixed: tools.1.name = %q, want %q", got, "proxy_my_custom_tool")
	}
}

func TestApplyClaudeToolPrefix_BuiltinToolSkipped(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"type": "web_search_20250305", "name": "web_search", "max_uses": 5},
			{"name": "Read"}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "web_search", "id": "ws1", "input": {}},
				{"type": "tool_use", "name": "Read", "id": "r1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "web_search" {
		t.Fatalf("tools.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "web_search" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.name").String(); got != "proxy_Read" {
		t.Fatalf("messages.0.content.1.name = %q, want %q", got, "proxy_Read")
	}
}

func TestApplyClaudeToolPrefix_KnownBuiltinInHistoryOnly(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"name": "Read"}
		],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "web_search", "id": "ws1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "web_search" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "web_search")
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
	}
}

func TestApplyClaudeToolPrefix_CustomToolsPrefixed(t *testing.T) {
	body := []byte(`{
		"tools": [{"name": "Read"}, {"name": "Write"}],
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_use", "name": "Read", "id": "r1", "input": {}},
				{"type": "tool_use", "name": "Write", "id": "w1", "input": {}}
			]}
		]
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_Write" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_Write")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "proxy_Read" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "proxy_Read")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.name").String(); got != "proxy_Write" {
		t.Fatalf("messages.0.content.1.name = %q, want %q", got, "proxy_Write")
	}
}

func TestApplyClaudeToolPrefix_ToolChoiceBuiltin(t *testing.T) {
	body := []byte(`{
		"tools": [
			{"type": "web_search_20250305", "name": "web_search"},
			{"name": "Read"}
		],
		"tool_choice": {"type": "tool", "name": "web_search"}
	}`)
	out := applyClaudeToolPrefix(body, "proxy_")

	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "web_search" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "web_search")
	}
}

func TestApplyClaudeToolPrefix_KnownFallbackBuiltinsRemainUnprefixed(t *testing.T) {
	for _, builtin := range []string{"web_search", "code_execution", "text_editor", "computer"} {
		t.Run(builtin, func(t *testing.T) {
			input := []byte(fmt.Sprintf(`{
				"tools":[{"name":"Read"}],
				"tool_choice":{"type":"tool","name":%q},
				"messages":[{"role":"assistant","content":[{"type":"tool_use","name":%q,"id":"toolu_1","input":{}},{"type":"tool_reference","tool_name":%q},{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"tool_reference","tool_name":%q}]}]}]
			}`, builtin, builtin, builtin, builtin))
			out := applyClaudeToolPrefix(input, "proxy_")

			if got := gjson.GetBytes(out, "tool_choice.name").String(); got != builtin {
				t.Fatalf("tool_choice.name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != builtin {
				t.Fatalf("messages.0.content.0.name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.1.tool_name").String(); got != builtin {
				t.Fatalf("messages.0.content.1.tool_name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "messages.0.content.2.content.0.tool_name").String(); got != builtin {
				t.Fatalf("messages.0.content.2.content.0.tool_name = %q, want %q", got, builtin)
			}
			if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Read" {
				t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Read")
			}
		})
	}
}

func TestStripClaudeToolPrefixFromResponse(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_use","name":"proxy_alpha","id":"t1","input":{}},{"type":"tool_use","name":"bravo","id":"t2","input":{}}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")

	if got := gjson.GetBytes(out, "content.0.name").String(); got != "alpha" {
		t.Fatalf("content.0.name = %q, want %q", got, "alpha")
	}
	if got := gjson.GetBytes(out, "content.1.name").String(); got != "bravo" {
		t.Fatalf("content.1.name = %q, want %q", got, "bravo")
	}
}

func TestStripClaudeToolPrefixFromResponse_WithToolReference(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_reference","tool_name":"proxy_alpha"},{"type":"tool_reference","tool_name":"bravo"}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")

	if got := gjson.GetBytes(out, "content.0.tool_name").String(); got != "alpha" {
		t.Fatalf("content.0.tool_name = %q, want %q", got, "alpha")
	}
	if got := gjson.GetBytes(out, "content.1.tool_name").String(); got != "bravo" {
		t.Fatalf("content.1.tool_name = %q, want %q", got, "bravo")
	}
}

func TestStripClaudeToolPrefixFromStreamLine(t *testing.T) {
	line := []byte(`data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"proxy_alpha","id":"t1"},"index":0}`)
	out := stripClaudeToolPrefixFromStreamLine(line, "proxy_")

	payload := bytes.TrimSpace(out)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if got := gjson.GetBytes(payload, "content_block.name").String(); got != "alpha" {
		t.Fatalf("content_block.name = %q, want %q", got, "alpha")
	}
}

func TestStripClaudeToolPrefixFromStreamLine_WithToolReference(t *testing.T) {
	line := []byte(`data: {"type":"content_block_start","content_block":{"type":"tool_reference","tool_name":"proxy_beta"},"index":0}`)
	out := stripClaudeToolPrefixFromStreamLine(line, "proxy_")

	payload := bytes.TrimSpace(out)
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if got := gjson.GetBytes(payload, "content_block.tool_name").String(); got != "beta" {
		t.Fatalf("content_block.tool_name = %q, want %q", got, "beta")
	}
}

func TestApplyClaudeToolPrefix_NestedToolReference(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"tool_reference","tool_name":"mcp__nia__manage_resource"}]}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content.0.tool_name").String()
	if got != "proxy_mcp__nia__manage_resource" {
		t.Fatalf("nested tool_reference tool_name = %q, want %q", got, "proxy_mcp__nia__manage_resource")
	}
}

func TestClaudeExecutor_ReusesUserIDAcrossModelsWhenCacheEnabled(t *testing.T) {
	var userIDs []string
	var requestModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userID := gjson.GetBytes(body, "metadata.user_id").String()
		model := gjson.GetBytes(body, "model").String()
		userIDs = append(userIDs, userID)
		requestModels = append(requestModels, model)
		t.Logf("HTTP Server received request: model=%s, user_id=%s, url=%s", model, userID, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	t.Logf("End-to-end test: Fake HTTP server started at %s", server.URL)

	cacheEnabled := true
	executor := NewClaudeExecutor(&config.Config{
		ClaudeKey: []config.ClaudeKey{
			{
				APIKey:  "key-123",
				BaseURL: server.URL,
				Cloak: &config.CloakConfig{
					CacheUserID: &cacheEnabled,
				},
			},
		},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	models := []string{"claude-3-5-sonnet", "claude-3-5-haiku"}
	for _, model := range models {
		t.Logf("Sending request for model: %s", model)
		modelPayload, _ := sjson.SetBytes(payload, "model", model)
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   model,
			Payload: modelPayload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
		}); err != nil {
			t.Fatalf("Execute(%s) error: %v", model, err)
		}
	}

	if len(userIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(userIDs))
	}
	if userIDs[0] == "" || userIDs[1] == "" {
		t.Fatal("expected user_id to be populated")
	}
	t.Logf("user_id[0] (model=%s): %s", requestModels[0], userIDs[0])
	t.Logf("user_id[1] (model=%s): %s", requestModels[1], userIDs[1])
	if userIDs[0] != userIDs[1] {
		t.Fatalf("expected user_id to be reused across models, got %q and %q", userIDs[0], userIDs[1])
	}
	if !helps.IsValidUserID(userIDs[0]) {
		t.Fatalf("user_id %q is not valid", userIDs[0])
	}
	t.Logf("✓ End-to-end test passed: Same user_id (%s) was used for both models", userIDs[0])
}

func TestClaudeExecutor_GeneratesNewUserIDByDefault(t *testing.T) {
	var userIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userIDs = append(userIDs, gjson.GetBytes(body, "metadata.user_id").String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	for i := 0; i < 2; i++ {
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet",
			Payload: payload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
		}); err != nil {
			t.Fatalf("Execute call %d error: %v", i, err)
		}
	}

	if len(userIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(userIDs))
	}
	if userIDs[0] == "" || userIDs[1] == "" {
		t.Fatal("expected user_id to be populated")
	}
	if userIDs[0] == userIDs[1] {
		t.Fatalf("expected user_id to change when caching is not enabled, got identical values %q", userIDs[0])
	}
	if !helps.IsValidUserID(userIDs[0]) || !helps.IsValidUserID(userIDs[1]) {
		t.Fatalf("user_ids should be valid, got %q and %q", userIDs[0], userIDs[1])
	}
}

func TestClaudeExecutor_UsesFixedAccountCloakUserID(t *testing.T) {
	var userIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userIDs = append(userIDs, gjson.GetBytes(body, "metadata.user_id").String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	fixedUserID := "2cbd8b9e-b3ec-4b6f-a831-76437a59b04e"
	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":       "key-123",
		"base_url":      server.URL,
		"cloak_user_id": fixedUserID,
	}}
	payload := []byte(`{"metadata":{"user_id":"downstream-user"},"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	for i := 0; i < 2; i++ {
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet",
			Payload: payload,
		}, cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString("claude"),
		}); err != nil {
			t.Fatalf("Execute call %d error: %v", i, err)
		}
	}

	if len(userIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(userIDs))
	}
	if userIDs[0] != fixedUserID || userIDs[1] != fixedUserID {
		t.Fatalf("user_ids = %+v, want fixed account user_id %q", userIDs, fixedUserID)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolUsesBuiltinFullProfile(t *testing.T) {
	var seenBody []byte
	var seenUA string
	var seenBeta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		seenUA = r.Header.Get("User-Agent")
		seenBeta = r.Header.Get("Anthropic-Beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	fixedUserID := "user_be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714_account_11111111-2222-4333-8444-555555555555_session_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                                   "key-123",
		"base_url":                                  server.URL,
		"claude_oauth_pool":                         "true",
		"cloak_user_id":                             fixedUserID,
		"claude_code_profile_version":               "0.0.1",
		"claude_code_profile_user_agent":            "bad-client/0.0.1",
		"claude_code_profile_system_prompt":         "bad prompt",
		"claude_code_profile_billing_block_enabled": "false",
	}, Metadata: map[string]any{"account_uuid": "99999999-aaaa-4bbb-8ccc-dddddddddddd"}}
	payload := []byte(`{"system":"Custom downstream system.","messages":[{"role":"user","content":"hi"}]}`)

	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected upstream body to be captured")
	}
	if seenUA != "claude-cli/2.1.178 (external, sdk-cli)" {
		t.Fatalf("User-Agent = %q, want builtin Claude Code UA", seenUA)
	}
	if !strings.Contains(seenBeta, "claude-code-20250219") || strings.Contains(seenBeta, "oauth-2025-04-20") {
		t.Fatalf("Anthropic-Beta = %q, want builtin Claude Code betas", seenBeta)
	}

	blocks := gjson.GetBytes(seenBody, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 builtin system blocks, got %d: %s", len(blocks), string(seenBody))
	}
	billingHeader := blocks[0].Get("text").String()
	if !strings.HasPrefix(billingHeader, "x-anthropic-billing-header: cc_version=2.1.178.") {
		t.Fatalf("billing header = %q, want builtin 2.1.178 header", billingHeader)
	}
	if strings.Contains(billingHeader, "cch=00000;") {
		t.Fatalf("billing header should be signed before upstream, got %q", billingHeader)
	}
	if got := blocks[1].Get("text").String(); got != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("identity block = %q", got)
	}
	if got := blocks[2].Get("text").String(); got != expectedClaudeCodeStaticPrompt() {
		t.Fatalf("static prompt block did not use builtin full Claude Code prompt")
	}
	userID := gjson.GetBytes(seenBody, "metadata.user_id").String()
	parsed, ok := helps.ParseClaudeCodeMetadataUserID(userID)
	if !ok {
		t.Fatalf("metadata.user_id = %q, want Claude Code metadata user id", userID)
	}
	if parsed.DeviceID != "be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714" {
		t.Fatalf("metadata.user_id device_id = %q", parsed.DeviceID)
	}
	if parsed.AccountUUID != "99999999-aaaa-4bbb-8ccc-dddddddddddd" {
		t.Fatalf("metadata.user_id account_uuid = %q", parsed.AccountUUID)
	}
	if !strings.HasPrefix(strings.TrimSpace(userID), "{") {
		t.Fatalf("metadata.user_id = %q, want JSON format for builtin Claude Code profile", userID)
	}
	if got := gjson.GetBytes(seenBody, "messages.0.content").String(); !strings.Contains(got, "Use the available tools when needed") || strings.Contains(got, "Custom downstream system.") {
		t.Fatalf("forwarded system reminder was not sanitized for OAuth account pool: %q", got)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolRequestShape(t *testing.T) {
	var seenBody []byte
	var seenHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		seenHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                    "sk-ant-oat-test",
		"base_url":                   server.URL,
		"claude_oauth_pool":          "true",
		"cloak_user_id":              "user_be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714_account_11111111-2222-4333-8444-555555555555_session_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		"header:X-Client-Request-Id": "bad-request-id",
		"header:X-Foo-Trace":         "kept",
	}, Metadata: map[string]any{"account_uuid": "11111111-2222-4333-8444-555555555555"}}
	payload := []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if seenHeaders.Get("User-Agent") != "claude-cli/2.1.178 (external, sdk-cli)" {
		t.Fatalf("User-Agent = %q", seenHeaders.Get("User-Agent"))
	}
	if seenHeaders.Get("X-Client-Request-Id") != "" {
		t.Fatalf("X-Client-Request-Id should not be synthesized for account pool: %q", seenHeaders.Get("X-Client-Request-Id"))
	}
	if seenHeaders.Get("Anthropic-Dangerous-Direct-Browser-Access") != "" {
		t.Fatalf("browser access header should not be sent for account pool")
	}
	beta := seenHeaders.Get("Anthropic-Beta")
	for _, want := range []string{
		"claude-code-20250219",
		"mid-conversation-system-2026-04-07",
		"advisor-tool-2026-03-01",
		"effort-2025-11-24",
	} {
		if !strings.Contains(beta, want) {
			t.Fatalf("Anthropic-Beta = %q, missing %q", beta, want)
		}
	}
	if strings.Contains(beta, "oauth-2025-04-20") {
		t.Fatalf("Anthropic-Beta = %q, should not force oauth beta", beta)
	}
	if strings.Contains(beta, "context-1m-2025-08-07") {
		t.Fatalf("Anthropic-Beta = %q, should not add long-context beta by default", beta)
	}
	if gjson.GetBytes(seenBody, "tools").Exists() {
		t.Fatalf("ordinary account-pool mimic request should not inject tools: %s", seenBody)
	}
	if gjson.GetBytes(seenBody, "thinking").Exists() {
		t.Fatalf("ordinary account-pool mimic request should not inject thinking: %s", seenBody)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolPreservesCachedSystemBlocks(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":           "sk-ant-oat-test",
		"base_url":          server.URL,
		"claude_oauth_pool": "true",
		"cloak_user_id":     "user_be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714_account_11111111-2222-4333-8444-555555555555_session_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
	}, Metadata: map[string]any{"account_uuid": "11111111-2222-4333-8444-555555555555"}}
	payload := []byte(`{
		"model":"claude-sonnet-4-6",
		"max_tokens":16,
		"system":[
			{"type":"text","text":"CACHE_ME","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"Do not cache this plain instruction."}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-sonnet-4-6",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected upstream body to be captured")
	}
	blocks := gjson.GetBytes(seenBody, "system").Array()
	if len(blocks) != 4 {
		t.Fatalf("expected 3 injected blocks plus 1 cached block, got %d: %s", len(blocks), seenBody)
	}
	if got := blocks[3].Get("text").String(); got != "CACHE_ME" {
		t.Fatalf("cached system block text = %q, want CACHE_ME; body=%s", got, seenBody)
	}
	if got := blocks[3].Get("cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("cached system block cache_control.type = %q, want ephemeral; body=%s", got, seenBody)
	}
	userContent := gjson.GetBytes(seenBody, "messages.0.content").String()
	if strings.Contains(userContent, "CACHE_ME") {
		t.Fatalf("cached system block should not be moved into user content: %q", userContent)
	}
	if !strings.Contains(userContent, "Use the available tools when needed") || strings.Contains(userContent, "Do not cache this plain instruction.") {
		t.Fatalf("plain system reminder was not sanitized for OAuth account pool: %q", userContent)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolPreservesExplicitLongContextBeta(t *testing.T) {
	var seenBeta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBeta = r.Header.Get("Anthropic-Beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-sonnet-4-6","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":           "sk-ant-oat-test",
		"base_url":          server.URL,
		"claude_oauth_pool": "true",
	}}
	payload := []byte(`{"model":"claude-sonnet-4-6","max_tokens":16,"betas":["context-1m-2025-08-07"],"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-sonnet-4-6",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if !strings.Contains(seenBeta, "context-1m-2025-08-07") {
		t.Fatalf("Anthropic-Beta = %q, want explicit long-context beta preserved", seenBeta)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolModelSpecificBetas(t *testing.T) {
	tests := []struct {
		model   string
		want    []string
		notWant []string
	}{
		{
			model:   "claude-haiku-4-5-20251001",
			want:    []string{"interleaved-thinking-2025-05-14", "thinking-token-count-2026-05-13", "context-management-2025-06-27", "prompt-caching-scope-2026-01-05", "claude-code-20250219", "advisor-tool-2026-03-01"},
			notWant: []string{"context-1m-2025-08-07", "effort-2025-11-24", "mid-conversation-system-2026-04-07"},
		},
		{
			model:   "claude-sonnet-4-6",
			want:    []string{"claude-code-20250219", "interleaved-thinking-2025-05-14", "thinking-token-count-2026-05-13", "context-management-2025-06-27", "prompt-caching-scope-2026-01-05", "advisor-tool-2026-03-01", "effort-2025-11-24"},
			notWant: []string{"context-1m-2025-08-07", "mid-conversation-system-2026-04-07"},
		},
		{
			model:   "claude-opus-4-8",
			want:    []string{"claude-code-20250219", "interleaved-thinking-2025-05-14", "thinking-token-count-2026-05-13", "context-management-2025-06-27", "prompt-caching-scope-2026-01-05", "mid-conversation-system-2026-04-07", "advisor-tool-2026-03-01", "effort-2025-11-24"},
			notWant: []string{"context-1m-2025-08-07"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			beta := strings.Join(claudeCodeAccountPoolBetasForModel(tt.model), ",")
			for _, want := range tt.want {
				if !strings.Contains(beta, want) {
					t.Fatalf("beta %q missing %q", beta, want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(beta, notWant) {
					t.Fatalf("beta %q should not contain %q", beta, notWant)
				}
			}
		})
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolThinkingPreservesClientIntent(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`)
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"claude_oauth_pool": "true",
		"cloak_user_id":     "user_be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714_account_11111111-2222-4333-8444-555555555555_session_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
	}, Metadata: map[string]any{"account_uuid": "11111111-2222-4333-8444-555555555555"}}

	out := applyClaudeCodeAccountPoolProfile(context.Background(), auth, body, cliproxyexecutor.Request{Payload: body}, cliproxyexecutor.Options{})
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "high" {
		t.Fatalf("output_config.effort = %q, want high; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "context_management.edits.0.type").String(); got != "clear_thinking_20251015" {
		t.Fatalf("context_management edit = %q, want clear_thinking_20251015; body=%s", got, out)
	}
}

func TestClaudeExecutor_ClaudeCodeAccountPoolSessionIDIsStableByConversation(t *testing.T) {
	var userIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		userIDs = append(userIDs, gjson.GetBytes(body, "metadata.user_id").String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                  "key-123",
		"base_url":                 server.URL,
		"claude_oauth_pool":        "true",
		"claude_code_pool":         "true",
		"cloak_user_id":            "user_be82c3aee1e0c2d74535bacc85f9f559228f02dd8a17298cf522b71e6c375714_account_11111111-2222-4333-8444-555555555555_session_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		resourcepool.AttrAccountID: "account-a",
	}, Metadata: map[string]any{"account_uuid": "11111111-2222-4333-8444-555555555555"}}
	payload := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	for i := 0; i < 2; i++ {
		if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
			t.Fatalf("Execute call %d error: %v", i, err)
		}
	}
	changedPayload := []byte(`{"messages":[{"role":"user","content":"different conversation"}]}`)
	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet",
		Payload: changedPayload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}); err != nil {
		t.Fatalf("Execute changed conversation error: %v", err)
	}

	if len(userIDs) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(userIDs))
	}
	first, ok := helps.ParseClaudeCodeMetadataUserID(userIDs[0])
	if !ok {
		t.Fatalf("first metadata.user_id = %q", userIDs[0])
	}
	second, ok := helps.ParseClaudeCodeMetadataUserID(userIDs[1])
	if !ok {
		t.Fatalf("second metadata.user_id = %q", userIDs[1])
	}
	third, ok := helps.ParseClaudeCodeMetadataUserID(userIDs[2])
	if !ok {
		t.Fatalf("third metadata.user_id = %q", userIDs[2])
	}
	if first.DeviceID != second.DeviceID || first.AccountUUID != second.AccountUUID {
		t.Fatalf("account identity changed across calls: first=%+v second=%+v", first, second)
	}
	if first.SessionID != second.SessionID {
		t.Fatalf("same conversation session_id changed: %q vs %q", first.SessionID, second.SessionID)
	}
	if first.SessionID == third.SessionID {
		t.Fatalf("different conversation reused session_id %q", first.SessionID)
	}
}

func TestClaudeExecutor_CleanInputTokensRewriteNonStream(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"claude_oauth_pool":                               "true",
		"claude_code_clean_input_tokens":                  "true",
		"claude_code_clean_input_default_overhead_tokens": "1909",
		"claude_code_usage_overheads_json":                `{"claude-opus-4-8":1909}`,
	}}
	payload := []byte(`{"usage":{"input_tokens":1910,"output_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"iterations":[{"input_tokens":1908,"output_tokens":13,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"type":"message"}]},"debug":{"input_tokens":1910}}`)
	rewritten := rewriteClaudeUsageForCleanInput(auth, "claude-opus-4-8", payload, false, 0)
	if got := gjson.GetBytes(rewritten, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("input_tokens = %d, want 1; payload=%s", got, rewritten)
	}
	if got := gjson.GetBytes(rewritten, "usage.iterations.0.input_tokens").Int(); got != 1 {
		t.Fatalf("iterations input_tokens = %d, want 1; payload=%s", got, rewritten)
	}
	if got := gjson.GetBytes(rewritten, "usage.output_tokens").Int(); got != 7 {
		t.Fatalf("output_tokens = %d, want unchanged 7", got)
	}
	if got := gjson.GetBytes(rewritten, "debug.input_tokens").Int(); got != 1910 {
		t.Fatalf("debug input_tokens = %d, want unchanged 1910; payload=%s", got, rewritten)
	}
}

func TestClaudeExecutor_ExecuteRewritesCleanInputTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1910,"output_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                        "sk-ant-oat-test",
		"base_url":                       server.URL,
		"claude_oauth_pool":              "true",
		"claude_code_clean_input_tokens": "true",
		"claude_code_clean_input_default_overhead_tokens": "1909",
		"claude_code_usage_overheads_json":                `{"claude-opus-4-8":1909}`,
	}}
	payload := []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	resp, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("response usage.input_tokens = %d, want 1; payload=%s", got, resp.Payload)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.output_tokens").Int(); got != 7 {
		t.Fatalf("response usage.output_tokens = %d, want unchanged 7", got)
	}
}

func TestClaudeExecutor_ExecuteCleanInputTokensUsesVisibleInputFloor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1910,"output_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                        "sk-ant-oat-test",
		"base_url":                       server.URL,
		"claude_oauth_pool":              "true",
		"claude_code_clean_input_tokens": "true",
		"claude_code_clean_input_default_overhead_tokens": "1909",
		"claude_code_usage_overheads_json":                `{"claude-opus-4-8":1909}`,
	}}
	payload := []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"你是谁"}]}`)
	floor := estimateClaudeVisibleInputTokens(payload)
	if floor <= 1 {
		t.Fatalf("estimated visible floor = %d, want > 1", floor)
	}

	resp, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.input_tokens").Int(); got != floor {
		t.Fatalf("response usage.input_tokens = %d, want estimated floor %d; payload=%s", got, floor, resp.Payload)
	}
}

func TestClaudeExecutor_CountTokensRewritesCleanInputTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":112}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                        "sk-ant-oat-test",
		"base_url":                       server.URL,
		"claude_oauth_pool":              "true",
		"claude_code_clean_input_tokens": "true",
		"claude_code_clean_input_default_overhead_tokens": "105",
		"claude_code_usage_overheads_json":                `{"claude-opus-4-8":105}`,
	}}
	payload := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"Deterministic token accounting verification text."}]}`)

	resp, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "input_tokens").Int(); got != 7 {
		t.Fatalf("count_tokens input_tokens = %d, want clean 7; payload=%s", got, resp.Payload)
	}
}

func TestClaudeExecutor_CleanInputTokensKeepsMinimumOne(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"claude_oauth_pool":                               "true",
		"claude_code_clean_input_tokens":                  "true",
		"claude_code_clean_input_default_overhead_tokens": "1909",
	}}
	payload := []byte(`{"usage":{"input_tokens":10,"output_tokens":1}}`)
	rewritten := rewriteClaudeUsageForCleanInput(auth, "claude-3-5-haiku-latest", payload, false, 0)
	if got := gjson.GetBytes(rewritten, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("input_tokens = %d, want minimum 1", got)
	}
}

func TestClaudeExecutor_CleanInputTokensDisabledDoesNotRewrite(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"claude_oauth_pool":                               "true",
		"claude_code_clean_input_tokens":                  "false",
		"claude_code_clean_input_default_overhead_tokens": "1909",
	}}
	payload := []byte(`{"usage":{"input_tokens":1910,"output_tokens":1}}`)
	rewritten := rewriteClaudeUsageForCleanInput(auth, "claude-opus-4-8", payload, false, 0)
	if string(rewritten) != string(payload) {
		t.Fatalf("payload rewritten while disabled: %s", rewritten)
	}
}

func TestClaudeExecutor_CleanInputTokensRewriteStreamMessageUsage(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"claude_oauth_pool":                               "true",
		"claude_code_clean_input_tokens":                  "true",
		"claude_code_clean_input_default_overhead_tokens": "1909",
	}}
	line := []byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":1910,"output_tokens":0,"iterations":[{"input_tokens":1908,"output_tokens":13,"type":"message"}]}}}`)
	rewritten := rewriteClaudeStreamUsageForCleanInput(auth, "claude-opus-4-8", line, 0)
	payload := strings.TrimSpace(strings.TrimPrefix(string(rewritten), "data:"))
	if got := gjson.Get(payload, "message.usage.input_tokens").Int(); got != 1 {
		t.Fatalf("stream message input_tokens = %d, want 1; line=%s", got, rewritten)
	}
	if got := gjson.Get(payload, "message.usage.iterations.0.input_tokens").Int(); got != 1 {
		t.Fatalf("stream iterations input_tokens = %d, want 1; line=%s", got, rewritten)
	}
}

func TestClaudeExecutor_PoolPureModeSkipsCloakingAndAutoCacheControl(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                    "key-123",
		"base_url":                   server.URL,
		claudeapipool.AttrPool:       "true",
		claudeapipool.AttrPureMode:   "true",
		claudeapipool.AttrPosition:   "1",
		claudeapipool.AttrItemHash:   "hash",
		claudeapipool.AttrModelsJSON: `[{"name":"claude-opus-4-8"}]`,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(seenBody) == 0 {
		t.Fatal("expected request body to be captured")
	}
	if gjson.GetBytes(seenBody, "system").Exists() {
		t.Fatalf("system was injected in pure mode: %s", string(seenBody))
	}
	if got := countCacheControls(seenBody); got != 0 {
		t.Fatalf("cache_control count = %d, want 0 in pure mode; body: %s", got, string(seenBody))
	}
	if got := gjson.GetBytes(seenBody, "messages.0.content.0.text").String(); got != "hi" {
		t.Fatalf("message text = %q, want hi", got)
	}
}

func TestClaudeExecutor_ExecuteOpenAINonStreamRejectsEmptyClaudeStream(t *testing.T) {
	_, err := executeOpenAIChatCompletionThroughClaude(t, "")
	if err == nil {
		t.Fatal("Execute error = nil, want empty stream error")
	}
	assertStatusErr(t, err, http.StatusBadGateway)
	if !strings.Contains(err.Error(), "empty stream response") {
		t.Fatalf("Execute error = %q, want empty stream response", err.Error())
	}
}

func TestClaudeExecutor_ExecuteOpenAINonStreamRejectsClaudeErrorEvent(t *testing.T) {
	body := `data: {"type":"error","error":{"type":"overloaded_error","message":"upstream overloaded"}}` + "\n"
	_, err := executeOpenAIChatCompletionThroughClaude(t, body)
	if err == nil {
		t.Fatal("Execute error = nil, want upstream error event")
	}
	assertStatusErr(t, err, http.StatusBadGateway)
	if !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("Execute error = %q, want upstream overloaded", err.Error())
	}
}

func TestClaudeExecutor_ExecuteOpenAINonStreamRejectsIncompleteClaudeStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_123","model":"claude-3-5-sonnet-20241022"}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	_, err := executeOpenAIChatCompletionThroughClaude(t, body)
	if err == nil {
		t.Fatal("Execute error = nil, want incomplete stream error")
	}
	assertStatusErr(t, err, http.StatusBadGateway)
	if !strings.Contains(err.Error(), "ended before message completion") {
		t.Fatalf("Execute error = %q, want incomplete stream error", err.Error())
	}
}

func TestClaudeExecutor_ExecuteOpenAINonStreamConvertsValidClaudeStream(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_123","model":"claude-3-5-sonnet-20241022"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":2,"output_tokens":1}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	resp, err := executeOpenAIChatCompletionThroughClaude(t, body)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "id").String(); got != "msg_123" {
		t.Fatalf("response id = %q, want msg_123; payload=%s", got, string(resp.Payload))
	}
	if got := gjson.GetBytes(resp.Payload, "model").String(); got != "claude-3-5-sonnet-20241022" {
		t.Fatalf("response model = %q, want claude-3-5-sonnet-20241022", got)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "ok" {
		t.Fatalf("response content = %q, want ok", got)
	}
	if got := gjson.GetBytes(resp.Payload, "usage.total_tokens").Int(); got != 3 {
		t.Fatalf("usage.total_tokens = %d, want 3", got)
	}
}

func executeOpenAIChatCompletionThroughClaude(t *testing.T, upstreamBody string) (cliproxyexecutor.Response, error) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`)

	return executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
	})
}

func assertStatusErr(t *testing.T, err error, want int) {
	t.Helper()

	status, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error %T does not expose StatusCode", err)
	}
	if got := status.StatusCode(); got != want {
		t.Fatalf("StatusCode() = %d, want %d", got, want)
	}
}

func TestStripClaudeToolPrefixFromResponse_NestedToolReference(t *testing.T) {
	input := []byte(`{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"tool_reference","tool_name":"proxy_mcp__nia__manage_resource"}]}]}`)
	out := stripClaudeToolPrefixFromResponse(input, "proxy_")
	got := gjson.GetBytes(out, "content.0.content.0.tool_name").String()
	if got != "mcp__nia__manage_resource" {
		t.Fatalf("nested tool_reference tool_name = %q, want %q", got, "mcp__nia__manage_resource")
	}
}

func TestApplyClaudeToolPrefix_NestedToolReferenceWithStringContent(t *testing.T) {
	// tool_result.content can be a string - should not be processed
	input := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"plain string result"}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content").String()
	if got != "plain string result" {
		t.Fatalf("string content should remain unchanged = %q", got)
	}
}

func TestApplyClaudeToolPrefix_SkipsBuiltinToolReference(t *testing.T) {
	input := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"tool_reference","tool_name":"web_search"}]}]}]}`)
	out := applyClaudeToolPrefix(input, "proxy_")
	got := gjson.GetBytes(out, "messages.0.content.0.content.0.tool_name").String()
	if got != "web_search" {
		t.Fatalf("built-in tool_reference should not be prefixed, got %q", got)
	}
}

func TestNormalizeCacheControlTTL_DowngradesLaterOneHourBlocks(t *testing.T) {
	payload := []byte(`{
		"tools": [{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"system": [{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]
	}`)

	out := normalizeCacheControlTTL(payload)

	if got := gjson.GetBytes(out, "tools.0.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("tools.0.cache_control.ttl = %q, want %q", got, "1h")
	}
	if gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists() {
		t.Fatalf("messages.0.content.0.cache_control.ttl should be removed after a default-5m block")
	}
}

func TestNormalizeCacheControlTTL_PreservesOriginalBytesWhenNoChange(t *testing.T) {
	// Payload where no TTL normalization is needed (all blocks use 1h with no
	// preceding 5m block). The text intentionally contains HTML chars (<, >, &)
	// that json.Marshal would escape to \u003c etc., altering byte identity.
	payload := []byte(`{"tools":[{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}}],"system":[{"type":"text","text":"<system-reminder>foo & bar</system-reminder>","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

	out := normalizeCacheControlTTL(payload)

	if !bytes.Equal(out, payload) {
		t.Fatalf("normalizeCacheControlTTL altered bytes when no change was needed.\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestNormalizeCacheControlTTL_PreservesKeyOrderWhenModified(t *testing.T) {
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],"tools":[{"name":"t1","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}]}`)

	out := normalizeCacheControlTTL(payload)

	if gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists() {
		t.Fatalf("messages.0.content.0.cache_control.ttl should be removed after a default-5m block")
	}

	outStr := string(out)
	idxModel := strings.Index(outStr, `"model"`)
	idxMessages := strings.Index(outStr, `"messages"`)
	idxTools := strings.Index(outStr, `"tools"`)
	idxSystem := strings.Index(outStr, `"system"`)
	if idxModel == -1 || idxMessages == -1 || idxTools == -1 || idxSystem == -1 {
		t.Fatalf("failed to locate top-level keys in output: %s", outStr)
	}
	if !(idxModel < idxMessages && idxMessages < idxTools && idxTools < idxSystem) {
		t.Fatalf("top-level key order changed:\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestEnforceCacheControlLimit_StripsNonLastToolBeforeMessages(t *testing.T) {
	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}}
		],
		"system": [{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}}]}
		]
	}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed first (non-last tool)")
	}
	if !gjson.GetBytes(out, "tools.1.cache_control").Exists() {
		t.Fatalf("tools.1.cache_control (last tool) should be preserved")
	}
	if !gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists() || !gjson.GetBytes(out, "messages.1.content.0.cache_control").Exists() {
		t.Fatalf("message cache_control blocks should be preserved when non-last tool removal is enough")
	}
}

func TestEnforceCacheControlLimit_PreservesKeyOrderWhenModified(t *testing.T) {
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"t1","cache_control":{"type":"ephemeral"}},{"name":"t2","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}}]}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed first (non-last tool)")
	}

	outStr := string(out)
	idxModel := strings.Index(outStr, `"model"`)
	idxMessages := strings.Index(outStr, `"messages"`)
	idxTools := strings.Index(outStr, `"tools"`)
	idxSystem := strings.Index(outStr, `"system"`)
	if idxModel == -1 || idxMessages == -1 || idxTools == -1 || idxSystem == -1 {
		t.Fatalf("failed to locate top-level keys in output: %s", outStr)
	}
	if !(idxModel < idxMessages && idxMessages < idxTools && idxTools < idxSystem) {
		t.Fatalf("top-level key order changed:\noriginal: %s\ngot:      %s", payload, out)
	}
}

func TestEnforceCacheControlLimit_ToolOnlyPayloadStillRespectsLimit(t *testing.T) {
	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}},
			{"name":"t3","cache_control":{"type":"ephemeral"}},
			{"name":"t4","cache_control":{"type":"ephemeral"}},
			{"name":"t5","cache_control":{"type":"ephemeral"}}
		]
	}`)

	out := enforceCacheControlLimit(payload, 4)

	if got := countCacheControls(out); got != 4 {
		t.Fatalf("cache_control count = %d, want 4", got)
	}
	if gjson.GetBytes(out, "tools.0.cache_control").Exists() {
		t.Fatalf("tools.0.cache_control should be removed to satisfy max=4")
	}
	if !gjson.GetBytes(out, "tools.4.cache_control").Exists() {
		t.Fatalf("last tool cache_control should be preserved when possible")
	}
}

func TestClaudeExecutor_CountTokens_AppliesCacheControlGuards(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{
		"tools": [
			{"name":"t1","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"name":"t2","cache_control":{"type":"ephemeral"}}
		],
		"system": [
			{"type":"text","text":"s1","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"type":"text","text":"s2","cache_control":{"type":"ephemeral","ttl":"1h"}}
		],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
			{"role":"user","content":[{"type":"text","text":"u2","cache_control":{"type":"ephemeral","ttl":"1h"}}]}
		]
	}`)

	_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-haiku-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	if len(seenBody) == 0 {
		t.Fatal("expected count_tokens request body to be captured")
	}
	if got := countCacheControls(seenBody); got > 4 {
		t.Fatalf("count_tokens body has %d cache_control blocks, want <= 4", got)
	}
	if hasTTLOrderingViolation(seenBody) {
		t.Fatalf("count_tokens body still has ttl ordering violations: %s", string(seenBody))
	}
}

func TestClaudeExecutor_CountTokens_StripsAccountPoolMetadata(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":3819}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":           "sk-ant-oat-test",
		"base_url":          server.URL,
		"claude_oauth_pool": "true",
		"cloak_user_id":     helps.GenerateFakeUserID(),
	}}

	payload := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`)
	_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-opus-4-8",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("CountTokens error: %v", err)
	}

	if gjson.GetBytes(seenBody, "metadata").Exists() {
		t.Fatalf("count_tokens account-pool body must not include metadata: %s", seenBody)
	}
	if got := gjson.GetBytes(seenBody, "system.0.text").String(); !strings.HasPrefix(got, "x-anthropic-billing-header:") {
		t.Fatalf("count_tokens account-pool body missing billing system block: %s", seenBody)
	}
	if got := gjson.GetBytes(seenBody, "system.2.text").String(); !strings.Contains(got, helps.ClaudeCodeIntro) {
		t.Fatalf("count_tokens account-pool body missing Claude Code static prompt: %s", seenBody)
	}
}

func TestClaudeExecutor_ExecuteSanitizesSignaturesBeforeUpstream(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-sonnet-4-5","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}

	payload := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 16,
		"messages": [
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"drop this","signature":""},
				{"type":"text","text":"I will run git status."},
				{"type":"tool_use","id":"Bash-1","name":"Bash","input":{"command":"git status"},"signature":"bad","thoughtSignature":"bad2","model":"claude-opus-4-1"}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"Bash-1","content":"ok"}]}
		]
	}`)

	if _, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-sonnet-4-5",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	parts := gjson.GetBytes(seenBody, "messages.0.content").Array()
	if len(parts) != 2 {
		t.Fatalf("messages.0.content length = %d, want 2; body=%s", len(parts), seenBody)
	}
	if parts[0].Get("type").String() != "text" {
		t.Fatalf("first remaining part = %s, want text", parts[0].Raw)
	}
	toolUse := parts[1]
	if toolUse.Get("type").String() != "tool_use" {
		t.Fatalf("second remaining part = %s, want tool_use", toolUse.Raw)
	}
	for _, path := range []string{"signature", "thoughtSignature", "model"} {
		if toolUse.Get(path).Exists() {
			t.Fatalf("tool_use.%s should be removed before upstream: %s", path, seenBody)
		}
	}
}

func hasTTLOrderingViolation(payload []byte) bool {
	seen5m := false
	violates := false

	checkCC := func(cc gjson.Result) {
		if !cc.Exists() || violates {
			return
		}
		ttl := cc.Get("ttl").String()
		if ttl != "1h" {
			seen5m = true
			return
		}
		if seen5m {
			violates = true
		}
	}

	tools := gjson.GetBytes(payload, "tools")
	if tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			checkCC(tool.Get("cache_control"))
			return !violates
		})
	}

	system := gjson.GetBytes(payload, "system")
	if system.IsArray() {
		system.ForEach(func(_, item gjson.Result) bool {
			checkCC(item.Get("cache_control"))
			return !violates
		})
	}

	messages := gjson.GetBytes(payload, "messages")
	if messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			content := msg.Get("content")
			if content.IsArray() {
				content.ForEach(func(_, item gjson.Result) bool {
					checkCC(item.Get("cache_control"))
					return !violates
				})
			}
			return !violates
		})
	}

	return violates
}

func TestClaudeExecutor_Execute_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func TestClaudeExecutor_ExecuteStream_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func TestClaudeExecutor_CountTokens_InvalidGzipErrorBodyReturnsDecodeMessage(t *testing.T) {
	testClaudeExecutorInvalidCompressedErrorBody(t, func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error {
		_, err := executor.CountTokens(context.Background(), auth, cliproxyexecutor.Request{
			Model:   "claude-3-5-sonnet-20241022",
			Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
		return err
	})
}

func testClaudeExecutorInvalidCompressedErrorBody(
	t *testing.T,
	invoke func(executor *ClaudeExecutor, auth *cliproxyauth.Auth, payload []byte) error,
) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("not-a-valid-gzip-stream"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	err := invoke(executor, auth, payload)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode error response body") {
		t.Fatalf("expected decode failure message, got: %v", err)
	}
	if statusProvider, ok := err.(interface{ StatusCode() int }); !ok || statusProvider.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected status code 400, got: %v", err)
	}
}

func TestEnsureModelMaxTokens_UsesRegisteredMaxCompletionTokens(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-max-completion-tokens-client"
	modelID := "test-claude-max-completion-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		Type:                "claude",
		OwnedBy:             "anthropic",
		Object:              "model",
		Created:             time.Now().Unix(),
		MaxCompletionTokens: 4096,
		UserDefined:         true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-max-completion-tokens-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != 4096 {
		t.Fatalf("max_tokens = %d, want %d", got, 4096)
	}
}

func TestEnsureModelMaxTokens_DefaultsMissingValue(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-default-max-tokens-client"
	modelID := "test-claude-default-max-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:          modelID,
		Type:        "claude",
		OwnedBy:     "anthropic",
		Object:      "model",
		Created:     time.Now().Unix(),
		UserDefined: true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-default-max-tokens-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != defaultModelMaxTokens {
		t.Fatalf("max_tokens = %d, want %d", got, defaultModelMaxTokens)
	}
}

func TestEnsureModelMaxTokens_PreservesExplicitValue(t *testing.T) {
	reg := registry.GetGlobalRegistry()
	clientID := "test-claude-preserve-max-tokens-client"
	modelID := "test-claude-preserve-max-tokens-model"
	reg.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		Type:                "claude",
		OwnedBy:             "anthropic",
		Object:              "model",
		Created:             time.Now().Unix(),
		MaxCompletionTokens: 4096,
		UserDefined:         true,
	}})
	defer reg.UnregisterClient(clientID)

	input := []byte(`{"model":"test-claude-preserve-max-tokens-model","max_tokens":2048,"messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, modelID)

	if got := gjson.GetBytes(out, "max_tokens").Int(); got != 2048 {
		t.Fatalf("max_tokens = %d, want %d", got, 2048)
	}
}

func TestEnsureModelMaxTokens_SkipsUnregisteredModel(t *testing.T) {
	input := []byte(`{"model":"test-claude-unregistered-model","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureModelMaxTokens(input, "test-claude-unregistered-model")

	if gjson.GetBytes(out, "max_tokens").Exists() {
		t.Fatalf("max_tokens should remain unset, got %s", gjson.GetBytes(out, "max_tokens").Raw)
	}
}

// TestClaudeExecutor_ExecuteStream_SetsIdentityAcceptEncoding verifies that streaming
// requests use Accept-Encoding: identity so the upstream cannot respond with a
// compressed SSE body that would silently break the line scanner.
func TestClaudeExecutor_ExecuteStream_SetsIdentityAcceptEncoding(t *testing.T) {
	var gotEncoding, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}

	if gotEncoding != "identity" {
		t.Errorf("Accept-Encoding = %q, want %q", gotEncoding, "identity")
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept = %q, want %q", gotAccept, "text/event-stream")
	}
}

// TestClaudeExecutor_Execute_SetsCompressedAcceptEncoding verifies that non-streaming
// requests keep the full accept-encoding to allow response compression (which
// decodeResponseBody handles correctly).
func TestClaudeExecutor_Execute_SetsCompressedAcceptEncoding(t *testing.T) {
	var gotEncoding, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet-20241022","role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if gotEncoding != "gzip, deflate, br, zstd" {
		t.Errorf("Accept-Encoding = %q, want %q", gotEncoding, "gzip, deflate, br, zstd")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/json")
	}
}

// TestClaudeExecutor_ExecuteStream_GzipSuccessBodyDecoded verifies that a streaming
// HTTP 200 response with Content-Encoding: gzip is correctly decompressed before
// the line scanner runs, so SSE chunks are not silently dropped.
func TestClaudeExecutor_ExecuteStream_GzipSuccessBodyDecoded(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("data: {\"type\":\"message_stop\"}\n"))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var combined strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		combined.Write(chunk.Payload)
	}

	if combined.Len() == 0 {
		t.Fatal("expected at least one chunk from gzip-encoded SSE body, got none (body was not decompressed)")
	}
	if !strings.Contains(combined.String(), "message_stop") {
		t.Errorf("expected SSE content in chunks, got: %q", combined.String())
	}
}

// TestDecodeResponseBody_MagicByteGzipNoHeader verifies that decodeResponseBody
// detects gzip-compressed content via magic bytes even when Content-Encoding is absent.
func TestDecodeResponseBody_MagicByteGzipNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(plaintext))
	_ = gz.Close()

	rc := io.NopCloser(&buf)
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestDecodeResponseBody_MagicByteZstdNoHeader verifies that decodeResponseBody
// detects zstd-compressed content via magic bytes even when Content-Encoding is absent.
func TestDecodeResponseBody_MagicByteZstdNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"

	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	_, _ = enc.Write([]byte(plaintext))
	_ = enc.Close()

	rc := io.NopCloser(&buf)
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestDecodeResponseBody_PlainTextNoHeader verifies that decodeResponseBody returns
// plain text untouched when Content-Encoding is absent and no magic bytes match.
func TestDecodeResponseBody_PlainTextNoHeader(t *testing.T) {
	const plaintext = "data: {\"type\":\"message_stop\"}\n"
	rc := io.NopCloser(strings.NewReader(plaintext))
	decoded, err := decodeResponseBody(rc, "")
	if err != nil {
		t.Fatalf("decodeResponseBody error: %v", err)
	}
	defer decoded.Close()

	got, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decoded = %q, want %q", got, plaintext)
	}
}

// TestClaudeExecutor_ExecuteStream_GzipNoContentEncodingHeader verifies the full
// pipeline: when the upstream returns a gzip-compressed SSE body WITHOUT setting
// Content-Encoding (a misbehaving upstream), the magic-byte sniff in
// decodeResponseBody still decompresses it, so chunks reach the caller.
func TestClaudeExecutor_ExecuteStream_GzipNoContentEncodingHeader(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("data: {\"type\":\"message_stop\"}\n"))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}

	var combined strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		combined.Write(chunk.Payload)
	}

	if combined.Len() == 0 {
		t.Fatal("expected chunks from gzip body without Content-Encoding header, got none (magic-byte sniff failed)")
	}
	if !strings.Contains(combined.String(), "message_stop") {
		t.Errorf("unexpected chunk content: %q", combined.String())
	}
}

// TestClaudeExecutor_Execute_GzipErrorBodyNoContentEncodingHeader verifies that the
// error path (4xx) correctly decompresses a gzip body even when the upstream omits
// the Content-Encoding header.  This closes the gap left by PR #1771, which only
// fixed header-declared compression on the error path.
func TestClaudeExecutor_Execute_GzipErrorBodyNoContentEncodingHeader(t *testing.T) {
	const errJSON = `{"type":"error","error":{"type":"invalid_request_error","message":"test error"}}`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(errJSON))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err == nil {
		t.Fatal("expected an error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "test error") {
		t.Errorf("error message should contain decompressed JSON, got: %q", err.Error())
	}
}

// TestClaudeExecutor_ExecuteStream_GzipErrorBodyNoContentEncodingHeader verifies
// the same for the streaming executor: 4xx gzip body without Content-Encoding is
// decoded and the error message is readable.
func TestClaudeExecutor_ExecuteStream_GzipErrorBodyNoContentEncodingHeader(t *testing.T) {
	const errJSON = `{"type":"error","error":{"type":"invalid_request_error","message":"stream test error"}}`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(errJSON))
	_ = gz.Close()
	compressedBody := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Intentionally omit Content-Encoding to simulate misbehaving upstream.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(compressedBody)
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err == nil {
		t.Fatal("expected an error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "stream test error") {
		t.Errorf("error message should contain decompressed JSON, got: %q", err.Error())
	}
}

// TestClaudeExecutor_ExecuteStream_AcceptEncodingOverrideCannotBypassIdentity verifies that the
// streaming executor enforces Accept-Encoding: identity regardless of auth.Attributes override.
func TestClaudeExecutor_ExecuteStream_AcceptEncodingOverrideCannotBypassIdentity(t *testing.T) {
	var gotEncoding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":                "key-123",
		"base_url":               server.URL,
		"header:Accept-Encoding": "gzip, deflate, br, zstd",
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}

	if gotEncoding != "identity" {
		t.Errorf("Accept-Encoding = %q; stream path must enforce identity regardless of auth.Attributes override", gotEncoding)
	}
}

func expectedClaudeCodeStaticPrompt() string {
	return strings.Join([]string{
		helps.ClaudeCodeIntro,
		helps.ClaudeCodeSystem,
		helps.ClaudeCodeDoingTasks,
		helps.ClaudeCodeToneAndStyle,
		helps.ClaudeCodeOutputEfficiency,
	}, "\n\n")
}

func expectedForwardedSystemReminder(text string) string {
	return fmt.Sprintf(`<system-reminder>
As you answer the user's questions, you can use the following context from the system:
%s

IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.
</system-reminder>
`, text)
}

// Test case 1: String system prompt is preserved by forwarding it to the first user message
func TestCheckSystemInstructionsWithMode_StringSystemPreserved(t *testing.T) {
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false)

	system := gjson.GetBytes(out, "system")
	if !system.IsArray() {
		t.Fatalf("system should be an array, got %s", system.Type)
	}

	blocks := system.Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}

	if !strings.HasPrefix(blocks[0].Get("text").String(), "x-anthropic-billing-header:") {
		t.Fatalf("blocks[0] should be billing header, got %q", blocks[0].Get("text").String())
	}
	if blocks[1].Get("text").String() != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("blocks[1] should be agent block, got %q", blocks[1].Get("text").String())
	}
	if blocks[2].Get("text").String() != expectedClaudeCodeStaticPrompt() {
		t.Fatalf("blocks[2] should be static Claude Code prompt, got %q", blocks[2].Get("text").String())
	}
	if blocks[2].Get("cache_control").Exists() {
		t.Fatalf("blocks[2] should not have cache_control, got %s", blocks[2].Get("cache_control").Raw)
	}

	if got := gjson.GetBytes(out, "messages.0.content").String(); got != expectedForwardedSystemReminder("You are a helpful assistant.")+"hi" {
		t.Fatalf("messages[0].content should include forwarded system prompt, got %q", got)
	}
}

// Test case 2: Strict mode keeps only the injected Claude Code system blocks
func TestCheckSystemInstructionsWithMode_StringSystemStrict(t *testing.T) {
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, true)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("strict mode should produce 3 injected blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "hi" {
		t.Fatalf("strict mode should not forward system prompt into messages, got %q", got)
	}
}

// Test case 3: Empty string system prompt does not alter the first user message
func TestCheckSystemInstructionsWithMode_EmptyStringSystemIgnored(t *testing.T) {
	payload := []byte(`{"system":"","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("empty string system should still produce 3 injected blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "hi" {
		t.Fatalf("empty string system should not alter messages, got %q", got)
	}
}

// Test case 4: Array system prompt is forwarded to the first user message
func TestCheckSystemInstructionsWithMode_ArraySystemStillWorks(t *testing.T) {
	payload := []byte(`{"system":[{"type":"text","text":"Be concise."}],"messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}
	if blocks[2].Get("text").String() != expectedClaudeCodeStaticPrompt() {
		t.Fatalf("blocks[2] should be static Claude Code prompt, got %q", blocks[2].Get("text").String())
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != expectedForwardedSystemReminder("Be concise.")+"hi" {
		t.Fatalf("messages[0].content should include forwarded array system prompt, got %q", got)
	}
}

// Test case 5: Special characters in string system prompt survive forwarding
func TestCheckSystemInstructionsWithMode_StringWithSpecialChars(t *testing.T) {
	payload := []byte(`{"system":"Use <xml> tags & \"quotes\" in output.","messages":[{"role":"user","content":"hi"}]}`)

	out := checkSystemInstructionsWithMode(payload, false)

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != expectedForwardedSystemReminder(`Use <xml> tags & "quotes" in output.`)+"hi" {
		t.Fatalf("forwarded system prompt text mangled, got %q", got)
	}
}

func TestClaudeExecutor_ExperimentalCCHSigningDisabledByDefaultKeepsLegacyHeader(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(seenBody) == 0 {
		t.Fatal("expected request body to be captured")
	}

	billingHeader := gjson.GetBytes(seenBody, "system.0.text").String()
	if !strings.HasPrefix(billingHeader, "x-anthropic-billing-header:") {
		t.Fatalf("system.0.text = %q, want billing header", billingHeader)
	}
	if strings.Contains(billingHeader, "cch=00000;") {
		t.Fatalf("legacy mode should not forward cch placeholder, got %q", billingHeader)
	}
}

func TestClaudeExecutor_ExperimentalCCHSigningOptInSignsFinalBody(t *testing.T) {
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody = bytes.Clone(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-3-5-sonnet","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{
		ClaudeKey: []config.ClaudeKey{{
			APIKey:                 "key-123",
			BaseURL:                server.URL,
			ExperimentalCCHSigning: true,
		}},
	})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "key-123",
		"base_url": server.URL,
	}}
	const messageText = "please keep literal cch=00000 in this message"
	payload := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"please keep literal cch=00000 in this message"}]}]}`)

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(seenBody) == 0 {
		t.Fatal("expected request body to be captured")
	}
	if got := gjson.GetBytes(seenBody, "messages.0.content.0.text").String(); got != messageText {
		t.Fatalf("message text = %q, want %q", got, messageText)
	}

	billingPattern := regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)([0-9a-f]{5})(;)`)
	match := billingPattern.FindSubmatch(seenBody)
	if match == nil {
		t.Fatalf("expected signed billing header in body: %s", string(seenBody))
	}
	actualCCH := string(match[2])
	unsignedBody := billingPattern.ReplaceAll(seenBody, []byte(`${1}00000${3}`))
	wantCCH := fmt.Sprintf("%05x", xxHash64.Checksum(unsignedBody, 0x6E52736AC806831E)&0xFFFFF)
	if actualCCH != wantCCH {
		t.Fatalf("cch = %q, want %q\nbody: %s", actualCCH, wantCCH, string(seenBody))
	}
}

func TestApplyCloaking_PreservesConfiguredStrictModeAndSensitiveWordsWhenModeOmitted(t *testing.T) {
	cfg := &config.Config{
		ClaudeKey: []config.ClaudeKey{{
			APIKey: "key-123",
			Cloak: &config.CloakConfig{
				StrictMode:     true,
				SensitiveWords: []string{"proxy"},
			},
		}},
	}
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "key-123"}}
	payload := []byte(`{"system":"proxy rules","messages":[{"role":"user","content":[{"type":"text","text":"proxy access"}]}]}`)

	out := applyCloaking(context.Background(), cfg, auth, payload, "claude-3-5-sonnet-20241022", "key-123")

	blocks := gjson.GetBytes(out, "system").Array()
	if len(blocks) != 3 {
		t.Fatalf("expected strict mode to keep the 3 injected Claude Code system blocks, got %d", len(blocks))
	}
	if got := gjson.GetBytes(out, "messages.0.content.#").Int(); got != 1 {
		t.Fatalf("strict mode should not prepend a forwarded system reminder block, got %d content blocks", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.text").String(); !strings.Contains(got, "\u200B") {
		t.Fatalf("expected configured sensitive word obfuscation to apply, got %q", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_AdaptiveCoercesToOne(t *testing.T) {
	payload := []byte(`{"temperature":0,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_EnabledCoercesToOne(t *testing.T) {
	payload := []byte(`{"temperature":0.2,"thinking":{"type":"enabled","budget_tokens":2048}}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_NoThinkingLeavesTemperatureAlone(t *testing.T) {
	payload := []byte(`{"temperature":0,"messages":[{"role":"user","content":"hi"}]}`)
	out := normalizeClaudeTemperatureForThinking(payload)

	if got := gjson.GetBytes(out, "temperature").Float(); got != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
}

func TestNormalizeClaudeTemperatureForThinking_AfterForcedToolChoiceKeepsOriginalTemperature(t *testing.T) {
	payload := []byte(`{"temperature":0,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"tool_choice":{"type":"any"}}`)
	out := disableThinkingIfToolChoiceForced(payload)
	out = normalizeClaudeTemperatureForThinking(out)

	if gjson.GetBytes(out, "thinking").Exists() {
		t.Fatalf("thinking should be removed when tool_choice forces tool use")
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
}

func TestRemapOAuthToolNames_CustomToolNameAndSchemaPreserved(t *testing.T) {
	body := []byte(`{"tools":[{"name":"AskUserQuestion","description":"Ask the user","input_schema":{"type":"object","properties":{"questions":{"type":"array","items":{"type":"string"}}},"required":["questions"]}},{"name":"question","description":"custom question tool","input_schema":{"type":"object","properties":{"questions":{"type":"array","items":{"type":"string"}}}}}],"tool_choice":{"type":"tool","name":"AskUserQuestion"},"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	out, reverseMap := remapOAuthToolNames(body)
	if len(reverseMap) != 0 {
		t.Fatalf("reverseMap = %v, want empty", reverseMap)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "AskUserQuestion" {
		t.Fatalf("tools.0.name = %q, want %q", got, "AskUserQuestion")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "question" {
		t.Fatalf("tools.1.name = %q, want %q", got, "question")
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "AskUserQuestion" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "AskUserQuestion")
	}
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.questions.type").String(); got != "array" {
		t.Fatalf("questions schema type = %q, want array", got)
	}

	resp := []byte(`{"content":[{"type":"tool_use","id":"toolu_01","name":"AskUserQuestion","input":{"question":"bad shape"}}]}`)
	reversed := reverseRemapOAuthToolNames(resp, reverseMap)
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "AskUserQuestion" {
		t.Fatalf("content.0.name = %q, want %q", got, "AskUserQuestion")
	}
}

func TestRemapOAuthToolNames_StaticSessionPrefix_RenamesAndRestores(t *testing.T) {
	body := []byte(`{"tools":[{"name":"sessions_list","input_schema":{"type":"object"}},{"name":"session_get","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}],"tool_choice":{"type":"tool","name":"sessions_list"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"session_get","input":{}}]}]}`)

	out, reverseMap := remapOAuthToolNames(body)
	if reverseMap["cc_sess_list"] != "sessions_list" || reverseMap["cc_ses_get"] != "session_get" {
		t.Fatalf("reverseMap = %v, want static session prefix entries", reverseMap)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "cc_sess_list" {
		t.Fatalf("tools.0.name = %q, want %q", got, "cc_sess_list")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "cc_ses_get" {
		t.Fatalf("tools.1.name = %q, want %q", got, "cc_ses_get")
	}
	if got := gjson.GetBytes(out, "tools.2.name").String(); got != "web_search" {
		t.Fatalf("server tool name = %q, want web_search", got)
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "cc_sess_list" {
		t.Fatalf("tool_choice.name = %q, want %q", got, "cc_sess_list")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "cc_ses_get" {
		t.Fatalf("message tool_use name = %q, want %q", got, "cc_ses_get")
	}

	resp := []byte(`{"content":[{"type":"tool_use","id":"toolu_01","name":"cc_sess_list","input":{}}]}`)
	reversed := reverseRemapOAuthToolNames(resp, reverseMap)
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "sessions_list" {
		t.Fatalf("content.0.name = %q, want %q", got, "sessions_list")
	}
}

func TestRemapOAuthToolNames_SmallToolSetDoesNotTitleCaseBuiltins(t *testing.T) {
	body := []byte(`{"tools":[` +
		`{"name":"Bash","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}},` +
		`{"name":"glob","input_schema":{"type":"object","properties":{"filePattern":{"type":"string"}}}}` +
		`]}`)

	out, reverseMap := remapOAuthToolNames(body)
	if len(reverseMap) != 0 {
		t.Fatalf("reverseMap = %v, want empty", reverseMap)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "Bash" {
		t.Fatalf("tools.0.name = %q, want %q", got, "Bash")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "glob" {
		t.Fatalf("tools.1.name = %q, want %q", got, "glob")
	}

	resp := []byte(`{"content":[{"type":"tool_use","id":"toolu_01","name":"glob","input":{"filePattern":"**/*.go"}}]}`)
	reversed := reverseRemapOAuthToolNames(resp, reverseMap)
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "glob" {
		t.Fatalf("content.0.name = %q, want %q", got, "glob")
	}
}

func TestRemapOAuthToolNames_DynamicMap_RenamesAndRestores(t *testing.T) {
	body := []byte(`{"tools":[` +
		`{"name":"alpha_search","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}},` +
		`{"name":"beta_lookup","input_schema":{"type":"object"}},` +
		`{"name":"gamma_fetch","input_schema":{"type":"object"}},` +
		`{"name":"delta_update","input_schema":{"type":"object"}},` +
		`{"name":"epsilon_parse","input_schema":{"type":"object"}},` +
		`{"name":"zeta_render","input_schema":{"type":"object"}},` +
		`{"type":"web_search_20250305","name":"web_search"}` +
		`],"tool_choice":{"type":"tool","name":"gamma_fetch"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"gamma_fetch","input":{}}]}]}`)

	out, reverseMap := remapOAuthToolNames(body)
	if len(reverseMap) != 6 {
		t.Fatalf("reverseMap length = %d, want 6: %v", len(reverseMap), reverseMap)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got == "alpha_search" {
		t.Fatalf("dynamic map should rename alpha_search")
	}
	if got := gjson.GetBytes(out, "tools.0.input_schema.properties.q.type").String(); got != "string" {
		t.Fatalf("schema should be preserved, got q.type=%q", got)
	}
	if got := gjson.GetBytes(out, "tools.6.name").String(); got != "web_search" {
		t.Fatalf("server tool name = %q, want web_search", got)
	}

	var fakeGamma string
	for fake, real := range reverseMap {
		if real == "gamma_fetch" {
			fakeGamma = fake
			break
		}
	}
	if fakeGamma == "" {
		t.Fatalf("reverseMap missing gamma_fetch: %v", reverseMap)
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != fakeGamma {
		t.Fatalf("tool_choice.name = %q, want %q", got, fakeGamma)
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != fakeGamma {
		t.Fatalf("message tool_use name = %q, want %q", got, fakeGamma)
	}

	resp := []byte(fmt.Sprintf(`{"content":[{"type":"tool_use","id":"toolu_01","name":%q,"input":{}}]}`, fakeGamma))
	reversed := reverseRemapOAuthToolNames(resp, reverseMap)
	if got := gjson.GetBytes(reversed, "content.0.name").String(); got != "gamma_fetch" {
		t.Fatalf("content.0.name = %q, want gamma_fetch", got)
	}
}

// TestReverseRemapOAuthToolNamesFromStreamLine_HonorsPerRequestMap guards the
// SSE streaming code path against the same mixed-case bug.
func TestReverseRemapOAuthToolNamesFromStreamLine_HonorsPerRequestMap(t *testing.T) {
	reverseMap := map[string]string{"Glob": "glob"}

	// Bash block was never renamed, must pass through as-is.
	bashLine := []byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"Bash","input":{}}}`)
	out := reverseRemapOAuthToolNamesFromStreamLine(bashLine, reverseMap)
	if !bytes.Contains(out, []byte(`"name":"Bash"`)) {
		t.Fatalf("Bash should be preserved, got: %s", string(out))
	}
	if bytes.Contains(out, []byte(`"name":"bash"`)) {
		t.Fatalf("Bash must not be lowercased, got: %s", string(out))
	}

	// Glob block IS in the reverseMap, must be restored to `glob`.
	globLine := []byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_02","name":"Glob","input":{}}}`)
	out = reverseRemapOAuthToolNamesFromStreamLine(globLine, reverseMap)
	if !bytes.Contains(out, []byte(`"name":"glob"`)) {
		t.Fatalf("Glob should be restored to glob, got: %s", string(out))
	}
}

func TestPrepareClaudeOAuthToolNamesForUpstream_MixedCaseWithPrefix(t *testing.T) {
	body := []byte(`{"tools":[` +
		`{"name":"Bash","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}},` +
		`{"name":"glob","input_schema":{"type":"object","properties":{"filePattern":{"type":"string"}}}}` +
		`],"messages":[{"role":"assistant","content":[` +
		`{"type":"tool_use","id":"toolu_01","name":"Bash","input":{}},` +
		`{"type":"tool_use","id":"toolu_02","name":"glob","input":{}}` +
		`]}]}`)

	out, reverseMap := prepareClaudeOAuthToolNamesForUpstream(body, "proxy_", false)

	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "proxy_Bash" {
		t.Fatalf("tools.0.name = %q, want %q", got, "proxy_Bash")
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "proxy_glob" {
		t.Fatalf("tools.1.name = %q, want %q", got, "proxy_glob")
	}
	if got := gjson.GetBytes(out, "messages.0.content.0.name").String(); got != "proxy_Bash" {
		t.Fatalf("messages.0.content.0.name = %q, want %q", got, "proxy_Bash")
	}
	if got := gjson.GetBytes(out, "messages.0.content.1.name").String(); got != "proxy_glob" {
		t.Fatalf("messages.0.content.1.name = %q, want %q", got, "proxy_glob")
	}
	if len(reverseMap) != 0 {
		t.Fatalf("reverseMap = %v, want empty", reverseMap)
	}
}

func TestRestoreClaudeOAuthToolNamesFromResponse_MixedCaseWithPrefix(t *testing.T) {
	reverseMap := map[string]string{}
	resp := []byte(`{"content":[` +
		`{"type":"tool_use","id":"toolu_01","name":"proxy_Bash","input":{}},` +
		`{"type":"tool_use","id":"toolu_02","name":"proxy_glob","input":{}}` +
		`]}`)

	out := restoreClaudeOAuthToolNamesFromResponse(resp, "proxy_", false, reverseMap)

	if got := gjson.GetBytes(out, "content.0.name").String(); got != "Bash" {
		t.Fatalf("content.0.name = %q, want %q", got, "Bash")
	}
	if got := gjson.GetBytes(out, "content.1.name").String(); got != "glob" {
		t.Fatalf("content.1.name = %q, want %q", got, "glob")
	}
}

func TestRestoreClaudeOAuthToolNamesFromStreamLine_MixedCaseWithPrefix(t *testing.T) {
	reverseMap := map[string]string{}

	bashLine := []byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"proxy_Bash","input":{}}}`)
	out := restoreClaudeOAuthToolNamesFromStreamLine(bashLine, "proxy_", false, reverseMap)
	if !bytes.Contains(out, []byte(`"name":"Bash"`)) {
		t.Fatalf("Bash should be preserved, got: %s", string(out))
	}
	if bytes.Contains(out, []byte(`"name":"bash"`)) {
		t.Fatalf("Bash must not be lowercased, got: %s", string(out))
	}

	globLine := []byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_02","name":"proxy_glob","input":{}}}`)
	out = restoreClaudeOAuthToolNamesFromStreamLine(globLine, "proxy_", false, reverseMap)
	if !bytes.Contains(out, []byte(`"name":"glob"`)) {
		t.Fatalf("Glob should be restored to glob, got: %s", string(out))
	}
}
