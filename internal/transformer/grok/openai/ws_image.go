package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clisimplehub/internal/transformer/grok/shared"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const wsImagineURL = "wss://grok.com/ws/imagine/listen"

type wsImageEvent struct {
	Type    string `json:"type"`
	BlobLen int    `json:"blobLen,omitempty"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
	Stage   string `json:"stage,omitempty"`
}

func handleImageGenerationWS(w http.ResponseWriter, ctx context.Context,
	ssoToken, proxyURL string, prompt string, n int,
	aspectRatio, responseFormat string, settings *shared.GrokSettings) {

	if n < 1 {
		n = 1
	}

	dialer := websocket.DefaultDialer
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			dialer = &websocket.Dialer{
				Proxy:            http.ProxyURL(u),
				HandshakeTimeout: 15 * time.Second,
			}
		}
	}

	cookie := "sso=" + ssoToken
	if cf := settings.GetCfClearance(); cf != "" {
		cookie += "; cf_clearance=" + cf
	}

	headers := http.Header{}
	headers.Set("Cookie", cookie)
	headers.Set("Origin", "https://grok.com")
	headers.Set("User-Agent", settings.GetUserAgent())
	headers.Set("Accept-Language", "en-US,en;q=0.9")

	conn, _, err := dialer.DialContext(ctx, wsImagineURL, headers)
	if err != nil {
		writeImageError(w, http.StatusBadGateway, "ws connect failed: "+err.Error())
		return
	}
	defer conn.Close()

	requestID := uuid.NewString()
	wsMsg := map[string]any{
		"type":      "conversation.item.create",
		"timestamp": time.Now().UnixMilli(),
		"item": map[string]any{
			"type": "message",
			"content": []any{
				map[string]any{
					"requestId": requestID,
					"text":      prompt,
					"type":      "input_text",
					"properties": map[string]any{
						"section_count":  0,
						"is_kids_mode":   false,
						"enable_nsfw":    settings.GetImageWsNsfw(),
						"skip_upsampler": false,
						"is_initial":     false,
						"aspect_ratio":   aspectRatio,
					},
				},
			},
		},
	}

	if err := conn.WriteJSON(wsMsg); err != nil {
		writeImageError(w, http.StatusBadGateway, "ws write failed: "+err.Error())
		return
	}

	finalMinBytes := settings.GetImageWsFinalMin()
	mediumMinBytes := settings.GetImageWsMediumMin()
	blockedSec := settings.GetImageWsBlockedSec()

	type imageResult struct {
		URL   string
		Stage string
	}

	var finalImages []imageResult
	var mediumReceivedAt time.Time
	timeout := time.After(time.Duration(settings.GetTimeout()) * time.Second)

	isStreaming := responseFormat == "" // Will be set by caller

	if isStreaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}

	flusher, hasFlusher := w.(http.Flusher)

	for {
		select {
		case <-timeout:
			goto done
		case <-ctx.Done():
			goto done
		default:
		}

		conn.SetReadDeadline(time.Now().Add(time.Duration(settings.GetTimeout()) * time.Second))
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var evt map[string]any
		if json.Unmarshal(msgData, &evt) != nil {
			continue
		}

		evtType, _ := evt["type"].(string)

		switch evtType {
		case "image":
			imgURL, _ := evt["url"].(string)
			blobLen := 0
			if bl, ok := evt["blobLen"].(float64); ok {
				blobLen = int(bl)
			}

			if imgURL == "" {
				continue
			}

			var stage string
			if blobLen >= finalMinBytes {
				stage = "final"
			} else if blobLen >= mediumMinBytes {
				stage = "medium"
				if mediumReceivedAt.IsZero() {
					mediumReceivedAt = time.Now()
				}
			} else {
				stage = "preview"
			}

			if stage == "final" {
				finalImages = append(finalImages, imageResult{URL: imgURL, Stage: stage})

				if isStreaming && hasFlusher {
					eventData := map[string]any{
						"type":  "image_generation.completed",
						"index": len(finalImages) - 1,
						"url":   imgURL,
					}
					fmt.Fprintf(w, "event: image_generation.completed\ndata: %s\n\n", toJSON(eventData))
					flusher.Flush()
				}

				if len(finalImages) >= n {
					goto done
				}
			} else if isStreaming && hasFlusher {
				eventData := map[string]any{
					"type":  "image_generation.partial_image",
					"index": 0,
					"stage": stage,
					"url":   imgURL,
				}
				fmt.Fprintf(w, "event: image_generation.partial_image\ndata: %s\n\n", toJSON(eventData))
				flusher.Flush()
			}

		case "error":
			errMsg, _ := evt["error"].(string)
			if errMsg == "" {
				errMsg = "image generation error"
			}
			if !isStreaming {
				writeImageError(w, http.StatusInternalServerError, errMsg)
			} else if hasFlusher {
				fmt.Fprintf(w, "data: %s\n\n", toJSON(map[string]any{
					"error": map[string]any{"message": errMsg, "type": "upstream_error"},
				}))
				flusher.Flush()
			}
			return
		}

		// Check blocked detection
		if !mediumReceivedAt.IsZero() && blockedSec > 0 && len(finalImages) == 0 {
			if time.Since(mediumReceivedAt) > time.Duration(blockedSec)*time.Second {
				if !isStreaming {
					writeImageError(w, http.StatusInternalServerError, "image generation blocked (timeout after medium)")
				} else if hasFlusher {
					fmt.Fprintf(w, "data: %s\n\n", toJSON(map[string]any{
						"error": map[string]any{"message": "image generation blocked", "type": "upstream_error"},
					}))
					flusher.Flush()
				}
				return
			}
		}
	}

done:
	if isStreaming && hasFlusher {
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	// Non-streaming: return collected final images
	if len(finalImages) == 0 {
		writeImageError(w, http.StatusInternalServerError, "no images generated via websocket")
		return
	}

	urls := make([]string, 0, len(finalImages))
	for _, img := range finalImages {
		urls = append(urls, img.URL)
	}
	if len(urls) > n {
		urls = urls[:n]
	}
	writeImageSuccess(w, urls, responseFormat, proxyURL)
}

func buildWSHeaders(ssoToken string, settings *shared.GrokSettings) http.Header {
	cookie := "sso=" + strings.TrimPrefix(ssoToken, "sso=")
	if cf := settings.GetCfClearance(); cf != "" {
		cookie += "; cf_clearance=" + cf
	}
	h := http.Header{}
	h.Set("Cookie", cookie)
	h.Set("Origin", "https://grok.com")
	h.Set("User-Agent", settings.GetUserAgent())
	h.Set("Accept-Language", "en-US,en;q=0.9")
	return h
}
