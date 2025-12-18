// Package logger provides logging utilities for the application.
// Requirements: 5.3
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	// LevelDebug is for debug messages
	LevelDebug LogLevel = iota
	// LevelInfo is for informational messages
	LevelInfo
	// LevelWarn is for warning messages
	LevelWarn
	// LevelError is for error messages
	LevelError
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger provides structured logging for the application
// Requirements: 5.3
type Logger struct {
	mu      sync.RWMutex
	level   LogLevel
	prefix  string
	output  io.Writer
	logger  *log.Logger
	fileOut *os.File
}

// defaultLogger is the package-level default logger
var defaultLogger *Logger
var once sync.Once

// init initializes the default logger
func init() {
	defaultLogger = New("[AI-API-Proxy] ", LevelInfo, os.Stdout)
}

// New creates a new Logger instance
func New(prefix string, level LogLevel, output io.Writer) *Logger {
	l := &Logger{
		level:  level,
		prefix: prefix,
		output: output,
		logger: log.New(output, prefix, log.Ldate|log.Ltime|log.Lshortfile),
	}
	return l
}

// NewWithFile creates a new Logger that writes to both stdout and a file
// Requirements: 5.3
func NewWithFile(prefix string, level LogLevel, logDir string) (*Logger, error) {
	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file with date-based name
	logFileName := fmt.Sprintf("ai-api-proxy-%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(logDir, logFileName)

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Create multi-writer for both stdout and file
	multiWriter := io.MultiWriter(os.Stdout, file)

	l := &Logger{
		level:   level,
		prefix:  prefix,
		output:  multiWriter,
		logger:  log.New(multiWriter, prefix, log.Ldate|log.Ltime|log.Lshortfile),
		fileOut: file,
	}

	return l, nil
}

// Close closes the logger and any associated file handles
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileOut != nil {
		return l.fileOut.Close()
	}
	return nil
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current log level
func (l *Logger) GetLevel() LogLevel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SetOutput sets the output writer
func (l *Logger) SetOutput(output io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = output
	l.logger.SetOutput(output)
}

// log writes a log message at the specified level
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	l.mu.RLock()
	currentLevel := l.level
	l.mu.RUnlock()

	if level < currentLevel {
		return
	}

	msg := fmt.Sprintf(format, args...)
	l.logger.Output(3, fmt.Sprintf("[%s] %s", level.String(), msg))
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Printf logs a message using Printf format (for compatibility)
func (l *Logger) Printf(format string, args ...interface{}) {
	l.Info(format, args...)
}

// Println logs a message using Println format (for compatibility)
func (l *Logger) Println(args ...interface{}) {
	l.Info("%s", fmt.Sprint(args...))
}

// Fatalf logs a fatal error and exits
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.Error(format, args...)
	os.Exit(1)
}

// Package-level functions that use the default logger

// SetDefaultLevel sets the log level for the default logger
func SetDefaultLevel(level LogLevel) {
	defaultLogger.SetLevel(level)
}

// SetDefaultOutput sets the output for the default logger
func SetDefaultOutput(output io.Writer) {
	defaultLogger.SetOutput(output)
}

// Debug logs a debug message using the default logger
func Debug(format string, args ...interface{}) {
	defaultLogger.Debug(format, args...)
}

// Info logs an informational message using the default logger
func Info(format string, args ...interface{}) {
	defaultLogger.Info(format, args...)
}

// Warn logs a warning message using the default logger
func Warn(format string, args ...interface{}) {
	defaultLogger.Warn(format, args...)
}

// Error logs an error message using the default logger
func Error(format string, args ...interface{}) {
	defaultLogger.Error(format, args...)
}

// Printf logs a message using the default logger
func Printf(format string, args ...interface{}) {
	defaultLogger.Printf(format, args...)
}

// Println logs a message using the default logger
func Println(args ...interface{}) {
	defaultLogger.Println(args...)
}

// Fatalf logs a fatal error and exits using the default logger
func Fatalf(format string, args ...interface{}) {
	defaultLogger.Fatalf(format, args...)
}

// GetDefault returns the default logger instance
func GetDefault() *Logger {
	return defaultLogger
}

// InitDefault initializes the default logger with custom settings
// Requirements: 5.3
func InitDefault(prefix string, level LogLevel, logDir string) error {
	var err error
	once.Do(func() {
		if logDir != "" {
			defaultLogger, err = NewWithFile(prefix, level, logDir)
		} else {
			defaultLogger = New(prefix, level, os.Stdout)
		}
	})
	return err
}

// RequestLogger provides request-specific logging
// Requirements: 7.1
type RequestLogger struct {
	logger    *Logger
	requestID string
}

// NewRequestLogger creates a new request-specific logger
func NewRequestLogger(requestID string) *RequestLogger {
	return &RequestLogger{
		logger:    defaultLogger,
		requestID: requestID,
	}
}

// Debug logs a debug message with request context
func (r *RequestLogger) Debug(format string, args ...interface{}) {
	r.logger.Debug("[%s] "+format, append([]interface{}{r.requestID}, args...)...)
}

// Info logs an info message with request context
func (r *RequestLogger) Info(format string, args ...interface{}) {
	r.logger.Info("[%s] "+format, append([]interface{}{r.requestID}, args...)...)
}

// Warn logs a warning message with request context
func (r *RequestLogger) Warn(format string, args ...interface{}) {
	r.logger.Warn("[%s] "+format, append([]interface{}{r.requestID}, args...)...)
}

// Error logs an error message with request context
func (r *RequestLogger) Error(format string, args ...interface{}) {
	r.logger.Error("[%s] "+format, append([]interface{}{r.requestID}, args...)...)
}
