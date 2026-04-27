//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replay exports OpenTelemetry-aligned message telemetry into provider
// HTTP request payloads for debugging and best-effort request replay.
package replay

import (
	"encoding/json"
	"fmt"
	"strings"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com"

	chatCompletionsPath = "/v1/chat/completions"
	responsesPath       = "/v1/responses"
)

// HTTPRequest is a best-effort exported HTTP request ready for curl or SDK replay.
type HTTPRequest struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     json.RawMessage   `json:"body"`
	Warnings []string          `json:"warnings,omitempty"`
}

// OpenAIExportRequest contains the telemetry fields needed to reconstruct a
// provider request from trace attributes.
type OpenAIExportRequest struct {
	Model                  string
	InputMessagesJSON      string
	ToolDefinitionsJSON    string
	SystemInstructionsJSON string
	BaseURL                string
}

// ExportOpenAIChatCompletions converts OTel input messages into an OpenAI Chat
// Completions HTTP request payload.
func ExportOpenAIChatCompletions(req OpenAIExportRequest) (*HTTPRequest, error) {
	messages, warnings, err := chatCompletionsMessages(req)
	if err != nil {
		return nil, err
	}
	tools, toolWarnings, err := openAIChatTools(req.ToolDefinitionsJSON)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, toolWarnings...)

	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &HTTPRequest{
		Method:   "POST",
		URL:      joinOpenAIURL(req.BaseURL, chatCompletionsPath),
		Headers:  defaultOpenAIHeaders(),
		Body:     rawBody,
		Warnings: warnings,
	}, nil
}

// ExportOpenAIResponses converts OTel input messages into an OpenAI Responses
// HTTP request payload. The mapping is best-effort because the Responses API
// uses item types that do not exactly match OTel chat messages.
func ExportOpenAIResponses(req OpenAIExportRequest) (*HTTPRequest, error) {
	items, warnings, instructions, err := responsesItems(req)
	if err != nil {
		return nil, err
	}
	tools, toolWarnings, err := openAIResponsesTools(req.ToolDefinitionsJSON)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, toolWarnings...)

	body := map[string]any{
		"model": req.Model,
		"input": items,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &HTTPRequest{
		Method:   "POST",
		URL:      joinOpenAIURL(req.BaseURL, responsesPath),
		Headers:  defaultOpenAIHeaders(),
		Body:     rawBody,
		Warnings: warnings,
	}, nil
}

func chatCompletionsMessages(req OpenAIExportRequest) ([]map[string]any, []string, error) {
	messages, err := itelemetry.ParseOTelInputMessagesJSON(req.InputMessagesJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("parse gen_ai.input.messages: %w", err)
	}
	var out []map[string]any
	var warnings []string

	if req.SystemInstructionsJSON != "" {
		sysParts, err := parseSystemInstructionParts(req.SystemInstructionsJSON)
		if err != nil {
			return nil, nil, fmt.Errorf("parse gen_ai.system_instructions: %w", err)
		}
		msg, moreWarnings, ok := openAIChatMessageFromParts(model.RoleSystem, sysParts)
		warnings = append(warnings, moreWarnings...)
		if ok {
			out = append(out, msg)
		}
	}

	for _, message := range messages {
		converted, moreWarnings := openAIChatMessagesFromOTelMessage(message)
		out = append(out, converted...)
		warnings = append(warnings, moreWarnings...)
	}
	return out, warnings, nil
}

func responsesItems(req OpenAIExportRequest) ([]map[string]any, []string, string, error) {
	messages, err := itelemetry.ParseOTelInputMessagesJSON(req.InputMessagesJSON)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse gen_ai.input.messages: %w", err)
	}
	var items []map[string]any
	var warnings []string
	var instructions string

	if req.SystemInstructionsJSON != "" {
		sysParts, err := parseSystemInstructionParts(req.SystemInstructionsJSON)
		if err != nil {
			return nil, nil, "", fmt.Errorf("parse gen_ai.system_instructions: %w", err)
		}
		if text, ok := joinTextOnlySystemInstructions(sysParts); ok {
			instructions = text
		} else {
			warnings = append(warnings,
				`responses replay exporter only maps text-only gen_ai.system_instructions to "instructions"; non-text instructions are emitted as a system message when possible`)
			item, moreWarnings, ok := openAIResponsesMessageItem(model.RoleSystem, sysParts)
			warnings = append(warnings, moreWarnings...)
			if ok {
				items = append(items, item)
			}
		}
	}

	for _, message := range messages {
		converted, moreWarnings := openAIResponsesItemsFromOTelMessage(message)
		items = append(items, converted...)
		warnings = append(warnings, moreWarnings...)
	}
	return items, warnings, instructions, nil
}

