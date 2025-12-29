//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"errors"
	"fmt"
)

// Sentinel errors for Qdrant operations.
var (
	ErrNotFound           = errors.New("qdrant: not found")
	ErrInvalidInput       = errors.New("qdrant: invalid input")
	ErrInvalidConfig      = errors.New("qdrant: invalid configuration")
	ErrConnectionFailed   = errors.New("qdrant: connection failed")
	ErrCollectionMismatch = errors.New("qdrant: collection configuration mismatch")
	ErrInvalidFilter      = errors.New("qdrant: invalid filter")
)

// Input validation errors.
var (
	errDocumentRequired   = fmt.Errorf("%w: document is required", ErrInvalidInput)
	errDocumentIDRequired = fmt.Errorf("%w: document ID is required", ErrInvalidInput)
	errIDRequired         = fmt.Errorf("%w: id is required", ErrInvalidInput)
	errQueryRequired      = fmt.Errorf("%w: query is required", ErrInvalidInput)
	errEmbeddingRequired  = fmt.Errorf("%w: embedding is required", ErrInvalidInput)
	errVectorRequired     = fmt.Errorf("%w: vector is required for vector search", ErrInvalidInput)
	errQueryTextRequired  = fmt.Errorf("%w: query text is required for keyword search", ErrInvalidInput)
)

// ErrUnsupportedSearchMode is returned when a search mode is not supported by Qdrant.
var ErrUnsupportedSearchMode = errors.New("qdrant: unsupported search mode")
