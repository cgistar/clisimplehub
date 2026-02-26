package streaming

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// AWS EventStream 帧格式常量
const (
	preludeSize    = 12                // Total Length(4) + Header Length(4) + Prelude CRC(4)
	minMessageSize = preludeSize + 4   // Prelude + Message CRC
	maxMessageSize = 16 * 1024 * 1024  // 16 MB
	defaultMaxBuf  = 16 * 1024 * 1024  // 16 MB
	defaultMaxErrs = 5
)

// Header 值类型标识（AWS EventStream 协议）
const (
	headerTypeBoolTrue  = 0
	headerTypeBoolFalse = 1
	headerTypeString    = 7
)

// DecoderState 解码器状态
type DecoderState int

const (
	DecoderReady      DecoderState = iota // 就绪
	DecoderParsing                        // 解析中
	DecoderRecovering                     // 容错恢复中
	DecoderStopped                        // 已停止
)

// Frame 表示解码后的 AWS EventStream 帧
type Frame struct {
	Headers map[string]string
	Payload []byte
}

// MessageType 返回 ":message-type" header
func (f *Frame) MessageType() string { return f.Headers[":message-type"] }

// EventType 返回 ":event-type" header
func (f *Frame) EventType() string { return f.Headers[":event-type"] }

// ExceptionType 返回 ":exception-type" header
func (f *Frame) ExceptionType() string { return f.Headers[":exception-type"] }

// ErrorCode 返回 ":error-code" header
func (f *Frame) ErrorCode() string { return f.Headers[":error-code"] }

// EventStreamDecoder 流式二进制解码器
type EventStreamDecoder struct {
	buffer        []byte
	state         DecoderState
	framesDecoded int
	errorCount    int
	maxErrors     int
	maxBufferSize int
	bytesSkipped  int
}

// NewEventStreamDecoder 创建解码器
func NewEventStreamDecoder() *EventStreamDecoder {
	return &EventStreamDecoder{
		buffer:        make([]byte, 0, 8192),
		state:         DecoderReady,
		maxErrors:     defaultMaxErrs,
		maxBufferSize: defaultMaxBuf,
	}
}

// Feed 向解码器提供数据
func (d *EventStreamDecoder) Feed(data []byte) error {
	newSize := len(d.buffer) + len(data)
	if newSize > d.maxBufferSize {
		return fmt.Errorf("buffer overflow: %d > %d", newSize, d.maxBufferSize)
	}
	d.buffer = append(d.buffer, data...)
	if d.state == DecoderRecovering {
		d.state = DecoderReady
	}
	return nil
}

// Decode 尝试解码下一帧；数据不足返回 (nil, nil)
func (d *EventStreamDecoder) Decode() (*Frame, error) {
	if d.state == DecoderStopped {
		return nil, fmt.Errorf("decoder stopped: %d consecutive errors", d.errorCount)
	}
	if len(d.buffer) == 0 {
		d.state = DecoderReady
		return nil, nil
	}

	d.state = DecoderParsing
	frame, consumed, err := parseFrame(d.buffer)
	if err != nil {
		d.errorCount++
		if d.errorCount >= d.maxErrors {
			d.state = DecoderStopped
			return nil, fmt.Errorf("too many errors (%d): %w", d.errorCount, err)
		}
		d.tryRecover(err)
		d.state = DecoderRecovering
		return nil, err
	}
	if frame == nil {
		d.state = DecoderReady
		return nil, nil
	}

	// 紧凑化 buffer（避免已消费字节驻留内存）
	remaining := len(d.buffer) - consumed
	copy(d.buffer, d.buffer[consumed:])
	d.buffer = d.buffer[:remaining]
	d.state = DecoderReady
	d.framesDecoded++
	d.errorCount = 0
	return frame, nil
}

