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

	key := artifact.Key{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
		Scope:     artifact.ScopeSession,
		Name:      "test.txt",
	}

	// Put two versions.
	desc1, err := service.Put(ctx, key, bytes.NewReader([]byte("v1")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	desc2, err := service.Put(ctx, key, bytes.NewReader([]byte("v2")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	assert.NotEqual(t, desc1.Version, desc2.Version)

	// Open latest.
	data, latestDesc, err := artifact.ReadAll(ctx, service, key, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), data)
	assert.Equal(t, desc2.Version, latestDesc.Version)

	// Open specific.
	data, d1, err := artifact.ReadAll(ctx, service, key, &desc1.Version)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), data)
	assert.Equal(t, desc1.Version, d1.Version)

	// Versions.
	versions, err := service.Versions(ctx, key)
	require.NoError(t, err)
	assert.Len(t, versions, 2)

	// Delete removes all versions.
	err = service.Delete(ctx, key, artifact.DeleteAllOpt())
	require.NoError(t, err)
	_, _, err = artifact.ReadAll(ctx, service, key, nil)
	assert.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestListPagination(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	base := artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "s1", Scope: artifact.ScopeSession}
	_, err := service.Put(ctx, artifact.Key{AppName: base.AppName, UserID: base.UserID, SessionID: base.SessionID, Scope: base.Scope, Name: "a.txt"}, bytes.NewReader([]byte("a")))
	require.NoError(t, err)
	_, err = service.Put(ctx, artifact.Key{AppName: base.AppName, UserID: base.UserID, SessionID: base.SessionID, Scope: base.Scope, Name: "b.txt"}, bytes.NewReader([]byte("b")))
	require.NoError(t, err)

	prefix := artifact.KeyPrefix{AppName: base.AppName, UserID: base.UserID, SessionID: base.SessionID, Scope: base.Scope}
	page1, next, err := service.List(ctx, prefix, artifact.WithListLimit(1))
	require.NoError(t, err)
	require.Len(t, page1, 1)
	require.NotEmpty(t, next)

	page2, next2, err := service.List(ctx, prefix, artifact.WithListLimit(10), artifact.WithListPageToken(next))
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Empty(t, next2)
}

func TestUserScopeIgnoresSessionID(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	putKey := artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "s1", Scope: artifact.ScopeUser, Name: "profile.txt"}
	_, err := service.Put(ctx, putKey, bytes.NewReader([]byte("u")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)

	getKey := artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "s2", Scope: artifact.ScopeUser, Name: "profile.txt"}
	data, _, err := artifact.ReadAll(ctx, service, getKey, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("u"), data)
}
