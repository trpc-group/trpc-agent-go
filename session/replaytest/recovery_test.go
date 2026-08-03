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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRecoveryVerifiesCommittedWrites(t *testing.T) {
	tests := []struct {
		name   string
		build  func() Case
		setup  func(*committedErrorSessionService)
		assert func(*testing.T, Snapshot)
	}{
		{
			name: "event",
			build: func() Case {
				step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
				step.Recovery = RecoveryVerify
				return Case{Name: "recover-event", Requires: []Capability{CapabilitySession}, Steps: []Step{step}}
			},
			setup: func(service *committedErrorSessionService) { service.failAfterAppendEvent = true },
			assert: func(t *testing.T, snapshot Snapshot) {
				if len(snapshot.Events) != 1 {
					t.Fatalf("events = %d, want 1", len(snapshot.Events))
				}
			},
		},
		{
			name: "state",
			build: func() Case {
				step := stateStep("state", StateScopeSession, session.StateMap{"key": []byte("value")}, nil)
				step.Recovery = RecoveryVerify
				return Case{
					Name:     "recover-state",
					Requires: []Capability{CapabilitySession, CapabilitySessionState},
					Steps:    []Step{step},
				}
			},
			setup: func(service *committedErrorSessionService) { service.failAfterUpdateSessionState = true },
			assert: func(t *testing.T, snapshot Snapshot) {
				if _, ok := snapshot.State["session"]["key"]; !ok {
					t.Fatalf("state key missing: %#v", snapshot.State["session"])
				}
			},
		},
		{
			name: "summary",
			build: func() Case {
				step := Step{
					Name:     "summary",
					Kind:     StepCreateSummary,
					Recovery: RecoveryVerify,
					Summary:  &SummaryInput{Force: true},
				}
				return Case{
					Name:     "recover-summary",
					Requires: []Capability{CapabilitySession, CapabilitySummary},
					Steps: []Step{
						messageStep("user", "user", 1, "user", model.RoleUser, "hello", ""),
						step,
					},
				}
			},
			setup: func(service *committedErrorSessionService) { service.failAfterCreateSummary = true },
			assert: func(t *testing.T, snapshot Snapshot) {
				if _, ok := snapshot.Summaries[""]; !ok {
					t.Fatalf("full-session summary missing: %#v", snapshot.Summaries)
				}
			},
		},
		{
			name: "track",
			build: func() Case {
				step := trackStep("track", "tools", 1, map[string]any{"status": "ok"})
				step.Recovery = RecoveryVerify
				return Case{
					Name:     "recover-track",
					Requires: []Capability{CapabilitySession, CapabilityTrack},
					Steps:    []Step{step},
				}
			},
			setup: func(service *committedErrorSessionService) { service.failAfterAppendTrack = true },
			assert: func(t *testing.T, snapshot Snapshot) {
				if len(snapshot.Tracks["tools"]) != 1 {
					t.Fatalf("track events = %#v, want one", snapshot.Tracks["tools"])
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := committedErrorBackend(test.setup, nil)
			snapshot, err := Replay(context.Background(), test.build(), backend)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			test.assert(t, snapshot)
		})
	}
}

func TestRecoveryRetriesIdempotentMemoryWrite(t *testing.T) {
	backend := committedErrorBackend(nil, func(service *committedErrorMemoryService) {
		service.failBeforeAddMemory = true
	})
	replayCase := Case{
		Name:     "retry-memory",
		Requires: []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{{
			Name:     "memory",
			Kind:     StepAddMemory,
			Recovery: RecoveryRetryIdempotent,
			Memory:   &MemoryInput{Memory: "remember me", Topics: []string{"recovery"}},
		}},
	}
	snapshot, err := Replay(context.Background(), replayCase, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(snapshot.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(snapshot.Memories))
	}
}

func TestRecoveryVerifiesCommittedMemoryMetadata(t *testing.T) {
	eventTime := time.Date(2026, time.August, 2, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	backend := committedErrorBackend(nil, func(service *committedErrorMemoryService) {
		service.failAfterAddMemory = true
	})
	snapshot, err := Replay(context.Background(), Case{
		Name:     "verify-memory-metadata",
		Requires: []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{{
			Name:     "memory",
			Kind:     StepAddMemory,
			Recovery: RecoveryVerify,
			Memory: &MemoryInput{
				Memory: "remember the review",
				Topics: []string{"recovery", "review"},
				Metadata: &memory.Metadata{
					Kind:         memory.KindEpisode,
					EventTime:    &eventTime,
					Participants: []string{" Reviewer ", "user", "reviewer", ""},
					Location:     " remote ",
				},
			},
		}},
	}, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(snapshot.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(snapshot.Memories))
	}
}

func TestRecoveryRetriesIdempotentStateWrite(t *testing.T) {
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.failBeforeUpdateSessionState = true
	}, nil)
	step := stateStep("state", StateScopeSession, session.StateMap{"key": []byte("value")}, nil)
	step.Recovery = RecoveryRetryIdempotent
	snapshot, err := Replay(context.Background(), Case{
		Name:     "retry-state",
		Requires: []Capability{CapabilitySession, CapabilitySessionState},
		Steps:    []Step{step},
	}, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if _, ok := snapshot.State["session"]["key"]; !ok {
		t.Fatalf("state key missing after retry: %#v", snapshot.State["session"])
	}
}

func TestRecoveryDoesNotRetryObservedMemoryCommit(t *testing.T) {
	var memoryService *committedErrorMemoryService
	backend := committedErrorBackend(nil, func(service *committedErrorMemoryService) {
		memoryService = service
		service.failAfterAddMemory = true
	})
	snapshot, err := Replay(context.Background(), Case{
		Name:     "verify-memory",
		Requires: []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{{
			Name:     "memory",
			Kind:     StepAddMemory,
			Recovery: RecoveryRetryIdempotent,
			Memory:   &MemoryInput{Memory: "remember once"},
		}},
	}, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if memoryService == nil {
		t.Fatal("memory service was not opened")
	}
	if memoryService.addMemoryCalls != 1 {
		t.Fatalf("AddMemory calls = %d, want 1", memoryService.addMemoryCalls)
	}
	if len(snapshot.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(snapshot.Memories))
	}
}

func TestRecoveryVerifiesCommitAfterRetryError(t *testing.T) {
	var memoryService *committedErrorMemoryService
	backend := committedErrorBackend(nil, func(service *committedErrorMemoryService) {
		memoryService = service
		service.failBeforeAddMemory = true
		service.failAfterAddMemory = true
	})
	snapshot, err := Replay(context.Background(), Case{
		Name:     "verify-retry-memory",
		Requires: []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{{
			Name:     "memory",
			Kind:     StepAddMemory,
			Recovery: RecoveryRetryIdempotent,
			Memory:   &MemoryInput{Memory: "remember after retry"},
		}},
	}, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if memoryService == nil {
		t.Fatal("memory service was not opened")
	}
	if memoryService.addMemoryCalls != 2 {
		t.Fatalf("AddMemory calls = %d, want 2", memoryService.addMemoryCalls)
	}
	if len(snapshot.Memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(snapshot.Memories))
	}
}

func TestRecoveryVerifiesCommittedNilTrackPayload(t *testing.T) {
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.failAfterAppendTrack = true
	}, nil)
	_, err := Replay(context.Background(), Case{
		Name:     "verify-nil-track-payload",
		Requires: []Capability{CapabilitySession, CapabilityTrack},
		Steps: []Step{{
			Name:     "track",
			Kind:     StepAppendTrack,
			Recovery: RecoveryVerify,
			Track: &TrackInput{Event: &session.TrackEvent{
				Track: session.Track("tools"),
			}},
		}},
	}, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
}

