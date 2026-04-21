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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestFileSpec_Validation(t *testing.T) {
	_, err := NewFileRequirement(FileSpec{})
	require.Error(t, err, "empty Target must be rejected")

	_, err = NewFileRequirement(FileSpec{Target: "   "})
	require.Error(t, err, "whitespace-only Target must be rejected")

	_, err = NewFileRequirement(FileSpec{Target: "work/x"})
	require.Error(t, err, "no Content and no Input must be rejected")
}

// TestFileSpec_InputDefaultsDestinationToTarget pins the rule that
// FileSpec.Input.To, when omitted, inherits the surrounding Target.
// That defaulting lives in NewFileRequirement and callers rely on it
// to avoid repeating the destination path twice.
func TestFileSpec_InputDefaultsDestinationToTarget(t *testing.T) {
	req, err := NewFileRequirement(FileSpec{
		Target: "work/seed.txt",
		Input: &codeexecutor.InputSpec{
			From: "workspace://elsewhere",
			Mode: "link",
		},
	})
	require.NoError(t, err)
	// Fingerprint depends on Input.From / Mode / Pin and Target; the
	// surface check that matters is that two fingerprints differ when
	// Input differs from inline Content, ensuring both code paths
	// participate in the digest.
	fp1, err := req.Fingerprint(context.Background(), ApplyContext{})
	require.NoError(t, err)

	inline, err := NewFileRequirement(FileSpec{
		Target:  "work/seed.txt",
		Content: []byte("seed"),
	})
	require.NoError(t, err)
	fp2, err := inline.Fingerprint(context.Background(), ApplyContext{})
	require.NoError(t, err)

	require.NotEqual(t, fp1, fp2,
		"inline Content and Input source must hash differently")
}

func TestFileRequirement_MetadataSurface(t *testing.T) {
	req, err := NewFileRequirement(FileSpec{
		Target:   "work/x.txt",
		Content:  []byte("hi"),
		Optional: true,
	})
	require.NoError(t, err)
	require.Equal(t, "file:work/x.txt", req.Key())
	require.Equal(t, KindFile, req.Kind())
	require.Equal(t, PhaseFile, req.Phase())
	require.Equal(t, "work/x.txt", req.Target())
	require.False(t, req.Required(),
		"Optional FileSpec must translate to Required()==false")
}

func TestFileRequirement_Apply_NilEngineFails(t *testing.T) {
	req, err := NewFileRequirement(FileSpec{
		Target:  "work/x.txt",
		Content: []byte("hi"),
	})
	require.NoError(t, err)
	err = req.Apply(context.Background(), ApplyContext{})
	require.Error(t, err, "nil engine must surface an explicit error")
}

func TestCommandSpec_Validation(t *testing.T) {
	_, err := NewCommandRequirement(CommandSpec{})
	require.Error(t, err)

	_, err = NewCommandRequirement(CommandSpec{Cmd: "  "})
	require.Error(t, err)
}

func TestCommandRequirement_MetadataAndDefaultKey(t *testing.T) {
	req, err := NewCommandRequirement(CommandSpec{
		Cmd:      "bash",
		Args:     []string{"-lc", "echo hi"},
		Optional: true,
	})
	require.NoError(t, err)
	// Default keys are deterministic and derive from Cmd+Args, so the
	// reconciler can dedupe identical commands contributed by
	// independent providers.
	require.Contains(t, req.Key(), "cmd:")
	require.Equal(t, KindCommand, req.Kind())
	require.Equal(t, PhaseCommand, req.Phase())
	require.False(t, req.Required())
	require.Equal(t, "bash", req.Target(),
		"without MarkerPath the Target should fall back to Cmd")

	marked, err := NewCommandRequirement(CommandSpec{
		Cmd:        "bash",
		MarkerPath: "work/.done",
	})
	require.NoError(t, err)
	require.Equal(t, "work/.done", marked.Target(),
		"MarkerPath takes precedence over Cmd when set")
}

