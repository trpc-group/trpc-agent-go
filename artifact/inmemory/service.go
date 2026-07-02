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

const defaultContentType = "application/octet-stream"

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

// Head returns metadata for an artifact without loading its full content.
func (s *Service) Head(
	ctx context.Context,
	req *artifact.HeadRequest,
	opts ...artifact.HeadOption,
) (*artifact.HeadResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("head request is nil")
	}
	if req.SessionInfo.AppName == "" || req.SessionInfo.UserID == "" || req.SessionInfo.SessionID == "" {
		return nil, fmt.Errorf("session info fields cannot be empty")
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("filename cannot be empty")
	}
	if req.Version != nil && *req.Version < 0 {
		return nil, fmt.Errorf("version must be >= 0")
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := iartifact.BuildArtifactPath(req.SessionInfo, req.Filename)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, nil
	}

	targetVersion := 0
	if req.Version == nil {
		targetVersion = len(versions) - 1
	} else {
		targetVersion = *req.Version
	}
	if targetVersion < 0 || targetVersion >= len(versions) {
		return nil, nil
	}

	art := versions[targetVersion]
	if art == nil {
		return nil, nil
	}

	mt := art.MimeType
	if mt == "" {
		mt = defaultContentType
	}
	name := strings.TrimSpace(art.Name)
	if name == "" {
		name = req.Filename
	}

	o := artifact.HeadOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&o)
	}

	url := ""
	if o.IncludeURL {
		url = art.URL
	}

	return &artifact.HeadResponse{
		Filename: req.Filename,
		Version:  targetVersion,
		Size:     int64(len(art.Data)),
		MimeType: mt,
		URL:      url,
		Name:     name,
	}, nil
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
