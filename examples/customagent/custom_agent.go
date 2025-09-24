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
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SimpleIntentAgent is a minimal custom Agent implementation that demonstrates
// how to embed business logic flow without using Graph.
//
// Flow:
//  1. Run a quick intent classification using the LLM: "chitchat" or "task".
//  2. If chitchat, directly answer the user.
//  3. If task, provide a step plan (or route to downstream logic/tools in real apps).
type SimpleIntentAgent struct {
	name        string
	description string
	model       model.Model
}

func NewSimpleIntentAgent(name, description string, m model.Model) *SimpleIntentAgent {
	return &SimpleIntentAgent{name: name, description: description, model: m}
}

// Info implements agent.Agent.
func (a *SimpleIntentAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// Tools implements agent.Agent. No tools in this minimal example.
func (a *SimpleIntentAgent) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent. This example has no sub-agents.
func (a *SimpleIntentAgent) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (a *SimpleIntentAgent) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent.
func (a *SimpleIntentAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if a.model == nil {
		return nil, fmt.Errorf("custom agent %q has no model configured", a.name)
	}

	out := make(chan *event.Event, 64)

	go func() {
		defer close(out)

		// Align invocation metadata for downstream consumers (consistency with LLMAgent behavior).
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}
		if invocation.Agent == nil {
			invocation.Agent = a
		}
		if invocation.Model == nil {
			invocation.Model = a.model
		}

		// 1) Intent classification (not forwarded to user).
		intent := a.classifyIntent(ctx, invocation)

		// 2) Branch on intent and stream the chosen LLM response back to the user.
		switch intent {
		case "chitchat":
			a.replyChitChat(ctx, invocation, out)
		case "task":
			a.replyTaskPlan(ctx, invocation, out)
		default:
			// Fallback if classifier uncertain.
			a.replyChitChat(ctx, invocation, out)
		}
	}()

	return out, nil
}

// classifyIntent asks the LLM to classify the user input.
func (a *SimpleIntentAgent) classifyIntent(ctx context.Context, inv *agent.Invocation) string {
	// System prompt keeps output constrained to a single label.
	sys := model.NewSystemMessage("" +
		"You are an intent classifier. Read the user message and output exactly one word:\n" +
		"- 'chitchat' for casual conversation\n" +
		"- 'task' for requests that need specific actions or steps\n" +
		"Output only 'chitchat' or 'task'.")

	req := &model.Request{
		Messages: []model.Message{sys, inv.Message},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
	}

	rspCh, err := a.model.GenerateContent(ctx, req)
	if err != nil {
		// On error, default to chitchat to remain robust.
		return "chitchat"
	}

	var buf strings.Builder
	for rsp := range rspCh {
		// Accumulate both deltas and final message content for non-streaming providers.
		if len(rsp.Choices) > 0 {
			if rsp.Choices[0].Delta.Content != "" {
				buf.WriteString(rsp.Choices[0].Delta.Content)
			}
			if rsp.Choices[0].Message.Content != "" {
				buf.WriteString(rsp.Choices[0].Message.Content)
			}
		}
	}

	out := sanitizeIntent(buf.String())
	switch out {
	case "chitchat", "chat", "casual", "闲聊", "聊天", "对话":
		return "chitchat"
	case "task", "action", "todo", "job", "任务", "办理", "处理":
		return "task"
	default:
		return "chitchat"
	}
}

// replyChitChat streams a direct conversational answer.
func (a *SimpleIntentAgent) replyChitChat(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
	sys := model.NewSystemMessage("You are a concise, friendly assistant. Answer the user directly.")
	req := &model.Request{
		Messages: []model.Message{sys, inv.Message},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	rspCh, err := a.model.GenerateContent(ctx, req)
	if err != nil {
		agent.EmitEvent(ctx, inv, out, event.NewErrorEvent(
			inv.InvocationID,
			a.name,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return
	}
	for rsp := range rspCh {
		agent.EmitEvent(ctx, inv, out, event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			rsp,
		))
	}
}

// replyTaskPlan streams a simple step plan for task-like requests.
func (a *SimpleIntentAgent) replyTaskPlan(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
	sys := model.NewSystemMessage("" +
		"You are a task solver.\n" +
		"- First, produce a short bullet list plan (3-5 steps).\n" +
		"- Then propose the first concrete action succinctly.")
	req := &model.Request{
		Messages: []model.Message{sys, inv.Message},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	rspCh, err := a.model.GenerateContent(ctx, req)
	if err != nil {
		agent.EmitEvent(ctx, inv, out, event.NewErrorEvent(
			inv.InvocationID,
			a.name,
			model.ErrorTypeFlowError, err.Error(),
		))
		return
	}
	for rsp := range rspCh {
		agent.EmitEvent(ctx, inv, out, event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			rsp,
		))
	}
}

// sanitizeIntent normalizes the LLM output for intent classification.
func sanitizeIntent(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	// Keep just the first line in case the model added explanations.
	if idx := strings.IndexByte(t, '\n'); idx >= 0 {
		t = t[:idx]
	}
	// Trim common punctuation / quotes around a single token.
	t = strings.Trim(t, ".,!?;:。！？；：\"'`（）()[]{}<>")
	// Collapse spaces.
	t = strings.Join(strings.Fields(t), " ")
	return t
}
