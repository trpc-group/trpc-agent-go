//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/examples/guardrail/approval/internal/hostcontext"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review/systemone"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

type application struct {
	config    config
	runner    runner.Runner
	tools     tool.ToolSet
	userID    string
	sessionID string
	policy    tool.PermissionPolicy
}

func newApp(cfg config, mainModel model.Model) (*application, error) {
	baseDir, err := filepath.Abs(cfg.baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base directory: %w", err)
	}
	cfg.baseDir = baseDir
	reviewer, err := systemone.New(cfg.reviewBaseURL,
		systemone.WithAPIKey(cfg.reviewAPIKey),
		systemone.WithModel(cfg.reviewModel),
		systemone.WithTimeout(cfg.reviewTimeout),
		systemone.WithRiskThreshold(cfg.riskThreshold),
		systemone.WithMinConfidence(cfg.minConfidence),
	)
	if err != nil {
		return nil, fmt.Errorf("create system one reviewer: %w", err)
	}
	approvalPlugin, err := approval.New(approval.WithReviewer(reviewer), approval.WithContextProvider(hostcontext.Provider(cfg.baseDir)))
	if err != nil {
		return nil, fmt.Errorf("create approval plugin: %w", err)
	}
	tools, err := hostexec.NewToolSet(hostexec.WithBaseDir(cfg.baseDir))
	if err != nil {
		return nil, fmt.Errorf("create host tools: %w", err)
	}
	maxTokens := 4096
	mainAgent := llmagent.New("systemone-host-assistant",
		llmagent.WithModel(mainModel),
		llmagent.WithInstruction(fmt.Sprintf(`Help the user inspect their workspace and perform requested shell tasks.
The default working directory is %q. Include an explicit absolute workdir in each exec_command call so the reviewer can see the target directory.
Keep commands narrow and within the user's requested scope. All host tools require approval review.
If a tool reports a denial or review failure, explain it to the user; do not retry via an alternative command to bypass it.
Use tool results to report what actually happened. Do not claim a denied command was executed.`, cfg.baseDir)),
		llmagent.WithGenerationConfig(model.GenerationConfig{MaxTokens: &maxTokens, Stream: false}),
		llmagent.WithMaxLLMCalls(8),
		llmagent.WithToolSets([]tool.ToolSet{tools}),
		llmagent.WithToolCallbacks(hostcontext.Callbacks(cfg.baseDir)),
	)
	return &application{
		config: cfg,
		// The runner owns its default in-memory session service. Reusing these
		// IDs across turns preserves the history consumed by the reviewer.
		runner: runner.NewRunner("systemone-approval-demo", mainAgent),
		policy: approvalPlugin.PermissionPolicy(nil),
		tools:  tools, userID: "demo-user", sessionID: uuid.NewString(),
	}, nil
}

func (a *application) close() error {
	// Stop runs before closing the host tools and their process sessions.
	return errors.Join(a.runner.Close(), a.tools.Close())
}

func (a *application) runTurn(ctx context.Context, prompt string, output io.Writer) error {
	events, err := a.runner.Run(ctx, a.userID, a.sessionID,
		model.NewUserMessage(prompt), agent.WithRequestID(uuid.NewString()), agent.WithToolPermissionPolicy(a.policy))
	if err != nil {
		return err
	}
	// Drain the stream, including on model errors, so the runner can complete
	// persistence and cleanup before the next turn or application shutdown.
	var runErr error
	for evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Error != nil {
			runErr = fmt.Errorf("agent run: %s", evt.Error.Message)
			continue
		}
		if evt.IsRunnerCompletion() || evt.IsPartial {
			continue
		}
		for _, choice := range evt.Choices {
			msg := choice.Message
			for _, call := range msg.ToolCalls {
				fmt.Fprintf(output, "Tool call: %s\nArguments: %s\n", call.Function.Name, call.Function.Arguments)
			}
			switch msg.Role {
			case model.RoleTool:
				fmt.Fprintf(output, "Tool result (%s): %s\n", msg.ToolName, msg.Content)
			case model.RoleAssistant:
				if msg.Content != "" {
					fmt.Fprintf(output, "Assistant: %s\n", msg.Content)
				}
			}
		}
	}
	return errors.Join(runErr, ctx.Err())
}
