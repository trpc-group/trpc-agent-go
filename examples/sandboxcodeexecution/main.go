//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the sandbox code executor with real agent execution
// and deterministic sandbox behavior checks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var errSkip = errors.New("skip")

func isSandboxKind(err error, kind sandbox.ErrorKind) bool {
	return err != nil && strings.HasPrefix(err.Error(), string(kind))
}

type config struct {
	scenario         string
	modelName        string
	workspaceRoot    string
	keepWorkspace    bool
	requireOSSandbox bool
}

type scenario struct {
	name string
	run  func(context.Context, config) error
}

func main() {
	scenarioName := flag.String(
		"scenario",
		"basic",
		"basic|"+
			"agent-tool-manual-run|agent-tool-basic|agent-tool-session-persistence|agent-tool-security|"+
			"agent-artifact-stage|agent-artifact-save|agent-artifact-pin|"+
			"session-persistence|session-isolation|"+
			"env-redaction|metadata-protection|no-access|network-restricted|"+
			"network-policy-restricted|network-policy-enabled|network-policy-additional-permissions|network-policy-agent-enforcement|"+
			"timeout|output-cap|additional-permissions|"+
			"shell-environment-policy-default-all|shell-environment-policy-core|shell-environment-policy-none-set|"+
			"shell-environment-policy-include-only|shell-environment-policy-exclude-set|shell-environment-policy-agent|"+
			"file-system-policy-access-modes|file-system-policy-specificity|file-system-policy-glob-no-access|"+
			"file-system-policy-agent-enforcement|file-system-policy-symlink-no-access|"+
			"file-system-policy-stage-target-validation|file-system-policy-put-files-symlink-target|"+
			"file-system-policy-host-stage-absolute-grant|file-system-policy-host-stage-source-symlink|"+
			"file-system-policy-directory-no-access-mask|file-system-policy-missing-no-access-mask|"+
			"file-system-policy-glob-writable-reject|session-workspace-id-sanitization|"+
			"session-policy-explicit-zero|"+
			"all",
	)
	modelName := flag.String("model", "glm-4.7-flash", "model name")
	workspaceRoot := flag.String("workspace-root", "", "sandbox workspace root")
	keepWorkspace := flag.Bool("keep-workspace", false, "keep generated sandbox workspaces")
	requireOSSandbox := flag.Bool("require-os-sandbox", true, "fail instead of skipping when OS sandbox is unavailable")
	flag.Parse()

	root, cleanup, err := resolveWorkspaceRoot(*workspaceRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workspace root: %v\n", err)
		os.Exit(1)
	}
	if cleanup != nil && !*keepWorkspace {
		defer cleanup()
	}
	cfg := config{
		scenario:         *scenarioName,
		modelName:        *modelName,
		workspaceRoot:    root,
		keepWorkspace:    *keepWorkspace,
		requireOSSandbox: *requireOSSandbox,
	}
	fmt.Printf("sandbox workspace root: %s\n", cfg.workspaceRoot)
	fmt.Printf("model: %s\n", cfg.modelName)
	fmt.Printf("require OS sandbox: %t\n\n", cfg.requireOSSandbox)

	if err := runScenarios(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox example failed: %v\n", err)
		os.Exit(1)
	}
}

