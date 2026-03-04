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
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/cos"
)

func TestArtifact_SessionScope(t *testing.T) {
	// Put-Versions-Open-List-Delete-Versions-Open-List
	if os.Getenv("COS_INTEGRATION_TEST") != "1" {
		t.Skip("Skipping TCOS integration test (set COS_INTEGRATION_TEST=1 to run).")
	}
	bucketURL := os.Getenv("COS_BUCKET_URL")
	secretID := os.Getenv("COS_SECRETID")
	secretKey := os.Getenv("COS_SECRETKEY")
	if bucketURL == "" || secretID == "" || secretKey == "" {
		t.Skip("Skipping TCOS integration test (requires COS_BUCKET_URL, COS_SECRETID, COS_SECRETKEY).")
	}

	s, err := cos.NewService("cos-1", bucketURL)
	require.NoError(t, err)
	key := artifact.Key{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
		Scope:     artifact.ScopeSession,
		Name:      "test.txt",
	}
	var artifacts [][]byte
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, []byte("Hello, World!"+strconv.Itoa(i)))
	}
	t.Cleanup(func() {
		if err := s.Delete(context.Background(), key, artifact.DeleteAllOpt()); err != nil && !errors.Is(err, artifact.ErrNotFound) {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	var versionsPut []artifact.VersionID
	for _, data := range artifacts {
		desc, err := s.Put(context.Background(), key, bytes.NewReader(data), artifact.WithPutMimeType("text/plain"))
		require.NoError(t, err)
		versionsPut = append(versionsPut, desc.Version)
	}

	versions, err := s.Versions(context.Background(), key)
	require.NoError(t, err)
	require.ElementsMatch(t, versionsPut, versions)

	rc, desc, err := s.Open(context.Background(), key, nil)
	require.NoError(t, err)
	require.Equal(t, "text/plain", desc.MimeType)
	gotLatest, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.EqualValues(t, artifacts[len(artifacts)-1], gotLatest)

	for i, wanted := range artifacts {
		v := versionsPut[i]
		rc, _, err := s.Open(context.Background(), key, &v)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.EqualValues(t, wanted, got)
	}

	items, _, err := s.List(context.Background(), artifact.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Scope:     key.Scope,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, key.Name, items[0].Key.Name)

	err = s.Delete(context.Background(), key, artifact.DeleteAllOpt())
	require.NoError(t, err)

	items, _, err = s.List(context.Background(), artifact.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Scope:     key.Scope,
	})
	require.NoError(t, err)
	require.Empty(t, items)

	_, err = s.Versions(context.Background(), key)
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))

	_, _, err = s.Open(context.Background(), key, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))
}

func TestArtifact_SessionScope_PutHeadDelete(t *testing.T) {
	// Put-Head-Delete (session scope)
	if os.Getenv("COS_INTEGRATION_TEST") != "1" {
		t.Skip("Skipping TCOS integration test (set COS_INTEGRATION_TEST=1 to run).")
	}
	bucketURL := os.Getenv("COS_BUCKET_URL")
	secretID := os.Getenv("COS_SECRETID")
	secretKey := os.Getenv("COS_SECRETKEY")
	if bucketURL == "" || secretID == "" || secretKey == "" {
		t.Skip("Skipping TCOS integration test (requires COS_BUCKET_URL, COS_SECRETID, COS_SECRETKEY).")
	}

	s, err := cos.NewService("cos-1", bucketURL)
	require.NoError(t, err)

	key := artifact.Key{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
		Scope:     artifact.ScopeSession,
		Name:      "put-head-delete.txt",
	}

	t.Cleanup(func() {
		if err := s.Delete(context.Background(), key, artifact.DeleteAllOpt()); err != nil && !errors.Is(err, artifact.ErrNotFound) {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	data := []byte("PutHeadDelete")
	putDesc, err := s.Put(context.Background(), key, bytes.NewReader(data), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	require.NotEmpty(t, putDesc.Version)
	require.Equal(t, key.Name, putDesc.Key.Name)

	headDesc, err := s.Head(context.Background(), key, nil)
	require.NoError(t, err)
	t.Logf("headDesc: %+v", headDesc)
	require.Equal(t, putDesc.Version, headDesc.Version)
	require.Equal(t, "text/plain", headDesc.MimeType)

	err = s.Delete(context.Background(), key, artifact.DeleteAllOpt())
	require.NoError(t, err)

	_, err = s.Head(context.Background(), key, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))
}

func TestArtifact_UserScope(t *testing.T) {
	if os.Getenv("COS_INTEGRATION_TEST") != "1" {
		t.Skip("Skipping TCOS integration test (set COS_INTEGRATION_TEST=1 to run).")
	}
	bucketURL := os.Getenv("COS_BUCKET_URL")
	secretID := os.Getenv("COS_SECRETID")
	secretKey := os.Getenv("COS_SECRETKEY")
	if bucketURL == "" || secretID == "" || secretKey == "" {
		t.Skip("Skipping TCOS integration test (requires COS_BUCKET_URL, COS_SECRETID, COS_SECRETKEY).")
	}
	// Put-Versions-Open-List-Delete-Versions-Open-List
	s, err := cos.NewService("cos-2", bucketURL)
	require.NoError(t, err)
	key := artifact.Key{
		AppName:   "testapp",
		UserID:    "user2",
		SessionID: "session1",
		Scope:     artifact.ScopeUser,
		Name:      "test.txt",
	}
	t.Cleanup(func() {
		if err := s.Delete(context.Background(), key, artifact.DeleteAllOpt()); err != nil && !errors.Is(err, artifact.ErrNotFound) {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	var versionsPut []artifact.VersionID
	var artifacts [][]byte
	for i := 0; i < 3; i++ {
		data := []byte("Hi, World!" + strconv.Itoa(i))
		desc, err := s.Put(context.Background(), key, bytes.NewReader(data), artifact.WithPutMimeType("text/plain"))
		require.NoError(t, err)
		versionsPut = append(versionsPut, desc.Version)
		artifacts = append(artifacts, data)
	}

	versions, err := s.Versions(context.Background(), key)
	require.NoError(t, err)
	require.ElementsMatch(t, versionsPut, versions)

	rc, desc, err := s.Open(context.Background(), key, nil)
	require.NoError(t, err)
	require.Equal(t, "text/plain", desc.MimeType)
	gotLatest, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.EqualValues(t, artifacts[len(artifacts)-1], gotLatest)

	for i := 0; i < 3; i++ {
		v := versionsPut[i]
		rc, _, err := s.Open(context.Background(), key, &v)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.EqualValues(t, artifacts[i], got)
	}

	items, _, err := s.List(context.Background(), artifact.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: "", // not used for user scope
		Scope:     key.Scope,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, key.Name, items[0].Key.Name)

	err = s.Delete(context.Background(), key, artifact.DeleteAllOpt())
	require.NoError(t, err)

	items, _, err = s.List(context.Background(), artifact.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: "",
		Scope:     key.Scope,
	})
	require.NoError(t, err)
	require.Empty(t, items)

	_, err = s.Versions(context.Background(), key)
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))

	_, _, err = s.Open(context.Background(), key, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))
}
