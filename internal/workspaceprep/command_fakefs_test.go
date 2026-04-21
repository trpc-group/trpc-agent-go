//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// These tests lock down the FS()-backed fallbacks and error paths in
// commandRequirement that only fire for non-local engines (container /
// jupyter backends). The local-runtime based TestEngine cannot reach
// them because it always has a real Workspace.Path, so we drive the
// requirement with purpose-built fakes here.

// fakeFS is a minimal WorkspaceFS implementing only the methods
// commandRequirement actually calls: Collect and PutFiles.
type fakeFS struct {
	collectCalls   int
	collectReturns []codeexecutor.File
	collectErr     error

	putCalls int
	putErr   error
	putLast  []codeexecutor.PutFile
}

func (f *fakeFS) PutFiles(
	_ context.Context, _ codeexecutor.Workspace, files []codeexecutor.PutFile,
) error {
	f.putCalls++
	f.putLast = files
	return f.putErr
}

func (f *fakeFS) StageDirectory(
	context.Context, codeexecutor.Workspace,
	string, string, codeexecutor.StageOptions,
) error {
	return nil
}

func (f *fakeFS) Collect(
	_ context.Context, _ codeexecutor.Workspace, _ []string,
) ([]codeexecutor.File, error) {
	f.collectCalls++
	if f.collectErr != nil {
		return nil, f.collectErr
	}
	return f.collectReturns, nil
}

func (f *fakeFS) StageInputs(
	context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec,
) error {
	return nil
}

