package auth

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

// 对齐 grok2api：x-statsig-id = Base64(伪造的 JS TypeError 指纹字符串)。
// 静态兜底与浏览器/旧版抓包常见值一致。
const staticStatsigID = "ZTpUeXBlRXJyb3I6IENhbm5vdCByZWFkIHByb3BlcnRpZXMgb2YgdW5kZWZpbmVkIChyZWFkaW5nICdjaGlsZE5vZGVzJyk="

const (
	HeaderStatsigID    = "x-statsig-id"
	HeaderXAIRequestID = "x-xai-request-id"
)

// GenerateStatsigID 生成 grok.com 请求用的 x-statsig-id。
// dynamic=true 时每次随机一段属性名，降低固定头特征。
func GenerateStatsigID(dynamic bool) string {
	if !dynamic {
		return staticStatsigID
	}
	if randBit() {
		rand5 := randomCharset(5, "abcdefghijklmnopqrstuvwxyz0123456789")
		msg := "x1:TypeError: Cannot read properties of null (reading 'children['" + rand5 + "']')"
		return base64.StdEncoding.EncodeToString([]byte(msg))
	}
	rand10 := randomCharset(10, "abcdefghijklmnopqrstuvwxyz")
	msg := "x1:TypeError: Cannot read properties of undefined (reading '" + rand10 + "')"
	return base64.StdEncoding.EncodeToString([]byte(msg))
}

// DecodeStatsigID 仅用于测试/调试。
func DecodeStatsigID(value string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return string(raw)
}

func randomCharset(n int, alphabet string) string {
	if n <= 0 || alphabet == "" {
		return ""
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = alphabet[int(randByte())%len(alphabet)]
	}
	return string(out)
}

func randBit() bool {
	return randByte()&1 == 1
}

func randByte() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0x5a
	}
	return b[0]
}
