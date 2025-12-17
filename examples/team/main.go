//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the top-level team package.
//
// It supports two modes:
//   - team: a coordinator agent calls member agents as tools and then
//     replies.
//   - swarm: member agents hand off to each other via transfer_to_agent.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/team"
)

const (
	appName = "team-example"

	modeTeam  = "team"
	modeSwarm = "swarm"

	teamName = "team"

	agentCoder      = "coder"
	agentResearcher = "researcher"
	agentReviewer   = "reviewer"

	exitCommand = "/exit"
)

var (
	mode      = flag.String("mode", modeTeam, "Mode: team or swarm")
	modelName = flag.String("model", "deepseek-chat", "Model name")
	variant   = flag.String("variant", "openai", "OpenAI provider variant")
	streaming = flag.Bool("streaming", true, "Enable streaming")
)

func main() {
	flag.Parse()

	r, err := buildRunner(*mode, *modelName, *variant, *streaming)
	if err != nil {
		log.Fatalf("build runner: %v", err)
	}
	defer r.Close()

	userID := "demo-user"
	sessionID := "demo-" + uuid.NewString()

	fmt.Printf("Mode: %s\n", *mode)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Type %q to exit\n", exitCommand)
	fmt.Println(strings.Repeat("=", 50))

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
		if text == exitCommand {
			return
		}

		evCh, err := r.Run(
			context.Background(),
			userID,
			sessionID,
			model.NewUserMessage(text),
		)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		printEvents(evCh)
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("read input: %v", err)
	}
}

func buildRunner(
	mode string,
	modelName string,
	variant string,
	streaming bool,
) (runner.Runner, error) {
	modelInstance := openai.New(
		modelName,
		openai.WithVariant(openai.Variant(variant)),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      streaming,
	}

	coder := llmagent.New(
		agentCoder,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Writes Go code and fixes bugs."),
		llmagent.WithInstruction(
			"You write Go code. When you need research or review, "+
				"transfer to another agent.",
		),
	)

	researcher := llmagent.New(
		agentResearcher,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Finds background info and clarifies goals."),
		llmagent.WithInstruction(
			"You gather context and clarify requirements. When implementation "+
				"is needed, transfer to the coder.",
		),
	)

	reviewer := llmagent.New(
		agentReviewer,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Reviews plans and checks for mistakes."),
		llmagent.WithInstruction(
			"You review work for correctness and clarity. When changes are "+
				"needed, transfer to the coder.",
		),
	)

	members := []agent.Agent{coder, researcher, reviewer}

	var root agent.Agent
	switch mode {
	case modeTeam:
		coordinator := llmagent.New(
			teamName,
			llmagent.WithModel(modelInstance),
			llmagent.WithGenerationConfig(genConfig),
			llmagent.WithDescription("Coordinates a small team of agents."),
			llmagent.WithInstruction(
				"You are the team coordinator. You can call member agents as "+
					"tools (tool names match agent names). Ask the right member, "+
					"then synthesize a final answer for the user.",
			),
		)
		tm, err := team.New(teamName, coordinator, members)
		if err != nil {
			return nil, err
		}
		root = tm
	case modeSwarm:
		tm, err := team.NewSwarm(teamName, agentResearcher, members)
		if err != nil {
			return nil, err
		}
		root = tm
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}

	sessionService := sessioninmemory.NewSessionService()
	return runner.NewRunner(
		appName,
		root,
		runner.WithSessionService(sessionService),
	), nil
}

func printEvents(evCh <-chan *event.Event) {
	for ev := range evCh {
		if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}

		if ev.Object == model.ObjectTypeTransfer {
			fmt.Printf("\n[%s] %s\n", ev.Author, firstContent(ev))
			continue
		}

		text := firstDelta(ev)
		if text != "" {
			fmt.Print(text)
			continue
		}

		text = firstContent(ev)
		if text != "" {
			fmt.Print(text)
		}

		if ev.IsFinalResponse() {
			fmt.Println()
		}
	}
}

func firstDelta(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Delta.Content
}

func firstContent(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Message.Content
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
