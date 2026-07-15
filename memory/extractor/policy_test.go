//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractorUpdatePolicy_DefaultsToLegacy(t *testing.T) {
	ext := NewExtractor(nil)
	updateProvider, ok := ext.(UpdatePolicyProvider)
	require.True(t, ok)
	assert.Equal(t, UpdatePolicyLegacy, updateProvider.UpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_OptIn(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicyConservative),
	)
	assert.Equal(t, UpdatePolicyConservative, ext.(UpdatePolicyProvider).UpdatePolicy())
	assert.Equal(t, UpdatePolicyConservative, ext.Metadata()[metadataKeyUpdatePolicy])
}

func TestExtractorUpdatePolicy_InvalidValueUsesLegacy(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicy("invalid")),
	)
	assert.Equal(t, UpdatePolicyLegacy, ext.(UpdatePolicyProvider).UpdatePolicy())
}

func TestUpdatePolicyPromptBlock_IsOptIn(t *testing.T) {
	legacy := &memoryExtractor{}
	assert.Empty(t, legacy.updatePolicyPromptBlock())

	conservative := &memoryExtractor{updatePolicy: UpdatePolicyConservative}
	assert.Contains(t, conservative.updatePolicyPromptBlock(), "Preserve long-term history")
	assert.Contains(t, conservative.updatePolicyPromptBlock(), "Use memory_add for corrections")

	disabled := &memoryExtractor{updatePolicy: UpdatePolicyDisabled}
	assert.Contains(t, disabled.updatePolicyPromptBlock(), "Do not use memory_update")
}

func TestExtractorPolicies_InvalidToolCallsRemainNonFatal(t *testing.T) {
	policies := []UpdatePolicy{
		UpdatePolicyLegacy,
		UpdatePolicyConservative,
		UpdatePolicyDisabled,
	}
	calls := []model.ToolCall{
		makeToolCall(memory.AddToolName, []byte(`{`)),
		makeToolCall(memory.AddToolName, []byte(`{"topics":["missing memory"]}`)),
	}

	for _, policy := range policies {
		ext := &memoryExtractor{updatePolicy: policy}
		for _, call := range calls {
			assert.Nil(t, ext.parseToolCall(context.Background(), call))
		}
	}
}