func runScenarios(ctx context.Context, cfg config) error {
	scenarios := []scenario{
		{"basic", runBasic},
		{"agent-tool-manual-run", runAgentToolManualRun}, // manual run of agent with tool to see what it can do
		{"agent-tool-basic", runAgentToolBasic},
		{"agent-tool-session-persistence", runAgentToolSessionPersistence},
		{"agent-tool-security", runAgentToolSecurity},
		{"agent-artifact-stage", runAgentArtifactStage},
		{"agent-artifact-save", runAgentArtifactSave},
		{"agent-artifact-pin", runAgentArtifactPin},
		{"session-persistence", runSessionPersistence},
		{"session-isolation", runSessionIsolation},
		{"env-redaction", runEnvRedaction},
		{"metadata-protection", runMetadataProtection},
		{"no-access", runNoAccess},
		{"network-restricted", runNetworkRestricted},
		{"network-policy-restricted", runNetworkPolicyRestricted},
		{"network-policy-enabled", runNetworkPolicyEnabled},
		{"network-policy-additional-permissions", runNetworkPolicyAdditionalPermissions},
		{"network-policy-agent-enforcement", runNetworkPolicyAgentEnforcement},
		{"timeout", runTimeout},
		{"output-cap", runOutputCap},
		{"additional-permissions", runAdditionalPermissions},
		{"shell-environment-policy-default-all", runShellEnvironmentPolicyDefaultAll},
		{"shell-environment-policy-core", runShellEnvironmentPolicyCore},
		{"shell-environment-policy-none-set", runShellEnvironmentPolicyNoneSet},
		{"shell-environment-policy-include-only", runShellEnvironmentPolicyIncludeOnly},
		{"shell-environment-policy-exclude-set", runShellEnvironmentPolicyExcludeSet},
		{"shell-environment-policy-agent", runShellEnvironmentPolicyAgent},
		{"file-system-policy-access-modes", runFileSystemPolicyAccessModes},
		{"file-system-policy-specificity", runFileSystemPolicySpecificity},
		{"file-system-policy-glob-no-access", runFileSystemPolicyGlobNoAccess},
		{"file-system-policy-agent-enforcement", runFileSystemPolicyAgentEnforcement},
		{"file-system-policy-symlink-no-access", runFileSystemPolicySymlinkNoAccess},
		{"file-system-policy-stage-target-validation", runFileSystemPolicyStageTargetValidation},
		{"file-system-policy-put-files-symlink-target", runFileSystemPolicyPutFilesSymlinkTarget},
		{"file-system-policy-host-stage-absolute-grant", runFileSystemPolicyHostStageAbsoluteGrant},
		{"file-system-policy-host-stage-source-symlink", runFileSystemPolicyHostStageSourceSymlink},
		{"file-system-policy-directory-no-access-mask", runFileSystemPolicyDirectoryNoAccessMask},
		{"file-system-policy-missing-no-access-mask", runFileSystemPolicyMissingNoAccessMask},
		{"file-system-policy-glob-writable-reject", runFileSystemPolicyGlobWritableReject},
		{"session-workspace-id-sanitization", runSessionWorkspaceIDSanitization},
		{"session-policy-explicit-zero", runSessionPolicyExplicitZero},
	}
	selected := map[string]scenario{}
	for _, sc := range scenarios {
		selected[sc.name] = sc
	}
	var toRun []scenario
	if cfg.scenario == "all" {
		toRun = scenarios
	} else {
		sc, ok := selected[cfg.scenario]
		if !ok {
			return fmt.Errorf("unknown scenario %q", cfg.scenario)
		}
		toRun = []scenario{sc}
	}
	for _, sc := range toRun {
		fmt.Printf("== %s ==\n", sc.name)
		err := sc.run(ctx, cfg)
		switch {
		case err == nil:
			fmt.Printf("PASS %s\n\n", sc.name)
		case errors.Is(err, errSkip):
			fmt.Printf("SKIP %s\n\n", sc.name)
		default:
			return fmt.Errorf("%s: %w", sc.name, err)
		}
	}
	return nil
}

