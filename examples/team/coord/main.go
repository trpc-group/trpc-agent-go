//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a coordinator Team.
//
// A coordinator Team has one coordinator Agent that consults member Agents
// and produces the final answer.
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
	appName  = "team-coordinator-example"
	teamName = "team"

	agentRequirementsAnalyst = "requirements_analyst"
	agentSolutionDesigner    = "solution_designer"
	agentQualityReviewer     = "quality_reviewer"

	memberHistoryParent   = "parent"
	memberHistoryIsolated = "isolated"

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
	showInner = flag.Bool(
		"show-inner",
		true,
		"Show member transcript",
	)
	memberHistory = flag.String(
		"member-history",
		memberHistoryParent,
		"Member history scope: parent or isolated",
	)
	memberSkipSummarization = flag.Bool(
		"member-skip-summarization",
		false,
		"Skip coordinator summary after member tool",
	)
	enableParallelTools = flag.Bool(
		"parallel-tools",
		false,
		"Enable parallel tool execution",
	)
)

func main() {
	flag.Parse()

	runnerInstance, err := buildRunner(
		*modelName,
		*variant,
		*streaming,
		*showInner,
		*memberHistory,
		*memberSkipSummarization,
		*enableParallelTools,
	)
	if err != nil {
		log.Fatalf("build runner: %v", err)
	}
	defer runnerInstance.Close()

	sessionID := sessionPrefix + uuid.NewString()

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Timeout: %s\n", timeout.String())
	fmt.Printf("ShowInner: %t\n", *showInner)
	fmt.Printf("MemberHistory: %s\n", *memberHistory)
	fmt.Printf(
		"MemberSkipSummarization: %t\n",
		*memberSkipSummarization,
	)
	fmt.Printf("ParallelTools: %t\n", *enableParallelTools)
	fmt.Printf("Type %q to exit\n", chat.DefaultExitCommand)
	fmt.Println(strings.Repeat("=", dividerWidth))

	loopCfg := chat.LoopConfig{
		Runner:        runnerInstance,
		UserID:        demoUserID,
		SessionID:     sessionID,
		Timeout:       *timeout,
		ShowInner:     *showInner,
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
	showInner bool,
	memberHistory string,
	memberSkipSummarization bool,
	parallelTools bool,
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

	requirementsAnalyst := llmagent.New(
		agentRequirementsAnalyst,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Clarifies goals, constraints, and acceptance criteria.",
		),
		llmagent.WithInstruction(
			"Clarify requirements, constraints, and success criteria.",
		),
	)

	solutionDesigner := llmagent.New(
		agentSolutionDesigner,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Proposes solution options and recommends a design.",
		),
		llmagent.WithInstruction(
			"Propose a design with tradeoffs and a clear plan.",
		),
	)

	qualityReviewer := llmagent.New(
		agentQualityReviewer,
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Reviews for risks, edge cases, and missing details.",
		),
		llmagent.WithInstruction(
			"Find risks and gaps. Suggest concrete improvements.",
		),
	)

	members := []agent.Agent{
		requirementsAnalyst,
		solutionDesigner,
		qualityReviewer,
	}

	coordinatorOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription("Coordinates a small team of agents."),
		llmagent.WithInstruction(
			"You are the coordinator. Consult the right specialists, " +
				"then produce the final answer.",
		),
	}
	if parallelTools {
		coordinatorOpts = append(
			coordinatorOpts,
			llmagent.WithEnableParallelTools(true),
		)
	}

	coordinator := llmagent.New(teamName, coordinatorOpts...)

	memberCfg := team.DefaultMemberToolConfig()
	memberCfg.StreamInner = showInner
	memberCfg.SkipSummarization = memberSkipSummarization

	switch memberHistory {
	case memberHistoryParent:
		memberCfg.HistoryScope = team.HistoryScopeParentBranch
	case memberHistoryIsolated:
		memberCfg.HistoryScope = team.HistoryScopeIsolated
	default:
		return nil, fmt.Errorf(
			"unknown member-history %q",
			memberHistory,
		)
	}

	teamInstance, err := team.New(
		coordinator,
		members,
		team.WithMemberToolConfig(memberCfg),
	)
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
