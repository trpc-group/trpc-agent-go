//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates direct host command execution with an LLM
// agent.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

const appName = "hostexec-demo"

func main() {
	modelName := flag.String("model", "deepseek-v4-flash", "Model name to use")
	baseDir := flag.String("base-dir", ".", "Base directory for commands")
	flag.Parse()

	app, err := newApp(*modelName, *baseDir)
	if err != nil {
		log.Fatalf("setup failed: %v", err)
	}
	defer app.runner.Close()
	defer app.tools.Close()

	if err := app.run(context.Background()); err != nil {
		log.Fatalf("run failed: %v", err)
	}
}

type cliApp struct {
	modelName string
	baseDir   string
	tools     tool.ToolSet
	runner    runner.Runner
	userID    string
	sessionID string
}

func newApp(modelName string, baseDir string) (*cliApp, error) {
	toolSet, err := hostexec.NewToolSet(
		hostexec.WithBaseDir(baseDir),
	)
	if err != nil {
		return nil, fmt.Errorf("create hostexec tool set: %w", err)
	}

	agt := llmagent.New(
		"hostexec-assistant",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription(
			"An assistant that can run local shell commands",
		),
		llmagent.WithInstruction(hostExecInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(2000),
			Stream:    true,
		}),
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
	)

	return &cliApp{
		modelName: modelName,
		baseDir:   baseDir,
		tools:     toolSet,
		runner:    runner.NewRunner(appName, agt),
		userID:    "user",
		sessionID: fmt.Sprintf("hostexec-%d", time.Now().Unix()),
	}, nil
}

func (a *cliApp) run(ctx context.Context) error {
	fmt.Printf("Host Exec Demo\n")
	fmt.Printf("Model: %s\n", a.modelName)
	fmt.Printf("Base Dir: %s\n", a.baseDir)
	fmt.Printf("Session: %s\n", a.sessionID)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("Ask for local shell work inside the base directory.")
	fmt.Println("Examples:")
	fmt.Println("  - List files in this project")
	fmt.Println("  - Run go test ./... and summarize failures")
	fmt.Println("  - Start a long command and keep polling it")
	fmt.Println("Type 'exit' to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "exit") {
			return nil
		}
		if err := a.runTurn(ctx, text); err != nil {
			fmt.Printf("Error: %v\n\n", err)
		}
	}
	return scanner.Err()
}

func (a *cliApp) runTurn(
	ctx context.Context,
	userText string,
) error {
	evCh, err := a.runner.Run(
		ctx,
		a.userID,
		a.sessionID,
		model.NewUserMessage(userText),
	)
	if err != nil {
		return err
	}

	fmt.Print("Assistant: ")
	var printed bool
	for ev := range evCh {
		if ev.Error != nil {
			fmt.Printf("\nError: %s\n", ev.Error.Message)
			continue
		}
		if err := printToolCalls(ev); err != nil {
			return err
		}
		if len(ev.Response.Choices) == 0 {
			continue
		}
		choice := ev.Response.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
			printed = true
		}
		if choice.Message.Content != "" && !ev.Done {
			fmt.Print(choice.Message.Content)
			printed = true
		}
	}
	if printed {
		fmt.Println()
	}
	fmt.Println()
	return nil
}

func printToolCalls(ev *event.Event) error {
	if len(ev.Response.Choices) == 0 {
		return nil
	}
	msg := ev.Response.Choices[0].Message
	if len(msg.ToolCalls) == 0 {
		return nil
	}
	fmt.Println()
	for _, tc := range msg.ToolCalls {
		fmt.Printf("Tool: %s\n", tc.Function.Name)
		fmt.Printf("Args: %s\n", tc.Function.Arguments)
	}
	fmt.Print("Assistant: ")
	return nil
}

func intPtr(v int) *int {
	return &v
}

const hostExecInstruction = `You are a careful assistant with a direct
host command tool.

Use exec_command for project-local shell work such as listing files,
running builds, running tests, or collecting command output.

Rules:
- Stay inside the configured base directory unless the user explicitly
  asks for another workdir.
- Prefer concise, non-interactive commands.
- For long-running commands, use exec_command with a positive
  yield_time_ms, then continue polling with write_stdin using empty
  chars.
- Use kill_session if a background command should stop.
- Summarize command results clearly after the tool output is available.`
