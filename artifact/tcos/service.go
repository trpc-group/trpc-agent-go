//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcos provides a Tencent Cloud Object Storage (COS) implementation of the artifact service.
package tcos

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// Service is a Tencent Cloud Object Storage implementation of the artifact service.
// It provides cloud-based storage for artifacts using Tencent COS.
type Service struct {
	// TODO: Add COS client and configuration fields
}

// NewService creates a new TCOS artifact service.
func NewService() *Service {
	return &Service{
		// TODO: Initialize COS client
	}
}

// SaveArtifact saves an artifact to Tencent Cloud Object Storage.
func (s *Service) SaveArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, art *artifact.Artifact) (int, error) {
	// TODO: Implement TCOS artifact saving
	panic("not implemented")
}

// LoadArtifact gets an artifact from Tencent Cloud Object Storage.
func (s *Service) LoadArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, version *int) (*artifact.Artifact, error) {
	// TODO: Implement TCOS artifact loading
	panic("not implemented")
}

// ListArtifactKeys lists all the artifact filenames within a session from TCOS.
func (s *Service) ListArtifactKeys(ctx context.Context, sessionInfo artifact.SessionInfo) ([]string, error) {
	// TODO: Implement TCOS artifact key listing
	panic("not implemented")
}

// DeleteArtifact deletes an artifact from Tencent Cloud Object Storage.
func (s *Service) DeleteArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) error {
	// TODO: Implement TCOS artifact deletion
	panic("not implemented")
}

// ListVersions lists all versions of an artifact from TCOS.
func (s *Service) ListVersions(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) ([]int, error) {
	// TODO: Implement TCOS artifact version listing
	panic("not implemented")
}
