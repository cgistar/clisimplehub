package openai

import (
	"regexp"
	"strings"
)

type TagFilter struct {
	tags   []string
	buffer string
	inTag  bool
	depth  int
	curTag string
}

func NewTagFilter(tags []string) *TagFilter {
	return &TagFilter{tags: tags}
}

func (f *TagFilter) Filter(token string) string {
	if len(f.tags) == 0 {
		return token
	}

	f.buffer += token
	var result strings.Builder

	for len(f.buffer) > 0 {
		if f.inTag {
			closeTag := "</" + f.curTag
			idx := strings.Index(f.buffer, closeTag)
			if idx == -1 {
				// Still inside tag, consume all
				if len(f.buffer) > 1024 {
					// Safety: discard very long buffered content inside tags
					f.buffer = f.buffer[len(f.buffer)-256:]
				}
				return result.String()
			}
			// Find end of closing tag
			endIdx := strings.Index(f.buffer[idx:], ">")
			if endIdx == -1 {
				return result.String()
			}
			f.buffer = f.buffer[idx+endIdx+1:]
			f.inTag = false
			f.curTag = ""
			continue
		}

		// Look for opening tag
		ltIdx := strings.Index(f.buffer, "<")
		if ltIdx == -1 {
			result.WriteString(f.buffer)
			f.buffer = ""
			break
		}

		// Output everything before the '<'
		if ltIdx > 0 {
			result.WriteString(f.buffer[:ltIdx])
			f.buffer = f.buffer[ltIdx:]
		}

		// Check if we have a complete opening tag
		gtIdx := strings.Index(f.buffer, ">")
		if gtIdx == -1 {
			// Partial tag — wait for more data
			return result.String()
		}

		tagContent := f.buffer[1:gtIdx]
		tagName := tagContent
		if spaceIdx := strings.IndexAny(tagName, " \t\n\r"); spaceIdx != -1 {
			tagName = tagName[:spaceIdx]
		}

		matched := false
		for _, ft := range f.tags {
			if strings.EqualFold(tagName, ft) {
				matched = true
				f.inTag = true
				f.curTag = ft
				f.buffer = f.buffer[gtIdx+1:]
				break
			}
		}
		if !matched {
			result.WriteString(f.buffer[:gtIdx+1])
			f.buffer = f.buffer[gtIdx+1:]
		}
	}

	return result.String()
}

func (f *TagFilter) Flush() string {
	if f.buffer == "" {
		return ""
	}
	out := f.buffer
	f.buffer = ""
	if f.inTag {
		return ""
	}
	return out
}

func FilterContent(content string, tags []string) string {
	if len(tags) == 0 {
		return content
	}
	result := content
	for _, tag := range tags {
		pattern := `(?is)<` + regexp.QuoteMeta(tag) + `[^>]*>.*?</` + regexp.QuoteMeta(tag) + `>`
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		result = re.ReplaceAllString(result, "")
	}
	return result
}