func TestRecoveryReportsUnobservedNonIdempotentWrite(t *testing.T) {
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.failBeforeAppendEvent = true
	}, nil)
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Recovery = RecoveryVerify
	_, err := Replay(context.Background(), Case{
		Name:     "uncertain-event",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}, backend)
	if !errors.Is(err, ErrUncertainCommit) {
		t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
	}
}

func TestRecoveryRejectsCorruptedCommittedEvent(t *testing.T) {
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.corruptAppendEvent = true
		service.failAfterAppendEvent = true
	}, nil)
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Recovery = RecoveryVerify
	_, err := Replay(context.Background(), Case{
		Name:     "corrupt-committed-event",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}, backend)
	if !errors.Is(err, ErrUncertainCommit) {
		t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
	}
}

func TestRecoveryRejectsWrongMemoryOwnership(t *testing.T) {
	backend := committedErrorBackend(nil, func(service *committedErrorMemoryService) {
		service.failAfterAddMemory = true
		service.returnWrongOwnership = true
	})
	_, err := Replay(context.Background(), Case{
		Name:     "wrong-memory-ownership",
		Requires: []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{{
			Name:     "memory",
			Kind:     StepAddMemory,
			Recovery: RecoveryVerify,
			Memory:   &MemoryInput{Memory: "remember ownership"},
		}},
	}, backend)
	if !errors.Is(err, ErrUncertainCommit) {
		t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
	}
}

