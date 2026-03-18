package cloudcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neirth/openlobster/internal/domain/ports"
)

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewGeminiAdapter(t *testing.T) {
	a := NewGeminiAdapter("tok", "proj-1", "gemini-2.5-pro", 8192)

	if a.accessToken != "tok" {
		t.Fatalf("accessToken = %q, want %q", a.accessToken, "tok")
	}
	if a.projectID != "proj-1" {
		t.Fatalf("projectID = %q, want %q", a.projectID, "proj-1")
	}
	if a.model != "gemini-2.5-pro" {
		t.Fatalf("model = %q, want %q", a.model, "gemini-2.5-pro")
	}
	if a.maxTokens != 8192 {
		t.Fatalf("maxTokens = %d, want %d", a.maxTokens, 8192)
	}
	if a.bodyAgent != "pi-coding-agent" {
		t.Fatalf("bodyAgent = %q, want %q", a.bodyAgent, "pi-coding-agent")
	}
	if a.requestType != "" {
		t.Fatalf("requestType = %q, want empty", a.requestType)
	}
	if !strings.Contains(a.endpoint, "cloudcode-pa.googleapis.com") {
		t.Fatalf("endpoint = %q, want cloudcode-pa.googleapis.com", a.endpoint)
	}
	if a.userAgent != "google-cloud-sdk vscode_cloudshelleditor/0.1" {
		t.Fatalf("userAgent = %q", a.userAgent)
	}
	if a.headers["X-Goog-Api-Client"] != "gl-node/22.17.0" {
		t.Fatalf("missing X-Goog-Api-Client header")
	}
}

func TestNewAntigravityAdapter(t *testing.T) {
	a := NewAntigravityAdapter("tok", "proj-2", "gemini-2.5-pro", 4096)

	if a.bodyAgent != "antigravity" {
		t.Fatalf("bodyAgent = %q, want %q", a.bodyAgent, "antigravity")
	}
	if a.requestType != "agent" {
		t.Fatalf("requestType = %q, want %q", a.requestType, "agent")
	}
	if !strings.Contains(a.endpoint, "daily-cloudcode-pa.sandbox.googleapis.com") {
		t.Fatalf("endpoint = %q, want daily-cloudcode-pa.sandbox", a.endpoint)
	}
	if a.userAgent != "antigravity/1.18.4" {
		t.Fatalf("userAgent = %q", a.userAgent)
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestAdapterImplementsPort(t *testing.T) {
	var _ ports.AIProviderPort = (*Adapter)(nil)
}

// ---------------------------------------------------------------------------
// Audio stubs
// ---------------------------------------------------------------------------

func TestSupportsAudio(t *testing.T) {
	a := NewGeminiAdapter("tok", "p", "m", 100)
	if a.SupportsAudioInput() {
		t.Fatal("SupportsAudioInput should be false")
	}
	if a.SupportsAudioOutput() {
		t.Fatal("SupportsAudioOutput should be false")
	}
}

func TestGetMaxTokens(t *testing.T) {
	a := NewGeminiAdapter("tok", "p", "m", 4096)
	if a.GetMaxTokens() != 4096 {
		t.Fatalf("GetMaxTokens = %d, want 4096", a.GetMaxTokens())
	}
}

// ---------------------------------------------------------------------------
// Chat – text-only response
// ---------------------------------------------------------------------------

func TestChat_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "google-cloud-sdk") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("X-Goog-Api-Client") != "gl-node/22.17.0" {
			t.Errorf("X-Goog-Api-Client = %q", r.Header.Get("X-Goog-Api-Client"))
		}

		body, _ := io.ReadAll(r.Body)
		var req ccRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if req.Project != "my-project" {
			t.Errorf("project = %q", req.Project)
		}
		if req.UserAgent != "pi-coding-agent" {
			t.Errorf("userAgent = %q", req.UserAgent)
		}
		if !strings.HasPrefix(req.RequestID, "openlobster-") {
			t.Errorf("requestId = %q", req.RequestID)
		}

		// Check system instruction was extracted
		if req.Request.SystemInstruction == nil {
			t.Fatal("expected systemInstruction")
		}
		if req.Request.SystemInstruction.Parts[0].Text != "You are helpful" {
			t.Errorf("system = %q", req.Request.SystemInstruction.Parts[0].Text)
		}

		// Check contents: should have user and model messages
		if len(req.Request.Contents) != 2 {
			t.Fatalf("contents len = %d, want 2", len(req.Request.Contents))
		}
		if req.Request.Contents[0].Role != "user" {
			t.Errorf("contents[0].role = %q", req.Request.Contents[0].Role)
		}
		if req.Request.Contents[1].Role != "model" {
			t.Errorf("contents[1].role = %q", req.Request.Contents[1].Role)
		}

		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{
					Role:  "model",
					Parts: []ccPart{{Text: "Hello there!"}},
				},
				FinishReason: "STOP",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewGeminiAdapter("test-token", "my-project", "gemini-2.5-pro", 8192)
	a.endpoint = srv.URL

	result, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hey"},
		},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if result.Content != "Hello there!" {
		t.Fatalf("Content = %q", result.Content)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q", result.StopReason)
	}
}

