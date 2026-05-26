# Cross-Format API Conversion

## Overview

AxonHub supports **cross-format API conversion**, allowing you to call LLM providers using API formats they don't natively support. This is achieved through the gateway's bidirectional transformation architecture.

## How It Works

AxonHub's architecture consists of three layers:

```
Client Request (Specific API Format)
        ↓
[Inbound Transformer] → Converts to Unified llm.Request
        ↓
[Orchestrator & Channel Selection] → Selects appropriate channels based on model
        ↓
[Outbound Transformer] → Converts from Unified llm.Request to Provider's API Format
        ↓
Provider API (Specific API Format)
```

### Key Principle: Format-Agnostic Channel Selection

**Channel selection is based on the requested model, not the API format.** This means:

- A client can call `/v1/responses` (Responses API)
- The gateway converts it to the unified `llm.Request` format
- The orchestrator selects a channel that supports the requested model
- If the channel uses `/v1/chat/completions`, the gateway automatically converts the request
- The response is converted back to Responses API format for the client

## Supported Conversions

### OpenAI Responses API ↔ Chat Completions API

**Use Case**: Access OpenAI-compatible providers that only support `/v1/chat/completions` through the `/v1/responses` endpoint.

#### Example: Using DeepSeek with Responses API

DeepSeek natively supports only `/v1/chat/completions`, but you can call it using `/v1/responses`:

**Channel Configuration:**
```yaml
type: "openai"  # or "deepseek" - both use Chat Completions format
name: "DeepSeek"
base_url: "https://api.deepseek.com/v1"
supported_models:
  - "deepseek-chat"
  - "deepseek-reasoner"
```

**Client Request to Gateway:**
```bash
curl https://your-gateway.com/v1/responses \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "instructions": "You are a helpful assistant.",
    "input": "What is the capital of France?",
    "temperature": 0.7
  }'
```

**Gateway Processing:**
1. Receives request in Responses API format
2. Converts to unified `llm.Request` format
3. Selects DeepSeek channel (supports "deepseek-chat")
4. Converts to Chat Completions format for DeepSeek
5. Receives response from DeepSeek
6. Converts response back to Responses API format
7. Returns to client

#### Feature Conversion

The following features are automatically converted between formats:

| Feature | Responses API | Chat Completions API | Status |
|---------|---------------|----------------------|--------|
| System Instructions | `instructions` | System message in `messages` | ✅ Supported |
| User Input | `input` (string or array) | User message in `messages` | ✅ Supported |
| Model Selection | `model` | `model` | ✅ Supported |
| Temperature | `temperature` | `temperature` | ✅ Supported |
| Function Tools | `tools` | `tools` | ✅ Supported |
| Tool Calls | Output items with `function_call` | `tool_calls` in message | ✅ Supported |
| Reasoning | Output items with `reasoning` | `reasoning_content` in message | ✅ Supported |
| Streaming | `stream` | `stream` | ✅ Supported |
| Max Tokens | `max_output_tokens` | `max_completion_tokens` | ✅ Supported |

### Other Supported Conversions

The same principle applies to other API formats:

#### OpenAI Chat Completions → Anthropic Messages
- Channel type: `anthropic`, `anthropic_aws`, `anthropic_gcp`
- Client uses `/v1/chat/completions`
- Gateway converts to Anthropic Messages format

#### OpenAI Chat Completions → Gemini Contents
- Channel type: `gemini`, `gemini_vertex`
- Client uses `/v1/chat/completions`
- Gateway converts to Gemini Contents format

#### And vice versa for all combinations

## Configuration

### No Special Configuration Required

Cross-format conversion works automatically. You only need to:

1. **Create channels** with the appropriate provider type
2. **Configure model mappings** (if needed) to map model names
3. **Ensure channels support the requested model**

### Model Mapping Example

If you want to expose a provider's model under a different name:

```json
{
  "modelMappings": [
    {
      "from": "gpt-4",
      "to": "deepseek-chat"
    }
  ]
}
```

This allows clients to request `gpt-4` and get routed to DeepSeek's `deepseek-chat` model.

## Testing

The cross-format conversion is tested in `internal/llm/transformer/crossformat_test.go`:

```bash
go test -v -run TestCrossFormatConversion ./internal/llm/transformer/
```

## Limitations

1. **Feature Parity**: Not all features are available in all API formats. The gateway converts what it can, but some provider-specific features may not translate.

2. **Streaming**: Both formats must support streaming for streaming to work end-to-end.

3. **Response Format**: The response is always returned in the format matching the client's request (inbound API format).

## Architecture Benefits

This design provides several advantages:

1. **Flexibility**: Use any API format with any provider
2. **Compatibility**: Existing applications don't need changes
3. **Simplicity**: No special configuration needed
4. **Transparency**: Conversion happens automatically in the gateway

## Frequently Asked Questions

### Q: Can I use model mapping to convert API formats?

**A: No.** Model mapping only converts model names, not API formats. API format conversion happens automatically based on channel type and inbound request format.

### Q: Do I need to create separate channels for different API formats?

**A: No.** One channel can serve requests from multiple API formats. The gateway handles conversion automatically.

### Q: What if a feature doesn't exist in the target format?

**A: The gateway converts what it can and preserves metadata when possible.** Some features may be silently ignored if the target format doesn't support them.

### Q: Can I see what's being converted?

**A: Yes.** Enable debug logging to see the request transformations:
```yaml
log:
  level: "debug"
```

## Example Use Cases

### 1. Unified API for Multiple Providers

Use Responses API for all your models, even if providers use different native formats:

```python
# Same API for all providers
response = openai.responses.create(
    model="gpt-4",  # Routes to OpenAI Chat Completions
    instructions="You are a helpful assistant.",
    input="Hello!"
)

response = openai.responses.create(
    model="claude-3-opus",  # Routes to Anthropic Messages
    instructions="You are a helpful assistant.",
    input="Hello!"
)

response = openai.responses.create(
    model="deepseek-chat",  # Routes to DeepSeek Chat Completions
    instructions="You are a helpful assistant.",
    input="Hello!"
)
```

### 2. Gradual Migration

Migrate to a new API format without changing your application:

```python
# Your app uses Responses API
# But you can route to any provider transparently
# No code changes needed when switching providers
```

### 3. Multi-Provider Fallback

Configure multiple channels with different providers, all accessible through your preferred API format.

## Conclusion

Cross-format conversion is a powerful feature that makes AxonHub a truly unified gateway. It eliminates the need to learn multiple API formats and allows seamless provider switching without application changes.
