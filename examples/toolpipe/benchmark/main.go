//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides an A/B benchmark comparing agent performance
// with and without the toolpipe extension on the same research task.
//
// Usage:
//
//	go run . -model="gpt-5" -mode=both
//	go run . -model="gpt-5" -mode=baseline
//	go run . -model="gpt-5" -mode=toolpipe
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	oai "github.com/openai/openai-go"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension/toolpipe"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
)

// tasks defines multiple benchmark scenarios covering different use cases.
var tasks = []struct {
	Name string
	Task string
}{
	{
		Name: "needle-in-haystack",
		Task: "I need to know: 1) What is the default port that Go's net/http ListenAndServe uses? " +
			"2) What does the Effective Go doc say about the 'comma ok' idiom? " +
			"Fetch https://go.dev/doc/effective_go to find the answers. Be concise.",
	},
	{
		Name: "extract-structure",
		Task: "Fetch https://go.dev/doc/effective_go and list all the section headings (lines starting with #). Just the headings, nothing else.",
	},
	{
		Name: "targeted-search",
		Task: "Fetch https://news.ycombinator.com and tell me: are there any stories about Rust or Go programming languages on the front page right now? List only those titles.",
	},
	{
		Name: "json-field-extract",
		Task: "Fetch https://hn.algolia.com/api/v1/search?query=kubernetes&tags=story&hitsPerPage=30 and give me just the titles and point counts of the top 5 stories by points.",
	},
	{
		Name: "large-page-specific-section",
		Task: "Fetch https://go.dev/doc/effective_go and tell me specifically what it says about 'defer'. Only the defer section, be concise.",
	},
}

