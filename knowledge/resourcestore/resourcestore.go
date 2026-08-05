//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package resourcestore defines persistent storage for source-level text
// resources and their safe metadata.
package resourcestore

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrNotFound means the requested source or resource does not exist.
	ErrNotFound = errors.New("resource not found")
	// ErrRepresentationUnavailable means a resource has no readable text representation.
	ErrRepresentationUnavailable = errors.New("resource representation unavailable")
	// ErrPermissionDenied means the store rejected access to the resource.
	ErrPermissionDenied = errors.New("resource permission denied")
	// ErrUnavailable means the resource store cannot currently serve the request.
	ErrUnavailable = errors.New("resource store unavailable")
)

// SourceInfo describes one persisted logical source without exposing provider
// connection details.
type SourceInfo struct {
	// ID is the stable source identifier used by resource calls and chunk metadata.
	ID string `json:"id"`
	// Name is the human-readable source name.
	Name string `json:"name"`
	// Type is the source type, such as file, dir, repo, or url.
	Type string `json:"type,omitempty"`
}

// ResourceInfo describes one persisted file or directory. Path is relative to
// the source root. Size is the stored text representation size in bytes; a
// negative value means unknown.
type ResourceInfo struct {
	// SourceID identifies the source that owns the resource.
	SourceID string `json:"source_id"`
	// Path is a safe path relative to the source root.
	Path string `json:"path"`
	// Name is the display name of the resource.
	Name string `json:"name"`
	// IsDir reports whether the resource is a directory.
	IsDir bool `json:"is_dir"`
	// Size is the stored text representation size in bytes. A negative value means unknown.
	Size int64 `json:"size"`
	// ModifiedAt is the source modification time when available.
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

// Store persists source-level text resources separately from chunks and vector
// indexes. Write methods are used by ingestion; read methods are used at Agent
// runtime. Implementations must be safe for concurrent use and honor context
// cancellation. All writes and deletes should be idempotent so a failed import
// can be retried safely.
type Store interface {
	// PutSource inserts or updates safe metadata for one logical source.
	PutSource(ctx context.Context, source *SourceInfo) error

	// PutResource inserts or replaces one normalized text file. Info.IsDir must
	// be false. The method consumes content before returning and must not retain
	// the reader. Stores derive directory entries from persisted file paths;
	// empty directories are not represented.
	PutResource(ctx context.Context, info *ResourceInfo, content io.Reader) error

	// DeleteResource deletes one resource by its stable source-relative key.
	DeleteResource(ctx context.Context, sourceID, path string) error

	// DeleteSource deletes a source and all resources owned by it.
	DeleteSource(ctx context.Context, sourceID string) error

	// ListSources lists persisted logical sources.
	ListSources(ctx context.Context) ([]*SourceInfo, error)

	// ListResources lists direct children of parentPath. An empty parentPath
	// addresses the source root.
	ListResources(ctx context.Context, sourceID, parentPath string) ([]*ResourceInfo, error)

	// OpenResource opens the persisted UTF-8 text representation at path. The
	// caller owns and must close the returned stream. Reads from the stream must
	// unblock when ctx is canceled.
	OpenResource(ctx context.Context, sourceID, path string) (io.ReadCloser, error)

	// Close releases resources held by the store.
	Close() error
}
