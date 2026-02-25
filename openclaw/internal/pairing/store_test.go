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
	"path/filepath"
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
