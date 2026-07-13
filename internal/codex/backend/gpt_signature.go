package backend

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const maxGPTReasoningSignatureLen = 32 * 1024 * 1024

// IsValidGPTReasoningSignature 校验 GPT/Codex reasoning encrypted_content 传输形态。
func IsValidGPTReasoningSignature(raw string) bool {
	return InspectGPTReasoningSignature(raw) == nil
}

// InspectGPTReasoningSignature 校验 Fernet-like 外壳（gAAAA 前缀 + 结构），不证明可解密。
func InspectGPTReasoningSignature(raw string) error {
	sig := strings.TrimSpace(raw)
	if sig == "" {
		return fmt.Errorf("empty GPT reasoning signature")
	}
	if len(sig) > maxGPTReasoningSignatureLen {
		return fmt.Errorf("GPT reasoning signature exceeds maximum length")
	}
	if sig != raw {
		return fmt.Errorf("GPT reasoning signature has leading or trailing whitespace")
	}
	if index, ok := firstInvalidGPTReasoningSignatureChar(sig); ok {
		return fmt.Errorf("invalid GPT reasoning signature: non-base64url character at %d", index)
	}
	if !strings.HasPrefix(sig, "gAAAA") {
		return fmt.Errorf("invalid GPT reasoning signature: expected gAAAA prefix")
	}
	decoded, err := decodeGPTReasoningSignature(sig)
	if err != nil {
		return err
	}
	if len(decoded) < 73 {
		return fmt.Errorf("invalid GPT reasoning signature: decoded payload too short")
	}
	if decoded[0] != 0x80 {
		return fmt.Errorf("invalid GPT reasoning signature: expected version 0x80, got 0x%02x", decoded[0])
	}
	ciphertextLen := len(decoded) - 1 - 8 - 16 - 32
	if ciphertextLen <= 0 || ciphertextLen%16 != 0 {
		return fmt.Errorf("invalid GPT reasoning signature: ciphertext length %d invalid", ciphertextLen)
	}
	return nil
}

func decodeGPTReasoningSignature(sig string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(sig); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(sig); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("invalid GPT reasoning signature: base64url decode failed")
}

func firstInvalidGPTReasoningSignatureChar(sig string) (int, bool) {
	for index, r := range sig {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '=':
		default:
			return index, true
		}
	}
	return 0, false
}
