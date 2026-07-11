package backend

import (
	"bytes"
	"io"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	dataTag  = []byte("data:")
	eventTag = []byte("event:")
)

// NormalizeReasoningSummaryData 将 xAI reasoning_text 形态转为 Codex summary 形态。
func NormalizeReasoningSummaryData(eventData []byte) []byte {
	if len(eventData) == 0 || !gjson.ValidBytes(eventData) {
		return eventData
	}

	normalized := eventData
	switch gjson.GetBytes(normalized, "type").String() {
	case "response.reasoning_text.delta":
		normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_text.delta")
		normalized = normalizeReasoningSummaryIndex(normalized)
	case "response.reasoning_text.done":
		normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.done")
		normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
		if text := gjson.GetBytes(normalized, "text"); text.Exists() {
			normalized, _ = sjson.SetBytes(normalized, "part.text", text.String())
		}
		normalized, _ = sjson.DeleteBytes(normalized, "text")
		normalized = normalizeReasoningSummaryIndex(normalized)
	case "response.content_part.added":
		if gjson.GetBytes(normalized, "part.type").String() == "reasoning_text" {
			normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.added")
			normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
			normalized = normalizeReasoningSummaryIndex(normalized)
		}
	case "response.content_part.done":
		if gjson.GetBytes(normalized, "part.type").String() == "reasoning_text" {
			normalized, _ = sjson.SetBytes(normalized, "type", "response.reasoning_summary_part.done")
			normalized, _ = sjson.SetBytes(normalized, "part.type", "summary_text")
			normalized = normalizeReasoningSummaryIndex(normalized)
		}
	}

	if item := gjson.GetBytes(normalized, "item"); item.Exists() && item.Type == gjson.JSON {
		updatedItem := normalizeReasoningOutputItem([]byte(item.Raw))
		if !bytes.Equal(updatedItem, []byte(item.Raw)) {
			normalized, _ = sjson.SetRawBytes(normalized, "item", updatedItem)
		}
	}
	if output := gjson.GetBytes(normalized, "response.output"); output.IsArray() {
		updatedOutput, changed := normalizeReasoningOutputItems(output.Array())
		if changed {
			normalized, _ = sjson.SetRawBytes(normalized, "response.output", updatedOutput)
		}
	}
	return normalized
}

// NormalizeReasoningSummaryDataEvents 对 reasoning_text.done 展开为 text.done + part.done。
func NormalizeReasoningSummaryDataEvents(eventData []byte) [][]byte {
	if len(eventData) == 0 || !gjson.ValidBytes(eventData) {
		return [][]byte{eventData}
	}
	if gjson.GetBytes(eventData, "type").String() != "response.reasoning_text.done" {
		return [][]byte{NormalizeReasoningSummaryData(eventData)}
	}
	textDone, _ := sjson.SetBytes(eventData, "type", "response.reasoning_summary_text.done")
	textDone = normalizeReasoningSummaryIndex(textDone)
	partDone := NormalizeReasoningSummaryData(eventData)
	return [][]byte{textDone, partDone}
}

func NormalizeReasoningSummaryEventName(eventName string) string {
	switch eventName {
	case "response.reasoning_text.delta":
		return "response.reasoning_summary_text.delta"
	case "response.reasoning_text.done":
		return "response.reasoning_summary_part.done"
	default:
		return eventName
	}
}

func NormalizeReasoningSummaryEventLine(line []byte, eventName string) []byte {
	if eventName == "" && bytes.HasPrefix(line, eventTag) {
		eventName = strings.TrimSpace(string(line[len(eventTag):]))
	}
	eventName = NormalizeReasoningSummaryEventName(eventName)
	if eventName == "" {
		return bytes.Clone(line)
	}
	return []byte("event: " + eventName)
}

// NormalizeNonStreamReasoning 非流完整 body 内 reasoning 形态归一。
func NormalizeNonStreamReasoning(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	// 可能是单条 completed JSON，或 SSE 拼接文本
	if gjson.ValidBytes(body) {
		return NormalizeReasoningSummaryData(body)
	}
	// SSE 块：逐 data 行处理
	return normalizeSSEPayload(body)
}

func normalizeSSEPayload(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var out bytes.Buffer
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, dataTag) {
			payload := bytes.TrimSpace(trimmed[len(dataTag):])
			// reasoning_text.done 可能展开为两条
			events := NormalizeReasoningSummaryDataEvents(payload)
			for j, ev := range events {
				if j > 0 {
					out.WriteByte('\n')
				}
				out.Write(dataTag)
				out.WriteByte(' ')
				out.Write(ev)
			}
			continue
		}
		if bytes.HasPrefix(trimmed, eventTag) {
			out.Write(NormalizeReasoningSummaryEventLine(trimmed, ""))
			continue
		}
		out.Write(line)
	}
	return out.Bytes()
}

