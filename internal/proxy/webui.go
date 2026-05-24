package proxy

import (
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/statsdb"
	"clisimplehub/internal/storage"

	"github.com/go-chi/chi/v5"
)

const (
	webUISessionCookieName = "clisimplehub_web_session"
	webUISessionTTL        = 24 * time.Hour
)

//go:embed webui/dist
var webUIDist embed.FS

type webUISession struct {
	Token     string
	AuthKey   string
	ExpiresAt time.Time
}

type webUISettings struct {
	Port       int    `json:"port"`
	APIKey     string `json:"apiKey"`
	Fallback   bool   `json:"fallback"`
	DebugMode  string `json:"debugMode,omitempty"`
	ListenAddr string `json:"listenAddr,omitempty"`
	ProxyURL   string `json:"proxyUrl,omitempty"`
	ClashPath  string `json:"clashPath,omitempty"`
	ConfigPath string `json:"configPath,omitempty"`
}

type webUIPathPickerEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	IsFile     bool   `json:"isFile"`
	Executable bool   `json:"executable"`
}

type webUIPathPickerResponse struct {
	CurrentPath string                 `json:"currentPath"`
	ParentPath  string                 `json:"parentPath,omitempty"`
	HomePath    string                 `json:"homePath,omitempty"`
	Separator   string                 `json:"separator"`
	Roots       []string               `json:"roots,omitempty"`
	Entries     []webUIPathPickerEntry `json:"entries"`
}

type webUIEndpointInfo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	APIURL        string `json:"apiUrl"`
	APIKey        string `json:"apiKey,omitempty"`
	Active        bool   `json:"active"`
	Enabled       bool   `json:"enabled"`
	InterfaceType string `json:"interfaceType"`
	ProviderName  string `json:"providerName,omitempty"`
	Model         string `json:"model,omitempty"`
	Transformer   string `json:"transformer,omitempty"`
	Priority      int    `json:"priority,omitempty"`
	TodayRequests int64  `json:"todayRequests"`
	TodayErrors   int64  `json:"todayErrors"`
	TodayInput    int64  `json:"todayInput"`
	TodayOutput   int64  `json:"todayOutput"`
}

type webUIEndpointGroup struct {
	InterfaceType      string              `json:"interfaceType"`
	ActiveEndpointID   int64               `json:"activeEndpointId"`
	ActiveEndpointName string              `json:"activeEndpointName,omitempty"`
	Endpoints          []webUIEndpointInfo `json:"endpoints"`
}

type webUIEndpointImportInput struct {
	ID            int64                  `json:"id"`
	Name          string                 `json:"name"`
	APIURL        string                 `json:"apiUrl"`
	APIKey        string                 `json:"apiKey"`
	Active        *bool                  `json:"active,omitempty"`
	Enabled       *bool                  `json:"enabled,omitempty"`
	InterfaceType string                 `json:"interfaceType"`
	ProviderName  string                 `json:"providerName,omitempty"`
	Model         string                 `json:"model,omitempty"`
	Transformer   string                 `json:"transformer,omitempty"`
	ProxyURL      string                 `json:"proxyUrl,omitempty"`
	Routes        []string               `json:"routes,omitempty"`
	Models        []storage.ModelMapping `json:"models,omitempty"`
	Headers       map[string]string      `json:"headers,omitempty"`
	Remark        string                 `json:"remark,omitempty"`
	Priority      int                    `json:"priority,omitempty"`
}

func webUIFS() (fs.FS, error) {
	return fs.Sub(webUIDist, "webui/dist")
}

type webUIAssetResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
	wrote      bool
}

