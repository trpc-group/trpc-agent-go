//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package cos provides a Tencent Cloud Object Storage (COS) implementation of the artifact service.
//
// The object name format used depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     artifact/{app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     artifact/{app_name}/{user_id}/{session_id}/{filename}/{version}
//
// Authentication:
// The service requires COS credentials which can be provided via:
// - Environment variables: COS_SECRETID and COS_SECRETKEY (recommended)
// - Option functions: WithSecretID() and WithSecretKey()
//
// Example:
//
//	// Set environment variables
//	export COS_SECRETID="your-secret-id"
//	export COS_SECRETKEY="your-secret-key"
//
//	// Create service
//	service := cos.NewService("https://bucket.cos.region.myqcloud.com")
package cos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	iartifact "trpc.group/trpc-go/trpc-agent-go/internal/artifact"
)

// Service is a Tencent Cloud Object Storage implementation of the artifact service.
// It provides cloud-based storage for artifacts using Tencent COS.
// The Object name format used depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     artifact/{app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     artifact/{app_name}/{user_id}/{session_id}/{filename}/{version}
type Service struct {
	cosClient client
}

const (
	defaultTimeout     = 60 * time.Second
	defaultContentType = "application/octet-stream"
	objectKeySep       = "/"
	artifactRootDir    = "artifact"
)

// NewService creates a new TCOS artifact service with optional configurations.
//
// Authentication credentials can be provided in multiple ways:
// 1. Set environment variables COS_SECRETID and COS_SECRETKEY (recommended)
// 2. Use WithSecretID() and WithSecretKey() options
// 3. Use WithClient() to provide a pre-configured COS client directly
//
// Example usage:
//
//	// Using environment variables (set COS_SECRETID and COS_SECRETKEY)
//	service := cos.NewService("https://bucket.cos.region.myqcloud.com")
//
//	// Using option functions
//	service := cos.NewService(
//	    "https://bucket.cos.region.myqcloud.com",
//	    cos.WithSecretID("your-secret-id"),
//	    cos.WithSecretKey("your-secret-key"),
//	    cos.WithTimeout(30*time.Second),
//	)
//
//	// Using a pre-configured COS client
//	cosClient := cos.NewClient("service-name", baseURL, httpClient)
//	service := cos.NewService("service-name", cos.WithClient(cosClient))
func NewService(name, bucketURL string, opts ...Option) (*Service, error) {
	c, err := globalBuilder(name, bucketURL, opts...)
	if err != nil {
		return nil, err
	}
	cli, ok := c.(client)
	if !ok {
		return nil, fmt.Errorf("client builder returned invalid type: expected client interface, got %T", c)
	}
	return &Service{
		cosClient: cli,
	}, nil
}

// SaveArtifact saves an artifact to Tencent Cloud Object Storage.
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
	versions, err := s.ListVersions(ctx, sessionInfo, filename)
	if err != nil {
		return 0, fmt.Errorf("failed to list versions: %w", err)
	}

	version := 0
	if len(versions) > 0 {
		maxVersion := 0
		for _, v := range versions {
			if v > maxVersion {
				maxVersion = v
			}
		}
		version = maxVersion + 1
	}

	objectName, _ := buildObjectNameCandidates(
		sessionInfo,
		filename,
		version,
	)

	// Upload the artifact data
	reader := bytes.NewReader(art.Data)
	putOpts := cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: art.MimeType,
			ContentDisposition: mime.FormatMediaType("attachment", map[string]string{
				"filename": filename,
			}),
		},
	}
	err = s.cosClient.PutObject(ctx, objectName, reader, putOpts)
	if err != nil {
		return 0, fmt.Errorf("failed to upload artifact: %w", err)
	}

	return version, nil
}

// LoadArtifact gets an artifact from Tencent Cloud Object Storage.
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

		maxVersion := 0
		for _, v := range versions {
			if v > maxVersion {
				maxVersion = v
			}
		}
		targetVersion = maxVersion
	} else {
		targetVersion = *version
	}

	objectName, legacyObjectName := buildObjectNameCandidates(
		sessionInfo,
		filename,
		targetVersion,
	)

	// Download the artifact
	respBody, respHeader, err := s.cosClient.GetObject(ctx, objectName)
	if err != nil {
		if !cos.IsNotFoundError(err) {
			return nil, fmt.Errorf("failed to download artifact: %w", err)
		}

		respBody, respHeader, err = s.cosClient.GetObject(
			ctx,
			legacyObjectName,
		)
		if err != nil {
			if cos.IsNotFoundError(err) {
				return nil, nil // Artifact not found
			}
			return nil, fmt.Errorf("failed to download artifact: %w", err)
		}
	}
	defer respBody.Close()

	// Read the data
	data, err := io.ReadAll(respBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact data: %w", err)
	}

	// Get content type from response headers
	contentType := respHeader.Get("Content-Type")
	if contentType == "" {
		contentType = defaultContentType
	}

	return &artifact.Artifact{
		Data:     data,
		MimeType: contentType,
		Name:     filename,
	}, nil
}

