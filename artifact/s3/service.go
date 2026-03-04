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

// Put stores artifact content and returns its descriptor.
func (s *Service) Put(
	ctx context.Context,
	key artifact.Key,
	r io.Reader,
	opts ...artifact.PutOption,
) (artifact.Descriptor, error) {
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
	objectKey := iartifact.BuildObjectName(key, v)
	contentType := cmp.Or(o.MimeType, defaultContentType)

	data, err := io.ReadAll(r)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if err := s.client.PutObject(ctx, objectKey, data, contentType); err != nil {
		return artifact.Descriptor{}, fmt.Errorf("failed to upload artifact: %w", err)
	}
	return artifact.Descriptor{
		Key:      key,
		Version:  v,
		MimeType: contentType,
		Size:     int64(len(data)),
	}, nil
}

// Head resolves an artifact version to its metadata and an optional URL.
func (s *Service) Head(
	ctx context.Context,
	key artifact.Key,
	version *artifact.VersionID,
) (artifact.Descriptor, error) {
	if err := validateKey(key); err != nil {
		return artifact.Descriptor{}, err
	}
	target, err := s.resolveVersion(ctx, key, version)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	objectKey := iartifact.BuildObjectName(key, target)
	contentType, size, err := s.client.HeadObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return artifact.Descriptor{}, artifact.ErrNotFound
		}
		return artifact.Descriptor{}, fmt.Errorf("failed to head artifact: %w", err)
	}
	desc := artifact.Descriptor{
		Key:      key,
		Version:  target,
		MimeType: cmp.Or(contentType, defaultContentType),
		Size:     size,
	}
	if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
		desc.URL = u
	}
	return desc, nil
}

// Open returns a streaming reader for the artifact content and its descriptor.
func (s *Service) Open(
	ctx context.Context,
	key artifact.Key,
	version *artifact.VersionID,
) (io.ReadCloser, artifact.Descriptor, error) {
	if err := validateKey(key); err != nil {
		return nil, artifact.Descriptor{}, err
	}
	target, err := s.resolveVersion(ctx, key, version)
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	objectKey := iartifact.BuildObjectName(key, target)
	body, contentType, size, err := s.client.OpenObject(ctx, objectKey)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, artifact.Descriptor{}, artifact.ErrNotFound
		}
		return nil, artifact.Descriptor{}, fmt.Errorf("failed to open artifact: %w", err)
	}
	desc := artifact.Descriptor{
		Key:      key,
		Version:  target,
		MimeType: cmp.Or(contentType, defaultContentType),
		Size:     size,
	}
	if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
		desc.URL = u
	}
	return body, desc, nil
}

// List returns the latest version descriptor for each artifact name under the given prefix.
func (s *Service) List(
	ctx context.Context,
	key artifact.Key,
	opts ...artifact.ListOption,
) ([]artifact.Descriptor, string, error) {
	if err := validateListKey(key); err != nil {
		return nil, "", err
	}

	o := artifact.ListOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	scopePrefix := iartifact.BuildListPrefix(key)
	keys, err := s.client.ListObjects(ctx, scopePrefix)
	if err != nil {
		if errors.Is(err, s3storage.ErrNotFound) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed to list artifacts: %w", err)
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
	if o.PageToken != "" {
		i, _ := slices.BinarySearch(names, o.PageToken)
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
		itemKey := artifact.Key{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: key.SessionID,
			Scope:     key.Scope,
			Name:      name,
		}
		ver := latestByName[name].version
		objectKey := iartifact.BuildObjectName(itemKey, ver)
		contentType, size, err := s.client.HeadObject(ctx, objectKey)
		if err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				continue
			}
			return nil, "", fmt.Errorf("failed to head listed artifact: %w", err)
		}
		desc := artifact.Descriptor{
			Key:      itemKey,
			Version:  ver,
			MimeType: cmp.Or(contentType, defaultContentType),
			Size:     size,
		}
		if u, err := s.client.PresignGetObject(ctx, objectKey, s.presignExpires); err == nil && u != "" {
			desc.URL = u
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
		prefix := iartifact.BuildObjectNamePrefix(key)
		keys, err := s.client.ListObjects(ctx, prefix)
		if err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to list artifact versions: %w", err)
		}
		if len(keys) == 0 {
			return artifact.ErrNotFound
		}
		if err := s.client.DeleteObjects(ctx, keys); err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to delete artifact: %w", err)
		}
		return nil
	case artifact.DeleteLatest:
		ver, err := s.resolveVersion(ctx, key, nil)
		if err != nil {
			return err
		}
		objectKey := iartifact.BuildObjectName(key, ver)
		if err := s.client.DeleteObjects(ctx, []string{objectKey}); err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to delete artifact: %w", err)
		}
		return nil
	case artifact.DeleteVersion:
		objectKey := iartifact.BuildObjectName(key, o.Version)
		if err := s.client.DeleteObjects(ctx, []string{objectKey}); err != nil {
			if errors.Is(err, s3storage.ErrNotFound) {
				return artifact.ErrNotFound
			}
			return fmt.Errorf("failed to delete artifact: %w", err)
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
		if s.logger != nil {
			s.logger.Debugf("artifact not found: %s/%s/%s/%s",
				key.AppName, key.UserID, key.SessionID, key.Name)
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

func (s *Service) listVersions(ctx context.Context, key artifact.Key) ([]artifact.VersionID, error) {
	prefix := iartifact.BuildObjectNamePrefix(key)
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

func validateKey(k artifact.Key) error {
	if k.AppName == "" || k.UserID == "" {
		return ErrEmptySessionInfo
	}
	switch k.Scope {
	case artifact.ScopeSession:
		if k.SessionID == "" {
			return ErrEmptySessionInfo
		}
	case artifact.ScopeUser:
		// ok
	default:
		return ErrEmptySessionInfo
	}
	return validateName(k.Name)
}

func validateListKey(k artifact.Key) error {
	if k.AppName == "" || k.UserID == "" {
		return ErrEmptySessionInfo
	}
	switch k.Scope {
	case artifact.ScopeSession:
		if k.SessionID == "" {
			return ErrEmptySessionInfo
		}
	case artifact.ScopeUser:
		// ok
	default:
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
