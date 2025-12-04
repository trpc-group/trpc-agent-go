//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const (
	permDenied        = "permission denied"
	unsupportedLangJS = "unsupported language: javascript"
)

func TestRuntime_RunProgram_Basic(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx,
		"rt-basic",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Stage a file
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{
		{
			Path:    "hello.txt",
			Content: []byte("hello runtime\n"),
			Mode:    0o644,
		},
	})
	require.NoError(t, err)

	// Run bash to print the file
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-c", "cat hello.txt"},
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hello runtime")
}

func TestRuntime_ExecuteInline_PythonOrSkip(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	rt := local.NewRuntime("")
	ctx := context.Background()
	blocks := []codeexecutor.CodeBlock{
		{Language: "python", Code: "print('hi v2')"},
	}
	res, err := rt.ExecuteInline(ctx, "rt-inline", blocks, 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hi v2")
}

func TestRuntime_PutDirectory_And_Collect(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Build a source directory with nested files.
	src := t.TempDir()
	sub := filepath.Join(src, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "a.txt"), []byte("alpha"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "img.bin"), []byte{0x1, 0x2, 0x3}, 0o644,
	))

	// Copy directory into workspace under dst/.
	require.NoError(t, rt.PutDirectory(ctx, ws, src, "dst"))

	// Collect using doublestar patterns.
	files, err := rt.Collect(ctx, ws, []string{
		"dst/**/*.txt", "dst/*.bin",
	})
	require.NoError(t, err)
	require.Len(t, files, 2)
	// Names are relative to workspace root.
	require.Contains(t, []string{files[0].Name, files[1].Name},
		"dst/sub/a.txt")
	require.Contains(t, []string{files[0].Name, files[1].Name},
		"dst/img.bin")
}

func TestRuntime_PutSkill_ReadOnly(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntimeWithOptions(
		"", local.WithReadOnlyStagedSkill(true),
	)
	ctx := context.Background()
	// Use a unique execID per run to avoid leftover perms between runs.
	ws, err := rt.CreateWorkspace(
		ctx, "rt-skill-"+t.Name(), codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare a skill directory.
	skillDir := t.TempDir()
	f := filepath.Join(skillDir, "script.sh")
	require.NoError(t, os.WriteFile(f, []byte("echo ok"), 0o755))

	// Stage skill under target path and make it read-only.
	// On some hosts, staging may hit transient permission policies;
	// if so, skip to avoid environment-specific flakiness.
	if err := rt.StageDirectory(ctx, ws, skillDir, "tool",
		codeexecutor.StageOptions{ReadOnly: true}); err != nil {
		if strings.Contains(err.Error(), permDenied) {
			t.Skip("skip due to permission policy: " + err.Error())
		}
		require.NoError(t, err)
	}

	// Attempt to append to the file should fail due to a-w.
	target := filepath.Join(ws.Path, "tool", "script.sh")
	ff, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0)
	require.Error(t, err)
	if ff != nil {
		_ = ff.Close()
	}

	// Still executable: run it.
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "./tool/script.sh"},
		Timeout: short,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Contains(t, res.Stdout, "ok")
}

func TestRuntime_PutFiles_PathEscapeRejected(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-escape", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "../evil.txt",
		Content: []byte("x"),
		Mode:    0,
	}})
	require.Error(t, err)
}

func TestRuntime_RunProgram_EnvAndStdin(t *testing.T) {
	const short = 5 * time.Second

	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-env", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-lc", "cat; echo $FOO"},
		Env:  map[string]string{"FOO": "BAR"},
		// Provide data via stdin so cat prints it.
		Stdin:   "HELLO\n",
		Timeout: short,
	})
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "HELLO")
	require.Contains(t, res.Stdout, "BAR")
}

func TestRuntime_RunProgram_Timeout(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-timeout", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Intentionally time out a sleeping command.
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "sleep 1; echo late"},
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, res.TimedOut)
}

func TestRuntime_RunProgram_DefaultTimeoutUsed(t *testing.T) {
	// When Timeout is zero, runtime uses defaultTimeout(); ensure
	// the call path executes successfully without timing out.
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-default", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "true"},
		Timeout: 0,
	})
	require.NoError(t, err)
	require.False(t, res.TimedOut)
}

func TestRuntime_PutDirectory_EmptyHostPath_Error(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-empty-host", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.PutDirectory(ctx, ws, "", "dst")
	require.Error(t, err)
}

