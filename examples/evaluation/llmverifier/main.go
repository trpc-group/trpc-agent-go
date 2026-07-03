//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/runner/bestofn"
)

var (
	modelName      = flag.String("model", "deepseek-v4-flash", "Candidate model name")
	judgeModelName = flag.String("judge-model", "deepseek-v4-flash", "Judge model name")
	baseURL        = flag.String("base-url", "", "OpenAI-compatible base URL")
	apiKey         = flag.String("api-key", "", "OpenAI-compatible API key")
	prompt         = flag.String("prompt", defaultPrompt, "User prompt")
	attempts       = flag.Int("attempts", 3, "Number of candidate attempts")
	judgeSamples   = flag.Int("judge-samples", 1, "Number of judge samples per candidate")
	maxTokens      = flag.Int("max-tokens", 512, "Candidate max output tokens")
	judgeMaxTokens = flag.Int("judge-max-tokens", 1200, "Judge max output tokens")
	temperature    = flag.Float64("temperature", 0.9, "Candidate sampling temperature")
)

const (
	appName      = "llm-verifier-example"
	judgeAppName = "llm-verifier-judge"
	userID       = "demo-user"
	sessionID    = "demo-session"
)

const defaultPrompt = "Explain LLM-as-a-Verifier for an online agent in no more than 120 words. Include the terms best-of-N and verifier."

func main() {
	flag.Parse()
	ctx := context.Background()
	modelOptions := make([]openai.Option, 0, 2)
	if *apiKey != "" {
		modelOptions = append(modelOptions, openai.WithAPIKey(*apiKey))
	}
	if *baseURL != "" {
		modelOptions = append(modelOptions, openai.WithBaseURL(*baseURL))
	}
	judgeRunner := runner.NewRunner(
		judgeAppName,
		newJudgeAgent(*judgeModelName, *judgeMaxTokens, modelOptions...),
	)
	defer judgeRunner.Close()
	bestOfNOpt, err := bestofn.NewRunnerOption(
		bestofn.WithAttempts(*attempts),
		bestofn.WithSelectionMode(bestofn.SelectionModePairwise),
		bestofn.WithEvalMetrics(llmVerifierMetric()),
		bestofn.WithJudgeRunner(judgeRunner),
		bestofn.WithJudgeRunnerNumSamples(*judgeSamples),
	)
	if err != nil {
		log.Fatalf("create best-of-N runner option: %v", err)
	}
	r := runner.NewRunner(
		appName,
		newCandidateAgent(*modelName, *maxTokens, *temperature, modelOptions...),
		bestOfNOpt,
	)
	defer r.Close()
	fmt.Printf("Prompt:\n%s\n\n", *prompt)
	fmt.Printf("Running %d candidate attempts and selecting with LLM verifier...\n\n", *attempts)
	answer, err := runOnce(ctx, r, *prompt)
	if err != nil {
		log.Fatalf("run agent: %v", err)
	}
	fmt.Println("Selected answer:")
	fmt.Println(answer)
}

func runOnce(ctx context.Context, r runner.Runner, prompt string) (string, error) {
	events, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return "", err
	}
	var final string
	for evt := range events {
		if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		if evt.IsError() {
			if evt.Response.Error != nil {
				return "", fmt.Errorf("%s", evt.Response.Error.Message)
			}
			return "", fmt.Errorf("agent emitted an error event")
		}
		if !evt.Response.IsPartial && evt.Response.Choices[0].Message.Content != "" {
			final = evt.Response.Choices[0].Message.Content
		}
	}
	if final == "" {
		return "", fmt.Errorf("agent returned an empty final answer")
	}
	return final, nil
}
