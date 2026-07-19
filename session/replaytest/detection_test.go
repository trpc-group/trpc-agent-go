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
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestStandardReplayCasesDetectInjectedDifferences(t *testing.T) {
	mutators := map[string]func(*Snapshot){
		"single-turn": func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events[0].Content = "mutated"
		},
		"multi-turn": func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events[0], snapshot.Sessions[0].Events[1] =
				snapshot.Sessions[0].Events[1], snapshot.Sessions[0].Events[0]
		},
		"tool-call": func(snapshot *Snapshot) {
			sessionByID(snapshot, standardSessionID).Events[1].ToolCalls[0].Arguments =
				map[string]any{"city": "Beijing"}
		},
		"state-update": func(snapshot *Snapshot) {
			snapshot.Sessions[0].State["theme"] = JSONStateValue("mutated")
		},
		"memory-read-write": func(snapshot *Snapshot) {
			snapshot.MemorySearches[0].Results[0].Content = "mutated"
		},
		"summary-update": func(snapshot *Snapshot) {
			sessionByID(snapshot, standardSessionID).Summaries[0].Text = "stale summary"
		},
		"summary-truncation": func(snapshot *Snapshot) {
			sessionByID(snapshot, standardSessionID).Summaries = nil
		},
		"track-events": func(snapshot *Snapshot) {
			snapshot.Sessions[0].Tracks[0].Events[1].Error = "mutated error"
		},
		"concurrent-out-of-order": func(snapshot *Snapshot) {
			sess := sessionByID(snapshot, standardSessionID)
			sess.Events[0], sess.Events[1] = sess.Events[1], sess.Events[0]
		},
		"failure-retry": func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events = append(
				snapshot.Sessions[0].Events,
				snapshot.Sessions[0].Events[len(snapshot.Sessions[0].Events)-1],
			)
		},
	}

	cases := StandardReplayCases()
	if len(mutators) != len(cases) {
		t.Fatalf("mutator count = %d, case count = %d", len(mutators), len(cases))
	}
	detected := 0
	for _, replayCase := range cases {
		replayCase := replayCase
		if t.Run(replayCase.Name, func(t *testing.T) {
			mutate, ok := mutators[replayCase.Name]
			if !ok {
				t.Fatalf("no mutator for case %q", replayCase.Name)
			}
			runner := modelRunner(nil, mutate)
			report, err := runner.Run(context.Background(), []ReplayCase{replayCase})
			if err != nil {
				if strings.Contains(err.Error(), "snapshot invariant") {
					return
				}
				t.Fatalf("Runner.Run() error = %v", err)
			}
			if !report.HasUnexpectedDifferences() {
				t.Fatalf("injected difference was not detected: %#v", report)
			}
		}) {
			detected++
		}
	}
	if detected != len(cases) {
		t.Fatalf("detected mutations = %d, injected mutations = %d", detected, len(cases))
	}
}

func TestSummaryCriticalDifferencesAreDetected(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Snapshot)
		locator func(Locator) bool
	}{
		{
			name: "missing",
			mutate: func(snapshot *Snapshot) {
				sessionByID(snapshot, standardSessionID).Summaries = nil
			},
			locator: func(locator Locator) bool {
				return locator.SummaryFilterKey == "branch/main"
			},
		},
		{
			name: "overwrite",
			mutate: func(snapshot *Snapshot) {
				sessionByID(snapshot, standardSessionID).Summaries[0].Text = "old summary"
			},
			locator: func(locator Locator) bool {
				return locator.SummaryFilterKey == "branch/main"
			},
		},
		{
			name: "session ownership",
			mutate: func(snapshot *Snapshot) {
				sessionByID(snapshot, standardSessionID).Summaries[0].SessionID = "wrong-session"
			},
			locator: func(locator Locator) bool {
				return locator.SessionID == standardSessionID
			},
		},
		{
			name: "filter key",
			mutate: func(snapshot *Snapshot) {
				sessionByID(snapshot, standardSessionID).Summaries[0].FilterKey = "wrong/filter"
			},
			locator: func(locator Locator) bool {
				return locator.SummaryFilterKey == "wrong/filter"
			},
		},
	}
	detected := 0
	for _, test := range tests {
		if t.Run(test.name, func(t *testing.T) {
			runner := modelRunner(nil, test.mutate)
			replayCase := summaryUpdateCase()
			// This test targets comparator locators; invariant enforcement is covered separately.
			replayCase.Invariants = nil
			report, err := runner.Run(context.Background(), []ReplayCase{replayCase})
			if err != nil {
				t.Fatalf("Runner.Run() error = %v", err)
			}
			if !report.HasUnexpectedDifferences() {
				t.Fatalf("summary difference was not detected: %#v", report)
			}
			foundLocator := false
			for _, difference := range report.Differences {
				foundLocator = foundLocator || test.locator(difference.Locator)
			}
			if !foundLocator {
				t.Fatalf("summary difference lacks semantic locator: %#v", report)
			}
		}) {
			detected++
		}
	}
	if detected != len(tests) {
		t.Fatalf("detected summary faults = %d, injected faults = %d", detected, len(tests))
	}
}

