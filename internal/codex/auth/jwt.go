package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type JWTClaims struct {
	Email         string        `json:"email"`
	EmailVerified bool          `json:"email_verified"`
	Exp           int64         `json:"exp"`
	Iat           int64         `json:"iat"`
	Iss           string        `json:"iss"`
	Sub           string        `json:"sub"`
	CodexAuth     CodexAuthInfo `json:"https://api.openai.com/auth"`
}

type CodexAuthInfo struct {
	ChatgptAccountID string `json:"chatgpt_account_id"`
	ChatgptPlanType  string `json:"chatgpt_plan_type"`
	ChatgptUserID    string `json:"chatgpt_user_id"`
}

func ParseJWTToken(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	claimsData, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT claims: %w", err)
	}

	var claims JWTClaims
	if err = json.Unmarshal(claimsData, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWT claims: %w", err)
	}
	return &claims, nil
}

func base64URLDecode(data string) ([]byte, error) {
	switch len(data) % 4 {
	case 2:
		data += "=="
	case 3:
		data += "="
	}
	return base64.URLEncoding.DecodeString(data)
}
