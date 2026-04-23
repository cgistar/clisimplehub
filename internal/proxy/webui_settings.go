package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clisimplehub/internal/config"
	"clisimplehub/internal/plugin"
	"clisimplehub/internal/storage"
)

const webUIBackupDir = "/clisimplehub"

var webUIWebDAVProxy = NewWebDAVProxy()

type webUIWebDAVConfig struct {
	ServerURL string `json:"serverUrl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

type webUIWebDAVRequest struct {
	Config   webUIWebDAVConfig `json:"config"`
	Path     string            `json:"path"`
	Depth    string            `json:"depth,omitempty"`
	Body     string            `json:"body,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	DestPath string            `json:"destPath,omitempty"`
}

type webUIBackupDataResponse struct {
	Filename string             `json:"filename"`
	Data     *config.BackupData `json:"data"`
}

type webUIBackupRestoreRequest struct {
	Data *config.BackupData `json:"data"`
	Mode string             `json:"mode"`
}

type webUIServerTestRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type webUIServerIndexRequest struct {
	Index int `json:"index"`
}

type webUIServerCurlResponse struct {
	Command string `json:"command"`
}

func (p *ProxyServer) handleWebUIGetWebDAVConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := p.loadWebUIWebDAVConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (p *ProxyServer) handleWebUISaveWebDAVConfig(w http.ResponseWriter, r *http.Request) {
	var req webUIWebDAVConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := p.saveWebUIWebDAVConfig(&req); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "webdav settings saved", "config": req})
}

func (p *ProxyServer) handleWebUITestWebDAVConnection(w http.ResponseWriter, r *http.Request) {
	var req webUIWebDAVConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	resp, err := webUIWebDAVProxy.List(toWebDAVProxyConfig(req), "/", "0")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p *ProxyServer) handleWebUIWebDAVList(w http.ResponseWriter, r *http.Request) {
	p.handleWebUIWebDAVRequest(w, r, func(req webUIWebDAVRequest) (*WebDAVResponse, error) {
		return webUIWebDAVProxy.List(toWebDAVProxyConfig(req.Config), req.Path, req.Depth)
	})
}

func (p *ProxyServer) handleWebUIWebDAVGet(w http.ResponseWriter, r *http.Request) {
	p.handleWebUIWebDAVRequest(w, r, func(req webUIWebDAVRequest) (*WebDAVResponse, error) {
		return webUIWebDAVProxy.Get(toWebDAVProxyConfig(req.Config), req.Path)
	})
}

func (p *ProxyServer) handleWebUIWebDAVPut(w http.ResponseWriter, r *http.Request) {
	p.handleWebUIWebDAVRequest(w, r, func(req webUIWebDAVRequest) (*WebDAVResponse, error) {
		return webUIWebDAVProxy.Put(toWebDAVProxyConfig(req.Config), req.Path, req.Body)
	})
}

func (p *ProxyServer) handleWebUIWebDAVDelete(w http.ResponseWriter, r *http.Request) {
	p.handleWebUIWebDAVRequest(w, r, func(req webUIWebDAVRequest) (*WebDAVResponse, error) {
		return webUIWebDAVProxy.Delete(toWebDAVProxyConfig(req.Config), req.Path)
	})
}

func (p *ProxyServer) handleWebUIWebDAVMkcol(w http.ResponseWriter, r *http.Request) {
	p.handleWebUIWebDAVRequest(w, r, func(req webUIWebDAVRequest) (*WebDAVResponse, error) {
		return webUIWebDAVProxy.Mkcol(toWebDAVProxyConfig(req.Config), req.Path)
	})
}

func (p *ProxyServer) handleWebUIWebDAVRequest(w http.ResponseWriter, r *http.Request, fn func(req webUIWebDAVRequest) (*WebDAVResponse, error)) {
	var req webUIWebDAVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	resp, err := fn(req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p *ProxyServer) handleWebUICreateBackupData(w http.ResponseWriter, r *http.Request) {
	backup, err := p.createWebUIBackupData()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, backup)
}

func (p *ProxyServer) handleWebUIRestoreBackupData(w http.ResponseWriter, r *http.Request) {
	var req webUIBackupRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := p.restoreWebUIBackupData(req.Data, req.Mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "backup restored"})
}

