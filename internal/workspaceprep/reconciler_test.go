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
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// newTestEngine returns a live local-runtime engine plus a fresh
// workspace for reconciler tests.
func newTestEngine(
	t *testing.T,
) (codeexecutor.Engine, codeexecutor.Workspace) {
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

func TestReconciler_ConcurrentInstancesMergePreparedMetadata(
	t *testing.T,
) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	base := codeexecutor.WorkspaceMetadata{
		Version: 1,
		Skills: map[string]codeexecutor.SkillMeta{
			"demo": {Name: "demo"},
		},
		Inputs: []codeexecutor.InputRecord{{
			From: "host://input", To: "work/input",
		}},
		Outputs: []codeexecutor.OutputRecord{{
			Globs: []string{"out/*"},
		}},
	}
	require.NoError(t, codeexecutor.SaveMetadata(ws.Path, base))

	var wg sync.WaitGroup
	keys := []string{"prep-a", "prep-b"}
	errs := make(chan error, len(keys))
	for _, key := range keys {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &orderReq{
				key:   key,
				kind:  KindCommand,
				phase: PhaseCommand,
				apply: func() {},
			}
			_, err := NewReconciler().Reconcile(
				ctx,
				eng,
				ws,
				[]Requirement{req},
			)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	md, err := codeexecutor.LoadMetadata(ws.Path)
	require.NoError(t, err)
	require.Contains(t, md.Prepared, "prep-a")
	require.Contains(t, md.Prepared, "prep-b")
	require.Contains(t, md.Skills, "demo")
	require.Len(t, md.Inputs, 1)
	require.Len(t, md.Outputs, 1)
}

func TestReconciler_SavePreparedDoesNotOverwriteStaleKeys(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	oldShared := codeexecutor.PreparedRecord{
		Key:         "shared",
		Kind:        string(KindCommand),
		Fingerprint: "old",
	}
	newShared := codeexecutor.PreparedRecord{
		Key:         "shared",
		Kind:        string(KindCommand),
		Fingerprint: "new",
	}
	newLocal := codeexecutor.PreparedRecord{
		Key:         "local",
		Kind:        string(KindCommand),
		Fingerprint: "local-new",
	}
	base := codeexecutor.WorkspaceMetadata{
		Version: 1,
		Skills: map[string]codeexecutor.SkillMeta{
			"demo": {Name: "demo"},
		},
		Prepared: map[string]codeexecutor.PreparedRecord{
			"shared": oldShared,
		},
	}
	require.NoError(t, codeexecutor.SaveMetadata(ws.Path, base))

	stale := cloneReconcileMetadata(base)
	stale.Prepared["local"] = newLocal
	latest, err := codeexecutor.LoadMetadata(ws.Path)
	require.NoError(t, err)
	latest.Prepared["shared"] = newShared
	latest.Inputs = []codeexecutor.InputRecord{{
		From: "host://latest", To: "work/latest",
	}}
	require.NoError(t, codeexecutor.SaveMetadata(ws.Path, latest))

	rec := NewReconciler().(*defaultReconciler)
	err = rec.saveReconcileMetadata(
		ctx,
		eng,
		ws,
		base,
		stale,
		[]string{"local"},
	)
	require.NoError(t, err)

	got, err := codeexecutor.LoadMetadata(ws.Path)
	require.NoError(t, err)
	require.Equal(t, "new", got.Prepared["shared"].Fingerprint)
	require.Equal(t, "local-new", got.Prepared["local"].Fingerprint)
	require.Contains(t, got.Skills, "demo")
	require.Len(t, got.Inputs, 1)
	require.Equal(t, "host://latest", got.Inputs[0].From)
}

func TestReconciler_SavePreparedPreservesDirectMetadataChanges(
	t *testing.T,
) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	base := codeexecutor.WorkspaceMetadata{
		Version: 1,
		Outputs: []codeexecutor.OutputRecord{},
		Prepared: map[string]codeexecutor.PreparedRecord{
			"shared": {Key: "shared", Fingerprint: "old"},
		},
	}
	require.NoError(t, codeexecutor.SaveMetadata(ws.Path, base))

	updated := cloneReconcileMetadata(base)
	updated.Outputs = []codeexecutor.OutputRecord{{
		Globs: []string{"out/*.txt"},
	}}
	updated.Prepared["local"] = codeexecutor.PreparedRecord{
		Key:         "local",
		Fingerprint: "new",
	}

	rec := NewReconciler().(*defaultReconciler)
	err := rec.saveReconcileMetadata(
		ctx,
		eng,
		ws,
		base,
		updated,
		[]string{"local"},
	)
	require.NoError(t, err)

	got, err := codeexecutor.LoadMetadata(ws.Path)
	require.NoError(t, err)
	require.Len(t, got.Outputs, 1)
	require.Equal(t, []string{"out/*.txt"}, got.Outputs[0].Globs)
	require.Equal(t, "new", got.Prepared["local"].Fingerprint)
}

