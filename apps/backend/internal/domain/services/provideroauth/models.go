package provideroauth

// ModelInfo describes a model available through an OAuth provider.
type ModelInfo struct {
	ID      string
	Name    string
	Default bool
}

// ProviderModels maps OAuth provider IDs to their available models.
var ProviderModels = map[string][]ModelInfo{
	"openai-codex": {
		{ID: "gpt-5.4", Name: "GPT-5.4", Default: true},
		{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
		{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark"},
		{ID: "gpt-5.2", Name: "GPT-5.2"},
		{ID: "gpt-5.1", Name: "GPT-5.1"},
		{ID: "gpt-5.1-codex-max", Name: "GPT-5.1 Codex Max"},
		{ID: "gpt-5.1-codex-mini", Name: "GPT-5.1 Codex Mini"},
	},
	"anthropic": {
		{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", Default: true},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"},
	},
	"github-copilot": {
		{ID: "claude-opus-4.6", Name: "Claude Opus 4.6", Default: true},
		{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5"},
		{ID: "claude-sonnet-4", Name: "Claude Sonnet 4"},
		{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5"},
	},
	"google-gemini-cli": {
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Default: true},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash"},
		{ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash"},
		{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview"},
	},
	"google-antigravity": {
		{ID: "claude-opus-4-6-thinking", Name: "Claude Opus 4.6 (Thinking)", Default: true},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		{ID: "gemini-3-flash", Name: "Gemini 3 Flash"},
		{ID: "gemini-3.1-pro-high", Name: "Gemini 3.1 Pro High"},
		{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B Medium"},
	},
}

// GetDefaultModel returns the default model ID for the given provider.
// Returns an empty string if the provider is not found.
func GetDefaultModel(providerID string) string {
	models, ok := ProviderModels[providerID]
	if !ok {
		return ""
	}
	for _, m := range models {
		if m.Default {
			return m.ID
		}
	}
	// Fallback to first model if none marked as default.
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}

// GetModels returns all models available for the given provider.
// Returns nil if the provider is not found.
func GetModels(providerID string) []ModelInfo {
	return ProviderModels[providerID]
}