func (p *ProxyServer) handleWebUIGetServers(w http.ResponseWriter, r *http.Request) {
	servers, err := p.getWebUIServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

func (p *ProxyServer) handleWebUISaveServers(w http.ResponseWriter, r *http.Request) {
	var servers []config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&servers); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := p.saveWebUIServers(servers); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "servers saved", "servers": servers})
}

func (p *ProxyServer) handleWebUITestServerConnection(w http.ResponseWriter, r *http.Request) {
	var req webUIServerTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := testWebUIServerConnection(req.URL, req.APIKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "server connection ok"})
}

func (p *ProxyServer) handleWebUISyncConfigToServer(w http.ResponseWriter, r *http.Request) {
	var req webUIServerIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	if err := p.syncWebUIConfigToServer(req.Index); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "config synced"})
}

func (p *ProxyServer) handleWebUIBuildSyncCurl(w http.ResponseWriter, r *http.Request) {
	var req webUIServerIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	command, err := p.buildWebUISyncConfigCurl(req.Index)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, webUIServerCurlResponse{Command: command})
}

func toWebDAVProxyConfig(cfg webUIWebDAVConfig) *WebDAVConfig {
	return &WebDAVConfig{
		ServerURL: strings.TrimSpace(cfg.ServerURL),
		Username:  strings.TrimSpace(cfg.Username),
		Password:  cfg.Password,
	}
}

func (p *ProxyServer) loadWebUIWebDAVConfig() (*webUIWebDAVConfig, error) {
	if p.store == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	cfg := &webUIWebDAVConfig{}
	if serverURL, err := p.store.GetConfig("webdav.serverUrl"); err == nil {
		cfg.ServerURL = strings.TrimSpace(serverURL)
	}
	if username, err := p.store.GetConfig("webdav.username"); err == nil {
		cfg.Username = strings.TrimSpace(username)
	}
	if password, err := p.store.GetConfig("webdav.password"); err == nil {
		cfg.Password = password
	}
	return cfg, nil
}

func (p *ProxyServer) saveWebUIWebDAVConfig(cfg *webUIWebDAVConfig) error {
	if p.store == nil {
		return fmt.Errorf("storage not initialized")
	}
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if err := p.store.SetConfig("webdav.serverUrl", strings.TrimSpace(cfg.ServerURL)); err != nil {
		return err
	}
	if err := p.store.SetConfig("webdav.username", strings.TrimSpace(cfg.Username)); err != nil {
		return err
	}
	if err := p.store.SetConfig("webdav.password", cfg.Password); err != nil {
		return err
	}
	return nil
}

