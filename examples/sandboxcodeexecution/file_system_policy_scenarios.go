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
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	fileSystemPolicyAccessModesMarker           = "FILE_SYSTEM_POLICY_ACCESS_MODES_OK"
	fileSystemPolicySpecificityMarker           = "FILE_SYSTEM_POLICY_SPECIFICITY_OK"
	fileSystemPolicyGlobNoAccessMarker          = "FILE_SYSTEM_POLICY_GLOB_NO_ACCESS_OK"
	fileSystemPolicyAgentEnforcementMarker      = "FILE_SYSTEM_POLICY_AGENT_ENFORCEMENT_OK"
	fileSystemPolicySymlinkNoAccessMarker       = "FILE_SYSTEM_POLICY_SYMLINK_NO_ACCESS_OK"
	fileSystemPolicyStageTargetValidationMarker = "FILE_SYSTEM_POLICY_STAGE_TARGET_VALIDATION_OK"
	fileSystemPolicyPutFilesSymlinkMarker       = "FILE_SYSTEM_POLICY_PUT_FILES_SYMLINK_TARGET_OK"
	fileSystemPolicyHostStageAbsoluteMarker     = "FILE_SYSTEM_POLICY_HOST_STAGE_ABSOLUTE_GRANT_OK"
	fileSystemPolicyHostStageSymlinkMarker      = "FILE_SYSTEM_POLICY_HOST_STAGE_SOURCE_SYMLINK_OK"
	fileSystemPolicyDirectoryNoAccessMarker     = "FILE_SYSTEM_POLICY_DIRECTORY_NO_ACCESS_MASK_OK"
	fileSystemPolicyMissingNoAccessMarker       = "FILE_SYSTEM_POLICY_MISSING_NO_ACCESS_MASK_OK"
	fileSystemPolicyGlobWritableRejectMarker    = "FILE_SYSTEM_POLICY_GLOB_WRITABLE_REJECT_OK"
	sessionPolicyExplicitZeroMarker             = "SESSION_POLICY_EXPLICIT_ZERO_OK"
	fileSystemPolicySecretSentinel              = "FILE_SYSTEM_POLICY_SECRET_SHOULD_NOT_APPEAR"
)

type collectPathInput struct {
	Path string `json:"path" jsonschema:"description=Workspace-relative path to collect."`
}

type collectPathOutput struct {
	Denied  bool   `json:"denied"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type stageHostValidationInput struct {
	To string `json:"to" jsonschema:"description=Workspace-relative destination. Use work/staged for this scenario."`
}

type stageHostValidationOutput struct {
	Denied bool   `json:"denied"`
	To     string `json:"to"`
	Error  string `json:"error,omitempty"`
}

type sessionPolicyProbeInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Optional sandbox session id."`
}

type sessionPolicyProbeOutput struct {
	Cleaned bool   `json:"cleaned"`
	Error   string `json:"error,omitempty"`
}

func runFileSystemPolicyAccessModes(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/secret.txt")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-access-modes", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/public.txt",
		Content: []byte("public"),
	}}); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, "work", "secret.txt"),
		[]byte(fileSystemPolicySecretSentinel),
		0o600,
	); err != nil {
		return err
	}
	files, err := rt.Collect(ctx, ws, []string{"work/public.txt"})
	if err != nil {
		return err
	}
	if len(files) != 1 || files[0].Content != "public" {
		return fmt.Errorf("public file was not readable: %#v", files)
	}
	if _, err := rt.Collect(ctx, ws, []string{"work/secret.txt"}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("no-access read was not denied: %v", err)
	}
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/secret.txt",
		Content: []byte("new"),
	}})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("no-access write was not denied: %v", err)
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    ".git/config",
		Content: []byte("bad"),
	}}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("protected metadata write was not denied: %v", err)
	}
	if err := maybeVerifyFileSystemPolicyNetworkRestricted(ctx, cfg, rt, ws); err != nil {
		return err
	}
	fmt.Println(fileSystemPolicyAccessModesMarker)
	return nil
}

