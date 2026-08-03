//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReplayPropagatesBackendOperationFailures(t *testing.T) {
	injected := errors.New("injected backend failure")
	tests := []replayOperationTest{
		{
			name:       "update app state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failUpdateAppState = true
			},
		},
		{
			name:       "delete app state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failDeleteAppState = true
			},
		},
		{
			name:       "update user state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failUpdateUserState = true
			},
		},
		{
			name:       "delete user state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failDeleteUserState = true
			},
		},
		{
			name:       "update session state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failUpdateSessionState = true
			},
		},
		{
			name:       "get final session",
			replayCase: singleTurnCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failGetMain = true
			},
		},
		{
			name:       "list app state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failListAppState = true
			},
		},
		{
			name:       "list user state",
			replayCase: stateCRUDCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failListUserState = true
			},
		},
		{
			name:       "add memory",
			replayCase: memoryCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.failAdd = true
			},
		},
		{
			name:       "search memory",
			replayCase: memorySearchCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.failSearch = true
			},
		},
		{
			name:       "read memories",
			replayCase: memoryCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.failRead = true
			},
		},
		{
			name:       "create summary",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failCreateSummary = true
			},
		},
		{
			name:       "reload session",
			replayCase: reloadContractCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failGetMain = true
			},
		},
		{
			name:       "create summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failCreateProbe = true
			},
		},
		{
			name:       "get summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failGetProbe = true
			},
		},
		{
			name:       "delete summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failDeleteSession = true
			},
		},
		{
			name:       "append track",
			replayCase: trackCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failAppendTrack = true
			},
		},
		{
			name:       "append concurrent event",
			replayCase: concurrentCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.failEvent = logicalEventMatcher("branch-a-1")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, _, _ := backendWithReplayFaults(InMemoryBackend(), injected, nil, test)
			if _, err := Replay(context.Background(), test.replayCase(), backend); !errors.Is(err, injected) {
				t.Fatalf("Replay() error = %v, want %v", err, injected)
			}
		})
	}
}

func TestReplayStopsImmediatelyAfterBackendCancellation(t *testing.T) {
	tests := []replayOperationTest{
		{
			name:       "create main session",
			replayCase: singleTurnCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelCreateMain = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "AppendEvent", sessionFaults.appendEventCalls.Load(), 0)
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 0)
			},
		},
		{
			name:       "get final session",
			replayCase: singleTurnCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelGetMain = true
				faults.poisonGetMain = true
			},
			verify: func(t *testing.T, _ *replaySessionFaults, _ *replayMemoryFaults, err error) {
				assertErrorOmits(t, err, "has no physical id")
			},
		},
		{
			name:       "list app state",
			replayCase: stateAndMemoryContractCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelListAppState = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, memoryFaults *replayMemoryFaults, _ error) {
				assertCalls(t, "ListUserStates", sessionFaults.listUserStateCalls.Load(), 0)
				assertCalls(t, "ReadMemories", memoryFaults.readCalls.Load(), 0)
			},
		},
		{
			name:       "list user state",
			replayCase: stateAndMemoryContractCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelListUserState = true
			},
			verify: func(t *testing.T, _ *replaySessionFaults, memoryFaults *replayMemoryFaults, _ error) {
				assertCalls(t, "ReadMemories", memoryFaults.readCalls.Load(), 0)
			},
		},
		{
			name:       "add memory",
			replayCase: memoryCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.cancelAdd = true
			},
			verify: func(t *testing.T, _ *replaySessionFaults, memoryFaults *replayMemoryFaults, _ error) {
				assertCalls(t, "AddMemory", memoryFaults.addCalls.Load(), 1)
				assertCalls(t, "ReadMemories", memoryFaults.readCalls.Load(), 0)
			},
		},
		{
			name:       "search memory",
			replayCase: memorySearchCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.cancelSearch = true
			},
			verify: func(t *testing.T, _ *replaySessionFaults, memoryFaults *replayMemoryFaults, _ error) {
				assertCalls(t, "SearchMemories", memoryFaults.searchCalls.Load(), 1)
				assertCalls(t, "ReadMemories", memoryFaults.readCalls.Load(), 0)
			},
		},
		{
			name:       "read memories",
			replayCase: memoryCase,
			memoryFaults: func(faults *replayMemoryFaults) {
				faults.cancelRead = true
				faults.poisonRead = true
			},
			verify: func(t *testing.T, _ *replaySessionFaults, _ *replayMemoryFaults, err error) {
				assertErrorOmits(t, err, "memory 0 is nil")
			},
		},
		{
			name:       "create summary",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelCreateSummary = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 0)
			},
		},
		{
			name:       "reload session",
			replayCase: reloadContractCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelGetMain = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 1)
			},
		},
		{
			name:       "create summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelCreateProbe = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "probe GetSession", sessionFaults.probeGetCalls.Load(), 0)
				assertCalls(t, "DeleteSession", sessionFaults.deleteSessionCalls.Load(), 0)
			},
		},
		{
			name:       "get summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelGetProbe = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "probe GetSession", sessionFaults.probeGetCalls.Load(), 1)
				assertCalls(t, "DeleteSession", sessionFaults.deleteSessionCalls.Load(), 0)
			},
		},
		{
			name:       "delete summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelDeleteSession = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 1)
				assertCalls(t, "DeleteSession", sessionFaults.deleteSessionCalls.Load(), 1)
			},
		},
		{
			name:       "append track",
			replayCase: trackCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelAppendTrack = true
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				assertCalls(t, "AppendTrackEvent", sessionFaults.appendTrackCalls.Load(), 1)
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 0)
			},
		},
		{
			name:       "append concurrent event",
			replayCase: concurrentCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.cancelEvent = logicalEventMatcher("branch-a-1")
				faults.observeEvent = logicalEventMatcher("branch-a-2")
			},
			verify: func(t *testing.T, sessionFaults *replaySessionFaults, _ *replayMemoryFaults, _ error) {
				if sessionFaults.targetAppendCalls.Load() == 0 {
					t.Fatal("concurrent AppendEvent was not reached")
				}
				assertCalls(t, "branch-a-2 AppendEvent", sessionFaults.observedEventCalls.Load(), 0)
				assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 0)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend, sessionFaults, memoryFaults := backendWithReplayFaults(
				InMemoryBackend(), nil, cancel, test,
			)
			_, err := Replay(ctx, test.replayCase(), backend)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Replay() error = %v, want context.Canceled", err)
			}
			if test.verify != nil {
				test.verify(t, sessionFaults, memoryFaults, err)
			}
		})
	}
}

