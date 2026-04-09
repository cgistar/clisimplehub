package entclawplugin

import "clisimplehub/internal/plugin"

func (p *EntclawPlugin) RegisterRoutes(r plugin.RouteRegistrar) {
	r.HandleFunc("/v1/entclaw/messages", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/chat/completions", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/responses", r.RequireAuth(p.handleInference))
	r.HandleFunc("/v1/entclaw/skills", r.RequireAuth(p.handleSkills))
	r.HandleFunc("/v1/entclaw/skills/*", r.RequireAuth(p.handleSkills))
}
