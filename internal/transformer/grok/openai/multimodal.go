package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ExtractResult struct {
	Message         string
	FileAttachments []string
}

func ExtractMessagesAndAttachments(ctx context.Context, req map[string]any, ssoToken, proxyURL string) (*ExtractResult, error) {
	messages, _ := req["messages"].([]any)
	if len(messages) == 0 {
		return &ExtractResult{}, nil
	}

	// Find last user message index
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if m, ok := messages[i].(map[string]any); ok {
			if role, _ := m["role"].(string); role == "user" {
				lastUserIdx = i
				break
			}
		}
	}

	var parts []string
	var allAttachments []string

	for i, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		text, attachments := extractContentAndFiles(ctx, m, ssoToken, proxyURL)
		allAttachments = append(allAttachments, attachments...)

		if text == "" {
			continue
		}
		if i == lastUserIdx {
			parts = append(parts, text)
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", role, text))
		}
	}

	return &ExtractResult{
		Message:         strings.Join(parts, "\n\n"),
		FileAttachments: allAttachments,
	}, nil
}

func extractContentAndFiles(ctx context.Context, msg map[string]any, ssoToken, proxyURL string) (string, []string) {
	// Simple string content
	if s, ok := msg["content"].(string); ok {
		return s, nil
	}

	arr, ok := msg["content"].([]any)
	if !ok {
		return "", nil
	}

	var texts []string
	var attachments []string

	for _, item := range arr {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)

		switch partType {
		case "text":
			if text, ok := part["text"].(string); ok {
				texts = append(texts, text)
			}

		case "image_url":
			imgObj, _ := part["image_url"].(map[string]any)
			if imgObj == nil {
				continue
			}
			imgURL, _ := imgObj["url"].(string)
			if imgURL == "" {
				continue
			}
			fileID, err := uploadFromURL(ctx, imgURL, ssoToken, proxyURL)
			if err == nil && fileID != "" {
				attachments = append(attachments, fileID)
			}

		case "file":
			fileObj, _ := part["file"].(map[string]any)
			if fileObj == nil {
				continue
			}
			if fileURL, _ := fileObj["url"].(string); fileURL != "" {
				fileID, err := uploadFromURL(ctx, fileURL, ssoToken, proxyURL)
				if err == nil && fileID != "" {
					attachments = append(attachments, fileID)
				}
			} else if fileData, _ := fileObj["data"].(string); fileData != "" {
				mimeType, _ := fileObj["mime_type"].(string)
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				fileID, err := uploadFromBase64(ctx, fileData, "file", mimeType, ssoToken, proxyURL)
				if err == nil && fileID != "" {
					attachments = append(attachments, fileID)
				}
			}

		case "input_audio":
			audioObj, _ := part["input_audio"].(map[string]any)
			if audioObj == nil {
				continue
			}
			audioData, _ := audioObj["data"].(string)
			if audioData == "" {
				continue
			}
			audioFmt, _ := audioObj["format"].(string)
			mimeType := "audio/" + audioFmt
			if audioFmt == "" {
				mimeType = "audio/wav"
			}
			fileID, err := uploadFromBase64(ctx, audioData, "audio."+audioFmt, mimeType, ssoToken, proxyURL)
			if err == nil && fileID != "" {
				attachments = append(attachments, fileID)
			}
		}
	}

	return strings.Join(texts, "\n"), attachments
}

func uploadFromURL(ctx context.Context, rawURL, ssoToken, proxyURL string) (string, error) {
	// Handle data: URIs
	if strings.HasPrefix(rawURL, "data:") {
		mimeType, b64Data := parseDataURI(rawURL)
		if b64Data == "" {
			return "", fmt.Errorf("invalid data URI")
		}
		return uploadFromBase64(ctx, b64Data, "upload", mimeType, ssoToken, proxyURL)
	}

	// Download the file
	dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	client := newProxyClient(proxyURL, 30*time.Second)
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return "", err
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	return UploadToGrok(ctx, ssoToken, proxyURL, "upload", mimeType, data)
}

func uploadFromBase64(ctx context.Context, b64Data, filename, mimeType, ssoToken, proxyURL string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		// Try URL-safe base64
		data, err = base64.URLEncoding.DecodeString(b64Data)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %w", err)
		}
	}
	return UploadToGrok(ctx, ssoToken, proxyURL, filename, mimeType, data)
}

func parseDataURI(dataURI string) (mimeType, b64Data string) {
	// data:[<mediatype>][;base64],<data>
	after := strings.TrimPrefix(dataURI, "data:")
	commaIdx := strings.Index(after, ",")
	if commaIdx == -1 {
		return "", ""
	}
	meta := after[:commaIdx]
	b64Data = after[commaIdx+1:]

	mimeType = "application/octet-stream"
	if semicolonIdx := strings.Index(meta, ";"); semicolonIdx > 0 {
		mimeType = meta[:semicolonIdx]
	} else if meta != "" && meta != "base64" {
		mimeType = meta
	}
	return mimeType, b64Data
}

// UploadToGrok uploads file data to Grok and returns the file URI.
func UploadToGrok(ctx context.Context, ssoToken, proxyURL, filename, mimeType string, data []byte) (string, error) {
	return uploadToGrok(ctx, ssoToken, proxyURL, filename, mimeType, data)
}
