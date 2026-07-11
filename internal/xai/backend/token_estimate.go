package backend

import (
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// EstimatePreparedTokens 对 prepare 后的 Responses body 做本地 token 估算。
// 对整包 body 粗估（无 tiktoken 依赖时的启发式）。
func EstimatePreparedTokens(preparedBody []byte) int {
	if len(preparedBody) == 0 {
		return 0
	}
	return estimateTokens(string(preparedBody))
}

// CountTokensForRequest 对请求 body 先 prepare 再估算。
func CountTokensForRequest(body []byte, model string, enableReplay bool, sessionKey string) (int, error) {
	prepared, err := PrepareResponsesBody(body, PrepareOptions{
		Stream:           false,
		Model:            model,
		IsCompact:        false,
		EnableReplay:     enableReplay,
		ReplaySessionKey: sessionKey,
	})
	if err != nil {
		// 无法 prepare 时退回原始 body
		return EstimatePreparedTokens(body), nil
	}
	if prepared == nil {
		return EstimatePreparedTokens(body), nil
	}
	return EstimatePreparedTokens(prepared.Body), nil
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	nonWestern := 0
	totalRunes := utf8.RuneCountInString(text)
	for _, r := range text {
		if isNonWesternChar(r) {
			nonWestern++
		}
	}
	western := totalRunes - nonWestern
	nonWesternTokens := (nonWestern + 1) / 2
	westernTokens := (western + 3) / 4
	estimated := nonWesternTokens + westernTokens
	if estimated <= 0 {
		return 1
	}
	return estimated
}

func isNonWesternChar(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x3040 && r <= 0x309F:
		return true
	case r >= 0x30A0 && r <= 0x30FF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	default:
		return false
	}
}

// FormatClaudeCountTokensResponse Claude count_tokens 响应体。
func FormatClaudeCountTokensResponse(tokens int) []byte {
	if tokens < 1 {
		tokens = 1
	}
	return []byte(`{"input_tokens":` + itoa(tokens) + `}`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ExtractModelForCount 从 body 取 model。
func ExtractModelForCount(body []byte) string {
	return BaseModelName(gjson.GetBytes(body, "model").String())
}
