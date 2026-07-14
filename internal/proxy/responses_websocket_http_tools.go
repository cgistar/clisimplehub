package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	responsesHTTPToolCacheMaxPerSession = 256
	responsesHTTPToolCacheTTL           = 30 * time.Minute
)

var (
	defaultResponsesHTTPToolOutputCache = newResponsesHTTPToolCache(0, responsesHTTPToolCacheMaxPerSession)
	defaultResponsesHTTPToolCallCache   = newResponsesHTTPToolCache(0, responsesHTTPToolCacheMaxPerSession)
	defaultResponsesHTTPToolSessionRefs = newResponsesHTTPToolSessionRefs()
)

type responsesHTTPToolCache struct {
	mu            sync.Mutex
	ttl           time.Duration
	maxPerSession int
	sessions      map[string]*responsesHTTPToolSession
}

type responsesHTTPToolSession struct {
	lastSeen time.Time
	items    map[string]json.RawMessage
	order    []string
}

func newResponsesHTTPToolCache(ttl time.Duration, maxPerSession int) *responsesHTTPToolCache {
	if ttl <= 0 {
		ttl = responsesHTTPToolCacheTTL
	}
	if maxPerSession <= 0 {
		maxPerSession = responsesHTTPToolCacheMaxPerSession
	}
	return &responsesHTTPToolCache{
		ttl:           ttl,
		maxPerSession: maxPerSession,
		sessions:      make(map[string]*responsesHTTPToolSession),
	}
}

func (c *responsesHTTPToolCache) record(sessionKey, callID string, item json.RawMessage) {
	sessionKey = strings.TrimSpace(sessionKey)
	callID = strings.TrimSpace(callID)
	if c == nil || sessionKey == "" || callID == "" || len(item) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupLocked(now)
	session := c.sessions[sessionKey]
	if session == nil {
		session = &responsesHTTPToolSession{items: make(map[string]json.RawMessage)}
		c.sessions[sessionKey] = session
	}
	session.lastSeen = now
	if _, ok := session.items[callID]; !ok {
		session.order = append(session.order, callID)
	}
	session.items[callID] = append(json.RawMessage(nil), item...)
	for len(session.order) > c.maxPerSession {
		evict := session.order[0]
		session.order = session.order[1:]
		delete(session.items, evict)
	}
}

func (c *responsesHTTPToolCache) get(sessionKey, callID string) (json.RawMessage, bool) {
	sessionKey = strings.TrimSpace(sessionKey)
	callID = strings.TrimSpace(callID)
	if c == nil || sessionKey == "" || callID == "" {
		return nil, false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupLocked(now)
	session := c.sessions[sessionKey]
	if session == nil {
		return nil, false
	}
	session.lastSeen = now
	item, ok := session.items[callID]
	if !ok || len(item) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), item...), true
}

func (c *responsesHTTPToolCache) deleteSession(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if c == nil || sessionKey == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionKey)
}

func (c *responsesHTTPToolCache) cleanupLocked(now time.Time) {
	if c == nil || c.ttl <= 0 {
		return
	}
	for key, session := range c.sessions {
		if session == nil || now.Sub(session.lastSeen) > c.ttl {
			delete(c.sessions, key)
		}
	}
}

type responsesHTTPToolSessionRefs struct {
	mu     sync.Mutex
	counts map[string]int
}

func newResponsesHTTPToolSessionRefs() *responsesHTTPToolSessionRefs {
	return &responsesHTTPToolSessionRefs{counts: make(map[string]int)}
}

func (c *responsesHTTPToolSessionRefs) acquire(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if c == nil || sessionKey == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[sessionKey]++
}

func (c *responsesHTTPToolSessionRefs) release(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if c == nil || sessionKey == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := c.counts[sessionKey]
	if count <= 1 {
		delete(c.counts, sessionKey)
		return true
	}
	c.counts[sessionKey] = count - 1
	return false
}

func retainResponsesHTTPToolCaches(sessionKey string) {
	defaultResponsesHTTPToolSessionRefs.acquire(sessionKey)
}

func releaseResponsesHTTPToolCaches(sessionKey string) {
	if !defaultResponsesHTTPToolSessionRefs.release(sessionKey) {
		return
	}
	defaultResponsesHTTPToolOutputCache.deleteSession(sessionKey)
	defaultResponsesHTTPToolCallCache.deleteSession(sessionKey)
}

func responsesHTTPWebsocketSessionKey(req *http.Request) string {
	if req == nil {
		return ""
	}
	if requestID := strings.TrimSpace(headerValueCI(req.Header, "X-Client-Request-Id")); requestID != "" {
		return requestID
	}
	if raw := strings.TrimSpace(headerValueCI(req.Header, "X-Codex-Turn-Metadata")); raw != "" {
		if sessionID := strings.TrimSpace(gjson.Get(raw, "session_id").String()); sessionID != "" {
			return sessionID
		}
	}
	if sessionID := strings.TrimSpace(headerValueCI(req.Header, "session_id")); sessionID != "" {
		return sessionID
	}
	return ""
}