func (w *webUIAssetResponseWriter) WriteHeader(statusCode int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if statusCode >= http.StatusOK && statusCode < http.StatusBadRequest {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if w.gzipWriter != nil {
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *webUIAssetResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzipWriter != nil {
		return w.gzipWriter.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func withWebUIAssetCaching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetWriter := &webUIAssetResponseWriter{ResponseWriter: w}
		if !shouldGzipWebUIAsset(r) {
			next.ServeHTTP(assetWriter, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")

		gz := gzip.NewWriter(w)
		defer gz.Close()
		assetWriter.gzipWriter = gz
		next.ServeHTTP(assetWriter, r)
	})
}

func shouldGzipWebUIAsset(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet || r.Header.Get("Range") != "" {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}
	path := strings.ToLower(r.URL.Path)
	return strings.HasSuffix(path, ".js") ||
		strings.HasSuffix(path, ".css") ||
		strings.HasSuffix(path, ".html") ||
		strings.HasSuffix(path, ".svg")
}

func (p *ProxyServer) registerWebUIRoutes(r chi.Router) {
	uiFS, err := webUIFS()
	if err != nil {
		return
	}

	assetHandler := withWebUIAssetCaching(http.StripPrefix("/web/", http.FileServer(http.FS(uiFS))))

	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusNoContent)
	})
	r.Handle("/web/assets/*", assetHandler)
	r.Get("/web/api/auth/session", p.handleWebUISession)
	r.Post("/web/api/auth/login", p.handleWebUILogin)
	r.Post("/web/api/auth/logout", p.handleWebUILogout)
	r.Get("/web/sse", p.requireWebUISession(p.handleWebUISSE))
	r.Get("/web/api/home", p.requireWebUISession(p.handleWebUIHome))
	r.Get("/web/api/home/stats", p.requireWebUISession(p.handleWebUIStats))
	r.Delete("/web/api/home/stats", p.requireWebUISession(p.handleWebUIClearStats))
	r.Get("/web/api/home/stats/hourly", p.requireWebUISession(p.handleWebUIHourlyStats))
	r.Post("/web/api/home/endpoints/active", p.requireWebUISession(p.handleWebUIActiveEndpoint))
	r.Post("/web/api/home/endpoints/import", p.requireWebUISession(p.handleWebUIImportEndpoints))
	r.Delete("/web/api/home/endpoints/{endpointId}", p.requireWebUISession(p.handleWebUIDeleteEndpoint))
	r.Get("/web/api/codex", p.requireWebUISession(p.handleWebUICodex))
	r.Post("/web/api/codex/active", p.requireWebUISession(p.handleWebUIActiveCodexAccount))
	r.Post("/web/api/codex/refresh-token", p.requireWebUISession(p.handleWebUIRefreshCodexToken))
	r.Post("/web/api/codex/usage", p.requireWebUISession(p.handleWebUIFetchCodexUsage))
	r.Post("/web/api/codex/config", p.requireWebUISession(p.handleWebUISaveCodexConfig))
	r.Post("/web/api/codex/accounts", p.requireWebUISession(p.handleWebUIAddCodexAccount))
	r.Post("/web/api/codex/accounts/update", p.requireWebUISession(p.handleWebUIUpdateCodexAccount))
	r.Delete("/web/api/codex/accounts/{accountId}", p.requireWebUISession(p.handleWebUIDeleteCodexAccount))
	r.Get("/web/api/settings", p.requireWebUISession(p.handleWebUISettings))
	r.Post("/web/api/settings", p.requireWebUISession(p.handleWebUISaveSettings))
	r.Get("/web/api/settings/clash/path-picker", p.requireWebUISession(p.handleWebUIClashPathPicker))
	r.Get("/web/api/settings/webdav", p.requireWebUISession(p.handleWebUIGetWebDAVConfig))
	r.Post("/web/api/settings/webdav", p.requireWebUISession(p.handleWebUISaveWebDAVConfig))
	r.Post("/web/api/settings/webdav/test", p.requireWebUISession(p.handleWebUITestWebDAVConnection))
	r.Post("/web/api/settings/webdav/list", p.requireWebUISession(p.handleWebUIWebDAVList))
	r.Post("/web/api/settings/webdav/get", p.requireWebUISession(p.handleWebUIWebDAVGet))
	r.Post("/web/api/settings/webdav/put", p.requireWebUISession(p.handleWebUIWebDAVPut))
	r.Post("/web/api/settings/webdav/delete", p.requireWebUISession(p.handleWebUIWebDAVDelete))
	r.Post("/web/api/settings/webdav/mkcol", p.requireWebUISession(p.handleWebUIWebDAVMkcol))
	r.Post("/web/api/settings/backup/create", p.requireWebUISession(p.handleWebUICreateBackupData))
	r.Post("/web/api/settings/backup/restore", p.requireWebUISession(p.handleWebUIRestoreBackupData))
	r.Get("/web/api/settings/servers", p.requireWebUISession(p.handleWebUIGetServers))
	r.Post("/web/api/settings/servers", p.requireWebUISession(p.handleWebUISaveServers))
	r.Post("/web/api/settings/servers/test", p.requireWebUISession(p.handleWebUITestServerConnection))
	r.Post("/web/api/settings/servers/sync", p.requireWebUISession(p.handleWebUISyncConfigToServer))
	r.Post("/web/api/settings/servers/curl", p.requireWebUISession(p.handleWebUIBuildSyncCurl))
	r.Get("/web", p.handleWebUIApp)
	r.Get("/web/*", p.handleWebUIApp)
}