func openAIChatMessagesFromOTelMessage(message itelemetry.OTelInputMessage) ([]map[string]any, []string) {
	switch message.Role {
	case model.RoleTool:
		return openAIChatToolMessages(message)
	default:
		msg, warnings, ok := openAIChatMessageFromParts(message.Role, message.Parts)
		if !ok {
			return nil, warnings
		}
		if message.Role == model.RoleAssistant {
			toolCalls := openAIChatToolCalls(message.Parts)
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
		}
		return []map[string]any{msg}, warnings
	}
}

func openAIChatMessageFromParts(role model.Role, parts []itelemetry.OTelMessagePart) (map[string]any, []string, bool) {
	contentParts := make([]map[string]any, 0, len(parts))
	warnings := make([]string, 0)
	for _, part := range parts {
		if part.Type == "tool_call" || part.Type == "tool_call_response" {
			continue
		}
		converted, warning, ok := openAIChatContentPart(role, part)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if ok {
			contentParts = append(contentParts, converted)
		}
	}

	toolCalls := openAIChatToolCalls(parts)
	if len(contentParts) == 0 && len(toolCalls) == 0 {
		return nil, warnings, false
	}

	msg := map[string]any{
		"role": string(role),
	}
	if len(contentParts) > 0 {
		msg["content"] = compactChatContent(contentParts)
	} else {
		msg["content"] = ""
	}
	return msg, warnings, true
}

func openAIChatToolMessages(message itelemetry.OTelInputMessage) ([]map[string]any, []string) {
	out := make([]map[string]any, 0, len(message.Parts))
	warnings := make([]string, 0)
	for _, part := range message.Parts {
		switch part.Type {
		case "tool_call_response":
			msg := map[string]any{
				"role":         string(model.RoleTool),
				"content":      rawMessageToString(part.Response),
				"tool_call_id": part.ID,
			}
			if part.ID == "" {
				warnings = append(warnings, "chat completions replay exporter emitted a tool message without tool_call_id")
			}
			out = append(out, msg)
		case "text":
			warnings = append(warnings, "chat completions replay exporter ignores free-form text parts on role=tool messages")
		case "reasoning":
			warnings = append(warnings, "chat completions replay exporter ignores reasoning parts on role=tool messages")
		default:
			warnings = append(warnings,
				fmt.Sprintf("chat completions replay exporter ignores unsupported role=tool part type %q", part.Type))
		}
	}
	return out, warnings
}

func openAIChatToolCalls(parts []itelemetry.OTelMessagePart) []map[string]any {
	out := make([]map[string]any, 0)
	for _, part := range parts {
		if part.Type != "tool_call" {
			continue
		}
		out = append(out, map[string]any{
			"id":   part.ID,
			"type": "function",
			"function": map[string]any{
				"name":      part.Name,
				"arguments": rawMessageJSONString(part.Arguments),
			},
		})
	}
	return out
}

