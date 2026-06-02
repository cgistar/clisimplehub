// Package logger provides logging utilities for the application.
package logger

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DebugFileLogger 提供基于文件的调试日志功能
// 当 debugMode="file" 时，将日志写入系统临时目录的 debug_logs 文件夹
type DebugFileLogger struct {
	mu      sync.RWMutex
	enabled bool
	logDir  string
}

var (
	globalDebugFileLogger *DebugFileLogger
	debugFileLoggerMu     sync.RWMutex
)

// GetDebugFileLogger 获取全局 DebugFileLogger 实例
func GetDebugFileLogger() *DebugFileLogger {
	debugFileLoggerMu.RLock()
	defer debugFileLoggerMu.RUnlock()
	return globalDebugFileLogger
}

// InitDebugFileLogger 初始化全局 DebugFileLogger
// debugMode: 配置值，当为 "file" 时启用文件日志
func InitDebugFileLogger(debugMode string) {
	debugFileLoggerMu.Lock()
	defer debugFileLoggerMu.Unlock()

	enabled := strings.EqualFold(strings.TrimSpace(debugMode), "file")

	if globalDebugFileLogger == nil {
		globalDebugFileLogger = &DebugFileLogger{}
	}

	globalDebugFileLogger.mu.Lock()
	defer globalDebugFileLogger.mu.Unlock()

	globalDebugFileLogger.enabled = enabled

	if enabled {
		// 使用系统临时目录
		tempDir := os.TempDir()
		globalDebugFileLogger.logDir = filepath.Join(tempDir, "clisimplehub_debug_logs")

		// 确保目录存在
		if err := os.MkdirAll(globalDebugFileLogger.logDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "[DebugFileLogger] 创建日志目录失败: %v\n", err)
			globalDebugFileLogger.enabled = false
		} else {
			fmt.Fprintf(os.Stdout, "[DebugFileLogger] 调试日志已启用，目录: %s\n", globalDebugFileLogger.logDir)
		}
	} else if globalDebugFileLogger.logDir != "" {
		fmt.Fprintf(os.Stdout, "[DebugFileLogger] 调试日志已禁用\n")
	}
}

// UpdateDebugMode 更新调试模式（配置变更时调用，立即生效）
func UpdateDebugMode(debugMode string) {
	InitDebugFileLogger(debugMode)
}

// IsEnabled 检查文件日志是否启用
func (d *DebugFileLogger) IsEnabled() bool {
	if d == nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.enabled
}

// GetLogDir 获取日志目录路径
func (d *DebugFileLogger) GetLogDir() string {
	if d == nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.logDir
}

// NewRequestDebugLogger 创建请求级别的调试日志记录器
func NewRequestDebugLogger(requestID string) *RequestDebugLogger {
	return &RequestDebugLogger{
		requestID:   requestID,
		startTime:   time.Now(),
		entries:     make([]string, 0),
		metadata:    make(map[string]string),
		sections:    make(map[string]string),
		rawSections: make(map[string][]byte),
	}
}

// RequestDebugLogger 请求级别的调试日志记录器
type RequestDebugLogger struct {
	mu          sync.Mutex
	requestID   string
	startTime   time.Time
	entries     []string
	metadata    map[string]string
	sections    map[string]string // 文本段落
	rawSections map[string][]byte // 二进制段落（会 base64 编码）
}

// SetMetadata 设置元数据
func (r *RequestDebugLogger) SetMetadata(key, value string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadata[key] = value
}

// Log 记录一条日志
func (r *RequestDebugLogger) Log(format string, args ...interface{}) {
	if r == nil {
		return
	}

	// 每次检查是否启用（支持热更新）
	logger := GetDebugFileLogger()
	if logger == nil || !logger.IsEnabled() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	elapsed := time.Since(r.startTime).Milliseconds()
	entry := fmt.Sprintf("[+%dms] "+format, append([]interface{}{elapsed}, args...)...)
	r.entries = append(r.entries, entry)
}

// SetSection 设置一个文本段落（如原始请求、转换后请求等）
func (r *RequestDebugLogger) SetSection(name, content string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sections[name] = content
}

// SetOriginalHeader 记录下游请求进入系统时的 header，敏感值会被掩码。
func (r *RequestDebugLogger) SetOriginalHeader(headers http.Header) {
	if r == nil {
		return
	}
	r.SetSection("OriginalHeader", FormatHeaderForDebug(headers))
}

// SetRawSection 设置一个二进制段落（会 base64 编码保存）
func (r *RequestDebugLogger) SetRawSection(name string, data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// 复制数据避免外部修改
	copied := make([]byte, len(data))
	copy(copied, data)
	r.rawSections[name] = copied
}

// AppendRawSection 追加二进制数据到段落
func (r *RequestDebugLogger) AppendRawSection(name string, data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rawSections[name] = append(r.rawSections[name], data...)
}

// Flush 将日志写入文件
func (r *RequestDebugLogger) Flush() error {
	if r == nil {
		return nil
	}

	// 最终检查是否启用
	logger := GetDebugFileLogger()
	if logger == nil || !logger.IsEnabled() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	logDir := logger.GetLogDir()
	if logDir == "" {
		return nil
	}

	// 生成文件名: 20251230_204100_87cbd7a2-claude-abc.log
	timestamp := r.startTime.Format("20060102_150405")
	shortID := r.requestID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	// 获取接口类型和端点名称
	interfaceType := sanitizeFilename(r.metadata["InterfaceType"])
	endpoint := sanitizeFilename(r.metadata["Endpoint"])

	// 构建文件名
	filenameParts := []string{timestamp, shortID}
	if interfaceType != "" {
		filenameParts = append(filenameParts, interfaceType)
	}
	if endpoint != "" {
		filenameParts = append(filenameParts, endpoint)
	}
	filename := filepath.Join(logDir, strings.Join(filenameParts, "-")+".log")

	// 构建文件内容
	content := r.buildContent()

	// 写入文件
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入调试日志失败: %w", err)
	}

	return nil
}

