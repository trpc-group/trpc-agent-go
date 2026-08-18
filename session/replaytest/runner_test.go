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
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRunnerComparesBackendsAndClosesFixtures(t *testing.T) {
	const expectedFixtureCloseCount = 1
	baseline := comparisonFixture()
	actual := comparisonFixture()
	actual.Sessions[0].Events[0].Content = "changed"
	baselineFixture := &fakeFixture{
		name:         "inmemory",
		capabilities: allCapabilities(),
		snapshot:     baseline,
	}
	actualFixture := &fakeFixture{
		name:         "sqlite",
		capabilities: allCapabilities(),
		snapshot:     actual,
	}
	runner := Runner{
		Backends: []Backend{
			fakeBackend("inmemory", baselineFixture),
			fakeBackend("sqlite", actualFixture),
		},
		NormalizeOptions: DefaultNormalizeOptions(),
		CompareOptions:   DefaultCompareOptions(),
	}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name:         "single",
		Capabilities: []Capability{CapabilitySession},
		Operations: []Operation{{
			Kind: OperationParallel,
			Parallel: []Operation{
				namedOperation(appendEvent("event-2", "assistant", "two", 2), "two", "one"),
				namedOperation(appendEvent("event-1", "user", "one", 1), "one"),
			},
		}},
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if report.Baseline != "inmemory" || len(report.Differences) != 1 {
		t.Fatalf("Runner.Run() report = %#v", report)
	}
	if !baselineFixture.isClosed() || !actualFixture.isClosed() {
		t.Fatal("Runner.Run() did not close all fixtures")
	}
	if baselineFixture.fixtureCloseCount() != expectedFixtureCloseCount ||
		actualFixture.fixtureCloseCount() != expectedFixtureCloseCount {
		t.Fatalf(
			"fixture close counts = baseline %d, actual %d",
			baselineFixture.fixtureCloseCount(), actualFixture.fixtureCloseCount(),
		)
	}
	if got := baselineFixture.operationCount(); got != 2 {
		t.Fatalf("baseline operation count = %d, want 2", got)
	}
	if got := baselineFixture.operationNames(); strings.Join(got, ",") != "one,two" {
		t.Fatalf("parallel completion order = %v, want [one two]", got)
	}
}

func TestRunnerMarksUnsupportedCapabilitiesAsAllowed(t *testing.T) {
	baselineFixture := &fakeFixture{
		name:         "inmemory",
		capabilities: allCapabilities(),
	}
	limitedFixture := &fakeFixture{
		name:         "limited",
		capabilities: CapabilitySet{CapabilitySession: true},
	}
	runner := Runner{Backends: []Backend{
		fakeBackend("inmemory", baselineFixture),
		fakeBackend("limited", limitedFixture),
	}, UnsupportedAllowances: []UnsupportedAllowance{{
		Backend: "limited", Case: "summary", Capability: CapabilitySummary,
		Reason: "limited test backend does not implement summaries",
	}}}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name:         "summary",
		Capabilities: []Capability{CapabilitySession, CapabilitySummary},
		Operations:   createSessionOperations(),
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 1 || !report.Differences[0].AllowedDiff {
		t.Fatalf("unsupported report = %#v", report)
	}
	if len(report.Cases) != 1 || report.Cases[0].Status != ResultInconclusive ||
		len(report.Cases[0].Backends) != 1 ||
		report.Cases[0].Backends[0].Status != ResultUnsupported {
		t.Fatalf("unsupported case result = %#v", report.Cases)
	}
	if report.Differences[0].Path != "$.unsupported.summary" ||
		report.Differences[0].Explanation !=
			"limited test backend does not implement summaries" {
		t.Fatalf("unsupported difference = %#v", report.Differences[0])
	}
	if got := limitedFixture.operationCount(); got != 0 {
		t.Fatalf("limited fixture executed %d operations", got)
	}
	if !limitedFixture.isClosed() {
		t.Fatal("unsupported fixture was not closed")
	}
}

