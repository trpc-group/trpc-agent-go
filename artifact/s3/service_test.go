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

func testKey(name string) artifact.Key {
	return artifact.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
		Name:      name,
	}
}

func TestService_PutOpenHead(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	desc, err := svc.Put(ctx, testKey("test.txt"), strings.NewReader("hello"), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	require.NotEmpty(t, desc.Version)
	assert.Equal(t, "text/plain", desc.MimeType)
	assert.Equal(t, int64(5), desc.Size)
	assert.Equal(t, "test.txt", desc.Key.Name)

	rc, od, err := svc.Open(ctx, testKey("test.txt"), nil)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), b)
	assert.Equal(t, desc.Version, od.Version)

	hd, err := svc.Head(ctx, testKey("test.txt"), &desc.Version)
	require.NoError(t, err)
	assert.Equal(t, desc.Version, hd.Version)
	assert.Equal(t, desc.MimeType, hd.MimeType)
	assert.Equal(t, desc.Size, hd.Size)
}

func TestService_VersionsAndDeleteAll(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	k := testKey("doc.txt")

	d1, err := svc.Put(ctx, k, strings.NewReader("v1"), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	d2, err := svc.Put(ctx, k, strings.NewReader("v2"), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)

	vers, err := svc.Versions(ctx, k)
	require.NoError(t, err)
	assert.Len(t, vers, 2)
	assert.Contains(t, vers, d1.Version)
	assert.Contains(t, vers, d2.Version)

	require.NoError(t, svc.Delete(ctx, k, artifact.DeleteAllOpt()))
	_, _, err = svc.Open(ctx, k, nil)
	assert.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestService_List_Paginates(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, _ = svc.Put(ctx, testKey("a.txt"), strings.NewReader("a"))
	_, _ = svc.Put(ctx, testKey("b.txt"), strings.NewReader("b"))
	_, _ = svc.Put(ctx, testKey("c.txt"), strings.NewReader("c"))

	ns := artifact.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	page1, next, err := svc.List(ctx, ns, artifact.WithListLimit(2))
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.NotEmpty(t, next)

	page2, next2, err := svc.List(ctx, ns, artifact.WithListLimit(2), artifact.WithListPageToken(next))
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Empty(t, next2)
}

func TestService_Put_ValidatesKey(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Put(ctx, artifact.Key{Name: "x"}, strings.NewReader("x"))
	assert.ErrorIs(t, err, ErrEmptySessionInfo)

	_, err = svc.Put(ctx, artifact.Key{AppName: "a", UserID: "u", Name: "x"}, strings.NewReader("x"))
	assert.NoError(t, err)

	_, err = svc.Put(ctx, artifact.Key{AppName: "a", UserID: "u", SessionID: "s", Name: ""}, strings.NewReader("x"))
	assert.ErrorIs(t, err, ErrEmptyFilename)
}
