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

// NewService creates a new S3 artifact service with optional configurations.
//
// The context is used for initializing the AWS SDK client (loading credentials,
// region detection, etc.). Pass a context with timeout if you need to limit
// initialization time.
//
// The bucket parameter specifies the S3 bucket name. Note: when using WithClient,
// this parameter is ignored as the client already has a bucket configured.
// Ensure the bucket matches the client's bucket to avoid confusion.
//
// Example usage:
//
//	// AWS S3 (using environment variables or AWS credential chain)
//	service, err := s3.NewService(ctx, "my-bucket",
//	    s3.WithRegion("eu-west-1"),
//	)
//
//	// MinIO
//	service, err := s3.NewService(ctx, "artifacts",
//	    s3.WithEndpoint("http://localhost:9000"),
//	    s3.WithCredentials("minioadmin", "minioadmin"),
//	    s3.WithPathStyle(true),
//	)
//
//	// DigitalOcean Spaces
//	service, err := s3.NewService(ctx, "my-space",
//	    s3.WithEndpoint("https://nyc3.digitaloceanspaces.com"),
//	    s3.WithRegion("nyc3"),
//	    s3.WithCredentials(accessKey, secretKey),
//	)
//
//	// Cloudflare R2
//	service, err := s3.NewService(ctx, "my-bucket",
//	    s3.WithEndpoint("https://ACCOUNT_ID.r2.cloudflarestorage.com"),
//	    s3.WithCredentials(accessKey, secretKey),
//	    s3.WithPathStyle(true),
//	)
//
//	// Using a pre-created client (bucket param is ignored, uses client's bucket)
//	client, err := s3storage.NewClient(ctx,
//	    s3storage.WithBucket("my-bucket"),
//	    s3storage.WithRegion("us-west-2"),
//	)
//	service, err := s3.NewService(ctx, "my-bucket", s3.WithClient(client))
func NewService(ctx context.Context, bucket string, opts ...Option) (*Service, error) {
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

	// Get existing versions to determine the next version number
	versions, err := s.listVersions(ctx, sessionInfo, filename)
	if err != nil {
		return 0, fmt.Errorf("failed to list versions: %w", err)
	}

	// Determine next version (first version is 0)
	version := 0
	if len(versions) > 0 {
		version = slices.Max(versions) + 1
	}

	// Build object key and upload
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
		// Get the latest version
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

	// Download the artifact
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

	// Collect filenames from both session and user scopes
	prefixes := []struct {
		prefix  string
		errWrap string
	}{
		{iartifact.BuildSessionPrefix(sessionInfo), "failed to list session artifacts"},
		{iartifact.BuildUserNamespacePrefix(sessionInfo), "failed to list user artifacts"},
	}

	for _, p := range prefixes {
		keys, err := s.client.ListObjects(ctx, p.prefix)
		if err != nil && !errors.Is(err, s3storage.ErrNotFound) {
			return nil, fmt.Errorf("%s: %w", p.errWrap, err)
		}
		for _, key := range keys {
			if filename := extractFilename(key, p.prefix); filename != "" {
				filenameSet[filename] = struct{}{}
			}
		}
	}

	// Convert set to sorted slice
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
	if err := validateSessionInfo(sessionInfo); err != nil {
		return nil, err
	}
	if err := validateFilename(filename); err != nil {
		return nil, err
	}
	return s.listVersions(ctx, sessionInfo, filename)
}

// listVersions is the internal implementation that skips validation.
// Use this when the caller has already validated the inputs.
func (s *Service) listVersions(
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
		// Extract version number from key suffix
		if idx := strings.LastIndex(key, "/"); idx != -1 {
			if v, err := strconv.Atoi(key[idx+1:]); err == nil {
				versions = append(versions, v)
			}
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

	// Remove prefix and extract filename (first segment before "/")
	relative := strings.TrimPrefix(objectKey, prefix)
	if filename, _, ok := strings.Cut(relative, "/"); ok && filename != "" {
		return filename
	}

	return ""
}

// validateSessionInfo checks that all required session info fields are present.
func validateSessionInfo(info artifact.SessionInfo) error {
	if info.AppName == "" || info.UserID == "" || info.SessionID == "" {
		return ErrEmptySessionInfo
	}
	return nil
}

// validateFilename checks that the filename is valid and safe.
// It rejects empty filenames, path traversal attempts, and other dangerous patterns.
func validateFilename(filename string) error {
	if filename == "" {
		return ErrEmptyFilename
	}

	// Check for path traversal and invalid characters
	// Note: "user:" prefix is allowed for user-scoped artifacts
	if strings.Contains(filename, "/") ||
		strings.Contains(filename, "\\") ||
		strings.Contains(filename, "..") ||
		strings.Contains(filename, "\x00") {
		return ErrInvalidFilename
	}

	return nil
}