// ListArtifactKeys lists all the artifact filenames within a session from TCOS.
func (s *Service) ListArtifactKeys(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
) ([]string, error) {
	if err := validateSessionInfo(sessionInfo); err != nil {
		return nil, err
	}

	filenameSet := make(map[string]struct{})

	// List session-scoped artifacts
	sessionPrefix, legacySessionPrefix := buildSessionPrefixCandidates(
		sessionInfo,
	)
	sessionPrefixes := []string{
		sessionPrefix,
		legacySessionPrefix,
	}
	for _, prefix := range sessionPrefixes {
		sessionResult, err := s.cosClient.GetBucket(ctx, prefix)
		if err != nil && !cos.IsNotFoundError(err) {
			return nil, fmt.Errorf(
				"failed to list session artifacts: %w",
				err,
			)
		}
		if sessionResult == nil {
			continue
		}

		for _, obj := range sessionResult.Contents {
			filename := extractFilenameFromObjectKey(obj.Key, prefix)
			if filename == "" {
				continue
			}
			filenameSet[filename] = struct{}{}
		}
	}

	// List user-namespaced artifacts
	userPrefix, legacyUserPrefix := buildUserNamespacePrefixCandidates(
		sessionInfo,
	)
	userPrefixes := []string{
		userPrefix,
		legacyUserPrefix,
	}
	for _, prefix := range userPrefixes {
		userResult, err := s.cosClient.GetBucket(ctx, prefix)
		if err != nil && !cos.IsNotFoundError(err) {
			return nil, fmt.Errorf(
				"failed to list user artifacts: %w",
				err,
			)
		}
		if userResult == nil {
			continue
		}

		for _, obj := range userResult.Contents {
			filename := extractFilenameFromObjectKey(obj.Key, prefix)
			if filename == "" {
				continue
			}
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

// DeleteArtifact deletes an artifact from Tencent Cloud Object Storage.
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
	versions, err := s.ListVersions(ctx, sessionInfo, filename)
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	// Delete all versions
	for _, version := range versions {
		objectName, legacyObjectName := buildObjectNameCandidates(
			sessionInfo,
			filename,
			version,
		)
		objectNames := []string{objectName, legacyObjectName}
		for _, name := range objectNames {
			err := s.cosClient.DeleteObject(ctx, name)
			if err != nil && !cos.IsNotFoundError(err) {
				return fmt.Errorf(
					"failed to delete artifact version %d: %w",
					version,
					err,
				)
			}
		}
	}

	return nil
}

// ListVersions lists all versions of an artifact from TCOS.
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

	prefix, legacyPrefix := buildObjectNamePrefixCandidates(
		sessionInfo,
		filename,
	)
	results := []*cos.BucketGetResult{}
	prefixes := []string{prefix, legacyPrefix}
	for _, p := range prefixes {
		result, err := s.cosClient.GetBucket(ctx, p)
		if err != nil {
			if cos.IsNotFoundError(err) {
				continue
			}
			return nil, fmt.Errorf("failed to list versions: %w", err)
		}

		if result != nil {
			results = append(results, result)
		}
	}

	if len(results) == 0 {
		return []int{}, nil
	}

	versionSet := make(map[int]struct{})
	for _, result := range results {
		for _, obj := range result.Contents {
			parts := strings.Split(obj.Key, objectKeySep)
			if len(parts) == 0 {
				continue
			}
			versionStr := parts[len(parts)-1]
			version, err := strconv.Atoi(versionStr)
			if err != nil {
				continue
			}
			versionSet[version] = struct{}{}
		}
	}

	versions := make([]int, 0, len(versionSet))
	for version := range versionSet {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions, nil
}

func validateSessionInfo(info artifact.SessionInfo) error {
	if info.AppName == "" || info.UserID == "" || info.SessionID == "" {
		return ErrEmptySessionInfo
	}
	return nil
}

func validateFilename(filename string) error {
	if strings.TrimSpace(filename) == "" {
		return ErrEmptyFilename
	}
	if strings.Contains(filename, "\x00") {
		return ErrInvalidFilename
	}
	return nil
}

func extractFilenameFromObjectKey(objectKey, prefix string) string {
	if !strings.HasPrefix(objectKey, prefix) {
		return ""
	}
	rel := strings.TrimPrefix(objectKey, prefix)
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, objectKeySep)
	if len(parts) < 2 {
		return ""
	}
	name := strings.Join(parts[:len(parts)-1], objectKeySep)
	return strings.TrimSpace(name)
}

func buildObjectNameCandidates(
	sessionInfo artifact.SessionInfo,
	filename string,
	version int,
) (string, string) {
	legacy := iartifact.BuildObjectName(sessionInfo, filename, version)
	return withArtifactRoot(legacy), legacy
}

func buildObjectNamePrefixCandidates(
	sessionInfo artifact.SessionInfo,
	filename string,
) (string, string) {
	legacy := iartifact.BuildObjectNamePrefix(sessionInfo, filename)
	return withArtifactRoot(legacy), legacy
}

func buildSessionPrefixCandidates(
	sessionInfo artifact.SessionInfo,
) (string, string) {
	legacy := iartifact.BuildSessionPrefix(sessionInfo)
	return withArtifactRoot(legacy), legacy
}

func buildUserNamespacePrefixCandidates(
	sessionInfo artifact.SessionInfo,
) (string, string) {
	legacy := iartifact.BuildUserNamespacePrefix(sessionInfo)
	return withArtifactRoot(legacy), legacy
}

func withArtifactRoot(key string) string {
	key = strings.TrimPrefix(key, objectKeySep)
	return artifactRootDir + objectKeySep + key
}