func TestReconciler_MergePreservesAllDirectMetadataChanges(t *testing.T) {
	baseTime := time.Unix(100, 0).UTC()
	updateTime := time.Unix(200, 0).UTC()
	base := codeexecutor.WorkspaceMetadata{
		Version:    1,
		CreatedAt:  baseTime,
		UpdatedAt:  baseTime,
		LastAccess: baseTime,
		Skills: map[string]codeexecutor.SkillMeta{
			"old": {Name: "old"},
		},
		Inputs: []codeexecutor.InputRecord{{
			From: "host://old",
			To:   "work/old",
		}},
		Outputs: []codeexecutor.OutputRecord{{
			Globs: []string{"out/old"},
		}},
	}
	latest := cloneReconcileMetadata(base)
	updated := codeexecutor.WorkspaceMetadata{
		Version:    2,
		CreatedAt:  updateTime,
		UpdatedAt:  updateTime,
		LastAccess: updateTime,
		Skills: map[string]codeexecutor.SkillMeta{
			"new": {Name: "new"},
		},
		Inputs: []codeexecutor.InputRecord{{
			From: "host://new",
			To:   "work/new",
		}},
		Outputs: []codeexecutor.OutputRecord{{
			Globs: []string{"out/new"},
		}},
		Prepared: map[string]codeexecutor.PreparedRecord{
			"changed": {Key: "changed"},
		},
	}

	got := mergeReconcileMetadata(
		latest,
		base,
		updated,
		[]string{"changed"},
	)

	require.Equal(t, 2, got.Version)
	require.True(t, got.CreatedAt.Equal(updateTime))
	require.True(t, got.UpdatedAt.Equal(updateTime))
	require.True(t, got.LastAccess.Equal(updateTime))
	require.Contains(t, got.Skills, "new")
	require.NotContains(t, got.Skills, "old")
	require.Equal(t, "host://new", got.Inputs[0].From)
	require.Equal(t, []string{"out/new"}, got.Outputs[0].Globs)
	require.Contains(t, got.Prepared, "changed")
}

func TestReconciler_SavePreparedReturnsLoadMetadataError(t *testing.T) {
	rec := NewReconciler().(*defaultReconciler)
	err := rec.saveReconcileMetadata(
		context.Background(),
		&fakeEngine{fs: &fakeFS{collectErr: fmt.Errorf("collect failed")}},
		codeexecutor.Workspace{Path: t.TempDir()},
		codeexecutor.WorkspaceMetadata{},
		codeexecutor.WorkspaceMetadata{},
		nil,
	)

	require.ErrorContains(t, err, "collect failed")
}

func TestReconciler_RequiredFailurePropagatesPartialSaveError(
	t *testing.T,
) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	metadataDirectory := &orderReq{
		key:   "changed",
		kind:  KindCommand,
		phase: PhaseCommand,
		apply: func() {
			metaPath := filepath.Join(ws.Path, codeexecutor.MetaFileName)
			require.NoError(t, os.Remove(metaPath))
			require.NoError(t, os.Mkdir(metaPath, 0o755))
		},
	}
	requiredFail := &orderReq{
		key:      "bad",
		kind:     KindCommand,
		phase:    PhaseCommand,
		applyErr: fmt.Errorf("boom"),
	}

	warnings, err := NewReconciler().Reconcile(
		ctx,
		eng,
		ws,
		[]Requirement{metadataDirectory, requiredFail},
	)
	require.Empty(t, warnings)
	require.ErrorContains(t, err, "save metadata after partial apply")
	require.ErrorContains(t, err, "is a directory")
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
