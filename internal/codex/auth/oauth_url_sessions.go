package auth

import (
	"context"
	"fmt"
	"sync"
)

type pendingOAuthURLLogin struct {
	pkce     *PKCECodes
	proxyURL string
	ctx      context.Context
	cancel   context.CancelFunc
}

var (
	pendingOAuthURLMu       sync.Mutex
	pendingOAuthURLAttempts sync.Mutex
	pendingOAuthURLLogins   = make(map[string]*pendingOAuthURLLogin)
)

func cacheOAuthURLLogin(state string, pkce *PKCECodes, proxyURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), OAuthLoginTimeout)
	sess := &pendingOAuthURLLogin{pkce: pkce, proxyURL: proxyURL, ctx: ctx, cancel: cancel}
	pendingOAuthURLMu.Lock()
	pendingOAuthURLLogins[state] = sess
	pendingOAuthURLMu.Unlock()
	go func() {
		<-ctx.Done()
		pendingOAuthURLMu.Lock()
		if pendingOAuthURLLogins[state] == sess {
			delete(pendingOAuthURLLogins, state)
		}
		pendingOAuthURLMu.Unlock()
	}()
}

func exchangePendingOAuthURLCode(state, code string) (*CodexLoginResult, error) {
	return completePendingOAuthURLCode(state, code, ExchangeCodeForTokens)
}

func completePendingOAuthURLCode(state, code string, exchange func(context.Context, string, *PKCECodes, string) (*CodexLoginResult, error)) (*CodexLoginResult, error) {
	// 旧链接和新链接共用监听端口，但必须使用各自的 PKCE 和代理配置。
	pendingOAuthURLAttempts.Lock()
	defer pendingOAuthURLAttempts.Unlock()
	pendingOAuthURLMu.Lock()
	sess := pendingOAuthURLLogins[state]
	pendingOAuthURLMu.Unlock()
	if sess == nil || sess.ctx.Err() != nil {
		return nil, fmt.Errorf("login session not found or expired; generate a new login link")
	}
	result, err := exchange(sess.ctx, code, sess.pkce, sess.proxyURL)
	if err != nil {
		return nil, err
	}
	clearPendingOAuthURLLogins()
	return result, nil
}

func clearPendingOAuthURLLogins() {
	pendingOAuthURLMu.Lock()
	defer pendingOAuthURLMu.Unlock()
	for state, sess := range pendingOAuthURLLogins {
		delete(pendingOAuthURLLogins, state)
		sess.cancel()
	}
}
