//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcos provides a Tencent Cloud Object Storage (COS) implementation of the artifact service.
// The object name format used depends on whether the filename has a user namespace:
// - For files with user namespace (starting with "user:"):
// {app_name}/{user_id}/user/{filename}/{version}
// - For regular session-scoped files:
// {app_name}/{user_id}/{session_id}/{filename}/{version}
package tcos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// Service is a Tencent Cloud Object Storage implementation of the artifact service.
// It provides cloud-based storage for artifacts using Tencent COS.
type Service struct {
	cosClient *cos.Client
}

// NewService creates a new TCOS artifact service.
func NewService(bucketURL string) *Service {
	u, _ := url.Parse(bucketURL)
	b := &cos.BaseURL{BucketURL: u}
	return &Service{
		cosClient: cos.NewClient(b, &http.Client{
			Timeout: 60 * time.Second,
			Transport: &cos.AuthorizationTransport{
				SecretID:  os.Getenv("COS_SECRETID"),
				SecretKey: os.Getenv("COS_SECRETKEY"),
			},
		}),
	}
}

// SaveArtifact saves an artifact to Tencent Cloud Object Storage.
func (s *Service) SaveArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, art *artifact.Artifact) (int, error) {
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

	objectName := buildObjectName(sessionInfo, filename, version)

	// Upload the artifact data
	reader := bytes.NewReader(art.Data)
	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: art.MimeType,
		},
	}

	_, err = s.cosClient.Object.Put(ctx, objectName, reader, opt)
	if err != nil {
		return 0, fmt.Errorf("failed to upload artifact: %w", err)
	}

	return version, nil
}

// LoadArtifact gets an artifact from Tencent Cloud Object Storage.
func (s *Service) LoadArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string, version *int) (*artifact.Artifact, error) {
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

	objectName := buildObjectName(sessionInfo, filename, targetVersion)

	// Download the artifact
	resp, err := s.cosClient.Object.Get(ctx, objectName, nil)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, nil // Artifact not found
		}
		return nil, fmt.Errorf("failed to download artifact: %w", err)
	}
	defer resp.Body.Close()

	// Read the data
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact data: %w", err)
	}

	// Get content type from response headers
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return &artifact.Artifact{
		Data:     data,
		MimeType: contentType,
		Name:     filename,
	}, nil
}

// ListArtifactKeys lists all the artifact filenames within a session from TCOS.
func (s *Service) ListArtifactKeys(ctx context.Context, sessionInfo artifact.SessionInfo) ([]string, error) {
	filenameSet := make(map[string]bool)

	// List session-scoped artifacts
	sessionPrefix := fmt.Sprintf("%s/%s/%s/", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID)
	sessionResult, _, err := s.cosClient.Bucket.Get(ctx, &cos.BucketGetOptions{
		Prefix: sessionPrefix,
	})
	if err != nil && !cos.IsNotFoundError(err) {
		return nil, fmt.Errorf("failed to list session artifacts: %w", err)
	}

	if sessionResult != nil {
		for _, obj := range sessionResult.Contents {
			parts := strings.Split(obj.Key, "/")
			if len(parts) >= 4 {
				filename := parts[len(parts)-2] // filename is before version
				filenameSet[filename] = true
			}
		}
	}

	// List user-namespaced artifacts
	userPrefix := fmt.Sprintf("%s/%s/user/", sessionInfo.AppName, sessionInfo.UserID)
	userResult, _, err := s.cosClient.Bucket.Get(ctx, &cos.BucketGetOptions{
		Prefix: userPrefix,
	})
	if err != nil && !cos.IsNotFoundError(err) {
		return nil, fmt.Errorf("failed to list user artifacts: %w", err)
	}

	if userResult != nil {
		for _, obj := range userResult.Contents {
			parts := strings.Split(obj.Key, "/")
			if len(parts) >= 4 {
				filename := parts[len(parts)-2] // filename is before version
				filenameSet[filename] = true
			}
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
func (s *Service) DeleteArtifact(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) error {
	// Get all versions of the artifact
	versions, err := s.ListVersions(ctx, sessionInfo, filename)
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	// Delete all versions
	for _, version := range versions {
		objectName := buildObjectName(sessionInfo, filename, version)
		_, err := s.cosClient.Object.Delete(ctx, objectName)
		if err != nil && !cos.IsNotFoundError(err) {
			return fmt.Errorf("failed to delete artifact version %d: %w", version, err)
		}
	}

	return nil
}

// ListVersions lists all versions of an artifact from TCOS.
func (s *Service) ListVersions(ctx context.Context, sessionInfo artifact.SessionInfo, filename string) ([]int, error) {
	prefix := buildObjectNamePrefix(sessionInfo, filename)

	result, _, err := s.cosClient.Bucket.Get(ctx, &cos.BucketGetOptions{
		Prefix: prefix,
	})
	if err != nil {
		if cos.IsNotFoundError(err) {
			return []int{}, nil // No versions found
		}
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	var versions []int
	for _, obj := range result.Contents {
		parts := strings.Split(obj.Key, "/")
		if len(parts) > 0 {
			versionStr := parts[len(parts)-1]
			if version, err := strconv.Atoi(versionStr); err == nil {
				versions = append(versions, version)
			}
		}
	}
	return versions, nil
}

// buildObjectName constructs the object name in COS.
func buildObjectName(sessionInfo artifact.SessionInfo, filename string, version int) string {
	if fileHasUserNamespace(filename) {
		return fmt.Sprintf("%s/%s/user/%s/%d", sessionInfo.AppName, sessionInfo.UserID, filename, version)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename, version)
}

// buildObjectNamePrefix constructs the object name prefix for listing versions.
func buildObjectNamePrefix(sessionInfo artifact.SessionInfo, filename string) string {
	if fileHasUserNamespace(filename) {
		return fmt.Sprintf("%s/%s/user/%s/", sessionInfo.AppName, sessionInfo.UserID, filename)
	}
	return fmt.Sprintf("%s/%s/%s/%s/", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
}

// fileHasUserNamespace checks if the filename has a user namespace.
func fileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}
