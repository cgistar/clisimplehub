package appdb

import (
	"fmt"
	"log"
	"time"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/proxy"
	"clisimplehub/internal/statsdb"
)

const oldStoreCloseGracePeriod = 10 * time.Second

type codexAccountStoreReplacer interface {
	ReplaceAccountStore(codexShared.CodexAccountStore) (codexShared.CodexAccountStore, error)
}

func ApplyRuntimeDatabase(cfg dbconfig.Config, proxyServer *proxy.ProxyServer) error {
	if proxyServer == nil {
		return fmt.Errorf("proxy server is nil")
	}

	usageStore, err := statsdb.OpenUsageStatsStore(cfg)
	if err != nil {
		return fmt.Errorf("open usage stats store: %w", err)
	}
	accountStore, err := codexShared.OpenCodexAccountStoreWithConfig(cfg)
	if err != nil {
		_ = usageStore.Close()
		return fmt.Errorf("open codex account store: %w", err)
	}

	oldUsageStore := proxyServer.ReplaceUsageStatsStore(usageStore)
	var oldAccountStore codexShared.CodexAccountStore
	replacedAccountStore := false
	for _, pl := range plugin.All() {
		replacer, ok := pl.(codexAccountStoreReplacer)
		if !ok {
			continue
		}
		oldAccountStore, err = replacer.ReplaceAccountStore(accountStore)
		if err != nil {
			_ = proxyServer.ReplaceUsageStatsStore(oldUsageStore)
			_ = usageStore.Close()
			_ = accountStore.Close()
			return fmt.Errorf("replace codex account store: %w", err)
		}
		replacedAccountStore = true
		break
	}

	closeOldStoreLater("usage stats", oldUsageStore)
	closeOldStoreLater("codex account", oldAccountStore)
	if !replacedAccountStore {
		_ = accountStore.Close()
	}

	log.Printf("Runtime database switched (%s): %s", cfg.Driver, dbconfig.DisplaySource(cfg))
	return nil
}

type closeableStore interface {
	Close() error
}

func closeOldStoreLater(name string, store closeableStore) {
	if store == nil {
		return
	}
	go func() {
		time.Sleep(oldStoreCloseGracePeriod)
		if err := store.Close(); err != nil {
			log.Printf("Warning: failed to close old %s db: %v", name, err)
		}
	}()
}
