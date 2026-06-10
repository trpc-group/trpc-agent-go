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
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	errCandidateMemoryWriteDisabled   = errors.New("candidate selector: memory writes are disabled in candidate attempts")
	errCandidateArtifactWriteDisabled = errors.New("candidate selector: artifact writes are disabled in candidate attempts")
)

type readOnlyMemoryService struct {
	base memory.Service
}

func newReadOnlyMemoryService(base memory.Service) memory.Service {
	if base == nil {
		return nil
	}
	return &readOnlyMemoryService{base: base}
}

func (s *readOnlyMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return errCandidateMemoryWriteDisabled
}

func (s *readOnlyMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return errCandidateMemoryWriteDisabled
}

func (s *readOnlyMemoryService) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	return errCandidateMemoryWriteDisabled
}

func (s *readOnlyMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return errCandidateMemoryWriteDisabled
}

func (s *readOnlyMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return s.base.ReadMemories(ctx, userKey, limit)
}

func (s *readOnlyMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.base.SearchMemories(ctx, userKey, query, opts...)
}

func (s *readOnlyMemoryService) Tools() []tool.Tool {
	return nil
}

func (s *readOnlyMemoryService) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	return errCandidateMemoryWriteDisabled
}

func (s *readOnlyMemoryService) Close() error {
	return nil
}

type readOnlyArtifactService struct {
	base artifact.Service
}

func newReadOnlyArtifactService(base artifact.Service) artifact.Service {
	if base == nil {
		return nil
	}
	return &readOnlyArtifactService{base: base}
}

func (s *readOnlyArtifactService) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	value *artifact.Artifact,
) (int, error) {
	return 0, errCandidateArtifactWriteDisabled
}

func (s *readOnlyArtifactService) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	return s.base.LoadArtifact(ctx, sessionInfo, filename, version)
}

func (s *readOnlyArtifactService) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	return s.base.ListArtifactKeys(ctx, sessionInfo)
}

func (s *readOnlyArtifactService) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	return errCandidateArtifactWriteDisabled
}

func (s *readOnlyArtifactService) ListVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	return s.base.ListVersions(ctx, sessionInfo, filename)
}
