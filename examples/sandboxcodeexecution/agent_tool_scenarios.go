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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	agentToolUserID             = "sandbox-agent-tool-user"
	agentToolBasicMarker        = "AGENT_TOOL_BASIC_OK"
	agentToolCreatedMarker      = "AGENT_TOOL_PERSISTENCE_CREATED"
	agentToolPersistenceMarker  = "AGENT_TOOL_PERSISTENCE_OK"
	agentToolSecurityMarker     = "AGENT_TOOL_SECURITY_OK"
	agentToolSecretLeakSentinel = "AGENT_SECRET_SHOULD_NOT_APPEAR"
)

type agentToolHarness struct {
	runner            runner.Runner
	workspaceExecSeen atomic.Int64
	toolInputMu       sync.Mutex
	toolInputs        []toolInputRecord
	toolOutputMu      sync.Mutex
	toolOutputs       []string
}

type toolInputRecord struct {
	Name     string
	Argument string
	CallID   string
}

type agentToolHarnessConfig struct {
	artifactService artifact.Service
	extraTools      []tool.Tool
	instructionTail string
	sandboxOptions  []sandbox.Option
}

type agentToolHarnessOption func(*agentToolHarnessConfig)

func withAgentToolArtifactService(svc artifact.Service) agentToolHarnessOption {
	return func(cfg *agentToolHarnessConfig) {
		cfg.artifactService = svc
	}
}

func withAgentToolExtraTools(extra []tool.Tool) agentToolHarnessOption {
	return func(cfg *agentToolHarnessConfig) {
		cfg.extraTools = append(cfg.extraTools, extra...)
	}
}

func withAgentToolInstructionTail(tail string) agentToolHarnessOption {
	return func(cfg *agentToolHarnessConfig) {
		cfg.instructionTail = tail
	}
}

func withAgentToolSandboxOptions(opts ...sandbox.Option) agentToolHarnessOption {
	return func(cfg *agentToolHarnessConfig) {
		cfg.sandboxOptions = append(cfg.sandboxOptions, opts...)
	}
}

func runAgentToolManualRun(ctx context.Context, cfg config) error {
	h, err := newAgentToolHarness(ctx, cfg, sandbox.WorkspaceWriteProfile(), nil)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "agent-tool-basic", `
	Use workspace_exec to print the current working directory and list the sandbox workspace directories.`)
	if err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func runAgentToolBasic(ctx context.Context, cfg config) error {
	h, err := newAgentToolHarness(ctx, cfg, sandbox.WorkspaceWriteProfile(), nil)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "agent-tool-basic", `Use the workspace_exec tool to analyze these values: 5, 12, 8, 15, 7, 9, 11.

Do not calculate mentally. Use workspace_exec to run Python or shell code in the sandbox.
The command you run should compute count, sum, and mean, and should print a line containing AGENT_TOOL_BASIC_OK.
After the tool result, answer concisely and include AGENT_TOOL_BASIC_OK, count=7, sum=67, and mean=9.57.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	for _, want := range []string{agentToolBasicMarker, "count=7", "sum=67"} {
		if err := expectContains(final, want); err != nil {
			return err
		}
	}
	fmt.Println(redact(final))
	return nil
}

func runAgentToolSessionPersistence(ctx context.Context, cfg config) error {
	h, err := newAgentToolHarness(ctx, cfg, sandbox.WorkspaceWriteProfile(), nil)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	sessionID := "agent-tool-session-persistence"
	createFinal, err := h.runTurn(ctx, sessionID, `Use workspace_exec to create a persistent marker file for this session.

Run a command equivalent to:
mkdir -p work && printf AGENT_TOOL_PERSISTENCE_OK > work/agent_marker.txt

After the tool result, answer exactly AGENT_TOOL_PERSISTENCE_CREATED.`)
	if err != nil {
		return err
	}
	if err := expectContains(createFinal, agentToolCreatedMarker); err != nil {
		return err
	}
	readFinal, err := h.runTurn(ctx, sessionID, `This is the same user session as the prior turn.

Use workspace_exec to read work/agent_marker.txt from the sandbox workspace.
After the tool result, answer with the marker content you read.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(2); err != nil {
		return err
	}
	if err := expectContains(readFinal, agentToolPersistenceMarker); err != nil {
		return err
	}
	fmt.Println(redact(readFinal))
	return nil
}

