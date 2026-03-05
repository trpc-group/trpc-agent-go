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
	// Put stores a new version of the artifact content and returns metadata.
	//
	// The implementation MUST create a new immutable version (not overwrite an existing version).
	Put(ctx context.Context, req *PutRequest, opts ...PutOption) (*PutResponse, error)

	// Head resolves an artifact version to its metadata and an optional URL.
	//
	// This method SHOULD NOT download artifact contents.
	// When req.Version is nil, Head MUST resolve the latest version.
	Head(ctx context.Context, req *HeadRequest, opts ...HeadOption) (*HeadResponse, error)

	// Open opens a streaming reader for an artifact version.
	//
	// This method MUST NOT read the full artifact into memory.
	// When req.Version is nil, Open MUST open the latest version.
	Open(ctx context.Context, req *OpenRequest, opts ...OpenOption) (*OpenResponse, error)

	// List lists artifacts within the given namespace, returning metadata for the latest version
	// of each artifact.
	//
	// Implementations may require multiple backend calls. The returned NextPageToken is empty
	// when there are no more results.
	List(ctx context.Context, req *ListRequest, opts ...ListOption) (*ListResponse, error)

	// Delete deletes artifact versions identified by the request.
	//
	// Delete is idempotent by default: deleting a non-existing artifact/version is not an error.
	//
	// When req.Version is nil, it deletes all versions.
	// When req.Version is non-nil, it deletes the specified version.
	Delete(ctx context.Context, req *DeleteRequest, opts ...DeleteOption) (*DeleteResponse, error)

	// Versions lists all versions of an artifact.
	//
	// When the artifact does not exist, it SHOULD return ErrNotFound.
	Versions(ctx context.Context, req *VersionsRequest, opts ...VersionsOption) (*VersionsResponse, error)
}

// PutRequest is the input for [Service.Put].
type PutRequest struct {
	AppName   string
	UserID    string
	SessionID string // Optional. When set, services may use it to namespace artifacts.
	Name      string

	Body io.Reader

	// Optional fields.
	MimeType string
}

// PutResponse is the output for [Service.Put].
type PutResponse struct {
	Version  VersionID
	MimeType string
	Size     int64
	URL      string
}

// HeadOptions configures Head behavior (reserved for future).
type HeadOptions struct{}

// HeadOption configures Head behavior (functional options style).
type HeadOption func(*HeadOptions)

// HeadRequest is the input for [Service.Head].
type HeadRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Name      string

	// Version is the target artifact version. When nil, the latest version is resolved.
	Version *VersionID
}

// HeadResponse is the output for [Service.Head].
type HeadResponse struct {
	Version  VersionID
	MimeType string
	Size     int64
	URL      string
}

// OpenOptions configures Open behavior (reserved for future).
type OpenOptions struct{}

// OpenOption configures Open behavior (functional options style).
type OpenOption func(*OpenOptions)

// OpenRequest is the input for [Service.Open].
type OpenRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Name      string

	// Version is the target artifact version. When nil, the latest version is opened.
	Version *VersionID
}

// OpenResponse is the output for [Service.Open].
type OpenResponse struct {
	Body io.ReadCloser

	Version  VersionID
	MimeType string
	Size     int64
	URL      string
}

// VersionsOptions configures Versions behavior (reserved for future).
type VersionsOptions struct{}

// VersionsOption configures Versions behavior (functional options style).
type VersionsOption func(*VersionsOptions)

// ListRequest is the input for [Service.List].
type ListRequest struct {
	AppName   string
	UserID    string
	SessionID string // Optional. When set, services may use it to namespace artifacts.

	// Limit and PageToken control pagination. Nil means not specified.
	Limit     *int
	PageToken *string
}

// ListItem is an item returned by [Service.List].
type ListItem struct {
	Name     string
	Version  VersionID
	MimeType string
	Size     int64
	URL      string
}

// ListResponse is the output for [Service.List].
type ListResponse struct {
	Items         []ListItem
	NextPageToken string
}

// DeleteRequest is the input for [Service.Delete].
type DeleteRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Name      string

	// Version controls which versions are deleted.
	// When nil, all versions are deleted.
	// When non-nil, only the specified version is deleted.
	Version *VersionID
}

// DeleteResponse is the output for [Service.Delete].
type DeleteResponse struct {
	Deleted bool
}

// VersionsRequest is the input for [Service.Versions].
type VersionsRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Name      string
}

// VersionsResponse is the output for [Service.Versions].
type VersionsResponse struct {
	Versions []VersionID
}

// ReadAll opens an artifact version and reads it into memory.
// This is a convenience helper for small artifacts.
func ReadAll(ctx context.Context, svc Service, req *OpenRequest, opts ...OpenOption) ([]byte, HeadResponse, error) {
	resp, err := svc.Open(ctx, req, opts...)
	if err != nil {
		return nil, HeadResponse{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, HeadResponse{}, err
	}
	return b, HeadResponse{Version: resp.Version, MimeType: resp.MimeType, Size: resp.Size, URL: resp.URL}, nil
}
