//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates mid-turn summarization in a single run where the
// model performs multiple sequential tool calls before producing the final
// answer.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model name to use")
	steps     = flag.Int("steps", 5, "Sequential tool calls required in one turn")
	query     = flag.String("query",
		"Plan and execute the task using the required step tool calls.",
		"User message for the run")
	waitSec = flag.Int("wait-sec", 8, "Wait seconds for async summary")
)

func main() {
	flag.Parse()
	if *steps <= 0 {
		fmt.Println("steps must be greater than 0")
		os.Exit(1)
	}
	d := &sameTurnDemo{
		modelName: *modelName,
		steps:     *steps,
	}
	if err := d.run(context.Background(), *query, time.Duration(*waitSec)*time.Second); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

type sameTurnDemo struct {
	modelName      string
	steps          int
	runner         runner.Runner
	sessionService session.Service
	app            string
	userID         string
	sessionID      string
	requestSeq     int64
}

func (d *sameTurnDemo) run(ctx context.Context, input string, wait time.Duration) error {
	if err := d.setup(); err != nil {
		return err
	}
	defer d.runner.Close()
	fmt.Println("🧪 Same-Turn Tool-Call Summary Demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("Session: %s\n", d.sessionID)
	fmt.Printf("Configured step count: %d\n", d.steps)
	fmt.Println(strings.Repeat("=", 70))
	if err := d.runSingleTurn(ctx, input); err != nil {
		return err
	}
	fmt.Printf("\n⏳ Waiting up to %s for async summary...\n", wait)
	summaryText, err := d.waitSummary(ctx, wait)
	if err != nil {
		return err
	}
	fmt.Println(strings.Repeat("=", 70))
	if summaryText == "" {
		fmt.Println("📝 Summary: <empty>.")
	} else {
		fmt.Println("📝 Summary:")
		fmt.Println(summaryText)
	}
	return nil
}

func (d *sameTurnDemo) setup() error {
	agentModel := openai.New(d.modelName)
	summarizerModel := openai.New(d.modelName)
	sum := summary.NewSummarizer(
		summarizerModel,
		summary.WithChecksAny(
			summary.CheckEventThreshold(1),
		),
	)
	d.sessionService = inmemory.NewSessionService(
		inmemory.WithSummarizer(sum),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(64),
		inmemory.WithSummaryJobTimeout(45*time.Second),
	)
	requiredCalls := strconv.Itoa(d.steps)
	instruction := "You must execute the tool named step_worker exactly " +
		requiredCalls + " times in one run before giving the final answer. " +
		"Call it sequentially with step values starting at 1, increasing by 1 " +
		"until step=" + requiredCalls + ". Do not skip steps and do not batch " +
		"multiple steps in one call. After each tool result, continue to the " +
		"next step. At the end, provide a concise final answer that references " +
		"the observed steps."
	callbacks := model.NewCallbacks().RegisterBeforeModel(d.beforeModel)
	tools := []tool.Tool{
		function.NewFunctionTool(
			d.stepWorker,
			function.WithName("step_worker"),
			function.WithDescription("Executes one deterministic step and returns a verbose output."),
		),
	}
	ag := llmagent.New(
		"same-turn-summary-agent",
		llmagent.WithModel(agentModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("An assistant for validating same-turn summary behavior."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			Temperature: floatPtr(0),
			MaxTokens:   intPtr(3000),
		}),
		llmagent.WithTools(tools),
		llmagent.WithModelCallbacks(callbacks),
		llmagent.WithAddSessionSummary(true),
	)
	d.app = "summary-same-turn-demo-app"
	d.userID = "user"
	d.sessionID = "summary-same-turn-" + strconv.FormatInt(time.Now().Unix(), 10)
	d.runner = runner.NewRunner(
		d.app,
		ag,
		runner.WithSessionService(d.sessionService),
	)
	return nil
}

func (d *sameTurnDemo) runSingleTurn(ctx context.Context, input string) error {
	fmt.Println("👤 User:")
	fmt.Println(input)
	fmt.Println()
	evtCh, err := d.runner.Run(
		ctx, d.userID, d.sessionID, model.NewUserMessage(input),
	)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	var (
		assistantText string
		callCount     int
		resultCount   int
	)
	requestIDSet := make(map[string]struct{})
	invocationIDSet := make(map[string]struct{})
	for evt := range evtCh {
		if evt.RequestID != "" {
			requestIDSet[evt.RequestID] = struct{}{}
		}
		if evt.InvocationID != "" {
			invocationIDSet[evt.InvocationID] = struct{}{}
		}
		if evt.Error != nil {
			fmt.Printf("❌ Event error: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				for _, tc := range choice.Message.ToolCalls {
					callCount++
					fmt.Printf("🔧 Tool call #%d: %s %s\n",
						callCount, tc.Function.Name, strings.TrimSpace(string(tc.Function.Arguments)))
				}
			}
			if choice.Message.Role == model.RoleTool {
				resultCount++
				fmt.Printf("   ↳ Tool result #%d: %s\n",
					resultCount, preview(choice.Message.Content, 140))
			}
			if choice.Message.Role == model.RoleAssistant &&
				choice.Message.Content != "" {
				assistantText = choice.Message.Content
			}
		}
	}
	fmt.Println()
	fmt.Printf("🤖 Final answer: %s\n", preview(assistantText, 240))
	fmt.Printf("📊 Tool calls observed: %d, tool results observed: %d\n",
		callCount, resultCount)
	requestIDs := sortedSetKeys(requestIDSet)
	invocationIDs := sortedSetKeys(invocationIDSet)
	fmt.Printf("🔎 Request IDs observed: %v\n", requestIDs)
	fmt.Printf("🔎 Invocation IDs observed: %v\n", invocationIDs)
	if len(requestIDs) == 1 {
		fmt.Println("✅ Turn check: single RequestID observed; this is one turn.")
	} else {
		fmt.Println("⚠️ Turn check: multiple RequestIDs observed; may include multiple turns.")
	}
	return nil
}

func (d *sameTurnDemo) waitSummary(ctx context.Context, wait time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	const pollInterval = 300 * time.Millisecond
	for {
		if time.Now().After(deadline) {
			return "", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
		text, err := d.readSummary(ctx)
		if err != nil {
			return "", err
		}
		if text != "" {
			return text, nil
		}
	}
}

func (d *sameTurnDemo) readSummary(ctx context.Context) (string, error) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName: d.app, UserID: d.userID, SessionID: d.sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		return "", nil
	}
	if text, ok := d.sessionService.GetSessionSummaryText(ctx, sess); ok {
		return text, nil
	}
	return "", nil
}