func runAgentToolSecurity(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
	manifest := &sandbox.Manifest{
		Files: []sandbox.ManifestFile{{
			Path:    "work/secret.env",
			Content: []byte("TOKEN=" + agentToolSecretLeakSentinel + "\n"),
			Mode:    0o600,
		}},
	}
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		profile,
		manifest,
		withAgentToolSandboxOptions(sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit: sandbox.ShellEnvironmentPolicyInheritCore,
		})),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "agent-tool-security", `Use workspace_exec to verify sandbox security behavior.

Run a shell command that checks both:
1. OPENAI_API_KEY is empty inside the sandbox process.
2. Reading work/secret.env is denied.

The command should print AGENT_TOOL_SECURITY_OK only if both checks pass.
After the tool result, answer concisely and include AGENT_TOOL_SECURITY_OK. Do not print environment variables.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := expectContains(final, agentToolSecurityMarker); err != nil {
		return err
	}
	if strings.Contains(final, agentToolSecretLeakSentinel) {
		return errors.New("agent final answer leaked denied secret content")
	}
	fmt.Println(redact(final))
	return nil
}

func newAgentToolHarness(
	ctx context.Context,
	cfg config,
	profile sandbox.PermissionProfile,
	manifest *sandbox.Manifest,
	options ...agentToolHarnessOption,
) (*agentToolHarness, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; source ./glm.sh from the repo root to run the real agent+tool scenario.")
		return nil, errSkip
	}
	harnessCfg := agentToolHarnessConfig{}
	for _, opt := range options {
		if opt != nil {
			opt(&harnessCfg)
		}
	}
	opts := commonOptions(cfg, profile, 1<<20, 10*time.Second)
	opts = append(opts, harnessCfg.sandboxOptions...)
	if manifest != nil {
		opts = append(opts, sandbox.WithManifest(*manifest))
	}
	exec := sandbox.New(opts...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return nil, err
	}
	h := &agentToolHarness{}
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if args != nil {
			h.recordToolInput(args)
			if args.ToolName == "workspace_exec" {
				h.workspaceExecSeen.Add(1)
			}
		}
		return &tool.BeforeToolResult{}, nil
	})
	callbacks.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		if args != nil {
			h.recordToolResult(args.Result, args.Error)
		}
		return &tool.AfterToolResult{}, nil
	})
	instruction := agentToolInstruction()
	if strings.TrimSpace(harnessCfg.instructionTail) != "" {
		instruction += "\n\n" + strings.TrimSpace(harnessCfg.instructionTail)
	}
	agent := llmagent.New(
		"sandbox_agent_tool_example",
		llmagent.WithModel(openai.New(cfg.modelName)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1600),
			Temperature: floatPtr(0.0),
		}),
		llmagent.WithInstruction(instruction),
		llmagent.WithCodeExecutor(exec),
		llmagent.WithTools(harnessCfg.extraTools),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithWorkspaceExecSurfaceEnabled(true),
		llmagent.WithMaxToolIterations(4),
		llmagent.WithMaxLLMCalls(8),
		llmagent.WithToolCallbacks(callbacks),
	)
	var runnerOpts []runner.Option
	if harnessCfg.artifactService != nil {
		runnerOpts = append(runnerOpts, runner.WithArtifactService(harnessCfg.artifactService))
	}
	h.runner = runner.NewRunner("sandbox_agent_tool_example", agent, runnerOpts...)
	return h, nil
}

func (h *agentToolHarness) runTurn(
	ctx context.Context,
	sessionID string,
	prompt string,
) (string, error) {
	events, err := h.runner.Run(
		ctx,
		agentToolUserID,
		sessionID,
		model.NewUserMessage(prompt),
	)
	if err != nil {
		return "", err
	}
	var final strings.Builder
	for event := range events {
		if event.Error != nil {
			return "", fmt.Errorf("agent event error: %s", event.Error.Message)
		}
		if event.Response == nil || len(event.Response.Choices) == 0 {
			continue
		}
		choice := event.Response.Choices[0]
		if choice.Message.Role != model.RoleTool && choice.Message.Content != "" {
			final.WriteString(choice.Message.Content)
		}
		if choice.Delta.Role != model.RoleTool && choice.Delta.Content != "" {
			final.WriteString(choice.Delta.Content)
		}
		if event.IsRunnerCompletion() {
			break
		}
	}
	answer := strings.TrimSpace(final.String())
	if answer == "" {
		answer = strings.TrimSpace(h.toolOutputText())
	}
	if answer == "" {
		return "", errors.New("agent produced no final answer or tool output")
	}
	return answer, nil
}

func (h *agentToolHarness) requireWorkspaceExecCalls(min int64) error {
	got := h.workspaceExecSeen.Load()
	if got < min {
		return fmt.Errorf("expected at least %d workspace_exec tool calls, got %d", min, got)
	}
	return nil
}

func (h *agentToolHarness) requireToolCalls(name string, min int) error {
	got := 0
	for _, record := range h.toolInputRecords() {
		if record.Name == name {
			got++
		}
	}
	if got < min {
		return fmt.Errorf("expected at least %d %s tool calls, got %d", min, name, got)
	}
	return nil
}

func (h *agentToolHarness) recordToolInput(args *tool.BeforeToolArgs) {
	if h == nil || args == nil {
		return
	}
	record := toolInputRecord{
		Name:     args.ToolName,
		Argument: redact(string(args.Arguments)),
		CallID:   args.ToolCallID,
	}
	h.toolInputMu.Lock()
	defer h.toolInputMu.Unlock()
	h.toolInputs = append(h.toolInputs, record)
}

func (h *agentToolHarness) recordToolResult(result any, runErr error) {
	var text string
	if runErr != nil {
		text = runErr.Error()
	} else if data, err := json.Marshal(result); err == nil {
		text = string(data)
	} else {
		text = fmt.Sprintf("%+v", result)
	}
	h.toolOutputMu.Lock()
	defer h.toolOutputMu.Unlock()
	h.toolOutputs = append(h.toolOutputs, text)
}

func (h *agentToolHarness) toolInputRecords() []toolInputRecord {
	h.toolInputMu.Lock()
	defer h.toolInputMu.Unlock()
	out := make([]toolInputRecord, len(h.toolInputs))
	copy(out, h.toolInputs)
	return out
}

func (h *agentToolHarness) toolOutputText() string {
	h.toolOutputMu.Lock()
	defer h.toolOutputMu.Unlock()
	return strings.Join(h.toolOutputs, "\n")
}

func (h *agentToolHarness) toolOutputRecords() []string {
	h.toolOutputMu.Lock()
	defer h.toolOutputMu.Unlock()
	out := make([]string, len(h.toolOutputs))
	copy(out, h.toolOutputs)
	return out
}

func (h *agentToolHarness) printToolTrace() {
	inputs := h.toolInputRecords()
	outputs := h.toolOutputRecords()
	if len(inputs) == 0 && len(outputs) == 0 {
		fmt.Println("Tool trace: no tool calls observed")
		return
	}
	fmt.Println("Tool trace:")
	for i, in := range inputs {
		fmt.Printf("- call %d: name=%s", i+1, in.Name)
		if in.CallID != "" {
			fmt.Printf(" id=%s", in.CallID)
		}
		fmt.Println()
		if in.Argument != "" {
			fmt.Printf("  args: %s\n", in.Argument)
		}
		if i < len(outputs) && outputs[i] != "" {
			fmt.Printf("  result: %s\n", redact(outputs[i]))
		}
	}
	if len(outputs) > len(inputs) {
		for i := len(inputs); i < len(outputs); i++ {
			fmt.Printf("- result %d: %s\n", i+1, redact(outputs[i]))
		}
	}
}

func agentToolInstruction() string {
	return `You are testing trpc-agent-go sandbox code execution.

When the user asks you to run or verify something, you must call the workspace_exec tool.
Do not solve execution tasks mentally. Use workspace_exec for filesystem, environment, and command behavior.
Keep final answers concise and include the exact marker requested by the user. Never print API keys or full environment variables.`
}
