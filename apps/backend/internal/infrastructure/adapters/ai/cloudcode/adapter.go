// Package cloudcode provides an AI adapter for Google Cloud Code Assist,
// serving both the Gemini CLI and Antigravity clients via raw HTTP requests.
//
// There is no Go SDK for Cloud Code Assist, so this adapter builds and parses
// the JSON request/response payloads directly.
//
// # License
// See LICENSE in the root of the repository.
package cloudcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/neirth/openlobster/internal/domain/ports"
)

// Adapter implements [ports.AIProviderPort] for Google Cloud Code Assist.
type Adapter struct {
	httpClient  *http.Client
	endpoint    string
	accessToken string
	projectID   string
	model       string
	maxTokens   int
	userAgent   string // HTTP User-Agent header
	bodyAgent   string // userAgent field in request body
	requestType string // optional requestType field in request body (Antigravity only)
	headers     map[string]string
}

// NewGeminiAdapter creates an Adapter configured for the Gemini CLI endpoint.
func NewGeminiAdapter(accessToken, projectID, model string, maxTokens int) *Adapter {
	return &Adapter{
		httpClient:  &http.Client{Timeout: 120 * time.Second},
		endpoint:    "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent",
		accessToken: accessToken,
		projectID:   projectID,
		model:       model,
		maxTokens:   maxTokens,
		userAgent:   "google-cloud-sdk vscode_cloudshelleditor/0.1",
		bodyAgent:   "pi-coding-agent",
		headers: map[string]string{
			"X-Goog-Api-Client": "gl-node/22.17.0",
		},
	}
}

// NewAntigravityAdapter creates an Adapter configured for the Antigravity endpoint.
func NewAntigravityAdapter(accessToken, projectID, model string, maxTokens int) *Adapter {
	return &Adapter{
		httpClient:  &http.Client{Timeout: 120 * time.Second},
		endpoint:    "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:streamGenerateContent",
		accessToken: accessToken,
		projectID:   projectID,
		model:       model,
		maxTokens:   maxTokens,
		userAgent:   "antigravity/1.18.4",
		bodyAgent:   "antigravity",
		requestType: "agent",
		headers:     map[string]string{},
	}
}

// ---------------------------------------------------------------------------
// Request / response types for the Cloud Code Assist API
// ---------------------------------------------------------------------------

type ccRequest struct {
	Project     string      `json:"project"`
	Model       string      `json:"model"`
	Request     ccInner     `json:"request"`
	UserAgent   string      `json:"userAgent"`
	RequestID   string      `json:"requestId"`
	RequestType string      `json:"requestType,omitempty"`
}

type ccInner struct {
	Contents          []ccContent       `json:"contents"`
	SystemInstruction *ccSystemInstr    `json:"systemInstruction,omitempty"`
	GenerationConfig  ccGenerationCfg   `json:"generationConfig"`
	Tools             []ccTool          `json:"tools,omitempty"`
	ToolConfig        *ccToolConfig     `json:"toolConfig,omitempty"`
}

type ccContent struct {
	Role  string   `json:"role"`
	Parts []ccPart `json:"parts"`
}

type ccPart struct {
	Text         string          `json:"text,omitempty"`
	FunctionCall *ccFunctionCall `json:"functionCall,omitempty"`
	FunctionResp *ccFunctionResp `json:"functionResponse,omitempty"`
}

type ccFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type ccFunctionResp struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type ccSystemInstr struct {
	Parts []ccPart `json:"parts"`
}

type ccGenerationCfg struct {
	MaxOutputTokens int     `json:"maxOutputTokens"`
	Temperature     float64 `json:"temperature"`
}

type ccTool struct {
	FunctionDeclarations []ccFunctionDecl `json:"functionDeclarations"`
}

type ccFunctionDecl struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type ccToolConfig struct {
	FunctionCallingConfig ccFuncCallCfg `json:"functionCallingConfig"`
}

type ccFuncCallCfg struct {
	Mode string `json:"mode"`
}

// Response types

type ccResponse struct {
	Candidates []ccCandidate `json:"candidates"`
}

type ccCandidate struct {
	Content      ccContent `json:"content"`
	FinishReason string    `json:"finishReason,omitempty"`
}

// ---------------------------------------------------------------------------
// AIProviderPort implementation
// ---------------------------------------------------------------------------

// Chat sends a non-streaming chat request to the Cloud Code Assist API.
func (a *Adapter) Chat(ctx context.Context, req ports.ChatRequest) (ports.ChatResponse, error) {
	body := a.buildRequest(req)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: marshal request: %w", err)
	}

	// Non-streaming: omit ?alt=sse
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.accessToken)
	httpReq.Header.Set("User-Agent", a.userAgent)
	for k, v := range a.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var ccResp ccResponse
	if err := json.Unmarshal(respBody, &ccResp); err != nil {
		return ports.ChatResponse{}, fmt.Errorf("cloudcode: unmarshal response: %w", err)
	}

	return a.parseResponse(ccResp), nil
}

