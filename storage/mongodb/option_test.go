//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithClientBuilderDSN(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithClientBuilderDSN("mongodb://localhost:27017")(opts)
	assert.Equal(t, "mongodb://localhost:27017", opts.URI)

	// Test overwrite
	WithClientBuilderDSN("mongodb://otherhost:27017")(opts)
	assert.Equal(t, "mongodb://otherhost:27017", opts.URI)
}

func TestWithExtraOptions(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithExtraOptions("opt1", "opt2")(opts)
	assert.Len(t, opts.ExtraOptions, 2)
	assert.Equal(t, "opt1", opts.ExtraOptions[0])
	assert.Equal(t, "opt2", opts.ExtraOptions[1])
}

func TestWithExtraOptionsAppend(t *testing.T) {
	opts := &ClientBuilderOpts{}

	// First call
	WithExtraOptions("opt1")(opts)
	assert.Len(t, opts.ExtraOptions, 1)

	// Second call should append
	WithExtraOptions("opt2", "opt3")(opts)
	assert.Len(t, opts.ExtraOptions, 3)
	assert.Equal(t, "opt1", opts.ExtraOptions[0])
	assert.Equal(t, "opt2", opts.ExtraOptions[1])
	assert.Equal(t, "opt3", opts.ExtraOptions[2])
}

func TestClientBuilderOptsDefaults(t *testing.T) {
	opts := &ClientBuilderOpts{}

	assert.Empty(t, opts.URI)
	assert.Nil(t, opts.ExtraOptions)
}

func TestWithExtraOptionsEmpty(t *testing.T) {
	opts := &ClientBuilderOpts{}

	// Call with no options
	WithExtraOptions()(opts)
	assert.Empty(t, opts.ExtraOptions)
}

func TestWithExtraOptionsMixedTypes(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithExtraOptions("string", 123, true, struct{ Name string }{"test"})(opts)
	assert.Len(t, opts.ExtraOptions, 4)
	assert.Equal(t, "string", opts.ExtraOptions[0])
	assert.Equal(t, 123, opts.ExtraOptions[1])
	assert.Equal(t, true, opts.ExtraOptions[2])
}
