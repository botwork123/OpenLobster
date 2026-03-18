package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neirth/openlobster/internal/domain/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseResponse builds a simple SSE response with a completed event.
func sseResponse(text string) string {
	return `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"` + text + `"}]}]}}` + "\n\ndata: [DONE]\n\n"
}

func sseHandler(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sseResponse(text)))
	}
}

func TestChat_RequestFormat(t *testing.T) {
	var captured codexRequest
	var headers http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(sseResponse("hello")))
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "test-token", "acct-123", "gpt-5.4", 4096)
	resp, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hi"},
		},
	})
	require.NoError(t, err)

	// Verify headers
	assert.Equal(t, "Bearer test-token", headers.Get("Authorization"))
	assert.Equal(t, "acct-123", headers.Get("chatgpt-account-id"))
	assert.Equal(t, "openlobster", headers.Get("originator"))
	assert.Equal(t, "responses=experimental", headers.Get("OpenAI-Beta"))
	assert.Equal(t, "text/event-stream", headers.Get("Accept"))

	// Verify body format - critical fields
	assert.Equal(t, "gpt-5.4", captured.Model)
	assert.True(t, captured.Stream, "stream MUST be true - API rejects false")
	assert.False(t, captured.Store)
	assert.Equal(t, "you are helpful", captured.Instructions)
	assert.Equal(t, 4096, captured.MaxOutputTokens)
	assert.Equal(t, "auto", captured.ToolChoice)
	assert.True(t, captured.ParallelToolCalls)
	require.NotNil(t, captured.Text)
	assert.Equal(t, "medium", captured.Text.Verbosity)
	assert.Contains(t, captured.Include, "reasoning.encrypted_content")

	// Verify input - system goes to instructions, only user in input
	require.Len(t, captured.Input, 1)
	assert.Equal(t, "message", captured.Input[0].Type)
	assert.Equal(t, "user", captured.Input[0].Role)
	assert.Equal(t, "hi", captured.Input[0].Content)

	// Verify response
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "completed", resp.StopReason)
}

func TestChat_SSETextDeltaFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	resp, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp.Content)
	assert.Equal(t, "end_turn", resp.StopReason)
}

func TestChat_ToolCallsFromCompletedEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"mcp__search","arguments":"{\"q\":\"test\"}"}]}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	resp, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "search"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tool_use", resp.StopReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_1", resp.ToolCalls[0].ID)
	assert.Equal(t, "mcp:search", resp.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"q":"test"}`, resp.ToolCalls[0].Function.Arguments)
}

func TestChat_ToolCallHistory(t *testing.T) {
	var captured codexRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		sseHandler("ok")(w, r)
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	_, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{
			{Role: "user", Content: "search"},
			{Role: "assistant", ToolCalls: []ports.ToolCall{
				{ID: "call_1", Type: "function", Function: ports.FunctionCall{Name: "mcp:search", Arguments: `{"q":"test"}`}},
			}},
			{Role: "tool", Content: "result data", ToolCallID: "call_1"},
		},
	})
	require.NoError(t, err)

	require.Len(t, captured.Input, 3)
	assert.Equal(t, "message", captured.Input[0].Type)
	assert.Equal(t, "function_call", captured.Input[1].Type)
	assert.Equal(t, "mcp__search", captured.Input[1].Name)
	assert.Equal(t, "function_call_output", captured.Input[2].Type)
	assert.Equal(t, "call_1", captured.Input[2].CallID)
}

func TestChat_ToolsInRequest(t *testing.T) {
	var captured codexRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		sseHandler("ok")(w, r)
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	_, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []ports.Tool{{Type: "function", Function: &ports.FunctionTool{
			Name: "mcp:search", Description: "search the web",
			Parameters: map[string]interface{}{"type": "object"},
		}}},
	})
	require.NoError(t, err)

	require.Len(t, captured.Tools, 1)
	assert.Equal(t, "function", captured.Tools[0].Type)
	assert.Equal(t, "mcp__search", captured.Tools[0].Name)
}

func TestChat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	_, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestChat_EmptySSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	adapter := NewAdapterWithEndpoint(srv.URL, "tok", "acct", "gpt-5.4", 4096)
	resp, err := adapter.Chat(t.Context(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "", resp.Content)
	assert.Equal(t, "end_turn", resp.StopReason)
}

func TestParseSSEResponse(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hi"}`,
		`data: {"type":"response.output_text.delta","delta":" there"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hi there"}]}]}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	resp, err := parseSSEResponse(strings.NewReader(sse))
	require.NoError(t, err)
	assert.Equal(t, "Hi there", resp.Content)
	assert.Equal(t, "completed", resp.StopReason)
}

func TestAdapter_Interface(t *testing.T) {
	var _ ports.AIProviderPort = (*Adapter)(nil)
}

func TestAdapter_GetMaxTokens(t *testing.T) {
	a := NewAdapterWithEndpoint("http://fake", "tok", "acct", "gpt-5.4", 8192)
	assert.Equal(t, 8192, a.GetMaxTokens())
}

func TestAdapter_AudioSupport(t *testing.T) {
	a := NewAdapterWithEndpoint("http://fake", "tok", "acct", "gpt-5.4", 4096)
	assert.False(t, a.SupportsAudioInput())
	assert.False(t, a.SupportsAudioOutput())
}