func TestSessionReloadCasePreservesContinuity(t *testing.T) {
	backend, sessionFaults, _ := backendWithReplayFaults(
		InMemoryBackend(),
		nil,
		nil,
		replayOperationTest{},
	)
	snapshot, err := Replay(context.Background(), sessionReloadCase(), backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	assertCalls(t, "main GetSession", sessionFaults.mainGetCalls.Load(), 3)
	if len(snapshot.Events) != 2 {
		t.Fatalf("reload case events = %d, want 2", len(snapshot.Events))
	}
	phase, ok := snapshot.State["session"]["phase"].(CanonicalMap)
	if !ok || phase["kind"] != "json" || phase["json"] != "2" {
		t.Fatalf("reload case phase = %#v, want normalized JSON 2", snapshot.State["session"]["phase"])
	}
}

func TestReplayRejectsWrongSessionIdentity(t *testing.T) {
	tests := []replayOperationTest{
		{
			name:       "create main session",
			replayCase: singleTurnCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.wrongCreateMainIdentity = true
			},
		},
		{
			name:       "get main session",
			replayCase: reloadContractCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.wrongGetMainIdentity = true
			},
		},
		{
			name:       "create summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.wrongCreateProbeIdentity = true
			},
		},
		{
			name:       "get summary isolation probe",
			replayCase: summaryFilterKeyCase,
			sessionFaults: func(faults *replaySessionFaults) {
				faults.wrongGetProbeIdentity = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, _, _ := backendWithReplayFaults(InMemoryBackend(), nil, nil, test)
			_, err := Replay(context.Background(), test.replayCase(), backend)
			if err == nil || !strings.Contains(err.Error(), "backend returned session") {
				t.Fatalf("Replay() error = %v, want session identity rejection", err)
			}
		})
	}
}

type replayOperationTest struct {
	name          string
	replayCase    func() Case
	sessionFaults func(*replaySessionFaults)
	memoryFaults  func(*replayMemoryFaults)
	verify        func(*testing.T, *replaySessionFaults, *replayMemoryFaults, error)
}

func reloadContractCase() Case {
	return Case{
		Name:     "reload-contract",
		Requires: []Capability{CapabilitySession},
		Steps: []Step{{
			Name: "reload",
			Kind: StepReloadSession,
		}},
	}
}

func stateAndMemoryContractCase() Case {
	replayCase := stateCRUDCase()
	replayCase.Name = "state-memory-snapshot-contract"
	replayCase.Requires = append(replayCase.Requires, CapabilityMemory)
	return replayCase
}