func TestEventAssociatedFieldsAreDetected(t *testing.T) {
	mutators := []func(*Snapshot){
		func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events[0].InvocationID = "wrong-invocation"
		},
		func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events[0].Object = "wrong.object"
		},
		func(snapshot *Snapshot) {
			snapshot.Sessions[0].Events[0].Done = !snapshot.Sessions[0].Events[0].Done
		},
	}
	for i, mutate := range mutators {
		runner := modelRunner(nil, mutate)
		report, err := runner.Run(context.Background(), []ReplayCase{singleTurnCase()})
		if err != nil {
			t.Fatalf("mutation %d: Runner.Run() error = %v", i, err)
		}
		if !report.HasUnexpectedDifferences() {
			t.Fatalf("mutation %d was not detected", i)
		}
	}
}

func TestStandardReplayCasesHaveNoModelFalsePositives(t *testing.T) {
	report, err := modelRunner(nil, nil).Run(context.Background(), StandardReplayCases())
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(report.Differences) != 0 {
		t.Fatalf("normal replay produced differences: %#v", report)
	}
}

func TestRecoveryCaseLeavesCleanFinalState(t *testing.T) {
	fixture := newModelFixture("model")
	snapshot, err := executeCase(context.Background(), fixture, recoveryCase())
	if err != nil {
		t.Fatalf("executeCase() error = %v", err)
	}
	if len(snapshot.Sessions) != 1 || len(snapshot.Sessions[0].Events) != 2 {
		t.Fatalf("recovery events = %#v", snapshot.Sessions)
	}
	if got := snapshot.Sessions[0].State["status"]; got != JSONStateValue("recovered") {
		t.Fatalf("recovery state status = %#v", got)
	}
	if len(snapshot.Memories) != 1 {
		t.Fatalf("recovery snapshot is dirty: %#v", snapshot)
	}
}

func modelRunner(baselineMutator, actualMutator func(*Snapshot)) Runner {
	return Runner{
		Backends: []Backend{
			modelBackend("baseline", baselineMutator),
			modelBackend("actual", actualMutator),
		},
		NormalizeOptions: DefaultNormalizeOptions(),
		CompareOptions:   DefaultCompareOptions(),
	}
}

func modelBackend(name string, mutate func(*Snapshot)) Backend {
	return Backend{
		Name: name,
		New: func(context.Context, string) (Fixture, error) {
			fixture := newModelFixture(name)
			fixture.mutate = mutate
			return fixture, nil
		},
	}
}

type modelFixture struct {
	mu          sync.Mutex
	name        string
	sessions    map[string]*SessionSnapshot
	memoryOrder []string
	memories    map[string]*MemorySnapshot
	searches    []Operation
	mutate      func(*Snapshot)
}

func newModelFixture(name string) *modelFixture {
	return &modelFixture{
		name:     name,
		sessions: make(map[string]*SessionSnapshot),
		memories: make(map[string]*MemorySnapshot),
	}
}

func (fixture *modelFixture) Name() string {
	return fixture.name
}

func (*modelFixture) Capabilities() CapabilitySet {
	return allCapabilities()
}

func (fixture *modelFixture) Apply(_ context.Context, operation Operation) error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	switch operation.Kind {
	case OperationCreateSession:
		fixture.applyCreateSession(operation)
	case OperationAppendEvent:
		return fixture.applyAppendEvent(operation)
	case OperationUpdateState:
		return fixture.applyUpdateState(operation)
	case OperationWriteMemory:
		fixture.applyWriteMemory(operation)
	case OperationSearchMemory:
		fixture.searches = append(fixture.searches, operation)
	case OperationUpdateSummary:
		return fixture.applyUpdateSummary(operation)
	case OperationSetReplayWindow:
		return fixture.applyReplayWindow(operation)
	case OperationAppendTrack:
		return fixture.applyAppendTrack(operation)
	default:
		return fmt.Errorf("unsupported model operation %q", operation.Kind)
	}
	return nil
}

func (fixture *modelFixture) applyCreateSession(operation Operation) {
	fixture.sessions[operation.SessionID] = &SessionSnapshot{
		ID: operation.SessionID, AppName: "replaytest", UserID: "user-1",
		State: make(map[string]StateValueSnapshot),
	}
}

func (fixture *modelFixture) applyAppendEvent(operation Operation) error {
	session, err := fixture.session(operation.SessionID)
	if err != nil {
		return err
	}
	for i := range session.Events {
		if session.Events[i].ID == operation.Event.ID {
			session.Events[i] = *operation.Event
			return nil
		}
	}
	session.Events = append(session.Events, *operation.Event)
	return nil
}

func (fixture *modelFixture) applyUpdateState(operation Operation) error {
	session, err := fixture.session(operation.SessionID)
	if err != nil {
		return err
	}
	for key, value := range operation.StateUpdates {
		session.State[key] = JSONStateValue(value)
	}
	for _, key := range operation.StateDeletes {
		delete(session.State, key)
	}
	return nil
}

