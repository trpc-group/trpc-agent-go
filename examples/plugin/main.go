//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates Runner plugins with a small interactive chat.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultModelName = "deepseek-chat"
	defaultVariant   = "openai"

	appName   = "plugin-demo"
	agentName = "chat-assistant"

	cmdExit = "/exit"
	cmdHelp = "/help"

	separatorWidth = 50

	globalInstruction = "Follow security policies. Be helpful and concise."
	agentInstruction  = "Use tools for exact math when needed."
)

var (
	modelName = flag.String(
		"model",
		defaultModelName,
		"Name of the model to use",
	)
	variant = flag.String(
		"variant",
		defaultVariant,
		"OpenAI provider variant",
	)
	streaming = flag.Bool(
		"streaming",
		false,
		"Enable streaming responses",
	)
	debug = flag.Bool(
		"debug",
		false,
		"Print plugin debug lines",
	)
)

func main() {
	flag.Parse()

	fmt.Println("🔌 Runner plugin demo")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Printf("Debug: %t\n", *debug)
	fmt.Printf("Type %q to exit, %q for help\n", cmdExit, cmdHelp)
	fmt.Println(strings.Repeat("=", separatorWidth))

	app := &chatApp{
		modelName: *modelName,
		variant:   *variant,
		streaming: *streaming,
		debug:     *debug,
	}
	if err := app.run(context.Background()); err != nil {
		fmt.Printf("❌ error: %v\n", err)
		os.Exit(1)
	}
}

type chatApp struct {
	modelName string
	variant   string
	streaming bool
	debug     bool

	runner    runner.Runner
	userID    string
	sessionID string
}

func (a *chatApp) run(ctx context.Context) error {
	if err := a.setup(); err != nil {
		return err
	}
	defer a.runner.Close()
	return a.loop(ctx)
}

func (a *chatApp) setup() error {
	modelInstance := openai.New(
		a.modelName,
		openai.WithVariant(openai.Variant(a.variant)),
	)
	sessionService := sessioninmemory.NewSessionService()

	genConfig := model.GenerationConfig{Stream: a.streaming}
	tools := []tool.Tool{newCalculatorTool()}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A chat agent with plugins enabled."),
		llmagent.WithInstruction(agentInstruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)

	a.runner = runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
		runner.WithPlugins(
			plugin.NewLogging(),
			plugin.NewGlobalInstruction(globalInstruction),
			newDemoPlugin(a.debug),
		),
	)

	a.userID = "demo-user"
	a.sessionID = fmt.Sprintf("demo-session-%d", time.Now().Unix())

	fmt.Printf("✅ Session: %s\n\n", a.sessionID)
	return nil
}

func (a *chatApp) loop(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		switch text {
		case cmdExit:
			fmt.Println("👋 Goodbye!")
			return nil
		case cmdHelp:
			a.printHelp()
			continue
		}
		if err := a.runOnce(ctx, text); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (a *chatApp) printHelp() {
	fmt.Println("Commands:")
	fmt.Printf("  %s: exit\n", cmdExit)
	fmt.Printf("  %s: show help\n", cmdHelp)
	fmt.Println()
	fmt.Println("Plugin hints:")
	fmt.Printf("  - Send %q to short-circuit the model.\n", denyKeyword)
	fmt.Printf(
		"  - Ask for a calculation to trigger %q.\n",
		toolNameCalculator,
	)
	fmt.Println()
}

func (a *chatApp) runOnce(ctx context.Context, userText string) error {
	reqID := uuid.NewString()
	msg := model.NewUserMessage(userText)
	evCh, err := a.runner.Run(
		ctx,
		a.userID,
		a.sessionID,
		msg,
		agent.WithRequestID(reqID),
	)
	if err != nil {
		return err
	}
	return a.printEvents(evCh)
}

func (a *chatApp) printEvents(evCh <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var lastFull string
	for evt := range evCh {
		if evt == nil {
			continue
		}

		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		if evt.IsToolCallResponse() {
			a.printToolCalls(evt)
			continue
		}
		if evt.IsToolResultResponse() {
			a.printToolResults(evt)
			continue
		}

		if evt.IsRunnerCompletion() {
			break
		}

		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}

		choice := evt.Response.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
			continue
		}
		msg := choice.Message
		if msg.Role != model.RoleAssistant || msg.Content == "" {
			continue
		}
		if msg.Content == lastFull {
			continue
		}
		fmt.Println(msg.Content)
		lastFull = msg.Content

		if a.debug {
			fmt.Printf("[debug] tag=%s author=%s\n", evt.Tag, evt.Author)
		}
	}
	return nil
}

func (a *chatApp) printToolCalls(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	fmt.Printf("\n🔧 Tool calls:\n")
	for _, choice := range evt.Response.Choices {
		for _, tc := range choice.Message.ToolCalls {
			fmt.Printf(
				"  - %s(%s)\n",
				tc.Function.Name,
				string(tc.Function.Arguments),
			)
		}
	}
}

func (a *chatApp) printToolResults(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	fmt.Printf("\n✅ Tool results:\n")
	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleTool || msg.Content == "" {
			continue
		}
		fmt.Printf("  - %s\n", msg.Content)
	}
}