func backendWithReplayFaults(
	backend Backend,
	failure error,
	cancel context.CancelFunc,
	test replayOperationTest,
) (Backend, *replaySessionFaults, *replayMemoryFaults) {
	sessionFaults := &replaySessionFaults{failure: failure, cancel: cancel}
	memoryFaults := &replayMemoryFaults{failure: failure, cancel: cancel}
	if test.sessionFaults != nil {
		test.sessionFaults(sessionFaults)
	}
	if test.memoryFaults != nil {
		test.memoryFaults(memoryFaults)
	}
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		sessionFaults.mainSessionID = caseName
		sessionFaults.Service = services.Session
		memoryFaults.Service = services.Memory
		services.Session = sessionFaults
		services.Memory = memoryFaults
		return services, nil
	}
	return backend, sessionFaults, memoryFaults
}

type replaySessionFaults struct {
	session.Service
	failure       error
	cancel        context.CancelFunc
	mainSessionID string

	mainGetCalls       atomic.Int64
	probeGetCalls      atomic.Int64
	listUserStateCalls atomic.Int64
	deleteSessionCalls atomic.Int64
	appendTrackCalls   atomic.Int64
	appendEventCalls   atomic.Int64
	targetAppendCalls  atomic.Int64
	observedEventCalls atomic.Int64

	failGetMain              bool
	failGetProbe             bool
	failUpdateAppState       bool
	failDeleteAppState       bool
	failUpdateUserState      bool
	failDeleteUserState      bool
	failUpdateSessionState   bool
	failListAppState         bool
	failListUserState        bool
	failCreateSummary        bool
	failCreateProbe          bool
	failDeleteSession        bool
	failAppendTrack          bool
	failEvent                func(*event.Event) bool
	cancelCreateMain         bool
	cancelGetMain            bool
	cancelGetProbe           bool
	cancelListAppState       bool
	cancelListUserState      bool
	cancelCreateSummary      bool
	cancelCreateProbe        bool
	cancelDeleteSession      bool
	cancelAppendTrack        bool
	cancelEvent              func(*event.Event) bool
	observeEvent             func(*event.Event) bool
	poisonGetMain            bool
	wrongCreateMainIdentity  bool
	wrongCreateProbeIdentity bool
	wrongGetMainIdentity     bool
	wrongGetProbeIdentity    bool
}

func (s *replaySessionFaults) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	probe := s.isSummaryIsolationProbe(key)
	if probe && s.failCreateProbe {
		return nil, s.failure
	}
	sess, err := s.Service.CreateSession(ctx, key, state, options...)
	if err == nil && sess != nil && ((!probe && s.wrongCreateMainIdentity) ||
		(probe && s.wrongCreateProbeIdentity)) {
		sess = wrongSessionIdentity(sess)
	}
	if err == nil && !probe && s.cancelCreateMain {
		s.cancel()
	}
	if err == nil && probe && s.cancelCreateProbe {
		s.cancel()
	}
	return sess, err
}

func (s *replaySessionFaults) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	probe := s.isSummaryIsolationProbe(key)
	if probe {
		s.probeGetCalls.Add(1)
		if s.failGetProbe {
			return nil, s.failure
		}
	} else {
		s.mainGetCalls.Add(1)
		if s.failGetMain {
			return nil, s.failure
		}
	}
	sess, err := s.Service.GetSession(ctx, key, options...)
	if err != nil {
		return sess, err
	}
	if sess != nil && ((!probe && s.wrongGetMainIdentity) ||
		(probe && s.wrongGetProbeIdentity)) {
		sess = wrongSessionIdentity(sess)
	}
	if probe && s.cancelGetProbe {
		s.cancel()
	}
	if !probe && s.cancelGetMain {
		if s.poisonGetMain && sess != nil {
			sess = sess.Clone()
			if len(sess.Events) > 0 {
				sess.Events[0].ID = ""
			}
		}
		s.cancel()
	}
	return sess, nil
}

func (s *replaySessionFaults) ListAppStates(
	ctx context.Context,
	appName string,
) (session.StateMap, error) {
	if s.failListAppState {
		return nil, s.failure
	}
	state, err := s.Service.ListAppStates(ctx, appName)
	if err == nil && s.cancelListAppState {
		s.cancel()
	}
	return state, err
}

func (s *replaySessionFaults) UpdateAppState(
	ctx context.Context,
	appName string,
	state session.StateMap,
) error {
	if s.failUpdateAppState {
		return s.failure
	}
	return s.Service.UpdateAppState(ctx, appName, state)
}

func (s *replaySessionFaults) DeleteAppState(
	ctx context.Context,
	appName string,
	key string,
) error {
	if s.failDeleteAppState {
		return s.failure
	}
	return s.Service.DeleteAppState(ctx, appName, key)
}

func (s *replaySessionFaults) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	if s.failUpdateUserState {
		return s.failure
	}
	return s.Service.UpdateUserState(ctx, userKey, state)
}