// ChatWithAudio processes a chat request that may include audio data.
// Audio is currently ignored; only text messages are forwarded.
func (a *Adapter) ChatWithAudio(ctx context.Context, req ports.ChatRequestWithAudio) (ports.ChatResponse, error) {
	return a.Chat(ctx, ports.ChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Tools:    req.Tools,
	})
}

// ChatToAudio sends a chat request and returns the response as text.
// Audio synthesis is not supported by Cloud Code Assist.
func (a *Adapter) ChatToAudio(ctx context.Context, req ports.ChatRequest) (ports.ChatResponseWithAudio, error) {
	resp, err := a.Chat(ctx, req)
	if err != nil {
		return ports.ChatResponseWithAudio{}, err
	}
	return ports.ChatResponseWithAudio{
		Content:    resp.Content,
		StopReason: resp.StopReason,
	}, nil
}

// SupportsAudioInput reports whether the adapter can process audio input.
func (a *Adapter) SupportsAudioInput() bool { return false }

// SupportsAudioOutput reports whether the adapter can produce audio output.
func (a *Adapter) SupportsAudioOutput() bool { return false }

// GetMaxTokens returns the configured maximum token budget.
func (a *Adapter) GetMaxTokens() int { return a.maxTokens }

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (a *Adapter) buildRequest(req ports.ChatRequest) ccRequest {
	var systemInstr *ccSystemInstr
	var contents []ccContent

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemInstr = &ccSystemInstr{
				Parts: []ccPart{{Text: m.Content}},
			}
		case "user":
			text := m.Content
			if len(m.Blocks) > 0 {
				var sb strings.Builder
				for _, b := range m.Blocks {
					if b.Type == ports.ContentBlockText && b.Text != "" {
						if sb.Len() > 0 {
							sb.WriteString("\n")
						}
						sb.WriteString(b.Text)
					}
				}
				if sb.Len() > 0 {
					text = sb.String()
				}
			}
			contents = append(contents, ccContent{
				Role:  "user",
				Parts: []ccPart{{Text: text}},
			})
		case "assistant":
			parts := make([]ccPart, 0)
			if m.Content != "" {
				parts = append(parts, ccPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				parts = append(parts, ccPart{
					FunctionCall: &ccFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, ccContent{
					Role:  "model",
					Parts: parts,
				})
			}
		case "tool":
			var respData map[string]interface{}
			if m.Content != "" {
				if err := json.Unmarshal([]byte(m.Content), &respData); err != nil {
					// If content is not valid JSON, wrap it.
					respData = map[string]interface{}{"result": m.Content}
				}
			}
			toolName := m.ToolName
			if toolName == "" {
				toolName = "unknown"
			}
			contents = append(contents, ccContent{
				Role: "user",
				Parts: []ccPart{{
					FunctionResp: &ccFunctionResp{
						Name:     toolName,
						Response: respData,
					},
				}},
			})
		default:
			// Fallback: treat as user message.
			contents = append(contents, ccContent{
				Role:  "user",
				Parts: []ccPart{{Text: m.Content}},
			})
		}
	}

	inner := ccInner{
		Contents:         contents,
		SystemInstruction: systemInstr,
		GenerationConfig: ccGenerationCfg{
			MaxOutputTokens: a.maxTokens,
			Temperature:     1.0,
		},
	}

	if len(req.Tools) > 0 {
		decls := make([]ccFunctionDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Function == nil {
				continue
			}
			decls = append(decls, ccFunctionDecl{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			})
		}
		if len(decls) > 0 {
			inner.Tools = []ccTool{{FunctionDeclarations: decls}}
			inner.ToolConfig = &ccToolConfig{
				FunctionCallingConfig: ccFuncCallCfg{Mode: "AUTO"},
			}
		}
	}

	ccReq := ccRequest{
		Project:   a.projectID,
		Model:     a.model,
		Request:   inner,
		UserAgent: a.bodyAgent,
		RequestID: fmt.Sprintf("openlobster-%d-%d", time.Now().UnixMilli(), rand.Intn(100000)),
	}

	if a.requestType != "" {
		ccReq.RequestType = a.requestType
	}

	return ccReq
}

func (a *Adapter) parseResponse(resp ccResponse) ports.ChatResponse {
	if len(resp.Candidates) == 0 {
		return ports.ChatResponse{StopReason: "no_response"}
	}

	candidate := resp.Candidates[0]
	var textParts []string
	var toolCalls []ports.ToolCall

	for i, part := range candidate.Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, ports.ToolCall{
				ID:   fmt.Sprintf("call_%d", i),
				Type: "function",
				Function: ports.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	stopReason := strings.ToLower(candidate.FinishReason)
	if stopReason == "stop" || stopReason == "" {
		stopReason = "end_turn"
	}
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	return ports.ChatResponse{
		Content:    strings.Join(textParts, ""),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}
}

// Compile-time interface satisfaction check.
var _ ports.AIProviderPort = (*Adapter)(nil)
