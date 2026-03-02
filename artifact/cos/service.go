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
//     {app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}/{version}
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
//     {app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}/{version}
type Service struct {
	cosClient client
	secretID  string
	secretKey string

	presignExpires time.Duration
}

const defaultTimeout = 60 * time.Second

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

// Put stores artifact content and returns its descriptor.
func (s *Service) Put(ctx context.Context, key artifact.Key, r io.Reader, opts ...artifact.PutOption) (artifact.Descriptor, error) {
	if err := validateKey(key); err != nil {
		return artifact.Descriptor{}, err
	}

	o := artifact.PutOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	v, err := artifact.NewVersionID()
	if err != nil {
		return artifact.Descriptor{}, err
	}
	objectName := iartifact.BuildObjectName(key, v)

	data, err := io.ReadAll(r)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	mt := o.MimeType
	if mt == "" {
		mt = "application/octet-stream"
	}
	if err := s.cosClient.PutObject(ctx, objectName, bytes.NewReader(data), mt); err != nil {
		return artifact.Descriptor{}, fmt.Errorf("failed to upload artifact: %w", err)
	}
	return artifact.Descriptor{Key: key, Version: v, MimeType: mt, Size: int64(len(data))}, nil
}

// Head resolves an artifact version to its metadata and an optional URL.
func (s *Service) Head(ctx context.Context, key artifact.Key, version *artifact.VersionID) (artifact.Descriptor, error) {
	if err := validateKey(key); err != nil {
		return artifact.Descriptor{}, err
	}
	target, err := s.resolveVersion(ctx, key, version)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	objectName := iartifact.BuildObjectName(key, target)
	h, err := s.cosClient.HeadObject(ctx, objectName)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return artifact.Descriptor{}, artifact.ErrNotFound
		}
		return artifact.Descriptor{}, fmt.Errorf("failed to head artifact: %w", err)
	}
	desc := descriptorFromHeader(key, target, h)
	s.bestEffortURL(ctx, &desc, objectName)
	return desc, nil
}

// Open returns a streaming reader for the artifact content and its descriptor.
func (s *Service) Open(ctx context.Context, key artifact.Key, version *artifact.VersionID) (io.ReadCloser, artifact.Descriptor, error) {
	if err := validateKey(key); err != nil {
		return nil, artifact.Descriptor{}, err
	}
	target, err := s.resolveVersion(ctx, key, version)
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	objectName := iartifact.BuildObjectName(key, target)
	body, header, err := s.cosClient.GetObject(ctx, objectName)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, artifact.Descriptor{}, artifact.ErrNotFound
		}
		return nil, artifact.Descriptor{}, fmt.Errorf("failed to download artifact: %w", err)
	}
	desc := descriptorFromHeader(key, target, header)
	s.bestEffortURL(ctx, &desc, objectName)
	return body, desc, nil
}

// LoadArtifactBytes is replaced by artifact.ReadAll helper.

// List returns the latest version descriptor for each artifact name under the given prefix.
func (s *Service) List(ctx context.Context, prefix artifact.KeyPrefix, opts ...artifact.ListOption) ([]artifact.Descriptor, string, error) {
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
	result, err := s.cosClient.GetBucket(ctx, scopePrefix)
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed to list artifacts: %w", err)
	}
	if result == nil {
		return nil, "", nil
	}

	type latest struct{ version artifact.VersionID }
	latestByName := make(map[string]latest)
	names := make([]string, 0)
	for _, obj := range result.Contents {
		name, ver, ok := parseNameAndVersion(obj.Key, scopePrefix)
		if !ok {
			continue
		}
		if prefix.NamePrefix != "" && !strings.HasPrefix(name, prefix.NamePrefix) {
			continue
		}
		if cur, exists := latestByName[name]; !exists {
			latestByName[name] = latest{version: ver}
			names = append(names, name)
		} else if artifact.CompareVersion(ver, cur.version) > 0 {
			latestByName[name] = latest{version: ver}
		}
	}
	sort.Strings(names)

	start := 0
	if o.PageToken != "" {
		i := sort.SearchStrings(names, o.PageToken)
		start = i
		for start < len(names) && names[start] <= o.PageToken {
			start++
		}
	}
	limit := o.Limit
	if limit <= 0 || limit > len(names)-start {
		limit = len(names) - start
	}
	end := start + limit
	page := names[start:end]

	out := make([]artifact.Descriptor, 0, len(page))
	for _, name := range page {
		key := artifact.Key{AppName: prefix.AppName, UserID: prefix.UserID, SessionID: prefix.SessionID, Scope: prefix.Scope, Name: name}
		ver := latestByName[name].version
		desc, err := s.Head(ctx, key, &ver)
		if err != nil {
			if errors.Is(err, artifact.ErrNotFound) {
				continue
			}
			return nil, "", err
		}
		out = append(out, desc)
	}

	next := ""
	if end < len(names) && len(page) > 0 {
		next = page[len(page)-1]
	}
	return out, next, nil
}

