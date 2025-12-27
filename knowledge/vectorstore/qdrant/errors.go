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
	ErrNotFound         = errors.New("qdrant: not found")
	ErrInvalidInput     = errors.New("qdrant: invalid input")
	ErrConnectionFailed = errors.New("qdrant: connection failed")
)

// Input validation errors.
var (
	errDocumentRequired   = fmt.Errorf("%w: document is required", ErrInvalidInput)
	errDocumentIDRequired = fmt.Errorf("%w: document ID is required", ErrInvalidInput)
	errIDRequired         = fmt.Errorf("%w: id is required", ErrInvalidInput)
	errQueryRequired      = fmt.Errorf("%w: query is required", ErrInvalidInput)
	errEmbeddingRequired  = fmt.Errorf("%w: embedding is required", ErrInvalidInput)
)
