//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const faultyBackendName = "faulty-inmemory"

type writeFault func(replaytest.Operation) (replaytest.Operation, bool)

type writeFaultFixture struct {
	replaytest.Fixture
	fault writeFault
}

type writeFaultProbe struct {
	mu   sync.Mutex
	hits int
}

func (probe *writeFaultProbe) wrap(fault writeFault) writeFault {
	return func(operation replaytest.Operation) (replaytest.Operation, bool) {
		before := cloneWriteOperation(operation)
		after, apply := fault(operation)
		if !apply || !reflect.DeepEqual(before, after) {
			probe.mu.Lock()
			probe.hits++
			probe.mu.Unlock()
		}
		return after, apply
	}
}

func (probe *writeFaultProbe) count() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.hits
}

func TestWriteFaultFixtureMutatesAppliedOperation(t *testing.T) {
	backend := newWriteFaultBackend(onceWriteFault(
		func(operation replaytest.Operation) bool { return operation.Event != nil },
		func(operation *replaytest.Operation) { operation.Event.Content = "mutated" },
	))
	fixture, err := backend.New(context.Background(), "write-boundary")
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	if err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: "session-1",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationAppendEvent, SessionID: "session-1",
		Event: &replaytest.EventSnapshot{
			ID: "event-1", InvocationID: "invocation-1", Author: "user", Role: "user",
			Content: "original", Done: true,
		},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot fixture: %v", err)
	}
	if got := snapshot.Sessions[0].Events[0].Content; got != "mutated" {
		t.Fatalf("event content = %q, want mutated", got)
	}
}

func (*writeFaultFixture) Name() string {
	return faultyBackendName
}

func (fixture *writeFaultFixture) Apply(ctx context.Context, operation replaytest.Operation) error {
	operation, apply := fixture.fault(cloneWriteOperation(operation))
	if !apply {
		return nil
	}
	return fixture.Fixture.Apply(ctx, operation)
}

func (fixture *writeFaultFixture) ApplyWithFault(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	operation, apply := fixture.fault(cloneWriteOperation(operation))
	if !apply {
		return fmt.Errorf("%w: dropped by write-boundary fault", replaytest.ErrInjectedFailure)
	}
	injector, ok := fixture.Fixture.(replaytest.FaultInjector)
	if !ok {
		return fmt.Errorf("wrapped fixture does not support fault injection")
	}
	return injector.ApplyWithFault(ctx, operation)
}

func TestStandardReplayCasesDetectWriteBoundaryFaults(t *testing.T) {
	tests := map[string]writeFault{
		"single-turn": onceWriteFault(
			func(operation replaytest.Operation) bool { return eventID(operation) == "event-1" },
			func(operation *replaytest.Operation) { operation.Event.Content = "write-fault" },
		),
		"multi-turn": onceWriteFault(
			func(operation replaytest.Operation) bool { return eventID(operation) == "event-2" },
			func(operation *replaytest.Operation) { operation.Event.Content = "write-fault" },
		),
		"tool-call": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.Event != nil && operation.Event.ToolResponse != nil
			},
			func(operation *replaytest.Operation) {
				operation.Event.Extensions[event.ToolCallArgsExtensionKey] = map[string]string{
					"call-1": `{"city":"Beijing"}`,
				}
			},
		),
		"state-update": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.Kind == replaytest.OperationUpdateState && operation.InjectedFailure == ""
			},
			func(operation *replaytest.Operation) { operation.StateUpdates["write_fault"] = true },
		),
		"memory-read-write": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.Memory != nil && operation.Memory.ID == "memory-2"
			},
			func(operation *replaytest.Operation) { operation.Memory.Content = "lives in Guangzhou" },
		),
		"summary-update": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.Summary != nil && operation.Summary.Text == "updated summary" &&
					operation.InjectedFailure == ""
			},
			func(operation *replaytest.Operation) { operation.Summary.Text = "write-fault summary" },
		),
		"summary-truncation": onceWriteFault(
			func(operation replaytest.Operation) bool { return operation.Summary != nil },
			func(operation *replaytest.Operation) { operation.Summary.Text = "write-fault context" },
		),
		"track-events": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.TrackEvent != nil && operation.TrackEvent.EventType == "completed"
			},
			func(operation *replaytest.Operation) { operation.TrackEvent.Error = "write-fault" },
		),
		"concurrent-out-of-order": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return operation.SessionID == "session-2" && operation.Event != nil &&
					operation.Event.Author == "sub-agent"
			},
			func(operation *replaytest.Operation) {
				operation.Event.Extensions = map[string]any{"write_fault": true}
			},
		),
		"failure-retry": onceWriteFault(
			func(operation replaytest.Operation) bool {
				return eventID(operation) == "event-2" && operation.InjectedFailure == ""
			},
			func(operation *replaytest.Operation) { operation.Event.Content = "retried with write-fault" },
		),
	}
	expectedPaths := map[string]string{
		"single-turn":             ".events[",
		"multi-turn":              ".events[",
		"tool-call":               ".extensions.trpc_agent.tool_call_args.call-1",
		"state-update":            ".state.write_fault",
		"memory-read-write":       "$.memories[",
		"summary-update":          ".summaries[",
		"summary-truncation":      ".summaries[",
		"track-events":            ".tracks[",
		"concurrent-out-of-order": ".events[",
		"failure-retry":           ".events[",
	}
	for _, replayCase := range replaytest.StandardReplayCases() {
		fault, ok := tests[replayCase.Name]
		if !ok {
			t.Fatalf("case %q lacks a write-boundary fault", replayCase.Name)
		}
		t.Run(replayCase.Name, func(t *testing.T) {
			probe := &writeFaultProbe{}
			report := runWriteFaultCase(t, replayCase, probe.wrap(fault))
			if probe.count() != 1 {
				t.Fatalf("write-boundary fault hits = %d, want 1", probe.count())
			}
			assertUnexpectedExplainedDifferences(t, report, expectedPaths[replayCase.Name])
		})
	}
}

