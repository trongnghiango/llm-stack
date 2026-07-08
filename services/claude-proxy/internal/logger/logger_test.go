package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper creates temporary logger pointing to files in a temp dir.
func setupTempLogger(t *testing.T) (*asyncLogger, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "info.log")
	errPath := filepath.Join(dir, "error.log")
	debugPath := filepath.Join(dir, "debug.log")
	l, err := newAsyncLogger(infoPath, errPath, debugPath)
	if err != nil {
		t.Fatalf("newAsyncLogger failed: %v", err)
	}
	return l, infoPath, errPath, debugPath
}

func TestInfoLoggingDisabled(t *testing.T) {
	t.Setenv("LOG_INFO", "0")
	t.Setenv("LOG_DEBUG", "0")
	initLogConfig()
	l, infoPath, _, _ := setupTempLogger(t)
	// Override global logger instance for the test.
	loggerInstance = l
	defer func() {
		loggerInstance.Close()
		loggerInstance = nil
	}()

	logger.Infof("should not appear")
	// Close to flush and stop background goroutine.
	loggerInstance.Close()
	data, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read info log: %v", err)
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Fatalf("expected empty info log, got: %q", string(data))
	}
}

func TestInfoLoggingEnabled(t *testing.T) {
	t.Setenv("LOG_INFO", "1")
	t.Setenv("LOG_DEBUG", "0")
	initLogConfig()
	l, infoPath, _, _ := setupTempLogger(t)
	loggerInstance = l
	defer func() {
		loggerInstance.Close()
		loggerInstance = nil
	}()

	logger.Infof("info message test")
	loggerInstance.Close()
	data, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read info log: %v", err)
	}
	if !strings.Contains(string(data), "[INFO]") {
		t.Fatalf("info log does not contain INFO tag: %q", string(data))
	}
	if !strings.Contains(string(data), "info message test") {
		t.Fatalf("info log does not contain message: %q", string(data))
	}
}

func TestDebugLoggingEnabled(t *testing.T) {
	t.Setenv("LOG_INFO", "1") // info needed for debug path
	t.Setenv("LOG_DEBUG", "1")
	initLogConfig()
	l, _, _, debugPath := setupTempLogger(t)
	loggerInstance = l
	defer func() {
		loggerInstance.Close()
		loggerInstance = nil
	}()

	logger.Debugf("debug message test")
	loggerInstance.Close()
	data, err := os.ReadFile(debugPath)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	if !strings.Contains(string(data), "[DEBUG]") {
		t.Fatalf("debug log does not contain DEBUG tag: %q", string(data))
	}
	if !strings.Contains(string(data), "debug message test") {
		t.Fatalf("debug log does not contain debug message: %q", string(data))
	}
}

func TestJSONLogging(t *testing.T) {
	t.Setenv("LOG_INFO", "1")
	t.Setenv("LOG_DEBUG", "0")
	initLogConfig()

	dir := t.TempDir()
	infoPath := filepath.Join(dir, "info.log")
	errPath := filepath.Join(dir, "error.log")
	debugPath := filepath.Join(dir, "debug.log")
	jsonPath := filepath.Join(dir, "info.jsonl")

	t.Setenv("LOG_JSON_PATH", jsonPath)
	l, err := newAsyncLogger(infoPath, errPath, debugPath)
	if err != nil {
		t.Fatalf("newAsyncLogger failed: %v", err)
	}

	loggerInstance = l
	defer func() {
		loggerInstance.Close()
		loggerInstance = nil
	}()

	logger.Infof("JSON logger test message req=req123")
	loggerInstance.Close()

	// Verify JSON file contains valid JSON
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "JSON logger test message") {
		t.Fatalf("JSON log does not contain message: %q", content)
	}
	if !strings.Contains(content, "req123") {
		t.Fatalf("JSON log does not contain request ID: %q", content)
	}
	if !strings.Contains(content, `"level":"INFO"`) {
		t.Fatalf("JSON log does not contain correct level: %q", content)
	}
}

