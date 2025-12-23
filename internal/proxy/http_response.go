package proxy

import (
	"net/http"
	"strings"
)

func writeResponseWithHeaders(w http.ResponseWriter, statusCode int, headers http.Header, body []byte) {
	if headers != nil {
		for key, values := range headers {
			if strings.EqualFold(key, "Content-Length") {
				continue
			}
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(body)
}
