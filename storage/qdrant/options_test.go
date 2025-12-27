//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientBuilderOpts(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{}

	WithHost("localhost")(opts)
	WithPort(6334)(opts)

	assert.Equal(t, "localhost", opts.Host)
	assert.Equal(t, 6334, opts.Port)
}

func TestWithHost(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{}
	WithHost("custom-host")(opts)
	assert.Equal(t, "custom-host", opts.Host)
}

func TestWithHostEmpty(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{Host: "original"}
	WithHost("")(opts)
	assert.Equal(t, "original", opts.Host)
}

func TestWithPort(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{}
	WithPort(9999)(opts)
	assert.Equal(t, 9999, opts.Port)
}

func TestWithPortInvalid(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{Port: 6334}

	WithPort(-1)(opts)
	assert.Equal(t, 6334, opts.Port)

	WithPort(0)(opts)
	assert.Equal(t, 6334, opts.Port)

	WithPort(70000)(opts)
	assert.Equal(t, 6334, opts.Port)
}

func TestWithAPIKey(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{}
	WithAPIKey("secret-key")(opts)
	assert.Equal(t, "secret-key", opts.APIKey)
}

func TestWithTLS(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{}
	WithTLS(true)(opts)
	assert.True(t, opts.UseTLS)
}

func TestWithTLSFalse(t *testing.T) {
	t.Parallel()
	opts := &ClientBuilderOpts{UseTLS: true}
	WithTLS(false)(opts)
	assert.False(t, opts.UseTLS)
}