func TestRecoveryReportsVerificationFailureAsUncertain(t *testing.T) {
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.failBeforeAppendEvent = true
		service.failGetSessionCall = 2
	}, nil)
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Recovery = RecoveryVerify
	_, err := Replay(context.Background(), Case{
		Name:     "failed-recovery-read",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}, backend)
	if !errors.Is(err, ErrUncertainCommit) {
		t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
	}
	if !strings.Contains(err.Error(), "injected recovery read failure") {
		t.Fatalf("Replay() error = %v, want recovery read failure", err)
	}
}

func TestRecoveryPreservesCancellationAsUncertain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := committedErrorBackend(func(service *committedErrorSessionService) {
		service.cancelOnAppendEvent = cancel
	}, nil)
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Recovery = RecoveryVerify
	_, err := Replay(ctx, Case{
		Name:     "canceled-recovery",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}, backend)
	if !errors.Is(err, ErrUncertainCommit) {
		t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want context.Canceled", err)
	}
}

func TestStateRecoveryPostconditions(t *testing.T) {
	ctx := context.Background()
	services, err := InMemoryBackend().Open(ctx, "state-recovery-postconditions")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := services.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	key := session.Key{AppName: "replaytest", UserID: "user-1", SessionID: "state-recovery-postconditions"}
	sess, err := services.Session.CreateSession(ctx, key, session.StateMap{
		"json-null": []byte("null"),
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := services.Session.UpdateAppState(ctx, key.AppName, session.StateMap{"empty": {}, "present": []byte("1")}); err != nil {
		t.Fatalf("UpdateAppState() error = %v", err)
	}
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	if err := services.Session.UpdateUserState(ctx, userKey, session.StateMap{"locale": []byte(`"en-US"`)}); err != nil {
		t.Fatalf("UpdateUserState() error = %v", err)
	}
	exec := execution{services: services, key: key, session: sess}
	tests := []struct {
		name    string
		input   *StateInput
		matches bool
		wantErr bool
	}{
		{
			name:    "app empty",
			input:   &StateInput{Scope: StateScopeApp, Values: session.StateMap{"empty": {}}},
			matches: true,
		},
		{
			name:  "app nil differs from empty",
			input: &StateInput{Scope: StateScopeApp, Values: session.StateMap{"empty": nil}},
		},
		{
			name:    "app absent delete",
			input:   &StateInput{Scope: StateScopeApp, DeleteKeys: []string{"missing"}},
			matches: true,
		},
		{
			name: "app delete wins over update",
			input: &StateInput{
				Scope:      StateScopeApp,
				Values:     session.StateMap{"missing": []byte("value")},
				DeleteKeys: []string{"missing", "missing"},
			},
			matches: true,
		},
		{
			name:  "app present delete",
			input: &StateInput{Scope: StateScopeApp, DeleteKeys: []string{"present"}},
		},
		{
			name:    "user value",
			input:   &StateInput{Scope: StateScopeUser, Values: session.StateMap{"locale": []byte(`"en-US"`)}},
			matches: true,
		},
		{
			name:  "session nil differs from json null",
			input: &StateInput{Scope: StateScopeSession, Values: session.StateMap{"json-null": nil}},
		},
		{
			name:    "unknown scope",
			input:   &StateInput{Scope: StateScope("unknown")},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches, err := exec.stateWriteMatches(ctx, test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("stateWriteMatches() error = %v, wantErr %v", err, test.wantErr)
			}
			if matches != test.matches {
				t.Fatalf("stateWriteMatches() = %v, want %v", matches, test.matches)
			}
		})
	}
}

func TestRecoveryValidationRejectsInvalidModes(t *testing.T) {
	unknown := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	unknown.Recovery = RecoveryMode("future")
	retryEvent := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	retryEvent.Recovery = RecoveryRetryIdempotent
	nonPersistedEvent := messageStep("non-persisted", "non-persisted", 1, "user", model.RoleUser, "hello", "")
	nonPersistedEvent.Event.Event.Response = nil
	nonPersistedEvent.Recovery = RecoveryVerify
	statefulEvent := messageStep("stateful", "stateful", 1, "user", model.RoleUser, "hello", "")
	statefulEvent.Event.Event.StateDelta = session.StateMap{"key": []byte("value")}
	statefulEvent.Recovery = RecoveryVerify
	tests := []struct {
		name string
		step Step
		want string
	}{
		{name: "unknown", step: unknown, want: "unknown recovery mode"},
		{
			name: "verify read",
			step: Step{
				Name:         "search",
				Kind:         StepSearchMemory,
				Recovery:     RecoveryVerify,
				MemorySearch: &MemorySearchInput{Query: "query"},
			},
			want: "cannot verify recovery",
		},
		{name: "retry append", step: retryEvent, want: "cannot idempotently retry"},
		{name: "non-persisted event", step: nonPersistedEvent, want: "persisted events without state delta"},
		{name: "stateful event", step: statefulEvent, want: "persisted events without state delta"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCase(Case{
				Name:     "invalid-recovery",
				Requires: []Capability{CapabilitySession, CapabilityMemory, CapabilityMemorySearch},
				Steps:    []Step{test.step},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCase() error = %v, want %q", err, test.want)
			}
		})
	}
}

type committedErrorSessionService struct {
	session.Service
	cancelOnAppendEvent          context.CancelFunc
	failBeforeAppendEvent        bool
	failAfterAppendEvent         bool
	corruptAppendEvent           bool
	failBeforeUpdateSessionState bool
	failAfterUpdateSessionState  bool
	failAfterCreateSummary       bool
	failAfterAppendTrack         bool
	failGetSessionCall           int
	getSessionCalls              int
}

func (s *committedErrorSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	s.getSessionCalls++
	if s.getSessionCalls == s.failGetSessionCall {
		return nil, errors.New("injected recovery read failure")
	}
	return s.Service.GetSession(ctx, key, options...)
}

func (s *committedErrorSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if s.cancelOnAppendEvent != nil {
		s.cancelOnAppendEvent()
		s.cancelOnAppendEvent = nil
		return errors.New("injected canceled event failure")
	}
	if s.failBeforeAppendEvent {
		s.failBeforeAppendEvent = false
		return errors.New("injected pre-commit event failure")
	}
	if s.corruptAppendEvent {
		s.corruptAppendEvent = false
		evt = evt.Clone()
		evt.Author += "-corrupted"
	}
	if err := s.Service.AppendEvent(ctx, sess, evt, options...); err != nil {
		return err
	}
	if s.failAfterAppendEvent {
		s.failAfterAppendEvent = false
		return errors.New("injected committed event failure")
	}
	return nil
}

func (s *committedErrorSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if s.failBeforeUpdateSessionState {
		s.failBeforeUpdateSessionState = false
		return errors.New("injected pre-commit state failure")
	}
	if err := s.Service.UpdateSessionState(ctx, key, state); err != nil {
		return err
	}
	if s.failAfterUpdateSessionState {
		s.failAfterUpdateSessionState = false
		return errors.New("injected committed state failure")
	}
	return nil
}

func (s *committedErrorSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if err := s.Service.CreateSessionSummary(ctx, sess, filterKey, force); err != nil {
		return err
	}
	if s.failAfterCreateSummary {
		s.failAfterCreateSummary = false
		return errors.New("injected committed summary failure")
	}
	return nil
}

func (s *committedErrorSessionService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	evt *session.TrackEvent,
	options ...session.Option,
) error {
	trackService, ok := s.Service.(session.TrackService)
	if !ok {
		return errors.New("wrapped service does not implement session.TrackService")
	}
	if err := trackService.AppendTrackEvent(ctx, sess, evt, options...); err != nil {
		return err
	}
	if s.failAfterAppendTrack {
		s.failAfterAppendTrack = false
		return errors.New("injected committed track failure")
	}
	return nil
}

type committedErrorMemoryService struct {
	memory.Service
	failBeforeAddMemory  bool
	failAfterAddMemory   bool
	returnWrongOwnership bool
	addMemoryCalls       int
}

func (s *committedErrorMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.Service.ReadMemories(ctx, userKey, limit)
	if err != nil || !s.returnWrongOwnership {
		return entries, err
	}
	entries = cloneMemoryEntries(entries)
	for _, entry := range entries {
		if entry != nil {
			entry.UserID += "-wrong"
		}
	}
	return entries, nil
}

func (s *committedErrorMemoryService) AddMemory(
	ctx context.Context,
	key memory.UserKey,
	value string,
	topics []string,
	options ...memory.AddOption,
) error {
	s.addMemoryCalls++
	if s.failBeforeAddMemory {
		s.failBeforeAddMemory = false
		return errors.New("injected pre-commit memory failure")
	}
	if err := s.Service.AddMemory(ctx, key, value, topics, options...); err != nil {
		return err
	}
	if s.failAfterAddMemory {
		s.failAfterAddMemory = false
		return errors.New("injected committed memory failure")
	}
	return nil
}

func committedErrorBackend(
	setupSession func(*committedErrorSessionService),
	setupMemory func(*committedErrorMemoryService),
) Backend {
	backend := InMemoryBackend()
	backend.Name = "recovery"
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		sessionService := &committedErrorSessionService{Service: services.Session}
		memoryService := &committedErrorMemoryService{Service: services.Memory}
		if setupSession != nil {
			setupSession(sessionService)
		}
		if setupMemory != nil {
			setupMemory(memoryService)
		}
		services.Session = sessionService
		services.Memory = memoryService
		return services, nil
	}
	return backend
}
