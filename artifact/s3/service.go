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
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	iartifact "trpc.group/trpc-go/trpc-agent-go/internal/artifact"
	s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

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
	client     s3storage.Client
	ownsClient bool // true if we created the client, false if provided via WithClient
}

// NewService creates a new S3 artifact service with optional configurations.
//
// Example usage:
//
//	// AWS S3 (using environment variables or AWS credential chain)
//	service, err := s3.NewService("my-bucket",
//	    s3.WithRegion("eu-west-1"),
//	)
//
//	// MinIO
//	service, err := s3.NewService("artifacts",
//	    s3.WithEndpoint("http://localhost:9000"),
//	    s3.WithCredentials("minioadmin", "minioadmin"),
//	    s3.WithPathStyle(true),
//	)
//
//	// DigitalOcean Spaces
//	service, err := s3.NewService("my-space",
//	    s3.WithEndpoint("https://nyc3.digitaloceanspaces.com"),
//	    s3.WithRegion("nyc3"),
//	    s3.WithCredentials(accessKey, secretKey),
//	)
//
//	// Cloudflare R2
//	service, err := s3.NewService("my-bucket",
//	    s3.WithEndpoint("https://ACCOUNT_ID.r2.cloudflarestorage.com"),
//	    s3.WithCredentials(accessKey, secretKey),
//	    s3.WithPathStyle(true),
//	)
//
//	// Using a pre-created client
//	client, err := s3storage.GetClientBuilder()(ctx,
//	    s3storage.WithBucket("my-bucket"),
//	    s3storage.WithRegion("us-west-2"),
//	)
//	service, err := s3.NewService("my-bucket", s3.WithClient(client))
func NewService(bucket string, opts ...Option) (*Service, error) {
	o := &options{
		bucket: bucket,
	}

	// Apply options
	for _, opt := range opts {
		opt(o)
	}

	// Use pre-created client if provided, otherwise create one
	client := o.client
	ownsClient := false
	if client == nil {
		// Build client builder options
		builderOpts := []s3storage.ClientBuilderOpt{
			s3storage.WithBucket(bucket),
		}
		builderOpts = append(builderOpts, o.clientBuilderOpts...)

		var err error
		client, err = s3storage.NewClient(context.Background(), builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage client: %w", err)
		}
		ownsClient = true
	}

	return &Service{
		client:     client,
		ownsClient: ownsClient,
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
func (s *Service) SaveArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	art *artifact.Artifact,
) (int, error) {
	// Get existing versions to determine the next version number
	versions, err := s.ListVersions(ctx, sessionInfo, filename)
	if err != nil {
		return 0, fmt.Errorf("failed to list versions: %w", err)
	}

	// Determine next version
	version := maxVersion(versions) + 1

	// Build object key
	objectKey := iartifact.BuildObjectName(sessionInfo, filename, version)

	// Upload the artifact
	contentType := art.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

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
	var targetVersion int

	if version == nil {
		// Get the latest version
		versions, err := s.ListVersions(ctx, sessionInfo, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to list versions: %w", err)
		}
		if len(versions) == 0 {
			return nil, nil // Artifact not found
		}
		targetVersion = maxVersion(versions)
	} else {
		targetVersion = *version
	}

	// Build object key
	objectKey := iartifact.BuildObjectName(sessionInfo, filename, targetVersion)

	// Download the artifact
	data, contentType, err := s.client.GetObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, nil // Artifact not found
		}
		return nil, fmt.Errorf("failed to download artifact: %w", err)
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return &artifact.Artifact{
		Data:     data,
		MimeType: contentType,
		Name:     filename,
	}, nil
}

// ListArtifactKeys lists all artifact filenames within a session.
// It returns artifacts from both session scope and user scope.
func (s *Service) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	filenameSet := make(map[string]struct{})

	// List session-scoped artifacts
	sessionPrefix := iartifact.BuildSessionPrefix(sessionInfo)
	sessionKeys, err := s.client.ListObjects(ctx, sessionPrefix)
	if err != nil && !errors.Is(err, s3storage.ErrNotFound) {
		return nil, fmt.Errorf("failed to list session artifacts: %w", err)
	}

	for _, key := range sessionKeys {
		filename := extractFilename(key, sessionPrefix)
		if filename != "" {
			filenameSet[filename] = struct{}{}
		}
	}

	// List user-namespaced artifacts
	userPrefix := iartifact.BuildUserNamespacePrefix(sessionInfo)
	userKeys, err := s.client.ListObjects(ctx, userPrefix)
	if err != nil && !errors.Is(err, s3storage.ErrNotFound) {
		return nil, fmt.Errorf("failed to list user artifacts: %w", err)
	}

	for _, key := range userKeys {
		filename := extractFilename(key, userPrefix)
		if filename != "" {
			filenameSet[filename] = struct{}{}
		}
	}

	// Convert set to sorted slice
	filenames := make([]string, 0, len(filenameSet))
	for filename := range filenameSet {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)

	return filenames, nil
}

// DeleteArtifact deletes all versions of an artifact from S3.
func (s *Service) DeleteArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
) error {
	// Get all versions of the artifact
	prefix := iartifact.BuildObjectNamePrefix(sessionInfo, filename)
	keys, err := s.client.ListObjects(ctx, prefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil // Nothing to delete
		}
		return fmt.Errorf("failed to list artifact versions: %w", err)
	}

	if len(keys) == 0 {
		return nil // Nothing to delete
	}

	// Batch delete
	if err := s.client.DeleteObjects(ctx, keys); err != nil {
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
	prefix := iartifact.BuildObjectNamePrefix(sessionInfo, filename)
	keys, err := s.client.ListObjects(ctx, prefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return []int{}, nil
		}
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	versions := make([]int, 0, len(keys))
	for _, key := range keys {
		// Extract version number from key
		// Key format: {prefix}/{version}
		if idx := strings.LastIndex(key, "/"); idx != -1 {
			versionStr := key[idx+1:]
			if version, err := strconv.Atoi(versionStr); err == nil {
				versions = append(versions, version)
			}
		}
	}

	sort.Ints(versions)
	return versions, nil
}

// maxVersion returns the maximum version from a slice of versions.
// Returns 0 if the slice is empty (so that the first version will be 1).
func maxVersion(versions []int) int {
	if len(versions) == 0 {
		return 0
	}
	max := versions[0]
	for _, v := range versions[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// extractFilename extracts the filename from an object key given a prefix.
// Object key format: {prefix}{filename}/{version}
// Returns the filename or empty string if the key doesn't match the expected format.
func extractFilename(objectKey, prefix string) string {
	if !strings.HasPrefix(objectKey, prefix) {
		return ""
	}

	// Remove prefix and extract filename (first segment before "/")
	relative := strings.TrimPrefix(objectKey, prefix)
	if filename, _, ok := strings.Cut(relative, "/"); ok && filename != "" {
		return filename
	}

	return ""
}
