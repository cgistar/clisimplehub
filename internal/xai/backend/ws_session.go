package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// WebsocketIDState 维护会话级 previous_response_id 映射与 transcript。
type WebsocketIDState struct {
	mu                   sync.Mutex
	downstreamToUpstream map[string]string
	sequence             int
	transcriptInput      []json.RawMessage
}

func NewWebsocketIDState() *WebsocketIDState {
	return &WebsocketIDState{downstreamToUpstream: make(map[string]string)}
}

type WebsocketIDStore struct {
	mu       sync.Mutex
	sessions map[string]*WebsocketIDState
}

func NewWebsocketIDStore() *WebsocketIDStore {
	return &WebsocketIDStore{sessions: make(map[string]*WebsocketIDState)}
}

var globalWSIDStore = NewWebsocketIDStore()

func GlobalWebsocketIDStore() *WebsocketIDStore { return globalWSIDStore }

func (s *WebsocketIDStore) Get(sessionID string) *WebsocketIDState {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*WebsocketIDState)
	}
	st := s.sessions[sessionID]
	if st == nil {
		st = NewWebsocketIDState()
		s.sessions[sessionID] = st
	}
	return st
}

func (s *WebsocketIDStore) Delete(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// RequestIDMapper 单次 response.create 的上下游 ID 映射器。
type RequestIDMapper struct {
	state                *WebsocketIDState
	downstreamPreviousID string
	upstreamPreviousID   string
	upstreamResponseID   string
	downstreamResponseID string
}

func NewRequestIDMapper(state *WebsocketIDState, downstreamRequest []byte) *RequestIDMapper {
	if state == nil {
		return nil
	}
	downstreamPreviousID := strings.TrimSpace(gjson.GetBytes(downstreamRequest, "previous_response_id").String())
	upstreamPreviousID := downstreamPreviousID
	if downstreamPreviousID != "" {
		upstreamPreviousID = state.UpstreamIDForDownstream(downstreamPreviousID)
	}
	return &RequestIDMapper{
		state:                state,
		downstreamPreviousID: downstreamPreviousID,
		upstreamPreviousID:   upstreamPreviousID,
	}
}

func (s *WebsocketIDState) UpstreamIDForDownstream(downstreamID string) string {
	downstreamID = strings.TrimSpace(downstreamID)
	if s == nil || downstreamID == "" {
		return downstreamID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if upstreamID, ok := s.downstreamToUpstream[downstreamID]; ok {
		return strings.TrimSpace(upstreamID)
	}
	return downstreamID
}

func (s *WebsocketIDState) MapDownstreamToUpstream(downstreamID, upstreamID string) {
	downstreamID = strings.TrimSpace(downstreamID)
	if s == nil || downstreamID == "" {
		return
	}
	s.mu.Lock()
	if s.downstreamToUpstream == nil {
		s.downstreamToUpstream = make(map[string]string)
	}
	s.downstreamToUpstream[downstreamID] = strings.TrimSpace(upstreamID)
	s.mu.Unlock()
}

func (s *WebsocketIDState) SnapshotTranscriptInput() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.transcriptInput) == 0 {
		return nil
	}
	return marshalRawMessages(s.transcriptInput)
}

func (s *WebsocketIDState) PrependTranscriptInput(payload []byte) []byte {
	if s == nil || len(payload) == 0 {
		return payload
	}
	s.mu.Lock()
	prefix := make([]json.RawMessage, 0, len(s.transcriptInput))
	for _, item := range s.transcriptInput {
		prefix = append(prefix, bytes.Clone(item))
	}
	s.mu.Unlock()
	if len(prefix) == 0 {
		return payload
	}
	current := jsonRawMessages(gjson.GetBytes(payload, "input"))
	merged := append(prefix, current...)
	out, err := sjson.SetRawBytes(payload, "input", marshalRawMessages(merged))
	if err != nil {
		return payload
	}
	return out
}

func (s *WebsocketIDState) RecordTranscriptTurn(requestPayload, completedPayload []byte) {
	if s == nil || len(requestPayload) == 0 || len(completedPayload) == 0 {
		return
	}
	inputItems := jsonRawMessages(gjson.GetBytes(requestPayload, "input"))
	outputItems := jsonRawMessages(gjson.GetBytes(completedPayload, "response.output"))
	if len(outputItems) == 0 {
		// completed 可能直接是 response 对象
		outputItems = jsonRawMessages(gjson.GetBytes(completedPayload, "output"))
	}
	if len(inputItems) == 0 && len(outputItems) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(gjson.GetBytes(requestPayload, "previous_response_id").String()) == "" {
		s.transcriptInput = nil
	}
	s.transcriptInput = append(s.transcriptInput, inputItems...)
	s.transcriptInput = append(s.transcriptInput, outputItems...)
}

func (s *WebsocketIDState) ReplaceTranscriptWithItems(items ...[]byte) {
	if s == nil {
		return
	}
	next := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || !json.Valid(item) {
			continue
		}
		next = append(next, bytes.Clone(item))
	}
	s.mu.Lock()
	s.transcriptInput = next
	s.mu.Unlock()
}

