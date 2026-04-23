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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// newTestEngine returns a live local-runtime engine plus a fresh
// workspace for reconciler tests.
func newTestEngine(t *testing.T) (codeexecutor.Engine, codeexecutor.Workspace) {
	t.Helper()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"prep-test",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(context.Background(), ws)
	})
	return eng, ws
}

func TestFileRequirement_InlineContentAppliesAndSkips(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	req, err := NewFileRequirement(FileSpec{
		Target:  "work/config.json",
		Content: []byte(`{"v":1}`),
	})
	require.NoError(t, err)
	rec := NewReconciler()

	warnings, err := rec.Reconcile(
		ctx, eng, ws, []Requirement{req},
	)
	require.NoError(t, err)
	require.Empty(t, warnings)

	got, err := os.ReadFile(filepath.Join(ws.Path, "work/config.json"))
	require.NoError(t, err)
	require.Equal(t, `{"v":1}`, string(got))

	// Second reconcile with unchanged fingerprint must be a skip,
	// i.e. the mtime of the sentinel should not change. We assert by
	// counting FS writes via a new fingerprint.
	infoBefore, err := os.Stat(filepath.Join(ws.Path, "work/config.json"))
	require.NoError(t, err)
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	infoAfter, err := os.Stat(filepath.Join(ws.Path, "work/config.json"))
	require.NoError(t, err)
	require.Equal(t, infoBefore.ModTime(), infoAfter.ModTime(),
		"file must not be rewritten when fingerprint matches")

	// Changing content forces a re-apply.
	req2, err := NewFileRequirement(FileSpec{
		Key:     req.Key(),
		Target:  "work/config.json",
		Content: []byte(`{"v":2}`),
	})
	require.NoError(t, err)
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req2})
	require.NoError(t, err)
	got, err = os.ReadFile(filepath.Join(ws.Path, "work/config.json"))
	require.NoError(t, err)
	require.Equal(t, `{"v":2}`, string(got))
}

func TestFileRequirement_SentinelMissingTriggersReapply(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	req, err := NewFileRequirement(FileSpec{
		Target:  "work/marker.txt",
		Content: []byte("hello"),
	})
	require.NoError(t, err)
	rec := NewReconciler()

	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	require.NoError(t, os.Remove(
		filepath.Join(ws.Path, "work/marker.txt"),
	))

	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(ws.Path, "work/marker.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestCommandRequirement_MarkerPathSelfHeals(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	// Use a command that records a side effect. The marker path is
	// the sentinel used for "was this requirement previously
	// satisfied on disk".
	cmd, err := NewCommandRequirement(CommandSpec{
		Cmd:        "bash",
		Args:       []string{"-lc", "mkdir -p work && echo ok > work/cmd.log"},
		MarkerPath: "work/.cmd-marker",
	})
	require.NoError(t, err)
	rec := NewReconciler()

	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{cmd})
	require.NoError(t, err)
	logBefore, err := os.ReadFile(filepath.Join(ws.Path, "work/cmd.log"))
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(logBefore))
	_, err = os.Stat(filepath.Join(ws.Path, "work/.cmd-marker"))
	require.NoError(t, err)

	// Skip path: fingerprint matches and marker exists.
	require.NoError(t, os.WriteFile(
		filepath.Join(ws.Path, "work/cmd.log"),
		[]byte("preserved"), 0o644,
	))
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{cmd})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(ws.Path, "work/cmd.log"))
	require.NoError(t, err)
	require.Equal(t, "preserved", string(got),
		"command must not re-run while marker is present")

	// Remove marker; the next reconcile must re-run the command.
	require.NoError(t, os.Remove(
		filepath.Join(ws.Path, "work/.cmd-marker"),
	))
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{cmd})
	require.NoError(t, err)
	got, err = os.ReadFile(filepath.Join(ws.Path, "work/cmd.log"))
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(got),
		"command must re-run after marker removal")
}

