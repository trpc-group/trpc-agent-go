//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	agentArtifactStageMarker = "AGENT_ARTIFACT_STAGE_OK"
	agentArtifactSaveMarker  = "AGENT_ARTIFACT_SAVE_OK"
	agentArtifactPinMarker   = "AGENT_ARTIFACT_PIN_OK"
)

type artifactStageInput struct {
	From string `json:"from" jsonschema:"description=Artifact input ref, for example artifact://inputs/numbers.txt or artifact://inputs/numbers.txt@0."`
	To   string `json:"to" jsonschema:"description=Workspace-relative destination path, for example work/inputs/numbers.txt."`
	Pin  bool   `json:"pin,omitempty" jsonschema:"description=When true, reuse the previously resolved artifact version for this destination in the same session."`
}

type artifactStageOutput struct {
	OK        bool   `json:"ok"`
	From      string `json:"from"`
	To        string `json:"to"`
	SizeBytes int64  `json:"size_bytes"`
	Content   string `json:"content,omitempty"`
}

func runAgentArtifactStage(ctx context.Context, cfg config) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; source ./glm.sh from the repo root to run the real artifact scenario.")
		return errSkip
	}
	sessionID := "agent-artifact-stage"
	svc := inmemory.NewService()
	exec := sandbox.New(commonOptions(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 10*time.Second)...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return err
	}
	if _, err := saveExampleArtifact(
		ctx,
		svc,
		sessionID,
		"inputs/numbers.txt",
		[]byte("3\n4\n8\n"),
		"text/plain",
	); err != nil {
		return err
	}
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		sandbox.WorkspaceWriteProfile(),
		nil,
		withAgentToolArtifactService(svc),
		withAgentToolExtraTools([]tool.Tool{newArtifactStageInputTool(exec)}),
		withAgentToolInstructionTail(
			"Use artifact_stage_input when the user asks to stage an artifact:// input into the sandbox workspace.",
		),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()

	final, err := h.runTurn(ctx, sessionID, `Stage artifact://inputs/numbers.txt into work/inputs/numbers.txt with artifact_stage_input.
Then use workspace_exec to read work/inputs/numbers.txt and compute the sum.
After the tool results, answer concisely with AGENT_ARTIFACT_STAGE_OK and sum=15.`)
	if err != nil {
		return err
	}
	if err := h.requireToolCalls("artifact_stage_input", 1); err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	for _, want := range []string{agentArtifactStageMarker, "sum=15"} {
		if err := expectContains(final, want); err != nil {
			return err
		}
	}
	fmt.Println(redact(final))
	return nil
}

