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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	artifacts map[string][]stored
}

type stored struct {
	version artifact.VersionID
	mime    string
	data    []byte
}

// NewService creates a new in-memory artifact service.
func NewService() *Service {
	return &Service{
		artifacts: make(map[string][]stored),
	}
}

var _ artifact.Service = (*Service)(nil)

// Put stores artifact content and returns its metadata.
func (s *Service) Put(ctx context.Context, req *artifact.PutRequest, opts ...artifact.PutOption) (*artifact.PutResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if req == nil {
		return nil, fmt.Errorf("put request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, fmt.Errorf("put request body is nil")
	}
	_ = opts // reserved

	path := iartifact.BuildArtifactPath(req.AppName, req.UserID, req.SessionID, req.Name)
	v, err := artifact.NewVersionID()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.artifacts[path] = append(s.artifacts[path], stored{
		version: v,
		mime:    req.MimeType,
		data:    data,
	})

	return &artifact.PutResponse{
		Version:  v,
		MimeType: mimeOrDefault(req.MimeType),
		Size:     int64(len(data)),
	}, nil
}

// Head resolves an artifact version to its metadata.
func (s *Service) Head(ctx context.Context, req *artifact.HeadRequest, opts ...artifact.HeadOption) (*artifact.HeadResponse, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if req == nil {
		return nil, fmt.Errorf("head request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	path := iartifact.BuildArtifactPath(req.AppName, req.UserID, req.SessionID, req.Name)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, artifact.ErrNotFound
	}

	st, ok := resolveVersion(versions, req.Version)
	if !ok {
		return nil, artifact.ErrNotFound
	}

	return &artifact.HeadResponse{
		Version:  st.version,
		MimeType: mimeOrDefault(st.mime),
		Size:     int64(len(st.data)),
	}, nil
}

// Open returns a streaming reader for the artifact content and its descriptor.
func (s *Service) Open(ctx context.Context, req *artifact.OpenRequest, opts ...artifact.OpenOption) (*artifact.OpenResponse, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if req == nil {
		return nil, fmt.Errorf("open request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	path := iartifact.BuildArtifactPath(req.AppName, req.UserID, req.SessionID, req.Name)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, artifact.ErrNotFound
	}

	st, ok := resolveVersion(versions, req.Version)
	if !ok {
		return nil, artifact.ErrNotFound
	}

	return &artifact.OpenResponse{
		Body:     io.NopCloser(bytes.NewReader(st.data)),
		Version:  st.version,
		MimeType: mimeOrDefault(st.mime),
		Size:     int64(len(st.data)),
	}, nil
}

// List returns the latest version metadata for each artifact name under the given namespace.
func (s *Service) List(ctx context.Context, req *artifact.ListRequest, opts ...artifact.ListOption) (*artifact.ListResponse, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if req == nil {
		return nil, fmt.Errorf("list request is nil")
	}
	if err := validateListFields(req.AppName, req.UserID); err != nil {
		return nil, err
	}
	_ = opts // reserved

	scopePrefix := iartifact.BuildListPrefix(req.AppName, req.UserID, req.SessionID)
	names := make([]string, 0)
	latest := make(map[string]stored)
	for path, versions := range s.artifacts {
		if !strings.HasPrefix(path, scopePrefix) {
			continue
		}
		rel := strings.TrimPrefix(path, scopePrefix)
		if rel == "" {
			continue
		}
		st, ok := resolveVersion(versions, nil)
		if !ok {
			continue
		}
		if _, exists := latest[rel]; !exists {
			names = append(names, rel)
			latest[rel] = st
			continue
		}
		if artifact.CompareVersion(st.version, latest[rel].version) > 0 {
			latest[rel] = st
		}
	}

	sort.Strings(names)
	start := 0
	if req.PageToken != nil && *req.PageToken != "" {
		tok := *req.PageToken
		i := sort.SearchStrings(names, tok)
		for i < len(names) && names[i] <= tok {
			i++
		}
		start = i
	}
	limit := 0
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit <= 0 || limit > len(names)-start {
		limit = len(names) - start
	}
	end := start + limit
	page := names[start:end]

	out := make([]artifact.ListItem, 0, len(page))
	for _, name := range page {
		st := latest[name]
		out = append(out, artifact.ListItem{
			Name:     name,
			Version:  st.version,
			MimeType: mimeOrDefault(st.mime),
			Size:     int64(len(st.data)),
		})
	}

	next := ""
	if end < len(names) {
		next = page[len(page)-1]
	}
	return &artifact.ListResponse{Items: out, NextPageToken: next}, nil
}

// Delete is idempotent by default.
func (s *Service) Delete(ctx context.Context, req *artifact.DeleteRequest, opts ...artifact.DeleteOption) (*artifact.DeleteResponse, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if req == nil {
		return nil, fmt.Errorf("delete request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	path := iartifact.BuildArtifactPath(req.AppName, req.UserID, req.SessionID, req.Name)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return &artifact.DeleteResponse{Deleted: false}, nil
	}

	if req.Version == nil {
		delete(s.artifacts, path)
		return &artifact.DeleteResponse{Deleted: true}, nil
	}

	deleted, err := deleteOneVersionLocked(s.artifacts, path, *req.Version)
	if err != nil {
		return nil, err
	}
	return &artifact.DeleteResponse{Deleted: deleted}, nil
}

func deleteOneVersionLocked(m map[string][]stored, path string, ver artifact.VersionID) (bool, error) {
	versions, ok := m[path]
	if !ok || len(versions) == 0 {
		return false, nil
	}
	out := make([]stored, 0, len(versions))
	found := false
	for _, st := range versions {
		if st.version == ver {
			found = true
			continue
		}
		out = append(out, st)
	}
	if !found {
		return false, nil
	}
	if len(out) == 0 {
		delete(m, path)
		return true, nil
	}
	m[path] = out
	return true, nil
}

// Versions lists all versions available for the provided artifact.
func (s *Service) Versions(ctx context.Context, req *artifact.VersionsRequest, opts ...artifact.VersionsOption) (*artifact.VersionsResponse, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if req == nil {
		return nil, fmt.Errorf("versions request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	path := iartifact.BuildArtifactPath(req.AppName, req.UserID, req.SessionID, req.Name)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, artifact.ErrNotFound
	}

	result := make([]artifact.VersionID, 0, len(versions))
	for _, st := range versions {
		result = append(result, st.version)
	}
	sort.Slice(result, func(i, j int) bool {
		return artifact.CompareVersion(result[i], result[j]) < 0
	})
	return &artifact.VersionsResponse{Versions: result}, nil
}

func resolveVersion(versions []stored, version *artifact.VersionID) (stored, bool) {
	if len(versions) == 0 {
		return stored{}, false
	}
	if version == nil {
		latest := versions[0]
		for _, st := range versions[1:] {
			if artifact.CompareVersion(st.version, latest.version) > 0 {
				latest = st
			}
		}
		return latest, true
	}
	for _, st := range versions {
		if st.version == *version {
			return st, true
		}
	}
	return stored{}, false
}

func mimeOrDefault(mt string) string {
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}

func validateKeyFields(appName, userID, name string) error {
	if appName == "" || userID == "" {
		return fmt.Errorf("invalid key: missing appName or userID")
	}
	if name == "" {
		return fmt.Errorf("invalid key: empty name")
	}
	return validateObjectName(name)
}

func validateListFields(appName, userID string) error {
	if appName == "" || userID == "" {
		return fmt.Errorf("invalid namespace: missing appName or userID")
	}
	return nil
}

func (s *Service) mustHaveArtifact(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, artifact.ErrNotFound) {
		return err
	}
	return err
}

func validateObjectName(name string) error {
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid key: invalid name")
	}
	if strings.Contains(name, "\\") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid key: invalid name")
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("invalid key: invalid name")
		}
	}
	return nil
}

func validateObjectPrefix(prefix string) error {
	if strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("invalid prefix: invalid namePrefix")
	}
	if strings.Contains(prefix, "\\") || strings.Contains(prefix, "\x00") {
		return fmt.Errorf("invalid prefix: invalid namePrefix")
	}
	parts := strings.Split(prefix, "/")
	for i, p := range parts {
		if p == "." || p == ".." {
			return fmt.Errorf("invalid prefix: invalid namePrefix")
		}
		// Allow trailing slash: last segment may be empty.
		if p == "" && i != len(parts)-1 {
			return fmt.Errorf("invalid prefix: invalid namePrefix")
		}
	}
	return nil
}
