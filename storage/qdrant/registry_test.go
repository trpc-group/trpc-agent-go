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

func TestRegisterAndGetQdrantInstance(t *testing.T) {
	defer UnregisterQdrantInstance("test-register-get")

	RegisterQdrantInstance("test-register-get", WithHost("test-host"), WithPort(1234))

	opts, ok := GetQdrantInstance("test-register-get")
	assert.True(t, ok)
	assert.Len(t, opts, 2)

	// Apply options to verify they work
	builderOpts := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(builderOpts)
	}
	assert.Equal(t, "test-host", builderOpts.Host)
	assert.Equal(t, 1234, builderOpts.Port)
}

func TestGetQdrantInstanceNotFound(t *testing.T) {
	t.Parallel()
	_, ok := GetQdrantInstance("nonexistent-instance-xyz")
	assert.False(t, ok)
}

func TestUnregisterQdrantInstance(t *testing.T) {
	RegisterQdrantInstance("test-unregister", WithHost("host"))

	// Verify it exists
	_, ok := GetQdrantInstance("test-unregister")
	assert.True(t, ok)

	// Unregister
	UnregisterQdrantInstance("test-unregister")

	// Verify it's gone
	_, ok = GetQdrantInstance("test-unregister")
	assert.False(t, ok)
}

func TestListQdrantInstances(t *testing.T) {
	// Clean up after test
	defer func() {
		UnregisterQdrantInstance("test-list-1")
		UnregisterQdrantInstance("test-list-2")
	}()

	RegisterQdrantInstance("test-list-1", WithHost("host1"))
	RegisterQdrantInstance("test-list-2", WithHost("host2"))

	names := ListQdrantInstances()

	// Check that our registered instances are in the list
	found1, found2 := false, false
	for _, name := range names {
		if name == "test-list-1" {
			found1 = true
		}
		if name == "test-list-2" {
			found2 = true
		}
	}
	assert.True(t, found1, "test-list-1 should be in the list")
	assert.True(t, found2, "test-list-2 should be in the list")
}

func TestRegisterQdrantInstanceOverwrite(t *testing.T) {
	defer UnregisterQdrantInstance("test-overwrite")

	// Register with first config
	RegisterQdrantInstance("test-overwrite", WithHost("host1"), WithPort(1111))

	// Overwrite with second config
	RegisterQdrantInstance("test-overwrite", WithHost("host2"), WithPort(2222))

	opts, ok := GetQdrantInstance("test-overwrite")
	assert.True(t, ok)

	builderOpts := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(builderOpts)
	}

	// Should have the second config
	assert.Equal(t, "host2", builderOpts.Host)
	assert.Equal(t, 2222, builderOpts.Port)
}
