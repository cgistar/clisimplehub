package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func GeneratePKCECodes() (*PKCECodes, error) {
	bytes := make([]byte, 96)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("xai pkce: generate verifier: %w", err)
	}
	verifier := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
	return &PKCECodes{CodeVerifier: verifier, CodeChallenge: challenge}, nil
}

func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}