// ---------------------------------------------------------------------------
// Chat – tool call response
// ---------------------------------------------------------------------------

func TestChat_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ccRequest
		json.Unmarshal(body, &req)

		// Verify tools were sent
		if len(req.Request.Tools) == 0 {
			t.Fatal("expected tools")
		}
		if req.Request.Tools[0].FunctionDeclarations[0].Name != "read_file" {
			t.Errorf("tool name = %q", req.Request.Tools[0].FunctionDeclarations[0].Name)
		}
		if req.Request.ToolConfig == nil || req.Request.ToolConfig.FunctionCallingConfig.Mode != "AUTO" {
			t.Error("expected toolConfig with AUTO mode")
		}

		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{
					Role: "model",
					Parts: []ccPart{
						{Text: "Let me read that file."},
						{FunctionCall: &ccFunctionCall{
							Name: "read_file",
							Args: map[string]interface{}{"path": "/tmp/test.txt"},
						}},
					},
				},
				FinishReason: "STOP",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewGeminiAdapter("tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	result, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{
			{Role: "user", Content: "Read /tmp/test.txt"},
		},
		Tools: []ports.Tool{{
			Type: "function",
			Function: &ports.FunctionTool{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if result.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool name = %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Type != "function" {
		t.Fatalf("tool type = %q", result.ToolCalls[0].Type)
	}
	// Verify args
	var args map[string]interface{}
	json.Unmarshal([]byte(result.ToolCalls[0].Function.Arguments), &args)
	if args["path"] != "/tmp/test.txt" {
		t.Fatalf("tool args path = %q", args["path"])
	}
	if result.Content != "Let me read that file." {
		t.Fatalf("Content = %q", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Chat – Antigravity mode includes requestType
// ---------------------------------------------------------------------------

func TestChat_AntigravityRequestType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "antigravity") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}

		body, _ := io.ReadAll(r.Body)
		var req ccRequest
		json.Unmarshal(body, &req)
		if req.RequestType != "agent" {
			t.Errorf("requestType = %q, want agent", req.RequestType)
		}
		if req.UserAgent != "antigravity" {
			t.Errorf("userAgent = %q, want antigravity", req.UserAgent)
		}

		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{Parts: []ccPart{{Text: "ok"}}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewAntigravityAdapter("tok", "proj", "model", 4096)
	a.endpoint = srv.URL

	_, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Chat – HTTP error
// ---------------------------------------------------------------------------

func TestChat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	a := NewGeminiAdapter("bad-tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	_, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want 403", err)
	}
}

// ---------------------------------------------------------------------------
// Chat – empty candidates
// ---------------------------------------------------------------------------

func TestChat_EmptyCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srv.Close()

	a := NewGeminiAdapter("tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	result, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if result.StopReason != "no_response" {
		t.Fatalf("StopReason = %q, want no_response", result.StopReason)
	}
}

// ---------------------------------------------------------------------------
// Tool result message conversion
// ---------------------------------------------------------------------------

func TestChat_ToolResultMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ccRequest
		json.Unmarshal(body, &req)

		// Find the tool result content
		found := false
		for _, c := range req.Request.Contents {
			for _, p := range c.Parts {
				if p.FunctionResp != nil {
					found = true
					if p.FunctionResp.Name != "read_file" {
						t.Errorf("functionResponse name = %q", p.FunctionResp.Name)
					}
				}
			}
		}
		if !found {
			t.Error("no functionResponse found in request")
		}

		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{Parts: []ccPart{{Text: "got it"}}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewGeminiAdapter("tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	_, err := a.Chat(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{
			{Role: "user", Content: "read the file"},
			{Role: "assistant", Content: "", ToolCalls: []ports.ToolCall{{
				ID: "call_0", Type: "function",
				Function: ports.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/x"}`},
			}}},
			{Role: "tool", Content: `{"content":"file data"}`, ToolCallID: "call_0", ToolName: "read_file"},
		},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ChatWithAudio delegates to Chat
// ---------------------------------------------------------------------------

func TestChatWithAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{Parts: []ccPart{{Text: "audio ignored"}}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewGeminiAdapter("tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	result, err := a.ChatWithAudio(context.Background(), ports.ChatRequestWithAudio{
		Messages:  []ports.ChatMessage{{Role: "user", Content: "hi"}},
		AudioData: []byte("fake-audio"),
	})
	if err != nil {
		t.Fatalf("ChatWithAudio error: %v", err)
	}
	if result.Content != "audio ignored" {
		t.Fatalf("Content = %q", result.Content)
	}
}

// ---------------------------------------------------------------------------
// ChatToAudio delegates to Chat
// ---------------------------------------------------------------------------

func TestChatToAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ccResponse{
			Candidates: []ccCandidate{{
				Content: ccContent{Parts: []ccPart{{Text: "text only"}}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewGeminiAdapter("tok", "proj", "model", 8192)
	a.endpoint = srv.URL

	result, err := a.ChatToAudio(context.Background(), ports.ChatRequest{
		Messages: []ports.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatToAudio error: %v", err)
	}
	if result.Content != "text only" {
		t.Fatalf("Content = %q", result.Content)
	}
	if len(result.AudioData) != 0 {
		t.Fatal("expected no audio data")
	}
}
