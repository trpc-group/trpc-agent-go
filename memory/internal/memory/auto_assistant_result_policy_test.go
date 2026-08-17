//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestAssistantResultPolicy_DropsExactDuplicate(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	existing := []*memory.Entry{assistantResultPolicyEntry(
		"result-1", "Assistant result: Recommended Alpha and Beta.",
	)}

	out := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{
			Type:   extractor.OperationAdd,
			Memory: "Assistant result: Recommended Alpha and Beta.",
		}},
		existing,
	)

	assert.Empty(t, out)
}

func TestAssistantResultPolicy_UpdatesOnlyLosslessEnrichment(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	existing := []*memory.Entry{assistantResultPolicyEntry(
		"result-1", "Assistant result: Alpha supports offline mode.",
	)}

	out := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{
			Type:   extractor.OperationAdd,
			Memory: "Assistant result: Alpha supports offline mode reliably.",
		}},
		existing,
	)

	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "result-1", out[0].MemoryID)
}

func TestAssistantResultPolicy_PreservesDistinctRecommendation(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	existing := []*memory.Entry{assistantResultPolicyEntry(
		"result-1", "Assistant result: Recommended Alpha, Beta, and Gamma.",
	)}

	out := worker.applyAssistantResultPolicy(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{
			Type:   extractor.OperationAdd,
			Memory: "Assistant result: Recommended Alpha, Beta, and Delta.",
		}},
		existing,
	)

	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func assistantResultPolicyEntry(id, text string) *memory.Entry {
	return &memory.Entry{
		ID: id,
		Memory: &memory.Memory{
			Memory: text,
		},
	}
}
