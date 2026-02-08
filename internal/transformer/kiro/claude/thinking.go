package claude

import (
	"strings"
	"unicode/utf8"
)

const (
	thinkingStartTag = "<thinking>"
	thinkingEndTag   = "</thinking>"
)

var quoteChars = func() [256]bool {
	var q [256]bool
	for _, b := range []byte{
		'`', '"', '\'', '\\', '#', '!', '@', '$', '%', '^', '&', '*', '(', ')', '-', '_', '=', '+',
		'[', ']', '{', '}', ';', ':', '<', '>', ',', '.', '?', '/',
	} {
		q[b] = true
	}
	return q
}()

func isQuoteChar(buffer string, pos int) bool {
	if pos < 0 || pos >= len(buffer) {
		return false
	}
	return quoteChars[buffer[pos]]
}

func findCharBoundary(s string, target int) int {
	if target <= 0 {
		return 0
	}
	if target >= len(s) {
		return len(s)
	}
	pos := target
	for pos > 0 && !utf8.RuneStart(s[pos]) {
		pos--
	}
	return pos
}

func findRealThinkingStartTag(buffer string) int {
	searchStart := 0
	for {
		pos := indexOf(buffer, thinkingStartTag, searchStart)
		if pos < 0 {
			return -1
		}

		before := pos - 1
		after := pos + len(thinkingStartTag)
		if !(pos > 0 && isQuoteChar(buffer, before)) && !isQuoteChar(buffer, after) {
			return pos
		}

		searchStart = pos + 1
	}
}

func findRealThinkingEndTag(buffer string) int {
	searchStart := 0
	for {
		pos := indexOf(buffer, thinkingEndTag, searchStart)
		if pos < 0 {
			return -1
		}

		before := pos - 1
		after := pos + len(thinkingEndTag)
		hasQuoteBefore := pos > 0 && isQuoteChar(buffer, before)
		hasQuoteAfter := isQuoteChar(buffer, after)
		if hasQuoteBefore || hasQuoteAfter {
			searchStart = pos + 1
			continue
		}

		// Need at least "\n\n" after the end tag to consider it final.
		if len(buffer) < after+2 {
			return -1
		}
		if buffer[after:after+2] == "\n\n" {
			return pos
		}

		searchStart = pos + 1
	}
}

// findRealThinkingEndTagAtBufferEnd finds </thinking> at the buffer end
// where only whitespace follows the tag. Used for boundary events:
// when tool_use immediately follows thinking, or stream ends.
func findRealThinkingEndTagAtBufferEnd(buffer string) int {
	searchStart := 0
	for {
		pos := indexOf(buffer, thinkingEndTag, searchStart)
		if pos < 0 {
			return -1
		}

		before := pos - 1
		after := pos + len(thinkingEndTag)
		hasQuoteBefore := pos > 0 && isQuoteChar(buffer, before)
		hasQuoteAfter := isQuoteChar(buffer, after)
		if hasQuoteBefore || hasQuoteAfter {
			searchStart = pos + 1
			continue
		}

		// Only match if everything after the tag is whitespace
		remaining := buffer[after:]
		if strings.TrimSpace(remaining) == "" {
			return pos
		}

		searchStart = pos + 1
	}
}

func indexOf(s, sub string, start int) int {
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return -1
	}
	if i := len(sub); i == 0 {
		return start
	}
	// stdlib strings.Index is fine, but keep this file dependency-light.
	for i := start; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// keepSuffixForPossibleTagPrefix 返回需要保留在 buffer 末尾的字节数，用于等待可能的 tag 前缀补全。
// 例如 buffer 以 "<thi" 结尾时，会保留 4 字节；若末尾不可能是 tag 的前缀，则返回 0。
//
// 注意：tag 为 ASCII（如 "<thinking>"），因此只需要做字节级匹配即可；若 buffer 末尾落在 UTF-8
// 多字节 rune 的中间，则不可能匹配到 ASCII 前缀，会自然返回 0。
func keepSuffixForPossibleTagPrefix(buffer string, tag string) int {
	if buffer == "" || tag == "" {
		return 0
	}

	maxKeep := len(tag) - 1
	if maxKeep <= 0 {
		return 0
	}
	if len(buffer) < maxKeep {
		maxKeep = len(buffer)
	}

	for keep := maxKeep; keep > 0; keep-- {
		suffix := buffer[len(buffer)-keep:]
		if tag[:keep] == suffix {
			return keep
		}
	}
	return 0
}
