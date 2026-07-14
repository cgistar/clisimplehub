package xaiplugin

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	xai "clisimplehub/internal/xai"
	xaiShared "clisimplehub/internal/xai/shared"
)

const (
	defaultTokenRefreshInterval = time.Minute
	tokenRefreshLead            = 5 * time.Minute
	tokenRefreshAccountTimeout  = 45 * time.Second
)

func (s *XaiService) reconcileTokenRefreshScheduler(pool *xai.XaiAccountPool) {
	enabled := false
	if pool != nil {
		if snapshot := pool.Snapshot(); snapshot != nil {
			enabled = snapshot.Config.AutoRefreshTokenEnabled()
		}
	}
	if enabled {
		s.startTokenRefreshScheduler(pool)
		return
	}
	s.stopTokenRefreshScheduler()
}

func (s *XaiService) startTokenRefreshScheduler(pool *xai.XaiAccountPool) {
	if s == nil || pool == nil {
		return
	}
	s.tokenRefreshMu.Lock()
	if s.tokenRefreshCancel != nil {
		s.tokenRefreshMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.tokenRefreshCancel = cancel
	s.tokenRefreshDone = done
	interval := s.tokenRefreshInterval
	if interval <= 0 {
		interval = defaultTokenRefreshInterval
	}
	refreshFn := s.tokenRefreshFn
	if refreshFn == nil {
		refreshFn = refreshScheduledAccount
	}
	s.tokenRefreshMu.Unlock()

	go func() {
		defer close(done)
		s.runTokenRefreshCycle(ctx, pool, refreshFn)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runTokenRefreshCycle(ctx, pool, refreshFn)
			}
		}
	}()
}

func (s *XaiService) stopTokenRefreshScheduler() {
	if s == nil {
		return
	}
	s.tokenRefreshMu.Lock()
	cancel := s.tokenRefreshCancel
	done := s.tokenRefreshDone
	s.tokenRefreshCancel = nil
	s.tokenRefreshDone = nil
	s.tokenRefreshMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (s *XaiService) runTokenRefreshCycle(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	refreshFn func(context.Context, *xai.XaiAccountPool, *xaiShared.XaiAccount, string) error,
) {
	if ctx.Err() != nil || pool == nil {
		return
	}
	now := time.Now().UTC()
	for _, snapshot := range pool.ListAccounts() {
		if ctx.Err() != nil {
			return
		}
		account := snapshot
		if !shouldAutoRefreshToken(&account, now) {
			continue
		}
		accountCtx, cancel := context.WithTimeout(ctx, tokenRefreshAccountTimeout)
		err := refreshFn(accountCtx, pool, &account, resolveAccountProxy(pool, &account))
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("[xAI] automatic token refresh failed for account %s: %v", strings.TrimSpace(account.ID), err)
		}
	}
}

func shouldAutoRefreshToken(account *xaiShared.XaiAccount, now time.Time) bool {
	if account == nil || !account.IsEnabled() {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(account.AuthKind), xaiShared.AuthKindOAuth) {
		return false
	}
	if strings.TrimSpace(account.RefreshToken) == "" {
		return false
	}
	if strings.TrimSpace(account.AccessToken) == "" {
		return true
	}
	if account.ExpiresAt.IsZero() {
		return account.LastRefresh.IsZero() || now.Sub(account.LastRefresh) >= tokenRefreshLead
	}
	return !account.ExpiresAt.After(now.Add(tokenRefreshLead))
}

func refreshScheduledAccount(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string) error {
	if pool != nil && account != nil {
		for _, current := range pool.ListAccounts() {
			if strings.TrimSpace(current.ID) != strings.TrimSpace(account.ID) {
				continue
			}
			if !shouldAutoRefreshToken(&current, time.Now().UTC()) {
				return nil
			}
			account = &current
			proxyURL = resolveAccountProxy(pool, account)
			break
		}
	}
	updated, err := refreshOAuthAccount(ctx, pool, account, proxyURL, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(updated.AccessToken) == "" {
		return fmt.Errorf("refreshed access token is empty")
	}
	return nil
}
