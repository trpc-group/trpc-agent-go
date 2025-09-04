//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory implementation of the artifact service.
package inmemory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	iartifact "trpc.group/trpc-go/trpc-agent-go/internal/artifact"
)

// Service is an in-memory implementation of the artifact service.
// It is suitable for testing and development environments.
type Service struct {
	// mutex protects concurrent access to the artifacts map
	mutex sync.RWMutex
	// artifacts stores artifacts by path, with each path containing a list of versions
	artifacts map[string][]*artifact.Artifact
}

// NewService creates a new in-memory artifact service.
func NewService() *Service {
	return &Service{
		artifacts: make(map[string][]*artifact.Artifact),
	}
}

// SaveArtifact saves an artifact to the in-memory storage.
func (s *Service) SaveArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, art *artifact.Artifact) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	path := iartifact.BuildArtifactPath(sessionInfo, filename)
	if s.artifacts[path] == nil {
		s.artifacts[path] = make([]*artifact.Artifact, 0)
	}

	version := len(s.artifacts[path])
	s.artifacts[path] = append(s.artifacts[path], art)

	return version, nil
}

// LoadArtifact gets an artifact from the in-memory storage.
func (s *Service) LoadArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, version *int) (*artifact.Artifact, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := iartifact.BuildArtifactPath(sessionInfo, filename)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, nil
	}

	var versionIndex int
	if version == nil {
		// Get the latest version (last element)
		versionIndex = len(versions) - 1
	} else {
		versionIndex = *version
		if versionIndex < 0 || versionIndex >= len(versions) {
			return nil, fmt.Errorf("version %d does not exist", *version)
		}
	}

	return versions[versionIndex], nil
}

// ListArtifactKeys lists all the artifact filenames within a session.
func (s *Service) ListArtifactKeys(ctx context.Context, sessionInfo artifact.SessionInfo) ([]string, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	sessionPrefix := iartifact.BuildSessionPrefix(sessionInfo)
	usernamespacePrefix := iartifact.BuildUserNamespacePrefix(sessionInfo)

	var filenames []string
	for path := range s.artifacts {
		if strings.HasPrefix(path, sessionPrefix) {
			filename := strings.TrimPrefix(path, sessionPrefix)
			filenames = append(filenames, filename)
		} else if strings.HasPrefix(path, usernamespacePrefix) {
			filename := strings.TrimPrefix(path, usernamespacePrefix)
			filenames = append(filenames, filename)
		}
	}

	sort.Strings(filenames)
	return filenames, nil
}

// DeleteArtifact deletes an artifact.
func (s *Service) DeleteArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	path := iartifact.BuildArtifactPath(sessionInfo, filename)
	if _, exists := s.artifacts[path]; !exists {
		// Artifact doesn't exist, but this is not an error in the Python implementation
		return nil
	}

	delete(s.artifacts, path)
	return nil
}

// ListVersions lists all versions of an artifact.
func (s *Service) ListVersions(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) ([]int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := iartifact.BuildArtifactPath(sessionInfo, filename)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return []int{}, nil
	}

	result := make([]int, len(versions))
	for i := range versions {
		result[i] = i
	}

	return result, nil
}
