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

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRecoveryStateClear(t *testing.T) {
	tests := []struct {
		name      string
		mode      RecoveryMode
		initial   session.StateMap
		values    session.StateMap
		failAt    int
		after     bool
		wantErr   bool
		wantCalls int
	}{
		{
			name: "verify uncommitted clear", mode: RecoveryVerify,
			initial: session.StateMap{"stale": []byte("1")},
			failAt:  1, wantErr: true, wantCalls: 1,
		},
		{
			name: "retry uncommitted clear", mode: RecoveryRetryIdempotent,
			initial: session.StateMap{"stale": []byte("1")},
			failAt:  1, wantCalls: 2,
		},
		{
			name: "verify committed clear", mode: RecoveryVerify,
			initial: session.StateMap{"stale": []byte("1")},
			failAt:  1, after: true, wantCalls: 1,
		},
		{
			name: "do not retry committed clear", mode: RecoveryRetryIdempotent,
			initial: session.StateMap{"stale": []byte("1")},
			failAt:  1, after: true, wantCalls: 1,
		},
		{
			name: "verify partial clear", mode: RecoveryVerify,
			initial: session.StateMap{"first": []byte("1"), "second": []byte("2")},
			failAt:  2, wantErr: true, wantCalls: 2,
		},
		{
			name: "retry partial clear", mode: RecoveryRetryIdempotent,
			initial: session.StateMap{"first": []byte("1"), "second": []byte("2")},
			failAt:  2, wantCalls: 3,
		},
		{
			name:    "clear includes new values",
			initial: session.StateMap{"stale": []byte("1")},
			values:  session.StateMap{"new": []byte("2")}, wantCalls: 2,
		},
		{
			name: "verify clear after adding values", mode: RecoveryVerify,
			values: session.StateMap{"new": []byte("2")},
			failAt: 1, after: true, wantCalls: 1,
		},
	}
	for _, scope := range []StateScope{StateScopeApp, StateScopeUser} {
		capability := CapabilityAppState
		if scope == StateScopeUser {
			capability = CapabilityUserState
		}
		for _, test := range tests {
			t.Run(string(scope)+"/"+test.name, func(t *testing.T) {
				var service *stateClearErrorService
				backend := InMemoryBackend()
				open := backend.Open
				backend.Open = func(ctx context.Context, name string) (*Services, error) {
					services, err := open(ctx, name)
					if err == nil {
						service = &stateClearErrorService{
							Service: services.Session, failAt: test.failAt, after: test.after,
						}
						services.Session = service
					}
					return services, err
				}
				var steps []Step
				if len(test.initial) > 0 {
					steps = append(steps, stateStep("seed", scope, test.initial, nil))
				}
				steps = append(steps, Step{
					Name: "clear", Kind: StepUpdateState, Recovery: test.mode,
					State: &StateInput{Scope: scope, Clear: true, Values: test.values},
				})
				snapshot, err := Replay(context.Background(), Case{
					Name: "recover-state-clear", Requires: []Capability{CapabilitySession, capability},
					Steps: steps,
				}, backend)
				if test.wantErr {
					if !errors.Is(err, ErrUncertainCommit) {
						t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
					}
				} else {
					if err != nil {
						t.Fatalf("Replay() error = %v", err)
					}
					if state := snapshot.State[string(scope)]; len(state) != 0 {
						t.Fatalf("cleared state = %#v, want empty scope", state)
					}
				}
				if service == nil || service.calls != test.wantCalls {
					t.Fatalf("delete service = %#v, want %d calls", service, test.wantCalls)
				}
			})
		}
	}
}

func TestCaseValidationRejectsConcurrentStateClear(t *testing.T) {
	for _, scope := range []StateScope{StateScopeApp, StateScopeUser} {
		clearStep := Step{Name: "clear", Kind: StepUpdateState, State: &StateInput{Scope: scope, Clear: true}}
		writeStep := stateStep("write", scope, session.StateMap{"key": []byte("value")}, nil)
		for _, other := range []Step{writeStep, {Name: "other-clear", Kind: StepUpdateState, State: clearStep.State}} {
			for _, reverse := range []bool{false, true} {
				branches := [][]Step{{clearStep}, {other}}
				if reverse {
					branches[0], branches[1] = branches[1], branches[0]
				}
				err := validateCase(Case{
					Name: "concurrent-clear",
					Requires: []Capability{
						CapabilitySession, CapabilityAppState, CapabilityUserState,
						CapabilityConcurrent, CapabilityConcurrentState,
					},
					Steps: []Step{{Name: "concurrent", Kind: StepConcurrent, Concurrent: branches}},
				})
				if err == nil || !strings.Contains(err.Error(), "cannot concurrently clear") {
					t.Fatalf("scope=%s other=%s reverse=%v: error = %v, want concurrent clear rejection", scope, other.Name, reverse, err)
				}
			}
		}
	}
}

type stateClearErrorService struct {
	session.Service
	failAt int
	after  bool
	calls  int
}

func (s *stateClearErrorService) DeleteAppState(ctx context.Context, appName, key string) error {
	return s.deleteState(func() error { return s.Service.DeleteAppState(ctx, appName, key) })
}

func (s *stateClearErrorService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return s.deleteState(func() error { return s.Service.DeleteUserState(ctx, userKey, key) })
}

func (s *stateClearErrorService) deleteState(remove func() error) error {
	s.calls++
	if s.calls == s.failAt && !s.after {
		return errors.New("injected state clear failure")
	}
	if err := remove(); err != nil {
		return err
	}
	if s.calls == s.failAt && s.after {
		return errors.New("injected committed state clear failure")
	}
	return nil
}