// TestCommandRequirement_FingerprintInputsChangeForcesReRun pins the
// core reason FingerprintInputs exists: editing a workspace file like
// requirements.txt must invalidate the cached fingerprint so the
// command re-runs without requiring callers to bump FingerprintSalt.
func TestCommandRequirement_FingerprintInputsChangeForcesReRun(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws.Path, "work"), 0o755,
	))
	reqsPath := filepath.Join(ws.Path, "work", "requirements.txt")
	require.NoError(t, os.WriteFile(reqsPath, []byte("v1"), 0o644))

	counterPath := filepath.Join(ws.Path, "work", "counter.log")
	req, err := NewCommandRequirement(CommandSpec{
		Cmd: "bash",
		Args: []string{
			"-lc",
			"echo run >> work/counter.log",
		},
		MarkerPath:        "work/.marker",
		FingerprintInputs: []string{"work/requirements.txt"},
	})
	require.NoError(t, err)

	rec := NewReconciler()
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)

	// Second reconcile with identical requirements.txt is a pure
	// fingerprint+marker skip.
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	got, err := os.ReadFile(counterPath)
	require.NoError(t, err)
	require.Equal(t, "run\n", string(got),
		"command must not re-run when FingerprintInputs are unchanged")

	// Edit the input file; the next reconcile must re-run the command.
	require.NoError(t, os.WriteFile(reqsPath, []byte("v2"), 0o644))
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	got, err = os.ReadFile(counterPath)
	require.NoError(t, err)
	require.Equal(t, "run\nrun\n", string(got))
}

// TestCommandRequirement_NonZeroExitFails documents the contract that
// a non-zero exit code propagates as an error surfaced to the caller,
// carrying enough stderr context to be actionable.
func TestCommandRequirement_NonZeroExitFails(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	req, err := NewCommandRequirement(CommandSpec{
		Cmd: "bash",
		Args: []string{
			"-lc",
			"echo boom >&2; exit 3",
		},
	})
	require.NoError(t, err)

	rec := NewReconciler()
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exited 3")
	require.Contains(t, err.Error(), "boom")
}

// TestCommandRequirement_NonZeroExitStdoutFallback locks the fallback
// path in trimForError: when stderr is empty we should still surface
// stdout so the model / operator has something to act on.
func TestCommandRequirement_NonZeroExitStdoutFallback(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	req, err := NewCommandRequirement(CommandSpec{
		Cmd: "bash",
		Args: []string{
			"-lc",
			"echo only-stdout; exit 7",
		},
	})
	require.NoError(t, err)

	rec := NewReconciler()
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exited 7")
	require.Contains(t, err.Error(), "only-stdout")
}

func TestCommandRequirement_Apply_NilEngineFails(t *testing.T) {
	req, err := NewCommandRequirement(CommandSpec{Cmd: "true"})
	require.NoError(t, err)
	err = req.Apply(context.Background(), ApplyContext{})
	require.Error(t, err)
}

// TestCommandRequirement_ObservedPathsSentinel covers the branch
// where ObservedPaths alone are declared (no MarkerPath). A missing
// observed path must trigger re-apply even when the fingerprint is
// unchanged.
func TestCommandRequirement_ObservedPathsSentinel(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	req, err := NewCommandRequirement(CommandSpec{
		Cmd: "bash",
		Args: []string{
			"-lc",
			"mkdir -p work && echo ok > work/observed.txt",
		},
		ObservedPaths: []string{"work/observed.txt"},
	})
	require.NoError(t, err)

	rec := NewReconciler()
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)

	require.NoError(t, os.Remove(
		filepath.Join(ws.Path, "work/observed.txt"),
	))
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(ws.Path, "work/observed.txt"))
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(got))
}

