package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

// ImagineGenerate 通过 wss://grok.com/ws/imagine/listen 生成图片。
func ImagineGenerate(
	ctx context.Context,
	sso, proxyURL, prompt, size, responseFormat string,
	n int,
	dynamicStatsig, enablePro bool,
) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt cannot be empty")
	}
	if n <= 0 {
		n = 1
	}
	if n > 6 {
		n = 6
	}
	aspect := ResolveImageAspectRatio(size)
	sso = strings.TrimSpace(sso)
	if strings.HasPrefix(strings.ToLower(sso), "sso=") {
		sso = strings.TrimSpace(sso[4:])
	}

	headers := http.Header{}
	headers.Set("Origin", "https://grok.com")
	headers.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")
	headers.Set("Cookie", "sso="+sso+"; sso-rw="+sso)
	headers.Set("x-statsig-id", generateGrokStatsigID(dynamicStatsig))

	dialer := newImagineDialer(proxyURL)
	conn, _, err := dialer.DialContext(ctx, GrokImagineWSURL, headers)
	if err != nil {
		return nil, fmt.Errorf("imagine ws dial: %w", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	if err := conn.WriteJSON(map[string]any{
		"type":      "conversation.item.create",
		"timestamp": time.Now().UnixMilli(),
		"item": map[string]any{
			"type":    "message",
			"content": []map[string]any{{"type": "reset"}},
		},
	}); err != nil {
		return nil, err
	}
	reqID := uuid.NewString()
	if err := conn.WriteJSON(map[string]any{
		"type":      "conversation.item.create",
		"timestamp": time.Now().UnixMilli(),
		"item": map[string]any{
			"type": "message",
			"content": []map[string]any{{
				"requestId": reqID,
				"text":      prompt,
				"type":      "input_text",
				"properties": map[string]any{
					"section_count":       0,
					"is_kids_mode":        false,
					"enable_nsfw":         true,
					"skip_upsampler":      false,
					"enable_side_by_side": true,
					"is_initial":          false,
					"aspect_ratio":        aspect,
					"enable_pro":          enablePro,
				},
			}},
		},
	}); err != nil {
		return nil, err
	}

	type slot struct {
		url  string
		blob string
		done bool
	}
	slots := map[string]*slot{}
	urls := make([]string, 0, n)
	b64s := make([]string, 0, n)
	deadline := time.Now().Add(120 * time.Second)

	collect := func(s *slot) {
		if s == nil || s.done {
			return
		}
		s.done = true
		if responseFormat == "b64_json" && s.blob != "" {
			b64s = append(b64s, s.blob)
			return
		}
		if s.url != "" {
			urls = append(urls, absolutizeAssetURL(s.url))
			return
		}
		if s.blob != "" {
			b64s = append(b64s, s.blob)
		}
	}

	for time.Now().Before(deadline) && len(urls)+len(b64s) < n {
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if len(urls)+len(b64s) > 0 {
				break
			}
			return nil, fmt.Errorf("imagine ws read: %w", err)
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch strings.TrimSpace(fmt.Sprint(msg["type"])) {
		case "json":
			status := fmt.Sprint(msg["current_status"])
			iid := strings.TrimSpace(fmt.Sprint(msg["image_id"]))
			if iid == "" || iid == "<nil>" {
				iid = strings.TrimSpace(fmt.Sprint(msg["job_id"]))
			}
			if iid == "" || iid == "<nil>" {
				continue
			}
			if status == "start_stage" {
				if slots[iid] == nil {
					slots[iid] = &slot{}
				}
			} else if status == "completed" {
				s := slots[iid]
				if s == nil {
					s = &slot{}
					slots[iid] = s
				}
				if boolFromAny(msg["moderated"]) {
					s.done = true
					continue
				}
				collect(s)
			}
		case "image":
			u := strings.TrimSpace(fmt.Sprint(msg["url"]))
			if u == "<nil>" {
				u = ""
			}
			blob, _ := msg["blob"].(string)
			iid, _ := parseImagineImageID(u)
			s := slots[iid]
			if s == nil {
				s = &slot{}
				if iid != "" {
					slots[iid] = s
				}
			}
			if u != "" {
				s.url = u
			}
			if blob != "" {
				s.blob = blob
			}
		case "error":
			return nil, fmt.Errorf("imagine error: %v", msg["err_msg"])
		}
	}

	if len(urls) == 0 && len(b64s) == 0 {
		for _, s := range slots {
			if s == nil || s.done {
				continue
			}
			collect(s)
			if len(urls)+len(b64s) >= n {
				break
			}
		}
	}
	if len(urls) == 0 && len(b64s) == 0 {
		return nil, fmt.Errorf("imagine returned no images")
	}
	return OpenAIImagesResponse(urls, b64s), nil
}

func parseImagineImageID(u string) (string, string) {
	if i := strings.Index(u, "/images/"); i >= 0 {
		rest := u[i+len("/images/"):]
		if j := strings.IndexAny(rest, ".?"); j >= 0 {
			return rest[:j], strings.TrimPrefix(rest[j:], ".")
		}
		return rest, "jpg"
	}
	return uuid.NewString(), "jpg"
}

func boolFromAny(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	default:
		return false
	}
}

func newImagineDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 25 * time.Second,
		NetDialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return dialer
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return dialer
	}
	switch parsed.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsed.User != nil {
			pw, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: pw}
		}
		socksDialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if err != nil {
			return dialer
		}
		dialer.Proxy = nil
		dialer.NetDial = socksDialer.Dial
	case "http", "https":
		dialer.Proxy = http.ProxyURL(parsed)
	}
	return dialer
}
