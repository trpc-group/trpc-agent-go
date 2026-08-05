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

func TestExtractorUpdatePolicy_DefaultsToMergeSimilar(t *testing.T) {
	ext := NewExtractor(nil).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyMergeSimilar, ext.configuredUpdatePolicy())
	assert.Equal(t, UpdatePolicyMergeSimilar, ConfiguredUpdatePolicy(ext))
	assert.Equal(t, UpdatePolicyMergeSimilar, ConfiguredUpdatePolicy(nil))
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_ZeroValueUsesMergeSimilar(t *testing.T) {
	var ext memoryExtractor
	assert.Equal(t, UpdatePolicyMergeSimilar, ext.configuredUpdatePolicy())
	assert.NotContains(t, ext.Metadata(), metadataKeyUpdatePolicy)
}

func TestExtractorUpdatePolicy_OptIn(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicyPreserveHistory),
	).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyPreserveHistory, ext.configuredUpdatePolicy())
	assert.Equal(t, UpdatePolicyPreserveHistory, ext.UpdatePolicy())
	assert.Equal(t, UpdatePolicyPreserveHistory, ConfiguredUpdatePolicy(ext))
	assert.Equal(t, UpdatePolicyPreserveHistory, ext.Metadata()[metadataKeyUpdatePolicy])
}

func TestExtractorUpdatePolicy_InvalidValueUsesMergeSimilar(t *testing.T) {
	ext := NewExtractor(
		nil,
		WithUpdatePolicy(UpdatePolicy("invalid")),
	).(*memoryExtractor)
	assert.Equal(t, UpdatePolicyMergeSimilar, ext.configuredUpdatePolicy())
}

func TestUpdatePolicyPromptBlock_IsOptIn(t *testing.T) {
	mergeSimilar := &memoryExtractor{updatePolicy: UpdatePolicyMergeSimilar}
	assert.Empty(t, mergeSimilar.updatePolicyPromptBlock())

	preserveHistory := &memoryExtractor{updatePolicy: UpdatePolicyPreserveHistory}
	assert.Contains(t, preserveHistory.updatePolicyPromptBlock(), "Preserve long-term history")
	assert.Contains(t, preserveHistory.updatePolicyPromptBlock(), "Use memory_add for corrections")
	assert.Contains(t, preserveHistory.updatePolicyPromptBlock(), "explicitly asks")
	assert.Contains(t, preserveHistory.updatePolicyToolDescription(
		memory.DeleteToolName, "default",
	), "explicitly asks")

	appendOnly := &memoryExtractor{updatePolicy: UpdatePolicyAppendOnly}
	assert.Contains(t, appendOnly.updatePolicyPromptBlock(), "Use only memory_add")
	assert.Equal(t, map[string]struct{}{
		memory.AddToolName: {},
	}, appendOnly.updatePolicyEnabledTools())
}

func TestExtractorUpdatePolicy_ToolSurface(t *testing.T) {
	tests := []struct {
		name      string
		policy    UpdatePolicy
		toolNames []string
	}{
		{
			name:   "mergeSimilar exposes existing tools",
			policy: UpdatePolicyMergeSimilar,
			toolNames: []string{
				memory.AddToolName,
				memory.UpdateToolName,
				memory.DeleteToolName,
				memory.ClearToolName,
			},
		},
		{
			name:   "preserveHistory exposes guarded destructive tools",
			policy: UpdatePolicyPreserveHistory,
			toolNames: []string{
				memory.AddToolName,
				memory.UpdateToolName,
				memory.DeleteToolName,
				memory.ClearToolName,
			},
		},
		{
			name:      "append-only exposes add",
			policy:    UpdatePolicyAppendOnly,
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
		UpdatePolicyMergeSimilar,
		UpdatePolicyPreserveHistory,
		UpdatePolicyAppendOnly,
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
