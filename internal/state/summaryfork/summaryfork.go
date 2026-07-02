//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summaryfork stores the parent model request snapshot used by
// cache-safe asynchronous summaries.
package summaryfork

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const stateKey = "trpc_agent.summary.cache_safe_fork_request"

// Attach stores a snapshot of the parent model request on the invocation.
func Attach(inv *agent.Invocation, req *model.Request) {
	if inv == nil || req == nil {
		return
	}
	inv.SetState(stateKey, cloneRequest(req))
}

// Request returns a snapshot of the parent model request, if one exists.
func Request(inv *agent.Invocation) (*model.Request, bool) {
	req, ok := agent.GetStateValue[*model.Request](inv, stateKey)
	if !ok || req == nil {
		return nil, false
	}
	return cloneRequest(req), true
}

// AppendResponse appends persisted response messages to the stored request
// snapshot. It is a no-op when no snapshot is present.
func AppendResponse(inv *agent.Invocation, rsp *model.Response) {
	req, ok := agent.GetStateValue[*model.Request](inv, stateKey)
	if !ok || req == nil || rsp == nil {
		return
	}
	messages := responseMessages(rsp)
	if len(messages) == 0 {
		return
	}

	next := cloneRequest(req)
	next.Messages = append(next.Messages, cloneMessages(messages)...)
	inv.SetState(stateKey, next)
}

func responseMessages(rsp *model.Response) []model.Message {
	if rsp == nil {
		return nil
	}
	messages := make([]model.Message, 0, len(rsp.Choices))
	for _, choice := range rsp.Choices {
		if messageHasPayloadForFork(choice.Message) {
			messages = append(messages, choice.Message)
			continue
		}
		if messageHasPayloadForFork(choice.Delta) {
			messages = append(messages, choice.Delta)
		}
	}
	return messages
}

func messageHasPayloadForFork(msg model.Message) bool {
	return model.HasPayload(msg) ||
		len(msg.ToolCalls) > 0 ||
		msg.ToolID != "" ||
		msg.ToolName != ""
}

func cloneRequest(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}

	cloned := *req
	cloned.Messages = cloneMessages(req.Messages)
	cloned.GenerationConfig = cloneGenerationConfig(req.GenerationConfig)
	cloned.StructuredOutput = cloneStructuredOutput(req.StructuredOutput)
	cloned.ExtraFields = jsonmap.Clone(req.ExtraFields)
	cloned.Headers = cloneHeaders(req.Headers)
	cloned.Tools = cloneTools(req.Tools)
	return &cloned
}

func cloneMessages(messages []model.Message) []model.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]model.Message, len(messages))
	for i := range messages {
		cloned[i] = cloneMessage(messages[i])
	}
	return cloned
}

func cloneMessage(msg model.Message) model.Message {
	cloned := msg
	cloned.ContentParts = cloneContentParts(msg.ContentParts)
	cloned.ToolCalls = cloneToolCalls(msg.ToolCalls)
	return cloned
}

func cloneContentParts(parts []model.ContentPart) []model.ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]model.ContentPart, len(parts))
	for i := range parts {
		cloned[i] = cloneContentPart(parts[i])
	}
	return cloned
}

func cloneContentPart(part model.ContentPart) model.ContentPart {
	cloned := part
	if part.Text != nil {
		text := *part.Text
		cloned.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = append([]byte(nil), part.Image.Data...)
		cloned.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = append([]byte(nil), part.Audio.Data...)
		cloned.Audio = &audio
	}
	if part.File != nil {
		file := *part.File
		file.Data = append([]byte(nil), part.File.Data...)
		cloned.File = &file
	}
	return cloned
}

func cloneToolCalls(toolCalls []model.ToolCall) []model.ToolCall {
	if toolCalls == nil {
		return nil
	}
	cloned := make([]model.ToolCall, len(toolCalls))
	for i := range toolCalls {
		cloned[i] = toolCalls[i]
		cloned[i].Function.Arguments = append(
			[]byte(nil),
			toolCalls[i].Function.Arguments...,
		)
		if toolCalls[i].Index != nil {
			index := *toolCalls[i].Index
			cloned[i].Index = &index
		}
		cloned[i].ExtraFields = jsonmap.Clone(toolCalls[i].ExtraFields)
	}
	return cloned
}

func cloneGenerationConfig(cfg model.GenerationConfig) model.GenerationConfig {
	cloned := cfg
	cloned.Stop = append([]string(nil), cfg.Stop...)
	cloned.MaxTokens = clonePtr(cfg.MaxTokens)
	cloned.Temperature = clonePtr(cfg.Temperature)
	cloned.TopP = clonePtr(cfg.TopP)
	cloned.PresencePenalty = clonePtr(cfg.PresencePenalty)
	cloned.FrequencyPenalty = clonePtr(cfg.FrequencyPenalty)
	cloned.Logprobs = clonePtr(cfg.Logprobs)
	cloned.TopLogprobs = clonePtr(cfg.TopLogprobs)
	cloned.ReasoningEffort = clonePtr(cfg.ReasoningEffort)
	cloned.ThinkingEnabled = clonePtr(cfg.ThinkingEnabled)
	cloned.ThinkingTokens = clonePtr(cfg.ThinkingTokens)
	cloned.ThinkingLevel = clonePtr(cfg.ThinkingLevel)
	return cloned
}

func clonePtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func cloneStructuredOutput(out *model.StructuredOutput) *model.StructuredOutput {
	if out == nil {
		return nil
	}
	cloned := *out
	if out.JSONSchema != nil {
		schema := *out.JSONSchema
		schema.Schema = jsonmap.Clone(out.JSONSchema.Schema)
		cloned.JSONSchema = &schema
	}
	return &cloned
}

func cloneHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for k, v := range headers {
		cloned[k] = v
	}
	return cloned
}

func cloneTools(tools map[string]tool.Tool) map[string]tool.Tool {
	if tools == nil {
		return nil
	}
	cloned := make(map[string]tool.Tool, len(tools))
	for name, t := range tools {
		cloned[name] = t
	}
	return cloned
}