func runBasic(ctx context.Context, cfg config) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; source ./glm.sh from the repo root to run the real LLM scenario.")
		return errSkip
	}
	exec := sandbox.New(commonOptions(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 10*time.Second)...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return err
	}
	agent := llmagent.New(
		"sandbox_code_agent",
		llmagent.WithModel(openai.New(cfg.modelName)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(1200),
			Temperature: floatPtr(0.1),
		}),
		llmagent.WithInstruction(
			"Use code execution for arithmetic. Prefer a short Python code block that prints deterministic JSON, then answer concisely.",
		),
		llmagent.WithCodeExecutor(exec),
	)
	r := runner.NewRunner("sandbox_code_agent", agent)
	defer r.Close()
	events, err := r.Run(
		ctx,
		"sandbox-example-user",
		"sandbox-example-basic",
		model.NewUserMessage("Use code execution to compute count, sum, and mean for: 5, 12, 8, 15, 7, 9, 11."),
	)
	if err != nil {
		return err
	}
	var final strings.Builder
	for event := range events {
		if event.Error != nil {
			return fmt.Errorf("agent event error: %w", event.Error)
		}
		// A model completion with Done=true can still be followed by tool/code
		// execution events. Stop only once the runner reports completion.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			if choice.Message.Role != model.RoleTool && choice.Message.Content != "" {
				final.WriteString(choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				final.WriteString(choice.Delta.Content)
			}
		}
		if event.IsRunnerCompletion() {
			break
		}
	}
	answer := strings.TrimSpace(final.String())
	if answer == "" {
		return errors.New("agent produced no final answer")
	}
	fmt.Println(redact(answer))
	return nil
}

func runSessionPersistence(ctx context.Context, cfg config) error {
	rt := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile(),
		1<<20,
		3*time.Second,
		sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit: sandbox.ShellEnvironmentPolicyInheritCore,
		}),
	)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "session-persistence", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if _, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "printf persistent > marker.txt"},
		Cwd:  codeexecutor.DirWork,
	}); err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "cat",
		Args: []string{"marker.txt"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	return expectContains(res.Stdout, "persistent")
}

func runSessionIsolation(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	s1, err := rt.CreateWorkspace(ctx, "s1", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if _, err := rt.RunProgram(ctx, s1, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "printf s1 > marker.txt"},
		Cwd:  codeexecutor.DirWork,
	}); err != nil {
		return err
	}
	s2, err := rt.CreateWorkspace(ctx, "s2", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, s2, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "test ! -f marker.txt && echo isolated"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	return expectContains(res.Stdout, "isolated")
}

func runEnvRedaction(ctx context.Context, cfg config) error {
	openAIAPIKey, hadOpenAIAPIKey := os.LookupEnv("OPENAI_API_KEY")
	if openAIAPIKey == "" {
		if err := os.Setenv("OPENAI_API_KEY", "sandbox-example-redacted"); err != nil {
			return err
		}
		defer func() {
			if hadOpenAIAPIKey {
				_ = os.Setenv("OPENAI_API_KEY", openAIAPIKey)
			} else {
				_ = os.Unsetenv("OPENAI_API_KEY")
			}
		}()
	}
	rt := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile(),
		1<<20,
		3*time.Second,
		sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit: sandbox.ShellEnvironmentPolicyInheritCore,
		}),
	)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "env-redaction", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", `if [ -z "$OPENAI_API_KEY" ]; then echo hidden; else echo visible; fi`},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	return expectContains(res.Stdout, "hidden")
}

func runMetadataProtection(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "metadata-protection", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    ".agents/should-not-write",
		Content: []byte("bad"),
	}}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("file API protected metadata write was not denied: %v", err)
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "echo bad > ../.git/config"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return errors.New("shell protected metadata write unexpectedly succeeded")
	}
	return nil
}

func runNoAccess(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/secret.env")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "no-access", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, "work", "secret.env"),
		[]byte("OPENAI_API_KEY=redacted"),
		0o600,
	); err != nil {
		return err
	}
	if _, err := rt.Collect(ctx, ws, []string{"work/secret.env"}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("file API no-access rule was not enforced: %v", err)
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/secret.env",
		Content: []byte("OPENAI_API_KEY=new"),
	}}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("file API no-access write was not enforced: %v", err)
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "cat secret.env >/dev/null 2>&1"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return errors.New("shell read of denied file unexpectedly succeeded")
	}
	return nil
}

