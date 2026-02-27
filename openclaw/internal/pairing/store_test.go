//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pairing

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewFileStore_Validation(t *testing.T) {
	t.Parallel()

	_, err := NewFileStore("")
	require.Error(t, err)

	_, err = NewFileStore("x", WithTTL(0))
	require.Error(t, err)
}

func TestFileStore_RequestApproveFlow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path, WithTTL(time.Hour))
	require.NoError(t, err)

	ctx := context.Background()

	code, approved, err := s.Request(ctx, "u1")
	require.NoError(t, err)
	require.False(t, approved)
	require.NotEmpty(t, code)

	ok, err := s.IsApproved(ctx, "u1")
	require.NoError(t, err)
	require.False(t, ok)

	pending, err := s.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, code, pending[0].Code)
	require.Equal(t, "u1", pending[0].UserID)

	userID, ok, err := s.Approve(ctx, code)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "u1", userID)

	ok, err = s.IsApproved(ctx, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	code, approved, err = s.Request(ctx, "u1")
	require.NoError(t, err)
	require.True(t, approved)
	require.Empty(t, code)
}

func TestFileStore_Request_ReusesPendingCode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx := context.Background()

	code1, approved, err := s.Request(ctx, "u1")
	require.NoError(t, err)
	require.False(t, approved)

	code2, approved, err := s.Request(ctx, "u1")
	require.NoError(t, err)
	require.False(t, approved)
	require.Equal(t, code1, code2)
}

func TestFileStore_Approve_UnknownCode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	userID, ok, err := s.Approve(context.Background(), "999999")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, userID)
}

func TestFileStore_Approve_EmptyCode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, _, err = s.Approve(context.Background(), " ")
	require.Error(t, err)
}

func TestFileStore_Request_EmptyUserID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, _, err = s.Request(context.Background(), " ")
	require.Error(t, err)
}

func TestFileStore_IsApproved_EmptyUserID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, err = s.IsApproved(context.Background(), " ")
	require.Error(t, err)
}

func TestFileStore_IsApproved_ContextCancelled(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = s.IsApproved(ctx, "u1")
	require.Error(t, err)
}

func TestFileStore_Reload_ReadError(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, err = s.IsApproved(context.Background(), "u1")
	require.Error(t, err)
}

func TestFileStore_Reload_DecodeError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, err = s.IsApproved(context.Background(), "u1")
	require.Error(t, err)
}

func TestFileStore_Reload_UnexpectedVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":999}`),
		0o600,
	))

	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, err = s.IsApproved(context.Background(), "u1")
	require.Error(t, err)
}

func TestFileStore_Request_CreateDirError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))

	path := filepath.Join(parent, "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	_, _, err = s.Request(context.Background(), "u1")
	require.Error(t, err)
}

func TestFileStore_ExpiresPending(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path, WithTTL(5*time.Millisecond))
	require.NoError(t, err)

	code, approved, err := s.Request(context.Background(), "u1")
	require.NoError(t, err)
	require.False(t, approved)

	time.Sleep(10 * time.Millisecond)

	pending, err := s.ListPending(context.Background())
	require.NoError(t, err)
	require.Empty(t, pending)

	userID, ok, err := s.Approve(context.Background(), code)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, userID)
}

func TestFileStore_ReloadsOnExternalChange(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s1, err := NewFileStore(path)
	require.NoError(t, err)
	s2, err := NewFileStore(path)
	require.NoError(t, err)

	ctx := context.Background()

	code, approved, err := s1.Request(ctx, "u1")
	require.NoError(t, err)
	require.False(t, approved)

	userID, ok, err := s2.Approve(ctx, code)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "u1", userID)

	ok, err = s1.IsApproved(ctx, "u1")
	require.NoError(t, err)
	require.True(t, ok)
}

type repeatingReader struct {
	b byte
}

func (r repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

type nthErrContext struct {
	context.Context

	after int
	err   error

	calls int
}

func (c *nthErrContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return c.err
	}
	return nil
}

func TestFileStore_NewCodeLocked_Exhausted(t *testing.T) {
	old := rand.Reader
	rand.Reader = repeatingReader{b: 0}
	t.Cleanup(func() { rand.Reader = old })

	s := &FileStore{
		state: state{
			Pending: map[string]pendingUser{
				"000000": {UserID: "u1"},
			},
		},
	}
	_, err := s.newCodeLocked()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to allocate code")
}