func (fixture *modelFixture) applyWriteMemory(operation Operation) {
	if _, exists := fixture.memories[operation.Memory.ID]; !exists {
		fixture.memoryOrder = append(fixture.memoryOrder, operation.Memory.ID)
	}
	memory := *operation.Memory
	memory.Scope = MemoryScope{AppName: memory.AppName, UserID: memory.UserID}
	fixture.memories[memory.ID] = &memory
}

func (fixture *modelFixture) applyUpdateSummary(operation Operation) error {
	session, err := fixture.session(operation.SessionID)
	if err != nil {
		return err
	}
	if len(session.Events) == 0 {
		return fmt.Errorf("session %q has no summary boundary event", operation.SessionID)
	}
	latest := session.Events[len(session.Events)-1]
	generated := *operation.Summary
	generated.Version = 1
	generated.Boundary = map[string]any{
		"filter_key":    operation.Summary.FilterKey,
		"cutoff_at":     latest.Timestamp,
		"last_event_id": latest.ID,
	}
	generated.UpdatedAt = latest.Timestamp
	for i := range session.Summaries {
		if session.Summaries[i].FilterKey == operation.Summary.FilterKey {
			session.Summaries[i] = generated
			return nil
		}
	}
	session.Summaries = append(session.Summaries, generated)
	return nil
}

func (fixture *modelFixture) applyReplayWindow(operation Operation) error {
	sess, err := fixture.session(operation.SessionID)
	if err != nil {
		return err
	}
	lastEventID := ""
	for _, summary := range sess.Summaries {
		if summary.FilterKey == operation.ReplayWindowFilterKey {
			lastEventID, _ = summary.Boundary["last_event_id"].(string)
			break
		}
	}
	if lastEventID == "" {
		return fmt.Errorf("summary %q has no event boundary", operation.ReplayWindowFilterKey)
	}
	for i := range sess.Events {
		if sess.Events[i].ID == lastEventID {
			sess.Events = append([]EventSnapshot(nil), sess.Events[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("boundary event %q not found", lastEventID)
}

func (fixture *modelFixture) applyAppendTrack(operation Operation) error {
	session, err := fixture.session(operation.SessionID)
	if err != nil {
		return err
	}
	trackIndex := -1
	for i := range session.Tracks {
		if session.Tracks[i].Name == operation.TrackName {
			trackIndex = i
		}
	}
	if trackIndex < 0 {
		session.Tracks = append(session.Tracks, TrackSnapshot{Name: operation.TrackName})
		trackIndex = len(session.Tracks) - 1
	}
	session.Tracks[trackIndex].Events = append(
		session.Tracks[trackIndex].Events, *operation.TrackEvent,
	)
	return nil
}

func (fixture *modelFixture) Snapshot(context.Context) (Snapshot, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	snapshot := Snapshot{}
	for _, session := range fixture.sessions {
		snapshot.Sessions = append(snapshot.Sessions, *session)
	}
	for _, id := range fixture.memoryOrder {
		snapshot.Memories = append(snapshot.Memories, *fixture.memories[id])
	}
	for _, search := range fixture.searches {
		result := MemorySearchSnapshot{
			AppName: search.SearchAppName,
			UserID:  search.SearchUserID,
			Query:   search.SearchQuery,
		}
		limit := search.SearchLimit
		matched := make([]string, 0, len(fixture.memoryOrder))
		for _, id := range fixture.memoryOrder {
			item := fixture.memories[id]
			if item.AppName == search.SearchAppName && item.UserID == search.SearchUserID &&
				strings.Contains(strings.ToLower(item.Content), strings.ToLower(search.SearchQuery)) {
				matched = append(matched, id)
			}
		}
		if limit > len(matched) {
			limit = len(matched)
		}
		for i, id := range matched[:limit] {
			memory := *fixture.memories[id]
			memory.Score = 1 / float64(i+1)
			result.Results = append(result.Results, memory)
		}
		snapshot.MemorySearches = append(snapshot.MemorySearches, result)
	}
	snapshot = cloneSnapshot(snapshot)
	if fixture.mutate != nil {
		fixture.mutate(&snapshot)
	}
	return snapshot, nil
}

func (*modelFixture) Close() error {
	return nil
}

func sessionByID(snapshot *Snapshot, id string) *SessionSnapshot {
	for index := range snapshot.Sessions {
		if snapshot.Sessions[index].ID == id {
			return &snapshot.Sessions[index]
		}
	}
	return nil
}

func (fixture *modelFixture) ApplyWithFault(ctx context.Context, operation Operation) error {
	if operation.FailurePoint == FailureAfterWrite {
		if err := fixture.Apply(ctx, operation); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: %s", ErrInjectedFailure, operation.InjectedFailure)
}

func (fixture *modelFixture) session(id string) (*SessionSnapshot, error) {
	session, ok := fixture.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return session, nil
}