func headerValueCI(headers http.Header, key string) string {
	if headers == nil {
		return ""
	}
	if val := strings.TrimSpace(headers.Get(key)); val != "" {
		return val
	}
	for existing, values := range headers {
		if !strings.EqualFold(existing, key) {
			continue
		}
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func repairResponsesHTTPToolCalls(sessionKey string, payload []byte) []byte {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(payload) == 0 {
		return payload
	}
	input := gjson.GetBytes(payload, "input")
	if !input.Exists() || !input.IsArray() {
		return payload
	}
	// HTTP 路径不保留 previous_response_id，禁止孤儿 output。
	updatedRaw, err := repairResponsesHTTPToolCallsArray(
		defaultResponsesHTTPToolOutputCache,
		defaultResponsesHTTPToolCallCache,
		sessionKey,
		input.Raw,
		false,
	)
	if err != nil || updatedRaw == "" || updatedRaw == input.Raw {
		return payload
	}
	updated, errSet := sjson.SetRawBytes(payload, "input", []byte(updatedRaw))
	if errSet != nil {
		return payload
	}
	return updated
}

func repairResponsesHTTPToolCallsArray(outputCache, callCache *responsesHTTPToolCache, sessionKey, rawArray string, allowOrphanOutputs bool) (string, error) {
	rawArray = strings.TrimSpace(rawArray)
	if rawArray == "" {
		return "[]", nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(rawArray), &items); err != nil {
		return "", err
	}
	outputPresent := make(map[string]struct{}, len(items))
	callPresent := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		itemType := strings.TrimSpace(gjson.GetBytes(item, "type").String())
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		if callID == "" {
			continue
		}
		switch {
		case isResponsesHTTPToolCallOutputType(itemType):
			outputPresent[callID] = struct{}{}
			outputCache.record(sessionKey, callID, item)
		case isResponsesHTTPToolCallType(itemType):
			callPresent[callID] = struct{}{}
			if callCache != nil {
				callCache.record(sessionKey, callID, item)
			}
		}
	}

	filtered := make([]json.RawMessage, 0, len(items))
	insertedCalls := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		itemType := strings.TrimSpace(gjson.GetBytes(item, "type").String())
		if isResponsesHTTPToolCallOutputType(itemType) {
			callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
			if callID == "" {
				continue
			}
			if allowOrphanOutputs {
				filtered = append(filtered, item)
				continue
			}
			if _, ok := callPresent[callID]; ok {
				filtered = append(filtered, item)
				continue
			}
			if callCache != nil {
				if cached, ok := callCache.get(sessionKey, callID); ok {
					if _, already := insertedCalls[callID]; !already {
						filtered = append(filtered, cached)
						insertedCalls[callID] = struct{}{}
						callPresent[callID] = struct{}{}
					}
					filtered = append(filtered, item)
					continue
				}
			}
			continue
		}
		if !isResponsesHTTPToolCallType(itemType) {
			filtered = append(filtered, item)
			continue
		}
		callID := strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
		if callID == "" {
			continue
		}
		if _, ok := outputPresent[callID]; ok {
			filtered = append(filtered, item)
			continue
		}
		if cached, ok := outputCache.get(sessionKey, callID); ok {
			filtered = append(filtered, item)
			filtered = append(filtered, cached)
			outputPresent[callID] = struct{}{}
			continue
		}
	}
	out, err := json.Marshal(filtered)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func recordResponsesHTTPToolCallsFromPayload(sessionKey string, payload []byte) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(payload) == 0 {
		return
	}
	switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
	case "response.completed":
		output := gjson.GetBytes(payload, "response.output")
		if !output.IsArray() {
			return
		}
		for _, item := range output.Array() {
			if !isResponsesHTTPToolCallType(item.Get("type").String()) {
				continue
			}
			callID := strings.TrimSpace(item.Get("call_id").String())
			if callID == "" {
				continue
			}
			defaultResponsesHTTPToolCallCache.record(sessionKey, callID, json.RawMessage(item.Raw))
		}
	case "response.output_item.added", "response.output_item.done":
		item := gjson.GetBytes(payload, "item")
		if !item.IsObject() || !isResponsesHTTPToolCallType(item.Get("type").String()) {
			return
		}
		callID := strings.TrimSpace(item.Get("call_id").String())
		if callID == "" {
			return
		}
		defaultResponsesHTTPToolCallCache.record(sessionKey, callID, json.RawMessage(item.Raw))
	}
}

func dedupeResponsesHTTPInput(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	seen := make(map[string]struct{}, len(input.Array()))
	items := make([]json.RawMessage, 0, len(input.Array()))
	changed := false
	for _, item := range input.Array() {
		key := ""
		if id := strings.TrimSpace(item.Get("id").String()); id != "" {
			key = "id:" + id
		} else if callID := strings.TrimSpace(item.Get("call_id").String()); callID != "" {
			switch strings.TrimSpace(item.Get("type").String()) {
			case "function_call", "custom_tool_call":
				key = "call:" + callID
			case "function_call_output", "custom_tool_call_output":
				key = "output:" + callID
			}
		}
		if key != "" {
			if _, ok := seen[key]; ok {
				changed = true
				continue
			}
			seen[key] = struct{}{}
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	if !changed {
		return body
	}
	updatedInput, err := json.Marshal(items)
	if err != nil {
		return body
	}
	updated, err := sjson.SetRawBytes(body, "input", updatedInput)
	if err != nil {
		return body
	}
	return updated
}

func isResponsesHTTPToolCallType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call", "custom_tool_call":
		return true
	default:
		return false
	}
}

func isResponsesHTTPToolCallOutputType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call_output", "custom_tool_call_output":
		return true
	default:
		return false
	}
}
