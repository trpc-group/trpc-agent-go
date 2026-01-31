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
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName = flag.String(
		"model",
		"deepseek-chat",
		"Name of the model to use",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming mode for responses",
	)
	variant = flag.String(
		"variant",
		"deepseek",
		"Model variant: openai, deepseek, qwen, hunyuan",
	)
	completionPromise = flag.String(
		"completion-promise",
		"DONE",
		"Stop when assistant outputs <promise>...</promise>",
	)
	maxIterations = flag.Int(
		"max-iterations",
		10,
		"Max RalphLoop iterations per message",
	)
	maxLLMCalls = flag.Int(
		"max-llm-calls",
		0,
		"Max model calls per message (0 = auto)",
	)
)

const (
	appName   = "ralphloop-demo"
	agentName = "ralphloop-assistant"

	commandExit = "/exit"

	promiseTagOpen  = "<promise>"
	promiseTagClose = "</promise>"

	defaultMaxTokens   = 2000
	defaultTemperature = 0.7
	defaultExtraCalls  = 2
)

const agentInstruction = "Be helpful and concise."

func main() {
	flag.Parse()

	fmt.Printf("RalphLoop demo: interactive task loop\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Variant: %s\n", *variant)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf(
		"Stop token: %s%s%s\n",
		promiseTagOpen,
		*completionPromise,
		promiseTagClose,
	)
	fmt.Printf("Type %q to exit\n", commandExit)
	fmt.Println(strings.Repeat("=", 50))

	chat := &taskChat{
		modelName: *modelName,
		streaming: *streaming,
		variant:   *variant,

		completionPromise: *completionPromise,
		maxIterations:     *maxIterations,
		maxLLMCalls:       *maxLLMCalls,
	}

	if err := chat.run(context.Background()); err != nil {
		log.Fatalf("RalphLoop demo failed: %v", err)
	}
}

type taskChat struct {
	modelName string
	streaming bool
	variant   string

	completionPromise string
	maxIterations     int
	maxLLMCalls       int

	runner    runner.Runner
	userID    string
	sessionID string
}

func (c *taskChat) run(ctx context.Context) error {
	if err := c.setup(); err != nil {
		return err
	}

	defer c.runner.Close()
	return c.startChat(ctx)
}

func (c *taskChat) setup() error {
	modelInstance := openai.New(
		c.modelName,
		openai.WithVariant(openai.Variant(c.variant)),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(defaultMaxTokens),
		Temperature: floatPtr(defaultTemperature),
		Stream:      c.streaming,
	}

	callLimit := c.maxLLMCalls
	if callLimit <= 0 {
		callLimit = c.maxIterations + defaultExtraCalls
	}

	instruction := fmt.Sprintf(
		"%s\n\nStop only when you output %s%s%s.",
		agentInstruction,
		promiseTagOpen,
		c.completionPromise,
		promiseTagClose,
	)

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Task-focused assistant in RalphLoop mode."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithMaxLLMCalls(callLimit),
	)

	c.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithRalphLoop(runner.RalphLoopConfig{
			MaxIterations:     c.maxIterations,
			CompletionPromise: c.completionPromise,
		}),
	)
	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("ralphloop-session-%d", time.Now().Unix())
	fmt.Printf("✅ Ready! Session: %s\n\n", c.sessionID)
	return nil
}

func (c *taskChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		if strings.EqualFold(userInput, commandExit) {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *taskChat) processMessage(
	ctx context.Context,
	userMessage string,
) error {
	msg := model.NewUserMessage(userMessage)
	requestID := uuid.New().String()
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		msg,
		agent.WithRequestID(requestID),
	)
	if err != nil {
		return err
	}
	return c.processResponse(eventChan)
}

func (c *taskChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	finalSeen := false

	for evt := range eventChan {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}
		if len(evt.Choices) > 0 {
			content := extractContent(evt.Choices[0], c.streaming)
			if content != "" {
				fmt.Print(content)
			}
		}
		if evt.IsFinalResponse() {
			finalSeen = true
			fmt.Print("\n")
		}
		if evt.IsRunnerCompletion() {
			break
		}
	}

	if !finalSeen {
		fmt.Print("\n")
	}
	return nil
}

func extractContent(choice model.Choice, streaming bool) string {
	if streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
