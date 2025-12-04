//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/ollama/ollama/api"
	openaisdk "github.com/openai/openai-go"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func long(s string) string { return s + ": " + repeat("lorem ipsum ", 40) }

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// summarizeMessages returns a concise multi-line preview of messages with truncation.
func summarizeMessages(msgs []model.Message, maxItems int) string {
	var b strings.Builder
	for i, m := range msgs {
		if i >= maxItems {
			fmt.Fprintf(&b, "... (%d more)\n", len(msgs)-i)
			break
		}
		role := string(m.Role)
		content := firstNonEmpty(
			strings.TrimSpace(m.Content),
			strings.TrimSpace(m.ReasoningContent),
			firstTextPart(m),
		)
		content = truncate(content, 100)
		// replace newlines to keep one-line per message
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, role, content)
	}
	return b.String()
}

// summarizeMessagesHeadTail shows head and tail messages with omitted middle count.
func summarizeMessagesHeadTail(msgs []model.Message, headCount, tailCount int) string {
	var b strings.Builder
	total := len(msgs)

	if total <= headCount+tailCount {
		// All messages fit, show them all.
		return summarizeMessages(msgs, total)
	}

	// Show head messages.
	for i := 0; i < headCount && i < total; i++ {
		m := msgs[i]
		role := string(m.Role)
		content := firstNonEmpty(
			strings.TrimSpace(m.Content),
			strings.TrimSpace(m.ReasoningContent),
			firstTextPart(m),
		)
		content = truncate(content, 100)
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, role, content)
	}

	// Show omitted count.
	omitted := total - headCount - tailCount
	if omitted > 0 {
		fmt.Fprintf(&b, "... (%d messages omitted)\n", omitted)
	}

	// Show tail messages.
	for i := total - tailCount; i < total; i++ {
		m := msgs[i]
		role := string(m.Role)
		content := firstNonEmpty(
			strings.TrimSpace(m.Content),
			strings.TrimSpace(m.ReasoningContent),
			firstTextPart(m),
		)
		content = truncate(content, 100)
		content = strings.ReplaceAll(content, "\n", " ")
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, role, content)
	}

	return b.String()
}

func firstTextPart(m model.Message) string {
	for _, p := range m.ContentParts {
		if p.Text != nil {
			return strings.TrimSpace(*p.Text)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// convertFromOpenAIMessages converts OpenAI SDK messages back to model.Message format.
// This is a simplified conversion that extracts the basic content for token counting.
func convertFromOpenAIMessages(openaiMsgs []openaisdk.ChatCompletionMessageParamUnion) []model.Message {
	messages := make([]model.Message, 0, len(openaiMsgs))
	for _, msg := range openaiMsgs {
		var m model.Message
		switch {
		case msg.OfSystem != nil:
			content := extractSystemContent(msg.OfSystem.Content)
			m = model.NewSystemMessage(content)
		case msg.OfUser != nil:
			content := extractUserContent(msg.OfUser.Content)
			m = model.NewUserMessage(content)
		case msg.OfAssistant != nil:
			content := extractAssistantContent(msg.OfAssistant.Content)
			m = model.NewAssistantMessage(content)
		case msg.OfTool != nil:
			content := extractToolContent(msg.OfTool.Content)
			m = model.Message{
				Role:    model.RoleTool,
				Content: content,
				ToolID:  msg.OfTool.ToolCallID,
			}
		default:
			continue
		}
		messages = append(messages, m)
	}
	return messages
}

func extractSystemContent(content openaisdk.ChatCompletionSystemMessageParamContentUnion) string {
	if content.OfString.Valid() {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var parts []string
		for _, part := range content.OfArrayOfContentParts {
			parts = append(parts, part.Text)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func extractUserContent(content openaisdk.ChatCompletionUserMessageParamContentUnion) string {
	if content.OfString.Valid() {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var parts []string
		for _, part := range content.OfArrayOfContentParts {
			if part.OfText != nil {
				parts = append(parts, part.OfText.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func extractAssistantContent(content openaisdk.ChatCompletionAssistantMessageParamContentUnion) string {
	if content.OfString.Valid() {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var parts []string
		for _, part := range content.OfArrayOfContentParts {
			if part.OfText != nil {
				parts = append(parts, part.OfText.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func extractToolContent(content openaisdk.ChatCompletionToolMessageParamContentUnion) string {
	if content.OfString.Valid() {
		return content.OfString.Value
	}
	if len(content.OfArrayOfContentParts) > 0 {
		var parts []string
		for _, part := range content.OfArrayOfContentParts {
			parts = append(parts, part.Text)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// convertFromAnthropicMessages converts Anthropic SDK messages back to model.Message format.
// This is a simplified conversion that extracts the basic content for token counting.
func convertFromAnthropicMessages(
	anthropicMsgs []anthropicsdk.MessageParam,
	systemPrompts []anthropicsdk.TextBlockParam,
) []model.Message {
	messages := make([]model.Message, 0, len(anthropicMsgs)+len(systemPrompts))

	// Add system messages first if present.
	for _, sysPrompt := range systemPrompts {
		messages = append(messages, model.NewSystemMessage(sysPrompt.Text))
	}

	// Convert conversation messages.
	for _, msg := range anthropicMsgs {
		var m model.Message
		role := string(msg.Role)

		// Extract content from message.
		var content string
		for _, block := range msg.Content {
			if block.OfText != nil {
				content += block.OfText.Text
			}
			// Tool use blocks are not included in token count for simplification.
		}

		switch role {
		case "user":
			m = model.NewUserMessage(content)
		case "assistant":
			m = model.NewAssistantMessage(content)
		default:
			continue
		}

		messages = append(messages, m)
	}

	return messages
}

func convertFromOllamaMessages(ollamaMsgs []api.Message) []model.Message {
	messages := make([]model.Message, 0, len(ollamaMsgs))
	for _, msg := range ollamaMsgs {
		var m model.Message
		role := string(msg.Role)

		content := msg.Content

		switch role {
		case "user":
			m = model.NewUserMessage(content)
		case "assistant":
			m = model.NewAssistantMessage(content)
		default:
			continue
		}

		messages = append(messages, m)
	}
	return messages
}

// loadMessagesFromJSON loads messages from a JSON file in the format of input.json.
func loadMessagesFromJSON(filename string) ([]model.Message, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var jsonData struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []any  `json:"tool_calls"`
			ToolID    string `json:"tool_id"`
			ToolName  string `json:"tool_name"`
		} `json:"messages"`
	}

	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	var messages []model.Message
	for _, msg := range jsonData.Messages {
		role := model.Role(msg.Role)
		switch role {
		case model.RoleSystem:
			messages = append(messages, model.NewSystemMessage(msg.Content))
		case model.RoleUser:
			messages = append(messages, model.NewUserMessage(msg.Content))
		case model.RoleAssistant:
			// For assistant messages, preserve tool_calls if present.
			m := model.NewAssistantMessage(msg.Content)
			// If there are tool_calls but no content, keep the message.
			// The API will handle it correctly.
			if len(msg.ToolCalls) > 0 && msg.Content == "" {
				// Preserve the message with tool_calls.
				m.Content = ""
			}
			messages = append(messages, m)
		case model.RoleTool:
			messages = append(messages, model.Message{
				Role:     role,
				Content:  msg.Content,
				ToolID:   msg.ToolID,
				ToolName: msg.ToolName,
			})
		default:
			// Skip unknown roles.
			continue
		}
	}

	return messages, nil
}
