//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent provides agent tool implementations for the agent system.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Tool wraps an agent as a tool that can be called within a larger application.
// The agent's input schema is used to define the tool's input parameters, and
// the agent's output is returned as the tool's result.
type Tool struct {
	agent             agent.Agent
	skipSummarization bool
	streamInner       bool
	historyScope      HistoryScope
	name              string
	description       string
	inputSchema       *tool.Schema
	outputSchema      *tool.Schema
}

// Option is a function that configures an AgentTool.
type Option func(*agentToolOptions)

// agentToolOptions holds the configuration options for AgentTool.
type agentToolOptions struct {
	skipSummarization bool
	streamInner       bool
	historyScope      HistoryScope
}

// WithSkipSummarization sets whether to skip summarization of the agent output.
func WithSkipSummarization(skip bool) Option {
	return func(opts *agentToolOptions) {
		opts.skipSummarization = skip
	}
}

// WithStreamInner controls whether the AgentTool should forward inner agent
// streaming events up to the parent flow. When false, the flow will treat the
// tool as callable-only (no inner streaming in the parent transcript).
func WithStreamInner(enabled bool) Option {
	return func(opts *agentToolOptions) {
		opts.streamInner = enabled
	}
}

// HistoryScope controls whether and how AgentTool inherits parent history.
//   - HistoryScopeIsolated: keep child events isolated; do not inherit parent history.
//   - HistoryScopeParentBranch: inherit parent branch history by using a hierarchical
//     filter key "parent/child-uuid" so that content processors see parent events via
//     prefix matching while keeping child events in a separate sub-branch.
type HistoryScope int

// HistoryScopeIsolated: keep child events isolated; do not inherit parent history.
// HistoryScopeParentBranch: inherit parent branch history by using a hierarchical
// filter key "parent/child-uuid" so that content processors see parent events via
// prefix matching while keeping child events in a separate sub-branch.
const (
	HistoryScopeIsolated HistoryScope = iota
	HistoryScopeParentBranch
)

// WithHistoryScope sets the history inheritance behavior for AgentTool.
func WithHistoryScope(scope HistoryScope) Option {
	return func(opts *agentToolOptions) {
		opts.historyScope = scope
	}
}

// NewTool creates a new Tool that wraps the given agent.
func NewTool(agent agent.Agent, opts ...Option) *Tool {
	// Default to allowing summarization so the parent agent can perform its
	// normal post-tool reasoning unless opt-out is requested.
	options := &agentToolOptions{skipSummarization: false, historyScope: HistoryScopeIsolated}
	for _, opt := range opts {
		opt(options)
	}
	info := agent.Info()

	// Use the agent's input schema if available, otherwise fall back to default.
	var inputSchema *tool.Schema
	if info.InputSchema != nil {
		// Convert the agent's input schema to tool.Schema format.
		inputSchema = convertMapToToolSchema(info.InputSchema)
	} else {
		// Generate default input schema for the agent tool.
		inputSchema = &tool.Schema{
			Type:        "object",
			Description: "Input for the agent tool",
			Properties: map[string]*tool.Schema{
				"request": {
					Type:        "string",
					Description: "The request to send to the agent",
				},
			},
			Required: []string{"request"},
		}
	}
	var outputSchema *tool.Schema
	if info.OutputSchema != nil {
		outputSchema = convertMapToToolSchema(info.OutputSchema)
	} else {
		outputSchema = &tool.Schema{
			Type:        "string",
			Description: "The response from the agent",
		}
	}
	return &Tool{
		agent:             agent,
		skipSummarization: options.skipSummarization,
		streamInner:       options.streamInner,
		historyScope:      options.historyScope,
		name:              info.Name,
		description:       info.Description,
		inputSchema:       inputSchema,
		outputSchema:      outputSchema,
	}
}

// Call executes the agent tool with the provided JSON arguments.
func (at *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	message := model.NewUserMessage(string(jsonArgs))

	// Prefer to reuse parent invocation + session so the child can see parent
	// history according to the configured history scope.
	if parentInv, ok := agent.InvocationFromContext(ctx); ok && parentInv != nil && parentInv.Session != nil {
		// Build child filter key.
		childKey := at.agent.Info().Name + "-" + uuid.NewString()
		if at.historyScope == HistoryScopeParentBranch {
			if pk := parentInv.GetEventFilterKey(); pk != "" {
				childKey = pk + agent.EventFilterKeyDelimiter + childKey
			}
		}

		subInv := parentInv.Clone(
			agent.WithInvocationAgent(at.agent),
			agent.WithInvocationMessage(message),
			agent.WithInvocationEventFilterKey(childKey),
		)

		// Ensure current tool input is visible to the child content processor by
		// appending it into the shared session.
		if subInv.Session != nil && message.Content != "" {
			evt := event.NewResponseEvent(
				subInv.InvocationID,
				"user",
				&model.Response{Done: false, Choices: []model.Choice{{Index: 0, Message: message}}},
			)
			agent.InjectIntoEvent(subInv, evt)
			subInv.Session.Events = append(subInv.Session.Events, *evt)
		}

		evCh, err := at.agent.Run(agent.NewInvocationContext(ctx, subInv), subInv)
		if err != nil {
			return nil, fmt.Errorf("failed to run agent: %w", err)
		}
		var response strings.Builder
		for ev := range evCh {
			if ev.Error != nil {
				return nil, fmt.Errorf("agent error: %s", ev.Error.Message)
			}
			if ev.Response != nil && len(ev.Response.Choices) > 0 {
				choice := ev.Response.Choices[0]
				if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
					response.WriteString(choice.Message.Content)
				}
			}
		}
		return response.String(), nil
	}

	// Fallback: isolated in-memory run when parent invocation is not available.
	r := runner.NewRunner(
		at.name,
		at.agent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	evCh, err := r.Run(ctx, "tool_user", "tool_session", message)
	if err != nil {
		return nil, fmt.Errorf("failed to run agent: %w", err)
	}
	var response strings.Builder
	for ev := range evCh {
		if ev.Error != nil {
			return nil, fmt.Errorf("agent error: %s", ev.Error.Message)
		}
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]
			if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
				response.WriteString(choice.Message.Content)
			}
		}
	}
	return response.String(), nil
}

