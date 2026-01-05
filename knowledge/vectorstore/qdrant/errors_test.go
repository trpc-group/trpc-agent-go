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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSentinelErrors_AreDistinct(t *testing.T) {
	t.Parallel()
	assert.False(t, errors.Is(ErrNotFound, ErrInvalidInput))
	assert.False(t, errors.Is(ErrNotFound, ErrConnectionFailed))
	assert.False(t, errors.Is(ErrInvalidInput, ErrConnectionFailed))
}

func TestSentinelErrors_CanBeJoined(t *testing.T) {
	t.Parallel()
	originalErr := errors.New("connection refused")
	joined := errors.Join(ErrConnectionFailed, originalErr)

	assert.True(t, errors.Is(joined, ErrConnectionFailed))
	assert.True(t, errors.Is(joined, originalErr))
}

func TestInputValidationErrors(t *testing.T) {
	t.Parallel()
	assert.True(t, errors.Is(errDocumentRequired, ErrInvalidInput))
	assert.True(t, errors.Is(errDocumentIDRequired, ErrInvalidInput))
	assert.True(t, errors.Is(errIDRequired, ErrInvalidInput))
	assert.True(t, errors.Is(errQueryRequired, ErrInvalidInput))
	assert.True(t, errors.Is(errEmbeddingRequired, ErrInvalidInput))
}

func TestInputValidationErrors_Messages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err      error
		contains string
	}{
		{errDocumentRequired, "document is required"},
		{errDocumentIDRequired, "document ID is required"},
		{errIDRequired, "id is required"},
		{errQueryRequired, "query is required"},
		{errEmbeddingRequired, "embedding is required"},
	}

	for _, tt := range tests {
		t.Run(tt.contains, func(t *testing.T) {
			assert.Contains(t, tt.err.Error(), tt.contains)
		})
	}
}
