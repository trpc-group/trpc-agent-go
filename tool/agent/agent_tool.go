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

// NewTool creates a new Tool that wraps the given agent.
func NewTool(agent agent.Agent, opts ...Option) *Tool {
	// Default to skipping summarization for AgentTool to avoid redundant
	// outer-agent summaries after tool.response. This mirrors ADK-style
	// behavior where the tool result is the end of the turn by default.
	options := &agentToolOptions{skipSummarization: true}
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
		name:              info.Name,
		description:       info.Description,
		inputSchema:       inputSchema,
		outputSchema:      outputSchema,
	}
}

// Call executes the agent tool with the provided JSON arguments.
func (at *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	message := model.NewUserMessage(string(jsonArgs))
	// Create a runner to execute the agent.
	runner := runner.NewRunner(
		at.name,
		at.agent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)

	// Run the agent.
	eventChan, err := runner.Run(ctx, "tool_user", "tool_session", message)
	if err != nil {
		return nil, fmt.Errorf("failed to run agent: %w", err)
	}

	// Collect the response from the agent.
	var response strings.Builder
	for event := range eventChan {
		if event.Error != nil {
			return nil, fmt.Errorf("agent error: %s", event.Error.Message)
		}

		if event.Response != nil && len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
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
			// Use random FilterKey for automatic isolation across multiple invocations.
			// This avoids the need for manual session cleanup while ensuring that
			// each AgentTool call has its own isolated event context.
			uniqueFilterKey := at.agent.Info().Name + "-" + uuid.NewString()

			subInv := parentInv.Clone(
				agent.WithInvocationAgent(at.agent),
				agent.WithInvocationMessage(message),
				// Reset event filter key to the sub-agent name so that content
				// processors fetch session messages belonging to the sub-agent,
				// not the parent agent. Use unique FilterKey to prevent cross-invocation event pollution.
				agent.WithInvocationEventFilterKey(uniqueFilterKey),
			)
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

// convertMapToToolSchema converts a map[string]interface{} schema to tool.Schema format.
// This function handles the conversion from the agent's input schema format to the tool schema format.
func convertMapToToolSchema(schema map[string]interface{}) *tool.Schema {
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