func normalizeReasoningSummaryIndex(eventData []byte) []byte {
	contentIndex := gjson.GetBytes(eventData, "content_index")
	if contentIndex.Exists() && contentIndex.Raw != "" && !gjson.GetBytes(eventData, "summary_index").Exists() {
		eventData, _ = sjson.SetRawBytes(eventData, "summary_index", []byte(contentIndex.Raw))
	}
	eventData, _ = sjson.DeleteBytes(eventData, "content_index")
	return eventData
}

func normalizeReasoningOutputItems(items []gjson.Result) ([]byte, bool) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	changed := false
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		updated := normalizeReasoningOutputItem([]byte(item.Raw))
		if !bytes.Equal(updated, []byte(item.Raw)) {
			changed = true
		}
		buf.Write(updated)
	}
	buf.WriteByte(']')
	return buf.Bytes(), changed
}

func normalizeReasoningOutputItem(item []byte) []byte {
	if !gjson.ValidBytes(item) || gjson.GetBytes(item, "type").String() != "reasoning" {
		return item
	}
	normalized := item
	if summary := gjson.GetBytes(normalized, "summary"); summary.IsArray() {
		updated, changed := normalizeReasoningSummaryItems(summary.Array())
		if changed {
			normalized, _ = sjson.SetRawBytes(normalized, "summary", updated)
		}
	}
	content := gjson.GetBytes(normalized, "content")
	if !content.IsArray() {
		return normalized
	}
	summaryItems := make([]gjson.Result, 0, len(content.Array()))
	for _, part := range content.Array() {
		if part.Get("type").String() == "reasoning_text" {
			summaryItems = append(summaryItems, part)
		}
	}
	if len(summaryItems) == 0 {
		return normalized
	}
	updatedSummary, _ := normalizeReasoningSummaryItems(summaryItems)
	normalized, _ = sjson.SetRawBytes(normalized, "summary", updatedSummary)
	normalized, _ = sjson.DeleteBytes(normalized, "content")
	return normalized
}

func normalizeReasoningSummaryItems(items []gjson.Result) ([]byte, bool) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	changed := false
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		itemRaw := []byte(item.Raw)
		if item.Get("type").String() == "reasoning_text" {
			if next, err := sjson.SetBytes(itemRaw, "type", "summary_text"); err == nil {
				itemRaw = next
				changed = true
			}
		}
		buf.Write(itemRaw)
	}
	buf.WriteByte(']')
	return buf.Bytes(), changed
}

// reasoningStreamWrapper 对流式 SSE 做 reasoning 归一（行缓冲）。
type reasoningStreamWrapper struct {
	src     io.ReadCloser
	buf     bytes.Buffer
	pending []byte
	srcErr  error
	done    bool
}

func WrapReasoningStream(src io.ReadCloser) io.ReadCloser {
	if src == nil {
		return nil
	}
	return &reasoningStreamWrapper{src: src}
}

func (w *reasoningStreamWrapper) Read(p []byte) (int, error) {
	if w == nil {
		return 0, io.EOF
	}
	for {
		if len(w.pending) > 0 {
			n := copy(p, w.pending)
			w.pending = w.pending[n:]
			return n, nil
		}
		if w.done {
			if w.srcErr != nil {
				return 0, w.srcErr
			}
			return 0, io.EOF
		}

		tmp := make([]byte, 4096)
		n, err := w.src.Read(tmp)
		if n > 0 {
			w.buf.Write(tmp[:n])
			w.drainLines()
		}
		if err != nil {
			if w.buf.Len() > 0 {
				w.pending = append(w.pending, normalizeSSELine(w.buf.Bytes())...)
				w.buf.Reset()
			}
			w.srcErr = err
			w.done = true
			if len(w.pending) > 0 {
				n2 := copy(p, w.pending)
				w.pending = w.pending[n2:]
				return n2, nil
			}
			return 0, err
		}
		if len(w.pending) == 0 {
			// 需要更多字节才能凑成完整行
			continue
		}
	}
}

func (w *reasoningStreamWrapper) drainLines() {
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return
		}
		line := append([]byte(nil), data[:idx+1]...)
		w.buf.Next(idx + 1)
		w.pending = append(w.pending, normalizeSSELine(line)...)
	}
}

func (w *reasoningStreamWrapper) Close() error {
	if w == nil || w.src == nil {
		return nil
	}
	return w.src.Close()
}

func normalizeSSELine(line []byte) []byte {
	// 保留换行
	hasNL := bytes.HasSuffix(line, []byte("\n"))
	trimmed := bytes.TrimRight(line, "\r\n")
	if bytes.HasPrefix(trimmed, dataTag) {
		payload := bytes.TrimSpace(trimmed[len(dataTag):])
		events := NormalizeReasoningSummaryDataEvents(payload)
		var out bytes.Buffer
		for i, ev := range events {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.Write(dataTag)
			out.WriteByte(' ')
			out.Write(ev)
			if hasNL {
				out.WriteByte('\n')
			}
		}
		return out.Bytes()
	}
	if bytes.HasPrefix(trimmed, eventTag) {
		out := NormalizeReasoningSummaryEventLine(trimmed, "")
		if hasNL {
			out = append(out, '\n')
		}
		return out
	}
	return line
}
