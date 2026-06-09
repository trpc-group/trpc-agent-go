//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCodeExecutorExecuteCodeDisabledProfile(t *testing.T) {
	e := New(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	if e.Engine() != e.Runtime() {
		t.Fatalf("Engine did not expose runtime")
	}
	if got := e.CodeBlockDelimiter(); got.Start != "```" || got.End != "```" {
		t.Fatalf("delimiter = %#v", got)
	}

	empty, err := e.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Output != "" {
		t.Fatalf("empty execution output = %q, want empty", empty.Output)
	}

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{
			AppName: "sandbox-app",
			UserID:  "user-1",
			ID:      "session-1",
		},
	})
	res, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "bash", Code: "echo from-bash"},
			{Language: "ruby", Code: "puts 'unsupported'"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "from-bash") {
		t.Fatalf("execution output = %q, missing bash stdout", res.Output)
	}
	if !strings.Contains(res.Output, "unsupported language: ruby") {
		t.Fatalf("execution output = %q, missing unsupported language error", res.Output)
	}
}

func TestCodeExecutorExecuteCodeErrorBranches(t *testing.T) {
	e := New(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	res, err := e.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "bash", Code: "echo stderr >&2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "stderr") {
		t.Fatalf("execution output = %q, missing stderr", res.Output)
	}

	rootFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(rootFile, []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	badRoot := New(
		WithWorkspaceRoot(rootFile),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	_, err = badRoot.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{Language: "bash", Code: "echo no-workspace"}},
	})
	if err == nil {
		t.Fatalf("ExecuteCode with file workspace root unexpectedly succeeded")
	}

	readOnly := New(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(ReadOnlyProfile()),
	)
	res, err = readOnly.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "readonly",
		CodeBlocks:  []codeexecutor.CodeBlock{{Language: "bash", Code: "echo denied"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "PathDenied write") {
		t.Fatalf("read-only execution output = %q, missing write denial", res.Output)
	}

	externalProfile := ExternalSandboxProfile(NetworkPolicy{})
	externalProfile.fileSystem = WorkspaceWriteProfile().fileSystem
	external := New(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(externalProfile),
	)
	res, err = external.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		ExecutionID: "external",
		CodeBlocks:  []codeexecutor.CodeBlock{{Language: "bash", Code: "echo unsupported"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "UnsupportedBackend") {
		t.Fatalf("external execution output = %q, missing unsupported backend", res.Output)
	}
}

func TestRuntimeRunProgramDisabledProfile(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/disabled", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:   "bash",
		Args:  []string{"-c", "read v; echo ${v}-${SANDBOX_RUN}; echo problem >&2; exit 7"},
		Cwd:   "work/new-dir",
		Stdin: "input",
		Env:   map[string]string{"SANDBOX_RUN": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 || strings.TrimSpace(res.Stdout) != "input-ok" {
		t.Fatalf("run result = %#v, want exit 7 stdout input-ok", res)
	}
	if strings.TrimSpace(res.Stderr) != "problem" {
		t.Fatalf("stderr = %q, want problem", res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "work", "new-dir")); err != nil {
		t.Fatalf("run cwd was not materialized: %v", err)
	}
}

func TestRuntimeRunProgramErrorsAndTimeout(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
		WithDefaultTimeout(25*time.Millisecond),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/errors", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{}); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("empty command error = %v, want ErrPolicyViolation", err)
	}
	if _, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "definitely-not-a-real-sandbox-test-command",
	}); !isKind(err, ErrSetupFailed) {
		t.Fatalf("start error = %v, want ErrSetupFailed", err)
	}

	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "sleep 1"},
	})
	if !isKind(err, ErrTimeout) {
		t.Fatalf("timeout error = %v, want ErrTimeout", err)
	}
	if !res.TimedOut || res.ExitCode != -1 {
		t.Fatalf("timeout result = %#v, want timed out exit -1", res)
	}

	externalProfile := ExternalSandboxProfile(NetworkPolicy{})
	externalProfile.fileSystem = WorkspaceWriteProfile().fileSystem
	external := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(externalProfile),
	)
	extWS, err := external.CreateWorkspace(context.Background(), "run/external", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = external.RunProgram(context.Background(), extWS, codeexecutor.RunProgramSpec{Cmd: "true"})
	if !isKind(err, ErrUnsupportedBackend) {
		t.Fatalf("external runtime error = %v, want ErrUnsupportedBackend", err)
	}

	strict := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(PermissionProfile{typ: profileManaged}),
	)
	strictWS, err := strict.CreateWorkspace(context.Background(), "run/strict", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = strict.RunProgram(context.Background(), strictWS, codeexecutor.RunProgramSpec{Cmd: "true"})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("strict runtime error = %v, want ErrPathDenied", err)
	}

	if code, err := exitCodeFromWait(errors.New("wait failed"), false); err == nil || code != 0 {
		t.Fatalf("exitCodeFromWait = %d, %v; want 0 with error", code, err)
	}
	fileCwd := filepath.Join(ws.Path, "work", "file-cwd")
	if err := os.WriteFile(fileCwd, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.ensureRunCwd(DangerFullAccessProfile(), ws, "work/file-cwd", fileCwd); err == nil {
		t.Fatalf("file cwd unexpectedly succeeded")
	}
}

