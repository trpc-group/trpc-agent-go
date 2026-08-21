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
	"path"
	"slices"
	"strconv"
	"strings"

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
// Artifact filenames may be canonical, forward-slash-separated relative paths.
//
// The object name format used depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     {app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}/{version}
type Service struct {
	client     s3storage.Client
	ownsClient bool // true if we created the client, false if provided via WithClient
	logger     log.Logger
}

// NewService creates a new S3 artifact service.
// When using WithClient, the bucket parameter is ignored as the client already has one configured.
func NewService(ctx context.Context, bucket string, opts ...Option) (*Service, error) {
	o := &options{
		bucket: bucket,
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
		client:     client,
		ownsClient: ownsClient,
		logger:     o.logger,
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

// SaveArtifact saves an artifact to S3.
// It automatically determines the next version number by listing existing versions.
//
// Concurrency: This method is NOT safe for concurrent writes to the same filename.
// If multiple goroutines save the same artifact concurrently, they may compute
// the same version number, causing one write to overwrite the other.
// For concurrent access, use external synchronization or unique filenames.
func (s *Service) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	art *artifact.Artifact,
) (int, error) {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return 0, err
	}
	if err := validateFilename(filename); err != nil {
		return 0, err
	}
	if art == nil {
		return 0, ErrNilArtifact
	}

	versions, err := s.listVersions(ctx, sessionInfo, filename)
	if err != nil {
		return 0, fmt.Errorf("failed to list versions: %w", err)
	}

	version := 0
	if len(versions) > 0 {
		version = slices.Max(versions) + 1
	}

	objectKey := iartifact.BuildObjectName(sessionInfo, filename, version)
	contentType := cmp.Or(art.MimeType, defaultContentType)

	if err := s.client.PutObject(ctx, objectKey, art.Data, contentType); err != nil {
		return 0, fmt.Errorf("failed to upload artifact: %w", err)
	}

	return version, nil
}

// LoadArtifact loads an artifact from S3.
// If version is nil, the latest version is loaded.
func (s *Service) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return nil, err
	}
	if err := validateFilename(filename); err != nil {
		return nil, err
	}

	targetVersion := 0
	if version != nil {
		targetVersion = *version
	} else {
		versions, err := s.listVersions(ctx, sessionInfo, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to list versions: %w", err)
		}
		if len(versions) == 0 {
			if s.logger != nil {
				s.logger.Debugf("artifact not found: %s/%s/%s/%s",
					sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
			}
			return nil, nil // Artifact not found
		}
		targetVersion = slices.Max(versions)
	}

	objectKey := iartifact.BuildObjectName(sessionInfo, filename, targetVersion)
	data, contentType, err := s.client.GetObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			if s.logger != nil {
				if version != nil {
					s.logger.Debugf("artifact version not found: %s/%s/%s/%s@%d",
						sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename, *version)
				} else {
					s.logger.Debugf("artifact not found: %s/%s/%s/%s",
						sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
				}
			}
			return nil, nil
		}
		return nil, fmt.Errorf("failed to download artifact: %w", err)
	}

	return &artifact.Artifact{
		Data:     data,
		MimeType: cmp.Or(contentType, defaultContentType),
		Name:     filename,
	}, nil
}

// ListArtifactKeys lists all artifact filenames within a session.
// It returns artifacts from both session scope and user scope.
func (s *Service) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return nil, err
	}

	filenameSet := make(map[string]struct{})
	prefixes := []string{
		iartifact.BuildSessionPrefix(sessionInfo),
		iartifact.BuildUserNamespacePrefix(sessionInfo),
	}

	for _, prefix := range prefixes {
		keys, err := s.client.ListObjects(ctx, prefix)
		if err != nil && !errors.Is(err, s3storage.ErrNotFound) {
			return nil, fmt.Errorf("failed to list artifacts: %w", err)
		}
		for _, key := range keys {
			if filename := extractFilename(key, prefix); filename != "" {
				filenameSet[filename] = struct{}{}
			}
		}
	}

	filenames := make([]string, 0, len(filenameSet))
	for filename := range filenameSet {
		filenames = append(filenames, filename)
	}
	slices.Sort(filenames)

	return filenames, nil
}

// DeleteArtifact deletes all versions of an artifact from S3.
func (s *Service) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return err
	}
	if err := validateFilename(filename); err != nil {
		return err
	}

	prefix := iartifact.BuildObjectNamePrefix(sessionInfo, filename)
	keys, err := s.client.ListObjects(ctx, prefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to list artifact versions: %w", err)
	}

	if len(keys) == 0 {
		return nil
	}

	versionKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := extractVersion(key, prefix); ok {
			versionKeys = append(versionKeys, key)
		}
	}
	if len(versionKeys) == 0 {
		return nil
	}

	if err := s.client.DeleteObjects(ctx, versionKeys); err != nil {
		return fmt.Errorf("failed to delete artifact: %w", err)
	}

	return nil
}

// ListVersions lists all versions of an artifact.
func (s *Service) ListVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return nil, err
	}
	if err := validateFilename(filename); err != nil {
		return nil, err
	}
	return s.listVersions(ctx, sessionInfo, filename)
}

func (s *Service) listVersions(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	prefix := iartifact.BuildObjectNamePrefix(sessionInfo, filename)
	keys, err := s.client.ListObjects(ctx, prefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	versions := make([]int, 0, len(keys))
	for _, key := range keys {
		if version, ok := extractVersion(key, prefix); ok {
			versions = append(versions, version)
		}
	}

	slices.Sort(versions)
	return versions, nil
}

// extractFilename extracts the filename from an object key given a prefix.
// Object key format: {prefix}{filename}/{version}
// Returns the filename or empty string if the key doesn't match the expected format.
func extractFilename(objectKey, prefix string) string {
	if !strings.HasPrefix(objectKey, prefix) {
		return ""
	}

	relative := strings.TrimPrefix(objectKey, prefix)
	separator := strings.LastIndexByte(relative, '/')
	if separator <= 0 {
		return ""
	}
	if _, ok := parseVersion(relative[separator+1:]); !ok {
		return ""
	}

	filename := relative[:separator]
	if err := validateFilename(filename); err != nil {
		return ""
	}
	return filename
}

// extractVersion extracts a direct version suffix from an object key.
// Descendant artifact keys that merely share the prefix are ignored.
func extractVersion(objectKey, prefix string) (int, bool) {
	if !strings.HasPrefix(objectKey, prefix) {
		return 0, false
	}

	relative := strings.TrimPrefix(objectKey, prefix)
	if relative == "" || strings.Contains(relative, "/") {
		return 0, false
	}

	return parseVersion(relative)
}

func parseVersion(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}

	version, err := strconv.Atoi(value)
	return version, err == nil
}

// validateSessionInfo checks that all required session info fields are present.
func validateSessionInfo(info artifact.SessionInfo) error {
	if info.AppName == "" || info.UserID == "" || info.SessionID == "" {
		return ErrEmptySessionInfo
	}
	return nil
}

// validateFilename checks that the filename is a safe relative artifact path.
func validateFilename(filename string) error {
	if filename == "" {
		return ErrEmptyFilename
	}

	// Note: "user:" prefix is allowed for user-scoped artifacts.
	if path.IsAbs(filename) ||
		path.Clean(filename) != filename ||
		filename == "." ||
		strings.Contains(filename, "\\") ||
		strings.Contains(filename, "\x00") {
		return ErrInvalidFilename
	}
	for _, segment := range strings.Split(filename, "/") {
		if segment == ".." {
			return ErrInvalidFilename
		}
	}

	return nil
}
