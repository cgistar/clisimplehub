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
var ssoAuthGroup singleflight.Group

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
	// 双重检查：可能并发路径已刷新。
	if pool != nil && !force {
		for _, acc := range pool.ListAccounts() {
			if strings.TrimSpace(acc.ID) != accountID {
				continue
			}
			if currentToken := strings.TrimSpace(acc.AccessToken); currentToken != "" &&
				(acc.ExpiresAt.IsZero() || time.Until(acc.ExpiresAt) > xaiAuth.RefreshLead()) {
				return currentToken, nil
			}
			break
		}
	}

	updated, err := refreshOAuthAccount(ctx, pool, account, proxyURL, force)
	if err != nil {
		if token != "" {
			return token, nil
		}
		return "", err
	}
	account.AccessToken = updated.AccessToken
	account.RefreshToken = updated.RefreshToken
	account.IDToken = updated.IDToken
	account.Email = updated.Email
	account.Subject = updated.Subject
	account.ExpiresAt = updated.ExpiresAt
	account.LastRefresh = updated.LastRefresh
	if refreshedToken := strings.TrimSpace(updated.AccessToken); refreshedToken != "" {
		return refreshedToken, nil
	}
	return "", fmt.Errorf("xai token refresh returned empty access token")
}

// refreshOAuthAccount 严格刷新单个账号，并通过账号级 singleflight 避免 refresh token 轮换竞争。
func refreshOAuthAccount(ctx context.Context, pool *xai.XaiAccountPool, account *xaiShared.XaiAccount, proxyURL string, force bool) (*xaiShared.XaiAccount, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	accountID := strings.TrimSpace(account.ID)
	key := accountID
	if key == "" {
		key = strings.TrimSpace(account.RefreshToken)
	}
	if key == "" {
		return nil, fmt.Errorf("account id or refresh token is required")
	}
	v, err, _ := refreshGroup.Do(key, func() (any, error) {
		current := *account
		if pool != nil && accountID != "" {
			for _, candidate := range pool.ListAccounts() {
				if strings.TrimSpace(candidate.ID) == accountID {
					current = candidate
					break
				}
			}
		}
		if strings.TrimSpace(current.RefreshToken) == "" {
			return nil, fmt.Errorf("refresh token is empty")
		}
		if !force {
			currentToken := strings.TrimSpace(current.AccessToken)
			if currentToken != "" && (current.ExpiresAt.IsZero() || time.Until(current.ExpiresAt) > xaiAuth.RefreshLead()) {
				return &current, nil
			}
		}
		svc := xaiAuth.NewXAIAuth(proxyURL)
		td, refreshErr := svc.RefreshTokens(ctx, current.RefreshToken, "")
		if refreshErr != nil {
			return nil, refreshErr
		}
		expiresAt := time.Time{}
		if td.Expire != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, td.Expire); parseErr == nil {
				expiresAt = parsed
			}
		}
		if pool != nil && accountID != "" {
			return pool.ApplyRefreshedCredentials(
				accountID,
				td.AccessToken,
				td.RefreshToken,
				td.IDToken,
				td.Email,
				td.Subject,
				expiresAt,
			)
		}
		current.AccessToken = strings.TrimSpace(td.AccessToken)
		if td.RefreshToken != "" {
			current.RefreshToken = strings.TrimSpace(td.RefreshToken)
		}
		if td.IDToken != "" {
			current.IDToken = strings.TrimSpace(td.IDToken)
		}
		if td.Email != "" {
			current.Email = strings.TrimSpace(td.Email)
		}
		if td.Subject != "" {
			current.Subject = strings.TrimSpace(td.Subject)
		}
		if !expiresAt.IsZero() {
			current.ExpiresAt = expiresAt.UTC()
		}
		current.LastRefresh = time.Now().UTC()
		return &current, nil
	})
	if err != nil {
		return nil, err
	}
	updated, ok := v.(*xaiShared.XaiAccount)
	if !ok || updated == nil {
		return nil, fmt.Errorf("unexpected refresh result")
	}
	copyAccount := *updated
	return &copyAccount, nil
}
