package mock

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newLLMRequest(method, body string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(method, "/v1/chat/completions", strings.NewReader(body))
	return httptest.NewRecorder(), req
}

func TestMockLLMMethodNotAllowed(t *testing.T) {
	m := NewMockLLM(t, func(*ChatCompletionRequest) *ChatCompletionResponse { return TextResponse("x") })
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.handleChatCompletions)
	rec, req := newLLMRequest(http.MethodGet, "")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestMockLLMBadRequestBody(t *testing.T) {
	m := NewMockLLM(t, func(*ChatCompletionRequest) *ChatCompletionResponse { return TextResponse("x") })
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.handleChatCompletions)
	rec, req := newLLMRequest(http.MethodPost, "{bad")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMockLLMNilHandlerResponse(t *testing.T) {
	m := NewMockLLM(t, func(*ChatCompletionRequest) *ChatCompletionResponse { return nil })
	resp := postChat(t, m.URL(), ChatCompletionRequest{Model: "m"})
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "" {
		t.Errorf("default response = %+v", resp)
	}
}

func TestMockLLMStreamNoChoices(t *testing.T) {
	m := NewMockLLM(t, func(*ChatCompletionRequest) *ChatCompletionResponse { return &ChatCompletionResponse{} })
	resp, err := http.Post(m.URL()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"m","stream":true}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteString("\n")
	}
	if !strings.Contains(body.String(), "[DONE]") {
		t.Errorf("stream body = %q, want [DONE]", body.String())
	}
}

func TestChatMessageUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "null content", in: `{"role":"user","content":null}`, want: ""},
		{name: "string content", in: `{"role":"user","content":"hello"}`, want: "hello"},
		{name: "parts content", in: `{"role":"user","content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}`, want: "ab"},
		{name: "raw fallback", in: `{"role":"user","content":42}`, want: "42"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var msg ChatMessage
			if err := json.Unmarshal([]byte(tc.in), &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if msg.Content != tc.want {
				t.Errorf("content = %q, want %q", msg.Content, tc.want)
			}
		})
	}
}

func TestMockLLMRequestsCopy(t *testing.T) {
	m := NewMockLLM(t, func(*ChatCompletionRequest) *ChatCompletionResponse { return TextResponse("x") })
	postChat(t, m.URL(), ChatCompletionRequest{Model: "m"})
	reqs := m.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	reqs[0].Model = "mutated"
	if m.Requests()[0].Model == "mutated" {
		t.Error("Requests must return a copy")
	}
	if !strings.HasPrefix(m.URL(), "http://") {
		t.Errorf("URL = %q", m.URL())
	}
}