func runFileSystemPolicySpecificity(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithReadPaths("work/readonly")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-specificity", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	readonly := filepath.Join(ws.Path, "work", "readonly")
	if err := os.MkdirAll(readonly, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(readonly, "note.txt"), []byte("readonly"), 0o600); err != nil {
		return err
	}
	files, err := rt.Collect(ctx, ws, []string{"work/readonly/note.txt"})
	if err != nil {
		return err
	}
	if len(files) != 1 || files[0].Content != "readonly" {
		return fmt.Errorf("readonly subtree was not readable: %#v", files)
	}
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/readonly/note.txt",
		Content: []byte("new"),
	}})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("more specific read rule did not block write: %v", err)
	}
	fmt.Println(fileSystemPolicySpecificityMarker)
	return nil
}

func runFileSystemPolicyGlobNoAccess(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-glob-no-access", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	secretPath := filepath.Join(ws.Path, "work", "secret.env")
	if err := os.WriteFile(secretPath, []byte("TOKEN="+fileSystemPolicySecretSentinel), 0o600); err != nil {
		return err
	}
	if _, err := rt.Collect(ctx, ws, []string{"work/*.env"}); !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("glob no-access read was not denied: %v", err)
	}
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/secret.env",
		Content: []byte("TOKEN=new"),
	}})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("glob no-access write was not denied: %v", err)
	}
	fmt.Println(fileSystemPolicyGlobNoAccessMarker)
	return nil
}

func runFileSystemPolicyAgentEnforcement(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/file-system-policy-secret.env")
	manifest := &sandbox.Manifest{
		Files: []sandbox.ManifestFile{{
			Path:    "work/file-system-policy-secret.env",
			Content: []byte("TOKEN=" + fileSystemPolicySecretSentinel + "\n"),
			Mode:    0o600,
		}},
	}
	h, err := newAgentToolHarness(ctx, cfg, profile, manifest)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "file-system-policy-agent-enforcement", `Use workspace_exec to verify the sandbox file system policy.

Run a shell command that checks both:
1. Reading work/file-system-policy-secret.env is denied.
2. Writing work/file-system-policy-secret.env is denied.

The command should print FILE_SYSTEM_POLICY_AGENT_ENFORCEMENT_OK only if both checks pass.
After the tool result, answer concisely and include FILE_SYSTEM_POLICY_AGENT_ENFORCEMENT_OK. Do not print file contents or environment variables.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := expectContains(final, fileSystemPolicyAgentEnforcementMarker); err != nil {
		return err
	}
	if strings.Contains(final, fileSystemPolicySecretSentinel) {
		return errors.New("agent final answer leaked denied secret content")
	}
	fmt.Println(redact(final))
	return nil
}

func runFileSystemPolicySymlinkNoAccess(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/secret.txt")
	manifest := &sandbox.Manifest{
		Files: []sandbox.ManifestFile{{
			Path:    "work/secret.txt",
			Content: []byte("TOKEN=" + fileSystemPolicySecretSentinel + "\n"),
			Mode:    0o600,
		}},
	}
	exec := sandbox.New(commonOptions(cfg, profile, 1<<20, 10*time.Second)...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return err
	}
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		profile,
		manifest,
		withAgentToolExtraTools([]tool.Tool{newCollectPathTool(exec)}),
		withAgentToolInstructionTail(
			"Use sandbox_collect_path when the user asks whether a workspace path is collectable.",
		),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "file-system-policy-symlink-no-access", `Use workspace_exec to run exactly:
mkdir -p work && rm -f work/secret-link.txt && ln -s secret.txt work/secret-link.txt

Then call sandbox_collect_path with path "work/secret-link.txt".
Answer with FILE_SYSTEM_POLICY_SYMLINK_NO_ACCESS_OK only if sandbox_collect_path reports denied=true.
Do not print file contents or environment variables.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := h.requireToolCalls("sandbox_collect_path", 1); err != nil {
		return err
	}
	if err := expectContains(final, fileSystemPolicySymlinkNoAccessMarker); err != nil {
		return err
	}
	if strings.Contains(final, fileSystemPolicySecretSentinel) {
		return errors.New("agent final answer leaked denied secret content")
	}
	fmt.Println(redact(final))
	return nil
}

