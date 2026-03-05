//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package s3 provides an S3-compatible artifact storage service implementation.
// It supports AWS S3, MinIO, DigitalOcean Spaces, Cloudflare R2, and other
// S3-compatible object storage services.
package s3

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	iartifact "trpc.group/trpc-go/trpc-agent-go/internal/artifact"
	"trpc.group/trpc-go/trpc-agent-go/log"
	s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

// defaultContentType is the fallback MIME type for artifacts without one.
const defaultContentType = "application/octet-stream"

// Compile-time check that Service implements artifact.Service.
var _ artifact.Service = (*Service)(nil)

// Service is an S3-compatible implementation of the artifact service.
// It supports AWS S3, MinIO, DigitalOcean Spaces, Cloudflare R2, and other
// S3-compatible object storage services.
//
// The object name format used depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     {app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}/{version}
type Service struct {
	client         s3storage.Client
	ownsClient     bool // true if we created the client, false if provided via WithClient
	logger         log.Logger
	presignExpires time.Duration
}

// NewService creates a new S3 artifact service.
// When using WithClient, the bucket parameter is ignored as the client already has one configured.
func NewService(ctx context.Context, bucket string, opts ...Option) (*Service, error) {
	o := &options{
		bucket:         bucket,
		presignExpires: 15 * time.Minute,
	}
	for _, opt := range opts {
		opt(o)
	}

	client := o.client
	ownsClient := false
	if client == nil {
		builderOpts := []s3storage.ClientBuilderOpt{
			s3storage.WithBucket(bucket),
		}
		builderOpts = append(builderOpts, o.clientBuilderOpts...)

		var err error
		client, err = s3storage.NewClient(ctx, builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage client: %w", err)
		}
		ownsClient = true
	}

	return &Service{
		client:         client,
		ownsClient:     ownsClient,
		logger:         o.logger,
		presignExpires: o.presignExpires,
	}, nil
}

// Close releases any resources held by the service.
// If the client was provided externally via WithClient, it is not closed.
func (s *Service) Close() error {
	if s.client == nil || !s.ownsClient {
		return nil
	}
	return s.client.Close()
}

// Put stores artifact content and returns its metadata.
func (s *Service) Put(ctx context.Context, req *artifact.PutRequest, opts ...artifact.PutOption) (*artifact.PutResponse, error) {
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

	v, err := artifact.NewVersionID()
	if err != nil {
		return nil, err
	}
	objectKey := iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, v)
	contentType := cmp.Or(req.MimeType, defaultContentType)

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := s.client.PutObject(ctx, objectKey, data, contentType); err != nil {
		return nil, fmt.Errorf("failed to upload artifact: %w", err)
	}
	resp := &artifact.PutResponse{Version: v, MimeType: contentType, Size: int64(len(data))}
	if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
		resp.URL = u
	}
	return resp, nil
}

// Head resolves an artifact version to its metadata and an optional URL.
func (s *Service) Head(ctx context.Context, req *artifact.HeadRequest, opts ...artifact.HeadOption) (*artifact.HeadResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("head request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	target, err := s.resolveVersion(ctx, req.AppName, req.UserID, req.SessionID, req.Name, req.Version)
	if err != nil {
		return nil, err
	}
	objectKey := iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, target)
	contentType, size, err := s.client.HeadObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, artifact.ErrNotFound
		}
		return nil, fmt.Errorf("failed to head artifact: %w", err)
	}
	resp := &artifact.HeadResponse{
		Version:  target,
		MimeType: cmp.Or(contentType, defaultContentType),
		Size:     size,
	}
	if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
		resp.URL = u
	}
	return resp, nil
}

// Open returns a streaming reader for the artifact content and its descriptor.
func (s *Service) Open(ctx context.Context, req *artifact.OpenRequest, opts ...artifact.OpenOption) (*artifact.OpenResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("open request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	target, err := s.resolveVersion(ctx, req.AppName, req.UserID, req.SessionID, req.Name, req.Version)
	if err != nil {
		return nil, err
	}
	objectKey := iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, target)
	body, contentType, size, err := s.client.OpenObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, artifact.ErrNotFound
		}
		return nil, fmt.Errorf("failed to open artifact: %w", err)
	}
	resp := &artifact.OpenResponse{
		Body:     body,
		Version:  target,
		MimeType: cmp.Or(contentType, defaultContentType),
		Size:     size,
	}
	if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
		resp.URL = u
	}
	return resp, nil
}

// List returns the latest version metadata for each artifact name under the given namespace.
func (s *Service) List(ctx context.Context, req *artifact.ListRequest, opts ...artifact.ListOption) (*artifact.ListResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list request is nil")
	}
	if err := validateListFields(req.AppName, req.UserID); err != nil {
		return nil, err
	}
	_ = opts // reserved

	scopePrefix := iartifact.BuildListPrefix(req.AppName, req.UserID, req.SessionID)
	keys, err := s.client.ListObjects(ctx, scopePrefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return &artifact.ListResponse{}, nil
		}
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}

	type latest struct {
		version artifact.VersionID
	}
	latestByName := make(map[string]latest)
	names := make([]string, 0)

	for _, objectKey := range keys {
		name, ver, ok := parseNameAndVersion(objectKey, scopePrefix)
		if !ok {
			continue
		}
		if cur, exists := latestByName[name]; !exists {
			latestByName[name] = latest{version: ver}
			names = append(names, name)
		} else if artifact.CompareVersion(ver, cur.version) > 0 {
			latestByName[name] = latest{version: ver}
		}
	}

	slices.Sort(names)

	start := 0
	if req.PageToken != nil && *req.PageToken != "" {
		tok := *req.PageToken
		i, _ := slices.BinarySearch(names, tok)
		start = i
		for start < len(names) && names[start] <= tok {
			start++
		}
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
		ver := latestByName[name].version
		objectKey := iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, name, ver)
		contentType, size, err := s.client.HeadObject(ctx, objectKey)
		if err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("failed to head listed artifact: %w", err)
		}
		item := artifact.ListItem{
			Name:     name,
			Version:  ver,
			MimeType: cmp.Or(contentType, defaultContentType),
			Size:     size,
		}
		if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
			item.URL = u
		}
		out = append(out, item)
	}

	next := ""
	if end < len(names) && len(page) > 0 {
		next = page[len(page)-1]
	}
	return &artifact.ListResponse{Items: out, NextPageToken: next}, nil
}

