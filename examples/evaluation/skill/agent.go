//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func newSkillAgent(modelName string, stream bool, repo skill.Repository) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}

	return llmagent.New(
		"skill-eval-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithGenerationConfig(genCfg),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(localexec.New()),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithInstruction(`You are running in an evaluation.

Rules:
- You MUST use the skill named "write-ok" for the user request.
- Call skill_load for "write-ok" before calling skill_run.
- Then call skill_run to execute: bash scripts/write_ok.sh
- When calling skill_run, include output_files: ["out/ok.txt"].
- Do not call other skills.
- After tools finish, briefly confirm completion.`),
		llmagent.WithDescription("Agent for skill tool-call evaluation."),
	)
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
