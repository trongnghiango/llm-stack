package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type MockRoundTripper struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

type CloseNotifyingRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func NewCloseNotifyingRecorder() *CloseNotifyingRecorder {
	return &CloseNotifyingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closed:           make(chan bool, 1),
	}
}

func (c *CloseNotifyingRecorder) CloseNotify() <-chan bool {
	return c.closed
}

func TestModelNameNormalization(t *testing.T) {
	sm := NewSessionManager(nil)
	acc := CFAccount{
		AccountID: "my_acc_id",
		APIToken:  "my_token",
		IsActive:  true,
	}

	var capturedURL string
	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				capturedURL = req.URL.String()
				// Return a fake 200 response with empty body
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":{"response":"hello"}}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	testCases := []struct {
		inputModel    string
		expectedModel string
	}{
		{"meta/llama-3.1-8b-instruct", "@cf/meta/llama-3.1-8b-instruct"},
		{"@cf/meta/llama-3.1-8b-instruct", "@cf/meta/llama-3.1-8b-instruct"},
		{"cf/meta/llama-3.1-8b-instruct", "@cf/meta/llama-3.1-8b-instruct"},
		{"qwen/qwen2.5-7b-instruct", "@cf/qwen/qwen2.5-7b-instruct"},
	}

	for _, tc := range testCases {
		_, err := handler.forwardToCloudflare(acc, tc.inputModel, OpenAIRequest{
			Model: tc.inputModel,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		expectedURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", acc.AccountID, tc.expectedModel)
		if capturedURL != expectedURL {
			t.Errorf("For input %s, expected URL %s, got %s", tc.inputModel, expectedURL, capturedURL)
		}
	}
}

func TestAccountLifecycleAndPenalization(t *testing.T) {
	sm := NewSessionManager(nil)
	acc1 := &CFAccount{
		AccountID:          "acc1",
		APIToken:           "token1",
		IsActive:           true,
		CurrentNeuronsUsed: 0,
	}
	acc2 := &CFAccount{
		AccountID:          "acc2",
		APIToken:           "token2",
		IsActive:           true,
		CurrentNeuronsUsed: 0,
	}

	sm.pool = []*CFAccount{acc1, acc2}

	// 1. GetAccount should return one of them (Round-Robin)
	gotAcc, ok := sm.GetAccount("session1")
	if !ok {
		t.Fatalf("Expected account to be available")
	}

	// 2. Track usage up to HandoffThreshold
	sm.TrackUsage(gotAcc.AccountID, HandoffThreshold+100)

	// Since we used Lock/Unlock and put it in penalized, check if it's inactive and penalized
	sm.mu.Lock()
	acc := sm.pool[0]
	if acc.AccountID != gotAcc.AccountID {
		acc = sm.pool[1]
	}
	if acc.IsActive {
		t.Errorf("Expected account %s to be inactive", acc.AccountID)
	}
	_, isPenalized := sm.penalized[acc.AccountID]
	if !isPenalized {
		t.Errorf("Expected account %s to be penalized", acc.AccountID)
	}
	sm.mu.Unlock()

	// 3. Mock time forward / manual unlock by editing penalized map in test
	sm.mu.Lock()
	// Set penalization time in the past
	sm.penalized[acc.AccountID] = time.Now().Add(-1 * time.Minute)
	sm.mu.Unlock()

	// 4. Calling GetAccount should trigger unpenalize loop
	_, _ = sm.GetAccount("session2")

	// Check if acc is active again and has neurons reset to 0
	sm.mu.Lock()
	if !acc.IsActive {
		t.Errorf("Expected account %s to be reactivated", acc.AccountID)
	}
	if atomic.LoadInt64(&acc.CurrentNeuronsUsed) != 0 {
		t.Errorf("Expected neurons to be reset to 0, got %d", atomic.LoadInt64(&acc.CurrentNeuronsUsed))
	}
	sm.mu.Unlock()
}

func TestAnthropicStandardResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	sm := NewSessionManager(nil)
	sm.pool = []*CFAccount{
		{
			AccountID: "my_acc_id",
			APIToken:  "my_token",
			IsActive:  true,
		},
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":{"response":"Hello, this is a mock assistant response!"}}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	r.POST("/v1/messages", handler.HandleAnthropicCompletion)

	reqBody := `{"model":"llama-3.1-8b-instruct","messages":[{"role":"user","content":"Hi"}],"stream":false}`
	req, _ := http.NewRequest("POST", "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["type"] != "message" {
		t.Errorf("Expected response type 'message', got %v", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("Expected role 'assistant', got %v", resp["role"])
	}

	content, ok := resp["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("Expected content array in response")
	}

	firstBlock, ok := content[0].(map[string]interface{})
	if !ok || firstBlock["type"] != "text" || firstBlock["text"] != "Hello, this is a mock assistant response!" {
		t.Errorf("Unexpected content block structure: %v", content)
	}
}

func TestAnthropicStreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	sm := NewSessionManager(nil)
	sm.pool = []*CFAccount{
		{
			AccountID: "my_acc_id",
			APIToken:  "my_token",
			IsActive:  true,
		},
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				cfStreamData := "data: {\"response\":\"Hello\"}\n\ndata: {\"response\":\" world\"}\n\ndata: [DONE]\n\n"
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(cfStreamData)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	r.POST("/v1/messages", handler.HandleAnthropicCompletion)

	reqBody := `{"model":"llama-3.1-8b-instruct","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	req, _ := http.NewRequest("POST", "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := NewCloseNotifyingRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "event: message_start") {
		t.Errorf("Expected body to contain message_start event")
	}
	if !strings.Contains(bodyStr, "event: content_block_start") {
		t.Errorf("Expected body to contain content_block_start event")
	}
	if !strings.Contains(bodyStr, "Hello") || !strings.Contains(bodyStr, "world") {
		t.Errorf("Expected body to contain content block deltas")
	}
	if !strings.Contains(bodyStr, "event: content_block_stop") {
		t.Errorf("Expected body to contain content_block_stop event")
	}
	if !strings.Contains(bodyStr, "event: message_delta") {
		t.Errorf("Expected body to contain message_delta event")
	}
	if !strings.Contains(bodyStr, "event: message_stop") {
		t.Errorf("Expected body to contain message_stop event")
	}
}

func TestAnthropicNullFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	sm := NewSessionManager(nil)
	sm.pool = []*CFAccount{
		{
			AccountID: "my_acc_id",
			APIToken:  "my_token",
			IsActive:  true,
		},
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":{"response":"Hi there"}}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	r.POST("/v1/messages", handler.HandleAnthropicCompletion)

	// Send request containing null values
	reqBody := `{"model":"llama-3.1-8b-instruct","messages":[{"role":"user","content":"Hi"}],"system":null,"max_tokens":null,"stream":null}`
	req, _ := http.NewRequest("POST", "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for null fields request, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestAnthropicDuplicateRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	sm := NewSessionManager(nil)
	sm.pool = []*CFAccount{
		{
			AccountID: "my_acc_id",
			APIToken:  "my_token",
			IsActive:  true,
		},
	}

	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":{"response":"Route OK"}}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	// Register duplicate routes like in main.go
	r.POST("/v1/messages", handler.HandleAnthropicCompletion)
	r.POST("/v1/v1/messages", handler.HandleAnthropicCompletion)

	// Request /v1/v1/messages
	reqBody := `{"model":"llama-3.1-8b-instruct","messages":[{"role":"user","content":"Hi"}],"stream":false}`
	req, _ := http.NewRequest("POST", "/v1/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for duplicate route /v1/v1/messages, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestAnthropicSystemArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	sm := NewSessionManager(nil)
	sm.pool = []*CFAccount{
		{
			AccountID: "my_acc_id",
			APIToken:  "my_token",
			IsActive:  true,
		},
	}

	var capturedBody string
	mockClient := &http.Client{
		Transport: &MockRoundTripper{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				bodyBytes, _ := io.ReadAll(req.Body)
				capturedBody = string(bodyBytes)
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":{"response":"OK"}}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	handler := &ProxyHandler{
		sm:     sm,
		client: mockClient,
	}

	r.POST("/v1/messages", handler.HandleAnthropicCompletion)

	// Send request containing system as block array
	reqBody := `{"model":"llama-3.1-8b-instruct","messages":[{"role":"user","content":"Hi"}],"system":[{"type":"text","text":"My custom system instruction."}],"stream":false}`
	req, _ := http.NewRequest("POST", "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for system array request, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Verify that the system instruction was parsed and prepended as system message in capturedBody
	if !strings.Contains(capturedBody, "My custom system instruction.") {
		t.Errorf("Expected captured cloudflare request body to contain the system instruction, got: %s", capturedBody)
	}
}

// ============================================================================
// Tests cho Tool Bash (localToolBash)
// ============================================================================

func TestLocalToolBash_Success(t *testing.T) {
	args := map[string]interface{}{
		"command": "echo hello_bash",
	}
	result, err := localToolBash(args)
	if err != nil {
		t.Fatalf("Không mong lỗi, nhận được: %v", err)
	}
	if !strings.Contains(result, "hello_bash") {
		t.Errorf("Kỳ vọng output chứa 'hello_bash', nhận được: %s", result)
	}
}

func TestLocalToolBash_ExitCodeNonZero(t *testing.T) {
	args := map[string]interface{}{
		"command": "exit 42",
	}
	result, err := localToolBash(args)
	// Không được trả lỗi hard, chỉ báo exit code trong output
	if err != nil {
		t.Fatalf("Bash tool không được trả lỗi hard cho exit code != 0, nhận: %v", err)
	}
	if !strings.Contains(result, "Exit code: 42") {
		t.Errorf("Kỳ vọng output chứa 'Exit code: 42', nhận được: %s", result)
	}
}

func TestLocalToolBash_Timeout(t *testing.T) {
	args := map[string]interface{}{
		"command":  "sleep 10",
		"timeout":  float64(1), // 1 giây
	}
	result, err := localToolBash(args)
	if err != nil {
		t.Fatalf("Không mong lỗi hard khi timeout, nhận: %v", err)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("Kỳ vọng output chứa 'timed out', nhận được: %s", result)
	}
}

func TestLocalToolBash_MissingCommand(t *testing.T) {
	args := map[string]interface{}{}
	_, err := localToolBash(args)
	if err == nil {
		t.Fatal("Kỳ vọng lỗi khi thiếu 'command', nhưng không có lỗi")
	}
}

func TestLocalToolBash_CaptureStderr(t *testing.T) {
	args := map[string]interface{}{
		"command": "echo stderr_msg >&2; exit 1",
	}
	result, err := localToolBash(args)
	if err != nil {
		t.Fatalf("Không mong lỗi hard, nhận: %v", err)
	}
	if !strings.Contains(result, "stderr_msg") {
		t.Errorf("Kỳ vọng output chứa stderr 'stderr_msg', nhận được: %s", result)
	}
}

func TestLocalToolBash_WithCwd(t *testing.T) {
	args := map[string]interface{}{
		"command": "pwd",
		"cwd":     "/tmp",
	}
	result, err := localToolBash(args)
	if err != nil {
		t.Fatalf("Không mong lỗi, nhận: %v", err)
	}
	if !strings.Contains(result, "/tmp") {
		t.Errorf("Kỳ vọng output chứa '/tmp', nhận được: %s", result)
	}
}

func TestParseRawJSONToolCall(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedName  string
		expectedParam string
		expectedText  string
		shouldPass    bool
	}{
		{
			name:          "JSON thô hoàn chỉnh",
			input:         `{"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}`,
			expectedName:  "Read",
			expectedParam: "/path/to/file.md",
			expectedText:  "",
			shouldPass:    true,
		},
		{
			name:          "JSON đi kèm text phía trước",
			input:         `Tôi sẽ đọc file này: {"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}`,
			expectedName:  "Read",
			expectedParam: "/path/to/file.md",
			expectedText:  "Tôi sẽ đọc file này:",
			shouldPass:    true,
		},
		{
			name:          "Không phải JSON tool call",
			input:         `Đây chỉ là một câu nói bình thường.`,
			expectedName:  "",
			expectedParam: "",
			expectedText:  "Đây chỉ là một câu nói bình thường.",
			shouldPass:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, textOut, ok := parseRawJSONToolCall(tc.input)
			if ok != tc.shouldPass {
				t.Fatalf("Kỳ vọng pass=%v, nhận ok=%v", tc.shouldPass, ok)
			}
			if tc.shouldPass {
				if len(res) == 0 {
					t.Fatal("Không nhận được tool calls")
				}
				funcMap := res[0]["function"].(map[string]interface{})
				name := funcMap["name"].(string)
				if name != tc.expectedName {
					t.Errorf("Kỳ vọng tên tool là %s, nhận được: %s", tc.expectedName, name)
				}
				args := funcMap["arguments"].(map[string]interface{})
				filePath := args["file_path"].(string)
				if filePath != tc.expectedParam {
					t.Errorf("Kỳ vọng tham số file_path là %s, nhận được: %s", tc.expectedParam, filePath)
				}
				if textOut != tc.expectedText {
					t.Errorf("Kỳ vọng text outside là '%s', nhận được: '%s'", tc.expectedText, textOut)
				}
			}
		})
	}
}