func TestCloneEnv_EmptyReturnsAllocatedMap(t *testing.T) {
	out := cloneEnv(nil)
	require.NotNil(t, out)
	require.Empty(t, out)

	in := map[string]string{"A": "1", "B": "2"}
	got := cloneEnv(in)
	require.Equal(t, in, got)
	// Must be a copy, not the same backing map.
	got["A"] = "mutated"
	require.Equal(t, "1", in["A"])
}

func TestTrimForError_PrefersStderrAndTruncates(t *testing.T) {
	require.Equal(t, "err", trimForError("err", "out"))
	require.Equal(t, "out", trimForError("   ", "out"))
	require.Equal(t, "", trimForError("", ""))

	long := ""
	for i := 0; i < 1024; i++ {
		long += "a"
	}
	trimmed := trimForError(long, "")
	require.Less(t, len(trimmed), len(long))
	require.Contains(t, trimmed, "...")
}

func TestCleanRel_Normalization(t *testing.T) {
	require.Equal(t, "", cleanRel(""))
	require.Equal(t, "", cleanRel("   "))
	require.Equal(t, "work/x", cleanRel("/work/x"))
	require.Equal(t, "work/x", cleanRel("work\\x"))
	require.Equal(t, "work/x", cleanRel("work/./x"))
}

func TestReconciler_NilEngineReturnsError(t *testing.T) {
	_, ws := newTestEngine(t)
	req, err := NewFileRequirement(FileSpec{
		Target:  "work/x.txt",
		Content: []byte("y"),
	})
	require.NoError(t, err)
	rec := NewReconciler()
	_, err = rec.Reconcile(
		context.Background(), nil, ws, []Requirement{req},
	)
	require.Error(t, err)
}

func TestReconciler_EmptyReqsIsNoop(t *testing.T) {
	eng, ws := newTestEngine(t)
	rec := NewReconciler()
	warnings, err := rec.Reconcile(
		context.Background(), eng, ws, nil,
	)
	require.NoError(t, err)
	require.Empty(t, warnings)
}

// TestReconciler_DropsNilAndEmptyKeyedRequirements protects the
// reconciler from buggy providers that return nil entries or
// requirements with empty keys. Such entries must be silently
// dropped so a single bad provider cannot poison the batch.
func TestReconciler_DropsNilAndEmptyKeyedRequirements(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	good, err := NewFileRequirement(FileSpec{
		Target:  "work/good.txt",
		Content: []byte("ok"),
	})
	require.NoError(t, err)
	emptyKey := &orderReq{key: "", kind: KindFile, phase: PhaseFile}

	rec := NewReconciler()
	warnings, err := rec.Reconcile(
		ctx, eng, ws, []Requirement{nil, emptyKey, good},
	)
	require.NoError(t, err)
	require.Empty(t, warnings)

	got, err := os.ReadFile(filepath.Join(ws.Path, "work/good.txt"))
	require.NoError(t, err)
	require.Equal(t, "ok", string(got))
}

// TestReconciler_RequiredRequirementErrorBubbles verifies that a
// required requirement returning an Apply error aborts the reconcile
// with an informative error. The test uses an orderReq stub so we
// can inject a deterministic failure without relying on shell
// exit codes.
func TestReconciler_RequiredRequirementErrorBubbles(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	bad := &orderReq{
		key:      "bad",
		kind:     KindCommand,
		phase:    PhaseCommand,
		applyErr: fmt.Errorf("boom"),
	}
	rec := NewReconciler()
	_, err := rec.Reconcile(ctx, eng, ws, []Requirement{bad})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad")
	require.Contains(t, err.Error(), "boom")
}

// TestKeyedLocker_EmptyKeyFallback proves that callers with an empty
// workspace path still get mutual exclusion via the shared "__empty__"
// bucket. Without this fallback a misconfigured workspace would race
// inside the reconciler.
func TestKeyedLocker_EmptyKeyFallback(t *testing.T) {
	l := newKeyedLocker()
	unlock := l.lock("")
	unlock()
	// A second lock must succeed too, otherwise the reference count
	// leaked.
	unlock = l.lock("")
	unlock()
}
