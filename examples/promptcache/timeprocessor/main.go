//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main compares prompt-cache behavior for time request processor modes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultModelName = "gpt-4o"
)

type cacheCase struct {
	Name        string
	Description string
	QueryPrefix string
	Options     []llmagent.Option
	Queries     []string
}

type usageStats struct {
	Requests       int
	PromptTokens   int
	CachedTokens   int
	RequestsCached int
}

func (s *usageStats) Add(usage *model.Usage) {
	if usage == nil {
		return
	}
	s.Requests++
	s.PromptTokens += usage.PromptTokens
	cached := usage.PromptTokensDetails.CachedTokens
	if cached > 0 {
		s.CachedTokens += cached
		s.RequestsCached++
	}
}

func (s usageStats) CacheRate() float64 {
	if s.PromptTokens == 0 {
		return 0
	}
	return float64(s.CachedTokens) / float64(s.PromptTokens) * 100
}

func main() {
	defaultModel := os.Getenv("MODEL_NAME")
	if defaultModel == "" {
		defaultModel = defaultModelName
	}

	modelName := flag.String("model", defaultModel, "OpenAI-compatible model name")
	caseName := flag.String("case", "all", "case to run: all, baseline, date-only, full-datetime, precise-tool")
	turnDelay := flag.Duration("turn-delay", 1200*time.Millisecond, "delay between turns so full-datetime changes")
	flag.Parse()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("OPENAI_API_KEY is required")
		return
	}

	llm := openai.New(*modelName, openAIOptions(apiKey)...)
	cases := selectCases(*caseName, buildCases())
	if len(cases) == 0 {
		fmt.Printf("unknown case %q\n", *caseName)
		return
	}

	fmt.Println("=== Time Processor Prompt Cache Demo ===")
	fmt.Printf("Model: %s\n", *modelName)
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		fmt.Printf("OpenAI-compatible base URL: %s\n", baseURL)
	}
	fmt.Printf("Cases: %s\n", strings.Join(caseNames(cases), ", "))
	fmt.Println()

	ctx := context.Background()
	results := make([]caseResult, 0, len(cases))
	for _, c := range cases {
		result := runCase(ctx, llm, c, *turnDelay)
		results = append(results, result)
	}

	fmt.Println(strings.Repeat("=", 72))
	fmt.Println("Summary")
	fmt.Println(strings.Repeat("=", 72))
	for _, result := range results {
		fmt.Printf(
			"%-18s cached=%5.1f%% cached_tokens=%d prompt_tokens=%d requests_with_cache=%d/%d\n",
			result.Name,
			result.Stats.CacheRate(),
			result.Stats.CachedTokens,
			result.Stats.PromptTokens,
			result.Stats.RequestsCached,
			result.Stats.Requests,
		)
	}
}

func openAIOptions(apiKey string) []openai.Option {
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if keyPrefix := os.Getenv("PROMPT_CACHE_KEY_PREFIX"); keyPrefix != "" {
		opts = append(opts, openai.WithExtraFields(map[string]any{
			"prompt_cache_key": keyPrefix,
		}))
	}
	return opts
}

func buildCases() []cacheCase {
	return []cacheCase{
		{
			Name:        "baseline",
			Description: "No time processor. This is the stable-prompt baseline.",
			QueryPrefix: "Answer briefly.",
			Queries:     regularQueries(),
		},
		{
			Name:        "date-only",
			Description: "WithAddCurrentTime(true) default mode. The system prompt contains only the current date.",
			QueryPrefix: "Answer briefly.",
			Options: []llmagent.Option{
				llmagent.WithAddCurrentTime(true),
			},
			Queries: regularQueries(),
		},
		{
			Name:        "full-datetime",
			Description: "Legacy-style full timestamp in the system prompt. This changes each turn and is less cache-friendly.",
			QueryPrefix: "Answer briefly.",
			Options: []llmagent.Option{
				llmagent.WithAddCurrentTime(true),
				llmagent.WithTimeFormat("2006-01-02 15:04:05 MST"),
			},
			Queries: regularQueries(),
		},
		{
			Name:        "precise-tool",
			Description: "Date-only system context plus built-in current_time tool for exact time.",
			QueryPrefix: "Use current_time only when exact clock time is needed.",
			Options: []llmagent.Option{
				llmagent.WithAddCurrentTime(true),
			},
			Queries: []string{
				"What is the singleton pattern?",
				"Use the current_time tool to tell me the current UTC time.",
				"What is the factory method pattern?",
				"Use the current_time tool again to tell me the current UTC date.",
			},
		},
	}
}

