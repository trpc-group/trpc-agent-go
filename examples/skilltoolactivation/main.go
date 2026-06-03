//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates Skill-triggered tool activation.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

const (
	appName          = "skill-tool-activation-example"
	agentName        = "skill-tool-activation-agent"
	userID           = "skill-tool-activation-user"
	skillName        = "release-notes"
	toolSetName      = "release_docs"
	defaultModelName = "deepseek-v4-flash"
)

var (
	flagModel = flag.String(
		"model",
		defaultModelName,
		"OpenAI-compatible model name",
	)
	flagMode = flag.String(
		"mode",
		string(llmagent.ToolActivationModeInclude),
		"tool activation mode: include|only",
	)
	flagLifetime = flag.String(
		"lifetime",
		string(llmagent.ToolActivationLifetimeInvocation),
		"tool activation lifetime: invocation|session",
	)
	flagSkillsRoot = flag.String(
		"skills-root",
		defaultExamplePath("skills"),
		"skills root directory",
	)
	flagDocsRoot = flag.String(
		"docs-root",
		defaultExamplePath("release_docs"),
		"directory exposed by the activatable release_docs ToolSet",
	)
	flagStreaming = flag.Bool(
		"streaming",
		false,
		"stream model responses",
	)
	flagTraceTools = flag.Bool(
		"trace-tools",
		true,
		"print model-visible tool names before each model request",
	)
	flagPrompt = flag.String(
		"prompt",
		"Use the release-notes skill to summarize the release date, owners, and rollout checklist.",
		"user prompt sent to the agent",
	)
)

func main() {
	flag.Parse()
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}
	mode, err := parseActivationMode(*flagMode)
	if err != nil {
		log.Fatal(err)
	}
	lifetime, err := parseActivationLifetime(*flagLifetime)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), mode, lifetime); err != nil {
		log.Fatal(err)
	}
}

func defaultExamplePath(name string) string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return name
	}
	return filepath.Join(filepath.Dir(filename), name)
}