func TestRuntime_RunProgram_NonexistentCommandExitCode(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-missing-cmd", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "this-command-should-not-exist",
		Args:    []string{"--version"},
		Timeout: 500 * time.Millisecond,
	})
	require.NoError(t, err)
	// Non-ExitError maps to -1 per implementation.
	require.Equal(t, -1, res.ExitCode)
}

func TestRuntime_StageDirectory_Writable_WhenNotReadOnly(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-rw", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare a temp dir with a file.
	src := t.TempDir()
	f := filepath.Join(src, "f.txt")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))

	require.NoError(t, rt.StageDirectory(
		ctx, ws, src, "tool", codeexecutor.StageOptions{ReadOnly: false},
	))

	target := filepath.Join(ws.Path, "tool", "f.txt")
	// Append should succeed when not read-only.
	ff, err := os.OpenFile(
		target, os.O_WRONLY|os.O_APPEND, 0,
	)
	require.NoError(t, err)
	_, _ = ff.Write([]byte("y"))
	_ = ff.Close()
}

func TestRuntime_StageDirectory_ReadOnly_EmptyDir(t *testing.T) {
	// Exercise makeTreeReadOnly without file permission churn by
	// staging an empty directory and enabling read-only flag.
	rt := local.NewRuntimeWithOptions(
		"", local.WithReadOnlyStagedSkill(true),
	)
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-ro-empty", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	empty := t.TempDir()
	err = rt.StageDirectory(ctx, ws, empty, "tool",
		codeexecutor.StageOptions{ReadOnly: true})
	if err != nil && strings.Contains(err.Error(), permDenied) {
		t.Skip("skip due to permission policy: " + err.Error())
	}
	require.NoError(t, err)
}

func TestRuntime_ExecuteInline_Bash(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	blocks := []codeexecutor.CodeBlock{{
		Language: "bash",
		Code:     "echo hi-bash",
	}}
	res, err := rt.ExecuteInline(
		ctx, "rt-inline-bash", blocks, 5*time.Second,
	)
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "hi-bash")
}

func TestRuntime_ExecuteInline_InvalidLanguageAndValid(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	blocks := []codeexecutor.CodeBlock{
		{Language: "javascript", Code: "console.log('x')"},
		{Language: "bash", Code: "echo ok"},
	}
	res, err := rt.ExecuteInline(
		ctx, "rt-inline-mixed", blocks, 2*time.Second,
	)
	require.NoError(t, err)
	require.Contains(t, res.Stderr, unsupportedLangJS)
	require.Contains(t, res.Stdout, "ok")
}

func TestRuntime_Collect_EmptyPatterns(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-empty", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	files, err := rt.Collect(ctx, ws, nil)
	require.NoError(t, err)
	require.Nil(t, files)
}

func TestRuntime_Collect_PathTraversalFiltered(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-filter", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Write a file outside the workspace root.
	outside := filepath.Join(filepath.Dir(ws.Path), "ext.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	// Attempt to collect via traversal; should be filtered out.
	files, err := rt.Collect(ctx, ws, []string{"../*.txt"})
	require.NoError(t, err)
	if len(files) > 0 {
		for _, f := range files {
			require.NotEqual(t, "ext.txt", f.Name)
		}
	}
}

func TestRuntime_Collect_SymlinkEscapeFiltered(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-symlink", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Outside file and a symlink inside workspace pointing to it.
	outside := filepath.Join(filepath.Dir(ws.Path), "out.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	link := filepath.Join(ws.Path, codeexecutor.DirWork, "link")
	require.NoError(t, os.MkdirAll(
		filepath.Dir(link), 0o755,
	))
	require.NoError(t, os.Symlink(outside, link))

	files, err := rt.Collect(
		ctx, ws, []string{filepath.Join(codeexecutor.DirWork, "*")},
	)
	require.NoError(t, err)
	for _, f := range files {
		require.NotEqual(t, "work/link", f.Name)
	}
}

func TestRuntime_Collect_DedupOverlappingGlobs(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-collect-dedup", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Create files under dst/ and dst/sub/.
	require.NoError(t, rt.PutFiles(ctx, ws, []codeexecutor.PutFile{
		{Path: "dst/a.txt", Content: []byte("a"), Mode: 0o644},
		{Path: "dst/sub/b.txt", Content: []byte("b"), Mode: 0o644},
	}))

	files, err := rt.Collect(ctx, ws, []string{
		"dst/*.txt", "dst/**/*.txt",
	})
	require.NoError(t, err)
	// a.txt matched by both; ensure dedup keeps it once.
	countA := 0
	for _, f := range files {
		if f.Name == "dst/a.txt" {
			countA++
		}
	}
	require.Equal(t, 1, countA)
}

func TestRuntime_Collect_EnvPrefixes(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-collect-env", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	const name = "env.txt"
	require.NoError(t, rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    filepath.Join(codeexecutor.DirOut, name),
		Content: []byte("v"),
		Mode:    0o644,
	}}))

	files, err := rt.Collect(
		ctx, ws, []string{"$OUTPUT_DIR/" + name},
	)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, filepath.Join(codeexecutor.DirOut, name),
		files[0].Name)
}

