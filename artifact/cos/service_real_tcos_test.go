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
	appName := "testapp"
	userID := "user1"
	sessionID := "session1"
	name := "test.txt"
	var artifacts [][]byte
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, []byte("Hello, World!"+strconv.Itoa(i)))
	}
	t.Cleanup(func() {
		if _, err := s.Delete(context.Background(), &artifact.DeleteRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
		}); err != nil {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	var versionsPut []artifact.VersionID
	for _, data := range artifacts {
		desc, err := s.Put(context.Background(), &artifact.PutRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
			Body:      bytes.NewReader(data),
			MimeType:  "text/plain",
		})
		require.NoError(t, err)
		versionsPut = append(versionsPut, desc.Version)
	}

	versions, err := s.Versions(context.Background(), &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, versionsPut, versions.Versions)

	openLatest, err := s.Open(context.Background(), &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	require.Equal(t, "text/plain", openLatest.MimeType)
	gotLatest, err := io.ReadAll(openLatest.Body)
	require.NoError(t, err)
	require.NoError(t, openLatest.Body.Close())
	require.EqualValues(t, artifacts[len(artifacts)-1], gotLatest)

	for i, wanted := range artifacts {
		v := versionsPut[i]
		out, err := s.Open(context.Background(), &artifact.OpenRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
			Version:   &v,
		})
		require.NoError(t, err)
		got, err := io.ReadAll(out.Body)
		require.NoError(t, err)
		require.NoError(t, out.Body.Close())
		require.EqualValues(t, wanted, got)
	}

	items, err := s.List(context.Background(), &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	require.Equal(t, name, items.Items[0].Name)

	_, err = s.Delete(context.Background(), &artifact.DeleteRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)

	items, err = s.List(context.Background(), &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	require.NoError(t, err)
	require.Empty(t, items.Items)

	_, err = s.Versions(context.Background(), &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))

	_, err = s.Open(context.Background(), &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
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

	appName := "testapp"
	userID := "user1"
	sessionID := "session1"
	name := "put-head-delete.txt"

	t.Cleanup(func() {
		if _, err := s.Delete(context.Background(), &artifact.DeleteRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
		}); err != nil {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	data := []byte("PutHeadDelete")
	putDesc, err := s.Put(context.Background(), &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      bytes.NewReader(data),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)
	require.NotEmpty(t, putDesc.Version)

	headDesc, err := s.Head(context.Background(), &artifact.HeadRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	t.Logf("headDesc: %+v", headDesc)
	require.Equal(t, putDesc.Version, headDesc.Version)
	require.Equal(t, "text/plain", headDesc.MimeType)

	_, err = s.Delete(context.Background(), &artifact.DeleteRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)

	_, err = s.Head(context.Background(), &artifact.HeadRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
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
	appName := "testapp"
	userID := "user2"
	sessionID := ""
	name := "test.txt"
	t.Cleanup(func() {
		if _, err := s.Delete(context.Background(), &artifact.DeleteRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
		}); err != nil {
			t.Logf("Cleanup: Delete: %v", err)
		}
	})

	var versionsPut []artifact.VersionID
	var artifacts [][]byte
	for i := 0; i < 3; i++ {
		data := []byte("Hi, World!" + strconv.Itoa(i))
		desc, err := s.Put(context.Background(), &artifact.PutRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
			Body:      bytes.NewReader(data),
			MimeType:  "text/plain",
		})
		require.NoError(t, err)
		versionsPut = append(versionsPut, desc.Version)
		artifacts = append(artifacts, data)
	}

	versions, err := s.Versions(context.Background(), &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, versionsPut, versions.Versions)

	openLatest, err := s.Open(context.Background(), &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	require.Equal(t, "text/plain", openLatest.MimeType)
	gotLatest, err := io.ReadAll(openLatest.Body)
	require.NoError(t, err)
	require.NoError(t, openLatest.Body.Close())
	require.EqualValues(t, artifacts[len(artifacts)-1], gotLatest)

	for i := 0; i < 3; i++ {
		v := versionsPut[i]
		out, err := s.Open(context.Background(), &artifact.OpenRequest{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			Name:      name,
			Version:   &v,
		})
		require.NoError(t, err)
		got, err := io.ReadAll(out.Body)
		require.NoError(t, err)
		require.NoError(t, out.Body.Close())
		require.EqualValues(t, artifacts[i], got)
	}

	items, err := s.List(context.Background(), &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: "", // user scope
	})
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	require.Equal(t, name, items.Items[0].Name)

	_, err = s.Delete(context.Background(), &artifact.DeleteRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)

	items, err = s.List(context.Background(), &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: "",
	})
	require.NoError(t, err)
	require.Empty(t, items.Items)

	_, err = s.Versions(context.Background(), &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))

	_, err = s.Open(context.Background(), &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, artifact.ErrNotFound))
}