// Delete removes artifact content according to the provided delete options.
func (s *Service) Delete(ctx context.Context, key artifact.Key, opts ...artifact.DeleteOption) error {
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

	switch o.Mode {
	case artifact.DeleteAll:
		versions, err := s.listVersions(ctx, key)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return artifact.ErrNotFound
		}
		for _, v := range versions {
			objectName := iartifact.BuildObjectName(key, v)
			if err := s.cosClient.DeleteObject(ctx, objectName); err != nil && !cos.IsNotFoundError(err) {
				return fmt.Errorf("failed to delete artifact version %s: %w", v, err)
			}
		}
		return nil
	case artifact.DeleteLatest:
		ver, err := s.resolveVersion(ctx, key, nil)
		if err != nil {
			return err
		}
		objectName := iartifact.BuildObjectName(key, ver)
		if err := s.cosClient.DeleteObject(ctx, objectName); err != nil {
			if cos.IsNotFoundError(err) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to delete artifact version %s: %w", ver, err)
		}
		return nil
	case artifact.DeleteVersion:
		objectName := iartifact.BuildObjectName(key, o.Version)
		if err := s.cosClient.DeleteObject(ctx, objectName); err != nil {
			if cos.IsNotFoundError(err) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to delete artifact version %s: %w", o.Version, err)
		}
		return nil
	default:
		return fmt.Errorf("unknown delete mode: %d", int(o.Mode))
	}
}

// Versions lists all versions available for the provided artifact key.
func (s *Service) Versions(ctx context.Context, key artifact.Key) ([]artifact.VersionID, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	versions, err := s.listVersions(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, artifact.ErrNotFound
	}
	return versions, nil
}

func (s *Service) resolveVersion(ctx context.Context, key artifact.Key, version *artifact.VersionID) (artifact.VersionID, error) {
	if version != nil {
		return *version, nil
	}
	versions, err := s.listVersions(ctx, key)
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

func (s *Service) listVersions(ctx context.Context, key artifact.Key) ([]artifact.VersionID, error) {
	prefix := iartifact.BuildObjectNamePrefix(key)
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
		parts := strings.Split(obj.Key, "/")
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
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	name := strings.Join(parts[:len(parts)-1], "/")
	ver := parts[len(parts)-1]
	if name == "" || ver == "" {
		return "", "", false
	}
	return name, artifact.VersionID(ver), true
}

func descriptorFromHeader(key artifact.Key, version artifact.VersionID, h http.Header) artifact.Descriptor {
	contentType := h.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var size int64
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			size = n
		}
	}
	return artifact.Descriptor{Key: key, Version: version, MimeType: contentType, Size: size}
}

func (s *Service) bestEffortURL(ctx context.Context, desc *artifact.Descriptor, objectName string) {
	if desc == nil {
		return
	}
	if s.secretID != "" && s.secretKey != "" {
		if u, err := s.cosClient.PresignGetObject(ctx, objectName, s.secretID, s.secretKey, s.presignExpires); err == nil && u != nil {
			desc.URL = u.String()
			return
		}
	}
	if u := s.cosClient.ObjectURL(objectName); u != nil {
		desc.URL = u.String()
	}
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
	default:
		return fmt.Errorf("invalid key: unknown scope")
	}
	return validateName(k.Name)
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
	default:
		return fmt.Errorf("invalid prefix: unknown scope")
	}
	if p.NamePrefix == "" {
		return nil
	}
	return validateNamePrefix(p.NamePrefix)
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid name: empty")
	}
	if strings.HasPrefix(name, "/") ||
		strings.Contains(name, "\\") ||
		strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid name")
	}
	parts := strings.Split(name, "/")
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
	if strings.HasPrefix(prefix, "/") ||
		strings.Contains(prefix, "\\") ||
		strings.Contains(prefix, "\x00") {
		return fmt.Errorf("invalid name")
	}
	parts := strings.Split(prefix, "/")
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
