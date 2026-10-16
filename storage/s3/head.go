//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package s3

// HeadObjectRequest identifies the object whose metadata should be retrieved.
type HeadObjectRequest struct {
	// Key is the object key in the configured bucket.
	Key string
}

// HeadObjectResponse contains object metadata returned by HeadObject.
type HeadObjectResponse struct {
	// Size is the object size in bytes.
	Size int64
	// ContentType is the object's content type.
	ContentType string
}

// HeadObjectOptions controls optional HeadObject behavior.
// It is reserved for future use.
type HeadObjectOptions struct{}

// HeadObjectOption configures a HeadObject call.
type HeadObjectOption func(*HeadObjectOptions)