func main() {
	modelName := flag.String("model", "gpt-5", "Model name")
	mode := flag.String("mode", "both", "Run mode: baseline, toolpipe, or both")
	taskFilter := flag.String("task", "", "Run only a specific task by name (empty = all)")
	flag.Parse()

	// Select tasks to run.
	var selectedTasks []struct{ Name, Task string }
	for _, t := range tasks {
		if *taskFilter == "" || t.Name == *taskFilter {
			selectedTasks = append(selectedTasks, t)
		}
	}
	if len(selectedTasks) == 0 {
		fmt.Fprintf(os.Stderr, "No task matching %q. Available:\n", *taskFilter)
		for _, t := range tasks {
			fmt.Fprintf(os.Stderr, "  - %s\n", t.Name)
		}
		os.Exit(1)
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  ToolPipe A/B Benchmark")
	fmt.Printf("  Model: %s | Tasks: %d | Mode: %s\n", *modelName, len(selectedTasks), *mode)
	fmt.Println("═══════════════════════════════════════════════════════════════")

	type result struct {
		Name     string
		Baseline *Stats
		Toolpipe *Stats
	}
	var results []result

	for i, t := range selectedTasks {
		fmt.Printf("\n━━━ [%d/%d] %s ━━━\n", i+1, len(selectedTasks), t.Name)
		fmt.Printf("  %s\n\n", truncate(t.Task, 100))

		var baseline, toolpipeStats *Stats

		if *mode == "baseline" || *mode == "both" {
			fmt.Println("  ▶ BASELINE")
			baseline = runBenchmark(*modelName, false, t.Task)
		}
		if *mode == "toolpipe" || *mode == "both" {
			fmt.Println("  ▶ TOOLPIPE")
			toolpipeStats = runBenchmark(*modelName, true, t.Task)
		}

		results = append(results, result{t.Name, baseline, toolpipeStats})
	}

	// Print summary table.
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  SUMMARY")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("\n  %-25s %8s %8s %8s %8s %8s\n", "Task", "B_Tok", "T_Tok", "Δ_Tok", "B_Peak", "T_Peak")
	fmt.Printf("  %-25s %8s %8s %8s %8s %8s\n", strings.Repeat("─", 25), "────────", "────────", "────────", "────────", "────────")

	for _, r := range results {
		if r.Baseline == nil || r.Toolpipe == nil {
			continue
		}
		bTok := r.Baseline.InputTokens + r.Baseline.OutputTokens
		tTok := r.Toolpipe.InputTokens + r.Toolpipe.OutputTokens
		fmt.Printf("  %-25s %8d %8d %8s %8s %8s\n",
			r.Name,
			bTok, tTok, delta(bTok, tTok),
			formatBytes(r.Baseline.PeakContextBytes),
			formatBytes(r.Toolpipe.PeakContextBytes),
		)
	}
	fmt.Println()
}

// Stats collects benchmark metrics.
type Stats struct {
	Mode              string
	Duration          time.Duration
	Rounds            int32  // model call rounds
	ToolCalls         int32  // total tool invocations
	InputTokens       int64  // cumulative prompt tokens
	OutputTokens      int64  // cumulative completion tokens
	PeakContextBytes  int64  // peak request messages size in bytes
	TotalContextBytes int64  // sum of all request sizes
	FinalAnswer       string // the model's final text output
}

func runBenchmark(modelName string, withToolpipe bool, task string) *Stats {
	stats := &Stats{}
	if withToolpipe {
		stats.Mode = "toolpipe"
	} else {
		stats.Mode = "baseline"
	}

	// Model with instrumentation callbacks.
	modelOpts := []openai.Option{
		openai.WithChatRequestCallback(func(_ context.Context, req *oai.ChatCompletionNewParams) {
			atomic.AddInt32(&stats.Rounds, 1)
			// Measure request size.
			raw, _ := json.Marshal(req.Messages)
			size := int64(len(raw))
			atomic.AddInt64(&stats.TotalContextBytes, size)
			for {
				old := atomic.LoadInt64(&stats.PeakContextBytes)
				if size <= old || atomic.CompareAndSwapInt64(&stats.PeakContextBytes, old, size) {
					break
				}
			}
		}),
		openai.WithChatStreamCompleteCallback(func(_ context.Context, _ *oai.ChatCompletionNewParams, acc *oai.ChatCompletionAccumulator, _ error) {
			if acc == nil {
				return
			}
			if usage := acc.Usage; usage.TotalTokens > 0 {
				atomic.AddInt64(&stats.InputTokens, int64(usage.PromptTokens))
				atomic.AddInt64(&stats.OutputTokens, int64(usage.CompletionTokens))
			}
		}),
	}
	modelInstance := openai.New(modelName, modelOpts...)

	// Tools — only web_fetch (large output, ideal for toolpipe).
	fetchTool := httpfetch.NewTool(httpfetch.WithMaxContentLength(80000))

	// Agent options.
	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research assistant"),
		llmagent.WithInstruction("You are a thorough research assistant. Fetch the requested pages and synthesize findings into a concise answer."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(16000),
			Stream:    true,
		}),
		llmagent.WithTools([]tool.Tool{fetchTool}),
	}

	if withToolpipe {
		pipe := toolpipe.New(
			toolpipe.WithToolNames("web_fetch"),
			toolpipe.WithAllowedOps(toolpipe.OpGrep, toolpipe.OpHead, toolpipe.OpTail, toolpipe.OpJQ),
			toolpipe.WithMaxOutputBytes(32<<10),
		)
		agentOpts = append(agentOpts, llmagent.WithExtensions(pipe))
	}

	agent := llmagent.New("researcher", agentOpts...)
	r := runner.NewRunner("benchmark", agent)
	defer r.Close()

	sessionID := fmt.Sprintf("bench-%s-%d", stats.Mode, time.Now().UnixNano())

	// Run.
	start := time.Now()
	msg := model.NewUserMessage(task)
	eventChan, err := r.Run(context.Background(), "user", sessionID, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return stats
	}

	// Consume events.
	var answer strings.Builder
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]
			// Count tool calls.
			if len(choice.Message.ToolCalls) > 0 {
				atomic.AddInt32(&stats.ToolCalls, int32(len(choice.Message.ToolCalls)))
				for _, tc := range choice.Message.ToolCalls {
					fmt.Printf("  🔧 %s\n", tc.Function.Name)
				}
			}
			// Collect final text.
			if choice.Delta.Content != "" {
				answer.WriteString(choice.Delta.Content)
			}
		}
		if ev.IsFinalResponse() {
			break
		}
	}
	stats.Duration = time.Since(start)
	stats.FinalAnswer = answer.String()

	fmt.Printf("  ✅ Done in %s\n", stats.Duration.Round(time.Millisecond))
	return stats
}

func delta(a, b int64) string {
	diff := b - a
	if diff == 0 {
		return "="
	}
	if a == 0 {
		return "N/A"
	}
	pct := float64(diff) / float64(a) * 100
	if diff < 0 {
		return fmt.Sprintf("%.0f%%", pct)
	}
	return fmt.Sprintf("+%.0f%%", pct)
}

func formatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	return fmt.Sprintf("%.1fKB", float64(b)/1024)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Respect UTF-8 boundaries: walk backwards to find a valid rune start.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "..."
}

func intPtr(i int) *int { return &i }
