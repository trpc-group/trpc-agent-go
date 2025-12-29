//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a Swarm Team.
//
// A Swarm Team starts from an entry Agent. Each Agent can transfer control to
// another Agent using transfer_to_agent. The last Agent reply is the final
// answer.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/examples/team/internal/chat"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/team"
)

const (
	appName  = "team-swarm-example"
	teamName = "team"

	agentFacilitator   = "facilitator"
	agentDevilAdvocate = "devil_advocate"
	agentSummarizer    = "summarizer"

	entryAgentName = agentFacilitator

	defaultModelName = "deepseek-chat"
	defaultVariant   = "openai"

	defaultTimeout = 5 * time.Minute

	defaultMaxTokens   = 2000
	defaultTemperature = 0.7

	sessionPrefix = "demo-"
	demoUserID    = "demo-user"

	dividerWidth = 50
)

var (
	modelName = flag.String(
		"model",
		defaultModelName,
		"Model name",
	)
	variant = flag.String(
		"variant",
		defaultVariant,
		"OpenAI provider variant",
	)
	streaming = flag.Bool(
		"streaming",
		true,
		"Enable streaming",
	)
	timeout = flag.Duration(
		"timeout",
		defaultTimeout,
		"Request timeout",
	)
)

func main() {
	flag.Parse()

	runnerInstance, err := buildRunner(
		*modelName,
		*variant,
		*streaming,
	)
	if err != nil {
		log.Fatalf("build runner: %v", err)
	}
	defer runnerInstance.Close()

	sessionID := sessionPrefix + uuid.NewString()

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Timeout: %s\n", timeout.String())
	fmt.Printf("EntryAgent: %s\n", entryAgentName)
	fmt.Printf("Type %q to exit\n", chat.DefaultExitCommand)
	fmt.Println(strings.Repeat("=", dividerWidth))

	loopCfg := chat.LoopConfig{
		Runner:        runnerInstance,
		UserID:        demoUserID,
		SessionID:     sessionID,
		Timeout:       *timeout,
		ShowInner:     true,
		RootAgentName: teamName,
		ExitCommand:   chat.DefaultExitCommand,
	}

	if err := chat.Run(context.Background(), loopCfg); err != nil {
		log.Fatalf("run: %v", err)
	}
}

func buildRunner(
	modelName string,
	variant string,
	streaming bool,
) (runner.Runner, error) {
	modelInstance := openai.New(
		modelName,
		openai.WithVariant(openai.Variant(variant)),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(defaultMaxTokens),
		Temperature: floatPtr(defaultTemperature),
		Stream:      streaming,
	}

	facilitator := llmagent.New(
		agentFacilitator,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Facilitates a discussion and keeps it on track.",
		),
		llmagent.WithInstruction(
			"Facilitate a discussion. Ask clarifying questions, "+
				"invite other agents to contribute, then decide "+
				"whether to transfer control or to wrap up.",
		),
	)

	devilAdvocate := llmagent.New(
		agentDevilAdvocate,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Challenges assumptions and points out risks.",
		),
		llmagent.WithInstruction(
			"Challenge the current proposal. Point out risks, "+
				"missing cases, and counterarguments. If another "+
				"agent should respond next, transfer control.",
		),
	)

	summarizer := llmagent.New(
		agentSummarizer,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Summarizes the discussion and proposes next steps.",
		),
		llmagent.WithInstruction(
			"Summarize key points, tradeoffs, and a clear "+
				"decision with next steps. Do not transfer "+
				"control.",
		),
	)

	members := []agent.Agent{
		facilitator,
		devilAdvocate,
		summarizer,
	}
	teamInstance, err := team.NewSwarm(teamName, entryAgentName, members)
	if err != nil {
		return nil, err
	}

	sessionService := sessioninmemory.NewSessionService()
	return runner.NewRunner(
		appName,
		teamInstance,
		runner.WithSessionService(sessionService),
	), nil
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
