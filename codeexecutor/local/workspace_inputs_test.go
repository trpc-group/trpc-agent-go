//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local_test

import (
	"context"
	"path/filepath"
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
		{From: "workspace://work/seed.txt", To: "work/inputs/copy.txt", Mode: "copy"},
		{From: "artifact://demo.txt", To: "work/inputs/art.txt"},
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
