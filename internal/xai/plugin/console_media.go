package xaiplugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	xai "clisimplehub/internal/xai"
	xaiBackend "clisimplehub/internal/xai/backend"
	xaiShared "clisimplehub/internal/xai/shared"
)

// HandleConsoleImagesGenerations POST /xai/console/v1/images/generations
// 对齐 grok2api：wss://grok.com/ws/imagine/listen + SSO。
func (s *XaiService) HandleConsoleImagesGenerations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	_ = r.Body.Close()

	var req struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		N              int    `json:"n"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.Size == "" {
		req.Size = "1024x1024"
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = "url"
	}
	enablePro := strings.Contains(strings.ToLower(req.Model), "pro") ||
		strings.Contains(strings.ToLower(req.Model), "quality")

	s.withConsoleAccount(w, r, func(ctx context.Context, pool *xai.XaiAccountPool, acc *xaiShared.XaiAccount, proxyURL string, dynamicStatsig bool) (int, bool, error) {
		out, err := xaiBackend.ImagineGenerate(
			ctx,
			acc.SSO,
			proxyURL,
			req.Prompt,
			req.Size,
			req.ResponseFormat,
			req.N,
			dynamicStatsig,
			enablePro,
		)
		if err != nil {
			status, retryable := classifyConsoleMediaErr(pool, acc, err)
			return status, retryable, err
		}
		pool.ReportSuccess(acc.ID)
		writeJSON(w, http.StatusOK, out)
		return 0, false, nil
	})
}

// HandleConsoleImagesEdits POST /xai/console/v1/images/edits
// JSON 体：{model,prompt,image|images:[dataURI],n,response_format}
// 对齐 grok2api：upload + media/post/create + app-chat imagine-image-edit。
func (s *XaiService) HandleConsoleImagesEdits(w http.ResponseWriter, r *http.Request) {
	// 支持 JSON 与 multipart（简化：优先 JSON）
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	var prompt string
	var images []string
	var n int
	var responseFormat string

	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		prompt = strings.TrimSpace(r.FormValue("prompt"))
		n, _ = strconv.Atoi(r.FormValue("n"))
		responseFormat = r.FormValue("response_format")
		// image[] files → 读成 base64 data URI
		if r.MultipartForm != nil {
			for _, fhs := range r.MultipartForm.File {
				for _, fh := range fhs {
					f, err := fh.Open()
					if err != nil {
						continue
					}
					raw, _ := io.ReadAll(io.LimitReader(f, 12<<20))
					_ = f.Close()
					if len(raw) == 0 {
						continue
					}
					mime := fh.Header.Get("Content-Type")
					if mime == "" {
						mime = "image/png"
					}
					images = append(images, "data:"+mime+";base64,"+encodeB64(raw))
				}
			}
		}
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
			return
		}
		_ = r.Body.Close()
		var req struct {
			Model          string          `json:"model"`
			Prompt         string          `json:"prompt"`
			N              int             `json:"n"`
			ResponseFormat string          `json:"response_format"`
			Image          json.RawMessage `json:"image"`
			Images         []string        `json:"images"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		prompt = req.Prompt
		n = req.N
		responseFormat = req.ResponseFormat
		images = append(images, req.Images...)
		if len(req.Image) > 0 {
			var one string
			if json.Unmarshal(req.Image, &one) == nil && one != "" {
				images = append(images, one)
			} else {
				var arr []string
				if json.Unmarshal(req.Image, &arr) == nil {
					images = append(images, arr...)
				}
			}
		}
	}
	if strings.TrimSpace(prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}
	if len(images) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "image is required (data URI)"})
		return
	}
	if n <= 0 {
		n = 1
	}
	if n > 2 {
		n = 2
	}
	if responseFormat == "" {
		responseFormat = "url"
	}

	s.withConsoleAccount(w, r, func(ctx context.Context, pool *xai.XaiAccountPool, acc *xaiShared.XaiAccount, proxyURL string, dynamicStatsig bool) (int, bool, error) {
		refs := make([]string, 0, len(images))
		for _, img := range images {
			id, uri, err := xaiBackend.UploadAssetDataURI(ctx, acc.SSO, proxyURL, img, dynamicStatsig)
			if err != nil {
				return http.StatusBadGateway, true, fmt.Errorf("upload failed: %w", err)
			}
			ref := uri
			if ref == "" {
				ref = id
			}
			refs = append(refs, ref)
		}
		postID, err := xaiBackend.CreateMediaPost(ctx, acc.SSO, proxyURL, "MEDIA_POST_TYPE_IMAGE", prompt, dynamicStatsig)
		if err != nil {
			status, retryable := classifyConsoleMediaErr(pool, acc, err)
			return status, retryable, err
		}
		payload := xaiBackend.BuildImageEditPayload(prompt, refs, postID)
		raw, status, err := xaiBackend.PostAppChatStream(ctx, acc.SSO, proxyURL, "https://grok.com/imagine", payload, dynamicStatsig)
		if err != nil {
			st, retryable := classifyConsoleMediaErr(pool, acc, err)
			return st, retryable, err
		}
		if status < 200 || status >= 300 {
			msg := strings.TrimSpace(string(raw))
			if msg == "" {
				msg = fmt.Sprintf("console media HTTP %d", status)
			}
			retryable := status == http.StatusTooManyRequests || status >= 500
			if status == http.StatusTooManyRequests {
				pool.CooldownAccount(acc.ID, 15*time.Second, "console_media_rate_limited")
			} else if isFreeUsageExhaustedBody(raw) || isQuotaLikeBody(raw) {
				pool.MarkFailed(acc.ID, xaiShared.XaiStatusExhausted, 0, "console_media_quota")
				retryable = true
			}
			return status, retryable, fmt.Errorf("%s", msg)
		}
		urls := xaiBackend.ExtractGeneratedImageURLs(raw)
		if len(urls) == 0 {
			return http.StatusBadGateway, true, fmt.Errorf("image edit returned no urls: %s", truncate(string(raw), 400))
		}
		if len(urls) > n {
			urls = urls[:n]
		}
		_ = responseFormat
		pool.ReportSuccess(acc.ID)
		writeJSON(w, http.StatusOK, xaiBackend.OpenAIImagesResponse(urls, nil))
		return 0, false, nil
	})
}

