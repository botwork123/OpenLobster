// Copyright (c) OpenLobster contributors. See LICENSE for details.

package oauthanthropic

import (
	"testing"

	"github.com/neirth/openlobster/internal/domain/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewAnthropicOAuthAdapter_DefaultMaxTokens(t *testing.T) {
	a := NewAnthropicOAuthAdapter("sk-ant-oat-test", "claude-sonnet-4-20250514", 0)
	assert.Equal(t, defaultMaxTokens, a.GetMaxTokens())
	assert.Equal(t, "claude-sonnet-4-20250514", a.model)
}

func TestNewAnthropicOAuthAdapter_CustomMaxTokens(t *testing.T) {
	a := NewAnthropicOAuthAdapter("sk-ant-oat-test", "claude-sonnet-4-20250514", 8192)
	assert.Equal(t, 8192, a.GetMaxTokens())
}

func TestNewCopilotAdapter_DefaultMaxTokens(t *testing.T) {
	a := NewCopilotAdapter("ghu_token", "https://api.individual.githubcopilot.com", "claude-sonnet-4-20250514", 0)
	assert.Equal(t, defaultMaxTokens, a.GetMaxTokens())
	assert.Equal(t, "claude-sonnet-4-20250514", a.model)
}

func TestNewCopilotAdapter_CustomMaxTokens(t *testing.T) {
	a := NewCopilotAdapter("ghu_token", "https://api.individual.githubcopilot.com", "claude-sonnet-4-20250514", 16384)
	assert.Equal(t, 16384, a.GetMaxTokens())
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestAdapter_ImplementsAIProviderPort(t *testing.T) {
	var _ ports.AIProviderPort = (*Adapter)(nil)
}

// ---------------------------------------------------------------------------
// Audio support flags
// ---------------------------------------------------------------------------

func TestAdapter_SupportsAudioInput(t *testing.T) {
	a := NewAnthropicOAuthAdapter("tok", "model", 0)
	assert.False(t, a.SupportsAudioInput())
}

func TestAdapter_SupportsAudioOutput(t *testing.T) {
	a := NewAnthropicOAuthAdapter("tok", "model", 0)
	assert.False(t, a.SupportsAudioOutput())
}

// ---------------------------------------------------------------------------
// Tool name encoding / decoding
// ---------------------------------------------------------------------------

func TestEncodeToolName(t *testing.T) {
	assert.Equal(t, "mcp__server__tool", encodeToolName("mcp:server:tool"))
	assert.Equal(t, "plain", encodeToolName("plain"))
}

func TestDecodeToolName(t *testing.T) {
	assert.Equal(t, "mcp:server:tool", decodeToolName("mcp__server__tool"))
	assert.Equal(t, "plain", decodeToolName("plain"))
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

func TestConvertMessages_SystemExtracted(t *testing.T) {
	msgs := []ports.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
	}
	systemBlocks, params := convertMessages(msgs)

	require.Len(t, systemBlocks, 1)
	assert.Equal(t, "You are helpful.", systemBlocks[0].Text)
	require.Len(t, params, 1)
}

func TestConvertMessages_AssistantWithToolCalls(t *testing.T) {
	msgs := []ports.ChatMessage{
		{Role: "user", Content: "run test"},
		{
			Role:    "assistant",
			Content: "I'll run that.",
			ToolCalls: []ports.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: ports.FunctionCall{
						Name:      "mcp:server:run",
						Arguments: `{"cmd":"test"}`,
					},
				},
			},
		},
	}
	_, params := convertMessages(msgs)
	require.Len(t, params, 2)
}

func TestConvertMessages_ToolResult(t *testing.T) {
	msgs := []ports.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: "result text", ToolCallID: "call_1"},
	}
	_, params := convertMessages(msgs)
	require.Len(t, params, 2)
}

// ---------------------------------------------------------------------------
// User block conversion
// ---------------------------------------------------------------------------

