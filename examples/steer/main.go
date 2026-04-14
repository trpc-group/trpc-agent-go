//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates safe-boundary user steering in a single run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String(
		"model",
		defaultModelName(),
		"Name of the model to use",
	)
	toolDelay = flag.Duration(
		"tool-delay",
		2*time.Second,
		"How long the tool should wait before returning",
	)
	steerAfter = flag.Duration(
		"steer-after",
		1*time.Second,
		"When to enqueue the extra user message",
	)
	question = flag.String(
		"question",
		defaultQuestion,
		"Initial user message",
	)
	steerText = flag.String(
		"steer",
		defaultSteerText,
		"Extra user message to insert into the same run",
	)
)

const (
	fallbackModelName = "gpt-4.1-mini"
	openAIAPIKeyEnv   = "OPENAI_API_KEY"

	appName   = "steer-demo"
	agentName = "steer-agent"
	userID    = "demo-user"

	toolName        = "load_launch_brief"
	toolDescription = "Load the launch brief for a project."

	defaultQuestion  = "Draft a short launch announcement for Project Atlas."
	defaultSteerText = "Update the draft: make the tone warmer " +
		"and explicitly mention the May 20 launch date."
)

const agentInstruction = `You are a launch announcement assistant.

You must call load_launch_brief exactly once before answering.
After the tool result arrives, if there are newer user messages in the
conversation, you must incorporate the newest user requirements while keeping
the factual details from the tool result.
Keep the final answer under 120 words.`

type launchBriefRequest struct {
	Project string `json:"project"`
}

type launchBrief struct {
	Project    string   `json:"project"`
	LaunchDate string   `json:"launch_date"`
	Audience   string   `json:"audience"`
	Highlights []string `json:"highlights"`
}

type steerDemo struct {
	modelName  string
	toolDelay  time.Duration
	steerAfter time.Duration
	question   string
	steerText  string
}

func main() {
	flag.Parse()

	demo := &steerDemo{
		modelName:  *modelName,
		toolDelay:  *toolDelay,
		steerAfter: *steerAfter,
		question:   *question,
		steerText:  *steerText,
	}

	if err := demo.run(context.Background()); err != nil {
		log.Fatalf("demo failed: %v", err)
	}
}

func (d *steerDemo) run(ctx context.Context) error {
	if os.Getenv(openAIAPIKeyEnv) == "" {
		return errors.New(openAIAPIKeyEnv + " is not set")
	}

	modelInstance := openai.New(d.modelName)
	sessionService := sessioninmemory.NewSessionService()

	briefTool := function.NewFunctionTool(
		d.loadLaunchBrief,
		function.WithName(toolName),
		function.WithDescription(toolDescription),
	)

	ag := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(agentInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			Temperature: floatPtr(0.1),
			MaxTokens:   intPtr(300),
		}),
		llmagent.WithTools([]tool.Tool{briefTool}),
	)

	r := runner.NewRunner(
		appName,
		ag,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	requestID := fmt.Sprintf("steer-%d", time.Now().UnixNano())
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())

	fmt.Println("Single-run steer demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("RequestID: %s\n", requestID)
	fmt.Printf("SessionID: %s\n", sessionID)
	fmt.Printf("Initial question: %s\n", d.question)
	fmt.Printf("Queued steer message: %s\n", d.steerText)
	fmt.Println()

	go d.enqueueSteer(ctx, r, requestID)

	eventChan, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(d.question),
		agent.WithRequestID(requestID),
	)
	if err != nil {
		return err
	}

	return d.printRun(eventChan)
}

func (d *steerDemo) enqueueSteer(
	ctx context.Context,
	r runner.Runner,
	requestID string,
) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(d.steerAfter):
	}

	err := runner.EnqueueUserMessage(
		r,
		requestID,
		model.NewUserMessage(d.steerText),
	)
	if err != nil {
		fmt.Printf("[steer] enqueue failed: %v\n", err)
		return
	}
	fmt.Printf("[steer] queued extra user message at %s\n", d.steerAfter)
}

func (d *steerDemo) loadLaunchBrief(
	ctx context.Context,
	req launchBriefRequest,
) (launchBrief, error) {
	fmt.Printf("[tool] loading brief for %s\n", req.Project)

	select {
	case <-ctx.Done():
		return launchBrief{}, ctx.Err()
	case <-time.After(d.toolDelay):
	}

	return launchBrief{
		Project:    req.Project,
		LaunchDate: "May 20",
		Audience:   "design and product teams",
		Highlights: []string{
			"centralized release notes",
			"faster stakeholder updates",
			"lighter weekly reporting",
		},
	}, nil
}

func (d *steerDemo) printRun(eventChan <-chan *event.Event) error {
	var finalAnswer string

	for evt := range eventChan {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("%s", evt.Error.Message)
		}

		if evt.IsRunnerCompletion() {
			fmt.Println("[run] runner completion")
			continue
		}
		if len(evt.Choices) == 0 {
			continue
		}

		message := evt.Choices[0].Message

		if len(message.ToolCalls) > 0 {
			toolCall := message.ToolCalls[0]
			fmt.Printf(
				"[model] tool_call %s args=%s\n",
				toolCall.Function.Name,
				string(toolCall.Function.Arguments),
			)
			continue
		}

		switch message.Role {
		case model.RoleUser:
			if message.Content == d.steerText {
				fmt.Printf(
					"[queue] persisted queued user message: %s\n",
					message.Content,
				)
			}
		case model.RoleTool:
			fmt.Printf("[tool] result: %s\n", message.Content)
		case model.RoleAssistant:
			if message.Content != "" {
				finalAnswer = message.Content
				fmt.Printf("[assistant] %s\n", finalAnswer)
			}
		}
	}

	if finalAnswer == "" {
		return fmt.Errorf("run finished without a final assistant answer")
	}
	return nil
}

func defaultModelName() string {
	if modelName := os.Getenv("OPENAI_MODEL"); modelName != "" {
		return modelName
	}
	return fallbackModelName
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
