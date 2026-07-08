package logger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"github.com/trongnghiango/claude-proxy/internal/utils"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logDateFormat defines date format for log filenames.
const logDateFormat = "2006-01-02"

// getLogPath returns path for a log file with base name and current date.
func getLogPath(base string) string {
	date := time.Now().Format(logDateFormat)
	return fmt.Sprintf("logs/%s-%s.log", base, date)
}

type logTarget struct {
	file   *os.File
	writer *bufio.Writer
}

type asyncLogger struct {
	mu        sync.Mutex
	closeOnce sync.Once
	info      *logTarget
	err       *logTarget
	debug     *logTarget
	json      *logTarget
	ch        chan []byte
	quit      chan struct{}
	wg        sync.WaitGroup
	total     atomic.Uint64 // total messages processed
	dropped   atomic.Uint64 // messages dropped when channel full
	errors    atomic.Uint64 // error messages processed
}

var loggerInstance *asyncLogger

// default buffer size; can be overridden by LOG_BUF_SIZE env var.
var logBufSize = 5000 // number of messages buffered before blocking

// Structured logger wrapper provides Infof, Errorf, Debugf methods.
var logger = &structuredLogger{}

// Infof writes log message if info logging is enabled.
func Infof(format string, a ...interface{}) {
	logger.Infof(format, a...)
}

// Errorf writes error log message.
func Errorf(format string, a ...interface{}) {
	logger.Errorf(format, a...)
}

// Debugf writes log message if debug logging is enabled.
func Debugf(format string, a ...interface{}) {
	logger.Debugf(format, a...)
}

// CloseLogger flushes buffers and closes files – call from main via defer.
func CloseLogger() {
	if loggerInstance != nil {
		loggerInstance.Close()
	}
}

// structuredLogger provides level-aware logging methods.
type structuredLogger struct{}

func (sl *structuredLogger) Infof(format string, a ...interface{}) {
	if !logInfoEnabled {
		return
	}
	sl.write("INFO", format, a...)
}

func (sl *structuredLogger) Errorf(format string, a ...interface{}) {
	sl.write("ERROR", format, a...)
}

func (sl *structuredLogger) Debugf(format string, a ...interface{}) {
	if !logDebugEnabled {
		return
	}
	sl.write("DEBUG", format, a...)
}

func (sl *structuredLogger) write(level, format string, a ...interface{}) {
	// Build timestamp, level, and formatted message.
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("%s [%s] %s\n", ts, level, fmt.Sprintf(format, a...))
	// Forward to async logger if initialized, else fallback to standard log.
	if loggerInstance != nil {
		_, _ = loggerInstance.Write([]byte(msg))
	} else {
		fmt.Fprintf(os.Stderr, "%s [%s] %s\n", ts, level, fmt.Sprintf(format, a...))
	}
}

// compiled detection patterns for error classification – pre‑computed as byte slices for speed.
var errorPatterns = [][]byte{[]byte("Error"), []byte("Exception"), []byte("[Lỗi"), []byte("[Router Exception]")}

// logInfoEnabled controls whether info logs are written. Set via LOG_INFO=0 to disable.
var logInfoEnabled bool
var logDebugEnabled bool
var logPayloadsEnabled bool
var redactSensitivePayloads bool
var payloadLogRetentionDays int = 7
var payloadsDir = "logs/payloads"

func initLogConfig() {
	// Toggle info logs via LOG_INFO env var (default enabled).
	if v := utils.Bool("LOG_INFO", true); !v {
		logInfoEnabled = false
	} else {
		logInfoEnabled = true
	}
	// Toggle debug logs via LOG_DEBUG env var (default disabled).
	if utils.Bool("LOG_DEBUG", false) {
		logDebugEnabled = true
	} else {
		logDebugEnabled = false
	}
	// Toggle payload logging via LOG_PAYLOADS env var (default matches logDebugEnabled).
	if v, ok := os.LookupEnv("LOG_PAYLOADS"); ok {
        if utils.Bool("LOG_PAYLOADS", false) {
            logPayloadsEnabled = true
        } else {
            logPayloadsEnabled = false
        }
    } else {
        logPayloadsEnabled = logDebugEnabled
    }
		logPayloadsEnabled = true
	} else if v == "0" || strings.EqualFold(v, "false") {
		logPayloadsEnabled = false
	} else {
		logPayloadsEnabled = logDebugEnabled
	}
	// Toggle payload redaction via REDACT_SENSITIVE_PAYLOADS env var (default enabled).
	if v := os.Getenv("REDACT_SENSITIVE_PAYLOADS"); v == "0" || strings.EqualFold(v, "false") {
		redactSensitivePayloads = false
	} else {
		redactSensitivePayloads = true
	}
	// Payload retention days via PAYLOAD_LOG_RETENTION_DAYS env var (default 7).
	if v := os.Getenv("PAYLOAD_LOG_RETENTION_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days >= 0 {
			payloadLogRetentionDays = days
		}
	}
	// Buffer size via LOG_BUF_SIZE env var (default 5000).
	if v := os.Getenv("LOG_BUF_SIZE"); v != "" {
		if sz, err := strconv.Atoi(v); err == nil && sz > 0 {
			logBufSize = sz
		}
	}
}

