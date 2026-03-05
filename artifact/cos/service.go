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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	secretID  string
	secretKey string

	presignExpires time.Duration
}

const (
	defaultTimeout     = 60 * time.Second
	defaultContentType = "application/octet-stream"
	objectKeySep       = "/"
	artifactRootDir    = "artifact"
)

// Compile-time check that Service implements artifact.Service.
var _ artifact.Service = (*Service)(nil)

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
	// capture options for presigning
	o := &options{
		timeout:        defaultTimeout,
		secretID:       os.Getenv("COS_SECRETID"),
		secretKey:      os.Getenv("COS_SECRETKEY"),
		presignExpires: 15 * time.Minute,
	}
	for _, opt := range opts {
		opt(o)
	}

	c, err := globalBuilder(name, bucketURL, opts...)
	if err != nil {
		return nil, err
	}
	cli, ok := c.(client)
	if !ok {
		return nil, fmt.Errorf("client builder returned invalid type: expected client interface, got %T", c)
	}
	return &Service{
		cosClient:      cli,
		secretID:       o.secretID,
		secretKey:      o.secretKey,
		presignExpires: o.presignExpires,
	}, nil
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

	po := artifact.PutOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&po)
		}
	}

	v, err := artifact.NewVersionID()
	if err != nil {
		return nil, err
	}
	objectName := withArtifactRoot(iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, v))

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	mt := req.MimeType
	if mt == "" {
		mt = defaultContentType
	}
	if err := s.cosClient.PutObject(ctx, objectName, bytes.NewReader(data), mt); err != nil {
		return nil, fmt.Errorf("failed to upload artifact: %w", err)
	}
	return &artifact.PutResponse{Version: v, MimeType: mt, Size: int64(len(data)), URL: s.bestEffortURL(ctx, objectName)}, nil
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
	objectName := withArtifactRoot(iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, target))
	h, err := s.cosClient.HeadObject(ctx, objectName)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, artifact.ErrNotFound
		}
		return nil, fmt.Errorf("failed to head artifact: %w", err)
	}
	resp := headResponseFromHeader(target, h)
	resp.URL = s.bestEffortURL(ctx, objectName)
	return &resp, nil
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
	objectName := withArtifactRoot(iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, target))
	body, header, err := s.cosClient.GetObject(ctx, objectName)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, artifact.ErrNotFound
		}
		return nil, fmt.Errorf("failed to open artifact: %w", err)
	}
	resp := openResponseFromHeader(body, target, header)
	resp.URL = s.bestEffortURL(ctx, objectName)
	return &resp, nil
}

// List returns the latest version descriptor for each artifact name under the given prefix.
func (s *Service) List(ctx context.Context, req *artifact.ListRequest, opts ...artifact.ListOption) (*artifact.ListResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("list request is nil")
	}
	if err := validateListFields(req.AppName, req.UserID); err != nil {
		return nil, err
	}
	_ = opts // reserved

	scopePrefix := withArtifactRoot(iartifact.BuildListPrefix(req.AppName, req.UserID, req.SessionID))
	result, err := s.cosClient.GetBucket(ctx, scopePrefix)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return &artifact.ListResponse{}, nil
		}
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}
	if result == nil {
		return &artifact.ListResponse{}, nil
	}

	names, latestByName := collectLatestByName(result.Contents, scopePrefix)
	if len(names) == 0 {
		return &artifact.ListResponse{}, nil
	}

	page, next := paginateNames(names, req.Limit, req.PageToken)

	out := make([]artifact.ListItem, 0, len(page))
	for _, name := range page {
		ver := latestByName[name]
		h, err := s.Head(ctx, &artifact.HeadRequest{
			AppName:   req.AppName,
			UserID:    req.UserID,
			SessionID: req.SessionID,
			Name:      name,
			Version:   &ver,
		})
		if err != nil {
			if errors.Is(err, artifact.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, artifact.ListItem{
			Name:     name,
			Version:  h.Version,
			MimeType: h.MimeType,
			Size:     h.Size,
			URL:      h.URL,
		})
	}
	return &artifact.ListResponse{Items: out, NextPageToken: next}, nil
}

// Delete removes artifact content according to the provided delete options.
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
		versions, err := s.listVersions(ctx, req.AppName, req.UserID, req.SessionID, req.Name)
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return &artifact.DeleteResponse{Deleted: false}, nil
		}
		deleted := false
		for _, v := range versions {
			objectName := withArtifactRoot(iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, v))
			if err := s.cosClient.DeleteObject(ctx, objectName); err != nil {
				if cos.IsNotFoundError(err) {
					continue
				}
				return nil, fmt.Errorf("failed to delete artifact version %s: %w", v, err)
			}
			deleted = true
		}
		return &artifact.DeleteResponse{Deleted: deleted}, nil
	}

	// Delete a specific version.
	objectName := withArtifactRoot(iartifact.BuildObjectName(req.AppName, req.UserID, req.SessionID, req.Name, *req.Version))
	if err := s.cosClient.DeleteObject(ctx, objectName); err != nil {
		if cos.IsNotFoundError(err) {
			return &artifact.DeleteResponse{Deleted: false}, nil
		}
		return nil, fmt.Errorf("failed to delete artifact version %s: %w", *req.Version, err)
	}
	return &artifact.DeleteResponse{Deleted: true}, nil
}

