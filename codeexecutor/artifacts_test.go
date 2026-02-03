//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
)

func TestParseArtifactRef(t *testing.T) {
	name, ver, err := ParseArtifactRef("item@123")
	require.NoError(t, err)
	require.Equal(t, "item", name)
	require.NotNil(t, ver)
	require.Equal(t, 123, *ver)

	name, ver, err = ParseArtifactRef("plain")
	require.NoError(t, err)
	require.Equal(t, "plain", name)
	require.Nil(t, ver)

	_, _, err = ParseArtifactRef("bad@v1")
	require.Error(t, err)
	_, _, err = ParseArtifactRef("a@b@c")
	require.Error(t, err)
}

func TestArtifactHelpers_SaveAndLoad(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewService()
	// Attach service and a dummy session to context.
	ctx = WithArtifactService(ctx, svc)
	ctx = WithArtifactSession(ctx, artifact.SessionInfo{
		AppName: "app", UserID: "u", SessionID: "s",
	})

	dataV0 := []byte("abc")
	ver0, err := SaveArtifactHelper(ctx, "a.txt", dataV0,
		"text/plain")
	require.NoError(t, err)
	dataV1 := []byte("def")
	ver1, err := SaveArtifactHelper(ctx, "a.txt", dataV1,
		"text/plain")
	require.NoError(t, err)

	// Load latest when version is nil.
	out, mt, actual, err := LoadArtifactHelper(
		ctx, "a.txt", nil,
	)
	require.NoError(t, err)
	require.Equal(t, dataV1, out)
	// Mime type should be preserved.
	require.Equal(t, "text/plain", mt)
	require.Equal(t, ver1, actual)

	// Load a specific version.
	out, mt, actual, err = LoadArtifactHelper(
		ctx, "a.txt", &ver0,
	)
	require.NoError(t, err)
	require.Equal(t, dataV0, out)
	require.Equal(t, "text/plain", mt)
	require.Equal(t, ver0, actual)

	out, mt, actual, err = LoadArtifactHelper(
		ctx, "a.txt", &ver1,
	)
	require.NoError(t, err)
	require.Equal(t, dataV1, out)
	require.Equal(t, "text/plain", mt)
	require.Equal(t, ver1, actual)

	// Save without a session to exercise the nil-branch.
	ctxNoSess := WithArtifactService(context.Background(), svc)
	_, err = SaveArtifactHelper(
		ctxNoSess, "b.bin", []byte{1, 2}, "application/octet-stream",
	)
	require.NoError(t, err)
}

func TestArtifactHelpers_NoServiceInContext(t *testing.T) {
	ctx := context.Background()
	_, _, _, err := LoadArtifactHelper(ctx, "x", nil)
	require.Error(t, err)
	_, err = SaveArtifactHelper(ctx, "x", nil, "")
	require.Error(t, err)
}

type listVersionsErrService struct{}

func (*listVersionsErrService) SaveArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
	_ *artifact.Artifact,
) (int, error) {
	return 0, nil
}

func (*listVersionsErrService) LoadArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
	_ *int,
) (*artifact.Artifact, error) {
	return &artifact.Artifact{Data: []byte("x")}, nil
}

func (*listVersionsErrService) ListArtifactKeys(
	_ context.Context,
	_ artifact.SessionInfo,
) ([]string, error) {
	return nil, nil
}

func (*listVersionsErrService) DeleteArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
) error {
	return nil
}

func (*listVersionsErrService) ListVersions(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
) ([]int, error) {
	return nil, errors.New("list versions failed")
}

func TestLoadArtifactHelper_LatestVersionUnknown_ReturnsZero(t *testing.T) {
	ctx := WithArtifactService(
		context.Background(), &listVersionsErrService{},
	)
	out, mt, actual, err := LoadArtifactHelper(ctx, "a.txt", nil)
	require.NoError(t, err)
	require.Equal(t, []byte("x"), out)
	require.Equal(t, "application/octet-stream", mt)
	require.Equal(t, 0, actual)
}
