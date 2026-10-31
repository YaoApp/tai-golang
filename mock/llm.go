// Package mock provides test doubles for the Tai SDK.
package mock

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// ChatHandler produces a response for a given chat completion request.
type ChatHandler func(req *ChatCompletionRequest) *ChatCompletionResponse

// MockLLM is a test HTTP server that implements the OpenAI chat completions API.
type MockLLM struct {
	addr    string
	ln      net.Listener
	server  *http.Server
	handler ChatHandler

	mu       sync.Mutex
	requests []ChatCompletionRequest
}

// NewMockLLM starts a MockLLM on an ephemeral port.
func NewMockLLM(t *testing.T, handler ChatHandler) *MockLLM {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mockllm: listen: %v", err)
	}

	m := &MockLLM{
		addr:    ln.Addr().String(),
		ln:      ln,
		handler: handler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.handleChatCompletions)
	m.server = &http.Server{Handler: mux}

	go m.server.Serve(ln)
	t.Cleanup(func() { m.server.Close() })
	return m
}

// URL returns the base URL (e.g. "http://127.0.0.1:PORT").
func (m *MockLLM) URL() string {
	return "http://" + m.addr
}

// Requests returns a copy of all recorded requests.
func (m *MockLLM) Requests() []ChatCompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChatCompletionRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// AssertRequestCount fails the test if the number of recorded requests does not equal n.
func (m *MockLLM) AssertRequestCount(t *testing.T, n int) {
	t.Helper()
	m.mu.Lock()
	got := len(m.requests)
	m.mu.Unlock()
	if got != n {
		t.Errorf("MockLLM: request count = %d, want %d", got, n)
	}
}

// ---------------------------------------------------------------------------
// Request / Response types
// ---------------------------------------------------------------------------

// ChatCompletionRequest mirrors the OpenAI chat completion request body.
type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []ChatTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

// ChatMessage is a single message in the conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// UnmarshalJSON handles content as either a plain string or an OpenAI content-parts array.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type alias ChatMessage
	raw := struct {
		*alias
		Content json.RawMessage `json:"content"`
	}{alias: (*alias)(m)}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Content, &s); err == nil {
		m.Content = s
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		for _, p := range parts {
			if p.Type == "text" {
				m.Content += p.Text
			}
		}
		return nil
	}
	m.Content = string(raw.Content)
	return nil
}

// ChatTool describes a tool available to the model.
type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatToolFunc `json:"function"`
}

// ChatToolFunc is the function definition inside a ChatTool.
type ChatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a tool invocation returned by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc holds the function name and arguments.
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletionResponse is the full non-streaming response.
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

// ChatChoice is one choice in the response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage reports token consumption.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Streaming types
type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// TextResponse creates a ChatCompletionResponse with a simple text reply.
func TextResponse(content string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "mock-model",
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: content},
			FinishReason: "stop",
		}},
		Usage: &ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// ToolCallResponse creates a response where the assistant invokes tool calls.
func ToolCallResponse(calls ...ToolCall) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "mock-model",
		Choices: []ChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:      "assistant",
				ToolCalls: calls,
			},
			FinishReason: "tool_calls",
		}},
		Usage: &ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// MakeToolCall is a convenience constructor for a single ToolCall.
func MakeToolCall(id, name, argsJSON string) ToolCall {
	return ToolCall{
		ID:   id,
		Type: "function",
		Function: ToolCallFunc{
			Name:      name,
			Arguments: argsJSON,
		},
	}
}

// ScriptHandler returns a ChatHandler that replays responses in order.
func ScriptHandler(responses ...*ChatCompletionResponse) ChatHandler {
	var mu sync.Mutex
	idx := 0
	return func(_ *ChatCompletionRequest) *ChatCompletionResponse {
		mu.Lock()
		defer mu.Unlock()
		if idx < len(responses) {
			r := responses[idx]
			idx++
			return r
		}
		return TextResponse("(end of script)")
	}
}

// ---------------------------------------------------------------------------
// HTTP handler
// ---------------------------------------------------------------------------

func (m *MockLLM) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	resp := m.handler(&req)
	if resp == nil {
		resp = TextResponse("")
	}

	if req.Stream {
		m.writeStream(w, resp)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func (m *MockLLM) writeStream(w http.ResponseWriter, resp *ChatCompletionResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	if len(resp.Choices) == 0 {
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	choice := resp.Choices[0]

	writeChunk(w, flusher, resp, chunkChoice{
		Index: 0,
		Delta: chunkDelta{Role: "assistant"},
	})

	if len(choice.Message.ToolCalls) > 0 {
		for _, tc := range choice.Message.ToolCalls {
			writeChunk(w, flusher, resp, chunkChoice{
				Index: 0,
				Delta: chunkDelta{ToolCalls: []ToolCall{tc}},
			})
		}
		finish := "tool_calls"
		writeChunk(w, flusher, resp, chunkChoice{
			Index:        0,
			Delta:        chunkDelta{},
			FinishReason: &finish,
		})
	} else {
		content := choice.Message.Content
		for _, ch := range content {
			writeChunk(w, flusher, resp, chunkChoice{
				Index: 0,
				Delta: chunkDelta{Content: string(ch)},
			})
		}
		finish := "stop"
		writeChunk(w, flusher, resp, chunkChoice{
			Index:        0,
			Delta:        chunkDelta{},
			FinishReason: &finish,
		})
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeChunk(w http.ResponseWriter, f http.Flusher, resp *ChatCompletionResponse, ch chunkChoice) {
	chunk := chatChunk{
		ID:      resp.ID,
		Object:  "chat.completion.chunk",
		Created: resp.Created,
		Model:   resp.Model,
		Choices: []chunkChoice{ch},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}