func TestRunnerConsumesAllowedDiffRulesAcrossMatrix(t *testing.T) {
	baselineBackend := Backend{Name: "baseline", New: func(_ context.Context, _ string) (Fixture, error) {
		return &fakeFixture{
			name: "baseline", capabilities: allCapabilities(), snapshot: comparisonFixture(),
		}, nil
	}}
	matchingBackend := Backend{Name: "matching", New: func(_ context.Context, _ string) (Fixture, error) {
		return &fakeFixture{
			name: "matching", capabilities: allCapabilities(), snapshot: comparisonFixture(),
		}, nil
	}}
	differentBackend := Backend{Name: "different", New: func(_ context.Context, caseName string) (Fixture, error) {
		snapshot := comparisonFixture()
		if caseName == "second" {
			snapshot.Sessions[0].Events[0].Content = "changed"
		}
		return &fakeFixture{
			name: "different", capabilities: allCapabilities(), snapshot: snapshot,
		}, nil
	}}
	runner := Runner{
		Backends: []Backend{baselineBackend, matchingBackend, differentBackend},
		CompareOptions: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "second", Backend: "different",
			Path: "$.sessions[0].events[0].content", Explanation: "known difference",
		}}},
	}
	report, err := runner.Run(context.Background(), []ReplayCase{{Name: "first"}, {Name: "second"}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 1 || !report.Differences[0].AllowedDiff {
		t.Fatalf("Runner.Run() report = %#v", report)
	}
}

func TestRunnerPreservesExecutionErrorBeforeUnusedAllowedDiff(t *testing.T) {
	wantErr := errors.New("snapshot unavailable")
	runner := Runner{
		Backends: []Backend{
			fakeBackend("baseline", &fakeFixture{name: "baseline", capabilities: allCapabilities()}),
			fakeBackend("candidate", &fakeFixture{
				name: "candidate", capabilities: allCapabilities(), snapshotErr: wantErr,
			}),
		},
		CompareOptions: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "case", Backend: "candidate", Path: "$.sessions",
			Explanation: "unused because execution fails",
		}}},
	}
	_, err := runner.Run(context.Background(), []ReplayCase{{Name: "case"}})
	if !errors.Is(err, wantErr) || strings.Contains(err.Error(), "unused allowed diff") {
		t.Fatalf("Runner.Run() error = %v, want execution error", err)
	}
}

func TestRunnerRejectsInvalidOrUnusedAllowedDiffRules(t *testing.T) {
	validRule := AllowedDiffRule{
		Case: "case", Backend: "candidate", Path: "$.sessions[0].missing",
		Explanation: "stale rule",
	}
	baseline := func() Backend {
		return Backend{Name: "baseline", New: func(_ context.Context, _ string) (Fixture, error) {
			return &fakeFixture{name: "baseline", capabilities: allCapabilities()}, nil
		}}
	}
	candidate := func(capabilities CapabilitySet) Backend {
		return Backend{Name: "candidate", New: func(_ context.Context, _ string) (Fixture, error) {
			return &fakeFixture{name: "candidate", capabilities: capabilities}, nil
		}}
	}
	tests := []struct {
		name     string
		backends []Backend
		cases    []ReplayCase
		rule     AllowedDiffRule
		want     string
	}{
		{
			name: "invalid before execution", backends: []Backend{baseline()},
			cases: []ReplayCase{{Name: "case"}}, rule: AllowedDiffRule{Path: "$.sessions"},
			want: "requires case",
		},
		{
			name: "baseline only", backends: []Backend{baseline()},
			cases: []ReplayCase{{Name: "case"}}, rule: validRule,
			want: "unknown backend",
		},
		{
			name: "candidate unsupported", backends: []Backend{
				baseline(), candidate(CapabilitySet{}),
			},
			cases: []ReplayCase{{Name: "case", Capabilities: []Capability{CapabilitySession}}},
			rule:  validRule, want: "unused allowed diff rule",
		},
		{
			name: "matching scope without difference", backends: []Backend{
				baseline(), candidate(allCapabilities()),
			},
			cases: []ReplayCase{{Name: "case"}}, rule: validRule,
			want: "unused allowed diff rule",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := Runner{
				Backends:       test.backends,
				CompareOptions: CompareOptions{AllowedDiffRules: []AllowedDiffRule{test.rule}},
			}
			_, err := runner.Run(context.Background(), test.cases)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunnerValidatesAllowedDiffRulesBeforeCreatingFixtures(t *testing.T) {
	tests := []struct {
		name string
		rule AllowedDiffRule
		want string
	}{
		{
			name: "missing fields",
			rule: AllowedDiffRule{Path: "$.sessions"},
			want: "requires case",
		},
		{
			name: "unknown backend",
			rule: AllowedDiffRule{
				Case: "case", Backend: "unknown", Path: "$.sessions",
				Explanation: "known diff",
			},
			want: "unknown backend",
		},
		{
			name: "unknown case",
			rule: AllowedDiffRule{
				Case: "unknown", Backend: "candidate", Path: "$.sessions",
				Explanation: "known diff",
			},
			want: "unknown case",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newCalls := 0
			newBackend := func(name string) Backend {
				return Backend{Name: name, New: func(context.Context, string) (Fixture, error) {
					newCalls++
					return &fakeFixture{name: name, capabilities: allCapabilities()}, nil
				}}
			}
			runner := Runner{
				Backends: []Backend{newBackend("baseline"), newBackend("candidate")},
				CompareOptions: CompareOptions{
					AllowedDiffRules: []AllowedDiffRule{test.rule},
				},
			}
			_, err := runner.Run(context.Background(), []ReplayCase{{Name: "case"}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.want)
			}
			if newCalls != 0 {
				t.Fatalf("backend.New() calls = %d, want 0", newCalls)
			}
		})
	}
}

func TestRunnerRejectsDuplicateNamesBeforeCreatingFixtures(t *testing.T) {
	newCalls := 0
	newBackend := func(name string) Backend {
		return Backend{Name: name, New: func(context.Context, string) (Fixture, error) {
			newCalls++
			return &fakeFixture{name: name, capabilities: allCapabilities()}, nil
		}}
	}
	tests := []struct {
		name     string
		backends []Backend
		cases    []ReplayCase
		want     string
	}{
		{
			name: "backend",
			backends: []Backend{
				newBackend("duplicate"), newBackend("duplicate"),
			},
			cases: []ReplayCase{{Name: "case"}},
			want:  `backend name "duplicate" is duplicated`,
		},
		{
			name:     "case",
			backends: []Backend{newBackend("baseline")},
			cases: []ReplayCase{
				{Name: "duplicate"}, {Name: "duplicate"},
			},
			want: `case name "duplicate" is duplicated`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newCalls = 0
			_, err := (Runner{Backends: test.backends}).Run(context.Background(), test.cases)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.want)
			}
			if newCalls != 0 {
				t.Fatalf("backend.New() calls = %d, want 0", newCalls)
			}
		})
	}
}