// Versions lists all versions available for the provided artifact key.
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

func collectLatestByName(contents []cos.Object, scopePrefix string) ([]string, map[string]artifact.VersionID) {
	latestByName := make(map[string]artifact.VersionID)
	names := make([]string, 0)
	for _, obj := range contents {
		name, ver, ok := parseNameAndVersion(obj.Key, scopePrefix)
		if !ok {
			continue
		}
		if cur, exists := latestByName[name]; !exists {
			latestByName[name] = ver
			names = append(names, name)
		} else if artifact.CompareVersion(ver, cur) > 0 {
			latestByName[name] = ver
		}
	}
	sort.Strings(names)
	return names, latestByName
}

func paginateNames(names []string, limitPtr *int, pageTokenPtr *string) (page []string, next string) {
	start := 0
	if pageTokenPtr != nil && *pageTokenPtr != "" {
		tok := *pageTokenPtr
		i := sort.SearchStrings(names, tok)
		start = i
		for start < len(names) && names[start] <= tok {
			start++
		}
	}
	limit := 0
	if limitPtr != nil {
		limit = *limitPtr
	}
	if limit <= 0 || limit > len(names)-start {
		limit = len(names) - start
	}
	end := start + limit
	page = names[start:end]

	next = ""
	if end < len(names) && len(page) > 0 {
		next = page[len(page)-1]
	}
	return page, next
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
	prefix := withArtifactRoot(iartifact.BuildObjectNamePrefix(appName, userID, sessionID, name))
	result, err := s.cosClient.GetBucket(ctx, prefix)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}
	if result == nil {
		return nil, nil
	}
	versions := make([]artifact.VersionID, 0, len(result.Contents))
	for _, obj := range result.Contents {
		parts := strings.Split(obj.Key, objectKeySep)
		if len(parts) == 0 {
			continue
		}
		v := parts[len(parts)-1]
		if v != "" {
			versions = append(versions, artifact.VersionID(v))
		}
	}
	sort.Slice(versions, func(i, j int) bool { return artifact.CompareVersion(versions[i], versions[j]) < 0 })
	return versions, nil
}

func parseNameAndVersion(objectKey, scopePrefix string) (string, artifact.VersionID, bool) {
	if !strings.HasPrefix(objectKey, scopePrefix) {
		return "", "", false
	}
	rel := strings.TrimPrefix(objectKey, scopePrefix)
	parts := strings.Split(rel, objectKeySep)
	if len(parts) < 2 {
		return "", "", false
	}
	name := strings.Join(parts[:len(parts)-1], objectKeySep)
	ver := parts[len(parts)-1]
	if name == "" || ver == "" {
		return "", "", false
	}
	return name, artifact.VersionID(ver), true
}

func headResponseFromHeader(version artifact.VersionID, h http.Header) artifact.HeadResponse {
	contentType := h.Get("Content-Type")
	if contentType == "" {
		contentType = defaultContentType
	}
	var size int64
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			size = n
		}
	}
	return artifact.HeadResponse{Version: version, MimeType: contentType, Size: size}
}

func openResponseFromHeader(body io.ReadCloser, version artifact.VersionID, h http.Header) artifact.OpenResponse {
	hr := headResponseFromHeader(version, h)
	return artifact.OpenResponse{
		Body:     body,
		Version:  hr.Version,
		MimeType: hr.MimeType,
		Size:     hr.Size,
		URL:      hr.URL,
	}
}

func (s *Service) bestEffortURL(ctx context.Context, objectName string) string {
	if s.secretID != "" && s.secretKey != "" {
		if u, err := s.cosClient.PresignGetObject(ctx, objectName, s.secretID, s.secretKey, s.presignExpires); err == nil && u != nil {
			return u.String()
		}
	}
	if u := s.cosClient.ObjectURL(objectName); u != nil {
		return u.String()
	}
	return ""
}

func validateKeyFields(appName, userID, name string) error {
	if appName == "" || userID == "" {
		return fmt.Errorf("invalid key: missing appName or userID")
	}
	return validateName(name)
}

func validateListFields(appName, userID string) error {
	if appName == "" || userID == "" {
		return fmt.Errorf("invalid namespace: missing appName or userID")
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid name: empty")
	}
	if strings.HasPrefix(name, objectKeySep) ||
		strings.Contains(name, "\\") ||
		strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid name")
	}
	parts := strings.Split(name, objectKeySep)
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("invalid name")
		}
	}
	return nil
}

func validateNamePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, objectKeySep) ||
		strings.Contains(prefix, "\\") ||
		strings.Contains(prefix, "\x00") {
		return fmt.Errorf("invalid name")
	}
	parts := strings.Split(prefix, objectKeySep)
	for i, p := range parts {
		if p == "." || p == ".." {
			return fmt.Errorf("invalid name")
		}
		// Allow trailing slash.
		if p == "" && i != len(parts)-1 {
			return fmt.Errorf("invalid name")
		}
	}
	return nil
}

func withArtifactRoot(key string) string {
	key = strings.TrimPrefix(key, objectKeySep)
	if strings.HasPrefix(key, artifactRootDir+objectKeySep) {
		return key
	}
	return artifactRootDir + objectKeySep + key
}
