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
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const stateKey = "trpc_agent.summary.cache_safe_fork_request"

// invocationState keeps an immutable snapshot opaque to Invocation.View's
// generic state cloner. Mutations must replace the holder instead of changing
// the stored request in place.
type invocationState struct {
	request *model.Request
}

// Attach stores a snapshot of the parent model request on the invocation.
func Attach(inv *agent.Invocation, req *model.Request) {
	if inv == nil || req == nil {
		return
	}
	inv.SetState(stateKey, &invocationState{request: cloneRequest(req)})
}

// Request returns a snapshot of the parent model request, if one exists.
func Request(inv *agent.Invocation) (*model.Request, bool) {
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.request == nil {
		return nil, false
	}
	return cloneRequest(state.request), true
}

// Invalidate removes the current cache-safe request snapshot. Summarization
// falls back to its standalone session input until a later Attach.
func Invalidate(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(stateKey)
}

// AppendResponse appends persisted response messages to the stored request
// snapshot. It is a no-op when no snapshot is present.
func AppendResponse(inv *agent.Invocation, rsp *model.Response) {
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.request == nil || rsp == nil {
		return
	}
	messages := responseMessages(rsp)
	if len(messages) == 0 {
		return
	}

	next := cloneRequest(state.request)
	next.Messages = append(next.Messages, statecopy.Messages(messages)...)
	inv.SetState(stateKey, &invocationState{request: next})
}

func responseMessages(rsp *model.Response) []model.Message {
	choice, ok := primaryChoice(rsp)
	if !ok {
		return nil
	}

	var messages []model.Message
	if messageHasPayloadForFork(choice.Message) {
		messages = append(messages, choice.Message)
		return messages
	}
	if messageHasPayloadForFork(choice.Delta) {
		messages = append(messages, choice.Delta)
	}
	return messages
}

func primaryChoice(rsp *model.Response) (model.Choice, bool) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return model.Choice{}, false
	}
	for _, choice := range rsp.Choices {
		if choice.Index == 0 {
			return choice, true
		}
	}
	return rsp.Choices[0], true
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
	cloned.Messages = statecopy.Messages(req.Messages)
	cloned.GenerationConfig = cloneGenerationConfig(req.GenerationConfig)
	cloned.StructuredOutput = cloneStructuredOutput(req.StructuredOutput)
	cloned.ExtraFields = jsonmap.Clone(req.ExtraFields)
	cloned.Headers = cloneHeaders(req.Headers)
	cloned.Tools = cloneTools(req.Tools)
	return &cloned
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
