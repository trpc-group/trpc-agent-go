//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides internal utilities
// management in the trpc-agent-go framework.
package util

import (
	"hash/fnv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStableHash32_MatchesStdlibFNV1a(t *testing.T) {
	key := "hello"
	h := fnv.New32a()
	_, err := h.Write([]byte(key))
	assert.NoError(t, err)
	assert.Equal(t, h.Sum32(), StableHash32(key))
}

func TestStableHashInt_MasksToNonNegative(t *testing.T) {
	key := "hello"
	sum := StableHash32(key)
	got := StableHashInt(key)
	assert.GreaterOrEqual(t, got, 0)
	assert.Equal(t, int(sum&0x7fffffff), got)
}

func TestStableHashIndex(t *testing.T) {
	key := "hello"
	assert.Equal(t, 0, StableHashIndex(key, 0))
	assert.Equal(t, 0, StableHashIndex(key, -1))

	idx := StableHashIndex(key, 10)
	assert.GreaterOrEqual(t, idx, 0)
	assert.Less(t, idx, 10)
	assert.Equal(t, StableHashInt(key)%10, idx)
}
