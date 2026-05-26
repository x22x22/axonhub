package orchestrator

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestApplyTransformOptions_ReplaceDeveloperRoleWithSystem(t *testing.T) {
	developerContent := "dev"
	userContent := "hi"
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
			{Role: "user", Content: llm.MessageContent{Content: &userContent}},
		},
	}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			ReplaceDeveloperRoleWithSystem: true,
		},
	}

	result := applyTransformOptions(req, settings)

	require.NotSame(t, req, result)
	require.Equal(t, "system", result.Messages[0].Role)
	require.Equal(t, "user", result.Messages[1].Role)
}

func TestApplyTransformOptions_KeepDeveloperRoleWhenDisabled(t *testing.T) {
	developerContent := "dev"
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
		},
	}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			ReplaceDeveloperRoleWithSystem: false,
		},
	}

	result := applyTransformOptions(req, settings)

	require.Same(t, req, result)
	require.Equal(t, "developer", result.Messages[0].Role)
}

func TestApplyTransformOptions_MergeDeveloperRoleIntoSystem(t *testing.T) {
	systemContent := "sys"
	developerContent := "dev"
	userContent := "hi"
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "system", Content: llm.MessageContent{Content: &systemContent}},
			{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
			{Role: "user", Content: llm.MessageContent{Content: &userContent}},
		},
	}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			MergeDeveloperRoleIntoSystem: true,
		},
	}

	result := applyTransformOptions(req, settings)

	require.NotSame(t, req, result)
	require.Len(t, result.Messages, 2)
	require.Equal(t, "system", result.Messages[0].Role)
	require.Equal(t, []llm.MessageContentPart{
		{Type: "text", Text: &systemContent},
		{Type: "text", Text: &developerContent},
	}, result.Messages[0].Content.MultipleContent)
	require.Equal(t, "user", result.Messages[1].Role)
}

func TestApplyTransformOptions_ReplaceDeveloperRoleWithSystemTakesPrecedence(t *testing.T) {
	developerContent := "dev"
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
		},
	}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			ReplaceDeveloperRoleWithSystem: true,
			MergeDeveloperRoleIntoSystem:   true,
		},
	}

	result := applyTransformOptions(req, settings)

	require.Len(t, result.Messages, 1)
	require.Equal(t, "system", result.Messages[0].Role)
	require.NotEmpty(t, result.Messages[0].Content.Content)
	require.Empty(t, result.Messages[0].Content.MultipleContent)
}

func TestApplyTransformOptions_NilSettings(t *testing.T) {
	req := &llm.Request{Model: "test-model"}

	result := applyTransformOptions(req, nil)

	require.Same(t, req, result)
}

func TestApplyTransformOptions_ForceArrayInstructions(t *testing.T) {
	req := &llm.Request{Model: "test-model"}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			ForceArrayInstructions: true,
		},
	}

	result := applyTransformOptions(req, settings)

	require.NotSame(t, req, result)
	require.Equal(t, lo.ToPtr(true), result.TransformOptions.ArrayInstructions)
}

func TestApplyTransformOptions_ForceArrayInputs(t *testing.T) {
	req := &llm.Request{Model: "test-model"}

	settings := &objects.ChannelSettings{
		TransformOptions: objects.TransformOptions{
			ForceArrayInputs: true,
		},
	}

	result := applyTransformOptions(req, settings)

	require.NotSame(t, req, result)
	require.Equal(t, lo.ToPtr(true), result.TransformOptions.ArrayInputs)
}

func TestReplaceDeveloperRoleWithSystem(t *testing.T) {
	tests := []struct {
		name     string
		messages []llm.Message
		expected []string
	}{
		{
			name:     "empty messages",
			messages: []llm.Message{},
			expected: []string{},
		},
		{
			name: "developer role replaced",
			messages: []llm.Message{
				{Role: "developer"},
				{Role: "user"},
			},
			expected: []string{"system", "user"},
		},
		{
			name: "Developer case insensitive",
			messages: []llm.Message{
				{Role: "Developer"},
				{Role: "DEVELOPER"},
			},
			expected: []string{"system", "system"},
		},
		{
			name: "no developer role",
			messages: []llm.Message{
				{Role: "system"},
				{Role: "user"},
			},
			expected: []string{"system", "user"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceDeveloperRoleWithSystem(tt.messages)
			for i, role := range tt.expected {
				require.Equal(t, role, result[i].Role)
			}
		})
	}
}

func TestMergeDeveloperRoleIntoSystem(t *testing.T) {
	systemContent := "system text"
	developerContent := "developer text"
	secondDeveloperContent := "developer text 2"
	userContent := "user text"
	multipleContentText := "developer part"

	tests := []struct {
		name     string
		messages []llm.Message
		assert   func(t *testing.T, result []llm.Message)
	}{
		{
			name:     "empty messages",
			messages: []llm.Message{},
			assert: func(t *testing.T, result []llm.Message) {
				require.Empty(t, result)
			},
		},
		{
			name: "no developer role returns original shape",
			messages: []llm.Message{
				{Role: "system", Content: llm.MessageContent{Content: &systemContent}},
				{Role: "user", Content: llm.MessageContent{Content: &userContent}},
			},
			assert: func(t *testing.T, result []llm.Message) {
				require.Len(t, result, 2)
				require.Equal(t, "system", result[0].Role)
				require.NotNil(t, result[0].Content.Content)
			},
		},
		{
			name: "developer merges after system and before non instruction messages",
			messages: []llm.Message{
				{Role: "system", Content: llm.MessageContent{Content: &systemContent}},
				{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
				{Role: "user", Content: llm.MessageContent{Content: &userContent}},
				{Role: "developer", Content: llm.MessageContent{Content: &secondDeveloperContent}},
			},
			assert: func(t *testing.T, result []llm.Message) {
				require.Len(t, result, 2)
				require.Equal(t, "system", result[0].Role)
				require.Equal(t, []llm.MessageContentPart{
					{Type: "text", Text: &systemContent},
					{Type: "text", Text: &developerContent},
					{Type: "text", Text: &secondDeveloperContent},
				}, result[0].Content.MultipleContent)
				require.Equal(t, "user", result[1].Role)
			},
		},
		{
			name: "developer only becomes one system message",
			messages: []llm.Message{
				{Role: "developer", Content: llm.MessageContent{Content: &developerContent}},
			},
			assert: func(t *testing.T, result []llm.Message) {
				require.Len(t, result, 1)
				require.Equal(t, "system", result[0].Role)
				require.Equal(t, []llm.MessageContentPart{{Type: "text", Text: &developerContent}}, result[0].Content.MultipleContent)
			},
		},
		{
			name: "multiple content is preserved",
			messages: []llm.Message{
				{Role: "Developer", Content: llm.MessageContent{MultipleContent: []llm.MessageContentPart{{Type: "text", Text: &multipleContentText}}}},
			},
			assert: func(t *testing.T, result []llm.Message) {
				require.Len(t, result, 1)
				require.Equal(t, []llm.MessageContentPart{{Type: "text", Text: &multipleContentText}}, result[0].Content.MultipleContent)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeDeveloperRoleIntoSystem(tt.messages)
			tt.assert(t, result)
		})
	}
}
