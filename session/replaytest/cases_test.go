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

func TestStandardReplayCasesCoverRequiredScenarios(t *testing.T) {
	cases := StandardReplayCases()
	if len(cases) != 10 {
		t.Fatalf("len(StandardReplayCases()) = %d, want 10", len(cases))
	}
	wantNames := map[string]bool{
		"single-turn":             false,
		"multi-turn":              false,
		"tool-call":               false,
		"state-update":            false,
		"memory-read-write":       false,
		"summary-update":          false,
		"summary-truncation":      false,
		"track-events":            false,
		"concurrent-out-of-order": false,
		"failure-retry":           false,
	}
	for _, replayCase := range cases {
		if _, ok := wantNames[replayCase.Name]; !ok {
			t.Fatalf("unexpected or duplicate case %q", replayCase.Name)
		}
		wantNames[replayCase.Name] = true
		if replayCase.Description == "" || len(replayCase.Operations) == 0 {
			t.Fatalf("case %q is incomplete: %#v", replayCase.Name, replayCase)
		}
		if len(replayCase.Invariants) == 0 {
			t.Errorf("case %q lacks a semantic snapshot invariant", replayCase.Name)
		}
		for i, operation := range replayCase.Operations {
			if err := operation.Validate(); err != nil {
				t.Errorf("case %q operation %d is invalid: %v", replayCase.Name, i, err)
			}
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("case %q is missing", name)
		}
	}
}

func TestStandardReplayCasesContainBehavioralOperations(t *testing.T) {
	cases := StandardReplayCases()
	assertMemoryCaseCoverage(t, replayCaseByName(t, cases, "memory-read-write"))
	assertRecoveryCaseCoverage(t, replayCaseByName(t, cases, "failure-retry"))
	assertSummaryRetryCoverage(t, replayCaseByName(t, cases, "summary-update"))
	assertToolCaseCoverage(t, replayCaseByName(t, cases, "tool-call"))
	assertConcurrentCaseCoverage(t, replayCaseByName(t, cases, "concurrent-out-of-order"))
}

func assertMemoryCaseCoverage(t *testing.T, memory ReplayCase) {
	t.Helper()
	if countOperations(memory.Operations, OperationWriteMemory) < 4 ||
		countOperations(memory.Operations, OperationSearchMemory) != 4 {
		t.Fatalf("memory case operations = %#v", memory.Operations)
	}
	if memory.Operations[3].Memory.UserID == memory.Operations[0].Memory.UserID {
		t.Fatal("memory case lacks an isolated logical scope")
	}
	memoryMetadata := memory.Operations[0].Memory.Metadata
	for _, key := range []string{"kind", "event_time", "participants", "location"} {
		if _, ok := memoryMetadata[key]; !ok {
			t.Errorf("memory metadata %q is not covered", key)
		}
	}
}

func assertRecoveryCaseCoverage(t *testing.T, recovery ReplayCase) {
	t.Helper()
	if len(recovery.Invariants) != 1 {
		t.Fatalf("recovery invariants = %#v", recovery.Invariants)
	}
	wantFailures := map[OperationKind]bool{
		OperationAppendEvent: false,
		OperationUpdateState: false,
		OperationWriteMemory: false,
	}
	for _, operation := range recovery.Operations {
		if operation.ExpectFailure {
			wantFailures[operation.Kind] = true
			if operation.Kind == OperationWriteMemory && operation.FailurePoint != FailureAfterWrite {
				t.Errorf("memory recovery failure point = %q, want %q", operation.FailurePoint, FailureAfterWrite)
			}
		}
	}
	for kind, found := range wantFailures {
		if !found {
			t.Errorf("recovery case lacks injected %s failure", kind)
		}
	}
}

func assertSummaryRetryCoverage(t *testing.T, summary ReplayCase) {
	t.Helper()
	for _, operation := range summary.Operations {
		if operation.Kind == OperationUpdateSummary && operation.ExpectFailure {
			return
		}
	}
	t.Fatal("summary case lacks injected update_summary failure")
}

