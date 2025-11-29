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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIf(t *testing.T) {
	assert.Equal(t, 1, If(true, 1, 2))
	assert.Equal(t, 2, If(false, 1, 2))
	assert.Equal(t, "2", If(false, "1", "2"))
	assert.Equal(t, "1", If(true, "1", "2"))
}

func lazy[T any](v T) Lazy[T] {
	return func() T {
		return v
	}
}

func TestIfLazy(t *testing.T) {
	assert.Equal(t, 1, IfLazy(true, lazy(1), lazy(2)))
	assert.Equal(t, 2, IfLazy(false, lazy(1), lazy(2)))
	assert.Equal(t, "1", IfLazy(true, lazy("1"), lazy("2")))
	assert.Equal(t, "2", IfLazy(false, lazy("1"), lazy("2")))
}

func TestIfLazyL(t *testing.T) {
	assert.Equal(t, 1, IfLazyL(true, lazy(1), 2))
	assert.Equal(t, 2, IfLazyL(false, lazy(1), 2))
	assert.Equal(t, "1", IfLazyL(true, lazy("1"), "2"))
	assert.Equal(t, "2", IfLazyL(false, lazy("1"), "2"))

}

func TestIfLazyR(t *testing.T) {
	assert.Equal(t, 1, IfLazyR(true, 1, lazy(2)))
	assert.Equal(t, 2, IfLazyR(false, 1, lazy(2)))
	assert.Equal(t, "1", IfLazyR(true, "1", lazy("2")))
	assert.Equal(t, "2", IfLazyR(false, "1", lazy("2")))
}
