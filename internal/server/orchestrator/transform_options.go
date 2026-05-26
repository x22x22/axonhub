package orchestrator

import (
	"strings"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// applyTransformOptions applies channel transform options to create a new llm.Request.
// It creates a new request instead of modifying the original one.
func applyTransformOptions(req *llm.Request, channelSettings *objects.ChannelSettings) *llm.Request {
	if channelSettings == nil {
		return req
	}

	transformOptions := channelSettings.TransformOptions

	if !transformOptions.ForceArrayInstructions &&
		!transformOptions.ForceArrayInputs &&
		!transformOptions.ReplaceDeveloperRoleWithSystem &&
		!transformOptions.MergeDeveloperRoleIntoSystem {
		return req
	}

	newReq := *req

	if transformOptions.ForceArrayInstructions {
		newReq.TransformOptions.ArrayInstructions = lo.ToPtr(true)
	}

	if transformOptions.ForceArrayInputs {
		newReq.TransformOptions.ArrayInputs = lo.ToPtr(true)
	}

	if transformOptions.ReplaceDeveloperRoleWithSystem {
		newReq.Messages = replaceDeveloperRoleWithSystem(newReq.Messages)
	} else if transformOptions.MergeDeveloperRoleIntoSystem {
		newReq.Messages = mergeDeveloperRoleIntoSystem(newReq.Messages)
	}

	return &newReq
}

// mergeDeveloperRoleIntoSystem merges all system/developer messages into one system message.
func mergeDeveloperRoleIntoSystem(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	hasDeveloper := false
	var systemParts []llm.MessageContentPart
	var otherMessages []llm.Message

	for _, msg := range messages {
		switch {
		case strings.EqualFold(msg.Role, "system"):
			systemParts = append(systemParts, messageContentParts(msg.Content)...)
		case strings.EqualFold(msg.Role, "developer"):
			hasDeveloper = true
			systemParts = append(systemParts, messageContentParts(msg.Content)...)
		default:
			otherMessages = append(otherMessages, msg)
		}
	}

	if !hasDeveloper {
		return messages
	}

	if len(systemParts) == 0 {
		return otherMessages
	}

	return append([]llm.Message{{
		Role: "system",
		Content: llm.MessageContent{
			MultipleContent: systemParts,
		},
	}}, otherMessages...)
}

func messageContentParts(content llm.MessageContent) []llm.MessageContentPart {
	if len(content.MultipleContent) > 0 {
		return content.MultipleContent
	}

	if content.Content == nil {
		return nil
	}

	return []llm.MessageContentPart{{Type: "text", Text: content.Content}}
}

// replaceDeveloperRoleWithSystem replaces developer role with system in messages.
func replaceDeveloperRoleWithSystem(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}

	replaced := false

	result := make([]llm.Message, len(messages))
	for i, msg := range messages {
		if strings.EqualFold(msg.Role, "developer") {
			msg.Role = "system"
			replaced = true
		}

		result[i] = msg
	}

	if !replaced {
		return messages
	}

	return result
}
