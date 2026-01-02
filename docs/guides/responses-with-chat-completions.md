# Using /v1/responses API with Chat Completions Providers

This document explains how AxonHub allows you to use the OpenAI Responses API (`/v1/responses`) to call providers that only support the Chat Completions API (`/v1/chat/completions`).

## Overview

AxonHub's transformer architecture provides seamless format translation between different LLM APIs. You can use the `/v1/responses` endpoint to call any OpenAI-compatible provider, even if they don't natively support the Responses API format.

## How It Works

The request flow is:

1. **Client** → Sends request to `/v1/responses` in Responses API format
2. **Inbound Transformer** → Converts Responses format to unified internal format
3. **Orchestrator** → Selects appropriate channel based on model
4. **Outbound Transformer** → Converts unified format to provider's native format (e.g., Chat Completions)
5. **Provider** → Processes request in its native format
6. **Response** → Flows back through transformers to client in Responses API format

## Configuration

To use this capability, simply:

1. Configure a channel with a model that supports Chat Completions (e.g., OpenAI, DeepSeek, Moonshot, etc.)
2. Call the `/v1/responses` endpoint with that model

No special configuration is needed! The transformer architecture handles the format conversion automatically.

### Example Channel Types

These channel types support Chat Completions and work with `/v1/responses`:

- `openai` - Official OpenAI API
- `deepseek` - DeepSeek API
- `moonshot` - Moonshot AI
- `zhipu` - ChatGLM / Zhipu AI
- `doubao` - ByteDance Doubao
- `siliconflow` - SiliconFlow
- `ppio` - PPio
- `vercel` - Vercel AI
- And many more...

## Usage Example

### Using OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-axonhub-api-key",
    base_url="http://localhost:8090/v1"
)

# Use responses API with a chat completions provider (e.g., deepseek-chat)
response = client.responses.create(
    model="deepseek-chat",
    input="What is the capital of France?"
)

print(response.output_text())
```

### Using curl

```bash
curl -X POST http://localhost:8090/v1/responses \
  -H "Authorization: Bearer your-axonhub-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "input": "What is the capital of France?"
  }'
```

## Supported Features

When using `/v1/responses` with Chat Completions providers, the following features are supported:

- ✅ Basic text generation
- ✅ Streaming responses
- ✅ Temperature and other sampling parameters
- ✅ Tool/function calling (where supported by provider)
- ✅ Multi-turn conversations via `previous_response_id`
- ✅ Custom metadata
- ✅ Token usage tracking

## Limitations

Some Responses API features may have limited support depending on the underlying provider:

- Image generation tools (requires provider support)
- Native reasoning models features (requires provider support)
- Prompt caching (requires provider support)

## Benefits

Using `/v1/responses` with Chat Completions providers offers several advantages:

1. **Unified API**: Use the same client code across different providers
2. **Simplified context management**: Use `previous_response_id` instead of managing message arrays
3. **Modern API design**: Responses API is OpenAI's newer, more streamlined interface
4. **Provider flexibility**: Switch providers without changing client code

## Testing

Integration tests are available in `integration_test/openai/responses/chat_completions_backend/` demonstrating this capability.

## See Also

- [OpenAI Responses API Documentation](https://platform.openai.com/docs/api-reference/responses)
- [AxonHub Architecture Overview](../README.md)
- Channel Configuration (see main README.md)
