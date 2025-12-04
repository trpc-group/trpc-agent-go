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

	data := []byte("abc")
	ver, err := SaveArtifactHelper(ctx, "a.txt", data,
		"text/plain")
	require.NoError(t, err)
	// Services may use 0 for the latest; any value is acceptable.
	_ = ver

	// Load latest when version is nil.
	out, mt, actual, err := LoadArtifactHelper(
		ctx, "a.txt", nil,
	)
	require.NoError(t, err)
	require.Equal(t, data, out)
	// Mime type should be preserved.
	require.Equal(t, "text/plain", mt)
	// Helper returns 0 when version was nil.
	require.Equal(t, 0, actual)

	// Load a specific version.
	out, mt, actual, err = LoadArtifactHelper(
		ctx, "a.txt", &ver,
	)
	require.NoError(t, err)
	require.Equal(t, data, out)
	require.Equal(t, "text/plain", mt)
	require.Equal(t, ver, actual)

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