func TestReconciler_FixedPhaseOrder(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	// Build one requirement per phase and verify commands run after
	// files regardless of declaration order.
	var sequence []string
	var mu sync.Mutex
	recordPhase := func(name string) Requirement {
		return &orderReq{
			key:   name,
			kind:  KindFile,
			phase: PhaseFile,
			apply: func() {
				mu.Lock()
				sequence = append(sequence, name)
				mu.Unlock()
			},
		}
	}
	recordCommand := func(name string) Requirement {
		return &orderReq{
			key:   name,
			kind:  KindCommand,
			phase: PhaseCommand,
			apply: func() {
				mu.Lock()
				sequence = append(sequence, name)
				mu.Unlock()
			},
		}
	}
	recordSkill := func(name string) Requirement {
		return &orderReq{
			key:   name,
			kind:  KindSkill,
			phase: PhaseSkill,
			apply: func() {
				mu.Lock()
				sequence = append(sequence, name)
				mu.Unlock()
			},
		}
	}

	reqs := []Requirement{
		recordCommand("cmd-1"),
		recordSkill("skill-1"),
		recordPhase("file-1"),
		recordCommand("cmd-2"),
		recordPhase("file-2"),
	}
	rec := NewReconciler()
	_, err := rec.Reconcile(ctx, eng, ws, reqs)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"file-1", "file-2", "skill-1", "cmd-1", "cmd-2"},
		sequence,
	)
}

func TestReconciler_ConcurrentReconcileIsSerialized(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	var parallel atomic.Int32
	var maxParallel atomic.Int32
	var calls atomic.Int32
	makeReq := func(key string) Requirement {
		return &orderReq{
			key:   key,
			kind:  KindCommand,
			phase: PhaseCommand,
			apply: func() {
				cur := parallel.Add(1)
				for {
					m := maxParallel.Load()
					if cur <= m ||
						maxParallel.CompareAndSwap(m, cur) {
						break
					}
				}
				calls.Add(1)
				parallel.Add(-1)
			},
		}
	}

	rec := NewReconciler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			req := makeReq(fmt.Sprintf("serialize-%d", i))
			_, err := rec.Reconcile(
				ctx, eng, ws, []Requirement{req},
			)
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, int32(8), calls.Load())
	require.LessOrEqual(t, int(maxParallel.Load()), 1,
		"reconciler must serialize applies for the same workspace")
}

func TestReconciler_OptionalRequirementFailureIsWarning(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	bad := &orderReq{
		key:      "bad",
		kind:     KindCommand,
		phase:    PhaseCommand,
		optional: true,
		applyErr: fmt.Errorf("boom"),
	}
	good := &orderReq{
		key:   "good",
		kind:  KindCommand,
		phase: PhaseCommand,
		apply: func() {},
	}
	rec := NewReconciler()
	warnings, err := rec.Reconcile(
		ctx, eng, ws, []Requirement{bad, good},
	)
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	require.Contains(t, warnings[0], "optional requirement")
}

// orderReq is a minimal Requirement implementation used by tests to
// observe the reconciler's phase ordering and locking semantics.
type orderReq struct {
	key      string
	kind     Kind
	phase    Phase
	optional bool
	apply    func()
	applyErr error
}

func (r *orderReq) Key() string  { return r.key }
func (r *orderReq) Kind() Kind   { return r.kind }
func (r *orderReq) Phase() Phase { return r.phase }
func (r *orderReq) Required() bool {
	return !r.optional
}
func (r *orderReq) Target() string { return r.key }

func (r *orderReq) Fingerprint(
	_ context.Context, _ ApplyContext,
) (string, error) {
	return r.key, nil
}

func (r *orderReq) SentinelExists(
	_ context.Context, _ ApplyContext,
) (bool, error) {
	return false, nil
}

func (r *orderReq) Apply(_ context.Context, _ ApplyContext) error {
	if r.apply != nil {
		r.apply()
	}
	return r.applyErr
}
