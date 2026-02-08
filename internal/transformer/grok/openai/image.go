package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	grokPool "clisimplehub/internal/transformer/grok"
	"clisimplehub/internal/transformer/grok/shared"
)

const uploadAPI = "https://grok.com/rest/app-chat/upload-file"
const createPostAPI = "https://grok.com/rest/media/post/create"

// IsGrokImagePath returns true for /v1/images/* paths.
func IsGrokImagePath(path string) bool {
	lp := strings.ToLower(path)
	return strings.HasPrefix(lp, "/v1/images/generations") || strings.HasPrefix(lp, "/v1/images/edits")
}

// HandleGrokImage dispatches image requests.
func HandleGrokImage(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	lp := strings.ToLower(path)
	switch {
	case strings.HasPrefix(lp, "/v1/images/generations"):
		handleImageGeneration(w, r.Context(), body)
	case strings.HasPrefix(lp, "/v1/images/edits"):
		handleImageEdit(w, r, body)
	default:
		writeImageError(w, http.StatusNotFound, "unknown image endpoint")
	}
}

func handleImageGeneration(w http.ResponseWriter, ctx context.Context, body []byte) {
	var req struct {
		Prompt         string `json:"prompt"`
		Model          string `json:"model"`
		N              int    `json:"n"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
		Stream         bool   `json:"stream"`
		Quality        string `json:"quality"`
		Style          string `json:"style"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeImageError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeImageError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	if req.N < 1 {
		req.N = 1
	}
	if req.Model == "" {
		req.Model = "grok-imagine-1.0"
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = "url"
	}

	account, proxyURL := selectAccount()
	if account == nil {
		writeImageError(w, http.StatusServiceUnavailable, "no available grok account")
		return
	}

	info, ok := LookupModel(req.Model)
	if !ok || !info.IsImage {
		info = ModelInfo{GrokModel: "grok-3", ModelMode: "MODEL_MODE_FAST", IsImage: true}
	}

	aspectRatio := mapSizeToAspectRatio(req.Size)

	// WebSocket image generation path
	settings := grokPool.GetSettings()
	if settings != nil && settings.GetImageWs() {
		handleImageGenerationWS(w, ctx, account.SsoToken, proxyURL, req.Prompt, req.N,
			aspectRatio, req.ResponseFormat, settings)
		return
	}

	grokBody, _ := json.Marshal(buildImageChatPayload(req.Prompt, info, nil, aspectRatio))

	// Handle streaming mode
	if req.Stream {
		handleImageGenerationStream(w, ctx, account.SsoToken, proxyURL, grokBody, req.N, req.ResponseFormat)
		return
	}

	// Non-streaming mode
	respBody, err := sendGrokChatRequest(ctx, account.SsoToken, proxyURL, grokBody)
	if err != nil {
		writeImageError(w, http.StatusBadGateway, err.Error())
		return
	}

	urls := collectImageURLs(respBody)
	if len(urls) == 0 {
		writeImageError(w, http.StatusInternalServerError, "no images generated")
		return
	}
	if len(urls) > req.N {
		urls = urls[:req.N]
	}
	writeImageSuccess(w, urls, req.ResponseFormat, proxyURL)
}

// --- edit ---

func handleImageEdit(w http.ResponseWriter, r *http.Request, body []byte) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		writeImageError(w, http.StatusBadRequest, "multipart/form-data required")
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		writeImageError(w, http.StatusBadRequest, "missing boundary")
		return
	}

	form, err := multipart.NewReader(bytes.NewReader(body), boundary).ReadForm(50 << 20)
	if err != nil {
		writeImageError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	defer form.RemoveAll()

	prompt := formValue(form, "prompt")
	if prompt == "" {
		writeImageError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	model := formValue(form, "model")
	if model == "" {
		model = "grok-imagine-1.0-edit"
	}

	if len(form.File["image"]) == 0 {
		writeImageError(w, http.StatusBadRequest, "image file is required")
		return
	}

	account, proxyURL := selectAccount()
	if account == nil {
		writeImageError(w, http.StatusServiceUnavailable, "no available grok account")
		return
	}

	var imageURLs []string
	for _, fh := range form.File["image"] {
		f, err := fh.Open()
		if err != nil {
			writeImageError(w, http.StatusBadRequest, "failed to open image")
			return
		}
		imgData, err := io.ReadAll(io.LimitReader(f, 50<<20))
		f.Close()
		if err != nil {
			writeImageError(w, http.StatusBadRequest, "failed to read image")
			return
		}
		mimeType := fh.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = detectMimeType(fh.Filename)
		}
		fileURI, err := uploadToGrok(r.Context(), account.SsoToken, proxyURL, fh.Filename, mimeType, imgData)
		if err != nil {
			writeImageError(w, http.StatusBadGateway, "upload failed: "+err.Error())
			return
		}
		if !strings.HasPrefix(fileURI, "http") {
			fileURI = "https://assets.grok.com/" + strings.TrimPrefix(fileURI, "/")
		}
		imageURLs = append(imageURLs, fileURI)
	}

	info, ok := LookupModel(model)
	if !ok {
		info = ModelInfo{GrokModel: "imagine-image-edit", ModelMode: "MODEL_MODE_FAST", IsImage: true}
	}

	override := map[string]any{
		"modelMap": map[string]any{
			"imageEditModel": "imagine",
			"imageEditModelConfig": map[string]any{
				"imageReferences": imageURLs,
			},
		},
	}

	// Try to get parentPostId via API first, then fallback to regex extraction
	parentPostID := ""
	if len(imageURLs) > 0 {
		if postID, err := createImagePost(r.Context(), account.SsoToken, proxyURL, imageURLs[0]); err == nil && postID != "" {
			parentPostID = postID
		}
	}
	if parentPostID == "" {
		parentPostID = extractParentPostID(imageURLs)
	}

	payload := buildImageChatPayload(prompt, info, override, "")
	if parentPostID != "" {
		if editConfig, ok := payload["responseMetadata"].(map[string]any); ok {
			if modelMap, ok := editConfig["modelConfigOverride"].(map[string]any); ok {
				if mm, ok := modelMap["modelMap"].(map[string]any); ok {
					if imgEditCfg, ok := mm["imageEditModelConfig"].(map[string]any); ok {
						imgEditCfg["parentPostId"] = parentPostID
					}
				}
			}
		}
	}
	payload["toolOverrides"] = map[string]any{"imageGen": true}
	payload["returnRawGrokInXaiRequest"] = false
	payload["disableTextFollowUps"] = true
	payload["isReasoning"] = false
	payload["forceSideBySide"] = false

	streamEdit := formValue(form, "stream") == "true"
	grokBody, _ := json.Marshal(payload)

	// Streaming edit
	if streamEdit {
		handleImageGenerationStream(w, r.Context(), account.SsoToken, proxyURL, grokBody, 1, "url")
		return
	}

	respBody, err := sendGrokChatRequest(r.Context(), account.SsoToken, proxyURL, grokBody)
	if err != nil {
		writeImageError(w, http.StatusBadGateway, err.Error())
		return
	}

	urls := collectImageURLs(respBody)
	if len(urls) == 0 {
		writeImageError(w, http.StatusInternalServerError, "no images generated")
		return
	}
	writeImageSuccess(w, urls, "url", proxyURL)
}