func TestRunnerRejectsInvalidCasesBeforeCreatingFixtures(t *testing.T) {
	parallelCycle := Operation{Kind: OperationParallel, Parallel: []Operation{
		namedOperation(appendEvent("event-1", "user", "one", 1), "one", "two"),
		namedOperation(appendEvent("event-2", "assistant", "two", 2), "two", "one"),
	}}
	tests := []struct {
		name  string
		cases []ReplayCase
		want  string
	}{
		{
			name: "invalid operation",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationAppendEvent, SessionID: "session",
			}}}},
			want: "append event requires session id and event",
		},
		{
			name: "top-level dependency",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{
				namedOperation(appendEvent("event-1", "user", "content", 1), "write", "ready"),
			}}},
			want: "top-level dependencies",
		},
		{
			name: "invalid invariant",
			cases: []ReplayCase{{Name: "case", Invariants: []SnapshotInvariant{{
				Name: "missing check",
			}}}},
			want: "snapshot invariant 0 is invalid",
		},
		{
			name: "parallel unknown dependency",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationParallel,
				Parallel: []Operation{
					namedOperation(appendEvent("event-1", "user", "content", 1), "write", "ready"),
				},
			}}}},
			want: `unknown dependency "ready"`,
		},
		{
			name: "parallel duplicate dependency name",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationParallel,
				Parallel: []Operation{
					namedOperation(appendEvent("event-1", "user", "one", 1), "write"),
					namedOperation(appendEvent("event-2", "assistant", "two", 2), "write"),
				},
			}}}},
			want: `duplicate parallel operation name "write"`,
		},
		{
			name: "parallel self dependency",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationParallel,
				Parallel: []Operation{
					namedOperation(appendEvent("event-1", "user", "content", 1), "write", "write"),
				},
			}}}},
			want: `parallel operation "write" depends on itself`,
		},
		{
			name:  "parallel cycle",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{parallelCycle}}},
			want:  "dependency cycle",
		},
		{
			name: "parallel unordered state conflict",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationParallel,
				Parallel: []Operation{
					{
						Kind: OperationUpdateState, SessionID: "session",
						StateUpdates: map[string]any{"status": "ready"},
					},
					{
						Kind: OperationUpdateState, SessionID: "session",
						StateDeletes: []string{"status"},
					},
				},
			}}}},
			want: `parallel state operations for session "session" key "status" must be ordered`,
		},
		{
			name: "reserved state prefix",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationUpdateState, SessionID: "session",
				StateUpdates: map[string]any{"app:theme": "dark"},
			}}}},
			want: "reserved scope prefix",
		},
		{
			name: "overlapping state update and delete",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationUpdateState, SessionID: "session",
				StateUpdates: map[string]any{"theme": nil}, StateDeletes: []string{"theme"},
			}}}},
			want: "cannot be both updated and deleted",
		},
		{
			name: "conflicting tool response fields",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationAppendEvent, SessionID: "session",
				Event: &EventSnapshot{
					Role: "assistant", Content: "outer",
					ToolResponse: &ToolResponse{Content: "nested"},
				},
			}}}},
			want: "tool response conflicts with event role",
		},
		{
			name: "nested parallel invalid operation",
			cases: []ReplayCase{{Name: "case", Operations: []Operation{{
				Kind: OperationParallel,
				Parallel: []Operation{{Kind: OperationParallel, Parallel: []Operation{{
					Kind: OperationAppendEvent, SessionID: "session",
				}}}},
			}}}},
			want: "append event requires session id and event",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newCalls := 0
			runner := Runner{Backends: []Backend{{
				Name: "baseline",
				New: func(context.Context, string) (Fixture, error) {
					newCalls++
					return &fakeFixture{name: "baseline", capabilities: allCapabilities()}, nil
				},
			}}}
			_, err := runner.Run(context.Background(), test.cases)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.want)
			}
			if newCalls != 0 {
				t.Fatalf("backend.New() calls = %d, want 0", newCalls)
			}
		})
	}
}