func TestRuntime_PutFiles_EmptyPathError(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-empty-path", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "",
		Content: []byte("x"),
		Mode:    0,
	}})
	require.Error(t, err)
}

func TestRuntime_PutFiles_DefaultMode(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-defmode", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Mode 0 should fall back to default (0644).
	const p = "a/b/c.txt"
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    p,
		Content: []byte("hi"),
		Mode:    0,
	}})
	require.NoError(t, err)
	st, err := os.Stat(filepath.Join(ws.Path, p))
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), st.Mode().Perm())
}

func TestRuntime_RunProgram_InjectsWorkspaceEnvs(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-env-inject", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Echo the injected env variables.
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "bash",
		Args: []string{
			"-lc",
			"echo $WORKSPACE_DIR; echo $SKILLS_DIR; echo $WORK_DIR; " +
				"echo $OUTPUT_DIR; echo $RUN_DIR",
		},
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)
	// All must be set and point within workspace.
	out := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	require.GreaterOrEqual(t, len(out), 5)
	require.Equal(t, ws.Path, out[0])
	require.Equal(t, filepath.Join(ws.Path, codeexecutor.DirSkills), out[1])
	require.Equal(t, filepath.Join(ws.Path, codeexecutor.DirWork), out[2])
	require.Equal(t, filepath.Join(ws.Path, codeexecutor.DirOut), out[3])
	require.True(t, strings.HasPrefix(out[4], filepath.Join(ws.Path,
		codeexecutor.DirRuns)))
}

func TestRuntime_StageInputs_ArtifactAndLinks(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-inputs", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare artifact service in context and save an artifact.
	svc := inmemory.NewService()
	actx := codeexecutor.WithArtifactService(ctx, svc)
	actx = codeexecutor.WithArtifactSession(
		actx, artifact.SessionInfo{AppName: "a", UserID: "u", SessionID: "s"},
	)
	_, err = codeexecutor.SaveArtifactHelper(
		actx, "a.txt", []byte("AX"), "text/plain",
	)
	require.NoError(t, err)

	// Stage artifact without explicit to; uses default path.
	err = rt.StageInputs(actx, ws, []codeexecutor.InputSpec{{
		From: "artifact://a.txt",
		Mode: "copy",
	}})
	require.NoError(t, err)
	// Default target is work/inputs/<name>.
	target := filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "a.txt",
	)
	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "AX", string(b))

	// host:// path with link mode creates a symlink.
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "f.txt"), []byte("Z"), 0o644,
	))
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "host://" + src,
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "linkdir"),
		Mode: "link",
	}})
	require.NoError(t, err)
	lpath := filepath.Join(ws.Path, codeexecutor.DirWork, "inputs",
		"linkdir")
	st, err := os.Lstat(lpath)
	require.NoError(t, err)
	require.True(t, st.Mode()&os.ModeSymlink != 0)

	// workspace:// copy mode from within the workspace.
	srcFile := filepath.Join(ws.Path, "src.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("W"), 0o644))
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "workspace://src.txt",
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "dst.txt"),
		Mode: "copy",
	}})
	require.NoError(t, err)
	b, err = os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "dst.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "W", string(b))
}

func TestRuntime_CreateWorkspace_AutoInputsHost(t *testing.T) {
	host := t.TempDir()
	hfile := filepath.Join(host, "auto.txt")
	require.NoError(t, os.WriteFile(hfile, []byte("V"), 0o644))

	rt := local.NewRuntimeWithOptions(
		"", local.WithInputsHostBase(host),
	)
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-auto-inputs", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	data, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "auto.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "V", string(data))
}

func TestRuntime_StageInputs_InvalidScheme_Error(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-invalid", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "unknown://path",
		Mode: "copy",
	}})
	require.Error(t, err)
}