func (p *ProxyServer) createWebUIBackupData() (*webUIBackupDataResponse, error) {
	if p.store == nil {
		return nil, fmt.Errorf("storage not initialized")
	}
	settings, err := p.loadWebUISettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get settings: %w", err)
	}
	vendors, err := p.store.GetVendors()
	if err != nil {
		return nil, fmt.Errorf("failed to get vendors: %w", err)
	}
	endpoints, err := p.store.GetEndpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	appConfig := map[string]interface{}{
		"port":       settings.Port,
		"apiKey":     settings.APIKey,
		"fallback":   settings.Fallback,
		"debugMode":  settings.DebugMode,
		"listenAddr": settings.ListenAddr,
		"proxyUrl":   settings.ProxyURL,
		"clashPath":  settings.ClashPath,
	}

	vendorConfigs := make([]config.VendorConfig, len(vendors))
	for i, v := range vendors {
		vendorConfigs[i] = config.VendorConfig{
			ID:      v.ID,
			Name:    v.Name,
			HomeURL: v.HomeURL,
			APIURL:  v.APIURL,
			Remark:  v.Remark,
		}
	}

	endpointConfigs := make([]config.EndpointConfig, len(endpoints))
	for i, e := range endpoints {
		models := make([]config.ModelMapping, len(e.Models))
		for j, m := range e.Models {
			models[j] = config.ModelMapping{Name: m.Name, Alias: m.Alias}
		}
		endpointConfigs[i] = config.EndpointConfig{
			ID:             e.ID,
			Name:           e.Name,
			APIURL:         e.APIURL,
			APIKey:         e.APIKey,
			Active:         e.Active,
			Enabled:        e.Enabled,
			InterfaceType:  e.InterfaceType,
			Transformer:    e.Transformer,
			ProviderName:   e.ProviderName,
			Model:          e.Model,
			Remark:         e.Remark,
			Priority:       e.Priority,
			ProxyURL:       e.ProxyURL,
			Routes:         e.Routes,
			Models:         models,
			Headers:        e.Headers,
			ClaudeMessages: e.ClaudeMessages,
		}
	}

	backupData := &config.BackupData{
		SchemaVersion: 3,
		CreatedAt:     time.Now().Format(time.RFC3339),
		AppConfig:     appConfig,
		Vendors:       vendorConfigs,
		Endpoints:     endpointConfigs,
	}

	configPath := strings.TrimSpace(p.getConfigPath())
	if raw := exportKiroSyncPayload(configPath); raw != nil {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			backupData.KiroMultiConfig = v
		}
	}
	if raw := exportClashSyncPayload(configPath); raw != nil {
		backupData.ClashConfig = json.RawMessage(raw)
	}
	if raw, err := exportCodexSyncPayload(configPath); err != nil {
		return nil, fmt.Errorf("failed to export codex sync payload: %w", err)
	} else if len(raw) > 0 {
		backupData.CodexConfig = json.RawMessage(raw)
	}

	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "Unknown-Computer"
	}
	filename := fmt.Sprintf("%s-%s.json", hostname, time.Now().Format("2006-01-02T15-04-05"))
	return &webUIBackupDataResponse{Filename: filename, Data: backupData}, nil
}

func (p *ProxyServer) restoreWebUIBackupData(backupData *config.BackupData, mode string) error {
	if p.store == nil {
		return fmt.Errorf("storage not initialized")
	}
	if backupData == nil {
		return fmt.Errorf("backup data is nil")
	}
	if err := validateWebUIBackupData(backupData); err != nil {
		return fmt.Errorf("invalid backup data: %w", err)
	}

	fileStore, ok := p.store.(*storage.ConfigFileStore)
	if !ok {
		return fmt.Errorf("storage type does not support backup restore")
	}
	configPath := strings.TrimSpace(p.getConfigPath())
	if configPath == "" {
		return fmt.Errorf("config path not configured")
	}
	oldInfo, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("failed to stat current config: %w", err)
	}
	oldMode := oldInfo.Mode().Perm()
	oldConfigRaw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to backup current config: %w", err)
	}

	pluginSnapshots, err := snapshotWebUIPlugins(configPath)
	if err != nil {
		return err
	}
	rollback := func(syncErr error) error {
		_ = os.WriteFile(configPath, oldConfigRaw, oldMode)
		for i := len(pluginSnapshots) - 1; i >= 0; i-- {
			s := pluginSnapshots[i]
			if len(s.snapshot) == 0 {
				continue
			}
			_ = s.importer.SyncImport(configPath, s.snapshot)
		}
		p.triggerWebUIReload()
		return syncErr
	}

	replaceMode := strings.EqualFold(strings.TrimSpace(mode), string(config.BackupMergeModeReplace))
	finalCfg, err := p.buildBackupRestoreConfig(backupData, replaceMode)
	if err != nil {
		return err
	}
	if err := p.applyWebUIBackupAppConfig(backupData.AppConfig); err != nil {
		return rollback(fmt.Errorf("apply app settings: %w", err))
	}
	if err := fileStore.ReplaceFullConfig(finalCfg); err != nil {
		return rollback(fmt.Errorf("replace config: %w", err))
	}
	if err := p.restoreWebUIBackupPlugins(configPath, backupData, replaceMode); err != nil {
		return rollback(err)
	}
	p.triggerWebUIReload()
	return nil
}

func validateWebUIBackupData(data *config.BackupData) error {
	if data == nil {
		return fmt.Errorf("backup data is nil")
	}
	if data.SchemaVersion < 1 {
		return fmt.Errorf("invalid schema version: %d", data.SchemaVersion)
	}
	if data.AppConfig == nil {
		return fmt.Errorf("appConfig is missing")
	}
	for i := range data.Endpoints {
		if errs := config.ValidateEndpoint(&data.Endpoints[i]); len(errs) > 0 {
			return errs[0]
		}
	}
	return nil
}

