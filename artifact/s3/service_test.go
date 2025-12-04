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
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// mockStorage is a mock implementation of the storage interface for testing.
type mockStorage struct {
	mu      sync.RWMutex
	objects map[string]*mockObject
}

type mockObject struct {
	data        []byte
	contentType string
}

func newMockClient() *mockStorage {
	return &mockStorage{
		objects: make(map[string]*mockObject),
	}
}

func (m *mockStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.objects[key] = &mockObject{
		data:        append([]byte(nil), data...), // Copy data
		contentType: contentType,
	}
	return nil
}

func (m *mockStorage) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	obj, ok := m.objects[key]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append([]byte(nil), obj.data...), obj.contentType, nil
}

func (m *mockStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var keys []string
	for key := range m.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	// Sort for deterministic results
	sort.Strings(keys)

	return keys, nil
}

func (m *mockStorage) DeleteObjects(ctx context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		delete(m.objects, key)
	}
	return nil
}

// Test helpers

func newTestService(t *testing.T) (*Service, *mockStorage) {
	mock := newMockClient()
	svc, err := NewService("test-bucket",
		WithRegion("us-east-1"),
		withStorage(mock),
	)
	require.NoError(t, err)
	return svc, mock
}

func testSessionInfo() artifact.SessionInfo {
	return artifact.SessionInfo{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}
}

// Tests

func TestNewService(t *testing.T) {
	t.Run("with valid config", func(t *testing.T) {
		mock := newMockClient()
		svc, err := NewService("my-bucket",
			WithRegion("eu-west-1"),
			withStorage(mock),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("with empty bucket", func(t *testing.T) {
		_, err := NewService("",
			WithRegion("us-east-1"),
			withStorage(newMockClient()),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bucket is required")
	})

	t.Run("with all options", func(t *testing.T) {
		mock := newMockClient()
		svc, err := NewService("my-bucket",
			WithEndpoint("http://localhost:9000"),
			WithRegion("us-east-1"),
			WithCredentials("access", "secret"),
			WithSessionToken("token"),
			WithPathStyle(),
			WithRetries(5),
			withStorage(mock),
		)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestSaveArtifact(t *testing.T) {
	t.Run("save first version", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		art := &artifact.Artifact{
			Data:     []byte("hello world"),
			MimeType: "text/plain",
			Name:     "test.txt",
		}

		version, err := svc.SaveArtifact(ctx, info, "test.txt", art)
		require.NoError(t, err)
		assert.Equal(t, 0, version)
	})

	t.Run("save multiple versions", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save version 0
		art := &artifact.Artifact{Data: []byte("v0"), MimeType: "text/plain"}
		v0, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
		require.NoError(t, err)
		assert.Equal(t, 0, v0)

		// Save version 1
		art.Data = []byte("v1")
		v1, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
		require.NoError(t, err)
		assert.Equal(t, 1, v1)

		// Save version 2
		art.Data = []byte("v2")
		v2, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
		require.NoError(t, err)
		assert.Equal(t, 2, v2)
	})

	t.Run("save with user namespace", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		art := &artifact.Artifact{Data: []byte("user data"), MimeType: "text/plain"}
		version, err := svc.SaveArtifact(ctx, info, "user:profile.txt", art)
		require.NoError(t, err)
		assert.Equal(t, 0, version)
	})
}

func TestLoadArtifact(t *testing.T) {
	t.Run("load existing artifact", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save artifact
		original := &artifact.Artifact{
			Data:     []byte("test content"),
			MimeType: "text/plain",
			Name:     "test.txt",
		}
		_, err := svc.SaveArtifact(ctx, info, "test.txt", original)
		require.NoError(t, err)

		// Load artifact
		loaded, err := svc.LoadArtifact(ctx, info, "test.txt", nil)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Equal(t, original.Data, loaded.Data)
		assert.Equal(t, original.MimeType, loaded.MimeType)
	})

	t.Run("load specific version", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save multiple versions
		for i := 0; i < 3; i++ {
			art := &artifact.Artifact{
				Data:     []byte("version " + string(rune('0'+i))),
				MimeType: "text/plain",
			}
			_, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
			require.NoError(t, err)
		}

		// Load version 1
		version := 1
		loaded, err := svc.LoadArtifact(ctx, info, "doc.txt", &version)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Equal(t, []byte("version 1"), loaded.Data)
	})

	t.Run("load latest version", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save multiple versions
		for i := 0; i < 3; i++ {
			art := &artifact.Artifact{
				Data:     []byte("version " + string(rune('0'+i))),
				MimeType: "text/plain",
			}
			_, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
			require.NoError(t, err)
		}

		// Load latest (should be version 2)
		loaded, err := svc.LoadArtifact(ctx, info, "doc.txt", nil)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Equal(t, []byte("version 2"), loaded.Data)
	})

	t.Run("load non-existent artifact", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		loaded, err := svc.LoadArtifact(ctx, info, "nonexistent.txt", nil)
		require.NoError(t, err)
		assert.Nil(t, loaded)
	})

	t.Run("load with user namespace", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save with user namespace
		original := &artifact.Artifact{Data: []byte("user data"), MimeType: "text/plain"}
		_, err := svc.SaveArtifact(ctx, info, "user:profile.txt", original)
		require.NoError(t, err)

		// Load with user namespace
		loaded, err := svc.LoadArtifact(ctx, info, "user:profile.txt", nil)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Equal(t, original.Data, loaded.Data)
	})
}