func openAIChatContentPart(role model.Role, part itelemetry.OTelMessagePart) (map[string]any, string, bool) {
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleSystem {
		return nil, fmt.Sprintf("chat completions replay exporter does not support role %q", role), false
	}

	switch part.Type {
	case "text":
		return map[string]any{
			"type": "text",
			"text": part.Content,
		}, "", true
	case "reasoning":
		return nil, "chat completions replay exporter does not replay reasoning parts", false
	case "tool_call", "tool_call_response":
		return nil, "", false
	}

	if role != model.RoleUser {
		return nil,
			fmt.Sprintf("chat completions replay exporter only replays non-text multimodal parts on role=user messages; got role=%q type=%q", role, part.Type),
			false
	}

	switch part.Type {
	case "uri":
		switch part.Modality {
		case "image":
			return chatImageURLPart(part.URI, part.Detail), "", true
		case "audio":
			return nil, "chat completions replay exporter does not support audio URIs; provide blob audio instead", false
		case "video":
			return nil, "chat completions replay exporter does not replay video inputs", false
		default:
			return nil, "chat completions replay exporter does not support external file URIs; use file_id or blob/file_data", false
		}
	case "blob":
		switch part.Modality {
		case "image":
			return chatImageURLPart(dataURL(part.MIMEType, part.Content), part.Detail), "", true
		case "audio":
			format := audioFormat(part.MIMEType)
			if format == "" {
				return nil, fmt.Sprintf("chat completions replay exporter does not know how to map audio mime_type %q", part.MIMEType), false
			}
			return map[string]any{
				"type": "input_audio",
				"input_audio": map[string]any{
					"data":   dataURL(part.MIMEType, part.Content),
					"format": format,
				},
			}, "", true
		case "video":
			return nil, "chat completions replay exporter does not replay video inputs", false
		default:
			filename := strings.TrimSpace(part.Filename)
			if filename == "" {
				filename = "attachment"
			}
			return map[string]any{
				"type": "file",
				"file": map[string]any{
					"filename":  filename,
					"file_data": dataURL(part.MIMEType, part.Content),
				},
			}, "", true
		}
	case "file":
		if part.Modality == "video" {
			return nil, "chat completions replay exporter does not replay video inputs", false
		}
		return map[string]any{
			"type": "file",
			"file": map[string]any{
				"file_id": part.FileID,
			},
		}, "", true
	default:
		return nil, fmt.Sprintf("chat completions replay exporter does not support part type %q", part.Type), false
	}
}

func openAIResponsesItemsFromOTelMessage(message itelemetry.OTelInputMessage) ([]map[string]any, []string) {
	items := make([]map[string]any, 0, 1+len(message.Parts))
	warnings := make([]string, 0)

	msgItem, msgWarnings, ok := openAIResponsesMessageItem(message.Role, message.Parts)
	warnings = append(warnings, msgWarnings...)
	if ok {
		items = append(items, msgItem)
	}

	for _, part := range message.Parts {
		switch part.Type {
		case "tool_call":
			items = append(items, map[string]any{
				"type":      "function_call",
				"call_id":   part.ID,
				"name":      part.Name,
				"arguments": rawMessageJSONString(part.Arguments),
			})
		case "tool_call_response":
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": part.ID,
				"output":  rawMessageToString(part.Response),
			})
		case "reasoning":
			warnings = append(warnings, "responses replay exporter does not replay reasoning parts")
		}
	}

	return items, warnings
}

func openAIResponsesMessageItem(role model.Role, parts []itelemetry.OTelMessagePart) (map[string]any, []string, bool) {
	contentParts := make([]map[string]any, 0, len(parts))
	warnings := make([]string, 0)
	for _, part := range parts {
		if part.Type == "tool_call" || part.Type == "tool_call_response" || part.Type == "reasoning" {
			continue
		}
		converted, warning, ok := openAIResponsesContentPart(part)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if ok {
			contentParts = append(contentParts, converted)
		}
	}
	if len(contentParts) == 0 {
		return nil, warnings, false
	}
	return map[string]any{
		"role":    string(role),
		"content": contentParts,
	}, warnings, true
}

