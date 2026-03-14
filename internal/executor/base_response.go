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
	return getEncodedReader(encoding, resp.Body)
}

func getEncodedReader(encoding string, body io.Reader) io.Reader {
	if strings.EqualFold(encoding, "gzip") {
		gzReader, err := gzip.NewReader(body)
		if err != nil {
			logger.Warn("[Executor] gzip reader failed: %v", err)
			return body
		}
		return gzReader
	}
	return body
}
