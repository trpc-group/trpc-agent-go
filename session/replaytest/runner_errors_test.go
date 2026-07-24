//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// stubTarget is a configurable Target for exercising RunCase guard rails.
type stubTarget struct {
	name     string
	caps     replaytest.Capability
	sess     session.Service
	mem      memory.Service
	resetErr error
}

func (s *stubTarget) Name() string                    { return s.name }
func (s *stubTarget) Caps() replaytest.Capability     { return s.caps }
func (s *stubTarget) SessionService() session.Service { return s.sess }
func (s *stubTarget) MemoryService() memory.Service   { return s.mem }
func (s *stubTarget) Reset(context.Context) error     { return s.resetErr }
func (s *stubTarget) Close() error                    { return nil }

// simpleSessionCase returns a minimal passing session case.
func simpleSessionCase() replaytest.Case {
	return replaytest.Case{
		Name:     "basic/one",
		NeedCaps: replaytest.Capability{Session: true},
		Steps: []replaytest.Step{
			{Op: replaytest.OpCreateSession, SessionID: "s1"},
			{Op: replaytest.OpAppendEvent, SessionID: "s1", Event: &replaytest.EventSpec{
				Author: "user", Role: "user", Content: "hi", InvocationID: "inv-1",
			}},
		},
	}
}

// TestRunCaseResetError covers the reset failure guard.
func TestRunCaseResetError(t *testing.T) {
	tgt := &stubTarget{name: "stub", resetErr: errors.New("reset boom")}
	_, err := replaytest.NewRunner().RunCase(
		context.Background(), simpleSessionCase(), tgt)
	assert.ErrorContains(t, err, "reset target stub")
	assert.ErrorContains(t, err, "reset boom")
}

// TestRunCaseNilServiceBehindClaim covers the guards against a target that
// claims a capability but exposes a nil service.
func TestRunCaseNilServiceBehindClaim(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	t.Run("session service nil", func(t *testing.T) {
		tgt := &stubTarget{name: "stub", caps: replaytest.CapAll}
		_, err := replaytest.NewRunner().RunCase(
			context.Background(), simpleSessionCase(), tgt)
		assert.ErrorContains(t, err, "claims session capability")
	})

	t.Run("memory service nil", func(t *testing.T) {
		tgt := &stubTarget{
			name: "stub",
			caps: replaytest.CapAll,
			sess: ref.SessionService(),
		}
		c := replaytest.Case{
			Name:     "memory/one",
			NeedCaps: replaytest.Capability{Memory: true},
			Steps: []replaytest.Step{
				{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{Content: "x"}},
			},
		}
		_, err := replaytest.NewRunner().RunCase(context.Background(), c, tgt)
		assert.ErrorContains(t, err, "claims memory capability")
	})
}

// TestRunCaseInvalidFloatDelta covers the negative/NaN/Inf delta guards.
func TestRunCaseInvalidFloatDelta(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	for _, delta := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		c := simpleSessionCase()
		c.FloatDelta = delta
		_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
		assert.ErrorContains(t, err, "float delta must be finite and non-negative",
			"delta=%v", delta)
	}
}

