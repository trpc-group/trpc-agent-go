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
	"time"

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
	timeout   = flag.Duration("timeout", 5*time.Minute, "Request timeout")
	showInner = flag.Bool(
		"show-inner",
		true,
		"Show inner member transcript in team mode",
	)
)

func main() {
	flag.Parse()

	r, err := buildRunner(
		*mode,
		*modelName,
		*variant,
		*streaming,
		*showInner,
	)
	if err != nil {
		log.Fatalf("build runner: %v", err)
	}
	defer r.Close()

	userID := "demo-user"
	sessionID := "demo-" + uuid.NewString()

	fmt.Printf("Mode: %s\n", *mode)
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Timeout: %s\n", timeout.String())
	fmt.Printf("ShowInner: %t\n", *showInner)
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

		reqCtx, cancel := context.WithTimeout(
			context.Background(),
			*timeout,
		)
		evCh, err := r.Run(
			reqCtx,
			userID,
			sessionID,
			model.NewUserMessage(text),
		)
		if err != nil {
			cancel()
			fmt.Printf("Error: %v\n", err)
			continue
		}
		printEvents(evCh, *showInner)
		if reqCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "\n[timeout] error: request timed out")
		}
		fmt.Println()
		cancel()
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
	showInner bool,
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
					"tools. Ask the right member, then synthesize a final "+
					"answer for the user.",
			),
		)
		tm, err := team.New(
			coordinator,
			members,
			team.WithMemberToolStreamInner(showInner),
		)
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

func printEvents(evCh <-chan *event.Event, showInner bool) {
	printedDelta := make(map[string]bool)
	printedToolCalls := make(map[string]bool)
	printedToolResults := make(map[string]bool)
	toolNameByID := make(map[string]string)
	printedPrefix := make(map[string]bool)

	atLineStart := true

	for ev := range evCh {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Fprintf(
				os.Stderr,
				"\n[%s] error: %s\n",
				ev.Author,
				ev.Error.Message,
			)
			continue
		}
		if ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}

		if ev.Object == model.ObjectTypeTransfer {
			fmt.Printf("\n[%s] %s\n", ev.Author, firstContent(ev))
			atLineStart = true
			continue
		}

		rspID := ev.Response.ID
		if ev.Response.IsToolCallResponse() && !printedToolCalls[rspID] {
			printedToolCalls[rspID] = true
			recordToolIDs(toolNameByID, ev)
			printToolCalls(ev, showInner)
			atLineStart = true
		}

		if ev.IsToolResultResponse() {
			printToolResults(toolNameByID, printedToolResults, ev)
			atLineStart = true
			continue
		}

		if ev.Response.IsPartial {
			text := firstDelta(ev)
			if text != "" {
				if showInner && ev.Author != teamName &&
					!printedPrefix[rspID] {
					if !atLineStart {
						fmt.Println()
					}
					fmt.Printf("[%s] ", ev.Author)
					printedPrefix[rspID] = true
				}
				printedDelta[rspID] = true
				fmt.Print(text)
				atLineStart = strings.HasSuffix(text, "\n")
			}
			continue
		}

		if printedDelta[rspID] {
			if ev.IsFinalResponse() {
				delete(printedDelta, rspID)
				fmt.Println()
				atLineStart = true
			}
			continue
		}

		text := firstContent(ev)
		if text != "" {
			if showInner && ev.Author != teamName &&
				!printedPrefix[rspID] {
				if !atLineStart {
					fmt.Println()
				}
				fmt.Printf("[%s] ", ev.Author)
				printedPrefix[rspID] = true
			}
			fmt.Print(text)
			atLineStart = strings.HasSuffix(text, "\n")
		}

		if ev.IsFinalResponse() {
			fmt.Println()
			atLineStart = true
		}
	}
}

func printToolCalls(ev *event.Event, showArgs bool) {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return
	}

	choice := ev.Response.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}
	if len(toolCalls) == 0 {
		return
	}

	fmt.Print("\n[tools] ")
	for i, tc := range toolCalls {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(tc.Function.Name)
	}
	fmt.Println()

	if !showArgs {
		return
	}
	for _, tc := range toolCalls {
		if len(tc.Function.Arguments) == 0 {
			continue
		}
		fmt.Printf(
			"[tool.args] %s: %s\n",
			tc.Function.Name,
			string(tc.Function.Arguments),
		)
	}
}

func recordToolIDs(toolNameByID map[string]string, ev *event.Event) {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return
	}

	choice := ev.Response.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}
	for _, tc := range toolCalls {
		if tc.ID == "" {
			continue
		}
		toolNameByID[tc.ID] = tc.Function.Name
	}
}

func printToolResults(
	toolNameByID map[string]string,
	printed map[string]bool,
	ev *event.Event,
) {
	if ev == nil || ev.Response == nil {
		return
	}
	for _, ch := range ev.Response.Choices {
		toolID := ch.Message.ToolID
		if toolID == "" {
			toolID = ch.Delta.ToolID
		}
		if toolID == "" || printed[toolID] {
			continue
		}
		printed[toolID] = true

		name := toolNameByID[toolID]
		if name == "" {
			name = toolID
		}
		fmt.Printf("[tool.done] %s\n", name)
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
