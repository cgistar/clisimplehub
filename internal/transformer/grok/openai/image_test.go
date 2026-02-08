package openai

import (
	"testing"
)

func TestMapSizeToAspectRatio(t *testing.T) {
	tests := []struct {
		size     string
		expected string
	}{
		{"", "2:3"},
		{"1024x1024", "1:1"},
		{"512x512", "1:1"},
		{"1024x576", "16:9"},
		{"1280x720", "16:9"},
		{"576x1024", "9:16"},
		{"720x1280", "9:16"},
		{"1024x1536", "2:3"},
		{"1536x1024", "3:2"},
		{"1:1", "1:1"},
		{"16:9", "16:9"},
		{"unknown", "2:3"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			result := mapSizeToAspectRatio(tt.size)
			if result != tt.expected {
				t.Errorf("mapSizeToAspectRatio(%q) = %q, want %q", tt.size, result, tt.expected)
			}
		})
	}
}

func TestExtractParentPostID(t *testing.T) {
	tests := []struct {
		name     string
		urls     []string
		expected string
	}{
		{
			name:     "empty",
			urls:     []string{},
			expected: "",
		},
		{
			name:     "generated pattern",
			urls:     []string{"https://assets.grok.com/generated/abc-123-def/image.png"},
			expected: "abc-123-def",
		},
		{
			name:     "users pattern",
			urls:     []string{"https://assets.grok.com/users/john/xyz-456/content"},
			expected: "xyz-456",
		},
		{
			name:     "no match",
			urls:     []string{"https://example.com/image.png"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractParentPostID(tt.urls)
			if result != tt.expected {
				t.Errorf("extractParentPostID() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"image.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"pic.jpeg", "image/jpeg"},
		{"animation.gif", "image/gif"},
		{"modern.webp", "image/webp"},
		{"unknown.txt", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := detectMimeType(tt.filename)
			if result != tt.expected {
				t.Errorf("detectMimeType(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}