// --- shared helpers ---

func selectAccount() (*shared.GrokAccount, string) {
	pool := grokPool.GetPool()
	if pool == nil {
		return nil, ""
	}
	account := pool.Select()
	if account == nil {
		return nil, ""
	}
	return account, account.ProxyUrl
}

type staticAuthSource struct {
	token string
	proxy string
}

func (s *staticAuthSource) GetSsoToken() string                   { return s.token }
func (s *staticAuthSource) GrokProxyURL() string                  { return s.proxy }
func (s *staticAuthSource) GetSettings() *shared.GrokSettings     { return grokPool.GetSettings() }

func buildImageChatPayload(prompt string, info ModelInfo, modelConfigOverride map[string]any, aspectRatio string) map[string]any {
	settings := grokPool.GetSettings()
	if settings == nil {
		def := shared.DefaultSettings()
		settings = &def
	}
	p := map[string]any{
		"temporary":             settings.GetTemporary(),
		"modelName":             info.GrokModel,
		"message":               prompt,
		"fileAttachments":       []any{},
		"imageAttachments":      []any{},
		"disableSearch":         false,
		"enableImageGeneration": true,
		"returnImageBytes":      false,
		"enableImageStreaming":  true,
		"imageGenerationCount":  2,
		"forceConcise":          false,
		"toolOverrides":         map[string]any{},
		"enableSideBySide":      true,
		"sendFinalMetadata":     true,
		"disableMemory":         settings.GetDisableMemory(),
	}
	if info.ModelMode != "" {
		p["modelMode"] = info.ModelMode
	}
	if modelConfigOverride != nil {
		p["responseMetadata"] = map[string]any{"modelConfigOverride": modelConfigOverride}
	}
	if aspectRatio != "" {
		p["aspectRatio"] = aspectRatio
	}
	return p
}