func TestRuntime_CollectOutputs_SaveAndInline(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-collect-out", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Create some files under out/.
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws.Path, codeexecutor.DirOut), 0o755,
	))
	small := filepath.Join(ws.Path, codeexecutor.DirOut, "a.txt")
	large := filepath.Join(ws.Path, codeexecutor.DirOut, "b.bin")
	require.NoError(t, os.WriteFile(small, []byte("ok"), 0o644))
	// Large file to exercise per-file cap.
	big := bytes.Repeat([]byte{'x'}, 1024)
	require.NoError(t, os.WriteFile(large, big, 0o644))

	// Attach artifact service to save outputs.
	svc := inmemory.NewService()
	actx := codeexecutor.WithArtifactService(ctx, svc)
	actx = codeexecutor.WithArtifactSession(
		actx, artifact.SessionInfo{AppName: "a", UserID: "u", SessionID: "s"},
	)
	mf, err := rt.CollectOutputs(actx, ws, codeexecutor.OutputSpec{
		Globs:         []string{filepath.Join(codeexecutor.DirOut, "*")},
		Inline:        true,
		Save:          true,
		NameTemplate:  "prefix-",
		MaxFiles:      2,
		MaxFileBytes:  16,
		MaxTotalBytes: 64,
	})
	require.NoError(t, err)
	require.Len(t, mf.Files, 2)
	// Should set names and inline content for small file.
	var sawSmall bool
	for _, f := range mf.Files {
		if f.Name == "out/a.txt" {
			sawSmall = true
			require.NotEmpty(t, f.Content)
			require.Equal(t, "prefix-"+f.Name, f.SavedAs)
		}
	}
	require.True(t, sawSmall)
	require.True(t, mf.LimitsHit)
}

func TestRuntime_CollectOutputs_EnvPrefixes(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-collect-env-out", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	require.NoError(t, os.MkdirAll(
		filepath.Join(ws.Path, codeexecutor.DirOut), 0o755,
	))
	target := filepath.Join(ws.Path, codeexecutor.DirOut, "x.txt")
	require.NoError(t, os.WriteFile(target, []byte("ok"), 0o644))

	mf, err := rt.CollectOutputs(
		ctx, ws, codeexecutor.OutputSpec{
			Globs:  []string{"$OUTPUT_DIR/x.txt"},
			Inline: true,
		},
	)
	require.NoError(t, err)
	require.Len(t, mf.Files, 1)
	require.Equal(t, "out/x.txt", mf.Files[0].Name)
	require.Equal(t, "ok", mf.Files[0].Content)
}

func TestRuntime_StageInputs_HostCopy(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-hostcopy", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	src := t.TempDir()
	sub := filepath.Join(src, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "x.txt"), []byte("hi"), 0o644,
	))

	// Copy entire dir under work/inputs/d.
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "host://" + src,
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "d"),
		Mode: "copy",
	}})
	require.NoError(t, err)
	// Verify presence of the file in destination.
	b, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "sub", "x.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "hi", string(b))
}

func TestRuntime_StageInputs_SkillCopyAndLink(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-skill", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Create a staged skill file under skills/.
	skillDir := filepath.Join(ws.Path, codeexecutor.DirSkills, "tool")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "k.txt"), []byte("ok"), 0o644,
	))

	// Copy from skill:// to workspace path.
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "skill://tool/k.txt",
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "kk.txt"),
		Mode: "copy",
	}})
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "kk.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "ok", string(b))

	// Link mode produces a symlink at destination.
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "skill://tool/k.txt",
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "lk.txt"),
		Mode: "link",
	}})
	require.NoError(t, err)
	st, err := os.Lstat(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "lk.txt",
	))
	require.NoError(t, err)
	require.True(t, st.Mode()&os.ModeSymlink != 0)
}

func TestRuntime_StageInputs_WorkspaceMissing_Error(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-stage-miss", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Missing workspace path should error in copy mode.
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "workspace://no/such/file.txt",
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "nf.txt"),
		Mode: "copy",
	}})
	require.Error(t, err)
}

func TestRuntime_CreateWorkspace_WithWorkRoot(t *testing.T) {
	rt := local.NewRuntime(t.TempDir())
	ctx := context.Background()
	// Exec id with characters to sanitize.
	ws, err := rt.CreateWorkspace(
		ctx, "id with spaces/and*chars", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)
	// Ensure standard subdirs exist.
	_, err = os.Stat(filepath.Join(ws.Path, codeexecutor.DirSkills))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(ws.Path, codeexecutor.DirWork))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(ws.Path, codeexecutor.DirRuns))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(ws.Path, codeexecutor.DirOut))
	require.NoError(t, err)
}

