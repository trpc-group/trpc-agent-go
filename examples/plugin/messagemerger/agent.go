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
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin/messagemerger"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func newRunner() runner.Runner {
	modelInstance := openai.New(*modelName)
	agentInstance := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(
			"Provide a concise travel answer based on the conversation context.",
		),
	)
	return runner.NewRunner(
		appName,
		agentInstance,
		runner.WithPlugins(
			messagemerger.New(),
		),
	)
}
