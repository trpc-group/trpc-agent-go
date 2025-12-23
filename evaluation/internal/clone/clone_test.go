//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type sample struct {
	Value string
}

type nonSerializable struct {
	Bad map[string]any
}

func TestCloneSuccess(t *testing.T) {
	src := &sample{Value: "ok"}
	dst, err := Clone(src)
	assert.NoError(t, err)
	assert.NotSame(t, src, dst)
	assert.Equal(t, src, dst)
}

func TestCloneNilInput(t *testing.T) {
	dst, err := Clone[*sample](nil)
	assert.Error(t, err)
	assert.Nil(t, dst)
}

func TestCloneGobError(t *testing.T) {
	src := &nonSerializable{Bad: map[string]any{"c": make(chan int)}}
	dst, err := Clone(src)
	assert.Error(t, err)
	assert.Nil(t, dst)
}
