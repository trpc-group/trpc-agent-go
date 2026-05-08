//go:build integration

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestDeferredToolSetRealDeepSeek(t *testing.T) {
	modelName := strings.TrimSpace(os.Getenv("MODEL_NAME"))
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if modelName == "" || apiKey == "" {
		t.Skip("source ./dpskv4.sh before running this integration smoke test")
	}

	deferred, err := NewDeferredToolSet(
		WithStateNamespace("real-smoke"),
		WithTools(
			function.NewFunctionTool(
				func(_ context.Context, input struct {
					Location string `json:"location"`
				}) (string, error) {
					return "sunny-" + strings.ToLower(strings.TrimSpace(input.Location)), nil
				},
				function.WithName("weather_lookup"),
				function.WithDescription("Look up the current weather for one city."),
			),
			function.NewFunctionTool(
				func(_ context.Context, input struct {
					Ticker string `json:"ticker"`
				}) (string, error) {
					return "quote-" + strings.ToUpper(strings.TrimSpace(input.Ticker)), nil
				},
				function.WithName("stock_quote"),
				function.WithDescription("Look up one stock quote."),
			),
		),
		WithMaxResults(1),
	)
	require.NoError(t, err)

	var output bytes.Buffer
	loggedModel := &realLoggingModel{
		inner:    openai.New(modelName),
		output:   &output,
		deferred: deferred,
	}
	llm := llmagent.New(
		"real-deferred-tool-search",
		llmagent.WithModel(loggedModel),
		llmagent.WithInstruction(
			"You may only call tools that are currently visible. "+
				"For this task, use at most two tool calls total: first call tool_search "+
				"once to reveal the weather tool, then call weather_lookup once for Shenzhen. "+
				"After weather_lookup returns, answer the user in one short sentence and do "+
				"not call any more tools.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(512),
			Temperature: floatPtr(0),
			Stream:      false,
		}),
		llmagent.WithMaxToolIterations(4),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inv := agent.NewInvocation(
		agent.WithInvocationID("real-deferred-tool-search"),
		agent.WithInvocationMessage(model.NewUserMessage(
			"Call tool_search exactly once to find the weather tool for Shenzhen. "+
				"Then call weather_lookup exactly once for Shenzhen. "+
				"Then reply with one short sentence and stop calling tools.",
		)),
		agent.WithInvocationSession(
			session.NewSession("toolsearch-real-smoke", "demo-user", "real-deferred-tool-search"),
		),
	)
	events, err := llm.Run(ctx, inv)
	require.NoError(t, err)

	finalText, err := consumeRealEvents(events)
	t.Logf("sanitized runtime-tool-search trace:\n%s", output.String())
	if err != nil {
		if !strings.Contains(err.Error(), "max tool iterations") {
			require.NoError(t, err)
		}
		t.Logf("real model stopped before a final answer: %v", err)
	}
	if strings.TrimSpace(finalText) == "" {
		t.Log("real model did not emit a final assistant sentence; treating this as non-fatal for smoke coverage")
	}

	requests := loggedModel.Summaries()
	require.NotEmpty(t, requests)

	var sawSearch, sawLoadedWeather, sawVisibleWeather bool
	for _, req := range requests {
		if containsString(req.ToolNames, "tool_search") {
			sawSearch = true
		}
		if containsString(req.LoadedTools, "weather_lookup") {
			sawLoadedWeather = true
		}
		if containsString(req.ToolNames, "weather_lookup") {
			sawVisibleWeather = true
		}
	}
	require.True(t, sawSearch, "expected at least one request with tool_search")
	require.True(t, sawLoadedWeather, "expected weather_lookup to be loaded")
	require.True(t, sawVisibleWeather, "expected weather_lookup to become visible")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type realRequestSummary struct {
	ToolNames    []string
	MessageRoles []string
	LoadedTools  []string
}

type realLoggingModel struct {
	inner    model.Model
	output   *bytes.Buffer
	deferred *DeferredToolSet

	mu        sync.Mutex
	step      int
	summaries []realRequestSummary
}

func (m *realLoggingModel) Info() model.Info {
	return m.inner.Info()
}

func (m *realLoggingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.logRequest(ctx, req)
	return m.inner.GenerateContent(ctx, req)
}

func (m *realLoggingModel) logRequest(ctx context.Context, req *model.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.step++
	summary := realRequestSummary{
		ToolNames:    requestToolNames(req),
		MessageRoles: requestMessageRoles(req),
		LoadedTools:  m.deferred.LoadedToolNames(ctx),
	}
	m.summaries = append(m.summaries, summary)
	if m.output != nil {
		m.output.WriteString(
			"[model step " + strconv.Itoa(m.step) + "] roles=" +
				fmt.Sprint(summary.MessageRoles) + " tools=" +
				fmt.Sprint(summary.ToolNames) + " loaded=" +
				fmt.Sprint(summary.LoadedTools) + "\n",
		)
	}
}

func (m *realLoggingModel) Summaries() []realRequestSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]realRequestSummary, len(m.summaries))
	for i, summary := range m.summaries {
		out[i] = realRequestSummary{
			ToolNames:    append([]string(nil), summary.ToolNames...),
			MessageRoles: append([]string(nil), summary.MessageRoles...),
			LoadedTools:  append([]string(nil), summary.LoadedTools...),
		}
	}
	return out
}

func requestToolNames(req *model.Request) []string {
	if req == nil || len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func requestMessageRoles(req *model.Request) []string {
	if req == nil || len(req.Messages) == 0 {
		return nil
	}
	roles := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		roles = append(roles, string(msg.Role))
	}
	return roles
}

func consumeRealEvents(events <-chan *event.Event) (string, error) {
	var finalText string
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return finalText, fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
		}
		if ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}
		for _, choice := range ev.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				len(choice.Message.ToolCalls) == 0 &&
				strings.TrimSpace(choice.Message.Content) != "" {
				finalText = strings.TrimSpace(choice.Message.Content)
			}
		}
	}
	return finalText, nil
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
