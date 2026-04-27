//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"os"
	"strings"
	"time"
)

const (
	appName              = "swarm-input-debug-example"
	teamName             = "debug_swarm"
	parentName           = "parent"
	childName            = "child"
	defaultModel         = "deepseek-chat"
	defaultTimeout       = 5 * time.Minute
	defaultInput         = "Original user input: Please summarize whether the child agent sees only the transfer message or also the parent conversation."
	defaultChildTemplate = `CHILD_TEMPLATE:
Please inspect your model input and answer from the child perspective.
ORIGINAL_USER_INPUT: {{.Input}}`
	userID        = "debug-user"
	sessionPrefix = "swarm-input-debug-"
	defaultLimit  = 4000
)

var (
	modelName         = flag.String("model", defaultModel, "Model name.")
	baseURL           = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL.")
	apiKey            = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key.")
	input             = flag.String("input", defaultInput, "One user message sent to the swarm team.")
	mockMode          = flag.Bool("mock", false, "Use a scripted local model instead of calling a remote model.")
	streaming         = flag.Bool("streaming", false, "Enable model streaming.")
	childIsolated     = flag.Bool("child-isolated", false, "Use isolated invocation history for the child agent.")
	rewriteChildInput = flag.Bool("rewrite-child-input", false, "Rewrite transfer_to_agent.message from the original user input.")
	childTemplate     = flag.String("child-template", defaultChildTemplate, "Text/template used when rewriting the child input.")
	timeout           = flag.Duration("timeout", defaultTimeout, "Run timeout.")
	contentLimit      = flag.Int("content-limit", defaultLimit, "Maximum printed characters per message.")
	printProviderJSON = flag.Bool("print-provider-json", false, "Print the OpenAI provider JSON request.")
)

type runConfig struct {
	ModelName         string
	BaseURL           string
	APIKey            string
	Input             string
	MockMode          bool
	Streaming         bool
	ChildIsolated     bool
	RewriteChildInput bool
	ChildTemplate     string
	Timeout           time.Duration
	ContentLimit      int
	PrintProviderJSON bool
}

func configFromFlags() runConfig {
	return runConfig{
		ModelName:         *modelName,
		BaseURL:           *baseURL,
		APIKey:            *apiKey,
		Input:             strings.TrimSpace(*input),
		MockMode:          *mockMode,
		Streaming:         *streaming,
		ChildIsolated:     *childIsolated,
		RewriteChildInput: *rewriteChildInput,
		ChildTemplate:     *childTemplate,
		Timeout:           *timeout,
		ContentLimit:      *contentLimit,
		PrintProviderJSON: *printProviderJSON,
	}
}