// HandleConsoleVideosCreate POST /xai/console/v1/videos
// 异步任务：create media post VIDEO + app-chat videoGenModelConfig。
func (s *XaiService) HandleConsoleVideosCreate(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	var model, prompt, size, resolution, preset string
	var seconds int
	var refs []string

	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		model = r.FormValue("model")
		prompt = r.FormValue("prompt")
		size = r.FormValue("size")
		resolution = r.FormValue("resolution_name")
		preset = r.FormValue("preset")
		seconds, _ = strconv.Atoi(r.FormValue("seconds"))
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
			return
		}
		_ = r.Body.Close()
		var req struct {
			Model           string   `json:"model"`
			Prompt          string   `json:"prompt"`
			Seconds         int      `json:"seconds"`
			Size            string   `json:"size"`
			ResolutionName  string   `json:"resolution_name"`
			Preset          string   `json:"preset"`
			InputReferences []string `json:"input_references"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		model, prompt, size = req.Model, req.Prompt, req.Size
		resolution, preset, seconds = req.ResolutionName, req.Preset, req.Seconds
		refs = req.InputReferences
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}
	if model == "" {
		model = "grok-imagine-video"
	}
	if size == "" {
		size = "720x1280"
	}
	if seconds <= 0 {
		seconds = 6
	}
	if seconds > 10 {
		seconds = 10 // 首版仅单段
	}
	aspect, defRes := xaiBackend.ResolveVideoSize(size)
	if resolution == "" {
		resolution = defRes
	}
	_ = preset

	jobID := "video_" + uuid.NewString()
	job := &xaiBackend.ConsoleVideoJob{
		ID:        jobID,
		Object:    "video",
		Model:     model,
		Status:    "queued",
		Progress:  0,
		CreatedAt: time.Now().Unix(),
		Prompt:    prompt,
		Seconds:   strconv.Itoa(seconds),
		Size:      size,
	}
	xaiBackend.PutConsoleVideoJob(job)
	xaiBackend.ExpireConsoleVideoJob(jobID, 2*time.Hour)

	// 异步执行
	go s.runConsoleVideoJob(jobID, prompt, aspect, resolution, seconds, refs)

	writeJSON(w, http.StatusOK, job.ToDict())
}

func (s *XaiService) runConsoleVideoJob(jobID, prompt, aspect, resolution string, seconds int, refs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool := xai.GetPool()
	if pool == nil {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = "xai pool not initialized"
		})
		return
	}
	acc := pool.SelectConsole()
	if acc == nil {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = "no available basic SSO accounts"
		})
		return
	}
	proxyURL := resolveAccountProxy(pool, acc)
	dynamicStatsig := true
	if snap := pool.Snapshot(); snap != nil {
		dynamicStatsig = snap.Config.DynamicStatsigEnabled()
	}

	xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
		j.Status = "in_progress"
		j.Progress = 5
	})

	imageRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.HasPrefix(strings.TrimSpace(ref), "data:") {
			id, uri, err := xaiBackend.UploadAssetDataURI(ctx, acc.SSO, proxyURL, ref, dynamicStatsig)
			if err != nil {
				xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
					j.Status = "failed"
					j.Error = "upload reference failed: " + err.Error()
				})
				return
			}
			if uri != "" {
				imageRefs = append(imageRefs, uri)
			} else {
				imageRefs = append(imageRefs, id)
			}
		} else if strings.TrimSpace(ref) != "" {
			imageRefs = append(imageRefs, strings.TrimSpace(ref))
		}
	}

	postID, err := xaiBackend.CreateMediaPost(ctx, acc.SSO, proxyURL, "MEDIA_POST_TYPE_VIDEO", prompt, dynamicStatsig)
	if err != nil {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = err.Error()
		})
		return
	}
	xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) { j.Progress = 20 })

	payload := xaiBackend.BuildVideoCreatePayload(prompt, postID, aspect, resolution, seconds, imageRefs)
	raw, status, err := xaiBackend.PostAppChatStream(ctx, acc.SSO, proxyURL, "https://grok.com/imagine", payload, dynamicStatsig)
	if err != nil {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = err.Error()
		})
		return
	}
	if status < 200 || status >= 300 {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = fmt.Sprintf("HTTP %d: %s", status, truncate(string(raw), 300))
		})
		return
	}
	videoURL, vPost, assetID := xaiBackend.ExtractVideoResult(raw)
	if videoURL == "" && assetID != "" {
		videoURL = "https://assets.grok.com/" + assetID
	}
	if videoURL == "" {
		xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
			j.Status = "failed"
			j.Error = "video generation returned no url"
		})
		return
	}
	_ = vPost
	// 尽量预取视频字节，方便 content 接口直出
	content, ct, dlErr := xaiBackend.DownloadGrokAsset(ctx, acc.SSO, proxyURL, videoURL, dynamicStatsig)
	pool.ReportSuccess(acc.ID)
	xaiBackend.UpdateConsoleVideoJob(jobID, func(j *xaiBackend.ConsoleVideoJob) {
		j.Status = "completed"
		j.Progress = 100
		j.CompletedAt = time.Now().Unix()
		j.VideoURL = videoURL
		if dlErr == nil && len(content) > 0 {
			j.Content = content
			if ct == "" {
				ct = "video/mp4"
			}
			j.ContentType = ct
		}
	})
}

// HandleConsoleVideosGet GET /xai/console/v1/videos/{id}
// 查询异步视频任务状态（对齐 grok2api GET /v1/videos/{video_id}）。
func (s *XaiService) HandleConsoleVideosGet(w http.ResponseWriter, r *http.Request) {
	id := videoIDFromPath(r.URL.Path)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "video id required"})
		return
	}
	job := xaiBackend.GetConsoleVideoJob(id)
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "video not found"})
		return
	}
	writeJSON(w, http.StatusOK, job.ToDict())
}

// HandleConsoleVideosContent GET /xai/console/v1/videos/{id}/content
// 获取最终视频文件（mp4 字节流）。对齐 grok2api GET /v1/videos/{id}/content。
// 优先使用任务内缓存；否则用 basic+SSO 从 grok CDN 拉取后回写。
func (s *XaiService) HandleConsoleVideosContent(w http.ResponseWriter, r *http.Request) {
	id := videoIDFromPath(strings.TrimSuffix(r.URL.Path, "/content"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "video id required"})
		return
	}
	job := xaiBackend.GetConsoleVideoJob(id)
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "video not found"})
		return
	}
	if job.Status != "completed" || strings.TrimSpace(job.VideoURL) == "" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "video content is not ready yet",
			"status": job.Status,
		})
		return
	}

	// 已有缓存
	if len(job.Content) > 0 {
		ct := job.ContentType
		if ct == "" {
			ct = "video/mp4"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.mp4"`, id))
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(job.Content)
		return
	}

	// 拉取并缓存
	ctx := r.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	pool := xai.GetPool()
	if pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "xai pool not initialized"})
		return
	}
	acc := pool.SelectConsole()
	if acc == nil {
		// 无可用账号时退回 302（CDN 可能公开）
		http.Redirect(w, r, job.VideoURL, http.StatusFound)
		return
	}
	proxyURL := resolveAccountProxy(pool, acc)
	dynamicStatsig := true
	if snap := pool.Snapshot(); snap != nil {
		dynamicStatsig = snap.Config.DynamicStatsigEnabled()
	}
	raw, ct, err := xaiBackend.DownloadGrokAsset(ctx, acc.SSO, proxyURL, job.VideoURL, dynamicStatsig)
	if err != nil {
		// 下载失败时仍尝试 302
		http.Redirect(w, r, job.VideoURL, http.StatusFound)
		return
	}
	if ct == "" {
		ct = "video/mp4"
	}
	xaiBackend.UpdateConsoleVideoJob(id, func(j *xaiBackend.ConsoleVideoJob) {
		j.Content = raw
		j.ContentType = ct
	})
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.mp4"`, id))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// classifyConsoleMediaErr 将 imagine/media 错误归类为是否可换号重试。
func classifyConsoleMediaErr(pool *xai.XaiAccountPool, acc *xaiShared.XaiAccount, err error) (status int, retryable bool) {
	if err == nil {
		return http.StatusOK, false
	}
	msg := strings.ToLower(err.Error())
	status = http.StatusBadGateway
	retryable = true
	switch {
	case strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") || strings.Contains(msg, "429"):
		status = http.StatusTooManyRequests
		if pool != nil && acc != nil {
			pool.CooldownAccount(acc.ID, 15*time.Second, "console_image_rate_limited")
		}
	case strings.Contains(msg, "free-usage") || strings.Contains(msg, "free usage") ||
		strings.Contains(msg, "spending-limit") || strings.Contains(msg, "quota"):
		status = http.StatusTooManyRequests
		if pool != nil && acc != nil {
			pool.MarkFailed(acc.ID, xaiShared.XaiStatusExhausted, 0, "console_media_quota")
		}
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "401") || strings.Contains(msg, "403"):
		status = http.StatusUnauthorized
		if pool != nil && acc != nil {
			pool.MarkFailed(acc.ID, xaiShared.XaiStatusBanned, 0, "console_media_auth")
		}
	case strings.Contains(msg, "invalid") && strings.Contains(msg, "prompt"):
		// 请求本身问题，换号无意义
		status = http.StatusBadRequest
		retryable = false
	}
	return status, retryable
}