func (p *ProxyServer) handleWebUIApp(w http.ResponseWriter, r *http.Request) {
	uiFS, err := webUIFS()
	if err != nil {
		http.Error(w, "web ui not available", http.StatusNotFound)
		return
	}

	content, err := fs.ReadFile(uiFS, "index.html")
	if err != nil {
		http.Error(w, "web ui index not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (p *ProxyServer) handleWebUISSE(w http.ResponseWriter, r *http.Request) {
	if p.sseHub == nil {
		http.Error(w, "sse not available", http.StatusServiceUnavailable)
		return
	}
	p.sseHub.HandleSSE(w, r)
}

func (p *ProxyServer) requireWebUISession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := p.currentWebUISession(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "Web UI 未登录或会话已过期",
			})
			return
		}
		next(w, r)
	}
}

func (p *ProxyServer) currentWebUISession(r *http.Request) (*webUISession, bool) {
	if r == nil {
		return nil, false
	}
	cookie, err := r.Cookie(webUISessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, false
	}

	token := strings.TrimSpace(cookie.Value)
	now := time.Now()
	currentAuthKey := strings.TrimSpace(p.getWebUILoginKey())

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.webUISessions == nil {
		p.webUISessions = make(map[string]webUISession)
	}
	for key, session := range p.webUISessions {
		if session.ExpiresAt.Before(now) {
			delete(p.webUISessions, key)
		}
	}

	session, ok := p.webUISessions[token]
	if !ok {
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(session.AuthKey), []byte(currentAuthKey)) != 1 {
		delete(p.webUISessions, token)
		return nil, false
	}
	if session.ExpiresAt.Before(now) {
		delete(p.webUISessions, token)
		return nil, false
	}
	copySession := session
	return &copySession, true
}

func (p *ProxyServer) createWebUISession(authKey string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.webUISessions == nil {
		p.webUISessions = make(map[string]webUISession)
	}
	p.webUISessions[token] = webUISession{
		Token:     token,
		AuthKey:   authKey,
		ExpiresAt: time.Now().Add(webUISessionTTL),
	}
	return token, nil
}

func (p *ProxyServer) destroyWebUISession(r *http.Request) {
	if r == nil {
		return
	}
	cookie, err := r.Cookie(webUISessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.webUISessions, strings.TrimSpace(cookie.Value))
}

func writeExpiredWebUICookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
	})
}

func (p *ProxyServer) handleWebUISession(w http.ResponseWriter, r *http.Request) {
	hasAPIKey := strings.TrimSpace(p.getWebUILoginKey()) != ""
	_, authenticated := p.currentWebUISession(r)
	if !authenticated {
		writeExpiredWebUICookie(w)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": authenticated,
		"hasApiKey":     hasAPIKey,
	})
}

func (p *ProxyServer) handleWebUILogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}

	requiredKey := strings.TrimSpace(p.getWebUILoginKey())
	providedKey := strings.TrimSpace(req.APIKey)
	if requiredKey != "" && subtle.ConstantTimeCompare([]byte(providedKey), []byte(requiredKey)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "API Key 错误",
		})
		return
	}

	token, err := p.createWebUISession(requiredKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "创建 Web 会话失败",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(webUISessionTTL),
		MaxAge:   int(webUISessionTTL.Seconds()),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "login success",
	})
}

func (p *ProxyServer) handleWebUILogout(w http.ResponseWriter, r *http.Request) {
	p.destroyWebUISession(r)
	writeExpiredWebUICookie(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "logout success",
	})
}