func TestListArtifactKeys(t *testing.T) {
	t.Run("list empty", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		keys, err := svc.ListArtifactKeys(ctx, info)
		require.NoError(t, err)
		assert.Empty(t, keys)
	})

	t.Run("list session artifacts", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save some artifacts
		files := []string{"doc1.pdf", "doc2.pdf", "image.png"}
		for _, name := range files {
			art := &artifact.Artifact{Data: []byte("data"), MimeType: "application/octet-stream"}
			_, err := svc.SaveArtifact(ctx, info, name, art)
			require.NoError(t, err)
		}

		keys, err := svc.ListArtifactKeys(ctx, info)
		require.NoError(t, err)
		assert.ElementsMatch(t, files, keys)
	})

	t.Run("list both session and user artifacts", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save session-scoped artifacts
		art := &artifact.Artifact{Data: []byte("data"), MimeType: "text/plain"}
		_, err := svc.SaveArtifact(ctx, info, "session-doc.txt", art)
		require.NoError(t, err)

		// Save user-scoped artifacts
		_, err = svc.SaveArtifact(ctx, info, "user:user-doc.txt", art)
		require.NoError(t, err)

		keys, err := svc.ListArtifactKeys(ctx, info)
		require.NoError(t, err)
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "session-doc.txt")
		assert.Contains(t, keys, "user:user-doc.txt")
	})

	t.Run("list with multiple versions", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save multiple versions of same file
		for i := 0; i < 3; i++ {
			art := &artifact.Artifact{Data: []byte("v"), MimeType: "text/plain"}
			_, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
			require.NoError(t, err)
		}

		// Should only list filename once
		keys, err := svc.ListArtifactKeys(ctx, info)
		require.NoError(t, err)
		assert.Equal(t, []string{"doc.txt"}, keys)
	})
}

func TestDeleteArtifact(t *testing.T) {
	t.Run("delete existing artifact", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save artifact
		art := &artifact.Artifact{Data: []byte("data"), MimeType: "text/plain"}
		_, err := svc.SaveArtifact(ctx, info, "test.txt", art)
		require.NoError(t, err)

		// Delete artifact
		err = svc.DeleteArtifact(ctx, info, "test.txt")
		require.NoError(t, err)

		// Verify deleted
		loaded, err := svc.LoadArtifact(ctx, info, "test.txt", nil)
		require.NoError(t, err)
		assert.Nil(t, loaded)
	})

	t.Run("delete all versions", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save multiple versions
		for i := 0; i < 3; i++ {
			art := &artifact.Artifact{Data: []byte("v"), MimeType: "text/plain"}
			_, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
			require.NoError(t, err)
		}

		// Delete artifact (should delete all versions)
		err := svc.DeleteArtifact(ctx, info, "doc.txt")
		require.NoError(t, err)

		// Verify all versions deleted
		versions, err := svc.ListVersions(ctx, info, "doc.txt")
		require.NoError(t, err)
		assert.Empty(t, versions)
	})

	t.Run("delete non-existent artifact", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Should not error
		err := svc.DeleteArtifact(ctx, info, "nonexistent.txt")
		require.NoError(t, err)
	})
}

func TestListVersions(t *testing.T) {
	t.Run("list versions", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		// Save multiple versions
		for i := 0; i < 5; i++ {
			art := &artifact.Artifact{Data: []byte("v"), MimeType: "text/plain"}
			_, err := svc.SaveArtifact(ctx, info, "doc.txt", art)
			require.NoError(t, err)
		}

		versions, err := svc.ListVersions(ctx, info, "doc.txt")
		require.NoError(t, err)
		assert.Equal(t, []int{0, 1, 2, 3, 4}, versions)
	})

	t.Run("list versions of non-existent artifact", func(t *testing.T) {
		svc, _ := newTestService(t)
		ctx := context.Background()
		info := testSessionInfo()

		versions, err := svc.ListVersions(ctx, info, "nonexistent.txt")
		require.NoError(t, err)
		assert.Empty(t, versions)
	})
}

