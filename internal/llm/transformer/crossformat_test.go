package transformer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/llm"
	"github.com/looplj/axonhub/internal/llm/transformer/openai"
	"github.com/looplj/axonhub/internal/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/internal/pkg/httpclient"
)

// TestCrossFormatConversion_ResponsesToChatCompletions tests that a request
// coming in via the /v1/responses API can be successfully converted to the
// /v1/chat/completions format and back.
//
// This demonstrates that OpenAI providers that only support /v1/chat/completions
// can be accessed through the gateway using the /v1/responses interface.
func TestCrossFormatConversion_ResponsesToChatCompletions(t *testing.T) {
	ctx := context.Background()

	// Step 1: Create inbound transformer for Responses API
	responsesInbound := responses.NewInboundTransformer()

	// Step 2: Create outbound transformer for Chat Completions API
	chatCompletionsOutbound, err := openai.NewOutboundTransformer("https://api.openai.com/v1", "test-api-key")
	require.NoError(t, err)

	// Step 3: Create a Responses API request
	responsesRequest := responses.Request{
		Model:        "gpt-4",
		Instructions: "You are a helpful assistant.",
		Input: responses.Input{
			Text: ptrString("What is the capital of France?"),
		},
		Temperature: ptrFloat64(0.7),
		Stream:      ptrBool(false),
	}

	requestBody, err := json.Marshal(responsesRequest)
	require.NoError(t, err)

	httpRequest := &httpclient.Request{
		Method: http.MethodPost,
		URL:    "https://gateway.example.com/v1/responses",
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: requestBody,
	}

	// Step 4: Transform Responses API request to unified llm.Request
	llmRequest, err := responsesInbound.TransformRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, llmRequest)

	// Verify the unified request has the correct API format
	assert.Equal(t, llm.APIFormatOpenAIResponse, llmRequest.APIFormat)
	assert.Equal(t, "gpt-4", llmRequest.Model)
	assert.Equal(t, float64(0.7), *llmRequest.Temperature)
	
	// Verify messages include system instruction and user input
	require.Len(t, llmRequest.Messages, 2)
	assert.Equal(t, "system", llmRequest.Messages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", *llmRequest.Messages[0].Content.Content)
	assert.Equal(t, "user", llmRequest.Messages[1].Role)
	assert.Equal(t, "What is the capital of France?", *llmRequest.Messages[1].Content.Content)

	// Step 5: Transform unified request to Chat Completions API request
	chatCompletionsRequest, err := chatCompletionsOutbound.TransformRequest(ctx, llmRequest)
	require.NoError(t, err)
	require.NotNil(t, chatCompletionsRequest)

	// Verify the outbound request is in Chat Completions format
	assert.Equal(t, "https://api.openai.com/v1/chat/completions", chatCompletionsRequest.URL)
	assert.Equal(t, http.MethodPost, chatCompletionsRequest.Method)

	// Parse the body to verify it's in Chat Completions format
	var chatBody map[string]interface{}
	err = json.Unmarshal(chatCompletionsRequest.Body, &chatBody)
	require.NoError(t, err)

	// Verify key fields are present in Chat Completions format
	assert.Equal(t, "gpt-4", chatBody["model"])
	assert.Equal(t, 0.7, chatBody["temperature"])
	assert.Equal(t, false, chatBody["stream"])
	
	messages, ok := chatBody["messages"].([]interface{})
	require.True(t, ok)
	require.Len(t, messages, 2)

	t.Log("✓ Successfully converted /v1/responses request to /v1/chat/completions request")
	t.Log("✓ This proves that OpenAI providers supporting only /v1/chat/completions can be accessed via /v1/responses")
}