func (p *ProxyServer) handleWebUIHome(w http.ResponseWriter, r *http.Request) {
	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "storage not initialized",
		})
		return
	}

	endpoints, err := p.store.GetEndpoints()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	groupMap := make(map[string][]webUIEndpointInfo)
	activeNameMap := make(map[string]string)
	activeIDMap := make(map[string]int64)
	var todayStats map[string]*statsdb.EndpointDailyStats
	if p.usageStats != nil {
		todayStats, _ = p.usageStats.GetTodayStatsByEndpoints(r.Context())
	}
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		info := webUIEndpointInfo{
			ID:            ep.ID,
			Name:          ep.Name,
			APIURL:        ep.APIURL,
			APIKey:        ep.APIKey,
			Active:        false,
			Enabled:       ep.Enabled,
			InterfaceType: ep.InterfaceType,
			ProviderName:  ep.ProviderName,
			Model:         ep.Model,
			Transformer:   ep.Transformer,
			Priority:      ep.Priority,
		}
		if todayStats != nil {
			if stat, ok := todayStats[strconv.FormatInt(ep.ID, 10)]; ok && stat != nil {
				info.TodayRequests = stat.RequestCount
				info.TodayErrors = stat.ErrorCount
				info.TodayInput = stat.InputTokens
				info.TodayOutput = stat.OutputTokens
			}
		}
		groupMap[ep.InterfaceType] = append(groupMap[ep.InterfaceType], info)
	}

	interfaceTypes := make([]string, 0, len(groupMap))
	for interfaceType := range groupMap {
		interfaceTypes = append(interfaceTypes, interfaceType)
		if p.router != nil {
			activeEndpoint := p.router.GetActiveEndpoint(InterfaceType(interfaceType))
			if activeEndpoint != nil {
				activeNameMap[interfaceType] = activeEndpoint.Name
				activeIDMap[interfaceType] = activeEndpoint.ID
			}
		}
	}
	sort.Strings(interfaceTypes)

	grouped := make([]webUIEndpointGroup, 0, len(interfaceTypes))
	enabledCount := 0
	for _, interfaceType := range interfaceTypes {
		eps := groupMap[interfaceType]
		sort.Slice(eps, func(i, j int) bool {
			if eps[i].Priority != eps[j].Priority {
				return eps[i].Priority < eps[j].Priority
			}
			return eps[i].Name < eps[j].Name
		})
		for i := range eps {
			if eps[i].Enabled {
				enabledCount++
			}
			if eps[i].ID == activeIDMap[interfaceType] {
				eps[i].Active = true
			}
		}
		grouped = append(grouped, webUIEndpointGroup{
			InterfaceType:      interfaceType,
			ActiveEndpointID:   activeIDMap[interfaceType],
			ActiveEndpointName: activeNameMap[interfaceType],
			Endpoints:          eps,
		})
	}

	recentLogs := make([]*RequestLog, 0)
	inProgressLogs := make([]*RequestLog, 0)
	if p.stats != nil {
		recentLogs = p.stats.GetRecentLogs(10)
		inProgressLogs = p.stats.GetInProgressLogs()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"configPath": p.getConfigPath(),
		"serverStatus": map[string]any{
			"running":    p.IsRunning(),
			"port":       p.GetPort(),
			"listenAddr": p.GetListenAddr(),
		},
		"summary": map[string]any{
			"endpointCount":        len(endpoints),
			"enabledEndpointCount": enabledCount,
			"interfaceTypeCount":   len(interfaceTypes),
			"recentLogCount":       len(recentLogs),
		},
		"groupedEndpoints": grouped,
		"recentLogs":       recentLogs,
		"inProgressLogs":   inProgressLogs,
	})
}

func parseWebUIStatsRange(raw string) (statsdb.TimeRange, bool) {
	switch statsdb.TimeRange(strings.TrimSpace(raw)) {
	case statsdb.TimeRangeToday:
		return statsdb.TimeRangeToday, true
	case statsdb.TimeRangeYesterday:
		return statsdb.TimeRangeYesterday, true
	case statsdb.TimeRangeWeek:
		return statsdb.TimeRangeWeek, true
	case statsdb.TimeRangeMonth:
		return statsdb.TimeRangeMonth, true
	case statsdb.TimeRangeAll:
		return statsdb.TimeRangeAll, true
	default:
		return statsdb.TimeRangeToday, false
	}
}

func (p *ProxyServer) handleWebUIStats(w http.ResponseWriter, r *http.Request) {
	if p.usageStats == nil {
		writeJSON(w, http.StatusOK, []statsdb.InterfaceTypeStatsSummary{})
		return
	}

	timeRange, ok := parseWebUIStatsRange(r.URL.Query().Get("range"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "range 无效",
		})
		return
	}

	stats, err := p.usageStats.GetStatsByInterfaceType(r.Context(), timeRange)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (p *ProxyServer) handleWebUIClearStats(w http.ResponseWriter, r *http.Request) {
	if p.usageStats == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "usage stats store not initialized",
		})
		return
	}

	timeRange, ok := parseWebUIStatsRange(r.URL.Query().Get("range"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "range 无效",
		})
		return
	}

	if err := p.usageStats.ClearStats(r.Context(), timeRange); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "token stats cleared",
	})
}

