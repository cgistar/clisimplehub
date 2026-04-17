package middleware

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"regexp"
	"strings"

	"clisimplehub/internal/storage"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const claudeMessagesCCHSeed uint64 = 0x6E52736AC806831E

var claudeMessagesBillingHeaderPattern = regexp.MustCompile(`\bcch=([0-9a-f]{5});`)

func shouldSignClaudeMessagesBody(endpoint *storage.Endpoint, cfg resolvedClaudeMessagesConfig) bool {
	if cfg.ExperimentalCCHSigning {
		return true
	}
	return shouldRemapClaudeMessagesOAuthTools(endpoint, cfg)
}

func signClaudeMessagesBody(body []byte) []byte {
	billingHeader := gjson.GetBytes(body, "system.0.text").String()
	if !claudeMessagesBillingHeaderPattern.MatchString(billingHeader) {
		return body
	}
	if !strings.Contains(billingHeader, "cch=00000;") {
		return body
	}

	unsignedHeader := claudeMessagesBillingHeaderPattern.ReplaceAllString(billingHeader, "cch=00000;")
	unsignedBody, err := sjson.SetBytes(body, "system.0.text", unsignedHeader)
	if err != nil {
		return body
	}

	cch := fmt.Sprintf("%05x", xxhash64Checksum(unsignedBody, claudeMessagesCCHSeed)&0xFFFFF)
	signedHeader := claudeMessagesBillingHeaderPattern.ReplaceAllString(unsignedHeader, "cch="+cch+";")
	signedBody, err := sjson.SetBytes(unsignedBody, "system.0.text", signedHeader)
	if err != nil {
		return unsignedBody
	}
	return signedBody
}

const (
	xxPrime1 uint64 = 11400714785074694791
	xxPrime2 uint64 = 14029467366897019727
	xxPrime3 uint64 = 1609587929392839161
	xxPrime4 uint64 = 9650029242287828579
	xxPrime5 uint64 = 2870177450012600261
)

func xxhash64Checksum(b []byte, seed uint64) uint64 {
	n := len(b)
	var h uint64

	if n >= 32 {
		v1 := seed + xxPrime1 + xxPrime2
		v2 := seed + xxPrime2
		v3 := seed
		v4 := seed - xxPrime1

		for len(b) >= 32 {
			v1 = xxhashRound(v1, binary.LittleEndian.Uint64(b[0:8]))
			v2 = xxhashRound(v2, binary.LittleEndian.Uint64(b[8:16]))
			v3 = xxhashRound(v3, binary.LittleEndian.Uint64(b[16:24]))
			v4 = xxhashRound(v4, binary.LittleEndian.Uint64(b[24:32]))
			b = b[32:]
		}

		h = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		h = xxhashMergeRound(h, v1)
		h = xxhashMergeRound(h, v2)
		h = xxhashMergeRound(h, v3)
		h = xxhashMergeRound(h, v4)
	} else {
		h = seed + xxPrime5
	}

	h += uint64(n)

	for len(b) >= 8 {
		k1 := xxhashRound(0, binary.LittleEndian.Uint64(b[:8]))
		h ^= k1
		h = bits.RotateLeft64(h, 27)*xxPrime1 + xxPrime4
		b = b[8:]
	}
	if len(b) >= 4 {
		h ^= uint64(binary.LittleEndian.Uint32(b[:4])) * xxPrime1
		h = bits.RotateLeft64(h, 23)*xxPrime2 + xxPrime3
		b = b[4:]
	}
	for len(b) > 0 {
		h ^= uint64(b[0]) * xxPrime5
		h = bits.RotateLeft64(h, 11) * xxPrime1
		b = b[1:]
	}

	h ^= h >> 33
	h *= xxPrime2
	h ^= h >> 29
	h *= xxPrime3
	h ^= h >> 32
	return h
}

func xxhashRound(acc, input uint64) uint64 {
	acc += input * xxPrime2
	acc = bits.RotateLeft64(acc, 31)
	acc *= xxPrime1
	return acc
}

func xxhashMergeRound(acc, val uint64) uint64 {
	val = xxhashRound(0, val)
	acc ^= val
	acc = acc*xxPrime1 + xxPrime4
	return acc
}
