package backend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"
)

// grok.com reverse 媒体端点（SSO Cookie，与 console.x.ai Chat 共用 basic+SSO 账号）。
const (
	GrokMediaPostURL  = "https://grok.com/rest/media/post/create"
	GrokMediaLinkURL  = "https://grok.com/rest/media/post/create-link"
	GrokAppChatURL    = "https://grok.com/rest/app-chat/conversations/new"
	GrokUploadFileURL = "https://grok.com/rest/app-chat/upload-file"
	GrokImagineWSURL  = "wss://grok.com/ws/imagine/listen"
	GrokAssetsCDN     = "https://assets.grok.com"
)

const (
	mediaTypeImage = "MEDIA_POST_TYPE_IMAGE"
	mediaTypeVideo = "MEDIA_POST_TYPE_VIDEO"
	videoModelName = "grok-video"
	imageEditModel = "imagine-image-edit"
)

// BuildGrokSSOHeaders 浏览器态 grok.com HTTP 头（含 x-statsig-id）。
func BuildGrokSSOHeaders(sso string, dynamicStatsig bool, origin, referer string) map[string]string {
	sso = strings.TrimSpace(sso)
	if strings.HasPrefix(strings.ToLower(sso), "sso=") {
		sso = strings.TrimSpace(sso[4:])
	}
	if origin == "" {
		origin = "https://grok.com"
	}
	if referer == "" {
		referer = "https://grok.com/"
	}
	return map[string]string{
		"Accept":           "*/*",
		"Accept-Language":  "zh-CN,zh;q=0.9,en;q=0.8",
		"Content-Type":     "application/json",
		"Origin":           origin,
		"Referer":          referer,
		"User-Agent":       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
		"x-statsig-id":     generateGrokStatsigID(dynamicStatsig),
		"x-xai-request-id": uuid.NewString(),
		"Cookie":           "sso=" + sso + "; sso-rw=" + sso,
	}
}

// generateGrokStatsigID 与 auth.GenerateStatsigID 同逻辑，避免 backend→auth 循环依赖。
func generateGrokStatsigID(dynamic bool) string {
	const staticID = "ZTpUeXBlRXJyb3I6IENhbm5vdCByZWFkIHByb3BlcnRpZXMgb2YgdW5kZWZpbmVkIChyZWFkaW5nICdjaGlsZE5vZGVzJyk="
	if !dynamic {
		return staticID
	}
	var b [1]byte
	_, _ = rand.Read(b[:])
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	randStr := func(n int, alphabet string) string {
		out := make([]byte, n)
		for i := 0; i < n; i++ {
			var bb [1]byte
			_, _ = rand.Read(bb[:])
			out[i] = alphabet[int(bb[0])%len(alphabet)]
		}
		return string(out)
	}
	var msg string
	if b[0]&1 == 0 {
		msg = "x1:TypeError: Cannot read properties of null (reading 'children['" + randStr(5, alphabet) + "']')"
	} else {
		msg = "x1:TypeError: Cannot read properties of undefined (reading '" + randStr(10, "abcdefghijklmnopqrstuvwxyz") + "')"
	}
	return base64.StdEncoding.EncodeToString([]byte(msg))
}

// ResolveImageAspectRatio OpenAI size → grok aspect_ratio。
func ResolveImageAspectRatio(size string) string {
	switch strings.TrimSpace(size) {
	case "1024x1024", "512x512", "256x256":
		return "1:1"
	case "1792x1024", "1280x720":
		return "16:9"
	case "1024x1792", "720x1280":
		return "9:16"
	case "1024x768":
		return "4:3"
	case "768x1024":
		return "3:4"
	default:
		return "2:3"
	}
}

// ResolveVideoSize → (aspectRatio, resolutionName)
func ResolveVideoSize(size string) (aspect, resolution string) {
	switch strings.TrimSpace(size) {
	case "1280x720":
		return "16:9", "720p"
	case "1024x1024":
		return "1:1", "720p"
	case "1024x1792", "720x1280":
		return "9:16", "720p"
	case "1792x1024":
		return "16:9", "720p"
	default:
		return "9:16", "720p"
	}
}

