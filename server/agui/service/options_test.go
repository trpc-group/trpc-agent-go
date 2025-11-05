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

	// Test with messages snapshot enabled
	opts = NewOptions(WithMessagesSnapshotEnabled(true))
	assert.True(t, opts.MessagesSnapshotEnabled)
	assert.Equal(t, opts.MessagesSnapshotPath, "/history")

	opts = NewOptions(WithMessagesSnapshotPath("/custom"))
	assert.False(t, opts.MessagesSnapshotEnabled)
	assert.Equal(t, opts.MessagesSnapshotPath, "/custom")
}