func runTimeout(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "timeout", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", "sleep 10"},
		Cwd:     codeexecutor.DirWork,
		Timeout: 100 * time.Millisecond,
	})
	if !isSandboxKind(err, sandbox.ErrTimeout) {
		return fmt.Errorf("expected timeout, got result=%#v err=%v", res, err)
	}
	return nil
}

func runOutputCap(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 96, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "output-cap", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "yes x | head -c 4096"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	return expectContains(res.Stdout, "[truncated]")
}

func runAdditionalPermissions(ctx context.Context, cfg config) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "additional-permissions", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	externalDir := filepath.Join(cfg.workspaceRoot, "external-input")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		return err
	}
	externalFile := filepath.Join(externalDir, "note.txt")
	if err := os.WriteFile(externalFile, []byte("temporary grant"), 0o600); err != nil {
		return err
	}
	err = rt.StageDirectory(ctx, ws, externalFile, "work/no-grant.txt", codeexecutor.StageOptions{})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("expected default external read denial, got %v", err)
	}
	grantCtx := sandbox.WithAdditionalPermissions(ctx, sandbox.AdditionalPermissions{
		ReadPaths: []string{externalFile},
	})
	if err := rt.StageDirectory(grantCtx, ws, externalFile, "work/granted.txt", codeexecutor.StageOptions{}); err != nil {
		return err
	}
	err = rt.StageDirectory(ctx, ws, externalFile, "work/no-grant-again.txt", codeexecutor.StageOptions{})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("expected per-command grant to expire, got %v", err)
	}
	files, err := rt.Collect(ctx, ws, []string{"work/granted.txt"})
	if err != nil {
		return err
	}
	if len(files) != 1 || files[0].Content != "temporary grant" {
		return fmt.Errorf("granted file not staged: %#v", files)
	}
	return nil
}

func newRuntime(
	cfg config,
	profile sandbox.PermissionProfile,
	outputCap int,
	timeout time.Duration,
	opts ...sandbox.Option,
) *sandbox.Runtime {
	options := commonOptions(cfg, profile, outputCap, timeout)
	options = append(options, opts...)
	return sandbox.NewRuntime(options...)
}

func commonOptions(
	cfg config,
	profile sandbox.PermissionProfile,
	outputCap int,
	timeout time.Duration,
) []sandbox.Option {
	return []sandbox.Option{
		sandbox.WithWorkspaceRoot(cfg.workspaceRoot),
		sandbox.WithPermissionProfile(profile),
		sandbox.WithOutputMaxBytes(outputCap),
		sandbox.WithDefaultTimeout(timeout),
	}
}

func requireManagedSandbox(ctx context.Context, rt *sandbox.Runtime, cfg config) error {
	ws, err := rt.CreateWorkspace(ctx, "__preflight", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	_, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "true",
		Cwd:     codeexecutor.DirWork,
		Timeout: 2 * time.Second,
	})
	if err == nil {
		return nil
	}
	if cfg.requireOSSandbox {
		return err
	}
	fmt.Printf("managed OS sandbox unavailable: %v\n", err)
	return errSkip
}

func resolveWorkspaceRoot(root string) (string, func(), error) {
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", nil, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", nil, err
		}
		return abs, nil, nil
	}
	tmp, err := os.MkdirTemp("", "trpc-agent-sandbox-example-")
	if err != nil {
		return "", nil, err
	}
	return tmp, func() { _ = os.RemoveAll(tmp) }, nil
}

func expectContains(got, want string) error {
	if !strings.Contains(got, want) {
		return fmt.Errorf("expected %q to contain %q", got, want)
	}
	return nil
}

func redact(s string) string {
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL"} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		s = strings.ReplaceAll(s, value, "<redacted>")
	}
	return s
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
