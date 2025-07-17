//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package agent provides agent tool implementations for the agent system.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
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
}

// WithSkipSummarization sets whether to skip summarization of the agent output.
func WithSkipSummarization(skip bool) Option {
	return func(opts *agentToolOptions) {
		opts.skipSummarization = skip
	}
}

// NewTool creates a new Tool that wraps the given agent.
func NewTool(agent agent.Agent, opts ...Option) *Tool {
	options := &agentToolOptions{}
	for _, opt := range opts {
		opt(options)
	}
	info := agent.Info()
	// Generate input schema for the agent tool.
	// For now, we'll use a simple string input schema.
	inputSchema := &tool.Schema{
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
	// Generate output schema for the agent tool.
	outputSchema := &tool.Schema{
		Type:        "string",
		Description: "The response from the agent",
	}
	return &Tool{
		agent:             agent,
		skipSummarization: options.skipSummarization,
		name:              info.Name,
		description:       info.Description,
		inputSchema:       inputSchema,
		outputSchema:      outputSchema,
	}
}

// Call executes the agent tool with the provided JSON arguments.
func (at *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	// Parse the input arguments.
	var input struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal(jsonArgs, &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	// Create a message for the agent.
	message := model.NewUserMessage(input.Request)

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

// Declaration returns the tool's declaration information.
func (at *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         at.name,
		Description:  at.description,
		InputSchema:  at.inputSchema,
		OutputSchema: at.outputSchema,
	}
}
