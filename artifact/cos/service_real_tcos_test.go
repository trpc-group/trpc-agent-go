//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cos_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/cos"
)

func TestArtifact_SessionScope(t *testing.T) {
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_SECRETID, COS_SECRETKEY and COS_BUCKET_URL")
	s, err := cos.NewService("cos-1", os.Getenv("COS_BUCKET_URL"))
	require.NoError(t, err)
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
	}
	sessionScopeKey := "test.txt"
	var artifacts []*artifact.Artifact
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, &artifact.Artifact{
			Data:     []byte("Hello, World!" + strconv.Itoa(i)),
			MimeType: "text/plain",
			Name:     "display_name_user_scope_test.txt",
		})
	}
	t.Cleanup(func() {
		if err := s.DeleteArtifact(context.Background(), sessionInfo, sessionScopeKey); err != nil {
			t.Logf("Cleanup: DeleteArtifact: %v", err)
		}
	})

	for i, a := range artifacts {
		version, err := s.SaveArtifact(context.Background(),
			sessionInfo, sessionScopeKey, a)
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	version, err := s.ListVersions(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, version)

	a, err := s.LoadArtifact(context.Background(), sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.EqualValues(t, &artifact.Artifact{
		Data:     []byte("Hello, World!" + strconv.Itoa(2)),
		MimeType: "text/plain",
		Name:     sessionScopeKey,
	}, a)
	for i, wanted := range artifacts {
		got, err := s.LoadArtifact(context.Background(),
			sessionInfo, sessionScopeKey, &i)
		require.NoError(t, err)
		require.EqualValues(t, wanted.Data, got.Data)
		require.EqualValues(t, wanted.MimeType, got.MimeType)
		require.EqualValues(t, sessionScopeKey, got.Name)
	}

	keys, err := s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{sessionScopeKey}, keys)

	err = s.DeleteArtifact(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)

	keys, err = s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	version, err = s.ListVersions(context.Background(), sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.Empty(t, version)

	a, err = s.LoadArtifact(context.Background(), sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, a)
}

func TestArtifact_UserScope(t *testing.T) {
	t.Skip("Skipping TCOS integration test, need to set up environment variables COS_BUCKET_URL, COS_SECRETID and COS_SECRETKEY")
	// Save-ListVersions-Load-ListKeys-Delete-ListVersions-Load-ListKeys
	s, err := cos.NewService("cos-2", os.Getenv("COS_BUCKET_URL"))
	require.NoError(t, err)
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user2",
		SessionID: "session1",
	}
	userScopeKey := "user:test.txt"
	t.Cleanup(func() {
		if err := s.DeleteArtifact(context.Background(), sessionInfo, userScopeKey); err != nil {
			t.Logf("Cleanup: DeleteArtifact: %v", err)
		}
	})

	for i := 0; i < 3; i++ {
		data := []byte("Hi, World!" + strconv.Itoa(i))
		version, err := s.SaveArtifact(context.Background(),
			sessionInfo, userScopeKey, &artifact.Artifact{
				Data:     data,
				MimeType: "text/plain",
				Name:     "display_name_user_scope_test.txt",
			})
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	version, err := s.ListVersions(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, version)

	a, err := s.LoadArtifact(context.Background(), sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.EqualValues(t, &artifact.Artifact{
		Data:     []byte("Hi, World!" + strconv.Itoa(2)),
		MimeType: "text/plain",
		Name:     userScopeKey,
	}, a)
	for i := 0; i < 3; i++ {
		a, err := s.LoadArtifact(context.Background(),
			sessionInfo, userScopeKey, &i)
		require.NoError(t, err)
		require.EqualValues(t, &artifact.Artifact{
			Data:     []byte("Hi, World!" + strconv.Itoa(i)),
			MimeType: "text/plain",
			Name:     userScopeKey,
		}, a)
	}

	keys, err := s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userScopeKey}, keys)

	err = s.DeleteArtifact(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)

	keys, err = s.ListArtifactKeys(context.Background(), sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	version, err = s.ListVersions(context.Background(), sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.Empty(t, version)

	a, err = s.LoadArtifact(context.Background(), sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, a)
}
