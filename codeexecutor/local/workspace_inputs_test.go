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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestStageInputs_WorkspaceAndArtifact(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "inputs", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Seed a file inside workspace under work/.
	err = rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    filepath.Join(codeexecutor.DirWork, "seed.txt"),
		Content: []byte("alpha"),
		Mode:    0o644,
	}})
	require.NoError(t, err)

	// Prepare an in-memory artifact and context.
	svc := inmemory.NewService()
	_, err = svc.SaveArtifact(ctx, artifact.SessionInfo{},
		"demo.txt", &artifact.Artifact{Data: []byte("beta")})
	require.NoError(t, err)
	ctxIO := codeexecutor.WithArtifactService(ctx, svc)
	ctxIO = codeexecutor.WithArtifactSession(ctxIO, artifact.SessionInfo{})

	specs := []codeexecutor.InputSpec{
		{From: "workspace://work/seed.txt",
			To: "work/inputs/copy.txt", Mode: "copy"},
		{From: "artifact://demo.txt",
			To: "work/inputs/art.txt"},
	}
	require.NoError(t, rt.StageInputs(ctxIO, ws, specs))

	// Collect both files via outputs spec.
	man, err := rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs:  []string{"work/inputs/*.txt"},
		Inline: true,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(man.Files), 2)
}

func TestStageInputs_HostLinkAndCopy(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "in-host",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Host directory with a file.
	host := t.TempDir()
	hf := filepath.Join(host, "h.txt")
	require.NoError(t, os.WriteFile(hf, []byte("h"), 0o644))

	// Link the host dir under workspace.
	specs := []codeexecutor.InputSpec{{
		From: "host://" + host,
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "hlink"),
		Mode: "link",
	}}
	require.NoError(t, rt.StageInputs(ctx, ws, specs))
	// The symlink should exist; read through it.
	data, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "hlink", "h.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "h", string(data))

	// Copy the host dir into work/inputs by specifying a file path
	// so Dir(To) maps to work/inputs.
	specs = []codeexecutor.InputSpec{{
		From: "host://" + host,
		To: filepath.Join(
			codeexecutor.DirWork, "inputs", "dummy.txt",
		),
		Mode: "copy",
	}}
	require.NoError(t, rt.StageInputs(ctx, ws, specs))
	data, err = os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "h.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "h", string(data))
}

func TestStageInputs_WorkspaceLink(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "in-ws-link",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Seed file in workspace.
	seed := filepath.Join(codeexecutor.DirWork, "seed.txt")
	require.NoError(t, rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    seed,
		Content: []byte("seed"),
		Mode:    0o644,
	}}))

	// Link to another place in workspace.
	specs := []codeexecutor.InputSpec{{
		From: "workspace://" + seed,
		To:   filepath.Join(codeexecutor.DirWork, "linked", "seed.txt"),
		Mode: "link",
	}}
	require.NoError(t, rt.StageInputs(ctx, ws, specs))
	data, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "linked", "seed.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "seed", string(data))
}

func TestStageInputs_SkillCopy(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "in-skill",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Create a fake skill at skills/demo with a file.
	sdir := filepath.Join(ws.Path, codeexecutor.DirSkills, "demo")
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "s.txt"), []byte("s"), 0o644,
	))

	specs := []codeexecutor.InputSpec{{
		From: "skill://demo/s.txt",
		To:   filepath.Join(codeexecutor.DirWork, "inputs", "s.txt"),
		Mode: "copy",
	}}
	require.NoError(t, rt.StageInputs(ctx, ws, specs))
	data, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "s.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "s", string(data))
}

func TestStageInputs_DefaultTo_Artifact(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "in-def-to",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Prepare artifact service and one artifact.
	svc := inmemory.NewService()
	_, err = svc.SaveArtifact(ctx, artifact.SessionInfo{},
		"foo.txt", &artifact.Artifact{Data: []byte("z")})
	require.NoError(t, err)
	ctxIO := codeexecutor.WithArtifactService(ctx, svc)
	ctxIO = codeexecutor.WithArtifactSession(
		ctxIO, artifact.SessionInfo{},
	)

	// No To specified; should map to work/inputs/foo.txt.
	specs := []codeexecutor.InputSpec{{
		From: "artifact://foo.txt",
	}}
	require.NoError(t, rt.StageInputs(ctxIO, ws, specs))
	data, err := os.ReadFile(filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs", "foo.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "z", string(data))
}

func TestStageInputs_UnsupportedScheme(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "in-unsup",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	specs := []codeexecutor.InputSpec{{From: "http://x"}}
	err = rt.StageInputs(ctx, ws, specs)
	require.Error(t, err)
}

func TestCollectOutputs_SaveInlineTemplateAndLimits(t *testing.T) {
	rt := local.NewRuntime("")
	ctx := context.Background()
	ws, err := rt.CreateWorkspace(ctx, "out-spec",
		codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	defer rt.Cleanup(ctx, ws)

	// Create output files.
	outDir := filepath.Join(codeexecutor.DirOut)
	require.NoError(t, rt.PutFiles(ctx, ws, []codeexecutor.PutFile{
		{Path: filepath.Join(outDir, "a.txt"),
			Content: []byte("alpha"), Mode: 0o644},
		{Path: filepath.Join(outDir, "b.txt"),
			Content: []byte("bravo"), Mode: 0o644},
	}))

	// Artifact service for Save.
	svc := inmemory.NewService()
	ctxIO := codeexecutor.WithArtifactService(ctx, svc)
	ctxIO = codeexecutor.WithArtifactSession(
		ctxIO, artifact.SessionInfo{},
	)

	man, err := rt.CollectOutputs(ctxIO, ws, codeexecutor.OutputSpec{
		Globs:         []string{filepath.Join(outDir, "*.txt")},
		MaxFiles:      1, // trigger limits
		MaxTotalBytes: 1024,
		Save:          true,
		NameTemplate:  "prefix/",
		Inline:        true,
	})
	require.NoError(t, err)
	require.True(t, man.LimitsHit)
	require.Equal(t, 1, len(man.Files))
	ref := man.Files[0]
	require.NotEmpty(t, ref.Content)
	require.True(t, strings.HasPrefix(ref.SavedAs, "prefix/"))
}
