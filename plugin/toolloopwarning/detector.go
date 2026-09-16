//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type toolRound struct {
	toolCalls []model.ToolCall
	results   []model.Message
}

type roundFingerprint struct {
	ToolCalls []callFingerprint `json:"tool_calls"`
}

type callFingerprint struct {
	ToolName  string        `json:"tool_name"`
	Arguments string        `json:"arguments"`
	Result    model.Message `json:"result"`
}

func matchingTrailingRoundFingerprint(
	messages []model.Message,
	excludedToolNames map[string]struct{},
) (string, bool) {
	latest, latestStart, ok := parseTrailingToolRound(messages, len(messages))
	if !ok {
		return "", false
	}
	previous, _, ok := parseTrailingToolRound(messages, latestStart)
	if !ok || roundContainsExcludedTool(latest, excludedToolNames) ||
		roundContainsExcludedTool(previous, excludedToolNames) {
		return "", false
	}
	latestFingerprint, ok := fingerprintRound(latest.toolCalls, latest.results)
	if !ok {
		return "", false
	}
	previousFingerprint, ok := fingerprintRound(previous.toolCalls, previous.results)
	if !ok || latestFingerprint != previousFingerprint {
		return "", false
	}
	return latestFingerprint, true
}

func parseTrailingToolRound(
	messages []model.Message,
	end int,
) (toolRound, int, bool) {
	if end <= 0 || end > len(messages) {
		return toolRound{}, 0, false
	}
	resultStart := end
	for resultStart > 0 && messages[resultStart-1].Role == model.RoleTool {
		resultStart--
	}
	if resultStart == end || resultStart == 0 {
		return toolRound{}, 0, false
	}
	assistantIndex := resultStart - 1
	assistant := messages[assistantIndex]
	if assistant.Role != model.RoleAssistant || len(assistant.ToolCalls) == 0 {
		return toolRound{}, 0, false
	}

	expected, ok := expectedToolCallIDs(assistant.ToolCalls)
	if !ok {
		return toolRound{}, 0, false
	}
	results, ok := orderedToolResults(
		messages[resultStart:end],
		assistant.ToolCalls,
		expected,
	)
	if !ok {
		return toolRound{}, 0, false
	}
	return toolRound{
		toolCalls: assistant.ToolCalls,
		results:   results,
	}, assistantIndex, true
}

func expectedToolCallIDs(
	toolCalls []model.ToolCall,
) (map[string]struct{}, bool) {
	expected := make(map[string]struct{}, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" || toolCall.Function.Name == "" {
			return nil, false
		}
		if _, exists := expected[toolCall.ID]; exists {
			return nil, false
		}
		expected[toolCall.ID] = struct{}{}
	}
	return expected, true
}

func orderedToolResults(
	messages []model.Message,
	toolCalls []model.ToolCall,
	expected map[string]struct{},
) ([]model.Message, bool) {
	byID := make(map[string]model.Message, len(toolCalls))
	for _, message := range messages {
		if !validToolResult(message, expected) {
			return nil, false
		}
		if _, exists := byID[message.ToolID]; exists {
			return nil, false
		}
		byID[message.ToolID] = message
	}
	if len(byID) != len(toolCalls) {
		return nil, false
	}

	results := make([]model.Message, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		result, exists := byID[toolCall.ID]
		if !exists {
			return nil, false
		}
		results = append(results, result)
	}
	return results, true
}

func validToolResult(
	message model.Message,
	expected map[string]struct{},
) bool {
	if message.Role != model.RoleTool || message.ToolID == "" ||
		!model.HasPayload(message) {
		return false
	}
	_, exists := expected[message.ToolID]
	return exists
}

func roundContainsExcludedTool(
	round toolRound,
	excludedToolNames map[string]struct{},
) bool {
	for _, toolCall := range round.toolCalls {
		if _, excluded := excludedToolNames[toolCall.Function.Name]; excluded {
			return true
		}
	}
	return false
}

func fingerprintRound(
	toolCalls []model.ToolCall,
	toolResultMessages []model.Message,
) (string, bool) {
	if len(toolCalls) == 0 || len(toolCalls) != len(toolResultMessages) {
		return "", false
	}
	fingerprint := roundFingerprint{
		ToolCalls: make([]callFingerprint, 0, len(toolCalls)),
	}
	for i, toolCall := range toolCalls {
		if toolCall.Function.Name == "" {
			return "", false
		}
		fingerprint.ToolCalls = append(fingerprint.ToolCalls, callFingerprint{
			ToolName: toolCall.Function.Name,
			Arguments: digestText(
				canonicalArguments(toolCall.Function.Arguments),
			),
			Result: boundedResultMessage(toolResultMessages[i]),
		})
	}
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func canonicalArguments(arguments []byte) string {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		var extra any
		if decoder.Decode(&extra) == io.EOF {
			if canonical, err := json.Marshal(value); err == nil {
				return string(canonical)
			}
		}
	}
	return string(trimmed)
}

func boundedResultMessage(message model.Message) model.Message {
	message.ToolID = ""
	// The tool-call fingerprint already carries the authoritative tool name.
	// Tool-result replacement hooks are required to preserve role and tool ID,
	// but are allowed to omit ToolName.
	message.ToolName = ""
	// Tool calls belong to assistant messages and do not participate in a
	// tool-result fingerprint. Clearing them also keeps unexpected payloads
	// bounded.
	message.ToolCalls = nil
	message.Content = digestText(message.Content)
	message.ReasoningContent = digestText(message.ReasoningContent)
	message.ReasoningSignature = digestText(message.ReasoningSignature)
	if len(message.ContentParts) == 0 {
		return message
	}
	parts := make([]model.ContentPart, len(message.ContentParts))
	for i, part := range message.ContentParts {
		parts[i] = boundedContentPart(part)
	}
	message.ContentParts = parts
	return message
}

func boundedContentPart(part model.ContentPart) model.ContentPart {
	if part.Text != nil {
		text := digestText(*part.Text)
		part.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = digestBytes(image.Data)
		part.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = digestBytes(audio.Data)
		part.Audio = &audio
	}
	if part.Video != nil {
		video := *part.Video
		video.Data = digestBytes(video.Data)
		part.Video = &video
	}
	if part.File != nil {
		file := *part.File
		file.Data = digestBytes(file.Data)
		part.File = &file
	}
	return part
}

func digestText(value string) string {
	if value == "" {
		return ""
	}
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, value)
	return strconv.Itoa(len(value)) + ":" + hex.EncodeToString(hasher.Sum(nil))
}

func digestBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	digest := sha256.Sum256(value)
	return digest[:]
}
