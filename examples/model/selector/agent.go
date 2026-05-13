//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName                    = "model-selector-demo"
	agentName                  = "calculator-assistant"
	calculatorCalledStateKey   = "example:model_selector:calculator_called"
	calculatorAgentInstruction = `You are a calculator assistant.
For every user request, call calculator before answering.
After the tool result is available, answer in Chinese in one concise sentence.
Do not calculate mentally or invent results.`
)

func run(ctx context.Context, cfg appConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	modelOptions := openAIModelOptions()
	toolCallModel := openai.New(cfg.toolCallModelName, modelOptions...)
	finalModel := openai.New(cfg.finalModelName, modelOptions...)
	modelSelector := selectByToolState(toolCallModel, finalModel)
	r := newRunner(toolCallModel)
	defer r.Close()
	printBanner(cfg)
	eventChan, err := r.Run(
		ctx,
		"demo-user",
		"demo-session",
		model.NewUserMessage(userPrompt),
		agent.WithModelSelector(modelSelector),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	return printEvents(eventChan)
}

func newRunner(baseModel model.Model) runner.Runner {
	agentInstance := llmagent.New(
		agentName,
		llmagent.WithModel(baseModel),
		llmagent.WithInstruction(calculatorAgentInstruction),
		llmagent.WithTools(calculatorTools()),
	)
	return runner.NewRunner(appName, agentInstance)
}

func openAIModelOptions() []openai.Option {
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		return nil
	}
	return []openai.Option{openai.WithBaseURL(baseURL)}
}

func selectByToolState(toolCallModel, finalModel model.Model) agent.ModelSelector {
	return func(_ context.Context, inv *agent.Invocation) (model.Model, error) {
		calculatorCalled, ok := agent.GetStateValue[bool](inv, calculatorCalledStateKey)
		if ok && calculatorCalled {
			fmt.Printf("ModelSelector: %s (final answer)\n", finalModel.Info().Name)
			return finalModel, nil
		}
		fmt.Printf("ModelSelector: %s (tool planning)\n", toolCallModel.Info().Name)
		return toolCallModel, nil
	}
}
