package resourcepool

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

var quotaRefresherOnce sync.Once

// StartAccountQuotaRefresher starts the background Claude OAuth usage refresher once per process.
func StartAccountQuotaRefresher(ctx context.Context, configFilePath string, cfgProvider func() *config.Config) {
	quotaRefresherOnce.Do(func() {
		go runAccountQuotaRefresher(ctx, configFilePath, cfgProvider)
	})
}

func runAccountQuotaRefresher(ctx context.Context, configFilePath string, cfgProvider func() *config.Config) {
	log.Info("resource pool account quota refresher started")
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
			log.WithError(err).Warn("open resource pool store for quota refresher failed")
			if !sleepOrDone(ctx, time.Minute) {
				return
			}
			continue
		}
		doc, err := store.GetConfig(ctx)
		if errClose := store.Close(); errClose != nil {
			log.WithError(errClose).Warn("close resource pool store after quota config load failed")
		}
		if err != nil {
			log.WithError(err).Warn("load resource pool config for quota refresher failed")
			if !sleepOrDone(ctx, time.Minute) {
				return
			}
			continue
		}
		quotaCfg := EffectiveAccountQuota(doc.AccountQuota)
		if quotaCfg.Enabled != nil && !*quotaCfg.Enabled {
			if !sleepOrDone(ctx, quotaInterval(quotaCfg)) {
				return
			}
			continue
		}
		runOneAccountQuotaSweep(ctx, configFilePath, cfg, quotaCfg)
		if !sleepOrDone(ctx, quotaInterval(quotaCfg)) {
			return
		}
	}
}

func runOneAccountQuotaSweep(ctx context.Context, configFilePath string, cfg *config.Config, quotaCfg AccountQuotaConfig) {
	store, err := Open(configFilePath, cfg)
	if err != nil {
		log.WithError(err).Warn("open resource pool store for quota sweep failed")
		return
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			log.WithError(errClose).Warn("close resource pool store after quota sweep failed")
		}
	}()
	accounts, err := store.ListAccounts(ctx)
	if err != nil {
		log.WithError(err).Warn("list claude code accounts for quota sweep failed")
		return
	}
	concurrency := quotaCfg.Concurrency
	if concurrency <= 0 {
		concurrency = quotaDefaultConcurrent
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, account := range accounts {
		account := account
		if !account.Enabled || !account.HasAuthData || strings.TrimSpace(account.ID) == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			accountStore, err := Open(configFilePath, cfg)
			if err != nil {
				log.WithError(err).WithField("account_id", account.ID).Debug("open resource pool store for account quota refresh failed")
				return
			}
			defer func() {
				if errClose := accountStore.Close(); errClose != nil {
					log.WithError(errClose).Warn("close resource pool store after account quota refresh failed")
				}
			}()
			if _, err := RefreshStoredAccountQuota(ctx, configFilePath, cfg, accountStore, account.ID); err != nil {
				log.WithError(err).WithField("account_id", account.ID).Debug("refresh claude code account quota failed")
			}
			PublishAccountChanged(account.ID, "quota")
		}()
	}
	wg.Wait()
}

func quotaInterval(quotaCfg AccountQuotaConfig) time.Duration {
	interval, err := time.ParseDuration(strings.TrimSpace(quotaCfg.Interval))
	if err != nil || interval <= 0 {
		return quotaDefaultInterval
	}
	return interval
}
