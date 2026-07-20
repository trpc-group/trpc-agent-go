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
		Operations:   []Operation{writeMemory("memory-1", "content", "fact")},
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

type fakeFixture struct {
	mu           sync.Mutex
	name         string
	capabilities CapabilitySet
	snapshot     Snapshot
	operations   []Operation
	applyErr     error
	faultErr     error
	snapshotErr  error
	closeErr     error
	closed       bool
	closeCount   int
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
	}
	return fmt.Errorf("%w: %s", ErrInjectedFailure, operation.InjectedFailure)
}

func (fixture *fakeFixture) Snapshot(context.Context) (Snapshot, error) {
	return fixture.snapshot, fixture.snapshotErr
}

func (fixture *fakeFixture) Close() error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
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
