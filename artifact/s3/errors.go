//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package s3

import (
	"errors"
)

// Sentinel errors for S3 operations.
var (
	// ErrNotFound is returned when an object does not exist.
	ErrNotFound = errors.New("s3: object not found")

	// ErrAccessDenied is returned when access to an object is denied.
	ErrAccessDenied = errors.New("s3: access denied")

	// ErrInvalidConfig is returned when the service configuration is invalid.
	ErrInvalidConfig = errors.New("s3: invalid configuration")

	// ErrBucketNotFound is returned when the bucket does not exist.
	ErrBucketNotFound = errors.New("s3: bucket not found")
)