func TestFileStore_Request_NewCodeLockedError(t *testing.T) {
	old := rand.Reader
	rand.Reader = repeatingReader{b: 0}
	t.Cleanup(func() { rand.Reader = old })

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	s.mu.Lock()
	s.state.Pending["000000"] = pendingUser{
		UserID:    "u2",
		ExpiresAt: time.Now().Add(time.Hour).UTC().UnixMilli(),
	}
	s.mu.Unlock()

	_, _, err = s.Request(context.Background(), "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to allocate code")
}

func TestFileStore_Request_WriteLocked_ContextError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx := &nthErrContext{
		Context: context.Background(),
		after:   2,
		err:     context.Canceled,
	}
	_, _, err = s.Request(ctx, "u1")
	require.Error(t, err)
}

func TestFileStore_Approve_WriteLocked_ContextError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	expiresAt := time.Now().Add(time.Hour).UTC().UnixMilli()
	s.mu.Lock()
	s.state.Pending["111111"] = pendingUser{
		UserID:    "u1",
		CreatedAt: time.Now().UTC().UnixMilli(),
		ExpiresAt: expiresAt,
	}
	s.mu.Unlock()

	ctx := &nthErrContext{
		Context: context.Background(),
		after:   2,
		err:     context.Canceled,
	}
	_, _, err = s.Approve(ctx, "111111")
	require.Error(t, err)
}

func TestFileStore_Approve_ContextCancelled(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = s.Approve(ctx, "111111")
	require.Error(t, err)
}

func TestFileStore_WriteLocked_ContextCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	require.Error(t, s.writeLocked(ctx))
}

func TestFileStore_WriteLocked_CreateDirError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))

	path := filepath.Join(parent, "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	s.mu.Lock()
	err = s.writeLocked(context.Background())
	s.mu.Unlock()

	require.Error(t, err)
	require.Contains(t, err.Error(), "create dir")
}

func TestFileStore_WriteLocked_WriteFileError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod differs on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	s.mu.Lock()
	err = s.writeLocked(context.Background())
	s.mu.Unlock()

	require.Error(t, err)
	require.Contains(t, err.Error(), "write store")
}

func TestFileStore_WriteLocked_RenameError_RemovesTemp(t *testing.T) {
	const fixedHex = "0000000000000000"

	old := rand.Reader
	rand.Reader = repeatingReader{b: 0}
	t.Cleanup(func() { rand.Reader = old })

	path := t.TempDir()
	s, err := NewFileStore(path)
	require.NoError(t, err)

	s.mu.Lock()
	err = s.writeLocked(context.Background())
	s.mu.Unlock()
	require.Error(t, err)
	require.Contains(t, err.Error(), "rename store")

	tmp := path + "." + fixedHex + ".tmp"
	_, statErr := os.Stat(tmp)
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestFileStore_SameFileLocked_NilInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	filePath := filepath.Join(t.TempDir(), "x")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))

	st, err := os.Stat(filePath)
	require.NoError(t, err)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileInfo = nil
	require.False(t, s.sameFileLocked(st))
}

func TestFileStore_ListPending_ContextCancelled(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = s.ListPending(ctx)
	require.Error(t, err)
}

func TestFileStore_ListPending_SortsByCreatedAt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pairing.json")
	s, err := NewFileStore(path)
	require.NoError(t, err)

	now := time.Now().UTC()
	exp := now.Add(time.Hour).UnixMilli()

	s.mu.Lock()
	s.state.Pending["c1"] = pendingUser{
		UserID:    "u1",
		CreatedAt: now.Add(-time.Minute).UnixMilli(),
		ExpiresAt: exp,
	}
	s.state.Pending["c2"] = pendingUser{
		UserID:    "u2",
		CreatedAt: now.UnixMilli(),
		ExpiresAt: exp,
	}
	s.mu.Unlock()

	out, err := s.ListPending(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "c1", out[0].Code)
	require.Equal(t, "c2", out[1].Code)
}

func TestFileStore_PendingCodeLocked_IgnoresOtherUsers(t *testing.T) {
	s := &FileStore{
		state: state{
			Pending: map[string]pendingUser{
				"123456": {UserID: "u1"},
			},
		},
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	require.Empty(t, s.pendingCodeLocked(time.Now(), "u2"))
}

func TestFileStore_PendingCodeLocked_Expired(t *testing.T) {
	t.Parallel()

	expiredAt := time.Now().Add(-time.Minute).UTC().UnixMilli()
	s := &FileStore{
		state: state{
			Pending: map[string]pendingUser{
				"123456": {
					UserID:    "u1",
					ExpiresAt: expiredAt,
				},
			},
		},
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	require.Empty(t, s.pendingCodeLocked(time.Now(), "u1"))
}
