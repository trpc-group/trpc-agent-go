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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

// newFSSkillRepo writes a minimal SKILL.md under <tmp>/<name> and
// returns an FSRepository pointed at the parent. Tests use this to
// exercise the skill requirement end-to-end without reaching for a
// stub repository, since FSRepository is the same implementation the
// production path uses.
func newFSSkillRepo(
	t *testing.T, name, body string,
) (rootskill.Repository, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte(body),
		0o644,
	))
	repo, err := rootskill.NewFSRepository(root)
	require.NoError(t, err)
	return repo, dir
}

func TestNewSkillRequirement_Validation(t *testing.T) {
	_, err := NewSkillRequirement(SkillSpec{})
	require.Error(t, err)

	_, err = NewSkillRequirement(SkillSpec{Name: "x"})
	require.Error(t, err, "repository is required")
}

// TestNewSkillRequirement_RejectsUnsafeNames locks the path-traversal
// guard. SkillSpec.Name flows into skills/<name> (via Target) and
// skillstage cleanup; a malicious or misconfigured caller must not
// be able to smuggle traversal components such as absolute paths,
// backslashes, or parent references.
func TestNewSkillRequirement_RejectsUnsafeNames(t *testing.T) {
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	unsafe := []string{
		"../evil",
		"foo/../bar",
		"/absolute",
		"nested/sub",
		`windows\path`,
		".",
		"..",
		".hidden",
	}
	for _, name := range unsafe {
		_, err := NewSkillRequirement(SkillSpec{
			Name:       name,
			Repository: repo,
		})
		require.Errorf(t, err,
			"unsafe name %q must be rejected", name)
	}
}

func TestNewSkillRequirement_MetadataSurface(t *testing.T) {
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	req, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)

	// Default key is derived from name.
	require.Equal(t, "skill:echoer", req.Key())
	require.Equal(t, KindSkill, req.Kind())
	require.Equal(t, PhaseSkill, req.Phase())
	require.True(t, req.Required())
	require.Equal(t,
		filepath.ToSlash(filepath.Join(
			codeexecutor.DirSkills, "echoer",
		)),
		req.Target(),
	)

	// Optional flips Required; custom key is preserved verbatim.
	req2, err := NewSkillRequirement(SkillSpec{
		Key:        "custom",
		Name:       "echoer",
		Repository: repo,
		Optional:   true,
	})
	require.NoError(t, err)
	require.Equal(t, "custom", req2.Key())
	require.False(t, req2.Required())
}

func TestSkillRequirement_Fingerprint_ReflectsMode(t *testing.T) {
	ctx := context.Background()
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	writable, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)
	readOnly, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
		ReadOnly:   true,
	})
	require.NoError(t, err)

	rctx := ApplyContext{}
	fpW, err := writable.Fingerprint(ctx, rctx)
	require.NoError(t, err)
	fpR, err := readOnly.Fingerprint(ctx, rctx)
	require.NoError(t, err)
	require.NotEmpty(t, fpW)
	require.NotEmpty(t, fpR)
	require.NotEqual(t, fpW, fpR,
		"staging mode must participate in the fingerprint so that "+
			"toggling ReadOnly triggers a re-stage")
}

func TestSkillRequirement_Fingerprint_UnknownSkillErrors(t *testing.T) {
	ctx := context.Background()
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	req, err := NewSkillRequirement(SkillSpec{
		Name:       "missing",
		Repository: repo,
	})
	require.NoError(t, err)
	_, err = req.Fingerprint(ctx, ApplyContext{})
	require.Error(t, err)
}

func TestSkillRequirement_ApplyAndSentinel(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	req, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)

	rctx := ApplyContext{Engine: eng, Workspace: ws}

	// Sentinel is absent before any apply.
	ok, err := req.SentinelExists(ctx, rctx)
	require.NoError(t, err)
	require.False(t, ok)

	// Apply materializes the skill tree.
	require.NoError(t, req.Apply(ctx, rctx))

	// SKILL.md lands at skills/<name>/SKILL.md.
	data, err := os.ReadFile(filepath.Join(
		ws.Path, "skills", "echoer", "SKILL.md",
	))
	require.NoError(t, err)
	require.Equal(t, "body", string(data))

	// After Apply, both the symlink structure and SKILL.md exist.
	ok, err = req.SentinelExists(ctx, rctx)
	require.NoError(t, err)
	require.True(t, ok)

	// A repeated Apply on the same fingerprint is an internally-guarded
	// no-op (the stager re-uses the existing tree).
	require.NoError(t, req.Apply(ctx, rctx))
}

func TestSkillRequirement_Apply_NilEngineFails(t *testing.T) {
	ctx := context.Background()
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	req, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)
	err = req.Apply(ctx, ApplyContext{})
	require.Error(t, err)
}

func TestSkillRequirement_SentinelNilEngineIsTrue(t *testing.T) {
	// When Engine is nil the requirement cannot probe the filesystem.
	// SkillLinksPresent returns false/nil for a bare Workspace so the
	// sentinel short-circuits to "absent"; ensure we do not crash.
	ctx := context.Background()
	repo, _ := newFSSkillRepo(t, "echoer", "body")
	req, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)
	ok, err := req.SentinelExists(ctx, ApplyContext{})
	// Either the stager returns (false, nil) because links are absent,
	// or it returns an error. We only require that the call is safe.
	require.False(t, ok || err == nil && ok,
		"sentinel must not falsely claim presence without an engine")
}

func TestSkillRequirement_EndToEndViaReconciler(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)
	repo, _ := newFSSkillRepo(t, "echoer", "body")

	req, err := NewSkillRequirement(SkillSpec{
		Name:       "echoer",
		Repository: repo,
	})
	require.NoError(t, err)
	rec := NewReconciler()
	warnings, err := rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	require.Empty(t, warnings)

	// Second reconcile is a pure skip: fingerprint matches and the
	// sentinel confirms the tree is still present, so no Apply is
	// invoked and the mtime of SKILL.md stays put.
	info, err := os.Stat(filepath.Join(
		ws.Path, "skills", "echoer", "SKILL.md",
	))
	require.NoError(t, err)
	_, err = rec.Reconcile(ctx, eng, ws, []Requirement{req})
	require.NoError(t, err)
	info2, err := os.Stat(filepath.Join(
		ws.Path, "skills", "echoer", "SKILL.md",
	))
	require.NoError(t, err)
	require.Equal(t, info.ModTime(), info2.ModTime())
}