func TestParseXMLToolCalls(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedName  string
		expectedParam string
		expectedText  string
		shouldPass    bool
	}{
		{
			name:          "Tag tools truyền thống",
			input:         `<tools>{"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}</tools>`,
			expectedName:  "Read",
			expectedParam: "/path/to/file.md",
			expectedText:  "",
			shouldPass:    true,
		},
		{
			name:          "Tag tool_use mới",
			input:         `Tôi sẽ gọi tool này: <tool_use>{"name": "Write", "arguments": {"file_path": "/path/to/new.txt"}}</tool_use>`,
			expectedName:  "Write",
			expectedParam: "/path/to/new.txt",
			expectedText:  "Tôi sẽ gọi tool này:",
			shouldPass:    true,
		},
		{
			name:          "Tag tools bị lỗi hoặc rỗng",
			input:         `<tools></tools>`,
			expectedName:  "",
			expectedParam: "",
			expectedText:  "<tools></tools>",
			shouldPass:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, textOut, ok := parseXMLToolCalls(tc.input)
			if ok != tc.shouldPass {
				t.Fatalf("Kỳ vọng pass=%v, nhận ok=%v", tc.shouldPass, ok)
			}
			if tc.shouldPass {
				if len(res) == 0 {
					t.Fatal("Không nhận được tool calls")
				}
				funcMap := res[0]["function"].(map[string]interface{})
				name := funcMap["name"].(string)
				if name != tc.expectedName {
					t.Errorf("Kỳ vọng tên tool là %s, nhận được: %s", tc.expectedName, name)
				}
				args := funcMap["arguments"].(map[string]interface{})
				filePath := args["file_path"].(string)
				if filePath != tc.expectedParam {
					t.Errorf("Kỳ vọng tham số file_path là %s, nhận được: %s", tc.expectedParam, filePath)
				}
				if textOut != tc.expectedText {
					t.Errorf("Kỳ vọng text outside là '%s', nhận được: '%s'", tc.expectedText, textOut)
				}
			}
		})
	}
}