func TestRuntimeRunProgramSerialTimeoutStartsAfterLock(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
		WithSessionPolicy(SessionPolicy{RunConcurrency: SessionRunConcurrencySerial}),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/serial-timeout", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	firstReady := filepath.Join(ws.Path, "work", "first-ready")
	firstDone := make(chan error, 1)
	go func() {
		_, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-c", "printf ready > first-ready; sleep 0.75"},
			Timeout: 2 * time.Second,
		})
		firstDone <- err
	}()
	waitUntil := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(firstReady); err == nil {
			break
		}
		select {
		case err := <-firstDone:
			t.Fatalf("first run finished before readiness marker: %v", err)
		default:
		}
		if time.Now().After(waitUntil) {
			t.Fatalf("first run did not write readiness marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	res, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:     "true",
		Timeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("queued run error = %v", err)
	}
	if res.TimedOut {
		t.Fatalf("queued run unexpectedly timed out: %#v", res)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first run error = %v", err)
	}
}

func TestRuntimeWorkspaceLifecycleAndManifest(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "sandbox", "unsafe", "id", "work", "stale.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(
		WithWorkspaceRoot(root),
		WithManifest(Manifest{
			Environment: map[string]string{"SANDBOX_MANIFEST_ENV": "yes", "": "ignored"},
			Files: []ManifestFile{{
				Path:    "work/manifest.txt",
				Content: []byte("manifest"),
			}},
			EphemeralPaths: []string{"work/stale.txt"},
		}),
	)
	if rt.Manager() != rt || rt.FS() != rt || rt.Runner() != rt {
		t.Fatalf("runtime did not expose manager/fs/runner")
	}
	ws, err := rt.CreateWorkspace(context.Background(), " unsafe/id ", codeexecutor.WorkspacePolicy{
		MaxDiskBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != "unsafe_id" {
		t.Fatalf("workspace id = %q, want unsafe_id", ws.ID)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("ephemeral file still exists, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(ws.Path, "work", "manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "manifest" {
		t.Fatalf("manifest content = %q", data)
	}
	if !hasEnv(rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{}), "SANDBOX_MANIFEST_ENV=yes") {
		t.Fatalf("manifest environment was not applied")
	}

	persistent := filepath.Join(ws.Path, "work", "keep.txt")
	if err := os.WriteFile(persistent, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.Cleanup(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(persistent); err != nil {
		t.Fatalf("default cleanup removed persistent workspace: %v", err)
	}

	cleaning := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithSessionPolicy(SessionPolicy{RunConcurrency: SessionRunConcurrencySerial}),
	)
	cleanWS, err := cleaning.CreateWorkspace(context.Background(), "cleanup", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cleaning.Cleanup(context.Background(), cleanWS); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cleanWS.Path); !os.IsNotExist(err) {
		t.Fatalf("cleanup workspace still exists, stat err=%v", err)
	}

	defaultWS, err := cleaning.CreateWorkspace(context.Background(), "", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if defaultWS.ID != "default" {
		t.Fatalf("default workspace id = %q", defaultWS.ID)
	}
	if err := cleaning.Cleanup(context.Background(), codeexecutor.Workspace{}); err != nil {
		t.Fatal(err)
	}

	layoutRoot := t.TempDir()
	layoutPath := filepath.Join(layoutRoot, "sandbox", "bad-layout")
	if err := os.MkdirAll(layoutPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layoutPath, codeexecutor.DirWork), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(WithWorkspaceRoot(layoutRoot)).CreateWorkspace(
		context.Background(),
		"bad-layout",
		codeexecutor.WorkspacePolicy{},
	); err == nil {
		t.Fatalf("workspace with file at work directory unexpectedly succeeded")
	}

	homeRoot := t.TempDir()
	homePath := filepath.Join(homeRoot, "sandbox", "bad-home")
	if err := os.MkdirAll(homePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "home"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(WithWorkspaceRoot(homeRoot)).CreateWorkspace(
		context.Background(),
		"bad-home",
		codeexecutor.WorkspacePolicy{},
	); err == nil {
		t.Fatalf("workspace with file at home directory unexpectedly succeeded")
	}

	protectedManifest := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithManifest(Manifest{Files: []ManifestFile{{
			Path:    ".git/config",
			Content: []byte("bad"),
		}}}),
	)
	if _, err := protectedManifest.CreateWorkspace(
		context.Background(),
		"protected-manifest",
		codeexecutor.WorkspacePolicy{},
	); !isKind(err, ErrPathDenied) {
		t.Fatalf("protected manifest error = %v, want ErrPathDenied", err)
	}
}

func TestRuntimeDefaultsDescribeAndHelpers(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		func(r *Runtime) {
			r.outputMaxBytes = 0
			r.defaultTimeout = 0
		},
	)
	if rt.outputMaxBytes != defaultOutputMaxBytes || rt.defaultTimeout != defaultRunTimeout {
		t.Fatalf("defaults output=%d timeout=%s", rt.outputMaxBytes, rt.defaultTimeout)
	}
	defaultProfile := NewRuntime(WithPermissionProfile(PermissionProfile{}))
	if defaultProfile.profile.typ != profileManaged || defaultProfile.profile.network.Mode != NetworkRestricted {
		t.Fatalf("default normalized profile = %#v", defaultProfile.profile)
	}
	disabled := NewRuntime(WithPermissionProfile(DangerFullAccessProfile()))
	if got := disabled.Describe(); got.Isolation != "none" || !got.NetworkAllowed || got.ReadOnlyMount {
		t.Fatalf("disabled capabilities = %#v", got)
	}
	externalProfile := ExternalSandboxProfile(NetworkPolicy{Mode: NetworkEnabled})
	external := NewRuntime(WithPermissionProfile(externalProfile))
	if got := external.Describe(); got.Isolation != "external" || !got.NetworkAllowed || got.ReadOnlyMount {
		t.Fatalf("external capabilities = %#v", got)
	}
	if caps := backendCapabilities(rt.backend, rt.profile); !caps.Stdin || !caps.PerCommandGrants {
		t.Fatalf("backend capabilities = %#v", caps)
	}
	if got := sanitizeID("!!!"); len(got) != 16 {
		t.Fatalf("hashed sanitizeID length = %d, want 16", len(got))
	}
	if sanitizeID("user:a") == sanitizeID("user_a") {
		t.Fatalf("sanitized IDs collided for distinct raw IDs")
	}
	long := strings.Repeat("a", 160)
	if got := sanitizeID(long); len(got) != 113 || !strings.Contains(got, "-") {
		t.Fatalf("long sanitizeID = %q", got)
	}
	if !sameOrChild(string(os.PathSeparator), filepath.Join(string(os.PathSeparator), "tmp")) {
		t.Fatalf("root should contain /tmp")
	}
	if sameOrChild(filepath.Join(string(os.PathSeparator), "tmp", "a"), filepath.Join(string(os.PathSeparator), "tmp", "ab")) {
		t.Fatalf("sibling path matched as child")
	}
	firstLock := rt.runLock(codeexecutor.Workspace{ID: "lock"})
	secondLock := rt.runLock(codeexecutor.Workspace{ID: "lock"})
	if firstLock != secondLock {
		t.Fatalf("runLock did not reuse lock")
	}
	missingRoot := filepath.Join(t.TempDir(), "missing-root")
	if err := ensureNoSymlinkEscape(missingRoot, filepath.Join(missingRoot, "child")); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeFilesystemOperations(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "fs/ops", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "work/a.txt", Content: []byte("alpha")},
		{Path: "out/b.bin", Content: []byte{0, 1, 2}, Mode: 0o600},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if !collectedFile(files, "work/a.txt", "alpha") || !collectedFile(files, "out/b.bin", "\x00\x01\x02") {
		t.Fatalf("collected files = %#v", files)
	}

	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "input.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.StageDirectory(context.Background(), ws, host, "work/host", codeexecutor.StageOptions{}); !isKind(err, ErrPathDenied) {
		t.Fatalf("ungranted host stage error = %v, want ErrPathDenied", err)
	}
	relativeRoot := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(relativeRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	if err := os.MkdirAll("relative-host", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("relative-host", "input.txt"), []byte("relative"), 0o600); err != nil {
		t.Fatal(err)
	}
	relative := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths("relative-host")),
	)
	relativeWS, err := relative.CreateWorkspace(context.Background(), "fs/relative-stage", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := relative.StageDirectory(
		context.Background(),
		relativeWS,
		"relative-host",
		"work/relative",
		codeexecutor.StageOptions{},
	); !isKind(err, ErrPathDenied) {
		t.Fatalf("relative host stage error = %v, want ErrPathDenied", err)
	}

	granted := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths(host)),
	)
	grantedWS, err := granted.CreateWorkspace(context.Background(), "fs/stage", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := granted.StageDirectory(
		context.Background(),
		grantedWS,
		host,
		"work/host",
		codeexecutor.StageOptions{ReadOnly: true},
	); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(grantedWS.Path, "work", "host", "input.txt")
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o444 {
		t.Fatalf("staged file mode = %o, want 0444", got)
	}
	if err := os.Chmod(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(staged, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStageDirectoryWriteDenied(t *testing.T) {
	ctx := context.Background()
	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "input.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(ReadOnlyProfile().WithReadPaths(host)),
	)
	ws, err := rt.CreateWorkspace(ctx, "fs/stage-write-denied", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = rt.StageDirectory(ctx, ws, host, "work/staged", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("StageDirectory write denial = %v, want ErrPathDenied", err)
	}
}

func TestRuntimeStageInputsWorkspaceSkillAndErrors(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "stage/inputs", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "work/source.txt", Content: []byte("workspace")},
		{Path: "skills/demo/SKILL.md", Content: []byte("skill")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{
		{From: "workspace://work/source.txt", To: "work/copied.txt"},
		{From: "skill://demo/SKILL.md", To: "work/skill.md"},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(context.Background(), ws, []string{"work/copied.txt", "work/skill.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !collectedFile(files, "work/copied.txt", "workspace") ||
		!collectedFile(files, "work/skill.md", "skill") {
		t.Fatalf("staged files = %#v", files)
	}

	if err := rt.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{
		From: "unsupported://input.txt",
		To:   "work/input.txt",
	}}); err == nil || !strings.Contains(err.Error(), "unsupported input") {
		t.Fatalf("unsupported stage input error = %v", err)
	}

	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "host.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostRuntime := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithReadPaths(host)),
	)
	hostWS, err := hostRuntime.CreateWorkspace(context.Background(), "stage/host", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := hostRuntime.StageInputs(context.Background(), hostWS, []codeexecutor.InputSpec{{
		From: "host://" + host,
		Mode: "LINK",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hostWS.Path, "work", "inputs", filepath.Base(host), "host.txt")); err != nil {
		t.Fatalf("host input was not staged at default destination: %v", err)
	}
}

func TestStageInputsLockedAndHostInputErrorBranches(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	fileWorkspace := filepath.Join(t.TempDir(), "workspace-file")
	if err := os.WriteFile(fileWorkspace, []byte("not a workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := rt.stageInputsLocked(ctx, codeexecutor.Workspace{ID: "bad", Path: fileWorkspace}, nil)
	if err == nil {
		t.Fatalf("stageInputsLocked unexpectedly succeeded for file workspace")
	}

	ws, err := rt.CreateWorkspace(ctx, "stage/host-error", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "host.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = rt.stageHostInput(ctx, ws, codeexecutor.InputSpec{
		From: "host://" + host,
	}, "work/host")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("stageHostInput error = %v, want ErrPathDenied", err)
	}
}

func TestCollectOutputsLimitsAndTruncation(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "collect/outputs", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "out/a.txt", Content: []byte("alpha")},
		{Path: "out/b.txt", Content: []byte("bravo")},
		{Path: "out/blob.bin", Content: []byte("abcdef")},
	}); err != nil {
		t.Fatal(err)
	}

	limited, err := rt.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{
		Inline:   true,
		MaxFiles: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Files) != 1 || !limited.LimitsHit {
		t.Fatalf("limited manifest = %#v, want one file with limits hit", limited)
	}

	truncated, err := rt.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{
		Globs:         []string{"out/blob.bin"},
		Inline:        true,
		MaxFileBytes:  2,
		MaxTotalBytes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(truncated.Files) != 1 ||
		truncated.Files[0].Content != "ab" ||
		!truncated.Files[0].Truncated ||
		!truncated.LimitsHit {
		t.Fatalf("truncated manifest = %#v", truncated)
	}
	if truncated.Files[0].MIMEType != "application/octet-stream" {
		t.Fatalf("MIME type = %q, want application/octet-stream", truncated.Files[0].MIMEType)
	}

	_, err = rt.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{
		Globs:        []string{"out/blob.bin"},
		Save:         true,
		MaxFileBytes: 2,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot save truncated output file") {
		t.Fatalf("truncated save error = %v", err)
	}
}

func TestPathPolicyResolutionAndAccess(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "path/policy", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	root, rel, err := rt.resolveWorkspacePath(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if root != ws.Path || rel != "." {
		t.Fatalf("empty path resolved to (%q, %q), want workspace root", root, rel)
	}
	inside := filepath.Join(ws.Path, "work", "inside.txt")
	abs, rel, err := rt.resolveWorkspacePath(ws, inside)
	if err != nil {
		t.Fatal(err)
	}
	if abs != inside || rel != "work/inside.txt" {
		t.Fatalf("absolute inside path resolved to (%q, %q)", abs, rel)
	}
	if _, _, err := rt.resolveWorkspacePath(ws, "../escape.txt"); !isKind(err, ErrPathDenied) {
		t.Fatalf("relative escape error = %v, want ErrPathDenied", err)
	}
	if _, _, err := rt.resolveWorkspacePath(ws, filepath.Dir(ws.Path)); !isKind(err, ErrPathDenied) {
		t.Fatalf("absolute escape error = %v, want ErrPathDenied", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws.Path, "work", "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.resolveWorkspacePath(ws, "work/link/file.txt"); !isKind(err, ErrPathDenied) {
		t.Fatalf("symlink escape error = %v, want ErrPathDenied", err)
	}

	profile := WorkspaceWriteProfile().
		WithNoAccessPaths("work/secret").
		WithReadPaths(filepath.Join(ws.Path, "work", "public"))
	decision, err := rt.decidePath(profile, ws, "work/secret/token.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !decision.matched || decision.access != accessNone {
		t.Fatalf("secret decision = %#v, want no access match", decision)
	}
	decision, err = rt.decidePath(profile, ws, "work/public/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if decision.access != accessRead {
		t.Fatalf("public decision = %#v, want read access", decision)
	}

	invalid := WorkspaceWriteProfile()
	invalid.fileSystem.Rules = append(invalid.fileSystem.Rules, fileSystemRule{
		Kind: ruleGlob, Access: accessRead, Glob: "work/**",
	})
	if err := rt.checkRead(invalid, ws, "work/a.txt"); !isKind(err, ErrPolicyViolation) {
		t.Fatalf("invalid glob read rule error = %v, want ErrPolicyViolation", err)
	}

	if ok, err := rt.matchRule(ws, "work/a.txt", filepath.Join(ws.Path, "work", "a.txt"), fileSystemRule{}); err != nil || ok {
		t.Fatalf("empty path rule match = %v, %v; want false nil", ok, err)
	}
	if _, ok := specialRel(specialPath("missing")); ok {
		t.Fatalf("unknown special path unexpectedly resolved")
	}
	if got := pathSpecificity("./work/a/b"); got != 3 {
		t.Fatalf("path specificity = %d, want 3", got)
	}
	if got := accessPrecedence(fileSystemAccess("unknown")); got != 0 {
		t.Fatalf("unknown access precedence = %d, want 0", got)
	}
	if accessCanRead(accessNone) || accessCanWrite(accessRead) {
		t.Fatalf("access helpers allowed insufficient permissions")
	}
	if target := ruleTarget(fileSystemRule{Kind: fileSystemRuleKind("mystery")}); !strings.Contains(target, "mystery") {
		t.Fatalf("unexpected unknown rule target %q", target)
	}
	if spec, err := ruleSpecificity(ws, fileSystemRule{Kind: ruleSpecial, Special: specialPath("missing")}); err != nil || spec != 0 {
		t.Fatalf("unknown special specificity = %d, %v", spec, err)
	}
	if spec, err := ruleSpecificity(ws, fileSystemRule{Kind: rulePath}); err != nil || spec != 0 {
		t.Fatalf("empty path specificity = %d, %v", spec, err)
	}
	if ok, err := matchSpecial(ws, filepath.Join(ws.Path, "work"), specialPath("missing")); err != nil || ok {
		t.Fatalf("unknown special match = %v, %v; want false nil", ok, err)
	}
	if matches, err := rt.deniedReadMatches(WorkspaceWriteProfile(), ws); err != nil || len(matches) != 0 {
		t.Fatalf("deniedReadMatches without deny rules = %#v, %v", matches, err)
	}
}

func TestSandboxErrorsAndLimitedBuffer(t *testing.T) {
	err := deniedf(ErrPathDenied, "read", "work/secret", "blocked")
	if !isKind(err, ErrPathDenied) || isKind(errors.New("plain"), ErrPathDenied) {
		t.Fatalf("IsKind did not classify sandbox errors correctly")
	}
	if msg := err.Error(); !strings.Contains(msg, "PathDenied read work/secret") {
		t.Fatalf("sandboxError message = %q", msg)
	}
	var nilErr *sandboxError
	if nilErr.Error() != "" || nilErr.Unwrap() != nil {
		t.Fatalf("nil sandboxError methods returned non-empty values")
	}

	buf := newLimitedBuffer(3)
	if n, err := buf.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("limited buffer write n=%d err=%v", n, err)
	}
	if got := buf.String(); got != "abc\n[truncated]\n" {
		t.Fatalf("limited buffer string = %q", got)
	}
	full := newLimitedBuffer(3)
	if _, err := full.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := full.Write([]byte("d")); err != nil {
		t.Fatal(err)
	}
	if got := full.String(); got != "abc\n[truncated]\n" {
		t.Fatalf("full limited buffer string = %q", got)
	}
	if !buf.Truncated() || (*limitedBuffer)(nil).Truncated() {
		t.Fatalf("limited buffer truncation flag mismatch")
	}
	disabled := newLimitedBuffer(0)
	if _, err := disabled.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if got := disabled.String(); got != "\n[truncated]\n" {
		t.Fatalf("zero-size buffer string = %q", got)
	}
	if (*limitedBuffer)(nil).String() != "" {
		t.Fatalf("nil limited buffer string should be empty")
	}

	if err := killProcessGroup(nil); err != nil {
		t.Fatal(err)
	}
	backend := backendError(ErrSetupFailed, "test-backend", errors.New("setup"))
	if !strings.Contains(backend.Error(), "backend=test-backend") ||
		!errors.Is(backend, errors.Unwrap(backend)) {
		t.Fatalf("backend error = %v", backend)
	}
}

func TestEnvironmentAndProfileBranches(t *testing.T) {
	t.Setenv("SANDBOX_SECRET_TOKEN", "secret")
	rt := NewRuntime(
		WithPermissionProfile(PermissionProfile{typ: profileDisabled}),
		WithShellEnvironmentPolicy(ShellEnvironmentPolicy{
			Inherit:              ShellEnvironmentPolicyInheritNone,
			ApplyDefaultExcludes: true,
			Set: map[string]string{
				"SANDBOX_VISIBLE": "yes",
			},
		}),
	)
	ws := codeexecutor.Workspace{ID: "env", Path: t.TempDir()}
	env := rt.buildEnvironment(ws, codeexecutor.RunProgramSpec{})
	if hasEnvPrefix(env, "SANDBOX_SECRET_TOKEN=") {
		t.Fatalf("secret env should have been redacted from inherited env: %v", env)
	}
	if !hasEnv(env, "SANDBOX_VISIBLE=yes") {
		t.Fatalf("set env missing from %v", env)
	}
	redacted := redactEnvironment([]string{"TOKEN=value", "PLAIN=value", "MALFORMED"})
	if !hasEnv(redacted, "TOKEN=<redacted>") ||
		!hasEnv(redacted, "PLAIN=value") ||
		!hasString(redacted, "MALFORMED") {
		t.Fatalf("redacted env = %v", redacted)
	}
	if envNameMatch("", "ANY") || envNameMatch("[", "ANY") {
		t.Fatalf("empty or invalid env pattern matched")
	}
	if !envNameMatchesAny([]string{"nope", "plain"}, "PLAIN") {
		t.Fatalf("envNameMatchesAny did not match case-insensitively")
	}

	p := WorkspaceWriteProfile().
		WithReadPaths("", "work/read").
		WithWritePaths("", "work/write").
		WithNoAccessPaths("", "work/none").
		WithNoAccessGlobs("", "work/*.secret")
	network := NetworkPolicy{Mode: NetworkEnabled}
	p = applyAdditionalPermissions(p, AdditionalPermissions{Network: &network})
	if p.network.Mode != NetworkEnabled {
		t.Fatalf("additional network permission not applied")
	}
	if len(p.fileSystem.Rules) < len(WorkspaceWriteProfile().fileSystem.Rules)+4 {
		t.Fatalf("empty profile rules were not skipped as expected: %#v", p.fileSystem.Rules)
	}
}

func TestFilesystemHelperBranches(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child", "input.txt")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("0123456789"), 0o640); err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithWritePaths(root)
	if !hostPathHasRule(profile, child, accessRead) {
		t.Fatalf("absolute child should inherit read-compatible grant from parent")
	}
	if !hostPathHasRule(profile, child, accessWrite) {
		t.Fatalf("absolute child should inherit write grant from parent")
	}
	if hostPathHasRule(WorkspaceWriteProfile().WithNoAccessPaths(root), child, accessRead) {
		t.Fatalf("no-access rule should not satisfy read grant")
	}
	if hostPathHasRule(WorkspaceWriteProfile().WithReadPaths(root), child, accessWrite) {
		t.Fatalf("read grant should not satisfy write access")
	}
	if hostPathHasRule(WorkspaceWriteProfile().WithReadPaths("relative"), child, accessRead) {
		t.Fatalf("relative rule should not satisfy host path grant")
	}
	if hostPathHasRule(WorkspaceWriteProfile().WithReadPaths(root), "relative", accessRead) {
		t.Fatalf("relative target should not satisfy host path grant")
	}

	copiedFile := filepath.Join(t.TempDir(), "nested", "copy.txt")
	if err := copyPath(child, copiedFile); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := readFileLimited(copiedFile, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "0123" || !truncated {
		t.Fatalf("readFileLimited = %q truncated=%v, want truncated 0123", data, truncated)
	}

	copiedDir := filepath.Join(t.TempDir(), "dir-copy")
	if err := copyPath(root, copiedDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(copiedDir, "child", "input.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("copied file mode = %o, want 0640", info.Mode().Perm())
	}

	for _, ref := range []string{"", "   ", "/"} {
		if got := inputName(ref); got != "input" {
			t.Fatalf("inputName(%q) = %q, want input", ref, got)
		}
	}
	if got := inputName("artifact://uploads/report.txt@7"); got != "report.txt" {
		t.Fatalf("artifact input name = %q, want report.txt", got)
	}
	if got := inputName("artifact://@bad"); got == "" || got == "input" {
		t.Fatalf("invalid artifact input name = %q, want sanitized fallback", got)
	}
}

func TestFilesystemErrorBranches(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ws, err := rt.CreateWorkspace(ctx, "fs/errors", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "work", "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "../escape.txt",
		Content: []byte("escape"),
	}})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("PutFiles escape error = %v, want ErrPathDenied", err)
	}

	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "input.txt"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = rt.StageDirectory(ctx, ws, host, "../escape", codeexecutor.StageOptions{})
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("StageDirectory escape error = %v, want ErrPathDenied", err)
	}

	fileWorkspace := filepath.Join(t.TempDir(), "workspace-file")
	if err := os.WriteFile(fileWorkspace, []byte("not a workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	badWS := codeexecutor.Workspace{ID: "bad", Path: fileWorkspace}
	if _, err := rt.Collect(ctx, badWS, []string{"."}); err == nil {
		t.Fatalf("Collect unexpectedly succeeded for file workspace")
	}
	if _, err := rt.CollectOutputs(ctx, badWS, codeexecutor.OutputSpec{}); err == nil {
		t.Fatalf("CollectOutputs unexpectedly succeeded for file workspace")
	}

	restrictive := PermissionProfile{typ: profileManaged}
	err = rt.stageWorkspaceRelativePath(ws, restrictive, "work/source.txt", "work/copy.txt")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("stageWorkspaceRelativePath read error = %v, want ErrPathDenied", err)
	}
	err = rt.stageWorkspaceRelativePath(ws, ReadOnlyProfile(), "work/source.txt", "work/copy.txt")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("stageWorkspaceRelativePath write error = %v, want ErrPathDenied", err)
	}
	err = rt.stageWorkspaceRelativePath(ws, DangerFullAccessProfile(), "../escape.txt", "work/copy.txt")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("stageWorkspaceRelativePath source escape = %v, want ErrPathDenied", err)
	}
	err = rt.stageWorkspaceRelativePath(ws, DangerFullAccessProfile(), "work/source.txt", "../escape.txt")
	if !isKind(err, ErrPathDenied) {
		t.Fatalf("stageWorkspaceRelativePath destination escape = %v, want ErrPathDenied", err)
	}

	rel, _, _, skip, err := rt.resolveCollectMatch(
		DangerFullAccessProfile(),
		ws,
		filepath.Join(filepath.Dir(ws.Path), "outside.txt"),
	)
	if err != nil || !skip || rel != "" {
		t.Fatalf("outside collect match rel=%q skip=%v err=%v, want skip", rel, skip, err)
	}

	if _, _, _, _, err := rt.resolveCollectMatch(
		DangerFullAccessProfile(),
		ws,
		filepath.Join(ws.Path, "work", "missing.txt"),
	); err == nil {
		t.Fatalf("resolveCollectMatch unexpectedly succeeded for missing file")
	}
	if rel, _, _, skip, err := rt.resolveCollectMatch(
		DangerFullAccessProfile(),
		ws,
		filepath.Join(ws.Path, "work"),
	); err != nil || !skip || rel != "" {
		t.Fatalf("directory collect match rel=%q skip=%v err=%v, want skip", rel, skip, err)
	}

	missingLink := filepath.Join(ws.Path, "work", "missing-link.txt")
	if err := os.Symlink("missing-target.txt", missingLink); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := rt.resolveCollectMatch(DangerFullAccessProfile(), ws, missingLink); err == nil {
		t.Fatalf("resolveCollectMatch unexpectedly succeeded for dangling symlink")
	}

	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideLink := filepath.Join(ws.Path, "work", "outside-link.txt")
	if err := os.Symlink(outsideFile, outsideLink); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := rt.resolveCollectMatch(DangerFullAccessProfile(), ws, outsideLink); !isKind(err, ErrPathDenied) {
		t.Fatalf("resolveCollectMatch symlink escape error = %v, want ErrPathDenied", err)
	}

	ref, consumed, skip, err := rt.collectOutputMatch(
		ctx,
		DangerFullAccessProfile(),
		ws,
		filepath.Join(ws.Path, "work", "source.txt"),
		codeexecutor.OutputSpec{Inline: true},
		10,
		-1,
	)
	if err != nil || skip || consumed != 0 || !ref.Truncated {
		t.Fatalf("negative budget output ref=%#v consumed=%d skip=%v err=%v", ref, consumed, skip, err)
	}
}

func TestFilesystemSymlinkAndCopyHelperBranches(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(ctx, "fs/symlink-helpers", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	realRoot := t.TempDir()
	linkedRoot := filepath.Join(t.TempDir(), "workspace-root")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("symlink root setup failed: %v", err)
	}
	linkedRuntime := NewRuntime(WithWorkspaceRoot(linkedRoot))
	linkedWS, err := linkedRuntime.CreateWorkspace(ctx, "fs/canonical-root", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := linkedRuntime.PutFiles(ctx, linkedWS, []codeexecutor.PutFile{{
		Path:    "work/canonical.txt",
		Content: []byte("ok"),
	}}); err != nil {
		t.Fatalf("put through symlink workspace root error = %v", err)
	}

	outside := t.TempDir()
	if err := rt.checkWorkspaceWriteTarget(WorkspaceWriteProfile(), ws, filepath.Join(outside, "x.txt")); !isKind(err, ErrPathDenied) {
		t.Fatalf("outside write target error = %v, want ErrPathDenied", err)
	}

	directLink := filepath.Join(ws.Path, "work", "direct-link.txt")
	if err := os.Symlink(filepath.Join(ws.Path, "work", "target.txt"), directLink); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkWorkspaceWriteTarget(WorkspaceWriteProfile(), ws, directLink); !isKind(err, ErrPathDenied) {
		t.Fatalf("direct symlink write target error = %v, want ErrPathDenied", err)
	}

	parentLink := filepath.Join(ws.Path, "work", "parent-link")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkWorkspaceWriteTarget(WorkspaceWriteProfile(), ws, filepath.Join(parentLink, "new.txt")); !isKind(err, ErrPathDenied) {
		t.Fatalf("parent symlink escape error = %v, want ErrPathDenied", err)
	}

	insideTarget := filepath.Join(ws.Path, "work", "inside")
	if err := os.MkdirAll(insideTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	insideLink := filepath.Join(ws.Path, "work", "inside-link")
	if err := os.Symlink(insideTarget, insideLink); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkWorkspaceWriteTarget(WorkspaceWriteProfile(), ws, filepath.Join(insideLink, "new.txt")); err != nil {
		t.Fatalf("inside parent symlink write target error = %v", err)
	}

	resolved, changed, err := resolvePotentialSymlinkTarget(directLink)
	if err != nil || !changed || resolved != filepath.Join(ws.Path, "work", "target.txt") {
		t.Fatalf("direct symlink resolved=%q changed=%v err=%v", resolved, changed, err)
	}
	resolved, changed, err = resolvePotentialSymlinkTarget(filepath.Join(insideLink, "nested.txt"))
	if err != nil || !changed || resolved != filepath.Join(insideTarget, "nested.txt") {
		t.Fatalf("parent symlink resolved=%q changed=%v err=%v", resolved, changed, err)
	}

	if err := copyPath(filepath.Join(outside, "missing.txt"), filepath.Join(t.TempDir(), "copy.txt")); err == nil {
		t.Fatalf("copyPath unexpectedly succeeded for missing source")
	}
	if err := copyPathWithValidator(filepath.Join(outside, "missing.txt"), filepath.Join(t.TempDir(), "copy.txt"), func(string) error {
		return nil
	}); err == nil {
		t.Fatalf("copyPathWithValidator unexpectedly succeeded for missing source")
	}
	source := filepath.Join(outside, "source.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolvePotentialSymlinkTarget(filepath.Join(source, "child.txt")); err == nil {
		t.Fatalf("resolvePotentialSymlinkTarget unexpectedly succeeded below file path")
	}
	fileParent := filepath.Join(t.TempDir(), "file-parent")
	if err := os.WriteFile(fileParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyPath(source, filepath.Join(fileParent, "copy.txt")); err == nil {
		t.Fatalf("copyPath unexpectedly succeeded with file as destination parent")
	}
	srcDir := filepath.Join(outside, "srcdir")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "nested.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	dstFile := filepath.Join(t.TempDir(), "dst-file")
	if err := os.WriteFile(dstFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyPath(srcDir, dstFile); err == nil {
		t.Fatalf("copyPath unexpectedly copied directory over file")
	}
	if err := copyPathWithValidator(source, filepath.Join(t.TempDir(), "copy.txt"), func(string) error {
		return deniedf(ErrPathDenied, "write", "copy.txt", "blocked")
	}); !isKind(err, ErrPathDenied) {
		t.Fatalf("copyPathWithValidator validation error = %v, want ErrPathDenied", err)
	}
	sourceLink := filepath.Join(outside, "source-link.txt")
	if err := os.Symlink(source, sourceLink); err != nil {
		t.Fatal(err)
	}
	if err := copyPathWithValidator(sourceLink, filepath.Join(t.TempDir(), "copy.txt"), func(string) error {
		return nil
	}); !isKind(err, ErrPathDenied) {
		t.Fatalf("copyPathWithValidator source symlink error = %v, want ErrPathDenied", err)
	}

	if err := copyFile(filepath.Join(outside, "missing.txt"), filepath.Join(t.TempDir(), "copy.txt"), 0o600); err == nil {
		t.Fatalf("copyFile unexpectedly succeeded for missing source")
	}
	if err := copyFile(source, filepath.Join(fileParent, "copy.txt"), 0o600); err == nil {
		t.Fatalf("copyFile unexpectedly succeeded with file as destination parent")
	}
	if err := copyFile(source, srcDir, 0o600); err == nil {
		t.Fatalf("copyFile unexpectedly opened directory destination")
	}
	if err := copyFile(srcDir, filepath.Join(t.TempDir(), "dir-as-file"), 0o600); err == nil {
		t.Fatalf("copyFile unexpectedly copied directory source")
	}
	if err := copyFileWithValidator(filepath.Join(outside, "missing.txt"), filepath.Join(t.TempDir(), "copy.txt"), 0o600, func(string) error {
		return nil
	}); err == nil {
		t.Fatalf("copyFileWithValidator unexpectedly succeeded for missing source")
	}
	if err := copyFileWithValidator(source, filepath.Join(fileParent, "copy.txt"), 0o600, func(string) error {
		return nil
	}); err == nil {
		t.Fatalf("copyFileWithValidator unexpectedly succeeded with file as destination parent")
	}
	if err := copyFileWithValidator(source, srcDir, 0o600, func(string) error {
		return nil
	}); err == nil {
		t.Fatalf("copyFileWithValidator unexpectedly opened directory destination")
	}
	if err := copyFileWithValidator(srcDir, filepath.Join(t.TempDir(), "dir-as-file"), 0o600, func(string) error {
		return nil
	}); err == nil {
		t.Fatalf("copyFileWithValidator unexpectedly copied directory source")
	}

	if err := writeFileAtomically(filepath.Join(fileParent, "out.txt"), []byte("out"), 0o600); err == nil {
		t.Fatalf("writeFileAtomically unexpectedly succeeded with file as parent")
	}
	if err := writeFileAtomically(filepath.Join(t.TempDir(), "bad\x00name"), []byte("out"), 0o600); err == nil {
		t.Fatalf("writeFileAtomically unexpectedly succeeded with invalid temp pattern")
	}
	existingDir := filepath.Join(t.TempDir(), "existing-dir")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomically(existingDir, []byte("out"), 0o600); err == nil {
		t.Fatalf("writeFileAtomically unexpectedly renamed file over directory")
	}
	if _, _, err := readFileLimited(filepath.Join(outside, "missing.txt"), 4); err == nil {
		t.Fatalf("readFileLimited unexpectedly succeeded for missing file")
	}
}

func TestPinnedArtifactVersionEdgeBranches(t *testing.T) {
	version := 7
	md := codeexecutor.WorkspaceMetadata{
		Inputs: []codeexecutor.InputRecord{
			{From: "artifact://old.txt@1", To: "work/other.txt", Resolved: "old.txt", Version: &version},
			{From: "artifact://report.txt@3", To: "work/report.txt", Version: &version},
		},
	}
	if got := pinnedArtifactVersion(md, "", "work/report.txt"); got != nil {
		t.Fatalf("blank artifact name returned version %v", *got)
	}
	if got := pinnedArtifactVersion(md, "report.txt", ""); got != nil {
		t.Fatalf("blank destination returned version %v", *got)
	}
	got := pinnedArtifactVersion(md, "report.txt", "work/report.txt")
	if got == nil || *got != version {
		t.Fatalf("pinned artifact version = %v, want %d", got, version)
	}
	if got := pinnedArtifactVersion(md, "missing.txt", "work/report.txt"); got != nil {
		t.Fatalf("missing artifact returned version %v", *got)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func collectedFile(files []codeexecutor.File, name string, content string) bool {
	for _, f := range files {
		if f.Name == name && f.Content == content {
			return true
		}
	}
	return false
}
