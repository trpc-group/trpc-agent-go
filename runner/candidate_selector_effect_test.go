//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestReadOnlyMemoryService_AllowsReadsAndRejectsWrites(t *testing.T) {
	base := &fakeMemoryService{
		entries: []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "remembered"}}},
	}
	readonly := newReadOnlyMemoryService(base)
	entries, err := readonly.ReadMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.NoError(t, err)
	assert.Equal(t, "m1", entries[0].ID)
	err = readonly.AddMemory(context.Background(), memory.UserKey{AppName: "app", UserID: "user"}, "new", nil)
	require.ErrorIs(t, err, errCandidateMemoryWriteDisabled)
	assert.Equal(t, 0, base.addCalls)
}

func TestReadOnlyMemoryService_RejectsAllWritesAndPreservesReads(t *testing.T) {
	assert.Nil(t, newReadOnlyMemoryService(nil))
	base := &fakeMemoryService{
		entries: []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "remembered"}}},
	}
	readonly := newReadOnlyMemoryService(base)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	memoryKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "m1"}

	err := readonly.UpdateMemory(ctx, memoryKey, "updated", nil)
	require.ErrorIs(t, err, errCandidateMemoryWriteDisabled)
	err = readonly.DeleteMemory(ctx, memoryKey)
	require.ErrorIs(t, err, errCandidateMemoryWriteDisabled)
	err = readonly.ClearMemories(ctx, userKey)
	require.ErrorIs(t, err, errCandidateMemoryWriteDisabled)
	err = readonly.EnqueueAutoMemoryJob(ctx, session.NewSession("app", "user", "session"))
	require.ErrorIs(t, err, errCandidateMemoryWriteDisabled)
	assert.Empty(t, readonly.Tools())
	assert.NoError(t, readonly.Close())

	entries, err := readonly.SearchMemories(ctx, userKey, "remembered")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "m1", entries[0].ID)
}

func TestReadOnlyArtifactService_AllowsReadsAndRejectsWrites(t *testing.T) {
	base := &fakeArtifactService{
		value: &artifact.Artifact{Data: []byte("data")},
	}
	readonly := newReadOnlyArtifactService(base)
	got, err := readonly.LoadArtifact(context.Background(), artifact.SessionInfo{AppName: "app", UserID: "user", SessionID: "session"}, "a.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), got.Data)
	_, err = readonly.SaveArtifact(context.Background(), artifact.SessionInfo{AppName: "app", UserID: "user", SessionID: "session"}, "a.txt", got)
	require.ErrorIs(t, err, errCandidateArtifactWriteDisabled)
	assert.Equal(t, 0, base.saveCalls)
}

func TestReadOnlyArtifactService_RejectsWritesAndDelegatesReads(t *testing.T) {
	assert.Nil(t, newReadOnlyArtifactService(nil))
	base := &fakeArtifactService{
		value: &artifact.Artifact{Data: []byte("data")},
	}
	readonly := newReadOnlyArtifactService(base)
	ctx := context.Background()
	sessionInfo := artifact.SessionInfo{AppName: "app", UserID: "user", SessionID: "session"}

	keys, err := readonly.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, keys)
	versions, err := readonly.ListVersions(ctx, sessionInfo, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, []int{0}, versions)
	err = readonly.DeleteArtifact(ctx, sessionInfo, "a.txt")
	require.ErrorIs(t, err, errCandidateArtifactWriteDisabled)
}

type fakeMemoryService struct {
	entries  []*memory.Entry
	addCalls int
}

func (s *fakeMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
	opts ...memory.AddOption,
) error {
	s.addCalls++
	return nil
}

func (s *fakeMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return nil
}

func (s *fakeMemoryService) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	return nil
}

func (s *fakeMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return nil
}

func (s *fakeMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return s.entries, nil
}

func (s *fakeMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.entries, nil
}

func (s *fakeMemoryService) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (s *fakeMemoryService) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	return nil
}

func (s *fakeMemoryService) Close() error {
	return nil
}

type fakeArtifactService struct {
	value     *artifact.Artifact
	saveCalls int
}

func (s *fakeArtifactService) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	value *artifact.Artifact,
) (int, error) {
	s.saveCalls++
	return 0, nil
}

func (s *fakeArtifactService) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	return s.value, nil
}

func (s *fakeArtifactService) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	return []string{"a.txt"}, nil
}

func (s *fakeArtifactService) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	return nil
}

func (s *fakeArtifactService) ListVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	return []int{0}, nil
}
