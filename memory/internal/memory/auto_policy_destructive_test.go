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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExplicitDestructiveRequest(t *testing.T) {
	tests := []struct {
		name        string
		messages    []model.Message
		allowDelete bool
		allowClear  bool
	}{
		{
			name:        "explicit delete",
			messages:    []model.Message{model.NewUserMessage("Please forget my coffee preference.")},
			allowDelete: true,
		},
		{
			name:        "explicit clear",
			messages:    []model.Message{model.NewUserMessage("Could you please clear all my memories?")},
			allowDelete: true,
			allowClear:  true,
		},
		{
			name:        "specific delete cannot authorize clear",
			messages:    []model.Message{model.NewUserMessage("Delete my coffee preference.")},
			allowDelete: true,
		},
		{
			name:     "negated request",
			messages: []model.Message{model.NewUserMessage("Please do not delete my coffee preference.")},
		},
		{
			name:     "assistant request is ignored",
			messages: []model.Message{model.NewAssistantMessage("Please forget everything about the user.")},
		},
		{
			name:        "explicit chinese delete",
			messages:    []model.Message{model.NewUserMessage("请删除我的咖啡偏好。")},
			allowDelete: true,
		},
		{
			name:        "explicit chinese clear",
			messages:    []model.Message{model.NewUserMessage("请清空所有记忆。")},
			allowDelete: true,
			allowClear:  true,
		},
		{
			name:     "negated chinese request",
			messages: []model.Message{model.NewUserMessage("请不要删除我的咖啡偏好。")},
		},
		{
			name: "latest negation wins",
			messages: []model.Message{
				model.NewUserMessage("Please clear all my memories."),
				model.NewUserMessage("Do not clear my memories."),
			},
		},
		{
			name: "latest specific request narrows clear",
			messages: []model.Message{
				model.NewUserMessage("Please clear all my memories."),
				model.NewUserMessage("Actually, please delete only my coffee preference."),
			},
			allowDelete: true,
		},
		{
			name:     "partial clear does not authorize clear",
			messages: []model.Message{model.NewUserMessage("Clear everything except my coffee preference.")},
		},
		{
			name:     "partial chinese clear does not authorize clear",
			messages: []model.Message{model.NewUserMessage("请清空除了咖啡偏好以外的所有记忆。")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := latestExplicitDestructiveRequest(test.messages)
			assert.Equal(t, test.allowDelete, request.explicit && !request.partial)
			assert.Equal(t, test.allowClear, request.clearAll && !request.partial)
		})
	}
}

func TestPreserveHistoryPolicy_DeleteAuthorizationIsTargetBound(t *testing.T) {
	existing := []*memory.Entry{
		{
			ID: "coffee",
			Memory: &memory.Memory{
				Memory: "User prefers dark roast coffee.",
				Topics: []string{"coffee", "preference"},
			},
		},
		{
			ID: "address",
			Memory: &memory.Memory{
				Memory: "User lives at 100 Main Street.",
				Topics: []string{"home", "address"},
			},
		},
	}
	ops := []*extractor.Operation{
		{Type: extractor.OperationDelete, MemoryID: "coffee"},
		{Type: extractor.OperationDelete, MemoryID: "address"},
		{Type: extractor.OperationDelete, MemoryID: "missing"},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), ops, existing,
		[]model.Message{model.NewUserMessage("Please forget my coffee preference.")},
	)
	require.Len(t, out, 1)
	assert.Equal(t, "coffee", out[0].MemoryID)
}

func TestPreserveHistoryPolicy_DeleteAuthorizationRejectsPartialTargetMatch(t *testing.T) {
	existing := []*memory.Entry{
		{
			ID: "address",
			Memory: &memory.Memory{
				Memory: "User has a home address in Seattle.",
				Topics: []string{"home", "address"},
			},
		},
		{
			ID: "office",
			Memory: &memory.Memory{
				Memory: "User converted a bedroom into a home office.",
				Topics: []string{"home", "office"},
			},
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{
			{Type: extractor.OperationDelete, MemoryID: "address"},
			{Type: extractor.OperationDelete, MemoryID: "office"},
		},
		existing,
		[]model.Message{model.NewUserMessage("Please forget my home address.")},
	)
	require.Len(t, out, 1)
	assert.Equal(t, "address", out[0].MemoryID)
}

func TestPreserveHistoryPolicy_DeleteAuthorizationSupportsChineseTarget(t *testing.T) {
	existing := []*memory.Entry{
		{
			ID: "coffee",
			Memory: &memory.Memory{
				Memory: "用户喜欢深烘焙咖啡。",
				Topics: []string{"咖啡", "偏好"},
			},
		},
		{
			ID: "address",
			Memory: &memory.Memory{
				Memory: "用户住在深圳。",
				Topics: []string{"住址"},
			},
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{
			{Type: extractor.OperationDelete, MemoryID: "coffee"},
			{Type: extractor.OperationDelete, MemoryID: "address"},
		},
		existing,
		[]model.Message{model.NewUserMessage("请删除我的咖啡偏好。")},
	)
	require.Len(t, out, 1)
	assert.Equal(t, "coffee", out[0].MemoryID)
}

func TestPreserveHistoryPolicy_ScopedEverythingDoesNotClearAll(t *testing.T) {
	existing := []*memory.Entry{
		{
			ID: "employer",
			Memory: &memory.Memory{
				Memory: "User's former employer was Acme.",
				Topics: []string{"former employer", "Acme"},
			},
		},
		{
			ID: "hobby",
			Memory: &memory.Memory{
				Memory: "User enjoys hiking.",
				Topics: []string{"hiking"},
			},
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{
			{Type: extractor.OperationDelete, MemoryID: "employer"},
			{Type: extractor.OperationDelete, MemoryID: "hobby"},
			{Type: extractor.OperationClear},
		}, existing, []model.Message{
			model.NewUserMessage("Please forget everything about my former employer."),
		},
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationDelete, out[0].Type)
	assert.Equal(t, "employer", out[0].MemoryID)
}

func TestPreserveHistoryPolicy_ClearAllAuthorizesDeletes(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "coffee",
		Memory: &memory.Memory{
			Memory: "User prefers coffee.",
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{{Type: extractor.OperationDelete, MemoryID: "coffee"}},
		existing,
		[]model.Message{model.NewUserMessage("Please clear all my memories.")},
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationDelete, out[0].Type)
}

func TestPreserveHistoryPolicy_QualifiedClearAllStaysScoped(t *testing.T) {
	existing := []*memory.Entry{
		{
			ID: "coffee",
			Memory: &memory.Memory{
				Memory: "User prefers coffee.",
				Topics: []string{"coffee", "咖啡"},
			},
		},
		{
			ID: "address",
			Memory: &memory.Memory{
				Memory: "User lives in Shenzhen.",
				Topics: []string{"address", "住址"},
			},
		},
	}
	operations := []*extractor.Operation{
		{Type: extractor.OperationDelete, MemoryID: "coffee"},
		{Type: extractor.OperationDelete, MemoryID: "address"},
		{Type: extractor.OperationClear},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	for _, request := range []string{
		"Please forget everything I told you about coffee.",
		"请删除关于咖啡的所有记忆。",
	} {
		t.Run(request, func(t *testing.T) {
			out := worker.reconcilePreserveHistoryOps(
				context.Background(),
				reconcileUserKey(),
				operations,
				existing,
				[]model.Message{model.NewUserMessage(request)},
			)

			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationDelete, out[0].Type)
			assert.Equal(t, "coffee", out[0].MemoryID)
		})
	}
}
