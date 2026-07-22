//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package reviewagent runs the model-assisted review through the real
// LLMAgent/Runner orchestration. It supports a deterministic fake model for
// key-less testing and an OpenAI-compatible model for production use.
package reviewagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

const (
	// ModeFakeModel runs the agent chain with a deterministic offline model.
	ModeFakeModel = "fake-model"
	// ModeLLM runs the agent chain with a real OpenAI-compatible model.
	ModeLLM = "llm"

	agentName     = "code-review-agent"
	appName       = "code-review"
	promptByteCap = 30000
	maxTokens     = 2000
	temperature   = 0.1
)

// Config controls one model-assisted review pass.
type Config struct {
	Mode      string
	ModelName string
	TaskID    string
	Timeout   time.Duration
}

// Output is the result of one model-assisted review pass.
type Output struct {
	Findings   []review.Finding
	Summary    string
	ModelCalls int
	DurationMS int64
}

const instruction = `You are a strict Go code reviewer. You receive a unified
diff of Go changes. Report only real problems introduced on added lines
(security risks, goroutine/context leaks, unclosed resources, error handling,
database transaction lifecycle, missing tests, leaked secrets). Respond with
one JSON object and nothing else:
{"summary":"...","findings":[{"severity":"critical|high|medium|low",
"category":"...","file":"...","line":1,"title":"...","evidence":"...",
"recommendation":"...","confidence":0.0,"rule_id":"LLM-..."}]}
Use an empty findings array when the diff looks clean. Confidence must be
between 0 and 1 and reflect how certain you are.`

// Review runs the review agent and returns validated model findings.
// Failures are returned as errors so the caller can degrade to rule-only
// results instead of aborting the review task.
func Review(ctx context.Context, cfg Config, files []review.ChangedFile) (Output, error) {
	mdl, err := buildModel(cfg)
	if err != nil {
		return Output{}, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	start := time.Now()
	out := Output{ModelCalls: 1}
	content, err := invoke(ctx, cfg, mdl, BuildPrompt(files))
	out.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		return out, err
	}
	parsed, err := ParseModelReview(content, files, cfg.Mode)
	if err != nil {
		return out, err
	}
	out.Findings = parsed.Findings
	out.Summary = parsed.Summary
	return out, nil
}

// buildModel selects the model implementation for the configured mode.
func buildModel(cfg Config) (model.Model, error) {
	switch cfg.Mode {
	case ModeFakeModel:
		return newFakeModel(), nil
	case ModeLLM:
		if os.Getenv("OPENAI_API_KEY") == "" {
			return nil, errors.New("llm mode requires OPENAI_API_KEY; use --mode fake-model for offline runs")
		}
		return openai.New(cfg.ModelName), nil
	default:
		return nil, fmt.Errorf("unsupported review agent mode %q", cfg.Mode)
	}
}

// invoke drives one prompt through LLMAgent + Runner and returns the final
// assistant message content.
func invoke(ctx context.Context, cfg Config, mdl model.Model, prompt string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      false,
	}
	reviewAgent := llmagent.New(
		agentName,
		llmagent.WithModel(mdl),
		llmagent.WithDescription("Reviews Go diffs and reports structured findings as JSON."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
	)
	r := runner.NewRunner(
		appName,
		reviewAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := "review-" + cfg.TaskID
	eventCh, err := r.Run(runCtx, "reviewer", sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return "", fmt.Errorf("run review agent: %w", err)
	}
	return collectFinalContent(eventCh)
}

// collectFinalContent drains runner events and returns the final response text.
func collectFinalContent(eventCh <-chan *event.Event) (string, error) {
	var content string
	for evt := range eventCh {
		if evt.Error != nil {
			return "", fmt.Errorf("review agent error: %s", evt.Error.Message)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
			content = choice.Message.Content
		} else if choice.Delta.Content != "" {
			content += choice.Delta.Content
		}
	}
	if strings.TrimSpace(content) == "" {
		return "", errors.New("review agent returned no content")
	}
	return content, nil
}

// BuildPrompt renders changed files into the diff prompt sent to the model.
// Callers must pass redacted files so secrets never reach a remote model.
func BuildPrompt(files []review.ChangedFile) string {
	var b strings.Builder
	b.WriteString("Review this Go diff and answer with the JSON contract only.\n\n")
fileLoop:
	for _, file := range files {
		fmt.Fprintf(&b, "FILE: %s", file.NewPath)
		if file.PackageName != "" {
			fmt.Fprintf(&b, " (package %s)", file.PackageName)
		}
		b.WriteByte('\n')
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case "added":
					fmt.Fprintf(&b, "+ %d: %s\n", line.NewLine, line.Content)
				case "removed":
					fmt.Fprintf(&b, "- %d: %s\n", line.OldLine, line.Content)
				default:
					fmt.Fprintf(&b, "  %d: %s\n", line.NewLine, line.Content)
				}
				// The cap must hold per line: one oversized hunk in an
				// untrusted diff must not produce an unbounded prompt.
				if b.Len() > promptByteCap {
					b.WriteString("[diff truncated]\n")
					break fileLoop
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// intPtr returns a pointer to i.
func intPtr(i int) *int { return &i }

// floatPtr returns a pointer to f.
func floatPtr(f float64) *float64 { return &f }
