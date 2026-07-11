package backend

import (
	"io"
	"net/http"
	"time"

	xaiShared "clisimplehub/internal/xai/shared"
)

type Request struct {
	Method      string
	Path        string // inbound path, e.g. /xai/v1/responses
	RawQuery    string
	Body        []byte
	Headers     http.Header
	IsStreaming bool
	Model       string

	Config      *xaiShared.XaiMultiConfig
	Account     *xaiShared.XaiAccount
	AccessToken string
	ProxyURL    string
	Client      *http.Client

	// EnableReplay：Claude 等多轮源启用 reasoning replay
	EnableReplay bool

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
	ReplayScope   ReplayScope
	Error         error
}
