package xaiplugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	xai "clisimplehub/internal/xai"
	xaiAuth "clisimplehub/internal/xai/auth"
	xaiShared "clisimplehub/internal/xai/shared"

	"golang.org/x/sync/singleflight"
)

var refreshGroup singleflight.Group

// ensureAccessToken 返回可用 Bearer token；OAuth 临近过期时自动刷新。
func ensureAccessToken(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string) (string, error) {
	return accessToken(ctx, pool, account, proxyURL, false)
}

// refreshAccessToken 强制向 OAuth token endpoint 刷新，不接受 pool 中仍未过期的旧 access token。
func refreshAccessToken(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string) (string, error) {
	return accessToken(ctx, pool, account, proxyURL, true)
}

func accessToken(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string, force bool) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is nil")
	}
	if strings.EqualFold(account.AuthKind, xaiShared.AuthKindAPIKey) || (account.APIKey != "" && account.RefreshToken == "") {
		token := strings.TrimSpace(account.APIKey)
		if token == "" {
			return "", fmt.Errorf("api key is empty")
		}
		return token, nil
	}

	token := strings.TrimSpace(account.AccessToken)
	needRefresh := token == ""
	if !needRefresh && !account.ExpiresAt.IsZero() {
		if time.Until(account.ExpiresAt) <= xaiAuth.RefreshLead() {
			needRefresh = true
		}
	}
	if !needRefresh && !force {
		return token, nil
	}
	if strings.TrimSpace(account.RefreshToken) == "" {
		if token != "" {
			return token, nil
		}
		return "", fmt.Errorf("refresh token is empty")
	}

	accountID := strings.TrimSpace(account.ID)
	key := accountID
	if key == "" {
		key = strings.TrimSpace(account.RefreshToken)
	}

	v, err, _ := refreshGroup.Do(key, func() (any, error) {
		// 双重检查：可能并发路径已刷新
		if pool != nil && !force {
			for _, acc := range pool.ListAccounts() {
				if strings.TrimSpace(acc.ID) != accountID {
					continue
				}
				if t := strings.TrimSpace(acc.AccessToken); t != "" {
					if acc.ExpiresAt.IsZero() || time.Until(acc.ExpiresAt) > xaiAuth.RefreshLead() {
						return t, nil
					}
				}
				break
			}
		}

		svc := xaiAuth.NewXAIAuth(proxyURL)
		td, refreshErr := svc.RefreshTokens(ctx, account.RefreshToken, "")
		if refreshErr != nil {
			if token != "" {
				return token, nil
			}
			return "", refreshErr
		}
		expiresAt := time.Time{}
		if td.Expire != "" {
			if t, errParse := time.Parse(time.RFC3339, td.Expire); errParse == nil {
				expiresAt = t
			}
		}
		if pool != nil {
			_ = pool.UpdateTokens(account.ID, td.AccessToken, td.RefreshToken, td.IDToken, expiresAt)
		}
		account.AccessToken = td.AccessToken
		if td.RefreshToken != "" {
			account.RefreshToken = td.RefreshToken
		}
		if td.IDToken != "" {
			account.IDToken = td.IDToken
		}
		if !expiresAt.IsZero() {
			account.ExpiresAt = expiresAt
		}
		return strings.TrimSpace(td.AccessToken), nil
	})
	if err != nil {
		return "", err
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("unexpected refresh result")
}
