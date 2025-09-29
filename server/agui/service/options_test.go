//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithPath(t *testing.T) {
	// Test default path
	opts := NewOptions()
	assert.Equal(t, opts.Path, "/")

	// Test with path
	opts = NewOptions(WithPath("/sse"))
	assert.Equal(t, opts.Path, "/sse")
}