func TestExtractFilename(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		prefix   string
		expected string
	}{
		{
			name:     "session scoped",
			key:      "app/user/session/doc.pdf/0",
			prefix:   "app/user/session/",
			expected: "doc.pdf",
		},
		{
			name:     "user scoped",
			key:      "app/user/user/user:profile.txt/0",
			prefix:   "app/user/user/",
			expected: "user:profile.txt",
		},
		{
			name:     "no match",
			key:      "other/path/file.txt",
			prefix:   "app/user/session/",
			expected: "",
		},
		{
			name:     "empty relative",
			key:      "app/user/session/",
			prefix:   "app/user/session/",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractFilename(tc.key, tc.prefix)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// errorMockStorage is a mock storage that can be configured to return errors.
type errorMockStorage struct {
	mockStorage
	listObjectsErr   error
	putObjectErr     error
	getObjectErr     error
	deleteObjectsErr error
}

func newErrorMockStorage() *errorMockStorage {
	return &errorMockStorage{
		mockStorage: mockStorage{
			objects: make(map[string]*mockObject),
		},
	}
}

func (m *errorMockStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	if m.listObjectsErr != nil {
		return nil, m.listObjectsErr
	}
	return m.mockStorage.ListObjects(ctx, prefix)
}

func (m *errorMockStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	if m.putObjectErr != nil {
		return m.putObjectErr
	}
	return m.mockStorage.PutObject(ctx, key, data, contentType)
}

func (m *errorMockStorage) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	if m.getObjectErr != nil {
		return nil, "", m.getObjectErr
	}
	return m.mockStorage.GetObject(ctx, key)
}

func (m *errorMockStorage) DeleteObjects(ctx context.Context, keys []string) error {
	if m.deleteObjectsErr != nil {
		return m.deleteObjectsErr
	}
	return m.mockStorage.DeleteObjects(ctx, keys)
}

func TestServiceErrorPaths(t *testing.T) {
	ctx := context.Background()
	sessionInfo := testSessionInfo()
	testErr := errors.New("test error")

	t.Run("SaveArtifact returns error when ListVersions fails", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		_, err = svc.SaveArtifact(ctx, sessionInfo, "test.txt", &artifact.Artifact{
			Data:     []byte("test"),
			MimeType: "text/plain",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list versions")
	})

	t.Run("SaveArtifact returns error when PutObject fails", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.putObjectErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		_, err = svc.SaveArtifact(ctx, sessionInfo, "test.txt", &artifact.Artifact{
			Data:     []byte("test"),
			MimeType: "text/plain",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to upload artifact")
	})

	t.Run("LoadArtifact returns error when ListVersions fails", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		_, err = svc.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list versions")
	})

	t.Run("LoadArtifact returns error when GetObject fails with non-NotFound error", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.getObjectErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		// First save an artifact so ListVersions returns a version
		mock.getObjectErr = nil
		_, err = svc.SaveArtifact(ctx, sessionInfo, "test.txt", &artifact.Artifact{
			Data:     []byte("test"),
			MimeType: "text/plain",
		})
		require.NoError(t, err)

		// Now set the error and try to load
		mock.getObjectErr = testErr
		_, err = svc.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to download artifact")
	})

	t.Run("LoadArtifact returns nil when GetObject returns NotFound", func(t *testing.T) {
		mock := newErrorMockStorage()

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		// Save an artifact first
		_, err = svc.SaveArtifact(ctx, sessionInfo, "test.txt", &artifact.Artifact{
			Data:     []byte("test"),
			MimeType: "text/plain",
		})
		require.NoError(t, err)

		// Set NotFound error
		mock.getObjectErr = ErrNotFound
		art, err := svc.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
		assert.NoError(t, err)
		assert.Nil(t, art)
	})

	t.Run("ListArtifactKeys returns error when listing session artifacts fails", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		_, err = svc.ListArtifactKeys(ctx, sessionInfo)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list session artifacts")
	})

	t.Run("DeleteArtifact returns error when ListObjects fails with non-NotFound error", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		err = svc.DeleteArtifact(ctx, sessionInfo, "test.txt")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list artifact versions")
	})

	t.Run("DeleteArtifact returns nil when ListObjects returns NotFound", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = ErrNotFound

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		err = svc.DeleteArtifact(ctx, sessionInfo, "test.txt")
		assert.NoError(t, err)
	})

	t.Run("DeleteArtifact returns error when DeleteObjects fails", func(t *testing.T) {
		mock := newErrorMockStorage()

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		// Save an artifact first
		_, err = svc.SaveArtifact(ctx, sessionInfo, "test.txt", &artifact.Artifact{
			Data:     []byte("test"),
			MimeType: "text/plain",
		})
		require.NoError(t, err)

		// Set delete error
		mock.deleteObjectsErr = testErr
		err = svc.DeleteArtifact(ctx, sessionInfo, "test.txt")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete artifact")
	})

	t.Run("ListVersions returns empty slice when ListObjects returns NotFound", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = ErrNotFound

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		versions, err := svc.ListVersions(ctx, sessionInfo, "test.txt")
		assert.NoError(t, err)
		assert.Empty(t, versions)
	})

	t.Run("ListVersions returns error when ListObjects fails with non-NotFound error", func(t *testing.T) {
		mock := newErrorMockStorage()
		mock.listObjectsErr = testErr

		svc, err := NewService("test-bucket", WithRegion("us-east-1"), withStorage(mock))
		require.NoError(t, err)

		_, err = svc.ListVersions(ctx, sessionInfo, "test.txt")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list versions")
	})
}