// StreamableCall executes the agent tool with streaming support and returns a stream reader.
// It runs the wrapped agent and forwards its streaming text output as chunks.
// The returned chunks' Content are plain strings representing incremental text.
func (at *Tool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	stream := tool.NewStream(64)

	go func() {
		defer stream.Writer.Close()

		// Try to reuse parent invocation for consistent invocationId/session
		parentInv, ok := agent.InvocationFromContext(ctx)
		message := model.NewUserMessage(string(jsonArgs))

		if ok && parentInv != nil && parentInv.Session != nil {
			// Build child filter key based on history scope.
			uniqueFilterKey := at.agent.Info().Name + "-" + uuid.NewString()
			if at.historyScope == HistoryScopeParentBranch {
				if pk := parentInv.GetEventFilterKey(); pk != "" {
					uniqueFilterKey = pk + agent.EventFilterKeyDelimiter + uniqueFilterKey
				}
			}

			subInv := parentInv.Clone(
				agent.WithInvocationAgent(at.agent),
				agent.WithInvocationMessage(message),
				// Reset event filter key to the sub-agent name so that content
				// processors fetch session messages belonging to the sub-agent,
				// not the parent agent. Use unique FilterKey to prevent cross-invocation event pollution.
				agent.WithInvocationEventFilterKey(uniqueFilterKey),
			)
			// Store tool input as Event via sub-agent's event channel (safe concurrency).
			// This ensures the tool input is available throughout all LLM calls within this AgentTool invocation.
			if message.Content != "" {
				evt := event.NewResponseEvent(
					subInv.InvocationID,
					"user", // Use "user" as author like Runner does for user messages.
					&model.Response{Done: false, Choices: []model.Choice{{Index: 0, Message: message}}},
				)
				agent.InjectIntoEvent(subInv, evt) // This will set the uniqueFilterKey.

				// Send the tool input event as the first event in the stream.
				if stream.Writer.Send(tool.StreamChunk{Content: evt}, nil) {
					return
				}
			}

			subCtx := agent.NewInvocationContext(ctx, subInv)
			evCh, err := at.agent.Run(subCtx, subInv)
			if err != nil {
				_ = stream.Writer.Send(tool.StreamChunk{Content: fmt.Sprintf("agent tool run error: %v", err)}, nil)
				return
			}
			for ev := range evCh {
				if stream.Writer.Send(tool.StreamChunk{Content: ev}, nil) {
					return
				}
			}
			return
		}

		// Fallback: run with ad-hoc runner
		r := runner.NewRunner(
			at.name,
			at.agent,
			runner.WithSessionService(inmemory.NewSessionService()),
		)
		evCh, err := r.Run(ctx, "tool_user", "tool_session", message)
		if err != nil {
			_ = stream.Writer.Send(tool.StreamChunk{Content: fmt.Sprintf("agent tool run error: %v", err)}, nil)
			return
		}
		for ev := range evCh {
			if ev != nil {
				if stream.Writer.Send(tool.StreamChunk{Content: ev}, nil) {
					return
				}
			}
		}
	}()

	return stream.Reader, nil
}

// SkipSummarization exposes whether the AgentTool prefers skipping
// outer-agent summarization after its tool.response.
func (at *Tool) SkipSummarization() bool { return at.skipSummarization }

// StreamInner exposes whether this AgentTool prefers the flow to treat it as
// streamable (forwarding inner deltas) versus callable-only.
func (at *Tool) StreamInner() bool { return at.streamInner }

// Declaration returns the tool's declaration information.
func (at *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         at.name,
		Description:  at.description,
		InputSchema:  at.inputSchema,
		OutputSchema: at.outputSchema,
	}
}

// convertMapToToolSchema converts a map[string]any schema to tool.Schema format.
// This function handles the conversion from the agent's input schema format to the tool schema format.
func convertMapToToolSchema(schema map[string]any) *tool.Schema {
	if schema == nil {
		return nil
	}
	bs, err := json.Marshal(schema)
	if err != nil {
		log.Errorf("json marshal schema error: %+v", err)
		return nil
	}
	result := &tool.Schema{}
	if err := json.Unmarshal(bs, result); err != nil {
		log.Errorf("json unmarshal schema error: %+v", err)
		return nil
	}
	return result
}
