package main

import (
	"strings"
	"testing"

	"github.com/looplj/axonhub/openai_test/internal/testutil"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// TestResponsesWithChatCompletionsBackend verifies that /v1/responses API
// can successfully call providers that only support /v1/chat/completions.
// This test demonstrates the transformer architecture's ability to bridge
// different API formats transparently.
func TestResponsesWithChatCompletionsBackend(t *testing.T) {
	helper := testutil.NewTestHelper(t, "TestResponsesWithChatCompletionsBackend")
	helper.PrintHeaders(t)

	ctx := helper.CreateTestContext()

	// Simple question to test the format conversion
	question := "What is the capital of France? Answer in one word."

	t.Logf("Sending question via /v1/responses to chat completions backend: %s", question)

	// Use Responses API format
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(helper.GetModel()),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(question),
		},
	}

	// Make the API call through /v1/responses endpoint
	resp, err := helper.CreateResponseWithHeaders(ctx, params)
	helper.AssertNoError(t, err, "Failed to get response from chat completions backend")

	if resp == nil {
		t.Fatal("Response is nil")
	}

	output := resp.OutputText()
	t.Logf("Response from chat completions backend: %s", output)

	if output == "" {
		t.Fatal("Expected non-empty output")
	}

	// Verify the answer is correct
	if !testutil.ContainsCaseInsensitive(output, "paris") {
		t.Errorf("Expected response to contain 'Paris', got: %s", output)
	}

	t.Log("✓ Successfully called chat completions backend via responses API")
}

// TestResponsesStreamingWithChatCompletionsBackend verifies streaming works
// when using responses API with chat completions providers.
func TestResponsesStreamingWithChatCompletionsBackend(t *testing.T) {
	helper := testutil.NewTestHelper(t, "TestResponsesStreamingWithChatCompletionsBackend")
	helper.PrintHeaders(t)

	ctx := helper.CreateTestContext()

	question := "Count from 1 to 5, one number per line."

	t.Logf("Sending streaming request via /v1/responses to chat completions backend: %s", question)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(helper.GetModel()),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(question),
		},
	}

	// Make streaming API call
	stream := helper.CreateResponseStreamingWithHeaders(ctx, params)
	helper.AssertNoError(t, stream.Err(), "Failed to start streaming from chat completions backend")

	// Read and process the stream
	var fullContent strings.Builder
	var chunks int

	for stream.Next() {
		event := stream.Current()
		chunks++

		// Handle text delta events
		if event.Type == "response.output_text.delta" && event.Delta != "" {
			fullContent.WriteString(event.Delta)
		}
	}

	// Check for stream errors
	if err := stream.Err(); err != nil {
		helper.AssertNoError(t, err, "Stream error occurred")
	}

	finalContent := fullContent.String()
	t.Logf("Received %d streaming events", chunks)
	t.Logf("Final streamed content: %s", finalContent)

	// Validate streaming response
	if chunks == 0 {
		t.Error("Expected at least one streaming event")
	}

	if len(finalContent) == 0 {
		t.Error("Expected non-empty streamed content")
	}

	t.Log("✓ Successfully streamed from chat completions backend via responses API")
}

// TestResponsesConversationWithChatCompletionsBackend verifies multi-turn
// conversations work when using responses API with chat completions providers.
func TestResponsesConversationWithChatCompletionsBackend(t *testing.T) {
	helper := testutil.NewTestHelper(t, "TestResponsesConversationWithChatCompletionsBackend")
	helper.PrintHeaders(t)

	ctx := helper.CreateTestContext()

	// First turn
	t.Log("Turn 1: Asking about a city")
	params1 := responses.ResponseNewParams{
		Model: shared.ResponsesModel(helper.GetModel()),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("I'm thinking of visiting Rome. What's one famous landmark there?"),
		},
	}

	resp1, err := helper.CreateResponseWithHeaders(ctx, params1)
	helper.AssertNoError(t, err, "Failed on first turn")

	if resp1 == nil {
		t.Fatal("First response is nil")
	}

	output1 := resp1.OutputText()
	t.Logf("Turn 1 response: %s", output1)

	if output1 == "" {
		t.Fatal("Expected non-empty output from first turn")
	}

	// Second turn with context
	t.Log("Turn 2: Follow-up question using previous_response_id")
	params2 := responses.ResponseNewParams{
		Model: shared.ResponsesModel(helper.GetModel()),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("What century was it built?"),
		},
		PreviousResponseID: openai.String(resp1.ID),
	}

	resp2, err := helper.CreateResponseWithHeaders(ctx, params2)
	helper.AssertNoError(t, err, "Failed on second turn")

	if resp2 == nil {
		t.Fatal("Second response is nil")
	}

	output2 := resp2.OutputText()
	t.Logf("Turn 2 response: %s", output2)

	if output2 == "" {
		t.Fatal("Expected non-empty output from second turn")
	}

	// Verify the response includes context awareness (mentions a century)
	hasTimeReference := testutil.ContainsAnyCaseInsensitive(output2,
		"century", "centuries", "BC", "AD", "year", "built", "constructed")

	if !hasTimeReference {
		t.Errorf("Expected context-aware response about when it was built, got: %s", output2)
	}

	t.Log("✓ Successfully maintained conversation context through chat completions backend")
}