func runFileSystemPolicyStageTargetValidation(ctx context.Context, cfg config) error {
	hostDir := filepath.Join(cfg.workspaceRoot, "stage-target-validation-host")
	if err := os.RemoveAll(hostDir); err != nil {
		return err
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(hostDir, "secret.txt"), []byte("stage-denied"), 0o600); err != nil {
		return err
	}
	profile := sandbox.WorkspaceWriteProfile().
		WithReadPaths(hostDir).
		WithNoAccessPaths("work/staged/secret.txt")
	exec := sandbox.New(commonOptions(cfg, profile, 1<<20, 10*time.Second)...)
	if err := requireManagedSandbox(ctx, exec.Runtime(), cfg); err != nil {
		return err
	}
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		profile,
		nil,
		withAgentToolExtraTools([]tool.Tool{newStageHostValidationTool(exec, hostDir)}),
		withAgentToolInstructionTail(
			"Use sandbox_stage_host_validation when the user asks to verify staging target policy.",
		),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "file-system-policy-stage-target-validation", `Call sandbox_stage_host_validation with to="work/staged".
Answer with FILE_SYSTEM_POLICY_STAGE_TARGET_VALIDATION_OK only if the tool reports denied=true.
Do not print file contents or environment variables.`)
	if err != nil {
		return err
	}
	if err := h.requireToolCalls("sandbox_stage_host_validation", 1); err != nil {
		return err
	}
	if err := expectContains(final, fileSystemPolicyStageTargetValidationMarker); err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func runFileSystemPolicyPutFilesSymlinkTarget(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/denied.txt")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-put-files-symlink-target", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	outside := filepath.Join(cfg.workspaceRoot, "put-files-outside-target.txt")
	if err := os.WriteFile(outside, []byte("outside-original"), 0o600); err != nil {
		return err
	}
	targets := []struct {
		name       string
		linkPath   string
		targetPath string
		original   string
	}{
		{
			name:       "git",
			linkPath:   filepath.Join(ws.Path, "work", "git-link.txt"),
			targetPath: filepath.Join(ws.Path, ".git", "config"),
			original:   "git-original",
		},
		{
			name:       "agents",
			linkPath:   filepath.Join(ws.Path, "work", "agents-link.txt"),
			targetPath: filepath.Join(ws.Path, ".agents", "state.json"),
			original:   "agents-original",
		},
		{
			name:       "no-access",
			linkPath:   filepath.Join(ws.Path, "work", "denied-link.txt"),
			targetPath: filepath.Join(ws.Path, "work", "denied.txt"),
			original:   "denied-original",
		},
		{
			name:       "outside",
			linkPath:   filepath.Join(ws.Path, "work", "outside-link.txt"),
			targetPath: outside,
			original:   "outside-original",
		},
	}
	for _, target := range targets {
		if err := os.MkdirAll(filepath.Dir(target.targetPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target.targetPath, []byte(target.original), 0o600); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target.linkPath), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(target.linkPath); err != nil {
			return err
		}
		if err := os.Symlink(target.targetPath, target.linkPath); err != nil {
			return err
		}
		err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
			Path:    "work/" + filepath.Base(target.linkPath),
			Content: []byte("unexpected-" + target.name),
		}})
		if !isSandboxKind(err, sandbox.ErrPathDenied) {
			return fmt.Errorf("%s symlink write was not denied: %v", target.name, err)
		}
		data, err := os.ReadFile(target.targetPath)
		if err != nil {
			return err
		}
		if string(data) != target.original {
			return fmt.Errorf("%s target changed through symlink: %q", target.name, data)
		}
	}
	fmt.Println(fileSystemPolicyPutFilesSymlinkMarker)
	return nil
}