func TestConvertUserBlocks_TextOnly(t *testing.T) {
	msg := ports.ChatMessage{Role: "user", Content: "hello"}
	blocks := convertUserBlocks(msg)

	require.Len(t, blocks, 1)
	require.NotNil(t, blocks[0].OfText)
	assert.Equal(t, "hello", blocks[0].OfText.Text)
}

func TestConvertUserBlocks_ImageURL(t *testing.T) {
	msg := ports.ChatMessage{
		Role:    "user",
		Content: "look at this",
		Blocks: []ports.ContentBlock{
			{Type: ports.ContentBlockText, Text: "look at this"},
			{Type: ports.ContentBlockImage, URL: "https://example.com/img.jpg", MIMEType: "image/jpeg"},
		},
	}
	blocks := convertUserBlocks(msg)

	require.Len(t, blocks, 2)
	assert.NotNil(t, blocks[0].OfText)
	require.NotNil(t, blocks[1].OfImage)
	require.NotNil(t, blocks[1].OfImage.Source.OfURL)
	assert.Equal(t, "https://example.com/img.jpg", blocks[1].OfImage.Source.OfURL.URL)
}

func TestConvertUserBlocks_ImageBase64(t *testing.T) {
	msg := ports.ChatMessage{
		Role: "user",
		Blocks: []ports.ContentBlock{
			{Type: ports.ContentBlockImage, Data: []byte{0xFF, 0xD8, 0xFF}, MIMEType: "image/jpeg"},
		},
	}
	blocks := convertUserBlocks(msg)

	require.Len(t, blocks, 1)
	require.NotNil(t, blocks[0].OfImage)
	require.NotNil(t, blocks[0].OfImage.Source.OfBase64)
	assert.Equal(t, "/9j/", blocks[0].OfImage.Source.OfBase64.Data[:4])
}

func TestConvertUserBlocks_AudioSkipped(t *testing.T) {
	msg := ports.ChatMessage{
		Role: "user",
		Blocks: []ports.ContentBlock{
			{Type: ports.ContentBlockText, Text: "transcribe"},
			{Type: ports.ContentBlockAudio, Data: []byte{0x01}, MIMEType: "audio/wav"},
		},
	}
	blocks := convertUserBlocks(msg)

	require.Len(t, blocks, 1)
	assert.NotNil(t, blocks[0].OfText)
	assert.Equal(t, "transcribe", blocks[0].OfText.Text)
}

func TestConvertUserBlocks_FallsBackToContentWhenAllAudio(t *testing.T) {
	msg := ports.ChatMessage{
		Role:    "user",
		Content: "voice message",
		Blocks: []ports.ContentBlock{
			{Type: ports.ContentBlockAudio, Data: []byte{0x01}, MIMEType: "audio/wav"},
		},
	}
	blocks := convertUserBlocks(msg)

	require.Len(t, blocks, 1)
	assert.NotNil(t, blocks[0].OfText)
	assert.Equal(t, "voice message", blocks[0].OfText.Text)
}

// ---------------------------------------------------------------------------
// Tool conversion
// ---------------------------------------------------------------------------

func TestConvertTools(t *testing.T) {
	tools := []ports.Tool{
		{
			Type: "function",
			Function: &ports.FunctionTool{
				Name:        "mcp:server:read",
				Description: "Read a file",
				Parameters: map[string]interface{}{
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		{Type: "function", Function: nil}, // should be skipped
	}

	result := convertTools(tools)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].OfTool)
	assert.Equal(t, "mcp__server__read", result[0].OfTool.Name)
}

func TestConvertTools_NoProperties(t *testing.T) {
	tools := []ports.Tool{
		{
			Type: "function",
			Function: &ports.FunctionTool{
				Name:        "ping",
				Description: "Ping",
				Parameters:  map[string]interface{}{},
			},
		},
	}

	result := convertTools(tools)
	require.Len(t, result, 1)
}
