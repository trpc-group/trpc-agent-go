//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import "testing"

func TestOperationValidateRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
	}{
		{name: "empty", operation: Operation{}},
		{name: "missing event", operation: Operation{Kind: OperationAppendEvent, SessionID: "session"}},
		{
			name: "unrelated payload",
			operation: Operation{
				Kind:      OperationCreateSession,
				SessionID: "session",
				Memory:    &MemorySnapshot{},
			},
		},
		{
			name: "missing replay window filter key",
			operation: Operation{
				Kind:      OperationSetReplayWindow,
				SessionID: "session",
			},
		},
		{
			name: "invalid search limit",
			operation: Operation{
				Kind:        OperationSearchMemory,
				SearchQuery: "query",
			},
		},
		{
			name: "summary ownership mismatch",
			operation: Operation{
				Kind:      OperationUpdateSummary,
				SessionID: "session-1",
				Summary:   &SummarySnapshot{SessionID: "session-2"},
			},
		},
		{
			name: "summary generated fields used as input",
			operation: Operation{
				Kind:      OperationUpdateSummary,
				SessionID: "session-1",
				Summary: &SummarySnapshot{
					SessionID: "session-1",
					Version:   1,
				},
			},
		},
		{
			name: "unexpected injected failure",
			operation: Operation{
				Kind:            OperationWriteMemory,
				Memory:          &MemorySnapshot{},
				InjectedFailure: "failure",
				ExpectFailure:   false,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.operation.Validate(); err == nil {
				t.Fatalf("Operation.Validate() error = nil for %#v", test.operation)
			}
		})
	}
}

func TestParallelDependenciesRejectInvalidGraphs(t *testing.T) {
	tests := []struct {
		name       string
		operations []Operation
	}{
		{
			name: "duplicate name",
			operations: []Operation{
				namedOperation(appendEvent("1", "user", "one", 1), "same"),
				namedOperation(appendEvent("2", "user", "two", 2), "same"),
			},
		},
		{
			name: "unknown dependency",
			operations: []Operation{
				namedOperation(appendEvent("1", "user", "one", 1), "one", "missing"),
			},
		},
		{
			name: "cycle",
			operations: []Operation{
				namedOperation(appendEvent("1", "user", "one", 1), "one", "two"),
				namedOperation(appendEvent("2", "user", "two", 2), "two", "one"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parallelDependencies(test.operations); err == nil {
				t.Fatal("parallelDependencies() error = nil")
			}
		})
	}
}