func (p *ProxyServer) buildBackupRestoreConfig(backupData *config.BackupData, replaceMode bool) (*config.AppConfig, error) {
	if replaceMode {
		return &config.AppConfig{
			Vendors:   cloneVendorConfigs(backupData.Vendors),
			Endpoints: cloneEndpointConfigs(backupData.Endpoints),
		}, nil
	}

	cfg, err := config.NewConfigLoader(p.getConfigPath()).Load()
	if err != nil {
		return nil, fmt.Errorf("load current config: %w", err)
	}
	vendorMap := make(map[string]config.VendorConfig, len(cfg.Vendors)+len(backupData.Vendors))
	for _, v := range cfg.Vendors {
		vendorMap[strings.TrimSpace(v.Name)] = v
	}
	for _, v := range backupData.Vendors {
		vendorMap[strings.TrimSpace(v.Name)] = v
	}
	vendors := make([]config.VendorConfig, 0, len(vendorMap))
	for _, v := range vendorMap {
		vendors = append(vendors, v)
	}

	endpointMap := make(map[int64]config.EndpointConfig, len(cfg.Endpoints)+len(backupData.Endpoints))
	for _, ep := range cfg.Endpoints {
		if ep.ID != 0 {
			endpointMap[ep.ID] = ep
		}
	}
	for _, ep := range backupData.Endpoints {
		if ep.ID != 0 {
			endpointMap[ep.ID] = ep
		}
	}
	endpoints := make([]config.EndpointConfig, 0, len(endpointMap))
	for _, ep := range endpointMap {
		endpoints = append(endpoints, ep)
	}

	return &config.AppConfig{Vendors: vendors, Endpoints: endpoints}, nil
}

func cloneVendorConfigs(items []config.VendorConfig) []config.VendorConfig {
	if len(items) == 0 {
		return nil
	}
	out := make([]config.VendorConfig, len(items))
	copy(out, items)
	return out
}

func cloneEndpointConfigs(items []config.EndpointConfig) []config.EndpointConfig {
	if len(items) == 0 {
		return nil
	}
	out := make([]config.EndpointConfig, len(items))
	for i := range items {
		out[i] = items[i]
		if len(items[i].Routes) > 0 {
			out[i].Routes = append([]string(nil), items[i].Routes...)
		}
		if len(items[i].Models) > 0 {
			out[i].Models = append([]config.ModelMapping(nil), items[i].Models...)
		}
		if len(items[i].Headers) > 0 {
			headers := make(map[string]string, len(items[i].Headers))
			for k, v := range items[i].Headers {
				headers[k] = v
			}
			out[i].Headers = headers
		}
	}
	return out
}

func (p *ProxyServer) applyWebUIBackupAppConfig(appConfig map[string]interface{}) error {
	if p.store == nil || appConfig == nil {
		return nil
	}
	if port, ok := normalizeIntValue(appConfig["port"]); ok {
		if err := config.ValidatePort(port); err != nil {
			return err
		}
		if err := p.store.SetConfig("port", fmt.Sprintf("%d", port)); err != nil {
			return err
		}
		p.SetPort(port)
	}
	if apiKey, ok := appConfig["apiKey"].(string); ok {
		if err := p.store.SetConfig("apiKey", strings.TrimSpace(apiKey)); err != nil {
			return err
		}
		p.SetAuthKey(strings.TrimSpace(apiKey))
	}
	if fallback, ok := appConfig["fallback"].(bool); ok {
		if err := p.store.SetConfigBool("fallback", fallback); err != nil {
			return err
		}
		p.SetFallbackEnabled(fallback)
	}
	if debugMode, ok := appConfig["debugMode"].(string); ok {
		if err := p.store.SetConfig("debugMode", strings.TrimSpace(debugMode)); err != nil {
			return err
		}
	}
	if listenAddr, ok := appConfig["listenAddr"].(string); ok {
		listenAddr = strings.TrimSpace(listenAddr)
		if listenAddr == "" {
			listenAddr = "0.0.0.0"
		}
		if err := config.ValidateListenAddr(listenAddr); err != nil {
			return err
		}
		if err := p.store.SetConfig("listenAddr", listenAddr); err != nil {
			return err
		}
		p.SetListenAddr(listenAddr)
	}
	if proxyURL, ok := appConfig["proxyUrl"].(string); ok {
		if err := p.store.SetConfig("proxyUrl", strings.TrimSpace(proxyURL)); err != nil {
			return err
		}
	}
	if clashPath, ok := appConfig["clashPath"].(string); ok {
		if err := p.store.SetConfig("clashPath", strings.TrimSpace(clashPath)); err != nil {
			return err
		}
	}
	p.UpdateDebugFileLogger()
	return nil
}

