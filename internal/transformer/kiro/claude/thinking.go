package claude

import "unicode/utf8"

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
