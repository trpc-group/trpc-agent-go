//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import "time"

// HeadRequest identifies which artifact metadata to retrieve.
type HeadRequest struct {
	// SessionInfo is the session information (app name, user ID, session ID).
	SessionInfo SessionInfo
	// Filename is the name of the artifact.
	// Filenames may include the "user:" prefix for user-scoped artifacts.
	Filename string
	// Version selects a specific version of the artifact.
	// If nil, the latest version is used.
	Version *int
}

// HeadResponse contains artifact metadata.
// It never includes the artifact payload bytes.
type HeadResponse struct {
	// Filename is the artifact filename.
	Filename string
	// Version is the resolved artifact version.
	Version int
	// Size is the artifact payload size in bytes.
	Size int64
	// MimeType is the artifact MIME type.
	MimeType string
	// URL is an optional URL where the artifact can be accessed.
	URL string
	// Name is an optional display name of the artifact.
	Name string
}

// HeadOptions controls optional Head behavior.
type HeadOptions struct {
	// IncludeURL controls whether Head should try to populate HeadResponse.URL.
	// This is best-effort and may still be empty depending on backend support
	// and access settings.
	IncludeURL bool

	// PresignedURL controls whether Head should try to populate HeadResponse.URL
	// with a presigned download URL when supported by the backend.
	// This implies IncludeURL and is best-effort.
	PresignedURL bool

	// PresignedURLExpires sets how long the presigned URL should remain valid.
	// It is ignored when PresignedURL is false.
	PresignedURLExpires time.Duration
}

// HeadOption configures a Head call.
type HeadOption func(*HeadOptions)

// WithIncludeURL toggles best-effort URL population in HeadResponse.
func WithIncludeURL(include bool) HeadOption {
	return func(o *HeadOptions) { o.IncludeURL = include }
}

// WithPresignedURL requests a presigned download URL (best effort).
// Backend implementations may apply a default when expires <= 0.
//
// This implies WithIncludeURL(true).
func WithPresignedURL(expires time.Duration) HeadOption {
	return func(o *HeadOptions) {
		o.IncludeURL = true
		o.PresignedURL = true
		o.PresignedURLExpires = expires
	}
}