// CreateMediaPost POST /rest/media/post/create
func CreateMediaPost(ctx context.Context, sso, proxyURL, mediaType, prompt string, dynamicStatsig bool) (postID string, err error) {
	payload := map[string]any{"mediaType": mediaType}
	if strings.TrimSpace(prompt) != "" {
		payload["prompt"] = prompt
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GrokMediaPostURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range BuildGrokSSOHeaders(sso, dynamicStatsig, "https://grok.com", "https://grok.com/imagine") {
		req.Header.Set(k, v)
	}
	client := newGrokHTTPClient(proxyURL, 60*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create media post HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Post *struct {
			ID string `json:"id"`
		} `json:"post"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse media post: %w", err)
	}
	if out.Post == nil || strings.TrimSpace(out.Post.ID) == "" {
		return "", fmt.Errorf("media post missing id")
	}
	return strings.TrimSpace(out.Post.ID), nil
}

// UploadAssetDataURI 上传 data URI / 纯 base64 到 grok upload-file。
func UploadAssetDataURI(ctx context.Context, sso, proxyURL, dataURI string, dynamicStatsig bool) (fileID, fileURI string, err error) {
	filename, mime, b64, err := parseDataURI(dataURI)
	if err != nil {
		return "", "", err
	}
	payload, _ := json.Marshal(map[string]any{
		"fileName":     filename,
		"fileMimeType": mime,
		"content":      b64,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GrokUploadFileURL, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	for k, v := range BuildGrokSSOHeaders(sso, dynamicStatsig, "https://grok.com", "https://grok.com/") {
		req.Header.Set(k, v)
	}
	client := newGrokHTTPClient(proxyURL, 60*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		FileMetadataID string `json:"fileMetadataId"`
		FileID         string `json:"fileId"`
		FileURI        string `json:"fileUri"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(out.FileMetadataID)
	if id == "" {
		id = strings.TrimSpace(out.FileID)
	}
	if id == "" {
		return "", "", fmt.Errorf("upload missing file id")
	}
	return id, strings.TrimSpace(out.FileURI), nil
}

func parseDataURI(in string) (filename, mime, b64 string, err error) {
	in = strings.TrimSpace(in)
	if strings.HasPrefix(in, "data:") {
		// data:image/png;base64,xxxx
		parts := strings.SplitN(in, ",", 2)
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("malformed data URI")
		}
		header := parts[0]
		b64 = strings.TrimSpace(parts[1])
		mime = "application/octet-stream"
		if strings.HasPrefix(header, "data:") {
			rest := header[5:]
			if i := strings.Index(rest, ";"); i >= 0 {
				mime = rest[:i]
			} else {
				mime = rest
			}
		}
		ext := "bin"
		if i := strings.Index(mime, "/"); i >= 0 {
			ext = mime[i+1:]
		}
		return "file." + ext, mime, b64, nil
	}
	// raw base64
	if _, e := base64.StdEncoding.DecodeString(in); e == nil {
		return "file.bin", "application/octet-stream", in, nil
	}
	return "", "", "", fmt.Errorf("image must be data URI or base64")
}

// PostAppChatStream POST app-chat conversations/new，返回原始响应体（SSE 文本）。
func PostAppChatStream(ctx context.Context, sso, proxyURL, referer string, payload map[string]any, dynamicStatsig bool) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GrokAppChatURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	for k, v := range BuildGrokSSOHeaders(sso, dynamicStatsig, "https://grok.com", referer) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")
	client := newGrokHTTPClient(proxyURL, 180*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	return raw, resp.StatusCode, nil
}

// BuildLiteImageChatPayload lite 图片：app-chat Drawing 路径。
// n 为期望张数（通常 1–2）；<=0 时默认 2。
func BuildLiteImageChatPayload(prompt string, n int) map[string]any {
	if n <= 0 {
		n = 2
	}
	if n > 2 {
		n = 2
	}
	return map[string]any{
		"temporary":             true,
		"modelName":             "grok-3",
		"message":               "Drawing: " + strings.TrimSpace(prompt),
		"enableImageGeneration": true,
		"returnImageBytes":      false,
		"enableImageStreaming":  true,
		"imageGenerationCount":  n,
		"forceConcise":          false,
		"enableSideBySide":      true,
		"sendFinalMetadata":     true,
		"isReasoning":           false,
		"disableTextFollowUps":  true,
		"disableMemory":         true,
	}
}

// BuildImageEditPayload 对齐 grok2api imagine-image-edit。
func BuildImageEditPayload(prompt string, imageRefs []string, parentPostID string) map[string]any {
	return map[string]any{
		"temporary":                 true,
		"modelName":                 imageEditModel,
		"message":                   prompt,
		"enableImageGeneration":     true,
		"returnImageBytes":          false,
		"returnRawGrokInXaiRequest": false,
		"enableImageStreaming":      true,
		"imageGenerationCount":      2,
		"forceConcise":              false,
		"enableSideBySide":          true,
		"sendFinalMetadata":         true,
		"isReasoning":               false,
		"disableTextFollowUps":      true,
		"responseMetadata": map[string]any{
			"modelConfigOverride": map[string]any{
				"modelMap": map[string]any{
					"imageEditModel": imageEditModel,
					"imageEditModelConfig": map[string]any{
						"imageReferences": imageRefs,
						"parentPostId":    parentPostID,
					},
				},
			},
		},
		"disableMemory":   true,
		"forceSideBySide": false,
	}
}

// BuildVideoCreatePayload 单段视频生成 payload。
func BuildVideoCreatePayload(prompt, parentPostID, aspect, resolution string, lengthSec int, imageRefs []string) map[string]any {
	if lengthSec <= 0 {
		lengthSec = 6
	}
	cfg := map[string]any{
		"parentPostId":   parentPostID,
		"aspectRatio":    aspect,
		"videoLength":    lengthSec,
		"resolutionName": resolution,
	}
	if len(imageRefs) > 0 {
		cfg["isVideoEdit"] = false
		cfg["isReferenceToVideo"] = true
		cfg["imageReferences"] = imageRefs
	}
	return map[string]any{
		"temporary":        true,
		"modelName":        videoModelName,
		"message":          prompt,
		"enableSideBySide": true,
		"responseMetadata": map[string]any{
			"experiments": []any{},
			"modelConfigOverride": map[string]any{
				"modelMap": map[string]any{
					"videoGenModelConfig": cfg,
				},
			},
		},
	}
}

// OpenAIImagesResponse 构造 images.generations 响应。
func OpenAIImagesResponse(urls []string, b64List []string) map[string]any {
	data := make([]map[string]any, 0, len(urls)+len(b64List))
	for _, u := range urls {
		if strings.TrimSpace(u) == "" {
			continue
		}
		data = append(data, map[string]any{"url": absolutizeAssetURL(u)})
	}
	for _, b := range b64List {
		if strings.TrimSpace(b) == "" {
			continue
		}
		data = append(data, map[string]any{"b64_json": b})
	}
	return map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	}
}

func absolutizeAssetURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if strings.HasPrefix(u, "/") {
		return GrokAssetsCDN + u
	}
	return GrokAssetsCDN + "/" + u
}

// ExtractGeneratedImageURLs 从 app-chat SSE 原始文本提取图片 URL。
func ExtractGeneratedImageURLs(sseBody []byte) []string {
	text := string(sseBody)
	urls := make([]string, 0)
	// 简单扫描 generatedImageUrls 与 assets 路径
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		walkCollectURLs(obj, &urls)
	}
	// 去重
	seen := map[string]struct{}{}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		u = absolutizeAssetURL(u)
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func walkCollectURLs(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if arr, ok := t["generatedImageUrls"].([]any); ok {
			for _, it := range arr {
				if s, ok := it.(string); ok && s != "" {
					*out = append(*out, s)
				}
			}
		}
		if s, ok := t["imageUrl"].(string); ok && s != "" {
			*out = append(*out, s)
		}
		if s, ok := t["url"].(string); ok && (strings.Contains(s, "/images/") || strings.Contains(s, "assets.grok")) {
			*out = append(*out, s)
		}
		// video
		if stream, ok := t["streamingVideoGenerationResponse"].(map[string]any); ok {
			if s, ok := stream["videoUrl"].(string); ok && s != "" {
				*out = append(*out, s)
			}
		}
		for _, child := range t {
			walkCollectURLs(child, out)
		}
	case []any:
		for _, child := range t {
			walkCollectURLs(child, out)
		}
	}
}