func (p *ProxyServer) handleWebUIHourlyStats(w http.ResponseWriter, r *http.Request) {
	if p.usageStats == nil {
		writeJSON(w, http.StatusOK, []statsdb.HourlyStatsSummary{})
		return
	}

	stats, err := p.usageStats.GetTodayHourlyStats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (p *ProxyServer) handleWebUIActiveEndpoint(w http.ResponseWriter, r *http.Request) {
	if p.store == nil || p.router == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "server state not initialized",
		})
		return
	}

	var req struct {
		InterfaceType string `json:"interfaceType"`
		EndpointID    int64  `json:"endpointId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}
	if strings.TrimSpace(req.InterfaceType) == "" || req.EndpointID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "interfaceType 和 endpointId 必填",
		})
		return
	}

	endpoints, err := p.store.GetEndpointsByType(req.InterfaceType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var target *storage.Endpoint
	for _, ep := range endpoints {
		if ep != nil && ep.ID == req.EndpointID {
			target = ep
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "endpoint not found",
		})
		return
	}
	if !target.Enabled {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "不能将禁用端点设为活跃",
		})
		return
	}

	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		shouldBeActive := ep.ID == req.EndpointID
		if ep.Active == shouldBeActive {
			continue
		}
		ep.Active = shouldBeActive
		if err := p.store.UpdateEndpoint(ep); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
			return
		}
	}

	allEndpoints, err := p.store.GetEndpoints()
	if err == nil {
		p.router.LoadEndpoints(convertStorageEndpoints(allEndpoints))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "active endpoint updated",
	})
}

func (p *ProxyServer) reloadWebUIRouterFromStore() error {
	if p.store == nil || p.router == nil {
		return nil
	}
	endpoints, err := p.store.GetEndpoints()
	if err != nil {
		return err
	}
	p.router.LoadEndpoints(convertStorageEndpoints(endpoints))
	return nil
}

func webUIHasEnabledActiveEndpoint(endpoints []*storage.Endpoint, interfaceType string, excludeID int64) bool {
	for _, ep := range endpoints {
		if ep == nil || ep.ID == excludeID {
			continue
		}
		if ep.InterfaceType == interfaceType && ep.Enabled && ep.Active {
			return true
		}
	}
	return false
}

func parseWebUIEndpointImportItems(r *http.Request) ([]webUIEndpointImportInput, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("请求体格式无效: %w", err)
	}

	var items []webUIEndpointImportInput
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}

	var wrapped struct {
		Endpoints []webUIEndpointImportInput `json:"endpoints"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Endpoints == nil {
		return nil, fmt.Errorf("请提供 endpoint JSON 数组")
	}
	return wrapped.Endpoints, nil
}

func (p *ProxyServer) endpointFromWebUIImportInput(input webUIEndpointImportInput) (*storage.Endpoint, error) {
	name := strings.TrimSpace(input.Name)
	apiURL := strings.TrimSpace(input.APIURL)
	apiKey := strings.TrimSpace(input.APIKey)
	interfaceType := strings.TrimSpace(input.InterfaceType)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if apiURL == "" {
		return nil, fmt.Errorf("apiUrl is required")
	}
	if interfaceType == "" {
		return nil, fmt.Errorf("interfaceType is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	active := false
	if input.Active != nil {
		active = *input.Active
	}

	priority := input.Priority
	if priority == 0 {
		priority = 5
	}

	endpoint := &storage.Endpoint{
		Name:          name,
		APIURL:        apiURL,
		APIKey:        apiKey,
		Active:        active,
		Enabled:       enabled,
		InterfaceType: interfaceType,
		ProviderName:  strings.TrimSpace(input.ProviderName),
		Model:         strings.TrimSpace(input.Model),
		Transformer:   strings.TrimSpace(input.Transformer),
		ProxyURL:      strings.TrimSpace(input.ProxyURL),
		Routes:        input.Routes,
		Models:        input.Models,
		Headers:       input.Headers,
		Remark:        strings.TrimSpace(input.Remark),
		Priority:      priority,
	}
	return endpoint, nil
}

func (p *ProxyServer) activateImportedWebUIEndpoint(endpoint *storage.Endpoint, endpoints []*storage.Endpoint) error {
	if endpoint == nil {
		return nil
	}
	oldActiveEndpoints := make([]*storage.Endpoint, 0)
	for _, ep := range endpoints {
		if ep == nil || ep.ID == endpoint.ID || ep.InterfaceType != endpoint.InterfaceType || !ep.Active {
			continue
		}
		oldActiveEndpoints = append(oldActiveEndpoints, ep)
		ep.Active = false
		if err := p.store.UpdateEndpoint(ep); err != nil {
			restoreWebUIActiveEndpoints(p.store, oldActiveEndpoints)
			return err
		}
	}
	endpoint.Active = true
	if err := p.store.UpdateEndpoint(endpoint); err != nil {
		restoreWebUIActiveEndpoints(p.store, oldActiveEndpoints)
		return err
	}
	return nil
}

func restoreWebUIActiveEndpoints(store storage.Storage, endpoints []*storage.Endpoint) {
	if store == nil {
		return
	}
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		ep.Active = true
		_ = store.UpdateEndpoint(ep)
	}
}

func (p *ProxyServer) handleWebUIImportEndpoints(w http.ResponseWriter, r *http.Request) {
	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "storage not initialized",
		})
		return
	}

	items, err := parseWebUIEndpointImportItems(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "没有可导入的 endpoint",
		})
		return
	}

	successCount := 0
	failed := make([]map[string]any, 0)
	for idx, item := range items {
		endpoint, err := p.endpointFromWebUIImportInput(item)
		if err != nil {
			failed = append(failed, map[string]any{"index": idx, "error": err.Error()})
			continue
		}

		endpoints, err := p.store.GetEndpoints()
		if err != nil {
			failed = append(failed, map[string]any{"index": idx, "error": err.Error()})
			continue
		}

		shouldActivate := endpoint.Active
		endpoint.Active = false
		if !shouldActivate && endpoint.Enabled && !webUIHasEnabledActiveEndpoint(endpoints, endpoint.InterfaceType, 0) {
			endpoint.Active = true
		}

		if err := p.store.SaveEndpoint(endpoint); err != nil {
			failed = append(failed, map[string]any{"index": idx, "error": err.Error()})
			continue
		}

		if shouldActivate {
			if err := p.activateImportedWebUIEndpoint(endpoint, endpoints); err != nil {
				failed = append(failed, map[string]any{"index": idx, "error": err.Error()})
				continue
			}
		}
		successCount++
	}

	if err := p.reloadWebUIRouterFromStore(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "reload endpoints failed: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("端点导入完成：成功 %d，失败 %d", successCount, len(failed)),
		"success": successCount,
		"failed":  failed,
	})
}