func TestSummaryCriticalWriteBoundaryFaults(t *testing.T) {
	tests := []struct {
		name  string
		fault writeFault
	}{
		{
			name: "missing",
			fault: func(operation replaytest.Operation) (replaytest.Operation, bool) {
				operation = cloneWriteOperation(operation)
				drop := operation.Kind == replaytest.OperationUpdateSummary &&
					operation.SessionID == "session-1" && operation.InjectedFailure == ""
				return operation, !drop
			},
		},
		{
			name: "overwrite",
			fault: onceWriteFault(
				finalSummaryWrite,
				func(operation *replaytest.Operation) { operation.Summary.Text = "stale summary" },
			),
		},
		{
			name: "wrong-session",
			fault: onceWriteFault(
				finalSummaryWrite,
				func(operation *replaytest.Operation) {
					operation.SessionID = "session-2"
					operation.Summary.SessionID = "session-2"
				},
			),
		},
		{
			name: "wrong-filter-key",
			fault: onceWriteFault(
				finalSummaryWrite,
				func(operation *replaytest.Operation) { operation.Summary.FilterKey = "wrong/filter" },
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &writeFaultProbe{}
			report := runWriteFaultCase(
				t, standardCase(t, "summary-update"), probe.wrap(test.fault),
			)
			wantHits := 1
			if test.name == "missing" {
				wantHits = 2
			}
			if probe.count() != wantHits {
				t.Fatalf("write-boundary fault hits = %d, want %d", probe.count(), wantHits)
			}
			assertUnexpectedExplainedDifferences(t, report, ".summaries")
			foundSummaryLocator := false
			for _, difference := range report.Differences {
				foundSummaryLocator = foundSummaryLocator || difference.Locator.SummaryFilterKey != ""
			}
			if !foundSummaryLocator {
				t.Fatalf("summary fault lacks summary locator: %#v", report.Differences)
			}
		})
	}
}

func runWriteFaultCase(
	t *testing.T,
	replayCase replaytest.ReplayCase,
	fault writeFault,
) replaytest.Report {
	t.Helper()
	// These tests exercise diff paths and explanations. Standard matrix tests retain
	// the case invariants and independently enforce the semantic oracle.
	replayCase.Invariants = nil
	runner := replaytest.Runner{
		Backends: []replaytest.Backend{
			newInMemoryBackend(),
			newWriteFaultBackend(fault),
		},
		NormalizeOptions: standardNormalizeOptions(),
		CompareOptions:   replaytest.DefaultCompareOptions(),
	}
	report, err := runner.Run(context.Background(), []replaytest.ReplayCase{replayCase})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	return report
}