// DownloadGrokAsset 使用 SSO 拉取 assets.grok.com / 任意 https 资源字节。
func DownloadGrokAsset(ctx context.Context, sso, proxyURL, assetURL string, dynamicStatsig bool) (data []byte, contentType string, err error) {
	assetURL = absolutizeAssetURL(assetURL)
	if assetURL == "" {
		return nil, "", fmt.Errorf("empty asset url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, "", err
	}
	headers := BuildGrokSSOHeaders(sso, dynamicStatsig, "https://grok.com", "https://grok.com/")
	// 下载不走 JSON Content-Type
	delete(headers, "Content-Type")
	headers["Accept"] = "video/mp4,video/*,image/*,*/*;q=0.8"
	headers["Sec-Fetch-Dest"] = "document"
	headers["Sec-Fetch-Mode"] = "navigate"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := newGrokHTTPClient(proxyURL, 180*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 200*1024*1024))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
		if strings.Contains(assetURL, ".mp4") {
			ct = "video/mp4"
		} else if strings.Contains(assetURL, ".png") {
			ct = "image/png"
		} else if strings.Contains(assetURL, ".jpg") || strings.Contains(assetURL, ".jpeg") {
			ct = "image/jpeg"
		}
	}
	// 拒绝明显非媒体（CF HTML / JSON 错误页）
	trim := bytes.TrimSpace(raw)
	if len(trim) > 0 && (trim[0] == '<' || trim[0] == '{') {
		return nil, "", fmt.Errorf("download returned non-media content")
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("download returned empty body")
	}
	return raw, ct, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ExtractVideoResult 从 app-chat SSE 提取视频 URL / postId。
func ExtractVideoResult(sseBody []byte) (videoURL, postID, assetID string) {
	text := string(sseBody)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		walkVideo(obj, &videoURL, &postID, &assetID)
	}
	videoURL = absolutizeAssetURL(videoURL)
	return
}

// newGrokHTTPClient 不依赖 executor 包，避免 backend↔executor 循环引用。
func newGrokHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			switch u.Scheme {
			case "http", "https":
				transport.Proxy = http.ProxyURL(u)
			case "socks5", "socks5h":
				var auth *proxy.Auth
				if u.User != nil {
					pw, _ := u.User.Password()
					auth = &proxy.Auth{User: u.User.Username(), Password: pw}
				}
				if d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct); err == nil {
					transport.Proxy = nil
					transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
						return d.Dial(network, addr)
					}
				}
			}
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func walkVideo(v any, videoURL, postID, assetID *string) {
	switch t := v.(type) {
	case map[string]any:
		if stream, ok := t["streamingVideoGenerationResponse"].(map[string]any); ok {
			if s, ok := stream["videoUrl"].(string); ok && s != "" {
				*videoURL = s
			}
			if s, ok := stream["videoPostId"].(string); ok && s != "" {
				*postID = s
			}
			if s, ok := stream["videoId"].(string); ok && s != "" && *postID == "" {
				*postID = s
			}
			if s, ok := stream["assetId"].(string); ok && s != "" {
				*assetID = s
			}
		}
		if s, ok := t["fileAttachments"].([]any); ok && *assetID == "" {
			for _, it := range s {
				if id, ok := it.(string); ok && id != "" {
					*assetID = id
					break
				}
			}
		}
		for _, child := range t {
			walkVideo(child, videoURL, postID, assetID)
		}
	case []any:
		for _, child := range t {
			walkVideo(child, videoURL, postID, assetID)
		}
	}
}