func (p *ProxyServer) handleWebUIDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "storage not initialized",
		})
		return
	}

	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "endpointId")), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "endpointId 无效",
		})
		return
	}
	if _, err := p.store.GetEndpointByID(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "endpoint not found",
		})
		return
	}

	if err := p.store.DeleteEndpoint(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	if p.usageStats != nil {
		if err := p.usageStats.DeleteStatsByEndpointID(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
			return
		}
	}

	if err := p.reloadWebUIRouterFromStore(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "reload endpoints failed: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "端点已删除",
	})
}

func (p *ProxyServer) handleWebUICodex(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"message":   "codex-accounts 插件不可用",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	accountsRaw, err := provider.GetAccounts(codexPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	globalConfigRaw, err := provider.GetCodexGlobalConfig(codexPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var accountsPayload map[string]any
	if err := json.Unmarshal(accountsRaw, &accountsPayload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to parse codex accounts payload",
		})
		return
	}
	var globalConfigPayload map[string]any
	if err := json.Unmarshal(globalConfigRaw, &globalConfigPayload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to parse codex config payload",
		})
		return
	}

	response := map[string]any{
		"available":    true,
		"configPath":   codexPath,
		"globalConfig": globalConfigPayload,
	}
	for key, value := range accountsPayload {
		response[key] = value
	}

	writeJSON(w, http.StatusOK, response)
}

func (p *ProxyServer) handleWebUIActiveCodexAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return
	}

	var req struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "accountId 必填",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	if err := provider.SetActiveAccount(codexPath, accountID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "active codex account updated",
	})
}

func (p *ProxyServer) handleWebUIRefreshCodexToken(w http.ResponseWriter, r *http.Request) {
	provider, accountID, codexPath, ok := p.parseWebUICodexAccountAction(w, r)
	if !ok {
		return
	}

	result, err := provider.TestAccount(codexPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to parse refresh token result",
		})
		return
	}
	payload["message"] = "refresh token updated"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIFetchCodexUsage(w http.ResponseWriter, r *http.Request) {
	provider, accountID, codexPath, ok := p.parseWebUICodexAccountAction(w, r)
	if !ok {
		return
	}

	result, err := provider.GetAccountUsage(r.Context(), codexPath, accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to parse usage payload",
		})
		return
	}
	payload["message"] = "usage loaded"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUISaveCodexConfig(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}

	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "配置数据格式无效",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	if err := provider.SaveCodexGlobalConfig(codexPath, dto); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "codex config saved",
	})
}

func (p *ProxyServer) handleWebUIAddCodexAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}

	refreshToken := strings.TrimSpace(fmt.Sprint(req["refreshToken"]))
	if refreshToken == "<nil>" {
		refreshToken = ""
	}
	accessToken := strings.TrimSpace(fmt.Sprint(req["accessToken"]))
	if accessToken == "<nil>" {
		accessToken = ""
	}
	if refreshToken == "" && accessToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "refreshToken 或 accessToken 至少需要一个",
		})
		return
	}

	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "账号数据格式无效",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	raw, err := provider.AddAccount(codexPath, dto)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to parse codex account payload",
		})
		return
	}
	payload["message"] = "codex account added"
	writeJSON(w, http.StatusOK, payload)
}

func (p *ProxyServer) handleWebUIDeleteCodexAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return
	}

	accountID := strings.TrimSpace(chi.URLParam(r, "accountId"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "accountId 必填",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	if err := provider.DeleteAccount(codexPath, accountID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "codex account deleted",
	})
}