func (f *fakeFS) CollectOutputs(
	context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

// fakeRunner implements just enough of ProgramRunner for Apply() so we
// can simulate a successful command run without spawning a shell.
type fakeRunner struct {
	res codeexecutor.RunResult
	err error
}

func (r *fakeRunner) RunProgram(
	context.Context, codeexecutor.Workspace,
	codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return r.res, r.err
}

// fakeEngine wires a fakeFS and fakeRunner into the Engine surface.
// Manager is unused by commandRequirement so we leave it nil.
type fakeEngine struct {
	fs     codeexecutor.WorkspaceFS
	runner codeexecutor.ProgramRunner
}

func (e *fakeEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e *fakeEngine) FS() codeexecutor.WorkspaceFS           { return e.fs }
func (e *fakeEngine) Runner() codeexecutor.ProgramRunner     { return e.runner }
func (e *fakeEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

// newCommand is a tiny helper that returns the concrete type so tests
// can reach the unexported readFile / pathExists helpers directly.
func newCommand(t *testing.T, spec CommandSpec) *commandRequirement {
	t.Helper()
	req, err := NewCommandRequirement(spec)
	require.NoError(t, err)
	r, ok := req.(*commandRequirement)
	require.True(t, ok, "NewCommandRequirement must return *commandRequirement")
	return r
}

// TestCommandRequirement_ReadFile_FSBackend exercises the branch where
// the workspace has no local Path (container-style engine). readFile
// must fall through to Engine.FS().Collect and surface its result.
func TestCommandRequirement_ReadFile_FSBackend(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	fs := &fakeFS{
		collectReturns: []codeexecutor.File{
			{Name: "work/requirements.txt", Content: "flask==3.0"},
		},
	}
	rctx := ApplyContext{
		Engine:    &fakeEngine{fs: fs},
		Workspace: codeexecutor.Workspace{ID: "ws"}, // no Path
	}

	got, err := r.readFile(context.Background(), rctx, "work/requirements.txt")
	require.NoError(t, err)
	require.Equal(t, "flask==3.0", string(got))
	require.Equal(t, 1, fs.collectCalls,
		"FS().Collect must be consulted when Workspace.Path is empty")
}

// TestCommandRequirement_ReadFile_FSBackendEmpty verifies that a
// non-local engine returning an empty Collect result yields a
// (nil, nil) read so that Fingerprint simply folds an empty hash
// segment in for the missing file.
func TestCommandRequirement_ReadFile_FSBackendEmpty(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	fs := &fakeFS{collectReturns: nil}
	rctx := ApplyContext{
		Engine:    &fakeEngine{fs: fs},
		Workspace: codeexecutor.Workspace{ID: "ws"},
	}

	got, err := r.readFile(context.Background(), rctx, "missing.txt")
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestCommandRequirement_ReadFile_FSError ensures Collect errors are
// propagated verbatim so FingerprintInputs cannot silently hide a
// broken FS backend.
func TestCommandRequirement_ReadFile_FSError(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	boom := fmt.Errorf("collect-failed")
	rctx := ApplyContext{
		Engine:    &fakeEngine{fs: &fakeFS{collectErr: boom}},
		Workspace: codeexecutor.Workspace{ID: "ws"},
	}

	_, err := r.readFile(context.Background(), rctx, "x")
	require.ErrorIs(t, err, boom)
}

// TestCommandRequirement_ReadFile_NilEngineFallback pins the defensive
// branch where both the local path lookup and the FS() fallback are
// unavailable: readFile must quietly return (nil, nil) so that a
// fingerprint can still be computed in unit tests.
func TestCommandRequirement_ReadFile_NilEngineFallback(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	got, err := r.readFile(context.Background(), ApplyContext{}, "x")
	require.NoError(t, err)
	require.Nil(t, got)

	// Engine is set but FS() returns nil.
	got, err = r.readFile(context.Background(), ApplyContext{
		Engine: &fakeEngine{},
	}, "x")
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestCommandRequirement_PathExists_FSBackend covers both FS()
// outcomes for pathExists: a non-empty Collect result means the path
// exists, an empty result means it does not.
func TestCommandRequirement_PathExists_FSBackend(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})

	// Present.
	rctx := ApplyContext{
		Engine: &fakeEngine{fs: &fakeFS{
			collectReturns: []codeexecutor.File{{Name: "a"}},
		}},
	}
	ok, err := r.pathExists(context.Background(), rctx, "a")
	require.NoError(t, err)
	require.True(t, ok)

	// Absent.
	rctx = ApplyContext{Engine: &fakeEngine{fs: &fakeFS{}}}
	ok, err = r.pathExists(context.Background(), rctx, "a")
	require.NoError(t, err)
	require.False(t, ok)
}

// TestCommandRequirement_PathExists_FSError locks that Collect errors
// are surfaced through SentinelExists so a broken FS backend cannot
// be mistaken for a missing sentinel (which would silently trigger
// a re-run of the bootstrap command).
func TestCommandRequirement_PathExists_FSError(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	boom := fmt.Errorf("stat-failed")
	rctx := ApplyContext{
		Engine: &fakeEngine{fs: &fakeFS{collectErr: boom}},
	}
	ok, err := r.pathExists(context.Background(), rctx, "missing")
	require.ErrorIs(t, err, boom)
	require.False(t, ok)
}

// TestCommandRequirement_PathExists_NilEngine ensures that without an
// FS the sentinel is reported as absent (not errored). Apply handles
// that by re-running the command, which is the safe default.
func TestCommandRequirement_PathExists_NilEngine(t *testing.T) {
	r := newCommand(t, CommandSpec{Cmd: "true"})
	ok, err := r.pathExists(context.Background(), ApplyContext{}, "x")
	require.NoError(t, err)
	require.False(t, ok)
}

// TestCommandRequirement_Apply_MarkerWriteFails covers the branch
// where RunProgram succeeds but PutFiles on MarkerPath fails. The
// error must be wrapped with a "write marker" prefix so operators can
// tell bootstrap succeeded but the sentinel persist failed. Without
// wrapping, a retry loop would just look at the raw PutFiles error.
func TestCommandRequirement_Apply_MarkerWriteFails(t *testing.T) {
	r := newCommand(t, CommandSpec{
		Cmd:        "true",
		MarkerPath: "work/.marker",
	})
	putErr := fmt.Errorf("disk-full")
	fs := &fakeFS{putErr: putErr}
	rctx := ApplyContext{
		Engine: &fakeEngine{
			fs:     fs,
			runner: &fakeRunner{res: codeexecutor.RunResult{ExitCode: 0}},
		},
	}

	err := r.Apply(context.Background(), rctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write marker")
	require.ErrorIs(t, err, putErr)
	require.Equal(t, 1, fs.putCalls,
		"marker PutFiles must be attempted exactly once")
}

// TestCommandRequirement_Apply_NilFSSkipsMarker pins that a command
// completing successfully on an engine without an FS surface does not
// panic even when MarkerPath is set: the marker write is simply
// skipped. This matches the defensive nil-check in Apply.
func TestCommandRequirement_Apply_NilFSSkipsMarker(t *testing.T) {
	r := newCommand(t, CommandSpec{
		Cmd:        "true",
		MarkerPath: "work/.marker",
	})
	rctx := ApplyContext{
		Engine: &fakeEngine{
			runner: &fakeRunner{res: codeexecutor.RunResult{ExitCode: 0}},
		},
	}
	require.NoError(t, r.Apply(context.Background(), rctx))
}

// TestCommandRequirement_Fingerprint_ReadFileErrorPropagates chains
// the FS error through Fingerprint, which is the public path that
// Reconciler actually drives. Without this, readFile could leak an
// error that gets silently ignored during digest computation.
func TestCommandRequirement_Fingerprint_ReadFileErrorPropagates(t *testing.T) {
	r := newCommand(t, CommandSpec{
		Cmd:               "true",
		FingerprintInputs: []string{"work/requirements.txt"},
	})
	boom := fmt.Errorf("collect-failed")
	rctx := ApplyContext{
		Engine: &fakeEngine{fs: &fakeFS{collectErr: boom}},
	}
	_, err := r.Fingerprint(context.Background(), rctx)
	require.ErrorIs(t, err, boom)
}

// TestCommandRequirement_SentinelExists_PathExistsErrorPropagates is
// the counterpart check for the sentinel query: a broken FS backend
// must not be mistaken for a missing sentinel.
func TestCommandRequirement_SentinelExists_PathExistsErrorPropagates(t *testing.T) {
	r := newCommand(t, CommandSpec{
		Cmd:           "true",
		ObservedPaths: []string{"work/out.bin"},
	})
	boom := fmt.Errorf("stat-failed")
	rctx := ApplyContext{
		Engine: &fakeEngine{fs: &fakeFS{collectErr: boom}},
	}
	ok, err := r.SentinelExists(context.Background(), rctx)
	require.ErrorIs(t, err, boom)
	require.False(t, ok)
}
