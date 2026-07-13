package resourcepool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

var healthCheckerOnce sync.Once

// StartHealthChecker starts the background proxy health worker once per process.
func StartHealthChecker(ctx context.Context, configFilePath string, cfgProvider func() *config.Config) {
	healthCheckerOnce.Do(func() {
		go runHealthChecker(ctx, configFilePath, cfgProvider)
	})
}

func runHealthChecker(ctx context.Context, configFilePath string, cfgProvider func() *config.Config) {
	log.Info("resource pool proxy health checker started")
	for {
		cfg := currentResourcePoolConfig(cfgProvider)
		if cfg == nil || !cfg.ResourcePools.Enabled {
			if !sleepOrDone(ctx, time.Minute) {
				return
			}
			continue
		}
		store, err := Open(configFilePath, cfg)
		if err != nil {
			log.WithError(err).Warn("open resource pool store for health checker config failed")
			if !sleepOrDone(ctx, time.Minute) {
				return
			}
			continue
		}
		doc, err := store.GetConfig(ctx)
		if errClose := store.Close(); errClose != nil {
			log.WithError(errClose).Warn("close resource pool store after health checker config load failed")
		}
		if err != nil {
			log.WithError(err).Warn("load resource pool config for health checker failed")
			if !sleepOrDone(ctx, time.Minute) {
				return
			}
			continue
		}
		if err := ApplyClaudeCodePoolRuntimeConfig(ctx, configFilePath, cfg); err != nil {
			log.WithError(err).Warn("apply claude code account pool runtime config failed")
		}
		healthCfg := EffectiveProxyHealth(doc.ProxyHealth)
		if !healthCfg.Enabled {
			if !sleepOrDone(ctx, healthCfg.Interval) {
				return
			}
			continue
		}
		runOneHealthSweep(ctx, configFilePath, cfg, healthCfg)
		if !sleepOrDone(ctx, healthCfg.Interval) {
			return
		}
	}
}

func runOneHealthSweep(ctx context.Context, configFilePath string, cfg *config.Config, healthCfg EffectiveProxyHealthConfig) {
	store, err := Open(configFilePath, cfg)
	if err != nil {
		log.WithError(err).Warn("open resource pool store for health checker failed")
		return
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			log.WithError(errClose).Warn("close resource pool store after health sweep failed")
		}
	}()
	proxies, err := store.ListEnabledProxiesForHealth(ctx)
	if err != nil {
		log.WithError(err).Warn("list proxy resources for health checker failed")
		return
	}
	if len(proxies) == 0 {
		return
	}
	sem := make(chan struct{}, healthCfg.Concurrency)
	var wg sync.WaitGroup
	for _, proxy := range proxies {
		proxy := proxy
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := TestProxyAndStore(ctx, store, proxy.ID, healthCfg); err != nil {
				log.WithError(err).WithField("proxy_id", proxy.ID).Warn("proxy health test failed")
			}
		}()
	}
	wg.Wait()
}

// TestProxyAndStore runs one proxy test and writes the resulting health state.
func TestProxyAndStore(ctx context.Context, store *Store, proxyID string, healthCfg EffectiveProxyHealthConfig) (*HealthResult, error) {
	if store == nil {
		return nil, fmt.Errorf("resource pool store is nil")
	}
	proxy, err := store.GetProxy(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	ok, latency, errTest := TestProxy(ctx, *proxy, healthCfg)
	result, errUpdate := store.UpdateProxyHealth(ctx, proxy.ID, ok, latency, errTest, healthCfg.FailureThreshold)
	if errUpdate != nil {
		return nil, errUpdate
	}
	PublishProxyChanged(proxy.ID, "health")
	routingScope := ""
	var boundPoolID string
	if err := store.db.QueryRowContext(ctx, `SELECT pool_id FROM claude_code_accounts WHERE proxy_resource_id = ?`, proxy.ID).Scan(&boundPoolID); err == nil {
		routingScope = AccountRoutingScope(boundPoolID)
	}
	if result.HealthStatus == HealthHealthy {
		if routingScope != "" {
			claudeapipool.ClearScopedProxyBlock(routingScope, proxy.ID)
		}
	} else if result.HealthStatus == HealthUnhealthy {
		cooldown := healthCfg.Interval * 2
		if cooldown < 10*time.Minute {
			cooldown = 10 * time.Minute
		}
		if routingScope != "" {
			claudeapipool.BlockScopedProxy(routingScope, proxy.ID, cooldown)
		}
	}
	if errTest != nil {
		return result, errTest
	}
	return result, nil
}

// TestProxy verifies connectivity through one proxy without mutating storage.
func TestProxy(ctx context.Context, proxy ProxyResource, healthCfg EffectiveProxyHealthConfig) (bool, time.Duration, error) {
	transport, mode, err := proxyutil.BuildHTTPTransport(proxy.ProxyURL)
	if err != nil {
		return false, 0, err
	}
	if mode != proxyutil.ModeProxy || transport == nil {
		return false, 0, fmt.Errorf("proxy resource does not contain a concrete proxy")
	}
	timeout := healthCfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	testURL := healthCfg.TestURL
	if testURL == "" {
		testURL = "https://api.anthropic.com/"
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, testURL, nil)
	if err != nil {
		transport.CloseIdleConnections()
		return false, 0, err
	}
	client := &http.Client{Transport: transport}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	transport.CloseIdleConnections()
	if err != nil {
		return false, latency, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("close proxy health response body failed")
		}
	}()
	if resp.StatusCode >= http.StatusInternalServerError {
		return false, latency, fmt.Errorf("test url returned %s", resp.Status)
	}
	return true, latency, nil
}

func currentResourcePoolConfig(cfgProvider func() *config.Config) *config.Config {
	if cfgProvider == nil {
		return &config.Config{}
	}
	cfg := cfgProvider()
	if cfg == nil {
		return &config.Config{}
	}
	return cfg
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = time.Minute
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
