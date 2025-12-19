// Package proxy implements the HTTP proxy server for AI API requests.
package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebDAVConfig holds WebDAV server configuration
type WebDAVConfig struct {
	ServerURL string `json:"serverUrl"` // WebDAV server base URL
	Username  string `json:"username"`  // Basic auth username
	Password  string `json:"password"`  // Basic auth password
}

// WebDAVProxy handles WebDAV requests proxying
type WebDAVProxy struct {
	client *http.Client
}

// NewWebDAVProxy creates a new WebDAV proxy instance
func NewWebDAVProxy() *WebDAVProxy {
	return &WebDAVProxy{
		client: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Follow redirects but preserve method and auth
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				// Copy auth header from original request
				if len(via) > 0 && via[0].Header.Get("Authorization") != "" {
					req.Header.Set("Authorization", via[0].Header.Get("Authorization"))
				}
				return nil
			},
		},
	}
}

// WebDAVResponse represents a response from WebDAV operations
type WebDAVResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// WebDAVFileInfo represents file/directory information
type WebDAVFileInfo struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	IsDir        bool   `json:"isDir"`
	Size         int64  `json:"size"`
	LastModified string `json:"lastModified,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
}

// buildWebDAVURL constructs the full WebDAV URL
func buildWebDAVURL(serverURL, path string) (string, error) {
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		return "", fmt.Errorf("empty server URL")
	}

	// Ensure server URL has scheme
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "https://" + serverURL
	}

	// Parse and validate URL
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}

	// Normalize path
	path = strings.TrimSpace(path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Combine base URL with path
	u.Path = strings.TrimRight(u.Path, "/") + path

	return u.String(), nil
}

// ProxyRequest proxies a WebDAV request to the target server
func (w *WebDAVProxy) ProxyRequest(config *WebDAVConfig, method, path string, body io.Reader, headers map[string]string) (*WebDAVResponse, error) {
	if config == nil {
		return nil, fmt.Errorf("WebDAV config is required")
	}

	targetURL, err := buildWebDAVURL(config.ServerURL, path)
	if err != nil {
		return nil, err
	}

	// Create request
	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set basic auth if provided
	if config.Username != "" {
		req.SetBasicAuth(config.Username, config.Password)
	}

	// Copy custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := w.client.Do(req)
	if err != nil {
		return &WebDAVResponse{
			StatusCode: 0,
			Error:      fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &WebDAVResponse{
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("failed to read response: %v", err),
		}, nil
	}

	// Collect response headers
	respHeaders := make(map[string]string)
	for key := range resp.Header {
		respHeaders[key] = resp.Header.Get(key)
	}

	return &WebDAVResponse{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       string(respBody),
	}, nil
}

// List lists files and directories at the given path using PROPFIND
func (w *WebDAVProxy) List(config *WebDAVConfig, path string, depth string) (*WebDAVResponse, error) {
	if depth == "" {
		depth = "1"
	}

	headers := map[string]string{
		"Depth":        depth,
		"Content-Type": "application/xml",
	}

	// PROPFIND request body for listing
	propfindBody := `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:displayname/>
    <D:resourcetype/>
    <D:getcontentlength/>
    <D:getlastmodified/>
    <D:getcontenttype/>
  </D:prop>
</D:propfind>`

	return w.ProxyRequest(config, "PROPFIND", path, strings.NewReader(propfindBody), headers)
}

// Get retrieves a file from the WebDAV server
func (w *WebDAVProxy) Get(config *WebDAVConfig, path string) (*WebDAVResponse, error) {
	return w.ProxyRequest(config, "GET", path, nil, nil)
}

// Put uploads a file to the WebDAV server
func (w *WebDAVProxy) Put(config *WebDAVConfig, path string, content string) (*WebDAVResponse, error) {
	return w.ProxyRequest(config, "PUT", path, strings.NewReader(content), nil)
}

// Delete removes a file or directory from the WebDAV server
func (w *WebDAVProxy) Delete(config *WebDAVConfig, path string) (*WebDAVResponse, error) {
	return w.ProxyRequest(config, "DELETE", path, nil, nil)
}

// Mkcol creates a new directory (collection) on the WebDAV server
func (w *WebDAVProxy) Mkcol(config *WebDAVConfig, path string) (*WebDAVResponse, error) {
	return w.ProxyRequest(config, "MKCOL", path, nil, nil)
}

// Move moves/renames a file or directory on the WebDAV server
func (w *WebDAVProxy) Move(config *WebDAVConfig, srcPath, destPath string) (*WebDAVResponse, error) {
	destURL, err := buildWebDAVURL(config.ServerURL, destPath)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Destination": destURL,
		"Overwrite":   "T",
	}

	return w.ProxyRequest(config, "MOVE", srcPath, nil, headers)
}

// Copy copies a file or directory on the WebDAV server
func (w *WebDAVProxy) Copy(config *WebDAVConfig, srcPath, destPath string) (*WebDAVResponse, error) {
	destURL, err := buildWebDAVURL(config.ServerURL, destPath)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Destination": destURL,
		"Overwrite":   "T",
	}

	return w.ProxyRequest(config, "COPY", srcPath, nil, headers)
}
