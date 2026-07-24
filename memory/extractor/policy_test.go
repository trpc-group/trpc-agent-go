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
	ext := NewExtractor(nil).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_ZeroValueUsesCompatible(t *testing.T) {
	var ext memoryExtractor
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_OptIn(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicyStrict),
	).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyStrict, ext.UpdatePolicy())
	assert.Equal(t, UpdatePolicyStrict, ext.Metadata()[metadataKeyUpdatePolicy])
}

func TestExtractorUpdatePolicy_InvalidValueUsesCompatible(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicy("invalid")),
	).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyCompatible, ext.UpdatePolicy())
}

func TestUpdatePolicyPromptBlock_IsOptIn(t *testing.T) {
	compatible := &memoryExtractor{updatePolicy: UpdatePolicyCompatible}
	assert.Empty(t, compatible.updatePolicyPromptBlock())

	strict := &memoryExtractor{updatePolicy: UpdatePolicyStrict}
	assert.Contains(t, strict.updatePolicyPromptBlock(), "Preserve long-term history")
	assert.Contains(t, strict.updatePolicyPromptBlock(), "Use memory_add for corrections")
	assert.Contains(t, strict.updatePolicyPromptBlock(), "explicitly asks")
	assert.Contains(t, strict.updatePolicyToolDescription(
		memory.DeleteToolName, "default",
	), "explicitly asks")

	addOnly := &memoryExtractor{updatePolicy: UpdatePolicyAddOnly}
	assert.Contains(t, addOnly.updatePolicyPromptBlock(), "Use only memory_add")
	assert.Equal(t, map[string]struct{}{
		memory.AddToolName: {},
	}, addOnly.updatePolicyEnabledTools())
}

func TestExtractorUpdatePolicy_ToolSurface(t *testing.T) {
	tests := []struct {
		name      string
		policy    UpdatePolicy
		toolNames []string
	}{
		{
			name:   "compatible exposes existing tools",
			policy: UpdatePolicyCompatible,
			toolNames: []string{
				memory.AddToolName,
				memory.UpdateToolName,
				memory.DeleteToolName,
				memory.ClearToolName,
			},
		},
		{
			name:   "strict exposes guarded destructive tools",
			policy: UpdatePolicyStrict,
			toolNames: []string{
				memory.AddToolName,
				memory.UpdateToolName,
				memory.DeleteToolName,
				memory.ClearToolName,
			},
		},
		{
			name:      "add-only exposes add",
			policy:    UpdatePolicyAddOnly,
			toolNames: []string{memory.AddToolName},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := &mockModel{name: "test-model"}
			ext := NewExtractor(m, WithUpdatePolicy(test.policy))
			_, err := ext.Extract(
				context.Background(),
				[]model.Message{model.NewUserMessage("Remember this.")},
				nil,
			)
			assert.NoError(t, err)
			if !assert.NotNil(t, m.lastRequest) {
				return
			}
			assert.Len(t, m.lastRequest.Tools, len(test.toolNames))
			for _, name := range test.toolNames {
				assert.Contains(t, m.lastRequest.Tools, name)
			}
		})
	}
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
		ext := &memoryExtractor{updatePolicy: policy}
		for _, call := range calls {
			assert.Nil(t, ext.parseToolCall(context.Background(), call))
		}
	}
}
