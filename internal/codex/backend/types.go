package backend

import (
	"io"
	"net/http"
	"time"

	codexShared "clisimplehub/internal/codex/shared"
)

const (
	SourceCodex       = "codex"
	SourceOpenAI      = "openai"
	SourceOpenAIImage = "openai-image"
	SourceClaude      = "claude"

	ImagesGenerationsPath = "/v1/images/generations"
	ImagesEditsPath       = "/v1/images/edits"
)

type Request struct {
	Method       string
	Path         string
	Source       string
	Model        string
	Body         []byte
	OriginalBody []byte
	Headers      http.Header
	IsStreaming  bool

	Config *codexShared.CodexMultiConfig
	Client *http.Client

	AccessToken            string
	AccountID              string
	LocalAccountID         string
	PlanType               string
	DisableImageGeneration string

	// ReplaySessionKey 可选显式 session；空则从 body/headers/OriginalBody 解析。
	ReplaySessionKey string

	Attempts   int
	RetryDelay time.Duration
}

type Result struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte
	Stream        io.ReadCloser
	TargetURL     string
	TargetHeaders map[string]string
	RequestBody   []byte
	Error         error
	ReplayScope   ReplayScope
}

type IdentityState struct {
	enabled                bool
	authID                 string
	originalPromptCacheKey string
	promptCacheKey         string
	turnIDs                []identityReplacement
}

type identityReplacement struct {
	original string
	confused string
}

type StatusError struct {
	Code       int
	Body       []byte
	RetryAfter *time.Duration
}

func (e StatusError) Error() string {
	if len(e.Body) > 0 {
		return string(e.Body)
	}
	return http.StatusText(e.Code)
}

func (e StatusError) StatusCode() int {
	return e.Code
}
