//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/team"
)

func buildRunner(cfg runConfig) (runner.Runner, error) {
	modelInstance := buildModel(cfg)
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1200),
		Temperature: floatPtr(0),
		Stream:      cfg.Streaming,
	}
	modelCallbacks := newModelDebugCallbacks(cfg.ContentLimit)
	toolCallbacks := newToolDebugCallbacks(cfg.ContentLimit)
	parent := llmagent.New(
		parentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Entry agent that always delegates to the child agent."),
		llmagent.WithInstruction(parentInstruction()),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithToolCallbacks(toolCallbacks),
	)
	child := llmagent.New(
		childName,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Child agent that reports what input it can see."),
		llmagent.WithInstruction(childInstruction()),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithToolCallbacks(toolCallbacks),
	)
	sw, err := team.NewSwarm(teamName, parentName, []agent.Agent{parent, child}, swarmHandoffOptions(cfg)...)
	if err != nil {
		return nil, err
	}
	return runner.NewRunner(
		appName,
		sw,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	), nil
}

func buildModel(cfg runConfig) model.Model {
	if cfg.MockMode {
		return &scriptedSwarmModel{name: "mock-swarm-debug"}
	}
	modelOptions := []openai.Option{}
	if cfg.APIKey != "" {
		modelOptions = append(modelOptions, openai.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(cfg.BaseURL))
	}
	if cfg.PrintProviderJSON {
		modelOptions = append(modelOptions, openai.WithChatRequestJSONCallback(printProviderRequestJSON(cfg.ContentLimit)))
	}
	return openai.New(cfg.ModelName, modelOptions...)
}

func swarmHandoffOptions(cfg runConfig) []team.Option {
	var opts []team.Option
	if cfg.ChildIsolated {
		opts = append(opts, team.WithSwarmIndependentAgents())
	}
	if cfg.RewriteChildInput {
		opts = append(opts, team.WithSwarmHandoffInputBuilder(func(ctx context.Context, args team.SwarmHandoffInputArgs) (model.Message, error) {
			_ = ctx
			rendered, err := renderChildInput(cfg.ChildTemplate, childInputTemplateData{
				Input:     args.RootInput.Content,
				FromAgent: args.FromAgentName,
				ToAgent:   args.ToAgentName,
			})
			if err != nil {
				return model.Message{}, err
			}
			return model.NewUserMessage(rendered), nil
		}))
	}
	return opts
}

func parentInstruction() string {
	return "You are the parent agent in a Swarm Team. On your first model turn, do not answer the user directly. Always call transfer_to_agent with agent_name set to child. Set the message field to a child-facing message that includes the original user request."
}

func childInstruction() string {
	return "You are the child agent. Answer briefly. State whether your visible user-facing input includes the original user input, the transfer message, and any parent-agent conversation context."
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