// TestRunCaseUnknownOp covers the default branch of the step dispatcher.
func TestRunCaseUnknownOp(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	c := replaytest.Case{
		Name:  "bogus/op",
		Steps: []replaytest.Step{{Op: "bogus_op", SessionID: "s1"}},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
	assert.ErrorContains(t, err, `unknown op "bogus_op"`)
}

// TestRunCaseSummaryOnUnknownSession covers applySummary's unknown-session
// error path.
func TestRunCaseSummaryOnUnknownSession(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	c := replaytest.Case{
		Name:     "summary/ghost",
		NeedCaps: replaytest.Capability{Summary: true},
		Steps: []replaytest.Step{
			{Op: replaytest.OpSummary, SessionID: "ghost",
				Summary: &replaytest.SummarySpec{FilterKey: "fk"}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
	assert.ErrorContains(t, err, `summary on unknown session "ghost"`)
}

// TestRunCaseTrackOnUnknownSession covers applyTrack's unknown-session
// error path.
func TestRunCaseTrackOnUnknownSession(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	c := replaytest.Case{
		Name: "track/ghost",
		Steps: []replaytest.Step{
			{Op: replaytest.OpAppendTrack, SessionID: "ghost",
				Track: &replaytest.TrackSpec{Track: "tr", Payload: `{"x":1}`}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
	assert.ErrorContains(t, err, `track on unknown session "ghost"`)
}

// TestRunCaseConcurrentOnUnknownSession covers applyConcurrent's
// unknown-session error path.
func TestRunCaseConcurrentOnUnknownSession(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	c := replaytest.Case{
		Name: "concurrent/ghost",
		Steps: []replaytest.Step{
			{Op: replaytest.OpConcurrentEvents, SessionID: "ghost",
				Concurrent: []replaytest.WriterSpec{{Branch: "b", Count: 1}}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
	assert.ErrorContains(t, err, `concurrent append on unknown session "ghost"`)
}

// TestRunCaseMemoryMatchMiss covers the findMemoryID not-found path reached
// through the runner.
func TestRunCaseMemoryMatchMiss(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	c := replaytest.Case{
		Name:     "memory/miss",
		NeedCaps: replaytest.Capability{Memory: true},
		Steps: []replaytest.Step{
			{Op: replaytest.OpUpdateMemory, Memory: &replaytest.MemorySpec{
				MatchContent: "ghost", Content: "new",
			}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, ref)
	assert.ErrorContains(t, err, `memory "ghost" not found`)
}

// TestRunPairWithReportPath covers WithReportPath and the report write.
func TestRunPairWithReportPath(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("cand")
	defer cand.Close()

	path := filepath.Join(t.TempDir(), "report.json")
	rep, err := replaytest.RunPair(context.Background(),
		[]replaytest.Case{simpleSessionCase()}, ref, cand,
		replaytest.WithReportPath(path))
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Totals.Pass)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var decoded replaytest.Report
	require.NoError(t, json.Unmarshal(b, &decoded))
	assert.Equal(t, 1, decoded.Totals.Pass)
}

// TestRunPairReportWriteError covers the report write failure: the report
// is still returned along with the error.
func TestRunPairReportWriteError(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("cand")
	defer cand.Close()

	bad := filepath.Join(t.TempDir(), "no-such-dir", "report.json")
	rep, err := replaytest.RunPair(context.Background(),
		[]replaytest.Case{simpleSessionCase()}, ref, cand,
		replaytest.WithReportPath(bad))
	require.Error(t, err)
	assert.ErrorContains(t, err, "write report")
	require.NotNil(t, rep)
	assert.Equal(t, 1, rep.Totals.Pass)
}

// TestWriteReportInvalidPath covers WriteReport's write failure path.
func TestWriteReportInvalidPath(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "no-such-dir", "report.json")
	err := replaytest.WriteReport(bad, &replaytest.Report{})
	assert.Error(t, err)
}

// TestRunPairTPass covers the testing.T runner on a passing pair.
func TestRunPairTPass(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("cand")
	defer cand.Close()

	rep := replaytest.RunPairT(t,
		[]replaytest.Case{simpleSessionCase()}, ref, cand)
	require.NotNil(t, rep)
	assert.Equal(t, 1, rep.Totals.Total)
	assert.Equal(t, 1, rep.Totals.Pass)
	assert.Equal(t, 0, rep.Totals.Fail)
}

// sessionServiceStub delegates to a real session.Service with fault
// injection hooks. It deliberately does not implement session.TrackService.
type sessionServiceStub struct {
	sess               session.Service
	createErr          error
	createNil          bool
	getErr             error
	getNil             bool
	getLeak            bool
	deleteErr          error
	appendErr          error
	deleteAppStateErr  error
	deleteUserStateErr error
}

func (s *sessionServiceStub) CreateSession(
	ctx context.Context, key session.Key, state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createNil {
		return nil, nil
	}
	return s.sess.CreateSession(ctx, key, state, opts...)
}

func (s *sessionServiceStub) GetSession(
	ctx context.Context, key session.Key, opts ...session.Option,
) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.getNil {
		return nil, nil
	}
	sess, err := s.sess.GetSession(ctx, key, opts...)
	if err != nil || sess == nil {
		return sess, err
	}
	if s.getLeak {
		sess.SummariesMu.Lock()
		if sess.Summaries == nil {
			sess.Summaries = map[string]*session.Summary{}
		}
		sess.Summaries["fk"] = &session.Summary{Summary: "leaked"}
		sess.SummariesMu.Unlock()
	}
	return sess, nil
}

func (s *sessionServiceStub) ListSessions(
	ctx context.Context, userKey session.UserKey, opts ...session.Option,
) ([]*session.Session, error) {
	return s.sess.ListSessions(ctx, userKey, opts...)
}

func (s *sessionServiceStub) DeleteSession(
	ctx context.Context, key session.Key, opts ...session.Option,
) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.sess.DeleteSession(ctx, key, opts...)
}

func (s *sessionServiceStub) UpdateAppState(
	ctx context.Context, appName string, state session.StateMap,
) error {
	return s.sess.UpdateAppState(ctx, appName, state)
}

func (s *sessionServiceStub) DeleteAppState(
	ctx context.Context, appName string, key string,
) error {
	if s.deleteAppStateErr != nil {
		return s.deleteAppStateErr
	}
	return s.sess.DeleteAppState(ctx, appName, key)
}

func (s *sessionServiceStub) ListAppStates(
	ctx context.Context, appName string,
) (session.StateMap, error) {
	return s.sess.ListAppStates(ctx, appName)
}

func (s *sessionServiceStub) UpdateUserState(
	ctx context.Context, userKey session.UserKey, state session.StateMap,
) error {
	return s.sess.UpdateUserState(ctx, userKey, state)
}

func (s *sessionServiceStub) ListUserStates(
	ctx context.Context, userKey session.UserKey,
) (session.StateMap, error) {
	return s.sess.ListUserStates(ctx, userKey)
}

func (s *sessionServiceStub) DeleteUserState(
	ctx context.Context, userKey session.UserKey, key string,
) error {
	if s.deleteUserStateErr != nil {
		return s.deleteUserStateErr
	}
	return s.sess.DeleteUserState(ctx, userKey, key)
}

func (s *sessionServiceStub) UpdateSessionState(
	ctx context.Context, key session.Key, state session.StateMap,
) error {
	return s.sess.UpdateSessionState(ctx, key, state)
}

func (s *sessionServiceStub) AppendEvent(
	ctx context.Context, sess *session.Session, evt *event.Event,
	opts ...session.Option,
) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	return s.sess.AppendEvent(ctx, sess, evt, opts...)
}

func (s *sessionServiceStub) CreateSessionSummary(
	ctx context.Context, sess *session.Session, filterKey string, force bool,
) error {
	return s.sess.CreateSessionSummary(ctx, sess, filterKey, force)
}

func (s *sessionServiceStub) EnqueueSummaryJob(
	ctx context.Context, sess *session.Session, filterKey string, force bool,
) error {
	return s.sess.EnqueueSummaryJob(ctx, sess, filterKey, force)
}

func (s *sessionServiceStub) GetSessionSummaryText(
	ctx context.Context, sess *session.Session, opts ...session.SummaryOption,
) (string, bool) {
	return s.sess.GetSessionSummaryText(ctx, sess, opts...)
}

func (s *sessionServiceStub) Close() error { return s.sess.Close() }

// summaryProbeCase returns a zero-step summary case: RunCase reaches
// verifySummaryIsolation immediately after the (empty) step loop.
func summaryProbeCase() replaytest.Case {
	return replaytest.Case{
		Name:     "summary/probe",
		NeedCaps: replaytest.Capability{Summary: true},
	}
}

// TestRunCaseSummaryIsolationProbeFailures covers every error branch of
// verifySummaryIsolation through fault injection at the service boundary.
func TestRunCaseSummaryIsolationProbeFailures(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	tests := []struct {
		name    string
		mutate  func(*sessionServiceStub)
		wantErr string
	}{
		{"create error", func(s *sessionServiceStub) {
			s.createErr = errors.New("create boom")
		}, "create summary-isolation probe session: create boom"},
		{"create nil", func(s *sessionServiceStub) {
			s.createNil = true
		}, "backend returned nil session"},
		{"get error", func(s *sessionServiceStub) {
			s.getErr = errors.New("get boom")
		}, "get summary-isolation probe session: get boom"},
		{"get nil", func(s *sessionServiceStub) {
			s.getNil = true
		}, "get summary-isolation probe session: backend returned nil session"},
		{"leaked summary", func(s *sessionServiceStub) {
			s.getLeak = true
		}, "cross-session summary leak"},
		{"delete error", func(s *sessionServiceStub) {
			s.deleteErr = errors.New("delete boom")
		}, "delete summary-isolation probe session: delete boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &sessionServiceStub{sess: ref.SessionService()}
			tt.mutate(stub)
			tgt := &stubTarget{
				name: "stub",
				caps: replaytest.CapAll,
				sess: stub,
				mem:  ref.MemoryService(),
			}
			_, err := replaytest.NewRunner().RunCase(
				context.Background(), summaryProbeCase(), tgt)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestRunCaseSummaryIsolationProbePass covers the happy path of the probe
// with a stubbed service (create/get/delete all succeed, no leak).
func TestRunCaseSummaryIsolationProbePass(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	tgt := &stubTarget{
		name: "stub",
		caps: replaytest.CapAll,
		sess: &sessionServiceStub{sess: ref.SessionService()},
		mem:  ref.MemoryService(),
	}
	snap, err := replaytest.NewRunner().RunCase(
		context.Background(), summaryProbeCase(), tgt)
	require.NoError(t, err)
	assert.Equal(t, "stub", snap.Backend)
}

// TestRunCaseTrackServiceNotImplemented covers applyTrack's type-assertion
// failure when the session service lacks session.TrackService.
func TestRunCaseTrackServiceNotImplemented(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	tgt := &stubTarget{
		name: "stub",
		caps: replaytest.CapAll,
		sess: &sessionServiceStub{sess: ref.SessionService()},
		mem:  ref.MemoryService(),
	}
	c := replaytest.Case{
		Name: "track/no-track-service",
		Steps: []replaytest.Step{
			{Op: replaytest.OpCreateSession, SessionID: "s1"},
			{Op: replaytest.OpAppendTrack, SessionID: "s1",
				Track: &replaytest.TrackSpec{Track: "tr", Payload: `{"x":1}`}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, tgt)
	assert.ErrorContains(t, err, "does not implement session.TrackService")
}

// TestRunPairCaseRunErrors covers runCasePair's reference and candidate
// failure paths.
func TestRunPairCaseRunErrors(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("cand")
	defer cand.Close()
	bad := &stubTarget{name: "bad", caps: replaytest.CapAll}

	_, err := replaytest.RunPair(context.Background(),
		[]replaytest.Case{simpleSessionCase()}, bad, cand)
	assert.ErrorContains(t, err, "reference bad")

	_, err = replaytest.RunPair(context.Background(),
		[]replaytest.Case{simpleSessionCase()}, ref, bad)
	assert.ErrorContains(t, err, "candidate bad")
}

// memoryServiceStub injects faults into the memory operations.
type memoryServiceStub struct {
	memory.Service
	addErr    error
	updateErr error
	deleteErr error
	clearErr  error
}

func (m *memoryServiceStub) AddMemory(
	ctx context.Context, userKey memory.UserKey, content string,
	topics []string, opts ...memory.AddOption,
) error {
	if m.addErr != nil {
		return m.addErr
	}
	return m.Service.AddMemory(ctx, userKey, content, topics, opts...)
}

func (m *memoryServiceStub) UpdateMemory(
	ctx context.Context, key memory.Key, content string,
	topics []string, opts ...memory.UpdateOption,
) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	return m.Service.UpdateMemory(ctx, key, content, topics, opts...)
}

func (m *memoryServiceStub) DeleteMemory(
	ctx context.Context, key memory.Key,
) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.Service.DeleteMemory(ctx, key)
}

func (m *memoryServiceStub) ClearMemories(
	ctx context.Context, userKey memory.UserKey,
) error {
	if m.clearErr != nil {
		return m.clearErr
	}
	return m.Service.ClearMemories(ctx, userKey)
}

// memoryStepTarget builds a target whose memory service carries the given
// fault hook, sharing the reference's real services otherwise.
func memoryStepTarget(
	ref *replaytest.InMemoryTarget, hook func(*memoryServiceStub),
) replaytest.Target {
	mstub := &memoryServiceStub{Service: ref.MemoryService()}
	hook(mstub)
	return &stubTarget{
		name: "stub",
		caps: replaytest.CapAll,
		sess: ref.SessionService(),
		mem:  mstub,
	}
}

// TestRunCaseMemoryOpErrors covers the backend-error paths of the memory
// operations.
func TestRunCaseMemoryOpErrors(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	addStep := replaytest.Step{Op: replaytest.OpAddMemory,
		Memory: &replaytest.MemorySpec{Content: "x"}}
	updateStep := replaytest.Step{Op: replaytest.OpUpdateMemory,
		Memory: &replaytest.MemorySpec{MatchContent: "x", Content: "y"}}
	deleteStep := replaytest.Step{Op: replaytest.OpDeleteMemory,
		Memory: &replaytest.MemorySpec{MatchContent: "x"}}
	clearStep := replaytest.Step{Op: replaytest.OpClearMemories}

	tests := []struct {
		name  string
		steps []replaytest.Step
		hook  func(*memoryServiceStub)
		want  string
	}{
		{"add fails", []replaytest.Step{addStep},
			func(m *memoryServiceStub) { m.addErr = errors.New("add boom") },
			"add boom"},
		{"update fails", []replaytest.Step{addStep, updateStep},
			func(m *memoryServiceStub) { m.updateErr = errors.New("update boom") },
			"update boom"},
		{"delete fails", []replaytest.Step{addStep, deleteStep},
			func(m *memoryServiceStub) { m.deleteErr = errors.New("delete boom") },
			"delete boom"},
		{"clear fails", []replaytest.Step{addStep, clearStep},
			func(m *memoryServiceStub) { m.clearErr = errors.New("clear boom") },
			"clear boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := replaytest.Case{
				Name:     "memory/op-error",
				NeedCaps: replaytest.Capability{Memory: true},
				Steps:    tt.steps,
			}
			_, err := replaytest.NewRunner().RunCase(
				context.Background(), c, memoryStepTarget(ref, tt.hook))
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// TestRunCaseDeleteStateErrors covers the delete-loops' error branches in
// applyStateOp.
func TestRunCaseDeleteStateErrors(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	tests := []struct {
		name string
		op   replaytest.OpKind
		hook func(*sessionServiceStub)
		want string
	}{
		{"delete app state", replaytest.OpDeleteAppState,
			func(s *sessionServiceStub) { s.deleteAppStateErr = errors.New("del app boom") },
			"del app boom"},
		{"delete user state", replaytest.OpDeleteUserState,
			func(s *sessionServiceStub) { s.deleteUserStateErr = errors.New("del user boom") },
			"del user boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &sessionServiceStub{sess: ref.SessionService()}
			tt.hook(stub)
			tgt := &stubTarget{
				name: "stub",
				caps: replaytest.CapAll,
				sess: stub,
				mem:  ref.MemoryService(),
			}
			c := replaytest.Case{
				Name: "state/delete-error",
				Steps: []replaytest.Step{
					{Op: tt.op, SessionID: "s1", StateKeys: []string{"k"}},
				},
			}
			_, err := replaytest.NewRunner().RunCase(context.Background(), c, tgt)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// TestRunCaseConcurrentAppendError covers the writer-failure path of
// applyConcurrent.
func TestRunCaseConcurrentAppendError(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	stub := &sessionServiceStub{
		sess:      ref.SessionService(),
		appendErr: errors.New("append boom"),
	}
	tgt := &stubTarget{
		name: "stub",
		caps: replaytest.CapAll,
		sess: stub,
		mem:  ref.MemoryService(),
	}
	c := replaytest.Case{
		Name: "concurrent/append-error",
		Steps: []replaytest.Step{
			{Op: replaytest.OpCreateSession, SessionID: "s1"},
			{Op: replaytest.OpConcurrentEvents, SessionID: "s1",
				Concurrent: []replaytest.WriterSpec{{Branch: "b", Count: 2}}},
		},
	}
	_, err := replaytest.NewRunner().RunCase(context.Background(), c, tgt)
	assert.ErrorContains(t, err, "concurrent append")
	assert.ErrorContains(t, err, "append boom")
}

// TestRunCaseSnapshotReadErrors covers takeSnapshot's read-back failures.
func TestRunCaseSnapshotReadErrors(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("ref")
	defer ref.Close()

	t.Run("session read-back fails", func(t *testing.T) {
		stub := &sessionServiceStub{
			sess:   ref.SessionService(),
			getErr: errors.New("snapshot boom"),
		}
		tgt := &stubTarget{
			name: "stub",
			caps: replaytest.CapAll,
			sess: stub,
			mem:  ref.MemoryService(),
		}
		_, err := replaytest.NewRunner().RunCase(
			context.Background(), simpleSessionCase(), tgt)
		assert.ErrorContains(t, err, "snapshot")
		assert.ErrorContains(t, err, "snapshot boom")
	})

	t.Run("memory read-back fails", func(t *testing.T) {
		// Force ReadMemories to fail via a wrapper.
		tgt := &stubTarget{
			name: "stub",
			caps: replaytest.CapAll,
			sess: ref.SessionService(),
			mem:  &failingReadMemories{Service: ref.MemoryService()},
		}
		c := replaytest.Case{
			Name:     "memory/snapshot-error",
			NeedCaps: replaytest.Capability{Memory: true},
			Steps: []replaytest.Step{
				{Op: replaytest.OpAddMemory,
					Memory: &replaytest.MemorySpec{Content: "x"}},
			},
		}
		_, err := replaytest.NewRunner().RunCase(context.Background(), c, tgt)
		assert.ErrorContains(t, err, "snapshot")
		assert.ErrorContains(t, err, "read boom")
	})
}

// failingReadMemories fails only ReadMemories, after the writes succeed.
type failingReadMemories struct {
	memory.Service
}

func (f *failingReadMemories) ReadMemories(
	context.Context, memory.UserKey, int,
) ([]*memory.Entry, error) {
	return nil, errors.New("read boom")
}
