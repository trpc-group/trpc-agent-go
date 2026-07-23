//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package assist wires an optional LLM / fake-model Skills agent for review.
package assist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Config configures one agent-assisted review pass.
type Config struct {
	SkillsRoot  string
	Executor    codeexecutor.CodeExecutor
	Model       model.Model
	Policy      tool.PermissionPolicy
	Prompt      string
	Timeout     time.Duration
	DiffSummary string
	DiffDigest  string
	DiffPath    string
}

// Result summarizes the agent assist pass.
type Result struct {
	ToolCalls int
	Events    int
	FinalText string
	ModelName string
	// Warning is a non-fatal assist issue (e.g. real model made no tool calls).
	Warning string
}

// Run executes a short Skills-enabled agent session (fake or real model).
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Model == nil {
		cfg.Model = NewFakeModel(FakeModelOptions{
			DiffSummary: cfg.DiffSummary,
			DiffDigest:  cfg.DiffDigest,
			DiffPath:    cfg.DiffPath,
		})
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	repo, err := skill.NewFSRepository(cfg.SkillsRoot)
	if err != nil {
		return nil, fmt.Errorf("skills repo: %w", err)
	}
	if cfg.Executor == nil {
		return nil, fmt.Errorf("code executor is required for agent assist")
	}

	gen := model.GenerationConfig{Stream: false, MaxTokens: intPtr(2048)}
	llm := llmagent.New(
		"code-review-agent",
		llmagent.WithModel(cfg.Model),
		llmagent.WithDescription("Go code review agent using the code-review skill"),
		llmagent.WithInstruction(`You are a Go code review agent.
Load the code-review skill with skill_load, then run checks via workspace_exec
using REVIEW_DIFF_PATH and skill scripts. Prefer safe, allowlisted commands.
Summarize findings briefly. Do not print secrets.
Host rule-engine findings are authoritative; your role is orchestration assist.`),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(cfg.Executor),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithMaxToolIterations(6),
		llmagent.WithMaxLLMCalls(8),
	)

	r := runner.NewRunner(
		"code-review-agent-app",
		llm,
		runner.WithArtifactService(inmemory.NewService()),
	)
	defer func() { _ = r.Close() }()

	userID := "reviewer"
	sessionID := "assist-" + uuid.NewString()
	prompt := cfg.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = fmt.Sprintf(
			"Review diff digest=%s summary=%s. Load code-review skill and run scripts/run_checks.sh.",
			cfg.DiffDigest, cfg.DiffSummary,
		)
	}

	var runOpts []agent.RunOption
	if cfg.Policy != nil {
		runOpts = append(runOpts, agent.WithToolPermissionPolicy(cfg.Policy))
	}

	ch, err := r.Run(runCtx, userID, sessionID, model.NewUserMessage(prompt), runOpts...)
	if err != nil {
		return nil, err
	}

	out := &Result{ModelName: cfg.Model.Info().Name}
	var final strings.Builder
	var errNotes []string
	for ev := range ch {
		if ev == nil {
			continue
		}
		out.Events++
		if ev.Error != nil && ev.Error.Message != "" {
			errNotes = append(errNotes, ev.Error.Message)
		}
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			msg := ev.Response.Choices[0].Message
			if len(msg.ToolCalls) > 0 {
				out.ToolCalls += len(msg.ToolCalls)
			}
			if msg.Content != "" && (ev.Author == "code-review-agent" || ev.Response.Done) {
				final.WriteString(msg.Content)
			}
		}
	}
	out.FinalText = strings.TrimSpace(final.String())
	if len(errNotes) > 0 {
		out.Warning = "llm_errors: " + strings.Join(uniqStrings(errNotes), "; ")
	} else if out.ModelName != "fake-code-review" && out.ToolCalls == 0 {
		out.Warning = "real model returned no tool calls; check OPENAI_BASE_URL / --model-variant (qwen→qwen, not qwen-flash)"
	}
	return out, nil
}

// uniqStrings returns unique strings preserving first-seen order.
func uniqStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// intPtr returns a pointer to v.
func intPtr(v int) *int { return &v }

// FakeModelOptions configures the scripted review model.
type FakeModelOptions struct {
	DiffSummary string
	DiffDigest  string
	DiffPath    string
}

// FakeModel is a deterministic model that drives skill_load then workspace_exec.
type FakeModel struct {
	turn        atomic.Int32
	diffSummary string
	diffDigest  string
	diffPath    string
}

// NewFakeModel returns a scripted review model (no API key).
func NewFakeModel(opts ...FakeModelOptions) *FakeModel {
	m := &FakeModel{}
	if len(opts) > 0 {
		m.diffSummary = opts[0].DiffSummary
		m.diffDigest = opts[0].DiffDigest
		m.diffPath = opts[0].DiffPath
	}
	return m
}

// Info implements model.Model.
func (m *FakeModel) Info() model.Info {
	return model.Info{Name: "fake-code-review"}
}

// GenerateContent implements model.Model.
func (m *FakeModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	n := m.turn.Add(1)
	ch := make(chan *model.Response, 1)

	switch {
	case n == 1:
		ch <- toolCallResponse("call-load", "skill_load", mustJSON(map[string]any{
			"skill": "code-review",
		}))
	case n == 2:
		// Keep the command allowlisted and path-agnostic; digest/summary are in the final reply.
		cmd := `bash -lc 'if [ -f skills/code-review/scripts/run_checks.sh ]; then bash skills/code-review/scripts/run_checks.sh; else echo []; fi'`
		ch <- toolCallResponse("call-exec", "workspace_exec", mustJSON(map[string]any{
			"command": cmd,
		}))
	default:
		summary := m.diffSummary
		if summary == "" {
			summary = "unknown"
		}
		digest := m.diffDigest
		if digest == "" {
			digest = "n/a"
		}
		pathNote := "diff_path=unset"
		if m.diffPath != "" {
			pathNote = "diff_path=set"
		}
		ch <- &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					Content: fmt.Sprintf(
						"Fake model assist complete for %s (digest=%s, %s). Loaded code-review skill and invoked workspace_exec. Host rule engine remains authoritative.",
						summary, digest, pathNote,
					),
				},
			}},
		}
	}
	close(ch)
	_ = req
	return ch, nil
}

// toolCallResponse builds a model response that contains one tool call.
func toolCallResponse(id, name string, args []byte) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: args,
					},
				}},
			},
		}},
	}
}

// mustJSON marshals v to JSON, returning "{}" on error.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
