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
	ID    string
	Attrs map[string]int
}

func TestCloneSuccess(t *testing.T) {

	src := &sample{
		ID: "item",
		Attrs: map[string]int{
			"score": 1,
		},
	}
	dst, err := Clone(src)
	assert.NoError(t, err)
	assert.NotSame(t, src, dst)
	assert.Equal(t, src, dst)

	dst.Attrs["score"] = 2
	assert.Equal(t, 1, src.Attrs["score"])
}

func TestCloneNilInput(t *testing.T) {

	var src *sample
	clone, err := Clone(src)
	assert.Nil(t, clone)
	assert.Error(t, err)
}
