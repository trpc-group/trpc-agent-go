//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

func TestRegisterModelContextWindow(t *testing.T) {
	// Save original GPT-4 size
	originalGPT4 := imodel.ResolveContextWindow("gpt-4")

	// Register a custom model
	RegisterModelContextWindow("test-model", 50000)

	// Verify it was registered
	result := imodel.ResolveContextWindow("test-model")
	assert.Equal(t, 50000, result, "Custom model should be registered with correct size")

	// Restore original GPT-4 size
	RegisterModelContextWindow("gpt-4", originalGPT4)
}

func TestRegisterModelContextWindows(t *testing.T) {
	// Save original sizes for models we might override
	originalGPT4 := imodel.ResolveContextWindow("gpt-4")
	originalGPT4o := imodel.ResolveContextWindow("gpt-4o")

	// Register multiple models at once
	models := map[string]int{
		"test-model-1": 10000,
		"test-model-2": 20000,
		"test-model-3": 30000,
		"gpt-4":        99999, // Override existing
	}
	RegisterModelContextWindows(models)

	// Verify all were registered
	for model, expectedSize := range models {
		result := imodel.ResolveContextWindow(model)
		assert.Equal(t, expectedSize, result, "Model %s should be registered with correct size", model)
	}

	// Restore original sizes
	RegisterModelContextWindow("gpt-4", originalGPT4)
	RegisterModelContextWindow("gpt-4o", originalGPT4o)
}

func TestRegistryOverridesExisting(t *testing.T) {
	// Save original GPT-4 size
	originalGPT4 := imodel.ResolveContextWindow("gpt-4")

	// Test that registration overrides existing models
	RegisterModelContextWindow("gpt-4", 99999) // Override existing GPT-4

	result := imodel.ResolveContextWindow("gpt-4")
	assert.Equal(t, 99999, result, "Registration should override existing model size")

	// Restore original GPT-4 size
	RegisterModelContextWindow("gpt-4", originalGPT4)
}

func TestConcurrentRegistryAccess(t *testing.T) {
	// Save original GPT-4 size for restoration
	originalGPT4 := imodel.ResolveContextWindow("gpt-4")

	// Test concurrent registration and resolution
	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func(id int) {
			defer func() { done <- true }()

			// Register a unique model
			modelName := fmt.Sprintf("concurrent-test-model-%d", id)
			RegisterModelContextWindow(modelName, 10000+id)

			// Verify it was registered
			result := imodel.ResolveContextWindow(modelName)
			assert.Equal(t, 10000+id, result, "Concurrent test %d should register model correctly", id)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	// Restore original GPT-4 size
	RegisterModelContextWindow("gpt-4", originalGPT4)
}
