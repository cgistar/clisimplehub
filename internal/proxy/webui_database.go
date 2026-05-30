package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/dbconfig"
	"clisimplehub/internal/statsdb"
)

type webUIDatabaseTestRequest struct {
	DBDriver string `json:"dbDriver"`
	DBSource string `json:"dbSource"`
}

func (p *ProxyServer) handleWebUITestDatabase(w http.ResponseWriter, r *http.Request) {
	var req webUIDatabaseTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体格式无效"})
		return
	}
	cfg, err := p.resolveWebUIDatabaseConfig(req.DBSource)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := testWebUIDatabaseConfig(cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "database connection ok",
		"dbDriver": cfg.Driver,
		"dbSource": dbconfig.DisplaySource(cfg),
	})
}

func (p *ProxyServer) resolveWebUIDatabaseConfig(source string) (dbconfig.Config, error) {
	return dbconfig.ResolveSource(p.getConfigPath(), source)
}

func testWebUIDatabaseConfig(cfg dbconfig.Config) error {
	usageStore, err := statsdb.OpenUsageStatsStore(cfg)
	if err != nil {
		return fmt.Errorf("open usage stats store: %w", err)
	}
	defer usageStore.Close()

	accountStore, err := codexShared.OpenCodexAccountStoreWithConfig(cfg)
	if err != nil {
		return fmt.Errorf("open codex account store: %w", err)
	}
	defer accountStore.Close()
	return nil
}

func normalizeWebUIDatabaseDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", dbconfig.DriverSQLite:
		return dbconfig.DriverSQLite
	case dbconfig.DriverPGX, "postgres", "postgresql":
		return dbconfig.DriverPGX
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}
