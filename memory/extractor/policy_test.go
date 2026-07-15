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

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractorUpdatePolicy_DefaultsToCompatible(t *testing.T) {
	ext := NewExtractor(nil)
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_ZeroValueUsesCompatible(t *testing.T) {
	var ext Extractor
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_OptIn(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicyStrict),
	)
	assert.Equal(t, UpdatePolicyStrict, ext.UpdatePolicy())
	assert.Equal(t, UpdatePolicyStrict, ext.Metadata()[metadataKeyUpdatePolicy])
}

func TestExtractorUpdatePolicy_InvalidValueUsesCompatible(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicy("invalid")),
	)
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
}

func TestUpdatePolicyPromptBlock_IsOptIn(t *testing.T) {
	compatible := &Extractor{updatePolicy: UpdatePolicyCompatible}
	assert.Empty(t, compatible.updatePolicyPromptBlock())

	strict := &Extractor{updatePolicy: UpdatePolicyStrict}
	assert.Contains(t, strict.updatePolicyPromptBlock(), "Preserve long-term history")
	assert.Contains(t, strict.updatePolicyPromptBlock(), "Use memory_add for corrections")

	addOnly := &Extractor{updatePolicy: UpdatePolicyAddOnly}
	assert.Contains(t, addOnly.updatePolicyPromptBlock(), "Do not use memory_update")
}

func TestExtractorPolicies_InvalidToolCallsRemainNonFatal(t *testing.T) {
	policies := []UpdatePolicy{
		UpdatePolicyCompatible,
		UpdatePolicyStrict,
		UpdatePolicyAddOnly,
	}
	calls := []model.ToolCall{
		makeToolCall(memory.AddToolName, []byte(`{`)),
		makeToolCall(memory.AddToolName, []byte(`{"topics":["missing memory"]}`)),
	}

	for _, policy := range policies {
		ext := &Extractor{updatePolicy: policy}
		for _, call := range calls {
			assert.Nil(t, ext.parseToolCall(context.Background(), call))
		}
	}
}