func normalizeIntValue(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

type webUIPluginSnapshot struct {
	name     string
	importer plugin.ConfigSyncImporter
	snapshot json.RawMessage
}

func snapshotWebUIPlugins(configPath string) ([]webUIPluginSnapshot, error) {
	results := make([]webUIPluginSnapshot, 0, 3)
	for _, pl := range plugin.All() {
		importer, ok := pl.(plugin.ConfigSyncImporter)
		if !ok {
			continue
		}
		exporter, ok := pl.(plugin.ConfigSyncExporter)
		if !ok {
			continue
		}
		_, snapshot, err := exporter.SyncExport(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to snapshot plugin %s: %w", pl.Name(), err)
		}
		results = append(results, webUIPluginSnapshot{name: pl.Name(), importer: importer, snapshot: snapshot})
	}
	return results, nil
}

func (p *ProxyServer) restoreWebUIBackupPlugins(configPath string, backupData *config.BackupData, replaceMode bool) error {
	if backupData.KiroMultiConfig != nil {
		if err := restoreKiroBackupPayload(configPath, backupData.KiroMultiConfig, replaceMode); err != nil {
			return fmt.Errorf("restore kiro backup: %w", err)
		}
	}
	if backupData.ClashConfig != nil {
		if err := restoreClashBackupPayload(configPath, backupData.ClashConfig); err != nil {
			return fmt.Errorf("restore clash backup: %w", err)
		}
	}
	if backupData.CodexConfig != nil {
		if err := restoreCodexBackupPayload(configPath, backupData.CodexConfig, replaceMode); err != nil {
			return fmt.Errorf("restore codex backup: %w", err)
		}
	}
	return nil
}

func restoreKiroBackupPayload(configPath string, data interface{}, replaceMode bool) error {
	provider := plugin.GetKiroDesktopProviderCached()
	if provider == nil {
		return fmt.Errorf("kiro plugin not available")
	}
	payloadRaw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return err
	}
	payload["replaceMode"] = replaceMode
	finalRaw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	kiroPath := filepath.Join(filepath.Dir(configPath), filepath.Base(provider.DefaultMultiConfigBasename()))
	return provider.SaveMultiConfigFromBackup(kiroPath, finalRaw)
}

func restoreClashBackupPayload(configPath string, data interface{}) error {
	pl := plugin.ByName("clash")
	if pl == nil {
		return fmt.Errorf("clash plugin not available")
	}
	importer, ok := pl.(plugin.ConfigSyncImporter)
	if !ok {
		return fmt.Errorf("clash plugin does not support sync import")
	}
	payloadRaw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return importer.SyncImport(configPath, payloadRaw)
}

func restoreCodexBackupPayload(configPath string, data interface{}, replaceMode bool) error {
	pl := plugin.ByName("codex-accounts")
	if pl == nil {
		return fmt.Errorf("codex plugin not available")
	}
	importer, ok := pl.(plugin.ConfigSyncImporter)
	if !ok {
		return fmt.Errorf("codex plugin does not support sync import")
	}
	payloadRaw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal codex backup: %w", err)
	}
	if !replaceMode {
		payloadRaw, err = mergeCodexSyncPayloadWithLocal(configPath, payloadRaw)
		if err != nil {
			return err
		}
	}
	return importer.SyncImport(configPath, payloadRaw)
}