// UpstreamRequestPayload 出站：下游 previous_response_id → 上游 id。
func (m *RequestIDMapper) UpstreamRequestPayload(payload []byte) []byte {
	if m == nil || len(payload) == 0 {
		return payload
	}
	if m.downstreamPreviousID == m.upstreamPreviousID {
		// 仍可能需要 strip instructions
		if m.upstreamPreviousID != "" && gjson.GetBytes(payload, "instructions").Exists() {
			payload, _ = sjson.DeleteBytes(payload, "instructions")
		}
		return payload
	}
	if m.upstreamPreviousID == "" {
		out, err := sjson.DeleteBytes(payload, "previous_response_id")
		if err != nil {
			return payload
		}
		if m.downstreamPreviousID != "" && m.state != nil {
			out = m.state.PrependTranscriptInput(out)
		}
		if gjson.GetBytes(out, "instructions").Exists() {
			out, _ = sjson.DeleteBytes(out, "instructions")
		}
		return out
	}
	out, err := sjson.SetBytes(payload, "previous_response_id", m.upstreamPreviousID)
	if err != nil {
		return payload
	}
	if gjson.GetBytes(out, "instructions").Exists() {
		out, _ = sjson.DeleteBytes(out, "instructions")
	}
	return out
}

// DownstreamResponsePayload 入站：上游 id → 下游 id 全树改写。
func (m *RequestIDMapper) DownstreamResponsePayload(payload []byte) []byte {
	if m == nil || len(payload) == 0 {
		return payload
	}
	upstreamResponseID := strings.TrimSpace(gjson.GetBytes(payload, "response.id").String())
	if upstreamResponseID == "" {
		upstreamResponseID = strings.TrimSpace(gjson.GetBytes(payload, "id").String())
	}
	downstreamResponseID := m.DownstreamIDForUpstreamResponse(upstreamResponseID)
	if downstreamResponseID == "" {
		return payload
	}
	return rewriteDownstreamIDs(payload, m.upstreamResponseID, downstreamResponseID, m.upstreamPreviousID, m.downstreamPreviousID)
}

func (m *RequestIDMapper) DownstreamIDForUpstreamResponse(upstreamResponseID string) string {
	upstreamResponseID = strings.TrimSpace(upstreamResponseID)
	if m == nil || m.state == nil {
		return upstreamResponseID
	}
	if m.upstreamResponseID != "" {
		return m.downstreamResponseID
	}
	if upstreamResponseID == "" {
		return ""
	}

	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.upstreamResponseID = upstreamResponseID
	m.downstreamResponseID = upstreamResponseID
	if m.state.downstreamToUpstream == nil {
		m.state.downstreamToUpstream = make(map[string]string)
	}
	_, seen := m.state.downstreamToUpstream[upstreamResponseID]
	if (m.downstreamPreviousID != "" && m.upstreamPreviousID != "" && upstreamResponseID == m.upstreamPreviousID) || seen {
		m.state.sequence++
		m.downstreamResponseID = fmt.Sprintf("%s-xai-%d", upstreamResponseID, m.state.sequence)
	}
	m.state.downstreamToUpstream[upstreamResponseID] = upstreamResponseID
	m.state.downstreamToUpstream[m.downstreamResponseID] = upstreamResponseID
	return m.downstreamResponseID
}

func rewriteDownstreamIDs(payload []byte, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID string) []byte {
	upstreamResponseID = strings.TrimSpace(upstreamResponseID)
	downstreamResponseID = strings.TrimSpace(downstreamResponseID)
	upstreamPreviousID = strings.TrimSpace(upstreamPreviousID)
	downstreamPreviousID = strings.TrimSpace(downstreamPreviousID)
	if len(payload) == 0 || (upstreamResponseID == downstreamResponseID && upstreamPreviousID == downstreamPreviousID) {
		return payload
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return payload
	}
	if !rewriteIDValue(value, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, "") {
		return payload
	}
	out, err := json.Marshal(value)
	if err != nil {
		return payload
	}
	return out
}

