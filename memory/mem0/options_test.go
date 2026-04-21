//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func apply(opts ...ServiceOpt) serviceOpts {
	o := defaultOptions.clone()
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func TestWithHost(t *testing.T) {
	assert.Equal(t, defaultHost, apply(WithHost("")).host, "empty host should be ignored")
	assert.Equal(t, "https://example.com", apply(WithHost("https://example.com")).host)
}

func TestWithAPIKey(t *testing.T) {
	assert.Empty(t, apply(WithAPIKey("")).apiKey, "empty api key should be ignored")
	assert.Equal(t, "k", apply(WithAPIKey("k")).apiKey)
}

func TestWithOrgProject(t *testing.T) {
	got := apply(WithOrgProject("o", "p"))
	assert.Equal(t, "o", got.orgID)
	assert.Equal(t, "p", got.projectID)
}

func TestWithAsyncMode(t *testing.T) {
	assert.False(t, apply(WithAsyncMode(false)).asyncMode)
	assert.True(t, apply(WithAsyncMode(true)).asyncMode)
}

func TestWithVersion(t *testing.T) {
	assert.Equal(t, defaultOptions.version, apply(WithVersion("")).version, "empty version should be ignored")
	assert.Equal(t, "v3", apply(WithVersion("v3")).version)
}

func TestWithTimeout(t *testing.T) {
	assert.Equal(t, defaultTimeout, apply(WithTimeout(0)).timeout, "zero should be ignored")
	assert.Equal(t, defaultTimeout, apply(WithTimeout(-1)).timeout, "negative should be ignored")
	assert.Equal(t, 5*time.Second, apply(WithTimeout(5*time.Second)).timeout)
}

func TestWithHTTPClient(t *testing.T) {
	assert.Nil(t, apply(WithHTTPClient(nil)).client, "nil should be ignored")
	hc := &http.Client{}
	assert.Same(t, hc, apply(WithHTTPClient(hc)).client)
}

func TestWithLoadToolEnabled(t *testing.T) {
	assert.True(t, apply(WithLoadToolEnabled(true)).loadToolEnabled)
	assert.False(t, apply(WithLoadToolEnabled(false)).loadToolEnabled)
}

func TestWithAsyncMemoryNum(t *testing.T) {
	assert.Equal(t, defaultAsyncMemoryNum, apply(WithAsyncMemoryNum(0)).asyncMemoryNum)
	assert.Equal(t, defaultAsyncMemoryNum, apply(WithAsyncMemoryNum(-1)).asyncMemoryNum)
	assert.Equal(t, 4, apply(WithAsyncMemoryNum(4)).asyncMemoryNum)
}

func TestWithMemoryQueueSize(t *testing.T) {
	assert.Equal(t, defaultMemoryQueueSize, apply(WithMemoryQueueSize(0)).memoryQueueSize)
	assert.Equal(t, 50, apply(WithMemoryQueueSize(50)).memoryQueueSize)
}

func TestWithMemoryJobTimeout(t *testing.T) {
	assert.Equal(t, defaultMemoryJobTimeout, apply(WithMemoryJobTimeout(0)).memoryJobTimeout)
	assert.Equal(t, 3*time.Second, apply(WithMemoryJobTimeout(3*time.Second)).memoryJobTimeout)
}

func TestDefaultOptionsCloneIsValueCopy(t *testing.T) {
	a := defaultOptions.clone()
	b := defaultOptions.clone()
	a.apiKey = "mutated"
	assert.Equal(t, "mutated", a.apiKey)
	assert.Empty(t, b.apiKey, "clone should produce an independent copy")
}
