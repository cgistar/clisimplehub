package backend

import (
	"sync"
	"time"
)

// ConsoleVideoJob 异步视频任务（内存，进程内）。
type ConsoleVideoJob struct {
	ID                 string `json:"id"`
	Object             string `json:"object"`
	Model              string `json:"model"`
	Status             string `json:"status"` // queued|in_progress|completed|failed
	Progress           int    `json:"progress"`
	CreatedAt          int64  `json:"created_at"`
	CompletedAt        int64  `json:"completed_at,omitempty"`
	Prompt             string `json:"prompt,omitempty"`
	Seconds            string `json:"seconds,omitempty"`
	Size               string `json:"size,omitempty"`
	VideoURL           string `json:"video_url,omitempty"`
	// Content 可选本地缓存的 mp4 字节（完成时按需下载填充）
	Content     []byte `json:"-"`
	ContentType string `json:"-"`
	Error              string `json:"error,omitempty"`
	RemixedFromVideoID string `json:"remixed_from_video_id,omitempty"`
}

var (
	consoleVideoJobs   = map[string]*ConsoleVideoJob{}
	consoleVideoJobsMu sync.RWMutex
)

func PutConsoleVideoJob(job *ConsoleVideoJob) {
	if job == nil || job.ID == "" {
		return
	}
	consoleVideoJobsMu.Lock()
	consoleVideoJobs[job.ID] = job
	consoleVideoJobsMu.Unlock()
}

func GetConsoleVideoJob(id string) *ConsoleVideoJob {
	consoleVideoJobsMu.RLock()
	defer consoleVideoJobsMu.RUnlock()
	j := consoleVideoJobs[id]
	if j == nil {
		return nil
	}
	// 浅拷贝
	cp := *j
	return &cp
}

func UpdateConsoleVideoJob(id string, fn func(*ConsoleVideoJob)) {
	consoleVideoJobsMu.Lock()
	defer consoleVideoJobsMu.Unlock()
	j := consoleVideoJobs[id]
	if j == nil {
		return
	}
	fn(j)
}

func ExpireConsoleVideoJob(id string, ttl time.Duration) {
	time.AfterFunc(ttl, func() {
		consoleVideoJobsMu.Lock()
		delete(consoleVideoJobs, id)
		consoleVideoJobsMu.Unlock()
	})
}

func (j *ConsoleVideoJob) ToDict() map[string]any {
	if j == nil {
		return nil
	}
	out := map[string]any{
		"id":         j.ID,
		"object":     "video",
		"model":      j.Model,
		"status":     j.Status,
		"progress":   j.Progress,
		"created_at": j.CreatedAt,
	}
	if j.CompletedAt > 0 {
		out["completed_at"] = j.CompletedAt
	}
	if j.Seconds != "" {
		out["seconds"] = j.Seconds
	}
	if j.Size != "" {
		out["size"] = j.Size
	}
	if j.VideoURL != "" {
		out["video_url"] = j.VideoURL
	}
	if j.Error != "" {
		out["error"] = map[string]any{"message": j.Error}
	}
	if j.RemixedFromVideoID != "" {
		out["remixed_from_video_id"] = j.RemixedFromVideoID
	}
	return out
}