func (s *replaySessionFaults) DeleteUserState(
	ctx context.Context,
	userKey session.UserKey,
	key string,
) error {
	if s.failDeleteUserState {
		return s.failure
	}
	return s.Service.DeleteUserState(ctx, userKey, key)
}

func (s *replaySessionFaults) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if s.failUpdateSessionState {
		return s.failure
	}
	return s.Service.UpdateSessionState(ctx, key, state)
}

func (s *replaySessionFaults) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	s.listUserStateCalls.Add(1)
	if s.failListUserState {
		return nil, s.failure
	}
	state, err := s.Service.ListUserStates(ctx, userKey)
	if err == nil && s.cancelListUserState {
		s.cancel()
	}
	return state, err
}

func (s *replaySessionFaults) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if s.failCreateSummary {
		return s.failure
	}
	err := s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
	if err == nil && s.cancelCreateSummary {
		s.cancel()
	}
	return err
}

func (s *replaySessionFaults) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	s.deleteSessionCalls.Add(1)
	if s.failDeleteSession {
		return s.failure
	}
	err := s.Service.DeleteSession(ctx, key, options...)
	if err == nil && s.cancelDeleteSession {
		s.cancel()
	}
	return err
}

func (s *replaySessionFaults) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	s.appendEventCalls.Add(1)
	failTarget := s.failEvent != nil && s.failEvent(evt)
	cancelTarget := s.cancelEvent != nil && s.cancelEvent(evt)
	if s.observeEvent != nil && s.observeEvent(evt) {
		s.observedEventCalls.Add(1)
	}
	if failTarget || cancelTarget {
		s.targetAppendCalls.Add(1)
	}
	if failTarget {
		return s.failure
	}
	err := s.Service.AppendEvent(ctx, sess, evt, options...)
	if err == nil && cancelTarget {
		s.cancel()
	}
	return err
}

func (s *replaySessionFaults) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	evt *session.TrackEvent,
	options ...session.Option,
) error {
	s.appendTrackCalls.Add(1)
	if s.failAppendTrack {
		return s.failure
	}
	trackService, ok := s.Service.(session.TrackService)
	if !ok {
		return errors.New("wrapped session service does not implement session.TrackService")
	}
	err := trackService.AppendTrackEvent(ctx, sess, evt, options...)
	if err == nil && s.cancelAppendTrack {
		s.cancel()
	}
	return err
}

func (s *replaySessionFaults) isSummaryIsolationProbe(key session.Key) bool {
	return key.SessionID == s.mainSessionID+summaryIsolationSessionSuffix
}

func wrongSessionIdentity(sess *session.Session) *session.Session {
	output := sess.Clone()
	output.UserID += "-wrong"
	return output
}

func logicalEventMatcher(want string) func(*event.Event) bool {
	return func(evt *event.Event) bool {
		logicalID, ok, err := event.GetExtension[string](evt, logicalEventIDExtension)
		return err == nil && ok && logicalID == want
	}
}

type replayMemoryFaults struct {
	memory.Service
	failure error
	cancel  context.CancelFunc

	addCalls    atomic.Int64
	searchCalls atomic.Int64
	readCalls   atomic.Int64

	failAdd      bool
	failSearch   bool
	failRead     bool
	cancelAdd    bool
	cancelSearch bool
	cancelRead   bool
	poisonRead   bool
}

func (s *replayMemoryFaults) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	text string,
	topics []string,
	options ...memory.AddOption,
) error {
	s.addCalls.Add(1)
	if s.failAdd {
		return s.failure
	}
	err := s.Service.AddMemory(ctx, userKey, text, topics, options...)
	if err == nil && s.cancelAdd {
		s.cancel()
	}
	return err
}

func (s *replayMemoryFaults) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	options ...memory.SearchOption,
) ([]*memory.Entry, error) {
	s.searchCalls.Add(1)
	if s.failSearch {
		return nil, s.failure
	}
	entries, err := s.Service.SearchMemories(ctx, userKey, query, options...)
	if err == nil && s.cancelSearch {
		s.cancel()
	}
	return entries, err
}

func (s *replayMemoryFaults) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	s.readCalls.Add(1)
	if s.failRead {
		return nil, s.failure
	}
	entries, err := s.Service.ReadMemories(ctx, userKey, limit)
	if err == nil && s.cancelRead {
		if s.poisonRead {
			entries = []*memory.Entry{nil}
		}
		s.cancel()
	}
	return entries, err
}

func assertCalls(t *testing.T, operation string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s calls = %d, want %d", operation, got, want)
	}
}

func assertErrorOmits(t *testing.T, err error, forbidden string) {
	t.Helper()
	if strings.Contains(err.Error(), forbidden) {
		t.Fatalf("Replay() continued after cancellation: %v", err)
	}
}
