//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package llmreview runs the Agent + Skill LLM review path.
package llmreview

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	appName   = "skills-code-review-agent"
	agentName = "code-review-agent"
	userID    = "review-user"
)

// Options configures an LLM-backed review run.
type Options struct {
	TaskID       string
	DiffRaw      string
	InputSummary string
	SkillsRoot   string
	Runtime      sandbox.Runtime
	Model        string
	RuleFindings []findings.Finding
	Timeout      time.Duration
}

// Run executes the LLM agent review and returns supplemental findings.
func Run(ctx context.Context, opts Options) ([]findings.Finding, error) {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required when --dry-run=false")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.SkillsRoot == "" {
		opts.SkillsRoot = "skills"
	}
	opts.SkillsRoot = sandbox.ResolveSkillsRoot(opts.SkillsRoot)
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = "gpt-4o-mini"
	}

	repo, err := skill.NewFSRepository(opts.SkillsRoot)
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}

	codeExec, err := sandbox.NewCodeExecutor(sandbox.Options{
		TaskID:     opts.TaskID,
		SkillsRoot: opts.SkillsRoot,
		Runtime:    opts.Runtime,
		Timeout:    opts.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create code executor: %w", err)
	}

	maxTokens := 2048
	temperature := 0.1
	agt := llmagent.New(
		agentName,
		llmagent.WithModel(openai.New(opts.Model)),
		llmagent.WithSkills(repo),
		llmagent.WithSkillToolProfile(llmagent.SkillToolProfileFull),
		llmagent.WithCodeExecutor(codeExec),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
		llmagent.WithInstruction(reviewInstruction()),
		llmagent.WithDescription("Go code review agent using code-review skill."),
	)

	r := runner.NewRunner(
		appName,
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	sessionID := fmt.Sprintf("review-%s", opts.TaskID)
	// 原始llm輸出
	events, err := r.Run(runCtx, userID, sessionID, model.NewUserMessage(buildUserPrompt(opts)))
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}

	content := collectAssistantText(events)

	// 結構化後的llm評論
	items, err := ParseFindings(content)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func reviewInstruction() string {
	return strings.TrimSpace(`
You are a Go code review agent.

Workflow:
1. Load the "code-review" skill with skill_load when you need rule references.
2. Analyze the unified diff and any rule-engine findings provided by the user.
3. Optionally run skill_run for: bash scripts/run_checks.sh work/inputs/changes.diff
4. Return ONLY a JSON array of additional findings not already covered by rule findings.

Each finding object must include:
severity, category, file, line, title, evidence, recommendation, confidence, rule_id, source.

Use source="llm". Severity: critical|high|medium|low. Confidence: 0.0-1.0.
If there are no additional issues, return [].
Do not wrap the JSON in markdown fences.
`)
}

func buildUserPrompt(opts Options) string {
	var b strings.Builder
	b.WriteString("Review this Go change set.\n\n")
	if strings.TrimSpace(opts.InputSummary) != "" {
		b.WriteString("Input summary:\n")
		b.WriteString(opts.InputSummary)
		b.WriteString("\n\n")
	}
	if len(opts.RuleFindings) > 0 {
		b.WriteString("Rule-engine findings already detected:\n")
		for _, f := range opts.RuleFindings {
			fmt.Fprintf(&b, "- [%s] %s:%d %s (%s)\n",
				f.Severity, f.File, f.Line, f.Title, f.RuleID)
		}
		b.WriteString("\n")
	}
	b.WriteString("Unified diff:\n```diff\n")
	b.WriteString(opts.DiffRaw)
	b.WriteString("\n```\n")
	return b.String()
}

// 收集llm輸出
func collectAssistantText(events <-chan *event.Event) string {
	var last string
	for evt := range events {
		if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		ch := evt.Response.Choices[0]
		if ch.Message.Role == model.RoleAssistant && strings.TrimSpace(ch.Message.Content) != "" {
			last = ch.Message.Content
		}
	}
	return last
}
