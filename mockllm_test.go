package tai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Non-streaming tests
// ---------------------------------------------------------------------------

func TestMockLLM_TextResponse(t *testing.T) {
	m := NewMockLLM(t, func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		return TextResponse("Hello, world!")
	})

	resp := postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   false,
	})

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello, world!" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "Hello, world!")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
	m.AssertRequestCount(t, 1)
}

func TestMockLLM_ToolCallResponse(t *testing.T) {
	m := NewMockLLM(t, func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		return ToolCallResponse(
			MakeToolCall("call_1", "get_weather", `{"city":"Beijing"}`),
		)
	})

	resp := postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "weather?"}},
		Tools: []ChatTool{{
			Type:     "function",
			Function: ChatToolFunc{Name: "get_weather", Description: "get weather"},
		}},
	})

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	ch := resp.Choices[0]
	if ch.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want %q", ch.FinishReason, "tool_calls")
	}
	if len(ch.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(ch.Message.ToolCalls))
	}
	tc := ch.Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", tc.Function.Name, "get_weather")
	}
	if tc.Function.Arguments != `{"city":"Beijing"}` {
		t.Errorf("tool args = %q, want %q", tc.Function.Arguments, `{"city":"Beijing"}`)
	}
}

func TestMockLLM_ScriptHandler(t *testing.T) {
	m := NewMockLLM(t, ScriptHandler(
		ToolCallResponse(MakeToolCall("c1", "bash", `{"cmd":"echo hi"}`)),
		TextResponse("done"),
	))

	// First request → tool_call
	r1 := postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "run echo hi"}},
	})
	if r1.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("r1 finish_reason = %q, want tool_calls", r1.Choices[0].FinishReason)
	}

	// Second request → text (after tool result)
	r2 := postChat(t, m.URL(), ChatCompletionRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "user", Content: "run echo hi"},
			{Role: "assistant", ToolCalls: []ToolCall{MakeToolCall("c1", "bash", `{"cmd":"echo hi"}`)}},
			{Role: "tool", ToolCallID: "c1", Content: "hi\n"},
		},
	})
	if r2.Choices[0].Message.Content != "done" {
		t.Errorf("r2 content = %q, want %q", r2.Choices[0].Message.Content, "done")
	}

	// Third request → end of script
	r3 := postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "more"}},
	})
	if r3.Choices[0].Message.Content != "(end of script)" {
		t.Errorf("r3 content = %q, want end of script", r3.Choices[0].Message.Content)
	}

	m.AssertRequestCount(t, 3)
}

// TestMockLLM_ArrayContent verifies UnmarshalJSON handles the OpenAI
// multi-modal content format: [{type:"text",text:"..."}]. PiG and other
// OpenAI-compatible providers send content in this array format.
func TestMockLLM_ArrayContent(t *testing.T) {
	var got string
	m := NewMockLLM(t, func(req *ChatCompletionRequest) *ChatCompletionResponse {
		if len(req.Messages) > 0 {
			got = req.Messages[0].Content
		}
		return TextResponse("ok")
	})

	// Array format — what PiG actually sends.
	body := `{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":" world"}]}]}`
	resp, err := http.Post(m.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if got != "hello world" {
		t.Errorf("content = %q, want %q", got, "hello world")
	}

	// Null content — should not panic.
	body2 := `{"model":"test","messages":[{"role":"assistant","content":null,"tool_calls":[]}]}`
	resp2, err := http.Post(m.URL()+"/v1/chat/completions", "application/json", strings.NewReader(body2))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp2.Body.Close()
	m.AssertRequestCount(t, 2)
}

func TestMockLLM_RequestRecording(t *testing.T) {
	m := NewMockLLM(t, func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		return TextResponse("ok")
	})

	postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "model-a",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	postChat(t, m.URL(), ChatCompletionRequest{
		Model:    "model-b",
		Messages: []ChatMessage{{Role: "user", Content: "world"}},
	})

	reqs := m.Requests()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	if reqs[0].Model != "model-a" {
		t.Errorf("reqs[0].Model = %q, want model-a", reqs[0].Model)
	}
	if reqs[1].Messages[0].Content != "world" {
		t.Errorf("reqs[1] content = %q, want world", reqs[1].Messages[0].Content)
	}
}

// ---------------------------------------------------------------------------
// Streaming tests
// ---------------------------------------------------------------------------

func TestMockLLM_StreamingText(t *testing.T) {
	m := NewMockLLM(t, func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		return TextResponse("Hi!")
	})

	chunks := streamChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	})

	// Expect: role chunk, 'H', 'i', '!', finish chunk
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks, want >= 4", len(chunks))
	}

	// First chunk sets role
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("first chunk role = %q, want assistant", chunks[0].Choices[0].Delta.Role)
	}

	// Reconstruct text from content deltas
	var text strings.Builder
	for _, ch := range chunks {
		if len(ch.Choices) > 0 {
			text.WriteString(ch.Choices[0].Delta.Content)
		}
	}
	if text.String() != "Hi!" {
		t.Errorf("streamed text = %q, want %q", text.String(), "Hi!")
	}

	// Last real chunk has finish_reason
	last := chunks[len(chunks)-1]
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Error("last chunk missing finish_reason=stop")
	}
}

func TestMockLLM_StreamingToolCall(t *testing.T) {
	m := NewMockLLM(t, func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		return ToolCallResponse(MakeToolCall("tc1", "read_file", `{"path":"a.go"}`))
	})

	chunks := streamChat(t, m.URL(), ChatCompletionRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "read a.go"}},
		Stream:   true,
	})

	// Find tool_call chunk
	var found bool
	for _, ch := range chunks {
		if len(ch.Choices) > 0 && len(ch.Choices[0].Delta.ToolCalls) > 0 {
			tc := ch.Choices[0].Delta.ToolCalls[0]
			if tc.Function.Name != "read_file" {
				t.Errorf("tool name = %q, want read_file", tc.Function.Name)
			}
			found = true
		}
	}
	if !found {
		t.Error("no tool_call chunk found in stream")
	}

	// Find finish_reason=tool_calls
	var hasFinish bool
	for _, ch := range chunks {
		if len(ch.Choices) > 0 && ch.Choices[0].FinishReason != nil && *ch.Choices[0].FinishReason == "tool_calls" {
			hasFinish = true
		}
	}
	if !hasFinish {
		t.Error("missing finish_reason=tool_calls in stream")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func postChat(t *testing.T, baseURL string, req ChatCompletionRequest) ChatCompletionResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func streamChat(t *testing.T, baseURL string, req ChatCompletionRequest) []chatChunk {
	t.Helper()
	body, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var chunks []chatChunk
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("decode chunk: %v (line: %s)", err, data)
		}
		chunks = append(chunks, chunk)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("scan: %v", err)
	}
	return chunks
}