func TestCloseLogger(t *testing.T) {
	origLogger := loggerInstance
	defer func() { loggerInstance = origLogger }()

	dir := t.TempDir()
	l, err := newAsyncLogger(filepath.Join(dir, "i.log"), filepath.Join(dir, "e.log"), filepath.Join(dir, "d.log"))
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	loggerInstance = l
	CloseLogger()
}

func TestLogger_WriteFallback(t *testing.T) {
	origBufSize := logBufSize
	logBufSize = 1
	defer func() { logBufSize = origBufSize }()

	dir := t.TempDir()
	infoPath := filepath.Join(dir, "info.log")
	errPath := filepath.Join(dir, "error.log")
	debugPath := filepath.Join(dir, "debug.log")

	l, err := newAsyncLogger(infoPath, errPath, debugPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer l.Close()

	// Flood the logger to trigger non-blocking channel drop fallback
	for i := 0; i < 50; i++ {
		l.Write([]byte(fmt.Sprintf("message %d [INFO] req=r123\n", i)))
		l.Write([]byte(fmt.Sprintf("error %d [ERROR]\n", i)))
		l.Write([]byte(fmt.Sprintf("debug %d [DEBUG]\n", i)))
	}
}

func TestFormatJSONLine(t *testing.T) {
	cases := []struct {
		input  string
		expect string
	}{
		{
			input:  "2026-07-06 15:04:05 [INFO] Hello req=123\n",
			expect: `"level":"INFO"`,
		},
		{
			input:  "2026-07-06 15:04:05 [ERROR] Bad error\n",
			expect: `"level":"ERROR"`,
		},
		{
			input:  "2026-07-06 15:04:05 [DEBUG] Debug trace\n",
			expect: `"level":"DEBUG"`,
		},
		{
			input:  "short log\n",
			expect: `"level":"INFO"`,
		},
	}
	for _, tc := range cases {
		out := formatJSONLine([]byte(tc.input))
		if !strings.Contains(string(out), tc.expect) {
			t.Errorf("expected %q to contain %q, got %q", string(out), tc.expect, string(out))
		}
	}
}

func TestLogPayload(t *testing.T) {
	// Backup original values
	origEnabled := logPayloadsEnabled
	origDir := payloadsDir
	defer func() {
		logPayloadsEnabled = origEnabled
		payloadsDir = origDir
	}()

	tempDir := t.TempDir()
	payloadsDir = tempDir

	// Case 1: Disabled
	logPayloadsEnabled = false
	LogPayload("req1", []byte(`{"hello":"world"}`))
	// Wait a tiny bit (goroutine execution)
	time.Sleep(10 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(tempDir, "req-req1.json")); !os.IsNotExist(err) {
		t.Fatalf("expected file to not exist when disabled")
	}

	// Case 2: Enabled, Valid JSON
	logPayloadsEnabled = true
	testJSON := []byte(`{"a":1,"b":"hello"}`)
	LogPayload("req2", testJSON)

	// Wait for goroutine to finish writing
	var data []byte
	var readErr error
	filePath := filepath.Join(tempDir, "req-req2.json")
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Millisecond)
		data, readErr = os.ReadFile(filePath)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatalf("failed to read payload log: %v", readErr)
	}

	// Verify it is pretty-printed (contains newlines and indentation)
	content := string(data)
	if !strings.Contains(content, "\n") || !strings.Contains(content, "  \"a\": 1") {
		t.Fatalf("expected pretty-printed JSON, got: %q", content)
	}

	// Case 3: Enabled, Invalid JSON
	LogPayload("req3", []byte(`not-json`))
	filePath3 := filepath.Join(tempDir, "req-req3.json")
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Millisecond)
		data, readErr = os.ReadFile(filePath3)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatalf("failed to read invalid json payload log: %v", readErr)
	}
	if string(data) != "not-json" {
		t.Fatalf("expected raw bytes for invalid json, got %q", string(data))
	}

	// Case 4: Redaction Enabled
	origRedact := redactSensitivePayloads
	defer func() {
		redactSensitivePayloads = origRedact
	}()
	redactSensitivePayloads = true
	LogPayload("req4", []byte(`{"api_key":"sk-999","content":"hello"}`))
	filePath4 := filepath.Join(tempDir, "req-req4.json")
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Millisecond)
		data, readErr = os.ReadFile(filePath4)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatalf("failed to read redacted payload log: %v", readErr)
	}
	if !strings.Contains(string(data), `"[REDACTED]"`) || strings.Contains(string(data), `"sk-999"`) {
		t.Fatalf("expected redacted api_key, got: %q", string(data))
	}
}

