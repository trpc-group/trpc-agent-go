//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using structured output together with Agent
// Skills. The agent is free to call tools first, and only the final answer
// must be a single JSON object matching the schema.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

var (
	flagModel     = flag.String("model", "deepseek-chat", "model name")
	flagStreaming = flag.Bool("streaming", true, "stream responses")
)

const (
	appName          = "structuredoutput-skills-demo"
	defaultSkillsDir = "skills"
)

const instructionText = `
You can use Agent Skills (tools) and must produce a typed JSON result.

Rules:
- You MAY call tools when needed.
- While calling tools, do not provide a user-facing answer.
- For every user request, do the following:
  1) Call skill_load for the "hello" skill.
  2) Call skill_run to run: bash scripts/hello.sh
  3) Return the final answer as JSON matching the schema.
`

type helloResult struct {
	Skill  string `json:"skill"`
	Output string `json:"output"`
}

func main() {
	flag.Parse()

	fmt.Println("Structured Output + Skills (Typed JSON)")
	fmt.Printf("Model: %s\n", *flagModel)
	fmt.Printf("Streaming: %t\n", *flagStreaming)
	fmt.Println("Type 'exit' to quit.")
	fmt.Println(strings.Repeat("=", 50))

	if err := run(); err != nil {
		fmt.Printf("run failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Model (OpenAI-compatible).
	modelInstance := openai.New(*flagModel)

	// Skills repository (defaults to ./skills in this directory).
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	skillsRoot := filepath.Join(cwd, defaultSkillsDir)
	repo, err := skill.NewFSRepository(skillsRoot)
	if err != nil {
		return fmt.Errorf("skills repo: %w", err)
	}

	// Local workspace executor for skill_run.
	exec := localexec.New()

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(800),
		Temperature: floatPtr(0.2),
		Stream:      *flagStreaming,
	}

	agentName := "structured-output-skills"
	a := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(exec),
		llmagent.WithStructuredOutputJSON(
			new(helloResult),
			true,
			"Run the hello skill and return its output",
		),
	)

	r := runner.NewRunner(
		appName,
		a,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("so-skill-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n\n", sessionID)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
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

		evCh, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(text))
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		for ev := range evCh {
			if ev.Error != nil {
				fmt.Printf("error: %s\n", ev.Error.Message)
				break
			}
			if ev.StructuredOutput != nil {
				if out, ok := ev.StructuredOutput.(*helloResult); ok {
					b, _ := json.MarshalIndent(out, "", "  ")
					fmt.Printf("\nTyped structured output:\n%s\n", string(b))
				}
			}
			if len(ev.Choices) == 0 {
				continue
			}
			if *flagStreaming {
				if s := ev.Choices[0].Delta.Content; s != "" {
					fmt.Print(s)
				}
			} else if s := ev.Choices[0].Message.Content; s != "" {
				fmt.Println(s)
			}
		}
		fmt.Println()
	}

	return scanner.Err()
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
