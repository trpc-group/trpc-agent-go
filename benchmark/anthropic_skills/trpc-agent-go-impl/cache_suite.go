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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
)

type promptCacheResult struct {
	Label    string
	Duration time.Duration
	Usage    usageTotals
}

func runPromptCacheSuite(
	repo skillrepo.Repository,
	exec codeexecutor.CodeExecutor,
	modelName string,
	debug bool,
	progress bool,
) error {
	progressf(progress, "🧠 Prompt Cache Suite")
	progressf(progress, "  Model: %s", modelName)
	progressf(progress, "  Case: internal-comms (skill_load + select_docs)")

	results := make([]promptCacheResult, 0, 2)
	modes := []struct {
		label              string
		loadedInToolResult bool
		suffix             string
	}{
		{
			label:              "A: system prompt (legacy)",
			loadedInToolResult: false,
			suffix:             "legacy",
		},
		{
			label:              "B: tool results",
			loadedInToolResult: true,
			suffix:             "tool-results",
		},
	}
	for _, m := range modes {
		agt := newBenchAgent(
			repo,
			exec,
			modelName,
			llmagent.SkillLoadModeTurn,
			m.loadedInToolResult,
		)

		svc := inmemory.NewSessionService()
		r := runner.NewRunner(
			defaultAppName,
			agt,
			runner.WithSessionService(svc),
		)

		sessionID := agentSessionPrefix + "prompt-cache-" + m.suffix
		cap, dur, err := runInternalCommsSelectDocsOnce(
			r,
			repo,
			sessionID,
			debug,
			progress,
		)
		r.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", m.label, err)
		}

		results = append(results, promptCacheResult{
			Label:    m.label,
			Duration: dur,
			Usage:    cap.Usage,
		})
		printPromptCacheModeSummary(
			m.label,
			dur,
			cap.Usage,
			progress,
		)
	}

	if len(results) == 2 {
		printPromptCacheComparison(
			results[0],
			results[1],
			progress,
		)
	}
	return nil
}

func runInternalCommsSelectDocsOnce(
	r runner.Runner,
	repo skillrepo.Repository,
	sessionID string,
	debug bool,
	progress bool,
) (runCapture, time.Duration, error) {
	wantLines, err := expected3PFormatLines(repo)
	if err != nil {
		return runCapture{}, 0, err
	}

	msg := model.NewUserMessage(internalCommsPrompt())
	start := time.Now()
	cap, err := runOnce(r, defaultUserID, sessionID, msg, debug, progress)
	dur := time.Since(start).Truncate(time.Millisecond)
	if err != nil {
		return cap, dur, err
	}

	if !hasSkillToolCall(
		cap.ToolCalls,
		toolSkillLoad,
		skillInternalComms,
	) {
		return cap, dur, fmt.Errorf(
			"internal-comms: missing skill_load%s",
			debugSuffix(cap, debug),
		)
	}
	if !hasSelectDocsCall(
		cap.ToolCalls,
		skillInternalComms,
		"examples/3p-updates.md",
	) {
		return cap, dur, fmt.Errorf(
			"internal-comms: missing skill_select_docs%s",
			debugSuffix(cap, debug),
		)
	}
	if !hasSkillToolCall(
		cap.ToolCalls,
		toolSkillRun,
		skillInternalComms,
	) {
		return cap, dur, fmt.Errorf(
			"internal-comms: missing skill_run%s",
			debugSuffix(cap, debug),
		)
	}

	gotLines, ok := formatLinesFromLastSkillRun(cap.ToolResults)
	if !ok {
		return cap, dur, fmt.Errorf(
			"internal-comms: missing skill_run stdout%s",
			debugSuffix(cap, debug),
		)
	}
	if len(gotLines) != len(wantLines) {
		return cap, dur, fmt.Errorf(
			"internal-comms: wrong line count%s",
			debugSuffix(cap, debug),
		)
	}
	for i := range wantLines {
		if strings.TrimSpace(gotLines[i]) != wantLines[i] {
			return cap, dur, fmt.Errorf(
				"internal-comms: line %d mismatch%s",
				i,
				debugSuffix(cap, debug),
			)
		}
	}
	return cap, dur, nil
}

func printPromptCacheModeSummary(
	label string,
	dur time.Duration,
	usage usageTotals,
	progress bool,
) {
	progressf(progress, "  %s", label)
	progressf(
		progress,
		"    Steps: %d | Duration: %s",
		usage.Steps,
		dur,
	)
	progressf(
		progress,
		"    Prompt: %d | Completion: %d | Total: %d",
		usage.PromptTokens,
		usage.CompletionTokens,
		usage.TotalTokens,
	)

	cacheHit := usage.CachedTokens + usage.CacheReadTokens
	if cacheHit > 0 {
		progressf(
			progress,
			"    Cache hit: %d (cached=%d read=%d create=%d)",
			cacheHit,
			usage.CachedTokens,
			usage.CacheReadTokens,
			usage.CacheCreateTokens,
		)
		return
	}
	if usage.CacheCreateTokens > 0 {
		progressf(
			progress,
			"    Cache create: %d",
			usage.CacheCreateTokens,
		)
	}
}

func printPromptCacheComparison(
	a promptCacheResult,
	b promptCacheResult,
	progress bool,
) {
	aHit := a.Usage.CachedTokens + a.Usage.CacheReadTokens
	bHit := b.Usage.CachedTokens + b.Usage.CacheReadTokens

	progressf(progress, "  📈 Cache Delta (B - A)")
	progressf(
		progress,
		"    Cached prompt tokens: %d -> %d (Δ %d)",
		aHit,
		bHit,
		bHit-aHit,
	)
	progressf(
		progress,
		"    Prompt tokens: %d -> %d (Δ %d)",
		a.Usage.PromptTokens,
		b.Usage.PromptTokens,
		b.Usage.PromptTokens-a.Usage.PromptTokens,
	)
	progressf(
		progress,
		"    Duration: %s -> %s",
		a.Duration,
		b.Duration,
	)
}