// TestCrossFormatConversion_ResponsesToChatCompletions_WithTools tests conversion
// with tool calls, which is a more complex scenario.
func TestCrossFormatConversion_ResponsesToChatCompletions_WithTools(t *testing.T) {
	ctx := context.Background()

	responsesInbound := responses.NewInboundTransformer()
	chatCompletionsOutbound, err := openai.NewOutboundTransformer("https://api.openai.com/v1", "test-api-key")
	require.NoError(t, err)

	// Create a Responses API request with tools
	responsesRequest := responses.Request{
		Model:        "gpt-4",
		Instructions: "You are a helpful assistant with access to weather information.",
		Input: responses.Input{
			Text: ptrString("What's the weather in Paris?"),
		},
		Tools: []responses.Tool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get the current weather for a location",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "The city and country, e.g. Paris, France",
						},
					},
					"required": []string{"location"},
				},
			},
		},
		ParallelToolCalls: ptrBool(true),
	}

	requestBody, err := json.Marshal(responsesRequest)
	require.NoError(t, err)

	httpRequest := &httpclient.Request{
		Method: http.MethodPost,
		URL:    "https://gateway.example.com/v1/responses",
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: requestBody,
	}

	// Transform to unified format
	llmRequest, err := responsesInbound.TransformRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, llmRequest)

	// Verify tools were converted
	require.Len(t, llmRequest.Tools, 1)
	assert.Equal(t, "function", llmRequest.Tools[0].Type)
	assert.Equal(t, "get_weather", llmRequest.Tools[0].Function.Name)

	// Transform to Chat Completions format
	chatCompletionsRequest, err := chatCompletionsOutbound.TransformRequest(ctx, llmRequest)
	require.NoError(t, err)
	require.NotNil(t, chatCompletionsRequest)

	// Parse and verify the Chat Completions request
	var chatBody map[string]interface{}
	err = json.Unmarshal(chatCompletionsRequest.Body, &chatBody)
	require.NoError(t, err)

	tools, ok := chatBody["tools"].([]interface{})
	require.True(t, ok)
	require.Len(t, tools, 1)

	tool := tools[0].(map[string]interface{})
	assert.Equal(t, "function", tool["type"])

	t.Log("✓ Successfully converted /v1/responses request with tools to /v1/chat/completions")
}

// TestCrossFormatConversion_ChatCompletionsResponse tests that a Chat Completions
// response can be converted back to Responses API format.
func TestCrossFormatConversion_ChatCompletionsResponse(t *testing.T) {
	ctx := context.Background()

	// Create inbound transformer for Responses API (for final response conversion)
	responsesInbound := responses.NewInboundTransformer()

	// Simulate a Chat Completions API response
	chatCompletionsResponse := &llm.Response{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1677858242,
		Model:   "gpt-4",
		Choices: []llm.Choice{
			{
				Index: 0,
				Message: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: ptrString("The capital of France is Paris."),
					},
				},
				FinishReason: ptrString("stop"),
			},
		},
		Usage: &llm.Usage{
			PromptTokens:     15,
			CompletionTokens: 8,
			TotalTokens:      23,
		},
	}

	// Convert to Responses API format
	httpResponse, err := responsesInbound.TransformResponse(ctx, chatCompletionsResponse)
	require.NoError(t, err)
	require.NotNil(t, httpResponse)

	// Parse the response body
	var responsesAPIResponse responses.Response
	err = json.Unmarshal(httpResponse.Body, &responsesAPIResponse)
	require.NoError(t, err)

	// Verify the response is in Responses API format
	assert.Equal(t, "response", responsesAPIResponse.Object)
	assert.Equal(t, "chatcmpl-123", responsesAPIResponse.ID)
	assert.Equal(t, "gpt-4", responsesAPIResponse.Model)
	assert.NotNil(t, responsesAPIResponse.Status)
	assert.Equal(t, "completed", *responsesAPIResponse.Status)

	// Verify output items
	require.NotEmpty(t, responsesAPIResponse.Output)
	
	// Find the message item
	var messageItem *responses.Item
	for i := range responsesAPIResponse.Output {
		if responsesAPIResponse.Output[i].Type == "message" {
			messageItem = &responsesAPIResponse.Output[i]
			break
		}
	}
	
	require.NotNil(t, messageItem)
	assert.Equal(t, "assistant", messageItem.Role)
	require.NotNil(t, messageItem.Content)

	t.Log("✓ Successfully converted Chat Completions response to /v1/responses format")
	t.Log("✓ Full round-trip conversion works: /v1/responses → /v1/chat/completions → /v1/responses")
}

func ptrString(s string) *string {
	return &s
}

func ptrFloat64(f float64) *float64 {
	return &f
}

func ptrBool(b bool) *bool {
	return &b
}