func newWriteFaultBackend(fault writeFault) replaytest.Backend {
	return replaytest.Backend{
		Name: faultyBackendName,
		New: func(context.Context, string) (replaytest.Fixture, error) {
			summarizer := &replaySummarizer{}
			fixture := newReplayFixture(replayFixtureConfig{
				name: faultyBackendName,
				sessionService: sessioninmemory.NewSessionService(
					sessioninmemory.WithSummarizer(summarizer),
					sessioninmemory.WithSummaryFilterAllowlist(filterKeyMain, "wrong/filter"),
				),
				memoryService: inmemory.NewMemoryService(),
				summarizer:    summarizer,
			})
			return &writeFaultFixture{Fixture: fixture, fault: fault}, nil
		},
	}
}

func onceWriteFault(
	match func(replaytest.Operation) bool,
	mutate func(*replaytest.Operation),
) writeFault {
	var mutex sync.Mutex
	applied := false
	return func(operation replaytest.Operation) (replaytest.Operation, bool) {
		mutex.Lock()
		defer mutex.Unlock()
		if !applied && match(operation) {
			mutate(&operation)
			applied = true
		}
		return operation, true
	}
}

func finalSummaryWrite(operation replaytest.Operation) bool {
	return operation.Summary != nil && operation.Summary.Text == "updated summary" &&
		operation.InjectedFailure == ""
}

func eventID(operation replaytest.Operation) string {
	if operation.Event == nil {
		return ""
	}
	return operation.Event.ID
}

func standardCase(t *testing.T, name string) replaytest.ReplayCase {
	t.Helper()
	for _, replayCase := range replaytest.StandardReplayCases() {
		if replayCase.Name == name {
			return replayCase
		}
	}
	t.Fatalf("standard case %q not found", name)
	return replaytest.ReplayCase{}
}

func assertUnexpectedExplainedDifferences(
	t *testing.T,
	report replaytest.Report,
	wantPath string,
) {
	t.Helper()
	if !report.HasUnexpectedDifferences() || len(report.Differences) == 0 {
		t.Fatalf("write-boundary fault was not detected: %#v", report)
	}
	foundPath := false
	for _, difference := range report.Differences {
		if !difference.AllowedDiff && difference.Explanation == "" {
			t.Fatalf("difference lacks explanation: %#v", difference)
		}
		if !difference.AllowedDiff && strings.Contains(difference.Path, wantPath) {
			foundPath = true
		}
	}
	if !foundPath {
		t.Fatalf("difference lacks expected path %q: %#v", wantPath, report.Differences)
	}
}

func cloneWriteOperation(operation replaytest.Operation) replaytest.Operation {
	// Keep this explicit deep-clone list in sync when Operation or its nested
	// snapshot types gain map or slice fields, preserving alias isolation.
	operation.After = append([]string(nil), operation.After...)
	operation.StateDeletes = append([]string(nil), operation.StateDeletes...)
	operation.StateUpdates = cloneAnyMap(operation.StateUpdates)
	if operation.Event != nil {
		event := *operation.Event
		event.ToolCalls = append([]replaytest.ToolCallSnapshot(nil), operation.Event.ToolCalls...)
		for index := range event.ToolCalls {
			event.ToolCalls[index].Arguments = cloneAny(event.ToolCalls[index].Arguments)
			event.ToolCalls[index].Extra = cloneAnyMap(event.ToolCalls[index].Extra)
		}
		if operation.Event.ToolResponse != nil {
			response := *operation.Event.ToolResponse
			response.Extra = cloneAnyMap(response.Extra)
			event.ToolResponse = &response
		}
		event.Extensions = cloneAnyMap(event.Extensions)
		event.StateDelta = cloneStateValues(event.StateDelta)
		operation.Event = &event
	}
	if operation.Memory != nil {
		memory := *operation.Memory
		memory.Topics = append([]string(nil), memory.Topics...)
		memory.Metadata = cloneAnyMap(memory.Metadata)
		operation.Memory = &memory
	}
	if operation.Summary != nil {
		summary := *operation.Summary
		summary.Boundary = cloneAnyMap(summary.Boundary)
		operation.Summary = &summary
	}
	if operation.TrackEvent != nil {
		trackEvent := *operation.TrackEvent
		trackEvent.Payload = cloneAnyMap(trackEvent.Payload)
		operation.TrackEvent = &trackEvent
	}
	return operation
}

func cloneStateValues(values map[string]replaytest.StateValueSnapshot) map[string]replaytest.StateValueSnapshot {
	if values == nil {
		return nil
	}
	cloned := make(map[string]replaytest.StateValueSnapshot, len(values))
	for key, value := range values {
		value.Value = cloneAny(value.Value)
		cloned[key] = value
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index := range typed {
			cloned[index] = cloneAny(typed[index])
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return value
	}
}