func regularQueries() []string {
	return []string{
		"What is the singleton pattern?",
		"What is the factory method pattern?",
		"What is the observer pattern?",
		"What is the strategy pattern?",
	}
}

type caseResult struct {
	Name  string
	Stats usageStats
}

func runCase(
	ctx context.Context,
	llm model.Model,
	c cacheCase,
	turnDelay time.Duration,
) caseResult {
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Case: %s\n", c.Name)
	fmt.Println(c.Description)
	fmt.Println(strings.Repeat("=", 72))

	agentOptions := []llmagent.Option{
		llmagent.WithModel(llm),
		llmagent.WithInstruction(longPrompt(c.QueryPrefix)),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	}
	agentOptions = append(agentOptions, c.Options...)
	agentInstance := llmagent.New("time-cache-"+c.Name, agentOptions...)

	r := runner.NewRunner(
		"promptcache-timeprocessor",
		agentInstance,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	stats := usageStats{}
	sessionID := fmt.Sprintf("%s-%d", c.Name, time.Now().UnixNano())
	for i, query := range c.Queries {
		if i > 0 && turnDelay > 0 {
			time.Sleep(turnDelay)
		}
		fmt.Printf("\nTurn %d: %s\n", i+1, query)
		usage, toolCalls, response := runTurn(ctx, r, sessionID, query)
		stats.Add(usage)
		if len(toolCalls) > 0 {
			fmt.Printf("  tool calls: %s\n", strings.Join(toolCalls, ", "))
		}
		if usage != nil {
			fmt.Printf(
				"  prompt_tokens=%d cached=%d cache_rate=%.1f%%\n",
				usage.PromptTokens,
				usage.PromptTokensDetails.CachedTokens,
				turnCacheRate(usage),
			)
		}
		fmt.Printf("  response: %s\n", trim(response, 180))
	}
	fmt.Printf("\nCase cache rate: %.1f%%\n\n", stats.CacheRate())
	return caseResult{Name: c.Name, Stats: stats}
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	query string,
) (*model.Usage, []string, string) {
	eventChan, err := r.Run(ctx, "demo-user", sessionID, model.NewUserMessage(query))
	if err != nil {
		return nil, nil, fmt.Sprintf("run error: %v", err)
	}
	var usage *model.Usage
	var response string
	var toolCalls []string
	for evt := range eventChan {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.Usage != nil {
			usage = evt.Response.Usage
		}
		for _, choice := range evt.Response.Choices {
			for _, tc := range choice.Message.ToolCalls {
				toolCalls = append(toolCalls, tc.Function.Name)
			}
			if choice.Message.Content != "" {
				response += choice.Message.Content
			}
		}
	}
	return usage, toolCalls, response
}

func longPrompt(prefix string) string {
	block := `You are a concise software architecture assistant.
Always answer with practical engineering trade-offs.
Keep terminology consistent across turns.
Prefer examples that are easy to compare.
This stable instruction block is intentionally repeated to exceed provider prompt-cache thresholds.
`
	return prefix + "\n\n" + strings.Repeat(block, 80)
}

func selectCases(name string, cases []cacheCase) []cacheCase {
	if name == "all" {
		return cases
	}
	for _, c := range cases {
		if c.Name == name {
			return []cacheCase{c}
		}
	}
	return nil
}

func caseNames(cases []cacheCase) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}
	return names
}

func turnCacheRate(usage *model.Usage) float64 {
	if usage == nil || usage.PromptTokens == 0 {
		return 0
	}
	return float64(usage.PromptTokensDetails.CachedTokens) /
		float64(usage.PromptTokens) * 100
}

func trim(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