// buildContent 构建日志文件内容
func (r *RequestDebugLogger) buildContent() string {
	var content strings.Builder

	content.WriteString("=== Debug Log ===\n")
	content.WriteString(fmt.Sprintf("Request ID: %s\n", r.requestID))
	content.WriteString(fmt.Sprintf("Start Time: %s\n", r.startTime.Format(time.RFC3339)))
	content.WriteString(fmt.Sprintf("Duration: %dms\n", time.Since(r.startTime).Milliseconds()))

	// 写入元数据
	if len(r.metadata) > 0 {
		content.WriteString("\n--- Metadata ---\n")
		// 按固定顺序输出关键元数据
		orderedKeys := []string{"InterfaceType", "Path", "Method", "Endpoint", "Transformer", "UpstreamURL", "StatusCode"}
		written := make(map[string]bool)
		for _, k := range orderedKeys {
			if v, ok := r.metadata[k]; ok {
				content.WriteString(fmt.Sprintf("%s: %s\n", k, v))
				written[k] = true
			}
		}
		// 输出其他元数据
		for k, v := range r.metadata {
			if !written[k] {
				content.WriteString(fmt.Sprintf("%s: %s\n", k, v))
			}
		}
	}

	// 写入日志条目
	if len(r.entries) > 0 {
		content.WriteString("\n--- Log Entries ---\n")
		for i, entry := range r.entries {
			content.WriteString(fmt.Sprintf("[%d] %s\n", i+1, entry))
		}
	}

	// 写入文本段落（按固定顺序）
	sectionOrder := []string{
		"OriginalHeader",         // 原始访问请求 header
		"UpstreamRequestHeaders", // 实际发给上游的 header
		"OriginalRequest",        // 原始访问请求
		"TransformedRequest",     // 转换器转换后的请求
		"UpstreamResponseHeaders",
		"UpstreamError",
		"UpstreamResponseBody",
		"UpstreamResponseRaw", // 上游返回原始数据（文本形式，如果是二进制则在 rawSections）
		"TransformedResponse", // 转换器转换后的响应（SSE）
	}
	writtenSections := make(map[string]bool)
	for _, name := range sectionOrder {
		if sectionContent, ok := r.sections[name]; ok {
			r.writeSection(&content, name, sectionContent)
			writtenSections[name] = true
		}
	}
	// 输出其他段落
	for name, sectionContent := range r.sections {
		if !writtenSections[name] {
			r.writeSection(&content, name, sectionContent)
		}
	}

	// 写入二进制段落（base64 编码）
	rawSectionOrder := []string{
		"UpstreamResponseRaw", // 上游返回原始数据（二进制，base64）
	}
	writtenRawSections := make(map[string]bool)
	for _, name := range rawSectionOrder {
		if data, ok := r.rawSections[name]; ok && len(data) > 0 {
			r.writeRawSection(&content, name, data)
			writtenRawSections[name] = true
		}
	}
	// 输出其他二进制段落
	for name, data := range r.rawSections {
		if !writtenRawSections[name] && len(data) > 0 {
			r.writeRawSection(&content, name, data)
		}
	}

	return content.String()
}

// FormatHeaderForDebug 将 header 转成稳定文本，避免调试文件泄漏认证信息。
func FormatHeaderForDebug(headers http.Header) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var content strings.Builder
	for _, key := range keys {
		values := headers.Values(key)
		if len(values) == 0 {
			content.WriteString(fmt.Sprintf("%s:\n", key))
			continue
		}
		for _, value := range values {
			content.WriteString(fmt.Sprintf("%s: %s\n", key, maskHeaderValueForDebug(key, value)))
		}
	}
	return strings.TrimRight(content.String(), "\n")
}

func maskHeaderValueForDebug(key, value string) string {
	if !isSensitiveHeader(key) {
		return value
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "***"
}

func isSensitiveHeader(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization",
		"x-api-key",
		"api-key",
		"openai-api-key",
		"anthropic-auth-token",
		"cookie",
		"set-cookie",
		"session",
		"session_id",
		"refresh-token",
		"refresh_token",
		"access-token",
		"access_token":
		return true
	default:
		return false
	}
}

func (r *RequestDebugLogger) writeSection(content *strings.Builder, name, sectionContent string) {
	content.WriteString(fmt.Sprintf("\n--- %s ---\n", name))
	content.WriteString(sectionContent)
	if !strings.HasSuffix(sectionContent, "\n") {
		content.WriteString("\n")
	}
}

func (r *RequestDebugLogger) writeRawSection(content *strings.Builder, name string, data []byte) {
	content.WriteString(fmt.Sprintf("\n--- %s (base64, %d bytes) ---\n", name, len(data)))
	encoded := base64.StdEncoding.EncodeToString(data)
	// 每 76 字符换行，便于阅读
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		content.WriteString(encoded[i:end])
		content.WriteString("\n")
	}
}

// IsDebugFileModeEnabled 检查是否启用了文件调试模式（便捷函数）
func IsDebugFileModeEnabled() bool {
	logger := GetDebugFileLogger()
	return logger != nil && logger.IsEnabled()
}

// sanitizeFilename 清理文件名中的特殊字符，保留字母、数字、下划线和连字符
func sanitizeFilename(s string) string {
	if s == "" {
		return ""
	}
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
