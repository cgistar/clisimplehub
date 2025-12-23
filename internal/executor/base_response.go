package executor

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"clisimplehub/internal/logger"
)

func getResponseReader(resp *http.Response) io.Reader {
	encoding := resp.Header.Get("Content-Encoding")
	if strings.EqualFold(encoding, "gzip") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			logger.Warn("[Executor] gzip reader failed: %v", err)
			return resp.Body
		}
		return gzReader
	}
	return resp.Body
}