func TestRedactPayload(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no sensitive fields",
			input:    `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`,
			expected: `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name:     "contains sensitive keys",
			input:    `{"api_key":"sk-12345","password":"my-secret-pass","normal_field":"value"}`,
			expected: `{"api_key":"[REDACTED]","normal_field":"value","password":"[REDACTED]"}`,
		},
		{
			name:     "contains nested sensitive keys",
			input:    `{"nested":{"api-key":"sk-123","token":"abc-123"},"list":[{"auth":"bearer xyz"}]}`,
			expected: `{"list":[{"auth":"[REDACTED]"}],"nested":{"api-key":"[REDACTED]","token":"[REDACTED]"}}`,
		},
		{
			name:     "contains sk- values outside sensitive keys",
			input:    `{"model":"gpt-4","custom_api":"sk-abcdefghijklmn","auth_hdr":"Bearer test-token"}`,
			expected: `{"auth_hdr":"[REDACTED]","custom_api":"[REDACTED]","model":"gpt-4"}`,
		},
		{
			name:     "invalid json",
			input:    `invalid-json-payload`,
			expected: `invalid-json-payload`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := RedactPayload([]byte(tt.input))
			var outMap, expMap interface{}
			if err := json.Unmarshal(output, &outMap); err != nil {
				// If it's invalid json, just assert string equality
				if string(output) != tt.expected {
					t.Errorf("expected %q, got %q", tt.expected, string(output))
				}
				return
			}
			if err := json.Unmarshal([]byte(tt.expected), &expMap); err != nil {
				t.Fatalf("failed to unmarshal expected: %v", err)
			}
			outJSON, _ := json.Marshal(outMap)
			expJSON, _ := json.Marshal(expMap)
			if string(outJSON) != string(expJSON) {
				t.Errorf("expected map %s, got %s", string(expJSON), string(outJSON))
			}
		})
	}
}

func TestCleanOldPayloadLogs(t *testing.T) {
	origDir := payloadsDir
	origDays := payloadLogRetentionDays
	defer func() {
		payloadsDir = origDir
		payloadLogRetentionDays = origDays
	}()

	tempDir := t.TempDir()
	payloadsDir = tempDir
	payloadLogRetentionDays = 1 // 1 day retention

	// Create some mock log files
	now := time.Now()

	// 1. A new file (should not be deleted)
	newPath := filepath.Join(tempDir, "req-new.json")
	if err := os.WriteFile(newPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write mock file: %v", err)
	}

	// 2. An old file (should be deleted)
	oldPath := filepath.Join(tempDir, "req-old.json")
	if err := os.WriteFile(oldPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write mock file: %v", err)
	}
	// Artificially change mod time of old file to 2 days ago
	twoDaysAgo := now.Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatalf("failed to change mock file time: %v", err)
	}

	// 3. A file with different prefix/suffix (should not be deleted even if old)
	otherPath := filepath.Join(tempDir, "info.log")
	if err := os.WriteFile(otherPath, []byte("log data"), 0644); err != nil {
		t.Fatalf("failed to write mock file: %v", err)
	}
	if err := os.Chtimes(otherPath, twoDaysAgo, twoDaysAgo); err != nil {
		t.Fatalf("failed to change mock file time: %v", err)
	}

	// Run cleanup
	CleanOldPayloadLogs()

	// Verify
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Errorf("new file was incorrectly deleted")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old file was not deleted")
	}
	if _, err := os.Stat(otherPath); os.IsNotExist(err) {
		t.Errorf("other non-payload file was incorrectly deleted")
	}
}
