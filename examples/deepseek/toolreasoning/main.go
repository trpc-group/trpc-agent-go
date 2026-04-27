//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main runs a real DeepSeek v4 thinking-mode smoke test for multi-turn
// tool calls.
//
// It compares the current reasoning_content replay behavior against a legacy
// projector that strips all previous-turn reasoning_content. Per DeepSeek's
// thinking-mode documentation, the legacy behavior should fail with HTTP 400
// once a later user turn replays a prior tool-call turn without its reasoning.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", envOrDefault("MODEL_NAME", "deepseek-v4-flash"), "DeepSeek model name")
	baseURL   = flag.String("base-url", envOrDefault("OPENAI_BASE_URL", "https://api.deepseek.com/v1"), "DeepSeek OpenAI-compatible base URL")
	apiKey    = flag.String("api-key", envOrDefault("OPENAI_API_KEY", ""), "DeepSeek API key")
	mode      = flag.String("mode", "both", "Run mode: both, fixed, legacy")
)

func main() {
	flag.Parse()
	if *apiKey == "" {
		exitf("OPENAI_API_KEY is required; run from dpskv3.sh or pass -api-key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	switch *mode {
	case "both":
		if err := runCompare(ctx); err != nil {
			exitf("%v", err)
		}
	case "fixed":
		if err := runCase(ctx, "fixed", false); err != nil {
			exitf("fixed run failed: %v", err)
		}
	case "legacy":
		if err := runCase(ctx, "legacy", true); err != nil {
			exitf("legacy run failed as expected: %v", err)
		}
		exitf("legacy run unexpectedly succeeded; expected DeepSeek HTTP 400")
	default:
		exitf("unknown -mode %q; use both, fixed, or legacy", *mode)
	}
}

func runCompare(ctx context.Context) error {
	fmt.Println("== DeepSeek v4 thinking/tool-call reasoning replay comparison ==")
	fmt.Println("1) legacy: simulate old behavior by stripping previous-turn reasoning_content")
	legacyErr := runCase(ctx, "legacy", true)
	if legacyErr == nil {
		return errors.New("legacy run unexpectedly succeeded; expected DeepSeek HTTP 400")
	}
	fmt.Printf("legacy failed as expected: %v\n\n", legacyErr)

	fmt.Println("2) fixed: keep reasoning_content for previous requests that performed tool calls")
	if err := runCase(ctx, "fixed", false); err != nil {
		return fmt.Errorf("fixed run failed: %w", err)
	}
	fmt.Println("fixed run succeeded")
	return nil
}

func runCase(ctx context.Context, label string, legacyStripReasoning bool) error {
	r, closeFn := buildRunner(label, legacyStripReasoning)
	defer closeFn()

	sessionID := fmt.Sprintf("deepseek-tool-reasoning-%s-%d", label, time.Now().UnixNano())
	turns := []string{
		"Use tools to answer: what is tomorrow's weather in Hangzhou? First call get_date, then call get_weather.",
		"Now use tools again for Guangzhou tomorrow. Reuse the established workflow and give the final answer.",
	}

	stats := runStats{}
	for i, prompt := range turns {
		fmt.Printf("[%s] turn %d user: %s\n", label, i+1, prompt)
		if err := runTurn(ctx, r, sessionID, prompt, &stats); err != nil {
			return fmt.Errorf("turn %d: %w", i+1, err)
		}
	}
	if stats.toolCalls == 0 {
		return errors.New("model did not call tools; test did not exercise tool-call reasoning replay")
	}
	if stats.reasoningMessages == 0 {
		return errors.New("model did not return reasoning_content; check thinking mode/model support")
	}
	fmt.Printf("[%s] completed: tool_calls=%d reasoning_messages=%d\n", label, stats.toolCalls, stats.reasoningMessages)
	return nil
}

func buildRunner(label string, legacyStripReasoning bool) (runner.Runner, func()) {
	opts := []openai.Option{
		openai.WithVariant(openai.VariantDeepSeek),
		openai.WithBaseURL(*baseURL),
		openai.WithAPIKey(*apiKey),
	}
	modelInstance := openai.New(*modelName, opts...)

	genConfig := model.GenerationConfig{
		MaxTokens:       intPtr(4096),
		Stream:          false,
		ThinkingEnabled: boolPtr(true),
		ReasoningEffort: stringPtr("high"),
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("DeepSeek thinking-mode tool-call replay smoke test agent."),
		llmagent.WithInstruction(strings.TrimSpace(`
You are testing tool calling. For weather requests, always call get_date first
to determine tomorrow's date, then call get_weather with the city and date.
After tool results are available, answer briefly.
`)),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithReasoningContentMode(llmagent.ReasoningContentModeDiscardPreviousTurns),
		llmagent.WithTools(buildTools()),
	}
	if legacyStripReasoning {
		agentOpts = append(agentOpts, llmagent.WithEventMessageProjector(stripPreviousTurnReasoning))
	}
	llmAgent := llmagent.New("deepseek-tool-reasoning-"+label, agentOpts...)
	r := runner.NewRunner("deepseek-tool-reasoning", llmAgent)
	return r, func() {
		_ = r.Close()
	}
}

func stripPreviousTurnReasoning(inv *agent.Invocation, evt event.Event, msg model.Message) model.Message {
	if inv == nil || msg.Role != model.RoleAssistant {
		return msg
	}
	if evt.RequestID != inv.RunOptions.RequestID {
		msg.ReasoningContent = ""
	}
	return msg
}

type runStats struct {
	toolCalls         int
	reasoningMessages int
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	prompt string,
	stats *runStats,
) error {
	events, err := r.Run(ctx, "deepseek-user", sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return err
	}
	for evt := range events {
		if evt.Error != nil {
			return fmt.Errorf("%s: %s", evt.Error.Type, evt.Error.Message)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if len(msg.ToolCalls) > 0 {
				stats.toolCalls += len(msg.ToolCalls)
				for _, tc := range msg.ToolCalls {
					fmt.Printf("  tool_call: %s %s\n", tc.ID, tc.Function.Name)
				}
			}
			if msg.Role == model.RoleTool && msg.ToolID != "" {
				fmt.Printf("  tool_result: %s %s\n", msg.ToolID, strings.TrimSpace(msg.Content))
			}
			if msg.ReasoningContent != "" {
				stats.reasoningMessages++
			}
			if evt.IsFinalResponse() && msg.Content != "" {
				fmt.Printf("  assistant: %s\n", strings.TrimSpace(msg.Content))
			}
		}
	}
	return nil
}

type dateInput struct{}

type dateOutput struct {
	Today    string `json:"today"`
	Tomorrow string `json:"tomorrow"`
}

type weatherInput struct {
	City string `json:"city" jsonschema:"description=City name,required"`
	Date string `json:"date" jsonschema:"description=Date in YYYY-MM-DD format,required"`
}

type weatherOutput struct {
	City        string `json:"city"`
	Date        string `json:"date"`
	Condition   string `json:"condition"`
	Temperature string `json:"temperature"`
}

func buildTools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			func(context.Context, dateInput) (dateOutput, error) {
				today := time.Now().Format("2006-01-02")
				tomorrow := time.Now().Add(24 * time.Hour).Format("2006-01-02")
				return dateOutput{Today: today, Tomorrow: tomorrow}, nil
			},
			function.WithName("get_date"),
			function.WithDescription("Get today's date and tomorrow's date."),
		),
		function.NewFunctionTool(
			func(_ context.Context, in weatherInput) (weatherOutput, error) {
				return weatherOutput{
					City:        in.City,
					Date:        in.Date,
					Condition:   "Cloudy",
					Temperature: "7~13 C",
				}, nil
			},
			function.WithName("get_weather"),
			function.WithDescription("Get mocked weather for a city on a date."),
		),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intPtr(v int) *int          { return &v }
func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