func TestRunnerAllowsOrderedParallelStateMutations(t *testing.T) {
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "ordered-state",
		Operations: []Operation{{
			Kind: OperationParallel,
			Parallel: []Operation{
				namedOperation(Operation{
					Kind: OperationUpdateState, SessionID: "session",
					StateUpdates: map[string]any{"status": "ready"},
				}, "write"),
				namedOperation(Operation{
					Kind: OperationUpdateState, SessionID: "session",
					StateDeletes: []string{"status"},
				}, "delete", "write"),
			},
		}},
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("Runner.Run() differences = %#v", report.Differences)
	}
	if got := fixture.operationNames(); strings.Join(got, ",") != "write,delete" {
		t.Fatalf("parallel state operation order = %v, want [write delete]", got)
	}
}

func TestRunnerContinuesAfterExpectedFailure(t *testing.T) {
	const expectedAppliedOperations = 1
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "recovery",
		Operations: []Operation{
			injectFailure(appendEvent("event-1", "user", "first", 1), "injected"),
			appendEvent("event-1", "user", "first", 1),
		},
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 0 || fixture.operationCount() != expectedAppliedOperations {
		t.Fatalf("report = %#v, operations = %d", report, fixture.operationCount())
	}
}

func TestExecuteOperationIsolatesFixtureInput(t *testing.T) {
	normal := appendEvent("event-1", "user", "original", 1)
	normal.Event.Extensions = map[string]any{
		"nested": map[string]any{"value": "original"},
		"typed": &typedClonePayload{
			Labels: map[string]string{"value": "original"},
			Items:  []string{"original"},
		},
	}
	fault := normal
	fault.InjectedFailure = "expected"
	fault.FailurePoint = FailureBeforeWrite
	fault.ExpectFailure = true
	faultAfter := fault
	faultAfter.FailurePoint = FailureAfterWrite
	second := appendEvent("event-2", "assistant", "second", 2)
	second.Event.Extensions = map[string]any{"nested": map[string]any{"value": "second"}}
	parallel := Operation{Kind: OperationParallel, Parallel: []Operation{normal, second}}
	for _, test := range []struct {
		name      string
		operation Operation
	}{
		{name: "normal", operation: normal},
		{name: "fault before write", operation: fault},
		{name: "fault after write", operation: faultAfter},
		{name: "parallel", operation: parallel},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := cloneOperation(test.operation)
			fixture := &fakeFixture{
				name: "mutating", capabilities: allCapabilities(),
				mutateOperation: mutateEventOperation,
			}
			if err := executeOperation(context.Background(), fixture, test.operation); err != nil {
				t.Fatalf("executeOperation() error = %v", err)
			}
			if !reflect.DeepEqual(test.operation, want) {
				t.Fatalf("fixture mutation escaped execution boundary:\ngot:  %#v\nwant: %#v", test.operation, want)
			}
		})
	}
}

func TestRunnerIsolatesOperationsBetweenBackends(t *testing.T) {
	operation := appendEvent("event-1", "user", "original", 1)
	operation.Event.Extensions = map[string]any{
		"nested": map[string]any{"value": "original"},
		"typed": &typedClonePayload{
			Labels: map[string]string{"value": "original"},
			Items:  []string{"original"},
		},
	}
	baseline := &fakeFixture{
		name: "baseline", capabilities: allCapabilities(), mutateOperation: mutateEventOperation,
	}
	candidate := &fakeFixture{name: "candidate", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{
		fakeBackend("baseline", baseline),
		fakeBackend("candidate", candidate),
	}}
	if _, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "isolated-input", Operations: []Operation{operation},
	}}); err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	got := candidate.operation(0)
	typed := got.Event.Extensions["typed"].(*typedClonePayload)
	if got.Event.Content != "original" ||
		got.Event.Extensions["nested"].(map[string]any)["value"] != "original" ||
		typed.Labels["value"] != "original" || typed.Items[0] != "original" {
		t.Fatalf("candidate received mutated operation: %#v", got)
	}
}

