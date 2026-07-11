package backend

import "time"

// /xai/v1/models 直接返回此列表，不转发上游。
var staticXaiModels = []map[string]any{
	{"id": "grok-build-0.1", "object": "model", "created": int64(1779321600), "owned_by": "xai"},
	{"id": "grok-4.5", "object": "model", "created": int64(1783526400), "owned_by": "xai"},
	{"id": "grok-4.3", "object": "model", "created": int64(1775606400), "owned_by": "xai"},
	{"id": "grok-4.20-0309-reasoning", "object": "model", "created": int64(1773014400), "owned_by": "xai"},
	{"id": "grok-4.20-0309-non-reasoning", "object": "model", "created": int64(1773014400), "owned_by": "xai"},
	{"id": "grok-4.20-multi-agent-0309", "object": "model", "created": int64(1773014400), "owned_by": "xai"},
	{"id": "grok-3-mini", "object": "model", "created": int64(1740960000), "owned_by": "xai"},
	{"id": "grok-3-mini-fast", "object": "model", "created": int64(1740960000), "owned_by": "xai"},
	{"id": "grok-composer-2.5-fast", "object": "model", "created": int64(1740960000), "owned_by": "xai"},
	{"id": "grok-imagine-image", "object": "model", "created": int64(0), "owned_by": "xai"},
	{"id": "grok-imagine-image-quality", "object": "model", "created": int64(0), "owned_by": "xai"},
	{"id": "grok-imagine-video", "object": "model", "created": int64(0), "owned_by": "xai"},
	{"id": "grok-imagine-video-1.5-preview", "object": "model", "created": int64(0), "owned_by": "xai"},
}

// StaticModelIDs 返回静态模型 id 列表（端点 Models/Routes 默认种子）。
func StaticModelIDs() []string {
	out := make([]string, 0, len(staticXaiModels))
	for _, m := range staticXaiModels {
		if id, ok := m["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	return out
}

// LocalModelsResponse 返回 OpenAI 兼容的 models 列表响应体。
func LocalModelsResponse() map[string]any {
	data := make([]map[string]any, 0, len(staticXaiModels))
	for _, m := range staticXaiModels {
		item := map[string]any{
			"id":       m["id"],
			"object":   m["object"],
			"created":  m["created"],
			"owned_by": m["owned_by"],
		}
		// created=0 时用当前时间，避免客户端异常
		if c, ok := item["created"].(int64); ok && c == 0 {
			item["created"] = time.Now().Unix()
		}
		data = append(data, item)
	}
	return map[string]any{
		"object": "list",
		"data":   data,
	}
}