func mergeCodexSyncPayloadWithLocal(configPath string, remoteRaw json.RawMessage) (json.RawMessage, error) {
	var remotePayload codexSyncPayloadDTO
	if err := json.Unmarshal(remoteRaw, &remotePayload); err != nil {
		return nil, fmt.Errorf("invalid codex backup payload: %w", err)
	}
	normalizeCodexSyncPayload(&remotePayload)
	localRaw, err := exportCodexSyncPayload(configPath)
	if err != nil {
		return nil, fmt.Errorf("export local codex payload: %w", err)
	}
	if len(localRaw) == 0 {
		return json.Marshal(remotePayload)
	}
	var localPayload codexSyncPayloadDTO
	if err := json.Unmarshal(localRaw, &localPayload); err != nil {
		return nil, fmt.Errorf("invalid local codex payload: %w", err)
	}
	normalizeCodexSyncPayload(&localPayload)
	mergedAccounts, err := mergeCodexAccounts(localPayload.Accounts, remotePayload.Accounts)
	if err != nil {
		return nil, err
	}
	mergedMultiConfig := deepMergeJSONMaps(localPayload.MultiConfig, remotePayload.MultiConfig)
	activeAccountID := pickCodexActiveAccountID(mergedAccounts, remotePayload.MultiConfig, localPayload.MultiConfig)
	if activeAccountID == "" {
		delete(mergedMultiConfig, "activeAccountId")
	} else {
		mergedMultiConfig["activeAccountId"] = activeAccountID
	}
	return json.Marshal(codexSyncPayloadDTO{MultiConfig: mergedMultiConfig, Accounts: mergedAccounts})
}

type codexSyncPayloadDTO struct {
	MultiConfig map[string]interface{}   `json:"multiConfig"`
	Accounts    []map[string]interface{} `json:"accounts"`
}

func normalizeCodexSyncPayload(payload *codexSyncPayloadDTO) {
	if payload == nil {
		return
	}
	if payload.MultiConfig == nil {
		payload.MultiConfig = map[string]interface{}{}
	}
	if payload.Accounts == nil {
		payload.Accounts = []map[string]interface{}{}
	}
}

func mergeCodexAccounts(localAccounts, remoteAccounts []map[string]interface{}) ([]map[string]interface{}, error) {
	localByID := make(map[string]map[string]interface{}, len(localAccounts))
	for _, account := range localAccounts {
		accountID := getCodexAccountID(account)
		if accountID == "" {
			return nil, fmt.Errorf("local codex payload contains account without accountId")
		}
		localByID[accountID] = account
	}
	merged := make([]map[string]interface{}, 0, len(localAccounts)+len(remoteAccounts))
	mergedIndex := make(map[string]int, len(remoteAccounts))
	seen := make(map[string]struct{}, len(localAccounts)+len(remoteAccounts))
	for _, remoteAccount := range remoteAccounts {
		accountID := getCodexAccountID(remoteAccount)
		if accountID == "" {
			return nil, fmt.Errorf("backup codex payload contains account without accountId")
		}
		account := deepMergeJSONMaps(localByID[accountID], remoteAccount)
		account["accountId"] = accountID
		if idx, exists := mergedIndex[accountID]; exists {
			merged[idx] = account
		} else {
			mergedIndex[accountID] = len(merged)
			merged = append(merged, account)
		}
		seen[accountID] = struct{}{}
	}
	for _, localAccount := range localAccounts {
		accountID := getCodexAccountID(localAccount)
		if _, exists := seen[accountID]; exists {
			continue
		}
		account := deepMergeJSONMaps(nil, localAccount)
		account["accountId"] = accountID
		merged = append(merged, account)
	}
	return merged, nil
}

func pickCodexActiveAccountID(accounts []map[string]interface{}, remoteMultiConfig, localMultiConfig map[string]interface{}) string {
	validAccountIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountID := getCodexAccountID(account)
		if accountID != "" {
			validAccountIDs[accountID] = struct{}{}
		}
	}
	for _, candidate := range []string{getCodexActiveAccountID(remoteMultiConfig), getCodexActiveAccountID(localMultiConfig)} {
		if _, exists := validAccountIDs[candidate]; exists {
			return candidate
		}
	}
	if len(accounts) > 0 {
		return getCodexAccountID(accounts[0])
	}
	return ""
}