func openAIResponsesContentPart(part itelemetry.OTelMessagePart) (map[string]any, string, bool) {
	switch part.Type {
	case "text":
		return map[string]any{
			"type": "input_text",
			"text": part.Content,
		}, "", true
	case "uri":
		switch part.Modality {
		case "image":
			payload := map[string]any{
				"type":      "input_image",
				"image_url": part.URI,
			}
			if part.Detail != "" {
				payload["detail"] = part.Detail
			}
			return payload, "", true
		case "video":
			return nil, "responses replay exporter does not replay video inputs", false
		case "audio":
			return nil, "responses replay exporter does not replay audio inputs yet", false
		default:
			return map[string]any{
				"type":     "input_file",
				"file_url": part.URI,
			}, "", true
		}
	case "blob":
		switch part.Modality {
		case "image":
			payload := map[string]any{
				"type":      "input_image",
				"image_url": dataURL(part.MIMEType, part.Content),
			}
			if part.Detail != "" {
				payload["detail"] = part.Detail
			}
			return payload, "", true
		case "audio":
			return nil, "responses replay exporter does not replay audio inputs yet", false
		case "video":
			return nil, "responses replay exporter does not replay video inputs", false
		default:
			filename := strings.TrimSpace(part.Filename)
			if filename == "" {
				filename = "attachment"
			}
			return map[string]any{
				"type":      "input_file",
				"filename":  filename,
				"file_data": dataURL(part.MIMEType, part.Content),
			}, "", true
		}
	case "file":
		if part.Modality == "video" {
			return nil, "responses replay exporter does not replay video inputs", false
		}
		return map[string]any{
			"type":    "input_file",
			"file_id": part.FileID,
		}, "", true
	case "reasoning":
		return nil, "responses replay exporter does not replay reasoning parts", false
	default:
		return nil, fmt.Sprintf("responses replay exporter does not support part type %q", part.Type), false
	}
}

func parseSystemInstructionParts(raw string) ([]itelemetry.OTelMessagePart, error) {
	if raw == "" {
		return nil, nil
	}
	var parts []itelemetry.OTelMessagePart
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return nil, err
	}
	return parts, nil
}

func joinTextOnlySystemInstructions(parts []itelemetry.OTelMessagePart) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type != "text" {
			return "", false
		}
		texts = append(texts, part.Content)
	}
	return strings.Join(texts, "\n"), true
}

func openAIChatTools(raw string) ([]map[string]any, []string, error) {
	tools, err := parseToolDeclarations(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse gen_ai.request.tool.definitions: %w", err)
	}
	out := make([]map[string]any, 0, len(tools))
	for _, decl := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        decl.Name,
				"description": decl.Description,
				"parameters":  schemaJSON(decl.InputSchema),
			},
		})
	}
	return out, nil, nil
}

func openAIResponsesTools(raw string) ([]map[string]any, []string, error) {
	tools, err := parseToolDeclarations(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse gen_ai.request.tool.definitions: %w", err)
	}
	out := make([]map[string]any, 0, len(tools))
	for _, decl := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        decl.Name,
			"description": decl.Description,
			"parameters":  schemaJSON(decl.InputSchema),
		})
	}
	return out, nil, nil
}

func parseToolDeclarations(raw string) ([]tool.Declaration, error) {
	if raw == "" {
		return nil, nil
	}
	var decls []tool.Declaration
	if err := json.Unmarshal([]byte(raw), &decls); err != nil {
		return nil, err
	}
	return decls, nil
}

func schemaJSON(schema *tool.Schema) map[string]any {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	bts, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	var out map[string]any
	if err := json.Unmarshal(bts, &out); err != nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return out
}

func compactChatContent(parts []map[string]any) any {
	if len(parts) == 1 {
		if partType, _ := parts[0]["type"].(string); partType == "text" {
			if text, ok := parts[0]["text"].(string); ok {
				return text
			}
		}
	}
	return parts
}

func chatImageURLPart(url, detail string) map[string]any {
	imageURL := map[string]any{"url": url}
	if detail != "" {
		imageURL["detail"] = detail
	}
	return map[string]any{
		"type":      "image_url",
		"image_url": imageURL,
	}
}

func audioFormat(mimeType string) string {
	switch strings.TrimSpace(strings.ToLower(mimeType)) {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	default:
		return ""
	}
}

func rawMessageJSONString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func rawMessageToString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return trimmed
}

func dataURL(mimeType, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if strings.TrimSpace(mimeType) == "" {
		return "data:application/octet-stream;base64," + content
	}
	return "data:" + strings.TrimSpace(mimeType) + ";base64," + content
}

func joinOpenAIURL(baseURL, path string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	return strings.TrimRight(base, "/") + path
}

func defaultOpenAIHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer $OPENAI_API_KEY",
		"Content-Type":  "application/json",
	}
}
