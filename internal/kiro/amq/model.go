package converters

import "strings"

// NormalizeModelID normalizes a model name for Kiro API.
// Transforms: claude-sonnet-4-20250514 → claude-sonnet-4
//
//	claude-haiku-4-5-20251001 → claude-haiku-4.5
func NormalizeModelID(modelName string) string {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return modelName
	}

	parts := strings.Split(name, "-")
	// Strip trailing 8-digit date suffix
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if len(last) == 8 {
			allDigits := true
			for _, c := range last {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				parts = parts[:len(parts)-1]
			}
		}
	}

	// Try pattern: claude-{family}-{major}-{minor} → claude-{family}-{major}.{minor}
	if len(parts) >= 4 && parts[0] == "claude" {
		minor := parts[len(parts)-1]
		major := parts[len(parts)-2]
		if isNumeric(minor) && isNumeric(major) {
			prefix := strings.Join(parts[:len(parts)-2], "-")
			return prefix + "-" + major + "." + minor
		}
	}

	return strings.Join(parts, "-")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