func getCodexAccountID(account map[string]interface{}) string {
	if account == nil {
		return ""
	}
	accountID, _ := account["accountId"].(string)
	return strings.TrimSpace(accountID)
}

func getCodexActiveAccountID(multiConfig map[string]interface{}) string {
	if multiConfig == nil {
		return ""
	}
	activeAccountID, _ := multiConfig["activeAccountId"].(string)
	return strings.TrimSpace(activeAccountID)
}

func deepMergeJSONMaps(base, override map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for key, val := range base {
		merged[key] = cloneJSONValue(val)
	}
	for key, val := range override {
		overrideMap, overrideIsMap := val.(map[string]interface{})
		baseMap, baseIsMap := merged[key].(map[string]interface{})
		if overrideIsMap && baseIsMap {
			merged[key] = deepMergeJSONMaps(baseMap, overrideMap)
			continue
		}
		merged[key] = cloneJSONValue(val)
	}
	return merged
}

func cloneJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return deepMergeJSONMaps(nil, v)
	case []interface{}:
		cloned := make([]interface{}, len(v))
		for i := range v {
			cloned[i] = cloneJSONValue(v[i])
		}
		return cloned
	default:
		return v
	}
}

func exportKiroSyncPayload(configPath string) json.RawMessage {
	pl := plugin.ByName("kiro")
	if pl == nil {
		return nil
	}
	exporter, ok := pl.(plugin.ConfigSyncExporter)
	if !ok {
		return nil
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var probe struct {
		Accounts []json.RawMessage `json:"accounts"`
	}
	if json.Unmarshal(data, &probe) != nil || len(probe.Accounts) == 0 {
		return nil
	}
	return data
}

func exportClashSyncPayload(configPath string) json.RawMessage {
	pl := plugin.ByName("clash")
	if pl == nil {
		return nil
	}
	exporter, ok := pl.(plugin.ConfigSyncExporter)
	if !ok {
		return nil
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var probe struct {
		Config struct {
			Subscriptions []json.RawMessage `json:"subscriptions"`
		} `json:"config"`
		Nodes []json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(data, &probe) != nil || (len(probe.Config.Subscriptions) == 0 && len(probe.Nodes) == 0) {
		return nil
	}
	return data
}

func exportCodexSyncPayload(configPath string) (json.RawMessage, error) {
	pl := plugin.ByName("codex-accounts")
	if pl == nil {
		return nil, nil
	}
	exporter, ok := pl.(plugin.ConfigSyncExporter)
	if !ok {
		return nil, nil
	}
	_, data, err := exporter.SyncExport(configPath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

type webUISyncRequestData struct {
	server      config.ServerConfig
	syncURL     string
	body        []byte
	verifyCodex bool
}

func normalizeRemoteServerBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must start with http:// or https://")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("server URL must include host")
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func encodeSyncPayload(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func shellSingleQuote(raw string) string {
	if raw == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(raw, "'", `'"'"'`) + "'"
}

func (p *ProxyServer) getWebUIServers() ([]config.ServerConfig, error) {
	fileStore, ok := p.store.(*storage.ConfigFileStore)
	if !ok {
		return nil, fmt.Errorf("storage type does not support servers")
	}
	return fileStore.GetServers()
}

func (p *ProxyServer) saveWebUIServers(servers []config.ServerConfig) error {
	fileStore, ok := p.store.(*storage.ConfigFileStore)
	if !ok {
		return fmt.Errorf("storage type does not support servers")
	}
	return fileStore.SaveServers(servers)
}

func testWebUIServerConnection(serverURL, apiKey string) error {
	base, err := normalizeRemoteServerBaseURL(serverURL)
	if err != nil {
		return err
	}
	healthURL := *base
	healthURL.Path += "/health"
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, healthURL.String(), nil)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server responded with status %d", resp.StatusCode)
	}
	return nil
}

func (p *ProxyServer) buildWebUISyncRequestData(index int) (*webUISyncRequestData, error) {
	servers, err := p.getWebUIServers()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(servers) {
		return nil, fmt.Errorf("invalid server index: %d", index)
	}
	server := servers[index]
	base, err := normalizeRemoteServerBaseURL(server.URL)
	if err != nil {
		return nil, err
	}
	syncURL := *base
	syncURL.Path += "/sync/config"
	cfg, err := config.NewConfigLoader(p.getConfigPath()).Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	type syncPayload struct {
		Vendors            []config.VendorConfig   `json:"vendors"`
		Endpoints          []config.EndpointConfig `json:"endpoints"`
		KiroConfigEncoded  string                  `json:"kiroConfigEncoded,omitempty"`
		ClashConfigEncoded string                  `json:"clashConfigEncoded,omitempty"`
		CodexConfigEncoded string                  `json:"codexConfigEncoded,omitempty"`
	}
	payload := syncPayload{
		Vendors:            cfg.Vendors,
		Endpoints:          cfg.Endpoints,
		KiroConfigEncoded:  encodeSyncPayload(exportKiroSyncPayload(p.getConfigPath())),
		ClashConfigEncoded: encodeSyncPayload(exportClashSyncPayload(p.getConfigPath())),
	}
	if raw, err := exportCodexSyncPayload(p.getConfigPath()); err != nil {
		return nil, fmt.Errorf("failed to export codex sync payload: %w", err)
	} else {
		payload.CodexConfigEncoded = encodeSyncPayload(raw)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize config: %w", err)
	}
	return &webUISyncRequestData{server: server, syncURL: syncURL.String(), body: body, verifyCodex: payload.CodexConfigEncoded != ""}, nil
}

func (p *ProxyServer) buildWebUISyncConfigCurl(index int) (string, error) {
	reqData, err := p.buildWebUISyncRequestData(index)
	if err != nil {
		return "", err
	}
	const payloadDelimiter = "__CLISIMPLEHUB_SYNC_PAYLOAD__"
	var builder strings.Builder
	fmt.Fprintf(&builder, "curl --request POST --url %s \\\n", shellSingleQuote(reqData.syncURL))
	fmt.Fprintf(&builder, "  --header %s", shellSingleQuote("Content-Type: application/json"))
	if strings.TrimSpace(reqData.server.APIKey) != "" {
		fmt.Fprintf(&builder, " \\\n  --header %s", shellSingleQuote("Authorization: Bearer "+reqData.server.APIKey))
	}
	fmt.Fprintf(&builder, " \\\n  --data-binary @- <<'%s'\n", payloadDelimiter)
	builder.Write(reqData.body)
	if len(reqData.body) == 0 || reqData.body[len(reqData.body)-1] != '\n' {
		builder.WriteByte('\n')
	}
	builder.WriteString(payloadDelimiter)
	return builder.String(), nil
}

func (p *ProxyServer) syncWebUIConfigToServer(index int) error {
	reqData, err := p.buildWebUISyncRequestData(index)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, reqData.syncURL, bytes.NewReader(reqData.body))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(reqData.server.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(reqData.server.APIKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("sync failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	if reqData.verifyCodex {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var syncResp struct {
			Codex struct {
				Synced  bool   `json:"synced"`
				Warning string `json:"warning,omitempty"`
			} `json:"codex"`
			Plugins map[string]struct {
				Synced  bool   `json:"synced"`
				Warning string `json:"warning,omitempty"`
			} `json:"plugins"`
		}
		if err := json.Unmarshal(respBody, &syncResp); err != nil {
			return fmt.Errorf("sync succeeded but failed to verify codex sync result: %w", err)
		}
		codexResult := syncResp.Codex
		if pluginResult, ok := syncResp.Plugins["codex-accounts"]; ok {
			codexResult = pluginResult
		}
		if !codexResult.Synced {
			if strings.TrimSpace(codexResult.Warning) != "" {
				return fmt.Errorf("remote codex sync failed: %s", codexResult.Warning)
			}
			return fmt.Errorf("remote codex sync failed")
		}
	}
	return nil
}

func (p *ProxyServer) triggerWebUIReload() {
	p.mu.RLock()
	reloadFunc := p.reloadFunc
	p.mu.RUnlock()
	if reloadFunc != nil {
		reloadFunc()
	}
}
