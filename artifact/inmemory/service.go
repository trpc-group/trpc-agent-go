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

// Put stores artifact content and returns its descriptor.
func (s *Service) Put(
	ctx context.Context,
	key artifact.Key,
	r io.Reader,
	opts ...artifact.PutOption,
) (artifact.Descriptor, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if err := validateKey(key); err != nil {
		return artifact.Descriptor{}, err
	}

	o := artifact.PutOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	path := iartifact.BuildArtifactPath(key)
	v, err := artifact.NewVersionID()
	if err != nil {
		return artifact.Descriptor{}, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	s.artifacts[path] = append(s.artifacts[path], stored{
		version: v,
		mime:    o.MimeType,
		data:    data,
	})

	return artifact.Descriptor{
		Key:      key,
		Version:  v,
		MimeType: mimeOrDefault(o.MimeType),
		Size:     int64(len(data)),
	}, nil
}

// Head resolves an artifact version to its descriptor.
func (s *Service) Head(
	ctx context.Context,
	key artifact.Key,
	version *artifact.VersionID,
) (artifact.Descriptor, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if err := validateKey(key); err != nil {
		return artifact.Descriptor{}, err
	}
	path := iartifact.BuildArtifactPath(key)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return artifact.Descriptor{}, artifact.ErrNotFound
	}

	st, ok := resolveVersion(versions, version)
	if !ok {
		return artifact.Descriptor{}, artifact.ErrNotFound
	}

	return artifact.Descriptor{
		Key:      key,
		Version:  st.version,
		MimeType: mimeOrDefault(st.mime),
		Size:     int64(len(st.data)),
	}, nil
}

// Open returns a streaming reader for the artifact content and its descriptor.
func (s *Service) Open(
	ctx context.Context,
	key artifact.Key,
	version *artifact.VersionID,
) (io.ReadCloser, artifact.Descriptor, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if err := validateKey(key); err != nil {
		return nil, artifact.Descriptor{}, err
	}
	path := iartifact.BuildArtifactPath(key)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return nil, artifact.Descriptor{}, artifact.ErrNotFound
	}

	st, ok := resolveVersion(versions, version)
	if !ok {
		return nil, artifact.Descriptor{}, artifact.ErrNotFound
	}

	desc := artifact.Descriptor{
		Key:      key,
		Version:  st.version,
		MimeType: mimeOrDefault(st.mime),
		Size:     int64(len(st.data)),
	}
	return io.NopCloser(bytes.NewReader(st.data)), desc, nil
}

// List returns the latest version descriptor for each artifact name under the given prefix.
func (s *Service) List(
	ctx context.Context,
	prefix artifact.KeyPrefix,
	opts ...artifact.ListOption,
) ([]artifact.Descriptor, string, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if err := validatePrefix(prefix); err != nil {
		return nil, "", err
	}

	o := artifact.ListOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	scopePrefix := iartifact.BuildListPrefix(prefix)
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
		if prefix.NamePrefix != "" && !strings.HasPrefix(rel, prefix.NamePrefix) {
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
	if o.PageToken != "" {
		i := sort.SearchStrings(names, o.PageToken)
		for i < len(names) && names[i] <= o.PageToken {
			i++
		}
		start = i
	}
	limit := o.Limit
	if limit <= 0 || limit > len(names)-start {
		limit = len(names) - start
	}
	end := start + limit
	page := names[start:end]

	out := make([]artifact.Descriptor, 0, len(page))
	for _, name := range page {
		st := latest[name]
		key := artifact.Key{
			AppName:   prefix.AppName,
			UserID:    prefix.UserID,
			SessionID: prefix.SessionID,
			Scope:     prefix.Scope,
			Name:      name,
		}
		out = append(out, artifact.Descriptor{
			Key:      key,
			Version:  st.version,
			MimeType: mimeOrDefault(st.mime),
			Size:     int64(len(st.data)),
		})
	}

	next := ""
	if end < len(names) {
		next = page[len(page)-1]
	}
	return out, next, nil
}

// Delete removes artifact content according to the provided delete options.
func (s *Service) Delete(ctx context.Context, key artifact.Key, opts ...artifact.DeleteOption) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if err := validateKey(key); err != nil {
		return err
	}

	o := artifact.DeleteOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if err := o.Validate(); err != nil {
		return err
	}
	path := iartifact.BuildArtifactPath(key)
	versions, exists := s.artifacts[path]
	if !exists || len(versions) == 0 {
		return artifact.ErrNotFound
	}

	switch o.Mode {
	case artifact.DeleteAll:
		delete(s.artifacts, path)
		return nil
	case artifact.DeleteLatest:
		latest, ok := resolveVersion(versions, nil)
		if !ok {
			return artifact.ErrNotFound
		}
		return deleteOneVersionLocked(s.artifacts, path, latest.version)
	case artifact.DeleteVersion:
		return deleteOneVersionLocked(s.artifacts, path, o.Version)
	default:
		return fmt.Errorf("unknown delete mode: %d", int(o.Mode))
	}
}

func deleteOneVersionLocked(m map[string][]stored, path string, ver artifact.VersionID) error {
	versions, ok := m[path]
	if !ok || len(versions) == 0 {
		return artifact.ErrNotFound
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
		return artifact.ErrNotFound
	}
	if len(out) == 0 {
		delete(m, path)
		return nil
	}
	m[path] = out
	return nil
}

// Versions lists all versions available for the provided artifact key.
func (s *Service) Versions(ctx context.Context, key artifact.Key) ([]artifact.VersionID, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	path := iartifact.BuildArtifactPath(key)
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
	return result, nil
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

func validateKey(k artifact.Key) error {
	if k.AppName == "" || k.UserID == "" {
		return fmt.Errorf("invalid key: missing appName or userID")
	}
	switch k.Scope {
	case artifact.ScopeSession:
		if k.SessionID == "" {
			return fmt.Errorf("invalid key: missing sessionID for session scope")
		}
	case artifact.ScopeUser:
		// ok
	default:
		return fmt.Errorf("invalid key: unknown scope %v", k.Scope)
	}
	if k.Name == "" {
		return fmt.Errorf("invalid key: empty name")
	}
	if err := validateObjectName(k.Name); err != nil {
		return err
	}
	return nil
}

func validatePrefix(p artifact.KeyPrefix) error {
	if p.AppName == "" || p.UserID == "" {
		return fmt.Errorf("invalid prefix: missing appName or userID")
	}
	switch p.Scope {
	case artifact.ScopeSession:
		if p.SessionID == "" {
			return fmt.Errorf("invalid prefix: missing sessionID for session scope")
		}
	case artifact.ScopeUser:
		// ok
	default:
		return fmt.Errorf("invalid prefix: unknown scope %v", p.Scope)
	}
	if p.NamePrefix != "" {
		if err := validateObjectPrefix(p.NamePrefix); err != nil {
			return err
		}
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