func runFileSystemPolicyHostStageAbsoluteGrant(ctx context.Context, cfg config) error {
	oldWD, err := os.Getwd()
	if err != nil {
		return err
	}
	relativeRoot := filepath.Join(cfg.workspaceRoot, "relative-stage-root")
	hostDir := filepath.Join(relativeRoot, "host-input")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(hostDir, "input.txt"), []byte("absolute-grant"), 0o600); err != nil {
		return err
	}
	if err := os.Chdir(relativeRoot); err != nil {
		return err
	}
	defer func() { _ = os.Chdir(oldWD) }()

	relativeRuntime := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile().WithReadPaths("host-input"),
		1<<20,
		3*time.Second,
	)
	relativeWS, err := relativeRuntime.CreateWorkspace(ctx, "file-system-policy-host-stage-relative", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	err = relativeRuntime.StageDirectory(ctx, relativeWS, "host-input", "work/relative", codeexecutor.StageOptions{})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("relative host source was not denied: %v", err)
	}

	relativeGrantRuntime := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile().WithReadPaths("host-input"),
		1<<20,
		3*time.Second,
	)
	relativeGrantWS, err := relativeGrantRuntime.CreateWorkspace(ctx, "file-system-policy-host-stage-relative-grant", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	err = relativeGrantRuntime.StageDirectory(ctx, relativeGrantWS, hostDir, "work/relative-grant", codeexecutor.StageOptions{})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("absolute source with relative grant was not denied: %v", err)
	}

	absoluteRuntime := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile().WithReadPaths(hostDir),
		1<<20,
		3*time.Second,
	)
	absoluteWS, err := absoluteRuntime.CreateWorkspace(ctx, "file-system-policy-host-stage-absolute", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := absoluteRuntime.StageDirectory(ctx, absoluteWS, hostDir, "work/absolute", codeexecutor.StageOptions{}); err != nil {
		return err
	}
	files, err := absoluteRuntime.Collect(ctx, absoluteWS, []string{"work/absolute/input.txt"})
	if err != nil {
		return err
	}
	if len(files) != 1 || files[0].Content != "absolute-grant" {
		return fmt.Errorf("absolute host stage did not copy input: %#v", files)
	}
	fmt.Println(fileSystemPolicyHostStageAbsoluteMarker)
	return nil
}

func runFileSystemPolicyHostStageSourceSymlink(ctx context.Context, cfg config) error {
	hostDir := filepath.Join(cfg.workspaceRoot, "host-stage-source-symlink")
	outsideDir := filepath.Join(cfg.workspaceRoot, "host-stage-outside")
	if err := os.RemoveAll(hostDir); err != nil {
		return err
	}
	if err := os.RemoveAll(outsideDir); err != nil {
		return err
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		return err
	}
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		return err
	}
	if err := os.Symlink(outsideFile, filepath.Join(hostDir, "outside-link.txt")); err != nil {
		return err
	}
	rt := newRuntime(
		cfg,
		sandbox.WorkspaceWriteProfile().WithReadPaths(hostDir),
		1<<20,
		3*time.Second,
	)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-host-stage-source-symlink", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	err = rt.StageDirectory(ctx, ws, hostDir, "work/staged", codeexecutor.StageOptions{})
	if !isSandboxKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("source symlink stage was not denied: %v", err)
	}
	fmt.Println(fileSystemPolicyHostStageSymlinkMarker)
	return nil
}

