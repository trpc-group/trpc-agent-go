//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the toolcallid plugin with one real agent and one real tool.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolcallid"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName          = "toolcallid-demo"
	defaultModelName = "gpt-5.4"
	defaultVariant   = "openai"
	defaultPrompt    = "Use the calculator tool exactly once to compute 17 * 23, then answer briefly."
)

var (
	modelName = flag.String("model", defaultModelName, "Name of the model to use")
	variant   = flag.String("variant", defaultVariant, "OpenAI provider variant")
	streaming = flag.Bool("streaming", false, "Enable streaming responses")
	prompt    = flag.String("prompt", defaultPrompt, "The user prompt to send")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	ag := newAgent(*modelName, *variant, *streaming)
	run := runner.NewRunner(
		appName,
		ag,
		runner.WithPlugins(toolcallid.New()),
	)
	defer run.Close()
	printBanner(*modelName, *variant, *streaming, *prompt)
	eventCh, err := run.Run(
		ctx,
		"toolcallid-demo-user",
		"toolcallid-demo-session",
		model.NewUserMessage(*prompt),
		agent.WithRequestID(uuid.NewString()),
	)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
	if err := printEvents(eventCh); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}
