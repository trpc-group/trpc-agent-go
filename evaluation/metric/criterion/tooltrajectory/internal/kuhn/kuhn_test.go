//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package kuhn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFullLeftMatchSuccess(t *testing.T) {
	matcher := New(2, 2)
	matcher.AddEdge(0, 0)
	matcher.AddEdge(0, 1)
	matcher.AddEdge(1, 0)

	unmatched, err := matcher.FullLeftMatch()
	assert.NoError(t, err)
	assert.Nil(t, unmatched)
}

func TestFullLeftMatchUnmatched(t *testing.T) {
	matcher := New(2, 1)
	matcher.AddEdge(0, 0)

	unmatched, err := matcher.FullLeftMatch()
	assert.Error(t, err)
	assert.Equal(t, []int{1}, unmatched)
}

func TestFullLeftMatchEmptySides(t *testing.T) {
	matcher := New(0, 3)
	unmatched, err := matcher.FullLeftMatch()
	assert.NoError(t, err)
	assert.Nil(t, unmatched)
}

func TestFullLeftMatchNoRightVertices(t *testing.T) {
	matcher := New(1, 0)

	unmatched, err := matcher.FullLeftMatch()
	assert.Error(t, err)
	assert.Equal(t, []int{0}, unmatched)
}