func sendGrokChatRequest(ctx context.Context, ssoToken, proxyURL string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GrokChatAPI+ChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applier := grokPool.NewAuthApplier(&staticAuthSource{token: ssoToken, proxy: proxyURL})
	if err := applier.Apply(req); err != nil {
		return nil, err
	}
	client := newProxyClient(proxyURL, 120*time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("grok returned %d: %s", resp.StatusCode, string(errBody))
	}
	return io.ReadAll(resp.Body)
}

func uploadToGrok(ctx context.Context, ssoToken, proxyURL, filename, mimeType string, data []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(data)
	payload, _ := json.Marshal(map[string]string{
		"fileName":     filename,
		"fileMimeType": mimeType,
		"content":      b64,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadAPI, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	applier := grokPool.NewAuthApplier(&staticAuthSource{token: ssoToken, proxy: proxyURL})
	if err := applier.Apply(req); err != nil {
		return "", err
	}
	client := newProxyClient(proxyURL, 60*time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("upload returned %d: %s", resp.StatusCode, string(errBody))
	}
	var result struct {
		FileURI string `json:"fileUri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.FileURI, nil
}

// collectImageURLs extracts image URLs from Grok NDJSON response.
func collectImageURLs(data []byte) []string {
	var urls []string
	seen := make(map[string]bool)
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			continue
		}
		result, _ := obj["result"].(map[string]any)
		if result == nil {
			continue
		}
		resp, _ := result["response"].(map[string]any)
		if resp == nil {
			continue
		}
		mr, _ := resp["modelResponse"].(map[string]any)
		if mr == nil {
			continue
		}
		walkImageURLs(mr, seen, &urls)
	}
	return urls
}

func walkImageURLs(v any, seen map[string]bool, out *[]string) {
	switch val := v.(type) {
	case map[string]any:
		for key, item := range val {
			if key == "generatedImageUrls" || key == "imageUrls" || key == "imageURLs" {
				switch u := item.(type) {
				case []any:
					for _, elem := range u {
						if s, ok := elem.(string); ok && s != "" && !seen[s] {
							seen[s] = true
							*out = append(*out, s)
						}
					}
				case string:
					if u != "" && !seen[u] {
						seen[u] = true
						*out = append(*out, u)
					}
				}
				continue
			}
			walkImageURLs(item, seen, out)
		}
	case []any:
		for _, item := range val {
			walkImageURLs(item, seen, out)
		}
	}
}

func writeImageError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": "invalid_request_error"},
	})
}

func writeImageSuccess(w http.ResponseWriter, urls []string, responseFormat string, proxyURL string) {
	data := wrapURLs(urls, responseFormat, proxyURL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	})
}

func formValue(form *multipart.Form, key string) string {
	if vs, ok := form.Value[key]; ok && len(vs) > 0 {
		return strings.TrimSpace(vs[0])
	}
	return ""
}

func newProxyClient(proxyURL string, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if u, err := url.Parse(strings.TrimSpace(proxyURL)); err == nil && proxyURL != "" {
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// mapSizeToAspectRatio maps OpenAI size format to Grok aspect ratio
func mapSizeToAspectRatio(size string) string {
	if size == "" {
		return "2:3"
	}
	// Direct ratio format
	if matched, _ := regexp.MatchString(`^\d+:\d+$`, size); matched {
		return size
	}
	// Parse WxH format
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return "2:3"
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return "2:3"
	}
	// Map known sizes
	sizeKey := fmt.Sprintf("%dx%d", w, h)
	mapping := map[string]string{
		"1024x1024": "1:1",
		"512x512":   "1:1",
		"1024x576":  "16:9",
		"1280x720":  "16:9",
		"1536x864":  "16:9",
		"576x1024":  "9:16",
		"720x1280":  "9:16",
		"864x1536":  "9:16",
		"1024x1536": "2:3",
		"512x768":   "2:3",
		"768x1024":  "2:3",
		"1536x1024": "3:2",
		"768x512":   "3:2",
		"1024x768":  "3:2",
	}
	if ratio, ok := mapping[sizeKey]; ok {
		return ratio
	}
	// Fallback: calculate ratio
	if w == h {
		return "1:1"
	} else if w > h {
		return "3:2"
	}
	return "2:3"
}

// wrapURLs converts URLs to response format (url or b64_json)
func wrapURLs(urls []string, format string, proxyURL string) []map[string]any {
	data := make([]map[string]any, len(urls))
	for i, u := range urls {
		if format == "b64_json" || format == "base64" {
			// Try to download and convert to base64
			b64, err := downloadAndConvertToBase64(u, proxyURL)
			if err == nil && b64 != "" {
				data[i] = map[string]any{"b64_json": b64}
			} else {
				// Fallback to URL on error
				data[i] = map[string]any{"url": u}
			}
		} else {
			data[i] = map[string]any{"url": u}
		}
	}
	return data
}

// downloadAndConvertToBase64 downloads an image and converts it to base64
func downloadAndConvertToBase64(imageURL string, proxyURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
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

	// Limit to 50MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

// createImagePost calls the Grok media post API to get a parentPostId for image edits.
func createImagePost(ctx context.Context, ssoToken, proxyURL, imageURL string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"mediaType": "MEDIA_POST_TYPE_IMAGE",
		"mediaUrl":  imageURL,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createPostAPI, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	applier := grokPool.NewAuthApplier(&staticAuthSource{token: ssoToken, proxy: proxyURL})
	if err := applier.Apply(req); err != nil {
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
		return "", fmt.Errorf("create post returned %d", resp.StatusCode)
	}
	var result struct {
		Post struct {
			ID string `json:"id"`
		} `json:"post"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Post.ID == "" {
		return "", fmt.Errorf("no post ID in response")
	}
	return result.Post.ID, nil
}

// extractParentPostID extracts post ID from Grok image URLs
func extractParentPostID(imageURLs []string) string {
	if len(imageURLs) == 0 {
		return ""
	}
	// Try regex patterns
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`/generated/([a-f0-9-]+)/`),
		regexp.MustCompile(`/users/[^/]+/([a-z0-9-]+)/`),
	}
	for _, url := range imageURLs {
		for _, pattern := range patterns {
			if matches := pattern.FindStringSubmatch(url); len(matches) > 1 {
				return matches[1]
			}
		}
	}
	return ""
}

// handleImageGenerationStream handles streaming SSE responses
func handleImageGenerationStream(w http.ResponseWriter, ctx context.Context, ssoToken, proxyURL string, grokBody []byte, n int, responseFormat string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeImageError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	resp, err := sendGrokChatRequestStream(ctx, ssoToken, proxyURL, grokBody)
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", toJSON(map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "upstream_error"},
		}))
		flusher.Flush()
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	seen := make(map[string]bool)
	var collectedURLs []string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			continue
		}

		result, _ := obj["result"].(map[string]any)
		if result == nil {
			continue
		}
		response, _ := result["response"].(map[string]any)
		if response == nil {
			continue
		}

		// Check for progress events
		if imgResp, ok := response["streamingImageGenerationResponse"].(map[string]any); ok {
			imageIndex, _ := imgResp["imageIndex"].(float64)
			progress, _ := imgResp["progress"].(float64)
			event := map[string]any{
				"type":     "image_generation.partial_image",
				"index":    int(imageIndex),
				"progress": progress,
				"url":      "",
			}
			fmt.Fprintf(w, "event: image_generation.partial_image\ndata: %s\n\n", toJSON(event))
			flusher.Flush()
			continue
		}

		// Check for final images
		if mr, ok := response["modelResponse"].(map[string]any); ok {
			var urls []string
			walkImageURLs(mr, seen, &urls)
			for _, u := range urls {
				if !contains(collectedURLs, u) {
					collectedURLs = append(collectedURLs, u)
				}
			}
		}
	}

	// Send completed events
	if len(collectedURLs) > n {
		collectedURLs = collectedURLs[:n]
	}
	for i, u := range collectedURLs {
		var eventData map[string]any
		if responseFormat == "b64_json" || responseFormat == "base64" {
			b64, _ := downloadAndConvertToBase64(u, proxyURL)
			eventData = map[string]any{
				"type":     "image_generation.completed",
				"index":    i,
				"b64_json": b64,
			}
		} else {
			eventData = map[string]any{
				"type":  "image_generation.completed",
				"index": i,
				"url":   u,
			}
		}
		fmt.Fprintf(w, "event: image_generation.completed\ndata: %s\n\n", toJSON(eventData))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// sendGrokChatRequestStream returns the response for streaming
func sendGrokChatRequestStream(ctx context.Context, ssoToken, proxyURL string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GrokChatAPI+ChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applier := grokPool.NewAuthApplier(&staticAuthSource{token: ssoToken, proxy: proxyURL})
	if err := applier.Apply(req); err != nil {
		return nil, err
	}
	client := newProxyClient(proxyURL, 120*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("grok returned %d: %s", resp.StatusCode, string(errBody))
	}
	return resp, nil
}

// toJSON converts value to JSON string
func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// contains checks if string slice contains a value
func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
