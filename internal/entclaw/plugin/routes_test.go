package entclawplugin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingRouteRegistrar struct {
	routes map[string]http.HandlerFunc
}

func newRecordingRouteRegistrar() *recordingRouteRegistrar {
	return &recordingRouteRegistrar{
		routes: make(map[string]http.HandlerFunc),
	}
}

func (r *recordingRouteRegistrar) HandleFunc(pattern string, handler http.HandlerFunc) {
	r.routes[pattern] = handler
}

func (r *recordingRouteRegistrar) RequireAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Auth-Mode", "optional")
		handler(w, req)
	}
}

func (r *recordingRouteRegistrar) RequireAuthStrict(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Auth-Mode", "strict")
		handler(w, req)
	}
}

func TestRegisterRoutesUsesGatewayAuthForSkillsEndpoints(t *testing.T) {
	t.Parallel()

	registrar := newRecordingRouteRegistrar()
	var plugin EntclawPlugin
	plugin.RegisterRoutes(registrar)

	handler, ok := registrar.routes["/v1/entclaw/skills"]
	if !ok {
		t.Fatal("skills route not registered")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/entclaw/skills", nil)
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	if got := recorder.Header().Get("X-Auth-Mode"); got != "optional" {
		t.Fatalf("X-Auth-Mode = %q, want optional", got)
	}
}
