# Responses API with Chat Completions Backend Test

This test verifies that the OpenAI Responses API (`/v1/responses`) can successfully call providers that only support the Chat Completions API (`/v1/chat/completions`).

## Purpose

Demonstrates that AxonHub's transformer architecture allows:
- Client uses `/v1/responses` API format
- AxonHub converts to unified format
- Provider receives `/v1/chat/completions` format
- Response flows back through transformers to client

## Test Cases

- **TestResponsesWithChatCompletionsBackend**: Basic question using responses API with chat completions provider
- **TestResponsesStreamingWithChatCompletionsBackend**: Streaming responses through chat completions provider
- **TestResponsesConversationWithChatCompletionsBackend**: Multi-turn conversation via responses API

## Configuration

Uses standard OpenAI/DeepSeek channels (chat completions providers) but calls them via the responses API endpoint.
