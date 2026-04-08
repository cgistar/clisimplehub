package entclawplugin

import (
	"net/http"

	"clisimplehub/internal/plugin"
)

func (p *EntclawPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	r.HandleFunc("/v1/entclaw/messages", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/chat/completions", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/responses", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/skills", r.RequireAuthStrict(p.handleSkills))
	r.HandleFunc("/v1/entclaw/skills/*", r.RequireAuthStrict(p.handleSkills))
}

func (p *EntclawPlugin) handleInference(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (p *EntclawPlugin) handleSkills(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
