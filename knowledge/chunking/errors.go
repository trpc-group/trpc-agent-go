//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package chunking

import "errors"

var (
	// ErrInvalidChunkSize indicates that the chunk size is invalid.
	ErrInvalidChunkSize = errors.New("chunk size must be greater than 0")

	// ErrInvalidOverlap indicates that the overlap value is invalid.
	ErrInvalidOverlap = errors.New("overlap must be non-negative")

	// ErrOverlapTooLarge indicates that the overlap is too large relative to chunk size.
	ErrOverlapTooLarge = errors.New("overlap must be less than chunk size")

	// ErrEmptyDocument indicates that the document has no content to chunk.
	ErrEmptyDocument = errors.New("document content is empty")

	// ErrNilDocument indicates that a nil document was provided.
	ErrNilDocument = errors.New("document cannot be nil")
)