func rewriteIDValue(value any, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for childKey, childValue := range typed {
			if childString, ok := childValue.(string); ok {
				replaced := rewriteIDString(childString, childKey, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID)
				if replaced != childString {
					typed[childKey] = replaced
					changed = true
				}
				continue
			}
			if rewriteIDValue(childValue, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, childKey) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i := range typed {
			if rewriteIDValue(typed[i], upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID, key) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func rewriteIDString(value, key, upstreamResponseID, downstreamResponseID, upstreamPreviousID, downstreamPreviousID string) string {
	switch key {
	case "id", "item_id":
		if upstreamResponseID != "" && downstreamResponseID != "" && downstreamResponseID != upstreamResponseID && strings.Contains(value, upstreamResponseID) {
			return strings.ReplaceAll(value, upstreamResponseID, downstreamResponseID)
		}
	case "previous_response_id":
		if upstreamPreviousID != "" && downstreamPreviousID != "" && value == upstreamPreviousID {
			return downstreamPreviousID
		}
	}
	return value
}

func jsonRawMessages(result gjson.Result) []json.RawMessage {
	if !result.Exists() || !result.IsArray() {
		return nil
	}
	items := result.Array()
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw := bytes.TrimSpace([]byte(item.Raw))
		if len(raw) == 0 || !json.Valid(raw) {
			continue
		}
		out = append(out, bytes.Clone(raw))
	}
	return out
}

func marshalRawMessages(items []json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(bytes.TrimSpace(item))
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// BuildWebsocketRequestBody 规范化 WS 出站 body。
func BuildWebsocketRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	out := bytes.Clone(body)
	out, _ = sjson.SetBytes(out, "type", "response.create")
	out, _ = sjson.DeleteBytes(out, "stream")
	out, _ = sjson.DeleteBytes(out, "stream_options")
	out, _ = sjson.DeleteBytes(out, "background")
	out, _ = sjson.SetBytes(out, "store", true)
	if strings.TrimSpace(gjson.GetBytes(out, "previous_response_id").String()) != "" {
		out, _ = sjson.DeleteBytes(out, "instructions")
	}
	return out
}

// IsWebsocketWarmup 是否 generate:false 预热请求。
func IsWebsocketWarmup(payload []byte) bool {
	generate := gjson.GetBytes(payload, "generate")
	return generate.Exists() && !generate.Bool()
}

// BuildWarmupCompletedPayload 伪造 warmup 的 response.completed。
func BuildWarmupCompletedPayload(createdPayload []byte) []byte {
	completed := []byte(`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
	if sequence := gjson.GetBytes(createdPayload, "sequence_number"); sequence.Exists() {
		completed, _ = sjson.SetBytes(completed, "sequence_number", sequence.Int()+1)
	}
	if response := gjson.GetBytes(createdPayload, "response"); response.Exists() && response.IsObject() {
		responsePayload := []byte(response.Raw)
		responsePayload, _ = sjson.SetBytes(responsePayload, "status", "completed")
		if !gjson.GetBytes(responsePayload, "output").Exists() {
			responsePayload, _ = sjson.SetRawBytes(responsePayload, "output", []byte("[]"))
		}
		if !gjson.GetBytes(responsePayload, "usage").Exists() {
			responsePayload, _ = sjson.SetRawBytes(responsePayload, "usage", []byte(`{"input_tokens":0,"output_tokens":0,"total_tokens":0}`))
		}
		completed, _ = sjson.SetRawBytes(completed, "response", responsePayload)
	}
	return completed
}

// InputHasCompactionTrigger 检测 compaction_trigger。
func InputHasCompactionTrigger(body []byte) bool {
	return InputHasItemType(body, "compaction_trigger")
}

// BuildCompactionPayload 用 transcript 构造 compact 请求。
func BuildCompactionPayload(payload []byte, transcriptInput []byte) ([]byte, error) {
	out := bytes.Clone(payload)
	if len(transcriptInput) == 0 {
		transcriptInput = []byte("[]")
	}
	var err error
	out, err = sjson.SetRawBytes(out, "input", transcriptInput)
	if err != nil {
		return nil, err
	}
	out, _ = sjson.DeleteBytes(out, "previous_response_id")
	out, _ = sjson.DeleteBytes(out, "stream")
	out, _ = sjson.DeleteBytes(out, "tools")
	out = removeInputItemsByType(out, "compaction_trigger")
	return out, nil
}

// CompactionOutputItem 从 compact 响应提取 compaction item。
func CompactionOutputItem(compactData []byte, responseID string) []byte {
	itemResult := gjson.GetBytes(compactData, "output.0")
	item := []byte(`{"type":"compaction"}`)
	if itemResult.Exists() && itemResult.Type == gjson.JSON {
		item = []byte(itemResult.Raw)
	}
	if !gjson.GetBytes(item, "type").Exists() {
		item, _ = sjson.SetBytes(item, "type", "compaction")
	}
	if !gjson.GetBytes(item, "id").Exists() {
		if strings.HasPrefix(responseID, "resp_") {
			item, _ = sjson.SetBytes(item, "id", "cmp_"+strings.TrimPrefix(responseID, "resp_"))
		} else if responseID != "" {
			item, _ = sjson.SetBytes(item, "id", "cmp_"+responseID)
		}
	}
	return item
}

// CompactionResponseID 从 compact 响应取 id。
func CompactionResponseID(compactData []byte) string {
	if responseID := strings.TrimSpace(gjson.GetBytes(compactData, "id").String()); responseID != "" {
		if strings.HasPrefix(responseID, "resp_") {
			return responseID
		}
		return "resp_" + strings.TrimPrefix(responseID, "cmp_")
	}
	return fmt.Sprintf("resp_xai_compaction_%d", timeNowUnixNano())
}

// timeNowUnixNano 便于测试替换；默认 time.Now().UnixNano。
var timeNowUnixNano = func() int64 {
	return time.Now().UnixNano()
}
