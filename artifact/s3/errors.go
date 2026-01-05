//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package s3

import "errors"

// Sentinel errors for artifact validation.
var (
	// ErrEmptyFilename is returned when the filename is empty.
	ErrEmptyFilename = errors.New("s3 artifact: filename cannot be empty")

	// ErrInvalidFilename is returned when the filename contains invalid characters.
	ErrInvalidFilename = errors.New("s3 artifact: filename contains invalid characters")

	// ErrNilArtifact is returned when the artifact is nil.
	ErrNilArtifact = errors.New("s3 artifact: artifact cannot be nil")

	// ErrEmptySessionInfo is returned when required session info fields are empty.
	ErrEmptySessionInfo = errors.New("s3 artifact: session info fields cannot be empty")
)
