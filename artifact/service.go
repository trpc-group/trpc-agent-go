//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound indicates the requested artifact or version does not exist.
var ErrNotFound = errors.New("artifact not found")

// Service defines the interface for artifact storage and retrieval operations.
type Service interface {
	// Put stores a new version of the artifact content and returns its descriptor.
	//
	// The implementation MUST create a new immutable version (not overwrite an existing version).
	Put(ctx context.Context, key Key, r io.Reader, opts ...PutOption) (Descriptor, error)

	// Head resolves an artifact version to its metadata and an optional URL.
	//
	// This method SHOULD NOT download artifact contents.
	// When version is nil, Head MUST resolve the latest version.
	Head(ctx context.Context, key Key, version *VersionID) (Descriptor, error)

	// Open opens a streaming reader for an artifact version.
	//
	// This method MUST NOT read the full artifact into memory.
	// When version is nil, Open MUST open the latest version.
	Open(ctx context.Context, key Key, version *VersionID) (io.ReadCloser, Descriptor, error)

	// List lists artifacts within the given prefix, returning descriptors for the latest version
	// of each artifact.
	//
	// Implementations may require multiple backend calls. The returned nextPageToken is empty
	// when there are no more results.
	//
	// The key identifies the namespace (AppName/UserID/SessionID/Scope). key.Name is ignored.
	List(ctx context.Context, key Key, opts ...ListOption) ([]Descriptor, string, error)

	// Delete deletes artifact versions identified by key.
	//
	// By default (no opts, or DeleteAllOpt), it deletes all versions.
	//
	// When the artifact does not exist, it SHOULD return ErrNotFound.
	Delete(ctx context.Context, key Key, opts ...DeleteOption) error

	// Versions lists all versions of an artifact.
	//
	// When the artifact does not exist, it SHOULD return ErrNotFound.
	Versions(ctx context.Context, key Key) ([]VersionID, error)
}

// ReadAll opens an artifact version and reads it into memory.
// This is a convenience helper for small artifacts.
func ReadAll(ctx context.Context, svc Service, key Key, version *VersionID) ([]byte, Descriptor, error) {
	rc, desc, err := svc.Open(ctx, key, version)
	if err != nil {
		return nil, Descriptor{}, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, Descriptor{}, err
	}
	return b, desc, nil
}