func runFileSystemPolicyDirectoryNoAccessMask(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/denied-dir")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-directory-no-access-mask", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(ws.Path, "work", "denied-dir"), 0o755); err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-c",
			"echo unexpected > denied-dir/new.txt 2>/dev/null || echo " + fileSystemPolicyDirectoryNoAccessMarker,
		},
		Cwd: codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if strings.Contains(res.Stdout, "unexpected") {
		return fmt.Errorf("denied directory was writable: result=%#v", res)
	}
	if err := expectContains(res.Stdout, fileSystemPolicyDirectoryNoAccessMarker); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "work", "denied-dir", "new.txt")); !os.IsNotExist(err) {
		return fmt.Errorf("denied directory write appeared on host, stat err=%v", err)
	}
	fmt.Println(fileSystemPolicyDirectoryNoAccessMarker)
	return nil
}

func runFileSystemPolicyMissingNoAccessMask(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessPaths("work/missing-secret.txt")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-missing-no-access-mask", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	missing := filepath.Join(ws.Path, "work", "missing-secret.txt")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		return fmt.Errorf("missing no-access target unexpectedly exists before run, stat err=%v", err)
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-c",
			"echo unexpected > missing-secret.txt 2>/dev/null || echo " + fileSystemPolicyMissingNoAccessMarker,
		},
		Cwd: codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if strings.Contains(res.Stdout, "unexpected") {
		return fmt.Errorf("missing no-access file was writable: result=%#v", res)
	}
	if err := expectContains(res.Stdout, fileSystemPolicyMissingNoAccessMarker); err != nil {
		return err
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		return fmt.Errorf("missing no-access placeholder leaked to host, stat err=%v", err)
	}
	fmt.Println(fileSystemPolicyMissingNoAccessMarker)
	return nil
}

func runFileSystemPolicyGlobWritableReject(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
	rt := newRuntime(cfg, profile, 1<<20, 3*time.Second)
	ws, err := rt.CreateWorkspace(ctx, "file-system-policy-glob-writable-reject", codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	_, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-c",
			"echo unexpected > future.env",
		},
		Cwd: codeexecutor.DirWork,
	})
	if !isSandboxKind(err, sandbox.ErrPolicyViolation) {
		return fmt.Errorf("writable-overlap glob no-access did not fail closed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "work", "future.env")); !os.IsNotExist(err) {
		return fmt.Errorf("glob setup rejection still created matching file, stat err=%v", err)
	}
	fmt.Println(fileSystemPolicyGlobWritableRejectMarker)
	return nil
}

func runSessionPolicyExplicitZero(ctx context.Context, cfg config) error {
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		sandbox.WorkspaceWriteProfile(),
		nil,
		withAgentToolExtraTools([]tool.Tool{newSessionPolicyProbeTool(cfg)}),
		withAgentToolInstructionTail(
			"Use sandbox_session_policy_probe when the user asks to verify explicit session policy.",
		),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "session-policy-explicit-zero", `Call sandbox_session_policy_probe.
Answer with SESSION_POLICY_EXPLICIT_ZERO_OK only if the tool reports cleaned=true.`)
	if err != nil {
		return err
	}
	if err := h.requireToolCalls("sandbox_session_policy_probe", 1); err != nil {
		return err
	}
	if err := expectContains(final, sessionPolicyExplicitZeroMarker); err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func newCollectPathTool(exec *sandbox.CodeExecutor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in collectPathInput) (collectPathOutput, error) {
			path := strings.TrimSpace(in.Path)
			if path == "" {
				return collectPathOutput{}, errors.New("path is required")
			}
			ctxIO, ws, err := artifactToolWorkspace(ctx, exec)
			if err != nil {
				return collectPathOutput{}, err
			}
			files, err := exec.Runtime().Collect(ctxIO, ws, []string{path})
			out := collectPathOutput{Path: path}
			if isSandboxKind(err, sandbox.ErrPathDenied) {
				out.Denied = true
				out.Error = err.Error()
				return out, nil
			}
			if err != nil {
				out.Error = err.Error()
				return out, nil
			}
			if len(files) > 0 {
				out.Content = files[0].Content
			}
			return out, nil
		},
		function.WithName("sandbox_collect_path"),
		function.WithDescription("Collect a workspace path and report whether sandbox policy denied the read."),
	)
}