// Delete is idempotent by default.
func (s *Service) Delete(ctx context.Context, req *artifact.DeleteRequest, opts ...artifact.DeleteOption) (*artifact.DeleteResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("delete request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	// Delete all versions.
	if req.Version == nil {
		prefix := iartifact.BuildObjectNamePrefix(req.AppName, req.UserID, req.SessionID, req.Name)
		keys, err := s.client.ListObjects(ctx, prefix)
		if err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return &artifact.DeleteResponse{Deleted: false}, nil
			}
			return nil, fmt.Errorf("failed to list artifact versions: %w", err)
		}
		if len(keys) == 0 {
			return &artifact.DeleteResponse{Deleted: false}, nil
		}
		if err := s.client.DeleteObjects(ctx, keys); err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return &artifact.DeleteResponse{Deleted: false}, nil
			}
			return nil, fmt.Errorf("failed to delete artifact: %w", err)
		}
		return &artifact.DeleteResponse{Deleted: true}, nil
	}

	// Delete a specific version.
	objectKey := iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, *req.Version)
	if err := s.client.DeleteObjects(ctx, []string{objectKey}); err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return &artifact.DeleteResponse{Deleted: false}, nil
		}
		return nil, fmt.Errorf("failed to delete artifact: %w", err)
	}
	return &artifact.DeleteResponse{Deleted: true}, nil
}

// Versions lists all versions available for the provided artifact.
func (s *Service) Versions(ctx context.Context, req *artifact.VersionsRequest, opts ...artifact.VersionsOption) (*artifact.VersionsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("versions request is nil")
	}
	if err := validateKeyFields(req.AppName, req.UserID, req.Name); err != nil {
		return nil, err
	}
	_ = opts // reserved

	versions, err := s.listVersions(ctx, req.AppName, req.UserID, req.SessionID, req.Name)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, artifact.ErrNotFound
	}
	return &artifact.VersionsResponse{Versions: versions}, nil
}

func (s *Service) resolveVersion(ctx context.Context, appName, userID, sessionID, name string, version *artifact.VersionID) (artifact.VersionID, error) {
	if version != nil {
		return *version, nil
	}
	versions, err := s.listVersions(ctx, appName, userID, sessionID, name)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		if s.logger != nil {
			s.logger.Debugf("artifact not found: %s/%s/%s/%s",
				appName, userID, sessionID, name)
		}
		return "", artifact.ErrNotFound
	}
	latest := versions[0]
	for _, v := range versions[1:] {
		if artifact.CompareVersion(v, latest) > 0 {
			latest = v
		}
	}
	return latest, nil
}

func (s *Service) listVersions(ctx context.Context, appName, userID, sessionID, name string) ([]artifact.VersionID, error) {
	prefix := iartifact.BuildObjectNamePrefix(appName, userID, sessionID, name)
	keys, err := s.client.ListObjects(ctx, prefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}
	versions := make([]artifact.VersionID, 0, len(keys))
	for _, objectKey := range keys {
		if idx := strings.LastIndex(objectKey, "/"); idx != -1 {
			s := objectKey[idx+1:]
			if s != "" {
				versions = append(versions, artifact.VersionID(s))
			}
		}
	}
	slices.SortFunc(versions, func(a, b artifact.VersionID) int { return artifact.CompareVersion(a, b) })
	return versions, nil
}

func parseNameAndVersion(objectKey, scopePrefix string) (name string, ver artifact.VersionID, ok bool) {
	if !strings.HasPrefix(objectKey, scopePrefix) {
		return "", "", false
	}
	rel := strings.TrimPrefix(objectKey, scopePrefix)
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	b := parts[len(parts)-1]
	a := strings.Join(parts[:len(parts)-1], "/")
	if a == "" || b == "" {
		return "", "", false
	}
	return a, artifact.VersionID(b), true
}

func validateKeyFields(appName, userID, name string) error {
	if appName == "" || userID == "" {
		return ErrEmptySessionInfo
	}
	return validateName(name)
}

func validateListFields(appName, userID string) error {
	if appName == "" || userID == "" {
		return ErrEmptySessionInfo
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return ErrEmptyFilename
	}
	if strings.HasPrefix(name, "/") ||
		strings.Contains(name, "\\") ||
		strings.Contains(name, "\x00") {
		return ErrInvalidFilename
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return ErrInvalidFilename
		}
	}
	return nil
}

func validateNamePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") ||
		strings.Contains(prefix, "\\") ||
		strings.Contains(prefix, "\x00") {
		return ErrInvalidFilename
	}
	parts := strings.Split(prefix, "/")
	for i, p := range parts {
		if p == "." || p == ".." {
			return ErrInvalidFilename
		}
		// Allow trailing slash.
		if p == "" && i != len(parts)-1 {
			return ErrInvalidFilename
		}
	}
	return nil
}
