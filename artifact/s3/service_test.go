//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

// mockStorage is a mock implementation of the storage Client.
type mockStorage struct {
	mu      sync.RWMutex
	objects map[string]*mockObject
}

type mockObject struct {
	data        []byte
	contentType string
}

func newMockClient() *mockStorage {
	return &mockStorage{objects: make(map[string]*mockObject)}
}

func (m *mockStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = &mockObject{
		data:        append([]byte(nil), data...),
		contentType: contentType,
	}
	return nil
}

func (m *mockStorage) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, "", s3storage.ErrNotFound
	}
	return append([]byte(nil), obj.data...), obj.contentType, nil
}

func (m *mockStorage) OpenObject(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, "", 0, s3storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), obj.contentType, int64(len(obj.data)), nil
}

func (m *mockStorage) HeadObject(ctx context.Context, key string) (string, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[key]
	if !ok {
		return "", 0, s3storage.ErrNotFound
	}
	return obj.contentType, int64(len(obj.data)), nil
}

func (m *mockStorage) PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	return "", errors.New("presign not supported in mock")
}

func (m *mockStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var keys []string
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (m *mockStorage) DeleteObjects(ctx context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.objects, k)
	}
	return nil
}

func (m *mockStorage) Close() error { return nil }

func newTestService(t *testing.T) (*Service, *mockStorage) {
	mock := newMockClient()
	svc, err := NewService(context.Background(), "test-bucket", WithClient(mock))
	require.NoError(t, err)
	return svc, mock
}

func TestService_PutOpenHead(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	appName := "test-app"
	userID := "user-123"
	sessionID := "session-456"
	name := "test.txt"

	desc, err := svc.Put(ctx, &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      strings.NewReader("hello"),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)
	require.NotEmpty(t, desc.Version)
	assert.Equal(t, "text/plain", desc.MimeType)
	assert.Equal(t, int64(5), desc.Size)

	od, err := svc.Open(ctx, &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	defer od.Body.Close()
	b, err := io.ReadAll(od.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), b)
	assert.Equal(t, desc.Version, od.Version)

	hd, err := svc.Head(ctx, &artifact.HeadRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Version:   &desc.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, desc.Version, hd.Version)
	assert.Equal(t, desc.MimeType, hd.MimeType)
	assert.Equal(t, desc.Size, hd.Size)
}

func TestService_VersionsAndDeleteAll(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	appName := "test-app"
	userID := "user-123"
	sessionID := "session-456"
	name := "doc.txt"

	d1, err := svc.Put(ctx, &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      strings.NewReader("v1"),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)
	d2, err := svc.Put(ctx, &artifact.PutRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
		Body:      strings.NewReader("v2"),
		MimeType:  "text/plain",
	})
	require.NoError(t, err)

	vers, err := svc.Versions(ctx, &artifact.VersionsRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	assert.Len(t, vers.Versions, 2)
	assert.Contains(t, vers.Versions, d1.Version)
	assert.Contains(t, vers.Versions, d2.Version)

	_, err = svc.Delete(ctx, &artifact.DeleteRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	require.NoError(t, err)
	_, err = svc.Open(ctx, &artifact.OpenRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Name:      name,
	})
	assert.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestService_List_Paginates(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	appName := "test-app"
	userID := "user-123"
	sessionID := "session-456"

	_, _ = svc.Put(ctx, &artifact.PutRequest{AppName: appName, UserID: userID, SessionID: sessionID, Name: "a.txt", Body: strings.NewReader("a")})
	_, _ = svc.Put(ctx, &artifact.PutRequest{AppName: appName, UserID: userID, SessionID: sessionID, Name: "b.txt", Body: strings.NewReader("b")})
	_, _ = svc.Put(ctx, &artifact.PutRequest{AppName: appName, UserID: userID, SessionID: sessionID, Name: "c.txt", Body: strings.NewReader("c")})

	limit2 := 2
	page1, err := svc.List(ctx, &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Limit:     &limit2,
	})
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	require.NotEmpty(t, page1.NextPageToken)

	next := page1.NextPageToken
	page2, err := svc.List(ctx, &artifact.ListRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Limit:     &limit2,
		PageToken: &next,
	})
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	require.Empty(t, page2.NextPageToken)
}

func TestService_Put_ValidatesKey(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Put(ctx, &artifact.PutRequest{Name: "x", Body: strings.NewReader("x")})
	assert.ErrorIs(t, err, ErrEmptySessionInfo)

	_, err = svc.Put(ctx, &artifact.PutRequest{AppName: "a", UserID: "u", Name: "x", Body: strings.NewReader("x")})
	assert.NoError(t, err)

	_, err = svc.Put(ctx, &artifact.PutRequest{AppName: "a", UserID: "u", SessionID: "s", Name: "", Body: strings.NewReader("x")})
	assert.ErrorIs(t, err, ErrEmptyFilename)
}
