package entclawplugin

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	entclawruntime "clisimplehub/internal/entclaw/runtime"
	"clisimplehub/internal/plugin"
)

func init() {
	plugin.Register(&EntclawPlugin{})
}

type EntclawPlugin struct {
	sessions     entclawruntime.SessionStore
	skills       entclawruntime.SkillStore
	tools        *entclawruntime.ToolRuntime
	client       entclawruntime.LoopbackClient
	orchestrator *entclawruntime.Orchestrator
}

func (p *EntclawPlugin) Name() string { return "entclaw" }

func (p *EntclawPlugin) Init(cfg plugin.InitConfig) error {
	configPath := strings.TrimSpace(cfg.ConfigPath)
	if configPath == "" {
		return fmt.Errorf("entclaw config path is required")
	}

	dataDir := filepath.Dir(configPath)
	sessions := entclawruntime.NewSessionStore(dataDir)
	skills := entclawruntime.NewSkillStore(dataDir)
	mcp := entclawruntime.NewMCPStore(dataDir)
	mcpCaller := entclawruntime.NewStdioMCPCaller(dataDir)
	tools := entclawruntime.NewToolRuntime(dataDir, sessions, skills, mcp, mcpCaller, nil)
	client := entclawruntime.HTTPClientLoopback{
		Client: http.DefaultClient,
	}

	p.sessions = sessions
	p.skills = skills
	p.tools = tools
	p.client = client
	p.orchestrator = entclawruntime.NewOrchestrator(client, tools, sessions)
	return nil
}

func (p *EntclawPlugin) Reload() error { return nil }