func (p *ProxyServer) handleWebUIUpdateCodexAccount(w http.ResponseWriter, r *http.Request) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}

	accountID := strings.TrimSpace(fmt.Sprint(req["id"]))
	if accountID == "" || accountID == "<nil>" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "id 必填",
		})
		return
	}

	dto, err := json.Marshal(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "账号数据格式无效",
		})
		return
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	if err := provider.UpdateAccount(codexPath, dto); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "codex account updated",
	})
}

func (p *ProxyServer) handleWebUISettings(w http.ResponseWriter, r *http.Request) {
	settings, err := p.loadWebUISettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (p *ProxyServer) handleWebUISaveSettings(w http.ResponseWriter, r *http.Request) {
	if p.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "storage not initialized",
		})
		return
	}

	currentSettings, err := p.loadWebUISettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var req webUISettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return
	}
	if err := config.ValidatePort(req.Port); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid port: " + err.Error(),
		})
		return
	}
	listenAddr := strings.TrimSpace(req.ListenAddr)
	if listenAddr == "" {
		listenAddr = "0.0.0.0"
	}
	if err := config.ValidateListenAddr(listenAddr); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid listen address: " + err.Error(),
		})
		return
	}

	newAPIKey := strings.TrimSpace(req.APIKey)
	debugMode := strings.TrimSpace(req.DebugMode)
	proxyURL := strings.TrimSpace(req.ProxyURL)
	clashPath := strings.TrimSpace(req.ClashPath)

	if err := p.store.SetConfig("port", strconv.Itoa(req.Port)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfig("apiKey", newAPIKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfigBool("fallback", req.Fallback); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfig("debugMode", debugMode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfig("listenAddr", listenAddr); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfig("proxyUrl", proxyURL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := p.store.SetConfig("clashPath", clashPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	oldAPIKey := strings.TrimSpace(p.getAuthKey())
	effectiveAPIKey := p.resolvePreferredAuthKey(newAPIKey)
	p.SetAuthKey(effectiveAPIKey)
	p.SetFallbackEnabled(req.Fallback)
	p.UpdateDebugFileLogger()

	updatedSettings, err := p.loadWebUISettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	restartRequired := currentSettings.Port != req.Port || strings.TrimSpace(currentSettings.ListenAddr) != listenAddr
	reloginRequired := subtle.ConstantTimeCompare([]byte(oldAPIKey), []byte(effectiveAPIKey)) != 1
	if reloginRequired {
		p.destroyWebUISession(r)
		writeExpiredWebUICookie(w)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":         "settings saved",
		"settings":        updatedSettings,
		"restartRequired": restartRequired,
		"reloginRequired": reloginRequired,
	})
}

func (p *ProxyServer) loadWebUISettings() (*webUISettings, error) {
	if p.store == nil {
		return nil, http.ErrServerClosed
	}

	settings := &webUISettings{
		Port:       5600,
		APIKey:     "",
		Fallback:   false,
		DebugMode:  "",
		ListenAddr: "0.0.0.0",
		ProxyURL:   "",
		ClashPath:  "",
		ConfigPath: p.getConfigPath(),
	}

	if portStr, err := p.store.GetConfig("port"); err == nil && strings.TrimSpace(portStr) != "" {
		if port, parseErr := strconv.Atoi(portStr); parseErr == nil {
			settings.Port = port
		}
	}
	if apiKey, err := p.store.GetConfig("apiKey"); err == nil {
		settings.APIKey = strings.TrimSpace(apiKey)
	}
	if fallbackStr, err := p.store.GetConfig("fallback"); err == nil && strings.EqualFold(strings.TrimSpace(fallbackStr), "true") {
		settings.Fallback = true
	}
	if debugMode, err := p.store.GetConfig("debugMode"); err == nil {
		settings.DebugMode = strings.TrimSpace(debugMode)
	}
	if listenAddr, err := p.store.GetConfig("listenAddr"); err == nil && strings.TrimSpace(listenAddr) != "" {
		settings.ListenAddr = strings.TrimSpace(listenAddr)
	}
	if proxyURL, err := p.store.GetConfig("proxyUrl"); err == nil {
		settings.ProxyURL = strings.TrimSpace(proxyURL)
	}
	if clashPath, err := p.store.GetConfig("clashPath"); err == nil {
		settings.ClashPath = strings.TrimSpace(clashPath)
	}
	return settings, nil
}

func (p *ProxyServer) handleWebUIClashPathPicker(w http.ResponseWriter, r *http.Request) {
	currentPath, err := p.resolveWebUIPathPickerDir(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	items, err := os.ReadDir(currentPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	entries := make([]webUIPathPickerEntry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(currentPath, item.Name())
		entries = append(entries, webUIPathPickerEntry{
			Name:       item.Name(),
			Path:       fullPath,
			IsDir:      info.IsDir(),
			IsFile:     info.Mode().IsRegular(),
			Executable: isWebUIExecutableFile(fullPath, info),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	parentPath := ""
	if parent := filepath.Dir(currentPath); parent != currentPath {
		parentPath = parent
	}
	homePath, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, webUIPathPickerResponse{
		CurrentPath: currentPath,
		ParentPath:  parentPath,
		HomePath:    strings.TrimSpace(homePath),
		Separator:   string(os.PathSeparator),
		Roots:       webUIPathRoots(),
		Entries:     entries,
	})
}

func (p *ProxyServer) resolveWebUIPathPickerDir(rawPath string) (string, error) {
	target := strings.TrimSpace(rawPath)
	if target == "" && p != nil && p.store != nil {
		if clashPath, err := p.store.GetConfig("clashPath"); err == nil {
			target = strings.TrimSpace(clashPath)
		}
	}
	if target == "" {
		if homePath, err := os.UserHomeDir(); err == nil {
			target = homePath
		}
	}
	if target == "" && p != nil {
		target = filepath.Dir(strings.TrimSpace(p.getConfigPath()))
	}
	if target == "" {
		target = "."
	}

	absPath, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		parent := filepath.Dir(absPath)
		if parentInfo, parentErr := os.Stat(parent); parentErr == nil && parentInfo.IsDir() {
			return filepath.Clean(parent), nil
		}
		return "", err
	}
	if !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}
	return filepath.Clean(absPath), nil
}

func isWebUIExecutableFile(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".bat", ".cmd", ".ps1", ".com":
			return true
		default:
			return false
		}
	}
	return info.Mode().Perm()&0111 != 0
}

func webUIPathRoots() []string {
	if runtime.GOOS != "windows" {
		return []string{string(os.PathSeparator)}
	}
	roots := make([]string, 0, 4)
	for ch := 'A'; ch <= 'Z'; ch++ {
		root := fmt.Sprintf("%c:%c", ch, os.PathSeparator)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	return roots
}

func (p *ProxyServer) resolvePreferredAuthKey(configKey string) string {
	if envKey := strings.TrimSpace(os.Getenv(config.EnvAPIKey)); envKey != "" {
		return envKey
	}
	return strings.TrimSpace(configKey)
}

func (p *ProxyServer) getWebUILoginKey() string {
	if p == nil || p.store == nil {
		return ""
	}
	key, err := p.store.GetConfig("apiKey")
	if err != nil {
		return p.resolvePreferredAuthKey("")
	}
	return p.resolvePreferredAuthKey(key)
}

func resolveWebUICodexConfigPath(provider plugin.CodexDesktopProvider, configPath string) string {
	if provider == nil {
		return ""
	}
	defaultPath := provider.DefaultMultiConfigBasename()
	if strings.TrimSpace(configPath) == "" {
		return defaultPath
	}
	return filepath.Join(filepath.Dir(configPath), filepath.Base(defaultPath))
}

func (p *ProxyServer) parseWebUICodexAccountAction(w http.ResponseWriter, r *http.Request) (plugin.CodexDesktopProvider, string, string, bool) {
	provider := plugin.GetCodexDesktopProviderCached()
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "codex-accounts 插件不可用",
		})
		return nil, "", "", false
	}

	var req struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "请求体格式无效",
		})
		return nil, "", "", false
	}

	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "accountId 必填",
		})
		return nil, "", "", false
	}

	codexPath := resolveWebUICodexConfigPath(provider, p.getConfigPath())
	return provider, accountID, codexPath, true
}

func convertStorageEndpoints(endpoints []*storage.Endpoint) []*Endpoint {
	result := make([]*Endpoint, len(endpoints))
	for i, e := range endpoints {
		if e == nil {
			continue
		}
		var models []ModelMapping
		if len(e.Models) > 0 {
			models = make([]ModelMapping, 0, len(e.Models))
			for _, m := range e.Models {
				models = append(models, ModelMapping{Name: m.Name, Alias: m.Alias})
			}
		}
		result[i] = &Endpoint{
			ID:            e.ID,
			Name:          e.Name,
			APIURL:        e.APIURL,
			APIKey:        e.APIKey,
			Active:        e.Active,
			Enabled:       e.Enabled,
			InterfaceType: e.InterfaceType,
			Transformer:   e.Transformer,
			ProviderName:  e.ProviderName,
			Model:         e.Model,
			Remark:        e.Remark,
			Priority:      e.Priority,
			ProxyURL:      e.ProxyURL,
			Routes:        e.Routes,
			Models:        models,
			Headers:       e.Headers,
		}
	}
	return result
}
