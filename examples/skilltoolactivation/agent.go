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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const instructionText = `
You demonstrate skill-based tool activation.

For every user request:
1. First call skill_load for "release-notes".
2. After the skill is loaded, use release_docs_read_file to read release_notes.md.
3. Answer only from the file content.
4. Do not call release_docs_read_file before skill_load.
`

func run(
	ctx context.Context,
	mode llmagent.ToolActivationMode,
	lifetime llmagent.ToolActivationLifetime,
) error {
	repo, err := skill.NewFSRepository(*flagSkillsRoot)
	if err != nil {
		return fmt.Errorf("load skills repo: %w", err)
	}
	releaseDocs, err := releaseDocsToolSet(*flagDocsRoot)
	if err != nil {
		return err
	}
	modelInstance := openai.New(
		*flagModel,
		openai.WithVariant(openai.VariantOpenAI),
	)
	genConfig := model.GenerationConfig{
		Temperature: floatPtr(0.2),
		Stream:      *flagStreaming,
	}
	agt := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Demonstrates activating a ToolSet after skill_load."),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSkills(repo),
		llmagent.WithTools([]tool.Tool{calculatorTool()}),
		llmagent.WithActivatableToolSets([]tool.ToolSet{releaseDocs}),
		llmagent.WithToolActivationOnSkillLoad(
			skillName,
			[]string{toolSetName},
			llmagent.WithToolActivationMode(mode),
			llmagent.WithToolActivationLifetime(lifetime),
		),
		llmagent.WithModelCallbacks(traceToolCallbacks(*flagTraceTools)),
	)
	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()
	sessionID := fmt.Sprintf("tool-activation-%d", time.Now().Unix())
	printRunHeader(mode, lifetime, sessionID)
	events, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(*flagPrompt),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	for evt := range events {
		printEvent(evt)
	}
	return nil
}

func floatPtr(v float64) *float64 {
	return &v
}