func isErrorMsg(p []byte) bool {
	// Simple byte‑slice search; no string allocation.
	for _, pat := range errorPatterns {
		if bytes.Contains(p, pat) {
			return true
		}
	}
	return false
}

// isDebugMsg detects debug log messages based on level prefix.
func isDebugMsg(p []byte) bool {
	// Simple detection: look for "[DEBUG]" marker in the log entry.
	return bytes.Contains(p, []byte("[DEBUG]"))
}

type jsonLogEntry struct {
	Time      string `json:"time"`
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	RequestID string `json:"request_id,omitempty"`
}

func formatJSONLine(p []byte) []byte {
	raw := strings.TrimRight(string(p), "\n")
	ts := ""
	level := "INFO"
	msg := raw
	if len(raw) >= 19 {
		ts = raw[:19]
		rest := strings.TrimSpace(raw[19:])
		if strings.HasPrefix(rest, "[INFO]") {
			level = "INFO"
			msg = strings.TrimSpace(rest[6:])
		} else if strings.HasPrefix(rest, "[ERROR]") {
			level = "ERROR"
			msg = strings.TrimSpace(rest[7:])
		} else if strings.HasPrefix(rest, "[DEBUG]") {
			level = "DEBUG"
			msg = strings.TrimSpace(rest[7:])
		}
	}

	// Extract request_id from "req=<id>" pattern
	reqID := ""
	if idx := strings.Index(msg, "req="); idx >= 0 {
		end := strings.IndexByte(msg[idx+4:], ' ')
		if end < 0 {
			reqID = msg[idx+4:]
		} else {
			reqID = msg[idx+4 : idx+4+end]
		}
	}

	entry := jsonLogEntry{
		Time:      ts,
		Level:     level,
		Msg:       msg,
		RequestID: reqID,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return append(p, '\n') // fallback to text line on marshal failure
	}
	return append(data, '\n')
}

// Write implements io.Writer – enqueues the message for asynchronous processing.
func (l *asyncLogger) Write(p []byte) (int, error) {
	l.total.Add(1)
	msg := make([]byte, len(p))
	copy(msg, p)
	select {
	case l.ch <- msg:
		// enqueued successfully.
	default:
		l.dropped.Add(1)
		l.mu.Lock()
		if isErrorMsg(msg) {
			_, _ = l.err.writer.Write(msg)
			_ = l.err.writer.Flush()
		} else if isDebugMsg(msg) {
			_, _ = l.debug.writer.Write(msg)
			_ = l.debug.writer.Flush()
		} else {
			_, _ = l.info.writer.Write(msg)
			_ = l.info.writer.Flush()
		}
		if l.json != nil && !isDebugMsg(msg) {
			_, _ = l.json.writer.Write(formatJSONLine(msg))
			_ = l.json.writer.Flush()
		}
		l.mu.Unlock()
	}
	return len(p), nil
}

func (l *asyncLogger) run() {
	defer l.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case msg := <-l.ch:
			l.mu.Lock()
			if isErrorMsg(msg) {
				_, _ = l.err.writer.Write(msg)
			} else if isDebugMsg(msg) {
				_, _ = l.debug.writer.Write(msg)
			} else {
				_, _ = l.info.writer.Write(msg)
			}
			if l.json != nil && !isDebugMsg(msg) {
				_, _ = l.json.writer.Write(formatJSONLine(msg))
			}
			l.mu.Unlock()
		case <-ticker.C:
			l.mu.Lock()
			_ = l.info.writer.Flush()
			_ = l.err.writer.Flush()
			_ = l.debug.writer.Flush()
			if l.json != nil {
				_ = l.json.writer.Flush()
			}
			l.mu.Unlock()
		case <-l.quit:
			for {
				select {
				case msg := <-l.ch:
					l.mu.Lock()
					if isErrorMsg(msg) {
						_, _ = l.err.writer.Write(msg)
					} else if isDebugMsg(msg) {
						_, _ = l.debug.writer.Write(msg)
					} else {
						_, _ = l.info.writer.Write(msg)
					}
					if l.json != nil && !isDebugMsg(msg) {
						_, _ = l.json.writer.Write(formatJSONLine(msg))
					}
					l.mu.Unlock()
				default:
					l.mu.Lock()
					_ = l.info.writer.Flush()
					_ = l.err.writer.Flush()
					_ = l.debug.writer.Flush()
					if l.json != nil {
						_ = l.json.writer.Flush()
						_ = l.json.file.Close()
					}
					_ = l.info.file.Close()
					_ = l.err.file.Close()
					_ = l.debug.file.Close()
					l.mu.Unlock()
					return
				}
			}
		}
	}
}

