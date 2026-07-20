//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"strings"
	"testing"
)

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

func TestOperationValidateRejectsInvalidConfigurations(t *testing.T) {
	validChild := appendEvent("event", "user", "content", 1)
	tests := []struct {
		name      string
		operation Operation
		want      string
	}{
		{
			name:      "unsupported kind",
			operation: Operation{Kind: "unknown"},
			want:      "unsupported operation kind",
		},
		{
			name: "expected failure without injection",
			operation: Operation{
				Kind: OperationCreateSession, SessionID: "session", ExpectFailure: true,
			},
			want: "expected failure requires injected failure",
		},
		{
			name: "invalid failure point",
			operation: Operation{
				Kind: OperationCreateSession, SessionID: "session",
				InjectedFailure: "failure", ExpectFailure: true, FailurePoint: "invalid",
			},
			want: "valid failure point",
		},
		{
			name: "failure point without injection",
			operation: Operation{
				Kind: OperationCreateSession, SessionID: "session", FailurePoint: FailureBeforeWrite,
			},
			want: "failure point requires injected failure",
		},
		{
			name: "children on non-parallel operation",
			operation: Operation{
				Kind: OperationCreateSession, SessionID: "session", Parallel: []Operation{validChild},
			},
			want: "cannot contain parallel operations",
		},
		{
			name: "failure on parallel parent",
			operation: Operation{
				Kind: OperationParallel, Parallel: []Operation{validChild},
				InjectedFailure: "failure", ExpectFailure: true, FailurePoint: FailureBeforeWrite,
			},
			want: "parallel failure must be injected on a child",
		},
		{
			name:      "create session without id",
			operation: Operation{Kind: OperationCreateSession},
			want:      "create session requires session id",
		},
		{
			name:      "update state without changes",
			operation: Operation{Kind: OperationUpdateState, SessionID: "session"},
			want:      "update state requires session id and state changes",
		},
		{
			name:      "write memory without memory",
			operation: Operation{Kind: OperationWriteMemory},
			want:      "write memory requires memory",
		},
		{
			name: "search score above one",
			operation: Operation{
				Kind: OperationSearchMemory, SearchQuery: "query", SearchLimit: 1,
				SearchAppName: "app", SearchUserID: "user", SearchMinScore: 1.1,
			},
			want: "score threshold",
		},
		{
			name:      "update summary without summary",
			operation: Operation{Kind: OperationUpdateSummary, SessionID: "session"},
			want:      "update summary requires session id and summary",
		},
		{
			name:      "append track without event",
			operation: Operation{Kind: OperationAppendTrack, SessionID: "session", TrackName: "tool"},
			want:      "append track requires session id, track name, and event",
		},
		{
			name:      "parallel without children",
			operation: Operation{Kind: OperationParallel},
			want:      "parallel operation requires child operations",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.operation.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Operation.Validate() error = %v, want %q", err, test.want)
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
		{
			name:       "invalid child operation",
			operations: []Operation{{Kind: OperationCreateSession}},
		},
		{
			name: "unnamed dependency",
			operations: []Operation{{
				Kind: OperationCreateSession, SessionID: "session", After: []string{"other"},
			}},
		},
		{
			name: "self dependency",
			operations: []Operation{{
				Kind: OperationCreateSession, SessionID: "session", Name: "self", After: []string{"self"},
			}},
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
