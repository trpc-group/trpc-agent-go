//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestNewService(t *testing.T) {
	service := NewService()

	assert.NotNil(t, service)
	assert.NotNil(t, service.artifacts)
	assert.Equal(t, 0, len(service.artifacts))
}

func TestPutOpenHeadVersionsDelete(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	appName := "testapp"
	userID := "user123"
	sessionID := "session456"
	name := "test.txt"

	// Put two versions.
	desc1, err := service.Put(ctx, &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      bytes.NewReader([]byte("v1")),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)
	desc2, err := service.Put(ctx, &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      bytes.NewReader([]byte("v2")),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)
	assert.NotEqual(t, desc1.Version, desc2.Version)

	// Open latest.
	data, latestDesc, err := artifact.ReadAll(ctx, service, &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), data)
	assert.Equal(t, desc2.Version, latestDesc.Version)

	// Open specific.
	data, d1, err := artifact.ReadAll(ctx, service, &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Version:   &desc1.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), data)
	assert.Equal(t, desc1.Version, d1.Version)

	// Versions.
	versions, err := service.Versions(ctx, &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	assert.Len(t, versions.Versions, 2)

	// Delete removes all versions.
	del, err := service.Delete(ctx, &artifact.DeleteRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	require.True(t, del.Deleted)
	_, _, err = artifact.ReadAll(ctx, service, &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	assert.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestListPagination(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	baseApp := "testapp"
	baseUser := "user123"
	baseSession := "s1"

	_, err := service.Put(ctx, &artifact.PutRequest{
		AppName:   baseApp,
		UserID:    baseUser,
		SessionID: baseSession,
		Name:      "a.txt",
		Body:      bytes.NewReader([]byte("a")),
	})
	require.NoError(t, err)
	_, err = service.Put(ctx, &artifact.PutRequest{
		AppName:   baseApp,
		UserID:    baseUser,
		SessionID: baseSession,
		Name:      "b.txt",
		Body:      bytes.NewReader([]byte("b")),
	})
	require.NoError(t, err)

	limit1 := 1
	page1, err := service.List(ctx, &artifact.ListRequest{
		AppName:   baseApp,
		UserID:    baseUser,
		SessionID: baseSession,
		Limit:     &limit1,
	})
	require.NoError(t, err)
	require.Len(t, page1.Items, 1)
	require.NotEmpty(t, page1.NextPageToken)

	limit10 := 10
	next := page1.NextPageToken
	page2, err := service.List(ctx, &artifact.ListRequest{
		AppName:   baseApp,
		UserID:    baseUser,
		SessionID: baseSession,
		Limit:     &limit10,
		PageToken: &next,
	})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	require.Empty(t, page2.NextPageToken)
}

func TestUserScopeIgnoresSessionID(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	putKey := &artifact.PutRequest{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "",
		Name:      "profile.txt",
		Body:      bytes.NewReader([]byte("u")),
		MimeType:  "text/plain",
	}
	_, err := service.Put(ctx, putKey)
	require.NoError(t, err)

	data, _, err := artifact.ReadAll(ctx, service, &artifact.OpenRequest{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "",
		Name:      "profile.txt",
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("u"), data)
}
