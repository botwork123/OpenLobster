// Package codex provides an AI adapter that uses the ChatGPT subscription
// OAuth token to call the Codex Responses API at chatgpt.com/backend-api.
//
// This is NOT the standard api.openai.com endpoint — subscription OAuth tokens
// only work with the ChatGPT backend. The request format matches what OpenClaw's
// pi-ai library sends (store:false, text.verbosity, include, etc.).
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neirth/openlobster/internal/domain/ports"
)

const defaultBaseURL = "https://chatgpt.com/backend-api/codex/responses"

// Adapter implements [ports.AIProviderPort] using raw HTTP against the ChatGPT
// backend Codex Responses API.
type Adapter struct {
	endpoint   string
	token      string
	accountID  string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// NewAdapter creates an Adapter targeting the ChatGPT backend.
func NewAdapter(accessToken, accountID, model string, maxTokens int) *Adapter {
	return NewAdapterWithEndpoint(defaultBaseURL, accessToken, accountID, model, maxTokens)
}

// NewAdapterWithEndpoint creates an Adapter targeting an arbitrary URL (for testing).
func NewAdapterWithEndpoint(endpoint, accessToken, accountID, model string, maxTokens int) *Adapter {
	return &Adapter{
		endpoint:   endpoint,
		token:      accessToken,
		accountID:  accountID,
		model:      model,
		maxTokens:  maxTokens,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// ── Request types (matching OpenClaw's pi-ai format) ─────────────────────────

type codexRequest struct {
	Model             string              `json:"model"`
	Store             bool                `json:"store"`
	Stream            bool                `json:"stream"`
	Instructions      string              `json:"instructions,omitempty"`
	Input             []codexInputItem    `json:"input"`
	Text              *codexText          `json:"text,omitempty"`
	Include           []string            `json:"include,omitempty"`
	ToolChoice        string              `json:"tool_choice,omitempty"`
	ParallelToolCalls bool                `json:"parallel_tool_calls,omitempty"`
	Tools             []codexTool         `json:"tools,omitempty"`
	// NOTE: max_output_tokens is NOT supported by the chatgpt.com backend.
}

type codexText struct {
	Verbosity string `json:"verbosity"`
}

type codexInputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	// For function_call items
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// For function_call_output items
	Output    string `json:"output,omitempty"`
}

type codexTool struct {
	Type     string          `json:"type"`
	Name     string          `json:"name,omitempty"`
	Desc     string          `json:"description,omitempty"`
	Params   json.RawMessage `json:"parameters,omitempty"`
	Strict   *bool           `json:"strict,omitempty"`
}

// ── Response types ───────────────────────────────────────────────────────────

type codexResponse struct {
	ID     string             `json:"id"`
	Status string             `json:"status"`
	Output []codexOutputItem  `json:"output"`
	Error  *codexError        `json:"error,omitempty"`
}

type codexOutputItem struct {
	Type      string               `json:"type"`
	Content   []codexContentPart   `json:"content,omitempty"`
	// function_call fields
	CallID    string               `json:"call_id,omitempty"`
	Name      string               `json:"name,omitempty"`
	Arguments string               `json:"arguments,omitempty"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type codexError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// ── AIProviderPort implementation ────────────────────────────────────────────

func (a *Adapter) Chat(ctx context.Context, req ports.ChatRequest) (ports.ChatResponse, error) {
	// Build request body.
	body := codexRequest{
		Model:             a.model,
		Store:             false,
		Stream:            true,
		Text:              &codexText{Verbosity: "medium"},
		Include:           []string{"reasoning.encrypted_content"},
		ToolChoice:        "auto",
		ParallelToolCalls: true,
	}

	// Convert messages.
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			body.Instructions = m.Content
		case "user":
			body.Input = append(body.Input, codexInputItem{
				Type:    "message",
				Role:    "user",
				Content: m.Content,
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				if m.Content != "" {
					body.Input = append(body.Input, codexInputItem{
						Type:    "message",
						Role:    "assistant",
						Content: m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					body.Input = append(body.Input, codexInputItem{
						Type:      "function_call",
						CallID:    tc.ID,
						Name:      strings.ReplaceAll(tc.Function.Name, ":", "__"),
						Arguments: tc.Function.Arguments,
					})
				}
			} else {
				body.Input = append(body.Input, codexInputItem{
					Type:    "message",
					Role:    "assistant",
					Content: m.Content,
				})
			}
		case "tool":
			body.Input = append(body.Input, codexInputItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		default:
			body.Input = append(body.Input, codexInputItem{
				Type:    "message",
				Role:    "user",
				Content: m.Content,
			})
		}
	}

	// Convert tools.
	for _, t := range req.Tools {
		if t.Function == nil {
			continue
		}
		name := strings.ReplaceAll(t.Function.Name, ":", "__")
		var params json.RawMessage
		if t.Function.Parameters != nil {
			params, _ = json.Marshal(t.Function.Parameters)
		}
		strict := false
		body.Tools = append(body.Tools, codexTool{
			Type:   "function",
			Name:   name,
			Desc:   t.Function.Description,
			Params: params,
			Strict: &strict,
		})
	}

	// Marshal and send.
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("codex: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("codex: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+a.token)
	httpReq.Header.Set("chatgpt-account-id", a.accountID)
	httpReq.Header.Set("originator", "openlobster")
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("codex: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return ports.ChatResponse{}, fmt.Errorf("codex: %d %s %s", resp.StatusCode, resp.Status, string(errBody))
	}

	// Parse SSE stream. We collect events and find the final "response.completed"
	// event which contains the full response object.
	return parseSSEResponse(resp.Body)
}

func (a *Adapter) ChatWithAudio(ctx context.Context, req ports.ChatRequestWithAudio) (ports.ChatResponse, error) {
	return a.Chat(ctx, ports.ChatRequest{Model: req.Model, Messages: req.Messages, Tools: req.Tools})
}

func (a *Adapter) ChatToAudio(ctx context.Context, req ports.ChatRequest) (ports.ChatResponseWithAudio, error) {
	resp, err := a.Chat(ctx, req)
	if err != nil {
		return ports.ChatResponseWithAudio{}, err
	}
	return ports.ChatResponseWithAudio{Content: resp.Content, StopReason: resp.StopReason}, nil
}

func (a *Adapter) SupportsAudioInput() bool  { return false }
func (a *Adapter) SupportsAudioOutput() bool { return false }
func (a *Adapter) GetMaxTokens() int         { return a.maxTokens }

// ── SSE parsing ──────────────────────────────────────────────────────────────

// parseSSEResponse reads an SSE stream and extracts the final response from
// the "response.completed" event. Falls back to accumulating text deltas if
// no completed event is found.
func parseSSEResponse(r io.Reader) (ports.ChatResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var lastResponse *codexResponse
	var accumulatedText string
	var toolCalls []ports.ToolCall

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// Parse the SSE event to determine its type.
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response,omitempty"`
			Delta    string          `json:"delta,omitempty"`
			// For response.completed, the full response is at top level
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.completed":
			// The completed event contains the full response object.
			var completed struct {
				Response codexResponse `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &completed); err == nil {
				lastResponse = &completed.Response
			}

		case "response.output_text.delta":
			// Accumulate text deltas as fallback.
			var delta struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err == nil {
				accumulatedText += delta.Delta
			}

		case "response.function_call_arguments.done":
			// A complete function call.
			var fc struct {
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(data), &fc); err == nil {
				toolCalls = append(toolCalls, ports.ToolCall{
					ID:   fc.CallID,
					Type: "function",
					Function: ports.FunctionCall{
						Name:      strings.ReplaceAll(fc.Name, "__", ":"),
						Arguments: fc.Arguments,
					},
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return ports.ChatResponse{}, fmt.Errorf("codex: read SSE stream: %w", err)
	}

	// Use the completed response if available.
	if lastResponse != nil {
		return convertResponse(lastResponse), nil
	}

	// Fallback: use accumulated deltas.
	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}
	return ports.ChatResponse{
		Content:    accumulatedText,
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}, nil
}

// ── Response conversion ──────────────────────────────────────────────────────

func convertResponse(resp *codexResponse) ports.ChatResponse {
	if resp == nil || len(resp.Output) == 0 {
		return ports.ChatResponse{StopReason: "no_response"}
	}

	var content string
	var toolCalls []ports.ToolCall

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					content += c.Text
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ports.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ports.FunctionCall{
					Name:      strings.ReplaceAll(item.Name, "__", ":"),
					Arguments: item.Arguments,
				},
			})
		}
	}

	stopReason := resp.Status
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	} else if stopReason == "" {
		stopReason = "completed"
	}

	return ports.ChatResponse{
		Content:    content,
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}
}

var _ ports.AIProviderPort = (*Adapter)(nil)