// consoleAccountFn 成功时自行写响应并返回 err=nil。
// 失败时不要写 w：返回 status + retryable，由 withConsoleAccount 决定换号或落最终错误。
type consoleAccountFn func(
	ctx context.Context,
	pool *xai.XaiAccountPool,
	acc *xaiShared.XaiAccount,
	proxyURL string,
	dynamicStatsig bool,
) (status int, retryable bool, err error)

// withConsoleAccount 选 basic+SSO 账号执行；failover/loadbalance 下可重试换号。
func (s *XaiService) withConsoleAccount(
	w http.ResponseWriter,
	r *http.Request,
	fn consoleAccountFn,
) {
	pool := xai.GetPool()
	if pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"type": "no_accounts", "message": "xai pool not initialized"},
		})
		return
	}
	mode := pool.Mode()
	first := pool.SelectConsole()
	if first == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"type":    "no_accounts",
				"message": "no available basic SSO accounts for /xai/console",
				"mode":    mode,
			},
		})
		return
	}
	ctx := r.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	dynamicStatsig := true
	if snap := pool.Snapshot(); snap != nil {
		dynamicStatsig = snap.Config.DynamicStatsigEnabled()
	}

	excluded := make(map[string]bool)
	var lastStatus int
	var lastErr error

	for attempt := 0; attempt < maxRetryAccounts; attempt++ {
		select {
		case <-ctx.Done():
			writeJSON(w, 499, map[string]any{"error": map[string]any{"message": "request cancelled"}})
			return
		default:
		}

		var acc *xaiShared.XaiAccount
		if attempt == 0 {
			acc = first
		} else {
			if mode == xaiShared.RotationFixed {
				break
			}
			acc = pool.SelectConsoleExcluding(excluded)
			if acc == nil {
				break
			}
		}

		proxyURL := resolveAccountProxy(pool, acc)
		status, retryable, err := fn(ctx, pool, acc, proxyURL, dynamicStatsig)
		if err == nil {
			return
		}
		lastStatus = status
		lastErr = err
		if !retryable {
			writeJSON(w, statusOr(status, http.StatusBadGateway), map[string]any{
				"error": map[string]any{"message": err.Error()},
			})
			return
		}
		excluded[strings.TrimSpace(acc.ID)] = true
	}

	writeJSON(w, statusOr(lastStatus, http.StatusBadGateway), map[string]any{
		"error": map[string]any{
			"type":    "all_accounts_failed",
			"message": errString(lastErr),
		},
	})
}

func videoIDFromPath(path string) string {
	// .../videos/{id} or .../videos/{id}/content
	path = strings.TrimSpace(path)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if p == "videos" && i+1 < len(parts) {
			id := parts[i+1]
			if id == "generations" {
				return ""
			}
			return id
		}
	}
	return ""
}

func encodeB64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