func TestRuntime_PutDirectory_PreservesMode(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-preserve", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	src := t.TempDir()
	execFile := filepath.Join(src, "run.sh")
	require.NoError(t, os.WriteFile(execFile, []byte("echo"), 0o755))
	require.NoError(t, rt.PutDirectory(ctx, ws, src, "dst"))

	target := filepath.Join(ws.Path, "dst", "run.sh")
	st, err := os.Stat(target)
	require.NoError(t, err)
	// Executable bit should be preserved.
	require.NotZero(t, st.Mode()&0o111)
}

func TestRuntime_PutDirectory_NonexistentSrc_Error(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-miss-src", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Source does not exist; copy should fail.
	miss := filepath.Join(t.TempDir(), "no-such")
	err = rt.PutDirectory(ctx, ws, miss, "dst")
	require.Error(t, err)
}

func TestRuntime_ExecuteInline_NoBlocks(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	// No code blocks should still return a valid result.
	res, err := rt.ExecuteInline(ctx, "rt-inline-empty", nil,
		1*time.Second,
	)
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Equal(t, "", res.Stdout)
	require.Equal(t, "", res.Stderr)
}

func TestRuntime_Collect_ReadLimitedLargeFile(t *testing.T) {
	// Create a file larger than the per-file read limit to ensure
	// Collect truncates to the internal cap.
	const readLimitBytes = 4 * 1024 * 1024
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-collect-big", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Write a large file under work/.
	big := bytes.Repeat([]byte{'x'}, readLimitBytes+123)
	p := filepath.Join(ws.Path, codeexecutor.DirWork, "big.bin")
	require.NoError(t, os.MkdirAll(
		filepath.Dir(p), 0o755,
	))
	require.NoError(t, os.WriteFile(p, big, 0o644))

	files, err := rt.Collect(
		ctx, ws, []string{filepath.Join(codeexecutor.DirWork, "*.bin")},
	)
	require.NoError(t, err)
	require.Len(t, files, 1)
	// Content should be capped to readLimitBytes.
	require.Equal(t, readLimitBytes, len(files[0].Content))
}

func TestRuntime_CollectOutputs_PerFileCapApplied(t *testing.T) {
	// When MaxFileBytes exceeds the internal cap, content should be
	// truncated to the cap size.
	const capBytes = 4 * 1024 * 1024
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-out-cap", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare a large file under out/ bigger than cap.
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws.Path, codeexecutor.DirOut), 0o755,
	))
	big := bytes.Repeat([]byte{'x'}, capBytes+111)
	f := filepath.Join(ws.Path, codeexecutor.DirOut, "b.bin")
	require.NoError(t, os.WriteFile(f, big, 0o644))

	mf, err := rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs:         []string{filepath.Join(codeexecutor.DirOut, "*")},
		Inline:        true,
		Save:          false,
		MaxFileBytes:  capBytes * 10,
		MaxTotalBytes: int64(capBytes * 10),
	})
	require.NoError(t, err)
	require.Len(t, mf.Files, 1)
	require.Equal(t, capBytes, len(mf.Files[0].Content))
}

func TestRuntime_StageInputs_HostLink_ReplacesExisting(t *testing.T) {
	// Ensure makeSymlink removes an existing regular file at dest.
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(
		ctx, "rt-link-repl", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Pre-create a regular file at destination path.
	dst := filepath.Join(
		codeexecutor.DirWork, "inputs", "lf.txt",
	)
	absDst := filepath.Join(ws.Path, dst)
	require.NoError(t, os.MkdirAll(filepath.Dir(absDst), 0o755))
	require.NoError(t, os.WriteFile(absDst, []byte("x"), 0o644))

	// Create a host source file to link to.
	src := filepath.Join(t.TempDir(), "s.txt")
	require.NoError(t, os.WriteFile(src, []byte("y"), 0o644))

	// Link mode should replace the existing file with a symlink.
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "host://" + src,
		To:   dst,
		Mode: "link",
	}})
	require.NoError(t, err)
	st, lerr := os.Lstat(absDst)
	require.NoError(t, lerr)
	require.NotZero(t, st.Mode()&os.ModeSymlink)
}
