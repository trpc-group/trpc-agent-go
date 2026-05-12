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
)

const (
	fileSystemPolicyAccessModesMarker      = "FILE_SYSTEM_POLICY_ACCESS_MODES_OK"
	fileSystemPolicySpecificityMarker      = "FILE_SYSTEM_POLICY_SPECIFICITY_OK"
	fileSystemPolicyGlobNoAccessMarker     = "FILE_SYSTEM_POLICY_GLOB_NO_ACCESS_OK"
	fileSystemPolicyAgentEnforcementMarker = "FILE_SYSTEM_POLICY_AGENT_ENFORCEMENT_OK"
	fileSystemPolicySecretSentinel         = "FILE_SYSTEM_POLICY_SECRET_SHOULD_NOT_APPEAR"
)

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
	if _, err := rt.Collect(ctx, ws, []string{"work/secret.txt"}); !sandbox.IsKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("AccessNone read was not denied: %v", err)
	}
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/secret.txt",
		Content: []byte("new"),
	}})
	if !sandbox.IsKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("AccessNone write was not denied: %v", err)
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    ".git/config",
		Content: []byte("bad"),
	}}); !sandbox.IsKind(err, sandbox.ErrPathDenied) {
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
	if !sandbox.IsKind(err, sandbox.ErrPathDenied) {
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
	if _, err := rt.Collect(ctx, ws, []string{"work/*.env"}); !sandbox.IsKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("glob AccessNone read was not denied: %v", err)
	}
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/secret.env",
		Content: []byte("TOKEN=new"),
	}})
	if !sandbox.IsKind(err, sandbox.ErrPathDenied) {
		return fmt.Errorf("glob AccessNone write was not denied: %v", err)
	}
	if err := maybeVerifyFileSystemPolicyShellMask(ctx, cfg, rt, ws); err != nil {
		return err
	}
	fmt.Println(fileSystemPolicyGlobNoAccessMarker)
	return nil
}

func runFileSystemPolicyAgentEnforcement(ctx context.Context, cfg config) error {
	profile := sandbox.WorkspaceWriteProfile().WithNoAccessGlobs("work/*.env")
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
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "python3",
		Args: []string{"-c", `
import socket
s = socket.socket()
s.settimeout(1)
try:
    s.connect(("1.1.1.1", 80))
    print("connected")
except OSError:
    print("network-denied")
`},
		Cwd: codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	return expectContains(res.Stdout, "network-denied")
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