func newStageHostValidationTool(exec *sandbox.CodeExecutor, hostDir string) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in stageHostValidationInput) (stageHostValidationOutput, error) {
			to := strings.TrimSpace(in.To)
			if to == "" {
				return stageHostValidationOutput{}, errors.New("to is required")
			}
			ctxIO, ws, err := artifactToolWorkspace(ctx, exec)
			if err != nil {
				return stageHostValidationOutput{}, err
			}
			err = exec.Runtime().StageDirectory(ctxIO, ws, hostDir, to, codeexecutor.StageOptions{})
			out := stageHostValidationOutput{To: to}
			if isSandboxKind(err, sandbox.ErrPathDenied) {
				out.Denied = true
				out.Error = err.Error()
				return out, nil
			}
			if err != nil {
				out.Error = err.Error()
			}
			return out, nil
		},
		function.WithName("sandbox_stage_host_validation"),
		function.WithDescription("Try staging a prepared host directory and report whether target policy denied it."),
	)
}

func newSessionPolicyProbeTool(cfg config) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in sessionPolicyProbeInput) (sessionPolicyProbeOutput, error) {
			sessionID := strings.TrimSpace(in.SessionID)
			if sessionID == "" {
				sessionID = "session-policy-explicit-zero-probe"
			}
			rt := sandbox.NewRuntime(
				sandbox.WithWorkspaceRoot(cfg.workspaceRoot),
				sandbox.WithPermissionProfile(sandbox.WorkspaceWriteProfile()),
				sandbox.WithSessionPolicy(sandbox.SessionPolicy{}),
			)
			ws, err := rt.CreateWorkspace(ctx, sessionID, codeexecutor.WorkspacePolicy{})
			if err != nil {
				return sessionPolicyProbeOutput{Error: err.Error()}, nil
			}
			if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
				Path:    "work/marker.txt",
				Content: []byte(sessionPolicyExplicitZeroMarker),
			}}); err != nil {
				return sessionPolicyProbeOutput{Error: err.Error()}, nil
			}
			if err := rt.Cleanup(ctx, ws); err != nil {
				return sessionPolicyProbeOutput{Error: err.Error()}, nil
			}
			if _, err := os.Stat(ws.Path); os.IsNotExist(err) {
				return sessionPolicyProbeOutput{Cleaned: true}, nil
			} else if err != nil {
				return sessionPolicyProbeOutput{Error: err.Error()}, nil
			}
			return sessionPolicyProbeOutput{Error: "workspace still exists after cleanup"}, nil
		},
		function.WithName("sandbox_session_policy_probe"),
		function.WithDescription("Verify explicit SessionPolicy{} disables persistence by cleaning a probe workspace."),
	)
}

func maybeVerifyFileSystemPolicyNetworkRestricted(
	ctx context.Context,
	cfg config,
	rt *sandbox.Runtime,
	ws codeexecutor.Workspace,
) error {
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		if errors.Is(err, errSkip) {
			return nil
		}
		return err
	}
	return expectNetworkDenied(ctx, rt, ws)
}

func maybeVerifyFileSystemPolicyShellMask(
	ctx context.Context,
	cfg config,
	rt *sandbox.Runtime,
	ws codeexecutor.Workspace,
) error {
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		if errors.Is(err, errSkip) {
			return nil
		}
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "cat secret.env >/dev/null 2>&1 || echo shell-mask-denied"},
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 && strings.Contains(res.Stdout, "shell-mask-denied") {
		return nil
	}
	return fmt.Errorf("shell mask did not deny read: result=%#v", res)
}
