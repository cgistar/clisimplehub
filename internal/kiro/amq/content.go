package converters

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractTextContent extracts text from various content formats.
// Supports: string, list of content blocks, nil.
func ExtractTextContent(content any) string {
	if content == nil {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case json.RawMessage:
		return extractTextFromRawMessage(c)
	case []any:
		return extractTextFromSlice(c)
	}
	return fmt.Sprintf("%v", content)
}

func extractTextFromRawMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return extractTextFromMapSlice(arr)
	}
	return string(raw)
}

func extractTextFromSlice(items []any) string {
	var parts []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
			continue
		}
		t, _ := m["type"].(string)
		if t == "image" || t == "image_url" {
			continue
		}
		if t == "text" {
			if text, ok := m["text"].(string); ok {
				parts = append(parts, text)
			}
		} else if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractTextFromMapSlice(items []map[string]any) string {
	var parts []string
	for _, m := range items {
		t, _ := m["type"].(string)
		if t == "image" || t == "image_url" {
			continue
		}
		if t == "text" {
			if text, ok := m["text"].(string); ok {
				parts = append(parts, text)
			}
		} else if text, ok := m["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

// ExtractImagesFromContent extracts images from message content.
// Supports OpenAI image_url and Anthropic image formats.
func ExtractImagesFromContent(content any) []ImageRef {
	var items []map[string]any

	switch c := content.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(c, &items); err != nil {
			return nil
		}
	case []any:
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
	default:
		return nil
	}

	var images []ImageRef
	for _, item := range items {
		t, _ := item["type"].(string)

		if t == "image_url" {
			imageURLObj, _ := item["image_url"].(map[string]any)
			if imageURLObj == nil {
				continue
			}
			url, _ := imageURLObj["url"].(string)
			if !strings.HasPrefix(url, "data:") {
				continue
			}
			parts := strings.SplitN(url, ",", 2)
			if len(parts) != 2 || parts[1] == "" {
				continue
			}
			mediaPart := strings.Split(parts[0], ";")[0]
			mediaType := strings.TrimPrefix(mediaPart, "data:")
			images = append(images, ImageRef{MediaType: mediaType, Data: parts[1]})
		}

		if t == "image" {
			source, _ := item["source"].(map[string]any)
			if source == nil {
				continue
			}
			srcType, _ := source["type"].(string)
			if srcType == "base64" {
				mediaType, _ := source["media_type"].(string)
				if mediaType == "" {
					mediaType = "image/jpeg"
				}
				data, _ := source["data"].(string)
				if data != "" {
					images = append(images, ImageRef{MediaType: mediaType, Data: data})
				}
			}
		}
	}
	return images
}