func runAgentArtifactSave(ctx context.Context, cfg config) error {
	sessionID := "agent-artifact-save"
	svc := inmemory.NewService()
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		sandbox.WorkspaceWriteProfile(),
		nil,
		withAgentToolArtifactService(svc),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()

	final, err := h.runTurn(ctx, sessionID, `Use workspace_exec to create out/report.txt with this exact content:
AGENT_ARTIFACT_SAVE_OK total=42

Then call workspace_save_artifact with path "out/report.txt".
After the save tool result, answer concisely with AGENT_ARTIFACT_SAVE_OK and the artifact:// reference.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := h.requireToolCalls("workspace_save_artifact", 1); err != nil {
		return err
	}
	if err := expectContains(final, agentArtifactSaveMarker); err != nil {
		return err
	}
	if err := expectContains(final, "artifact://out/report.txt@"); err != nil {
		return err
	}
	data, err := loadExampleArtifact(ctx, svc, sessionID, "out/report.txt", nil)
	if err != nil {
		return err
	}
	if err := expectContains(string(data), agentArtifactSaveMarker); err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func runAgentArtifactPin(ctx context.Context, cfg config) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; source ./glm.sh from the repo root to run the real artifact scenario.")
		return errSkip
	}
	sessionID := "agent-artifact-pin"
	svc := inmemory.NewService()
	exec := sandbox.New(commonOptions(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 10*time.Second)...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return err
	}
	if _, err := saveExampleArtifact(
		ctx,
		svc,
		sessionID,
		"inputs/pinned.txt",
		[]byte(agentArtifactPinMarker+" pinned=first\n"),
		"text/plain",
	); err != nil {
		return err
	}
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		sandbox.WorkspaceWriteProfile(),
		nil,
		withAgentToolArtifactService(svc),
		withAgentToolExtraTools([]tool.Tool{newArtifactStageInputTool(exec)}),
		withAgentToolInstructionTail(
			"Use artifact_stage_input with pin=true when the user asks to pin an artifact input version.",
		),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()

	first, err := h.runTurn(ctx, sessionID, fmt.Sprintf(`Call artifact_stage_input with from="artifact://inputs/pinned.txt", to="work/inputs/pinned.txt", and pin=true.
Then use workspace_exec to read work/inputs/pinned.txt.
Answer with %s and the pinned value you read.`, agentArtifactPinMarker))
	if err != nil {
		return err
	}
	if err := expectContains(first, agentArtifactPinMarker); err != nil {
		return err
	}
	if err := expectContains(first, "pinned=first"); err != nil {
		return err
	}
	if _, err := saveExampleArtifact(
		ctx,
		svc,
		sessionID,
		"inputs/pinned.txt",
		[]byte(agentArtifactPinMarker+" pinned=second\n"),
		"text/plain",
	); err != nil {
		return err
	}
	second, err := h.runTurn(ctx, sessionID, fmt.Sprintf(`A newer artifact version now exists, but this is the same session and destination.
Call artifact_stage_input again with from="artifact://inputs/pinned.txt", to="work/inputs/pinned.txt", and pin=true.
Then use workspace_exec to read work/inputs/pinned.txt.
Answer only with %s and the pinned value you read.`, agentArtifactPinMarker))
	if err != nil {
		return err
	}
	if err := h.requireToolCalls("artifact_stage_input", 2); err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(2); err != nil {
		return err
	}
	if err := expectContains(second, agentArtifactPinMarker); err != nil {
		return err
	}
	if err := expectContains(second, "pinned=first"); err != nil {
		return err
	}
	if strings.Contains(second, "pinned=second") {
		return errors.New("pinned artifact input unexpectedly used the newer version")
	}
	fmt.Println(redact(second))
	return nil
}

func newArtifactStageInputTool(exec *sandbox.CodeExecutor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in artifactStageInput) (artifactStageOutput, error) {
			if exec == nil {
				return artifactStageOutput{}, errors.New("sandbox executor is not configured")
			}
			if strings.TrimSpace(in.From) == "" {
				return artifactStageOutput{}, errors.New("from is required")
			}
			if strings.TrimSpace(in.To) == "" {
				return artifactStageOutput{}, errors.New("to is required")
			}
			ctxIO, ws, err := artifactToolWorkspace(ctx, exec)
			if err != nil {
				return artifactStageOutput{}, err
			}
			if err := exec.Runtime().StageInputs(ctxIO, ws, []codeexecutor.InputSpec{{
				From: strings.TrimSpace(in.From),
				To:   strings.TrimSpace(in.To),
				Pin:  in.Pin,
				Mode: "copy",
			}}); err != nil {
				return artifactStageOutput{}, err
			}
			files, err := exec.Runtime().Collect(ctxIO, ws, []string{strings.TrimSpace(in.To)})
			if err != nil {
				return artifactStageOutput{}, err
			}
			out := artifactStageOutput{
				OK:   true,
				From: strings.TrimSpace(in.From),
				To:   strings.TrimSpace(in.To),
			}
			if len(files) > 0 {
				out.SizeBytes = files[0].SizeBytes
				out.Content = files[0].Content
			}
			return out, nil
		},
		function.WithName("artifact_stage_input"),
		function.WithDescription(
			"Stage an artifact:// input into the current sandbox workspace so workspace_exec can read it.",
		),
	)
}

func artifactToolWorkspace(
	ctx context.Context,
	exec *sandbox.CodeExecutor,
) (context.Context, codeexecutor.Workspace, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return ctx, codeexecutor.Workspace{}, errors.New("agent invocation session is required")
	}
	ctxIO := ctx
	if inv.ArtifactService != nil {
		ctxIO = codeexecutor.WithArtifactService(ctxIO, inv.ArtifactService)
	}
	ctxIO = codeexecutor.WithArtifactSession(ctxIO, artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	})
	key := inv.Session.ID
	if inv.Session.AppName != "" && inv.Session.UserID != "" && inv.Session.ID != "" {
		key = inv.Session.AppName + "/" + inv.Session.UserID + "/" + inv.Session.ID
	}
	ws, err := exec.Runtime().CreateWorkspace(ctxIO, key, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return ctxIO, codeexecutor.Workspace{}, err
	}
	return ctxIO, ws, nil
}

func saveExampleArtifact(
	ctx context.Context,
	svc artifact.Service,
	sessionID string,
	name string,
	data []byte,
	mime string,
) (int, error) {
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, exampleArtifactSession(sessionID))
	return codeexecutor.SaveArtifactHelper(ctx, name, data, mime)
}

func loadExampleArtifact(
	ctx context.Context,
	svc artifact.Service,
	sessionID string,
	name string,
	version *int,
) ([]byte, error) {
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, exampleArtifactSession(sessionID))
	data, _, _, err := codeexecutor.LoadArtifactHelper(ctx, name, version)
	return data, err
}

func exampleArtifactSession(sessionID string) artifact.SessionInfo {
	return artifact.SessionInfo{
		AppName:   "sandbox_agent_tool_example",
		UserID:    agentToolUserID,
		SessionID: sessionID,
	}
}