func mutateEventOperation(operation *Operation) {
	if operation.Event == nil {
		return
	}
	operation.Event.Content = "mutated"
	if nested, ok := operation.Event.Extensions["nested"].(map[string]any); ok {
		nested["value"] = "mutated"
	}
	if typed, ok := operation.Event.Extensions["typed"].(*typedClonePayload); ok {
		typed.Labels["value"] = "mutated"
		typed.Items[0] = "mutated"
	}
}

func TestExecuteParallelCancellationReleasesReadyBarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	operations := []Operation{
		namedOperation(appendEvent("event-1", "user", "one", 1), "one"),
		namedOperation(appendEvent("event-2", "assistant", "two", 2), "two"),
	}
	err := executeParallel(ctx, fixture, operations)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeParallel() error = %v, want context canceled", err)
	}
	if fixture.operationCount() != 0 {
		t.Fatalf("canceled parallel execution applied %d operations", fixture.operationCount())
	}
}

func TestRunnerEnforcesSnapshotInvariants(t *testing.T) {
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	_, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "invalid-recovery",
		Invariants: []SnapshotInvariant{{
			Name: "one memory",
			Check: func(snapshot Snapshot) error {
				if len(snapshot.Memories) != 1 {
					return fmt.Errorf("memory count = %d", len(snapshot.Memories))
				}
				return nil
			},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "snapshot invariant \"one memory\"") {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if !fixture.isClosed() {
		t.Fatal("fixture was not closed after invariant failure")
	}
}

func TestRunnerEnforcesSnapshotInvariantsOnCandidates(t *testing.T) {
	baseline := &fakeFixture{
		name: "baseline", capabilities: allCapabilities(),
		snapshot: Snapshot{Memories: []MemorySnapshot{{ID: "memory-1"}}},
	}
	candidate := &fakeFixture{name: "candidate", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{
		fakeBackend("baseline", baseline), fakeBackend("candidate", candidate),
	}}
	_, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "candidate-invariant",
		Invariants: []SnapshotInvariant{{
			Name: "one memory",
			Check: func(snapshot Snapshot) error {
				if len(snapshot.Memories) != 1 {
					return fmt.Errorf("memory count = %d", len(snapshot.Memories))
				}
				return nil
			},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "backend \"candidate\"") {
		t.Fatalf("Runner.Run() error = %v", err)
	}
}

func TestRunnerIsolatesSnapshotsFromInvariants(t *testing.T) {
	baselineSnapshot := comparisonFixture()
	baselineSnapshot.Sessions[0].State = map[string]StateValueSnapshot{
		"state": JSONStateValue(map[string]any{"nested": "original"}),
	}
	baselineSnapshot.Sessions[0].Events[0].Extensions =
		map[string]any{"nested": map[string]any{"value": "original"}}
	baselineSnapshot.Sessions[0].Summaries[0].Boundary =
		map[string]any{"event": "event-1"}
	baselineSnapshot.Sessions[0].Tracks[0].Events[0].Payload =
		map[string]any{"value": "original"}
	baselineSnapshot.Memories[0].Topics = []string{"original"}
	baselineSnapshot.Memories[0].Metadata =
		map[string]any{"nested": map[string]any{"value": "original"}}
	baselineSnapshot.MemorySearches = []MemorySearchSnapshot{{
		Results: []MemorySnapshot{{
			ID: "search-result", Metadata: map[string]any{
				"nested": map[string]any{"value": "original"},
			},
		}},
	}}
	candidateSnapshot := comparisonFixture()
	candidateSnapshot.Sessions[0].State = cloneStateMap(baselineSnapshot.Sessions[0].State)
	candidateSnapshot.Sessions[0].Events[0].Extensions =
		cloneStringMap(baselineSnapshot.Sessions[0].Events[0].Extensions)
	candidateSnapshot.Sessions[0].Summaries[0].Boundary =
		cloneStringMap(baselineSnapshot.Sessions[0].Summaries[0].Boundary)
	candidateSnapshot.Sessions[0].Tracks[0].Events[0].Payload =
		cloneStringMap(baselineSnapshot.Sessions[0].Tracks[0].Events[0].Payload)
	candidateSnapshot.Memories[0].Topics =
		append([]string(nil), baselineSnapshot.Memories[0].Topics...)
	candidateSnapshot.Memories[0].Metadata =
		cloneStringMap(baselineSnapshot.Memories[0].Metadata)
	candidateSnapshot.MemorySearches =
		cloneSnapshot(baselineSnapshot).MemorySearches
	candidateSnapshot.Sessions[0].Events[0].Content = "candidate"
	runner := Runner{Backends: []Backend{
		fakeBackend("baseline", &fakeFixture{
			name: "baseline", capabilities: allCapabilities(), snapshot: baselineSnapshot,
		}),
		fakeBackend("candidate", &fakeFixture{
			name: "candidate", capabilities: allCapabilities(), snapshot: candidateSnapshot,
		}),
	}}
	mutate := SnapshotInvariant{
		Name: "mutate nested values",
		Check: func(snapshot Snapshot) error {
			snapshot.Sessions[0].State["state"].Value.(map[string]any)["nested"] = "mutated"
			snapshot.Sessions[0].Events[0].Content = "same"
			snapshot.Sessions[0].Events[0].Extensions["nested"].(map[string]any)["value"] = "mutated"
			snapshot.Sessions[0].Summaries[0].Boundary["event"] = "mutated"
			snapshot.Sessions[0].Tracks[0].Events[0].Payload["value"] = "mutated"
			snapshot.Memories[0].Topics[0] = "mutated"
			snapshot.Memories[0].Metadata["nested"].(map[string]any)["value"] = "mutated"
			snapshot.MemorySearches[0].Results[0].Metadata["nested"].(map[string]any)["value"] = "mutated"
			return nil
		},
	}
	if err := validateSnapshot(ReplayCase{Invariants: []SnapshotInvariant{mutate}}, baselineSnapshot); err != nil {
		t.Fatalf("validateSnapshot() error = %v", err)
	}
	if baselineSnapshot.Sessions[0].State["state"].Value.(map[string]any)["nested"] != "original" ||
		baselineSnapshot.Sessions[0].Events[0].Content != "answer" ||
		baselineSnapshot.Sessions[0].Events[0].Extensions["nested"].(map[string]any)["value"] != "original" ||
		baselineSnapshot.Sessions[0].Summaries[0].Boundary["event"] != "event-1" ||
		baselineSnapshot.Sessions[0].Tracks[0].Events[0].Payload["value"] != "original" ||
		baselineSnapshot.Memories[0].Topics[0] != "original" ||
		baselineSnapshot.Memories[0].Metadata["nested"].(map[string]any)["value"] != "original" ||
		baselineSnapshot.MemorySearches[0].Results[0].Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("invariant mutation escaped snapshot boundary: %#v", baselineSnapshot)
	}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "isolated-invariant", Invariants: []SnapshotInvariant{mutate},
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	differenceAt(
		t, report.Differences,
		"$.sessions[0].events[0].content",
	)
}

func TestRunnerRejectsInvalidSnapshotInvariant(t *testing.T) {
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	_, err := runner.Run(context.Background(), []ReplayCase{{
		Name:       "invalid-invariant",
		Invariants: []SnapshotInvariant{{Name: "missing check"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "snapshot invariant 0 is invalid") {
		t.Fatalf("Runner.Run() error = %v", err)
	}
}

func TestRunnerDoesNotAllowUnsupportedCapabilitiesByDefault(t *testing.T) {
	baselineFixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	limitedFixture := &fakeFixture{name: "limited", capabilities: CapabilitySet{}}
	runner := Runner{Backends: []Backend{
		fakeBackend("inmemory", baselineFixture),
		fakeBackend("limited", limitedFixture),
	}}
	report, err := runner.Run(context.Background(), []ReplayCase{{
		Name:         "memory",
		Capabilities: []Capability{CapabilityMemory},
		Operations:   []Operation{writeMemory("content", "fact")},
	}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 1 || report.Differences[0].AllowedDiff {
		t.Fatalf("unsupported report = %#v", report)
	}
	if len(report.Cases) != 1 || report.Cases[0].Status != ResultFail {
		t.Fatalf("unsupported case result = %#v", report.Cases)
	}
}

func TestRunnerRejectsInvalidOrUnusedUnsupportedAllowances(t *testing.T) {
	backend := fakeBackend(
		"inmemory", &fakeFixture{name: "inmemory", capabilities: allCapabilities()},
	)
	cases := []ReplayCase{{Name: "summary", Capabilities: []Capability{CapabilitySummary}}}
	tests := []struct {
		name       string
		allowances []UnsupportedAllowance
		want       string
	}{
		{
			name: "empty reason",
			allowances: []UnsupportedAllowance{{
				Backend: "inmemory", Case: "summary", Capability: CapabilitySummary,
			}},
			want: "empty fields",
		},
		{
			name: "duplicate",
			allowances: []UnsupportedAllowance{
				{Backend: "inmemory", Case: "summary", Capability: CapabilitySummary, Reason: "one"},
				{Backend: "inmemory", Case: "summary", Capability: CapabilitySummary, Reason: "two"},
			},
			want: "duplicated",
		},
		{
			name: "unknown backend",
			allowances: []UnsupportedAllowance{{
				Backend: "missing", Case: "summary", Capability: CapabilitySummary, Reason: "test",
			}},
			want: "unknown backend",
		},
		{
			name: "unknown case",
			allowances: []UnsupportedAllowance{{
				Backend: "inmemory", Case: "missing", Capability: CapabilitySummary, Reason: "test",
			}},
			want: "unknown case",
		},
		{
			name: "unused",
			allowances: []UnsupportedAllowance{{
				Backend: "inmemory", Case: "summary", Capability: CapabilitySummary,
				Reason: "not consumed because baseline has no candidate",
			}},
			want: "unused unsupported allowance",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := Runner{Backends: []Backend{backend}, UnsupportedAllowances: test.allowances}
			_, err := runner.Run(context.Background(), cases)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunnerExpectedFailureRejectsUnrelatedErrors(t *testing.T) {
	wantErr := errors.New("database unavailable")
	fixture := &fakeFixture{
		name:         "inmemory",
		capabilities: allCapabilities(),
		applyErr:     wantErr,
	}
	operation := appendEvent("event-1", "user", "content", 1)
	operation.ExpectFailure = true
	operation.InjectedFailure = "injected"
	operation.FailurePoint = FailureBeforeWrite
	fixture.faultErr = wantErr
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	_, err := runner.Run(context.Background(), []ReplayCase{{
		Name:       "failure",
		Operations: []Operation{operation},
	}})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "expected injected failure") {
		t.Fatalf("Runner.Run() error = %v", err)
	}
}

func TestRunnerPropagatesErrorsAndStillClosesFixture(t *testing.T) {
	wantErr := errors.New("append failed")
	fixture := &fakeFixture{
		name:         "inmemory",
		capabilities: allCapabilities(),
		applyErr:     wantErr,
	}
	runner := Runner{Backends: []Backend{fakeBackend("inmemory", fixture)}}
	_, err := runner.Run(context.Background(), []ReplayCase{{
		Name: "failure",
		Operations: []Operation{appendEvent(
			"event-1", "user", "content", 1,
		)},
	}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Runner.Run() error = %v, want %v", err, wantErr)
	}
	if !fixture.isClosed() {
		t.Fatal("failed fixture was not closed")
	}
}

func TestRunnerValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		runner  Runner
		cases   []ReplayCase
		wantErr string
	}{
		{name: "no backends", wantErr: "no backends"},
		{
			name:    "invalid backend",
			runner:  Runner{Backends: []Backend{{Name: "invalid"}}},
			wantErr: "backend 0 is invalid",
		},
		{
			name: "empty case",
			runner: Runner{Backends: []Backend{fakeBackend(
				"inmemory", &fakeFixture{name: "inmemory", capabilities: allCapabilities()},
			)}},
			cases:   []ReplayCase{{}},
			wantErr: "case name is empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.runner.Run(context.Background(), test.cases)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestRunnerPropagatesFixtureLifecycleErrors(t *testing.T) {
	wantErr := errors.New("fixture failure")
	tests := []struct {
		name     string
		backend  Backend
		replay   ReplayCase
		wantText string
	}{
		{
			name: "create error",
			backend: Backend{Name: "broken", New: func(context.Context, string) (Fixture, error) {
				return nil, wantErr
			}},
			replay: ReplayCase{Name: "case"}, wantText: "create fixture",
		},
		{
			name: "nil fixture",
			backend: Backend{Name: "nil", New: func(context.Context, string) (Fixture, error) {
				return nil, nil
			}},
			replay: ReplayCase{Name: "case"}, wantText: "returned nil",
		},
		{
			name: "fixture name mismatch",
			backend: fakeBackend("expected", &fakeFixture{
				name: "actual", capabilities: allCapabilities(), closeErr: wantErr,
			}),
			replay: ReplayCase{Name: "case"}, wantText: "does not match backend",
		},
		{
			name: "unsupported fixture close",
			backend: fakeBackend("limited", &fakeFixture{
				name: "limited", capabilities: CapabilitySet{}, closeErr: wantErr,
			}),
			replay:   ReplayCase{Name: "case", Capabilities: []Capability{CapabilitySession}},
			wantText: "close unsupported fixture",
		},
		{
			name: "snapshot read",
			backend: fakeBackend("broken", &fakeFixture{
				name: "broken", capabilities: allCapabilities(), snapshotErr: wantErr,
			}),
			replay: ReplayCase{Name: "case"}, wantText: "read snapshot",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := Runner{Backends: []Backend{test.backend}}
			_, err := runner.Run(context.Background(), []ReplayCase{test.replay})
			if err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Runner.Run() error = %v, want %q", err, test.wantText)
			}
		})
	}
}

func TestExecuteCaseRejectsTopLevelDependencies(t *testing.T) {
	fixture := &fakeFixture{name: "inmemory", capabilities: allCapabilities()}
	operation := appendEvent("event-1", "user", "content", 1)
	operation.After = []string{"dependency"}
	_, err := executeCase(context.Background(), fixture, ReplayCase{
		Name: "case", Operations: []Operation{operation},
	})
	if err == nil || !strings.Contains(err.Error(), "top-level dependencies") {
		t.Fatalf("executeCase() error = %v", err)
	}
}

func TestExecuteCaseDetachesSnapshotBeforeClosingFixture(t *testing.T) {
	state := map[string]StateValueSnapshot{
		"theme": JSONStateValue(map[string]any{"name": "dark"}),
	}
	fixture := &fakeFixture{
		name: "inmemory", capabilities: allCapabilities(),
		snapshot: Snapshot{Sessions: []SessionSnapshot{{ID: "session", State: state}}},
		mutateSnapshotOnClose: func(snapshot *Snapshot) {
			value := snapshot.Sessions[0].State["theme"].Value.(map[string]any)
			value["name"] = "cleared"
			delete(snapshot.Sessions[0].State, "theme")
		},
	}
	snapshot, err := executeCase(context.Background(), fixture, ReplayCase{Name: "case"})
	if err != nil {
		t.Fatalf("executeCase() error = %v", err)
	}
	value := snapshot.Sessions[0].State["theme"].Value.(map[string]any)
	if value["name"] != "dark" {
		t.Fatalf("detached snapshot state = %#v", snapshot.Sessions[0].State)
	}
}

type fakeFixture struct {
	mu                    sync.Mutex
	name                  string
	capabilities          CapabilitySet
	snapshot              Snapshot
	operations            []Operation
	applyErr              error
	faultErr              error
	snapshotErr           error
	closeErr              error
	closed                bool
	closeCount            int
	mutateOperation       func(*Operation)
	mutateSnapshotOnClose func(*Snapshot)
}

func (fixture *fakeFixture) Name() string {
	return fixture.name
}

func (fixture *fakeFixture) Capabilities() CapabilitySet {
	return fixture.capabilities
}

func (fixture *fakeFixture) Apply(_ context.Context, operation Operation) error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.mutateOperation != nil {
		fixture.mutateOperation(&operation)
	}
	fixture.operations = append(fixture.operations, operation)
	return fixture.applyErr
}

func (fixture *fakeFixture) ApplyWithFault(ctx context.Context, operation Operation) error {
	if fixture.faultErr != nil {
		return fixture.faultErr
	}
	if operation.FailurePoint == FailureAfterWrite {
		if err := fixture.Apply(ctx, operation); err != nil {
			return err
		}
	} else if fixture.mutateOperation != nil {
		fixture.mutateOperation(&operation)
	}
	return fmt.Errorf("%w: %s", ErrInjectedFailure, operation.InjectedFailure)
}

func (fixture *fakeFixture) operation(index int) Operation {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return cloneOperation(fixture.operations[index])
}

func (fixture *fakeFixture) Snapshot(context.Context) (Snapshot, error) {
	return fixture.snapshot, fixture.snapshotErr
}

func (fixture *fakeFixture) Close() error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.mutateSnapshotOnClose != nil {
		fixture.mutateSnapshotOnClose(&fixture.snapshot)
	}
	fixture.closed = true
	fixture.closeCount++
	return fixture.closeErr
}

func (fixture *fakeFixture) isClosed() bool {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.closed
}

func (fixture *fakeFixture) fixtureCloseCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.closeCount
}

func (fixture *fakeFixture) operationCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.operations)
}

func (fixture *fakeFixture) operationNames() []string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	names := make([]string, len(fixture.operations))
	for i := range fixture.operations {
		names[i] = fixture.operations[i].Name
	}
	return names
}

func fakeBackend(name string, fixture *fakeFixture) Backend {
	return Backend{
		Name: name,
		New: func(context.Context, string) (Fixture, error) {
			return fixture, nil
		},
	}
}

func allCapabilities() CapabilitySet {
	return CapabilitySet{
		CapabilitySession:      true,
		CapabilityMemory:       true,
		CapabilitySummary:      true,
		CapabilityTrack:        true,
		CapabilityEventPaging:  true,
		CapabilityTTL:          true,
		CapabilityMemorySearch: true,
	}
}
