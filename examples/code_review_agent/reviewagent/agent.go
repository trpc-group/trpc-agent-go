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

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
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
// results instead of aborting the review task. Every field that reaches
// the prompt is redacted here, so no caller can bypass the guarantee
// that secrets never leave the process toward a remote model.
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
	content, err := invoke(ctx, cfg, mdl, BuildPrompt(redactFiles(files)))
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

// redactFiles deep-copies the changed files and redacts every field
// rendered into the prompt: paths, package names, and line contents.
func redactFiles(files []review.ChangedFile) []review.ChangedFile {
	out := make([]review.ChangedFile, len(files))
	copy(out, files)
	for i := range out {
		out[i].OldPath = redaction.RedactText(out[i].OldPath)
		out[i].NewPath = redaction.RedactText(out[i].NewPath)
		out[i].PackageName = redaction.RedactText(out[i].PackageName)
		out[i].Hunks = make([]review.Hunk, len(files[i].Hunks))
		copy(out[i].Hunks, files[i].Hunks)
		for j := range out[i].Hunks {
			out[i].Hunks[j].Lines = make([]review.DiffLine, len(files[i].Hunks[j].Lines))
			copy(out[i].Hunks[j].Lines, files[i].Hunks[j].Lines)
			for k := range out[i].Hunks[j].Lines {
				out[i].Hunks[j].Lines[k].Content = redaction.RedactText(out[i].Hunks[j].Lines[k].Content)
			}
		}
	}
	return out
}

// BuildPrompt renders changed files into the diff prompt sent to the model.
// The result never exceeds promptByteCap bytes: the remaining budget is
// checked before each chunk is appended, so one oversized diff line in an
// untrusted input cannot inflate the prompt past the cap.
func BuildPrompt(files []review.ChangedFile) string {
	const truncationMarker = "[diff truncated]\n"
	var b strings.Builder
	b.WriteString("Review this Go diff and answer with the JSON contract only.\n\n")
	// Reserve room for the marker so the cap holds even when truncating.
	budget := promptByteCap - b.Len() - len(truncationMarker)
	truncated := false
fileLoop:
	for _, file := range files {
		header := "FILE: " + file.NewPath
		if file.PackageName != "" {
			header += " (package " + file.PackageName + ")"
		}
		if !appendWithinBudget(&b, header+"\n", &budget) {
			truncated = true
			break fileLoop
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				var text string
				switch line.Kind {
				case "added":
					text = fmt.Sprintf("+ %d: %s\n", line.NewLine, line.Content)
				case "removed":
					text = fmt.Sprintf("- %d: %s\n", line.OldLine, line.Content)
				default:
					text = fmt.Sprintf("  %d: %s\n", line.NewLine, line.Content)
				}
				if !appendWithinBudget(&b, text, &budget) {
					truncated = true
					break fileLoop
				}
			}
		}
		if !appendWithinBudget(&b, "\n", &budget) {
			truncated = true
			break fileLoop
		}
	}
	if truncated {
		b.WriteString(truncationMarker)
	}
	return b.String()
}

// appendWithinBudget writes text to b, clipping at the remaining budget.
// It returns false once the budget is exhausted.
func appendWithinBudget(b *strings.Builder, text string, budget *int) bool {
	if len(text) <= *budget {
		b.WriteString(text)
		*budget -= len(text)
		return true
	}
	b.WriteString(text[:*budget])
	*budget = 0
	return false
}

// intPtr returns a pointer to i.
func intPtr(i int) *int { return &i }

// floatPtr returns a pointer to f.
func floatPtr(f float64) *float64 { return &f }
