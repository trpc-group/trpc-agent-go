//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reader

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegistry_RegisterAndExtensions(t *testing.T) {
	ClearRegistry()
	RegisterReader([]string{".FOO"}, func() Reader { return nil })

	// Internal map should contain normalized extension key
	globalRegistry.mu.RLock()
	_, okLower := globalRegistry.readers[".foo"]
	_, okUpper := globalRegistry.readers[".FOO"]
	globalRegistry.mu.RUnlock()
	assert.True(t, okLower)
	assert.False(t, okUpper)

	// Registered extensions should include .foo
	exts := GetRegisteredExtensions()
	found := false
	for _, e := range exts {
		if e == ".foo" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestRegistry_ExtensionToType(t *testing.T) {
	// Verify mapping for several known types
	assert.Equal(t, "text", extensionToType(".txt"))
	assert.Equal(t, "markdown", extensionToType(".md"))
	assert.Equal(t, "json", extensionToType(".json"))
	assert.Equal(t, "csv", extensionToType(".csv"))
	assert.Equal(t, "pdf", extensionToType(".pdf"))
	assert.Equal(t, "docx", extensionToType(".docx"))
	assert.Equal(t, "foo", extensionToType(".foo"))
}