func (d *sameTurnDemo) beforeModel(
	_ context.Context, args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	reqNum := atomic.AddInt64(&d.requestSeq, 1)
	fmt.Printf("🧾 BeforeModel request #%d, messages=%d\n",
		reqNum, len(args.Request.Messages))
	for i, msg := range args.Request.Messages {
		if isSessionSummaryMessage(msg) {
			fmt.Printf("   [%d] role=%s summary(full):\n%s\n",
				i, msg.Role, msg.Content)
			continue
		}
		fmt.Printf("   [%d] role=%s content=%q\n",
			i, msg.Role, preview(msg.Content, 120))
	}
	fmt.Println()
	return nil, nil
}

func isSessionSummaryMessage(msg model.Message) bool {
	if msg.Role != model.RoleSystem {
		return false
	}
	return strings.Contains(
		msg.Content,
		"<summary_of_previous_interactions>",
	) || strings.Contains(
		msg.Content,
		"Here is a brief summary of your previous interactions:",
	)
}

func (d *sameTurnDemo) stepWorker(_ context.Context, req stepArgs) (stepResult, error) {
	detail := strings.Repeat(
		"step-detail-"+strconv.Itoa(req.Step)+";",
		18,
	)
	return stepResult{
		Step:    req.Step,
		Task:    req.Task,
		Status:  "ok",
		Detail:  detail,
		Checked: true,
	}, nil
}

type stepArgs struct {
	Step int    `json:"step" description:"Current step number, starts at 1"`
	Task string `json:"task" description:"Task description for this step"`
}

type stepResult struct {
	Step    int    `json:"step"`
	Task    string `json:"task"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Checked bool   `json:"checked"`
}

func preview(s string, max int) string {
	clean := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if clean == "" {
		return "<empty>"
	}
	runes := []rune(clean)
	if len(runes) <= max {
		return clean
	}
	return string(runes[:max]) + "..."
}

func sortedSetKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