func (l *asyncLogger) Close() {
	l.closeOnce.Do(func() {
		close(l.quit)
		l.wg.Wait()
	})
}

func newAsyncLogger(infoPath, errPath, debugPath string) (*asyncLogger, error) {
	infoFile, err := os.OpenFile(infoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	errFile, err := os.OpenFile(errPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		_ = infoFile.Close()
		return nil, err
	}
	debugFile, err := os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		_ = infoFile.Close()
		_ = errFile.Close()
		return nil, err
	}

	jsonPath := os.Getenv("LOG_JSON_PATH")
	if jsonPath == "" {
		if flag.Lookup("test.v") == nil {
			date := time.Now().Format(logDateFormat)
			jsonPath = fmt.Sprintf("logs/json/info-%s.jsonl", date)
		}
	}
	var jsonTarget *logTarget
	if jsonPath != "" {
		jsonFile, err := os.OpenFile(jsonPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			jsonTarget = &logTarget{file: jsonFile, writer: bufio.NewWriter(jsonFile)}
		}
	}

	l := &asyncLogger{
		info:  &logTarget{file: infoFile, writer: bufio.NewWriter(infoFile)},
		err:   &logTarget{file: errFile, writer: bufio.NewWriter(errFile)},
		debug: &logTarget{file: debugFile, writer: bufio.NewWriter(debugFile)},
		json:  jsonTarget,
		ch:    make(chan []byte, logBufSize),
		quit:  make(chan struct{}),
	}
	l.wg.Add(1)
	go l.run()
	return l, nil
}

func init() {
	initLogConfig()

	// Only initialize logging if not running package tests, or if explicitly requested.
	if flag.Lookup("test.v") == nil {
		if err := os.MkdirAll("logs", 0755); err != nil {
			log.Fatalf("Failed to create logs directory: %v", err)
		}
		if err := os.MkdirAll("logs/json", 0755); err != nil {
			log.Fatalf("Failed to create logs/json directory: %v", err)
		}
		if err := os.MkdirAll("logs/payloads", 0755); err != nil {
			log.Fatalf("Failed to create logs/payloads directory: %v", err)
		}
		var err error
		infoPath := os.Getenv("LOG_INFO_PATH")
		if infoPath == "" {
			infoPath = getLogPath("info")
		}
		errPath := os.Getenv("LOG_ERROR_PATH")
		if errPath == "" {
			errPath = getLogPath("error")
		}
		debugPath := os.Getenv("LOG_DEBUG_PATH")
		if debugPath == "" {
			debugPath = getLogPath("debug")
		}
		loggerInstance, err = newAsyncLogger(infoPath, errPath, debugPath)
		if err != nil {
			log.Fatalf("Failed to initialise logger: %v", err)
		}
		log.SetOutput(loggerInstance)
		log.SetFlags(0)
	}
}

// SetupTestLogger initializes a temporary logger in the given TempDir for package testing.
// It returns the path to the info log.
func SetupTestLogger(t testing.TB) string {
	t.Helper()
	initLogConfig()
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "info.log")
	errPath := filepath.Join(dir, "error.log")
	debugPath := filepath.Join(dir, "debug.log")
	payloadsDir = filepath.Join(dir, "payloads")
	if err := os.MkdirAll(payloadsDir, 0755); err != nil {
		t.Fatalf("Failed to create test payloads directory: %v", err)
	}
	l, err := newAsyncLogger(infoPath, errPath, debugPath)
	if err != nil {
		t.Fatalf("newAsyncLogger failed: %v", err)
	}
	loggerInstance = l
	t.Cleanup(func() {
		if loggerInstance != nil {
			loggerInstance.Close()
			loggerInstance = nil
		}
	})
	return infoPath
}