func assertToolCaseCoverage(t *testing.T, toolCall ReplayCase) {
	t.Helper()
	for _, operation := range toolCall.Operations {
		if operation.Event != nil && operation.Event.ToolResponse != nil {
			if len(operation.Event.ToolResponse.Extra) == 0 {
				t.Fatal("tool response extra is not covered")
			}
			return
		}
	}
	t.Fatal("tool response is not covered")
}

func assertConcurrentCaseCoverage(t *testing.T, concurrent ReplayCase) {
	t.Helper()
	if len(concurrent.Operations) != 5 || concurrent.Operations[4].Kind != OperationParallel {
		t.Fatalf("concurrent case operations = %#v", concurrent.Operations)
	}
	children := concurrent.Operations[4].Parallel
	if len(children) != 3 || children[0].Name != "primary-first" || len(children[0].After) != 0 ||
		children[1].Name != "secondary" || len(children[1].After) != 0 ||
		children[2].Name != "primary-second" || len(children[2].After) != 1 ||
		children[2].After[0] != "primary-first" {
		t.Fatalf("concurrent dependencies = %#v", children)
	}
	if children[0].SessionID == children[1].SessionID {
		t.Fatal("independent concurrent writes must use distinct sessions")
	}
}

func TestStandardReplayCasesReturnIndependentValues(t *testing.T) {
	first := StandardReplayCases()
	first[0].Operations[0].SessionID = "mutated"
	second := StandardReplayCases()
	if second[0].Operations[0].SessionID != standardSessionID {
		t.Fatalf("StandardReplayCases() reused mutable state: %#v", second[0])
	}
}

