package backend

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
)

const (
	maxGrokEncryptedContentLen          = 8 * 1024 * 1024
	minGrokEncryptedContentDecodedLen   = 50
	minGrokEncryptedContentEntropyRatio = 0.85
)

// InspectGrokEncryptedContent 校验 Grok reasoning/compaction encrypted_content 传输形态。
func InspectGrokEncryptedContent(raw string) error {
	sig := strings.TrimSpace(raw)
	if sig == "" {
		return fmt.Errorf("empty Grok encrypted_content")
	}
	if len(sig) > maxGrokEncryptedContentLen {
		return fmt.Errorf("Grok encrypted_content exceeds maximum length")
	}
	if sig != raw {
		return fmt.Errorf("Grok encrypted_content has leading or trailing whitespace")
	}
	if strings.HasPrefix(sig, "gAAAA") {
		return fmt.Errorf("Grok encrypted_content looks like GPT/Codex reasoning signature")
	}
	if strings.Contains(sig, "=") {
		return fmt.Errorf("invalid Grok encrypted_content: expected unpadded standard base64")
	}
	for index, r := range sig {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/':
		default:
			return fmt.Errorf("invalid Grok encrypted_content: non-base64 character at %d", index)
		}
	}
	decoded, err := base64.RawStdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid Grok encrypted_content: base64 decode failed: %w", err)
	}
	if len(decoded) < minGrokEncryptedContentDecodedLen {
		return fmt.Errorf("invalid Grok encrypted_content: decoded payload too short")
	}
	if byteEntropyRatio(decoded) < minGrokEncryptedContentEntropyRatio {
		return fmt.Errorf("invalid Grok encrypted_content: entropy too low")
	}
	return nil
}

func IsValidGrokEncryptedContent(raw string) bool {
	return InspectGrokEncryptedContent(raw) == nil
}

func byteEntropyRatio(buf []byte) float64 {
	if len(buf) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range buf {
		counts[b]++
	}
	n := float64(len(buf))
	entropy := 0.0
	for _, count := range counts {
		if count == 0 {
			continue
		}
		p := float64(count) / n
		entropy -= p * math.Log2(p)
	}
	maxSymbols := len(buf)
	if maxSymbols > 256 {
		maxSymbols = 256
	}
	if maxSymbols <= 1 {
		return 0
	}
	return entropy / math.Log2(float64(maxSymbols))
}
