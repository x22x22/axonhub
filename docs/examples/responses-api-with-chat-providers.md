# Example: Using OpenAI Responses API with Providers that Only Support Chat Completions

This example demonstrates how AxonHub enables you to use the OpenAI Responses API (`/v1/responses`) to call LLM providers that only support the Chat Completions API (`/v1/chat/completions`).

## Scenario

You want to use the modern OpenAI Responses API, but your provider (e.g., DeepSeek, Moonshot, or a custom OpenAI-compatible service) only supports the older Chat Completions API.

**With AxonHub, this works automatically!**

## Step 1: Configure Your Channel

Create a channel for your provider in AxonHub. Even though the provider only supports Chat Completions, you configure it normally:

### Example: DeepSeek Channel

```yaml
channels:
  - type: "deepseek"  # This uses Chat Completions format internally
    name: "DeepSeek"
    base_url: "https://api.deepseek.com/v1"
    credentials:
      apiKey: "your-deepseek-api-key"
    supported_models:
      - "deepseek-chat"
      - "deepseek-reasoner"
    status: "enabled"
```

## Step 2: Use the Responses API

Now you can call your provider using the Responses API, even though it doesn't natively support it!

### Python Example

```python
from openai import OpenAI

# Point to your AxonHub gateway
client = OpenAI(
    base_url="https://your-axonhub-gateway.com/v1",
    api_key="your-axonhub-api-key"
)

# Use the Responses API - AxonHub will convert to Chat Completions for the provider
response = client.responses.create(
    model="deepseek-chat",
    instructions="You are a helpful AI assistant.",
    input="What is the capital of France?",
    temperature=0.7,
    max_output_tokens=1000
)

# Access the response
for item in response.output:
    if item.type == "message":
        print(item.content[0].text)
```

## Conclusion

AxonHub's cross-format conversion makes it a true universal LLM gateway. No configuration, no special setup, no extra cost. It just works! 🎉

See [cross-format-conversion.md](../cross-format-conversion.md) for more details.