// SetRedactSensitivePayloads sets whether to redact sensitive fields in logged payloads.
func SetRedactSensitivePayloads(enable bool) {
	redactSensitivePayloads = enable
}

// SetPayloadLogRetentionDays sets the number of days to retain payload logs.
func SetPayloadLogRetentionDays(days int) {
	if days >= 0 {
		payloadLogRetentionDays = days
	}
}

// GetPayloadLogRetentionDays returns the configured retention days.
func GetPayloadLogRetentionDays() int {
	return payloadLogRetentionDays
}

// CleanOldPayloadLogs deletes payload log files that are older than payloadLogRetentionDays.
func CleanOldPayloadLogs() {
	if payloadLogRetentionDays <= 0 {
		return
	}
	files, err := os.ReadDir(payloadsDir)
	if err != nil {
		// If directory doesn't exist, nothing to clean
		if os.IsNotExist(err) {
			return
		}
		Errorf("[Logger] Failed to read payloads directory for cleanup: %v", err)
		return
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(payloadLogRetentionDays) * 24 * time.Hour)
	deletedCount := 0

	for _, entry := range files {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "req-") || !strings.HasSuffix(name, ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(payloadsDir, name)
			if err := os.Remove(path); err != nil {
				Errorf("[Logger] Failed to delete old payload log %s: %v", name, err)
			} else {
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		Infof("[Logger] Đã xóa %d file log payload hết hạn (nhiều hơn %d ngày)", deletedCount, payloadLogRetentionDays)
	}
}

// StartPayloadLogCleanupWorker starts a background worker that runs CleanOldPayloadLogs periodically.
// The task runs immediately on start, and then every interval.
func StartPayloadLogCleanupWorker(interval time.Duration) chan struct{} {
	stopChan := make(chan struct{})
	go func() {
		// Run initial cleanup
		CleanOldPayloadLogs()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				CleanOldPayloadLogs()
			case <-stopChan:
				return
			}
		}
	}()
	return stopChan
}

func redactValue(val interface{}) interface{} {
	switch v := val.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{}, len(v))
		for k, val := range v {
			lowerK := strings.ToLower(k)
			if strings.Contains(lowerK, "api_key") ||
				strings.Contains(lowerK, "apikey") ||
				strings.Contains(lowerK, "api-key") ||
				strings.Contains(lowerK, "token") ||
				strings.Contains(lowerK, "secret") ||
				strings.Contains(lowerK, "password") ||
				strings.Contains(lowerK, "authorization") ||
				strings.Contains(lowerK, "auth") {
				newMap[k] = "[REDACTED]"
			} else {
				newMap[k] = redactValue(val)
			}
		}
		return newMap
	case []interface{}:
		newArr := make([]interface{}, len(v))
		for i, val := range v {
			newArr[i] = redactValue(val)
		}
		return newArr
	case string:
		lowerV := strings.ToLower(v)
		if strings.HasPrefix(v, "sk-") || strings.HasPrefix(lowerV, "bearer ") {
			return "[REDACTED]"
		}
		return v
	default:
		return v
	}
}

// RedactPayload parses the payload, redacts sensitive keys and values, and returns the serialized JSON.
func RedactPayload(payload []byte) []byte {
	var data interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return payload
	}
	redacted := redactValue(data)
	redactedBytes, err := json.Marshal(redacted)
	if err != nil {
		return payload
	}
	return redactedBytes
}

// LogPayload writes the pretty-printed JSON payload to a dedicated file named by trace ID.
// This is executed asynchronously to prevent blocking the request handling path.
func LogPayload(reqID string, payload []byte) {
	if !logPayloadsEnabled || len(payload) == 0 || reqID == "" {
		return
	}
	go func() {
		targetPayload := payload
		if redactSensitivePayloads {
			targetPayload = RedactPayload(payload)
		}

		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, targetPayload, "", "  "); err != nil {
			// If it's not valid JSON, we fallback to writing raw bytes.
			prettyJSON.Reset()
			prettyJSON.Write(targetPayload)
		}

		path := filepath.Join(payloadsDir, fmt.Sprintf("req-%s.json", reqID))
		if err := os.WriteFile(path, prettyJSON.Bytes(), 0644); err != nil {
			Errorf("[Logger] Failed to write payload log for request %s: %v", reqID, err)
		}
	}()
}

// GetPayloadsDir returns the current directory used for logging payloads.
func GetPayloadsDir() string {
	return payloadsDir
}