func TestRecoverySnapshotInvariant(t *testing.T) {
	valid := Snapshot{
		Sessions: []SessionSnapshot{{
			State:  map[string]StateValueSnapshot{"status": JSONStateValue("recovered")},
			Events: []EventSnapshot{{Content: "start"}, {Content: "retried"}},
		}},
		Memories: []MemorySnapshot{{}},
	}
	tests := []struct {
		name    string
		mutate  func(*Snapshot)
		wantErr string
	}{
		{name: "valid"},
		{name: "session count", mutate: func(snapshot *Snapshot) {
			snapshot.Sessions = nil
		}, wantErr: "session count"},
		{name: "event count", mutate: func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events = nil
		}, wantErr: "event count"},
		{name: "state", mutate: func(snapshot *Snapshot) {
			snapshot.Sessions[0].State["status"] = JSONStateValue("dirty")
		}, wantErr: "state status"},
		{name: "memory count", mutate: func(snapshot *Snapshot) {
			snapshot.Memories = nil
		}, wantErr: "memory count"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := cloneSnapshot(valid)
			if test.mutate != nil {
				test.mutate(&snapshot)
			}
			err := validateRecoverySnapshot(snapshot)
			if test.wantErr == "" && err != nil {
				t.Fatalf("validateRecoverySnapshot() error = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("validateRecoverySnapshot() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestConcurrentSnapshotInvariant(t *testing.T) {
	valid := Snapshot{Sessions: []SessionSnapshot{
		{ID: standardSessionID, Events: []EventSnapshot{
			{Content: "primary request"}, {Content: "first"}, {Content: "second"},
		}},
		{ID: "session-2", Events: []EventSnapshot{
			{Content: "secondary request"}, {Content: "parallel"},
		}},
	}}
	if err := validateConcurrentSnapshot(valid); err != nil {
		t.Fatalf("validateConcurrentSnapshot() error = %v", err)
	}
	valid.Sessions[1].Events = nil
	if err := validateConcurrentSnapshot(valid); err == nil {
		t.Fatal("validateConcurrentSnapshot() error = nil for missing concurrent events")
	}
}

func TestReplayInvariantValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		check    func(Snapshot) error
		snapshot Snapshot
		want     string
	}{
		{
			name: "summary replay session count", check: validateSummaryReplayWindow,
			want: "session count",
		},
		{
			name: "summary replay events", check: validateSummaryReplayWindow,
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: standardSessionID}}},
			want:     "retained events",
		},
		{
			name: "event count", check: validateEvents(eventExpectation{content: "expected"}),
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: standardSessionID}}},
			want:     "event count",
		},
		{
			name: "tool event count", check: validateToolCall,
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: standardSessionID}}},
			want:     "event count",
		},
		{
			name: "state value", check: validateStateUpdate,
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: standardSessionID}}},
			want:     "state",
		},
		{
			name: "summary update session count", check: validateSummaryUpdate,
			want: "session count",
		},
		{
			name: "tracks missing", check: validateTracks,
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: standardSessionID}}},
			want:     "tracks",
		},
		{
			name: "concurrent unknown session", check: validateConcurrentSnapshot,
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: "unknown"}, {ID: "session-2"}}},
			want:     "unexpected session",
		},
		{
			name: "recovery event contents", check: validateRecoverySnapshot,
			snapshot: Snapshot{
				Sessions: []SessionSnapshot{{
					Events: []EventSnapshot{{Content: "wrong"}, {Content: "retried"}},
					State:  map[string]StateValueSnapshot{"status": JSONStateValue("recovered")},
				}},
				Memories: []MemorySnapshot{{}},
			},
			want: "event contents",
		},
		{
			name: "wrong only session id",
			check: func(snapshot Snapshot) error {
				_, err := onlySession(snapshot, standardSessionID)
				return err
			},
			snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: "wrong"}}},
			want:     "session id",
		},
		{
			name: "missing named session",
			check: func(snapshot Snapshot) error {
				_, err := findSessionSnapshot(snapshot, standardSessionID)
				return err
			},
			want: "not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.check(test.snapshot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invariant error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateSummaryRejectsInvalidPersistedFields(t *testing.T) {
	valid := SummarySnapshot{
		SessionID: standardSessionID,
		FilterKey: "branch/main",
		Text:      "summary",
		Version:   1,
		UpdatedAt: standardTime,
		Boundary: map[string]any{
			"filter_key": "branch/main", "last_event_id": "event-1", "cutoff_at": standardTime,
		},
	}
	tests := []struct {
		name   string
		mutate func(*SummarySnapshot)
		want   string
	}{
		{name: "main fields", mutate: func(summary *SummarySnapshot) {
			summary.Version = 0
		}, want: "summary ="},
		{name: "boundary filter", mutate: func(summary *SummarySnapshot) {
			summary.Boundary["filter_key"] = "wrong"
		}, want: "filter_key"},
		{name: "boundary event", mutate: func(summary *SummarySnapshot) {
			delete(summary.Boundary, "last_event_id")
		}, want: "last_event_id"},
		{name: "boundary cutoff", mutate: func(summary *SummarySnapshot) {
			delete(summary.Boundary, "cutoff_at")
		}, want: "cutoff_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := valid
			summary.Boundary = make(map[string]any, len(valid.Boundary))
			for key, value := range valid.Boundary {
				summary.Boundary[key] = value
			}
			test.mutate(&summary)
			err := validateSummary(summary, standardSessionID, "summary")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateSummary() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSameStringSetRejectsInvalidValues(t *testing.T) {
	if sameStringSet([]any{"one"}, "one", "two") {
		t.Fatal("sameStringSet() accepted mismatched lengths")
	}
	if sameStringSet([]any{1}, "one") {
		t.Fatal("sameStringSet() accepted a non-string value")
	}
	if sameStringSet([]any{"two"}, "one") {
		t.Fatal("sameStringSet() accepted an unexpected string")
	}
}

func replayCaseByName(t *testing.T, cases []ReplayCase, name string) ReplayCase {
	t.Helper()
	for _, replayCase := range cases {
		if replayCase.Name == name {
			return replayCase
		}
	}
	t.Fatalf("replay case %q not found", name)
	return ReplayCase{}
}

func countOperations(operations []Operation, kind OperationKind) int {
	count := 0
	for _, operation := range operations {
		if operation.Kind == kind {
			count++
		}
	}
	return count
}
