# OAuth Provider API Adapters - Implementation Plan

## Problem
The OAuth login flows work, but the API adapters don't. Each provider
needs a completely different API endpoint, headers, and request format.
We can't reuse the existing OpenAI/Anthropic adapters — they target
platform APIs, not the subscription-based OAuth endpoints.

## Provider Requirements

### 1. OpenAI Codex (ChatGPT Plus/Pro)
- **Endpoint:** `https://chatgpt.com/backend-api/codex/responses`
- **Auth:** `Authorization: Bearer <JWT>` + `chatgpt-account-id: <from JWT>`
- **Headers:** `originator: openlobster`, `OpenAI-Beta: responses=experimental`
- **Format:** OpenAI Responses API (NOT chat completions)
- **Models:** gpt-5.1, gpt-5.1-codex-max, gpt-5.1-codex-mini, gpt-5.2, gpt-5.3-codex, gpt-5.4
- **Notes:** Account ID extracted from JWT claim `https://api.openai.com/auth`.chatgpt_account_id

### 2. Anthropic (Claude Pro/Max)
- **Endpoint:** `https://api.anthropic.com/v1/messages`
- **Auth:** `Authorization: Bearer <sk-ant-oat* token>` (NOT x-api-key)
- **Headers:** `anthropic-beta: claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14`, `user-agent: claude-cli/2.1.75`, `x-app: cli`
- **Format:** Standard Anthropic Messages API, but tool names remapped to Claude Code casing
- **Models:** claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5
- **Notes:** OAuth tokens start with `sk-ant-oat`, use authToken (Bearer) not apiKey (x-api-key)

### 3. GitHub Copilot
- **Endpoint:** Dynamic from token's `proxy-ep` field → `https://api.individual.githubcopilot.com/v1/messages`
- **Auth:** `Authorization: Bearer <copilot session token>` (NOT the GitHub token)
- **Headers:** `User-Agent: GitHubCopilotChat/0.35.0`, `Editor-Version: vscode/1.107.0`, `Editor-Plugin-Version: copilot-chat/0.35.0`, `Copilot-Integration-Id: vscode-chat`
- **Format:** Anthropic Messages API (Copilot proxies Claude models)
- **Models:** claude-opus-4.6, claude-sonnet-4.5, claude-sonnet-4, claude-haiku-4.5
- **Notes:** After login, must enable each model via POST to `/models/{id}/policy`

### 4. Google Gemini CLI (Cloud Code Assist)
- **Endpoint:** `https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse`
- **Auth:** `Authorization: Bearer <access_token>`
- **Headers:** `User-Agent: google-cloud-sdk vscode_cloudshelleditor/0.1`, `X-Goog-Api-Client: gl-node/22.17.0`
- **Format:** Cloud Code Assist custom format with `project`, `model`, `request` wrapper
- **Models:** gemini-2.0-flash, gemini-2.5-flash, gemini-2.5-pro, gemini-3-flash-preview
- **Notes:** Needs project ID (from OAuth credentials). Request body is NOT standard Gemini API.

### 5. Google Antigravity
- **Endpoint:** `https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:streamGenerateContent?alt=sse`
- **Auth:** `Authorization: Bearer <access_token>`
- **Headers:** `User-Agent: antigravity/1.18.4 darwin/arm64`
- **Format:** Same as Gemini CLI but with `requestType: "agent"`, `userAgent: "antigravity"`
- **Models:** claude-opus-4-6-thinking, claude-sonnet-4-6, gemini-3-flash, gemini-3.1-pro, gpt-oss-120b
- **Notes:** Falls back through sandbox → autopush → prod endpoints

## Implementation Plan

### Step 1: New adapter package `adapters/ai/oauthproviders/`
Create provider-specific adapters that implement `ports.AIProviderPort`:

```
adapters/ai/oauthproviders/
├── codex_adapter.go        # OpenAI Codex Responses API
├── anthropic_adapter.go    # Anthropic Messages API (OAuth Bearer mode)
├── copilot_adapter.go      # GitHub Copilot (Anthropic Messages via proxy)
├── gemini_adapter.go       # Google Cloud Code Assist
├── antigravity_adapter.go  # Google Antigravity (Cloud Code Assist variant)
├── models.go               # Per-provider model lists
└── adapter_test.go         # Tests with mock HTTP servers
```

Each adapter:
- Implements `ports.AIProviderPort` (Chat, ChatWithAudio, ChatToAudio, etc.)
- Takes credentials (token, account ID, project ID) at construction
- Sets provider-specific headers on every request
- Translates between OpenLobster's `ports.ChatRequest` and provider's format
- Translates responses back to `ports.ChatResponse`

### Step 2: Model registry
New file `models.go` that defines available models per provider:
```go
var ProviderModels = map[string][]ModelInfo{
    "openai-codex": {
        {ID: "gpt-5.4", Name: "GPT-5.4", Default: true},
        {ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
        ...
    },
    "github-copilot": {
        {ID: "claude-opus-4.6", Name: "Claude Opus 4.6", Default: true},
        ...
    },
    ...
}
```

### Step 3: Update `buildAIProviderFromConfigWithOAuth`
Replace the current switch that creates standard adapters with Codex/OAuth tokens.
Instead, create the correct adapter based on the OAuth provider ID.

### Step 4: GraphQL query for available models
Add `providerOAuthModels(provider: String!): [OAuthModel!]!` query so the
frontend can populate the model dropdown based on which OAuth provider is selected.

### Step 5: Fix the Settings UI
When a user logs in via OAuth, the UI should:
1. Auto-select the OAuth provider as the active provider
2. Show a model dropdown populated from `providerOAuthModels`
3. Hide the API key field (not needed for OAuth)
4. Show which account is active

### Step 6: Auto-configure on OAuth login
When `initiateProviderOAuth` completes successfully:
1. Set `providers.oauth_provider` to the provider ID
2. Set the model to the provider's default model
3. Trigger a soft reboot to activate the new provider

## Effort Estimate
- Adapters: ~300 lines each × 5 = ~1500 lines
- Model registry: ~100 lines
- Wiring + config: ~100 lines
- GraphQL + frontend: ~200 lines
- Tests: ~500 lines
- Total: ~2400 lines
