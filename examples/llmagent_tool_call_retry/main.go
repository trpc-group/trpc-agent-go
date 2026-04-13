//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates single tool-call retry on an LLMAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName      = flag.String("model", defaultModelName, "Model name to use")
	baseURL        = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL")
	apiKey         = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key for the model service")
	location       = flag.String("location", defaultLocation, "Location passed to the weather tool")
	failCount      = flag.Int("fail", 1, "Number of transient failures before the tool succeeds")
	initialBackoff = flag.Duration("backoff", 200*time.Millisecond, "Initial retry backoff")
)

func main() {
	flag.Parse()
	printBanner(*modelName, *location, *failCount)
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := runScenario("without_retry", *failCount, nil, *failCount == 0); err != nil {
		return err
	}
	fmt.Println()
	policy := &tool.RetryPolicy{
		MaxAttempts:     *failCount + 1,
		InitialInterval: *initialBackoff,
		BackoffFactor:   2.0,
		MaxInterval:     2 * time.Second,
	}
	return runScenario("with_retry", *failCount, policy, true)
}

func runScenario(
	name string,
	initialFailures int,
	retryPolicy *tool.RetryPolicy,
	expectSuccess bool,
) error {
	printScenarioHeader(name)
	service := &flakyWeatherService{failuresRemaining: initialFailures}
	ag := buildAgent(*modelName, *baseURL, *apiKey, service, retryPolicy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := ag.Run(ctx, agent.NewInvocation(
		agent.WithInvocationMessage(newUserPrompt(*location)),
	))
	if err != nil {
		return err
	}
	toolResponse, runErr := collectScenarioResult(events, cancel)
	return printScenarioResult(name, service.Attempts(), toolResponse, runErr, expectSuccess)
}