// DecodeAll 解码所有可用帧
func (d *EventStreamDecoder) DecodeAll() ([]*Frame, error) {
	var frames []*Frame
	for {
		if d.state == DecoderStopped || d.state == DecoderRecovering {
			break
		}
		frame, err := d.Decode()
		if err != nil {
			return frames, err
		}
		if frame == nil {
			break
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

// Reset 重置解码器到初始状态
func (d *EventStreamDecoder) Reset() {
	d.buffer = d.buffer[:0]
	d.state = DecoderReady
	d.framesDecoded = 0
	d.errorCount = 0
	d.bytesSkipped = 0
}

// State 返回当前状态
func (d *EventStreamDecoder) State() DecoderState { return d.state }

// FramesDecoded 返回已解码帧数
func (d *EventStreamDecoder) FramesDecoded() int { return d.framesDecoded }

// BufferLen 返回缓冲区字节数
func (d *EventStreamDecoder) BufferLen() int { return len(d.buffer) }

// parseFrame 从缓冲区解析一帧（纯函数）
// 返回 (frame, consumed, error)；数据不足时 frame=nil, err=nil
func parseFrame(buf []byte) (*Frame, int, error) {
	if len(buf) < preludeSize {
		return nil, 0, nil
	}

	totalLen := binary.BigEndian.Uint32(buf[0:4])
	headerLen := binary.BigEndian.Uint32(buf[4:8])
	preludeCRC := binary.BigEndian.Uint32(buf[8:12])

	if totalLen < uint32(minMessageSize) {
		return nil, 0, &parseError{kind: errPrelude, msg: fmt.Sprintf("message too small: %d < %d", totalLen, minMessageSize)}
	}
	if totalLen > uint32(maxMessageSize) {
		return nil, 0, &parseError{kind: errPrelude, msg: fmt.Sprintf("message too large: %d > %d", totalLen, maxMessageSize)}
	}

	tl := int(totalLen)
	if len(buf) < tl {
		return nil, 0, nil // 数据不足
	}

	// Prelude CRC 校验
	actualPreludeCRC := crc32.ChecksumIEEE(buf[0:8])
	if actualPreludeCRC != preludeCRC {
		return nil, 0, &parseError{kind: errPrelude, msg: fmt.Sprintf("prelude CRC mismatch: 0x%08x != 0x%08x", actualPreludeCRC, preludeCRC)}
	}

	// Message CRC 校验
	messageCRC := binary.BigEndian.Uint32(buf[tl-4 : tl])
	actualMessageCRC := crc32.ChecksumIEEE(buf[0 : tl-4])
	if actualMessageCRC != messageCRC {
		return nil, 0, &parseError{kind: errData, msg: fmt.Sprintf("message CRC mismatch: 0x%08x != 0x%08x", actualMessageCRC, messageCRC)}
	}

	// Header 解析
	hl := int(headerLen)
	headersEnd := preludeSize + hl
	if headersEnd > tl-4 {
		return nil, 0, &parseError{kind: errData, msg: "header length exceeds message boundary"}
	}
	headers, err := parseHeaders(buf[preludeSize:headersEnd])
	if err != nil {
		return nil, 0, &parseError{kind: errData, msg: err.Error()}
	}

	// Payload
	payload := make([]byte, tl-4-headersEnd)
	copy(payload, buf[headersEnd:tl-4])

	return &Frame{Headers: headers, Payload: payload}, tl, nil
}

// parseHeaders 解析 header 段
func parseHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	offset := 0
	for offset < len(data) {
		nameLen := int(data[offset])
		offset++
		if nameLen == 0 || offset+nameLen > len(data) {
			return nil, fmt.Errorf("invalid header name length %d at offset %d", nameLen, offset-1)
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		if offset >= len(data) {
			return nil, fmt.Errorf("unexpected end of header data")
		}
		valueType := data[offset]
		offset++

		switch valueType {
		case headerTypeBoolTrue:
			headers[name] = "true"
		case headerTypeBoolFalse:
			headers[name] = "false"
		case headerTypeString:
			if offset+2 > len(data) {
				return nil, fmt.Errorf("incomplete string header value")
			}
			valLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if offset+valLen > len(data) {
				return nil, fmt.Errorf("string header value overflows: need %d, have %d", valLen, len(data)-offset)
			}
			headers[name] = string(data[offset : offset+valLen])
			offset += valLen
		default:
			// 跳过未知类型：按 AWS 规范推算长度
			skip, err := headerValueSize(valueType, data[offset:])
			if err != nil {
				return nil, fmt.Errorf("unsupported header type %d: %w", valueType, err)
			}
			offset += skip
		}
	}
	return headers, nil
}

// headerValueSize 返回给定 header 值类型的字节数（用于跳过未知类型）
func headerValueSize(vtype byte, data []byte) (int, error) {
	switch vtype {
	case 2: // Byte
		return 1, nil
	case 3: // Short
		return 2, nil
	case 4: // Integer
		return 4, nil
	case 5, 8: // Long, Timestamp
		return 8, nil
	case 9: // UUID
		return 16, nil
	case 6: // ByteArray
		if len(data) < 2 {
			return 0, fmt.Errorf("incomplete byte array length")
		}
		return 2 + int(binary.BigEndian.Uint16(data[0:2])), nil
	default:
		return 0, fmt.Errorf("unknown header value type: %d", vtype)
	}
}

// 错误分类（决定恢复策略）
const (
	errPrelude = iota // Prelude 阶段错误 → 逐字节跳过
	errData           // Data 阶段错误 → 跳过整帧
)

type parseError struct {
	kind int
	msg  string
}

func (e *parseError) Error() string { return e.msg }

// tryRecover 容错恢复
func (d *EventStreamDecoder) tryRecover(err error) {
	if len(d.buffer) == 0 {
		return
	}
	pe, ok := err.(*parseError)
	if !ok {
		// 通用：跳过 1 字节
		d.buffer = d.buffer[1:]
		d.bytesSkipped++
		return
	}

	switch pe.kind {
	case errPrelude:
		// Prelude 错误：逐字节跳过
		d.buffer = d.buffer[1:]
		d.bytesSkipped++
	case errData:
		// Data 错误：尝试跳过整帧
		if len(d.buffer) >= 4 {
			totalLen := int(binary.BigEndian.Uint32(d.buffer[0:4]))
			if totalLen >= minMessageSize && totalLen <= len(d.buffer) {
				d.bytesSkipped += totalLen
				d.buffer = d.buffer[totalLen:]
				return
			}
		}
		d.buffer = d.buffer[1:]
		d.bytesSkipped++
	}
}
