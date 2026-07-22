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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
)

// This test is the end-to-end counterpart of the canonical-level mutation
// matrix: it wraps a real session.Service (and memory.Service) so the
// inconsistency is injected at the backend boundary, then asserts the whole
// Runner -> Snapshot -> Normalize -> Diff chain reports a failure in the
// expected dimension (proving the snapshot cannot silently miss a write).
// Covered faults: a silently dropped write (acked but never persisted), a
// dirty half-write (event persisted, state delta lost), a wrong stored
// memory scope (read-back UserID differs from the written scope) and a
// cross-session summary leak.

// wrapperTarget swaps the inner target's session service for a faulty one,
// and optionally the memory service too. The faulty services are rebuilt on
// every Reset so injected counters restart per case.
type wrapperTarget struct {
	replaytest.Target
	wrap    func(session.Service) session.Service
	memWrap func(memory.Service) memory.Service
	svc     session.Service
	mem     memory.Service
}

func (t *wrapperTarget) Name() string { return t.Target.Name() + "-faulty" }

func (t *wrapperTarget) SessionService() session.Service { return t.svc }

func (t *wrapperTarget) MemoryService() memory.Service {
	if t.mem != nil {
		return t.mem
	}
	return t.Target.MemoryService()
}

func (t *wrapperTarget) Reset(ctx context.Context) error {
	if err := t.Target.Reset(ctx); err != nil {
		return err
	}
	t.svc = t.wrap(t.Target.SessionService())
	if t.memWrap != nil {
		t.mem = t.memWrap(t.Target.MemoryService())
	}
	return nil
}

// faultService is the shared base for faulty session services. Embedding an
// interface hides the concrete type's extra interfaces, so track appends are
// forwarded explicitly to keep the session.TrackService capability.
type faultService struct {
	session.Service
}

func (s *faultService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	te *session.TrackEvent,
	opts ...session.Option,
) error {
	ts, ok := s.Service.(session.TrackService)
	if !ok {
		return fmt.Errorf("underlying service does not implement session.TrackService")
	}
	return ts.AppendTrackEvent(ctx, sess, te, opts...)
}

// dropWriteService silently drops the Nth append while reporting success,
// simulating a backend that acks a write it never persisted.
type dropWriteService struct {
	faultService
	n     int32
	calls atomic.Int32
}

func (s *dropWriteService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if s.calls.Add(1) == s.n {
		return nil
	}
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// TestEndToEndDroppedWrite injects a silently dropped append at the backend
// boundary and asserts the full chain fails cases with an event diff.
func TestEndToEndDroppedWrite(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-ref")
	defer ref.Close()
	inner := replaytest.NewInMemoryTarget("inmemory-cand")
	defer inner.Close()
	cand := &wrapperTarget{
		Target: inner,
		wrap: func(s session.Service) session.Service {
			return &dropWriteService{faultService: faultService{Service: s}, n: 3}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cand.Reset(ctx))
	rep, err := replaytest.RunPair(ctx, cases.All(), ref, cand)
	require.NoError(t, err)

	assert.Greater(t, rep.Totals.Fail, 0,
		"a dropped write must fail at least one case end to end")
	dims := map[string]bool{}
	for _, cr := range rep.Cases {
		for _, d := range cr.Diffs {
			dims[d.Dimension] = true
		}
	}
	assert.True(t, dims[replaytest.DimEvent],
		"a dropped write must surface as an event-dimension diff, got %v", dims)
}

// dropDeltaService persists the event body but silently loses its state
// delta, simulating a backend dirty half-write: the delta is detached
// before delegating and restored afterwards so the caller's event object
// stays intact. Concurrent appends are safe: each call only touches its
// own evt pointer and the service keeps no shared state.
type dropDeltaService struct {
	faultService
}

func (s *dropDeltaService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if len(evt.StateDelta) == 0 {
		return s.Service.AppendEvent(ctx, sess, evt, opts...)
	}
	delta := evt.StateDelta
	evt.StateDelta = nil
	defer func() { evt.StateDelta = delta }()
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// TestEndToEndDroppedStateDelta injects a dirty half-write (event persisted,
// state delta lost) at the backend boundary and asserts the full chain
// fails cases with both an event diff (read-back state_delta is empty) and
// a state diff (session state misses the delta keys).
func TestEndToEndDroppedStateDelta(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-ref")
	defer ref.Close()
	inner := replaytest.NewInMemoryTarget("inmemory-cand")
	defer inner.Close()
	cand := &wrapperTarget{
		Target: inner,
		wrap: func(s session.Service) session.Service {
			return &dropDeltaService{faultService: faultService{Service: s}}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cand.Reset(ctx))
	rep, err := replaytest.RunPair(ctx, cases.All(), ref, cand)
	require.NoError(t, err)

	assert.Greater(t, rep.Totals.Fail, 0,
		"a lost state delta must fail at least one case end to end")
	dims := map[string]bool{}
	for _, cr := range rep.Cases {
		for _, d := range cr.Diffs {
			dims[d.Dimension] = true
		}
	}
	assert.True(t, dims[replaytest.DimEvent],
		"a lost state delta must surface as an event-dimension diff, got %v", dims)
	assert.True(t, dims[replaytest.DimState],
		"a lost state delta must surface as a state-dimension diff, got %v", dims)
}

// wrongScopeService simulates a backend that persists the wrong scope
// attribution: entries read back carry a stored UserID that differs from
// the user scope they were written under. Entries are shallow-copied before
// rewriting because the in-memory backend returns its stored pointers.
type wrongScopeService struct {
	memory.Service
}

// ReadMemories implements memory.Service.
func (s *wrongScopeService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.Service.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*memory.Entry, 0, len(entries))
	for _, e := range entries {
		cp := *e
		cp.UserID = "wrong-user"
		out = append(out, &cp)
	}
	return out, nil
}

// TestEndToEndMemoryScopeMismatch injects a wrong stored scope attribution
// at the backend boundary and asserts the full chain fails with a
// memory-dimension diff: the snapshot must trust the entry's stored UserID,
// not the queried key, or this fault would be masked by the read-back.
func TestEndToEndMemoryScopeMismatch(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-ref")
	defer ref.Close()
	inner := replaytest.NewInMemoryTarget("inmemory-cand")
	defer inner.Close()
	cand := &wrapperTarget{
		Target: inner,
		wrap: func(s session.Service) session.Service {
			return &faultService{Service: s}
		},
		memWrap: func(m memory.Service) memory.Service {
			return &wrongScopeService{Service: m}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cand.Reset(ctx))
	rep, err := replaytest.RunPair(ctx, cases.All(), ref, cand)
	require.NoError(t, err)

	assert.Greater(t, rep.Totals.Fail, 0,
		"a wrong stored memory scope must fail at least one case end to end")
	dims := map[string]bool{}
	for _, cr := range rep.Cases {
		for _, d := range cr.Diffs {
			dims[d.Dimension] = true
		}
	}
	assert.True(t, dims[replaytest.DimMemory],
		"a wrong stored memory scope must surface as a memory-dimension diff, got %v", dims)
}

// summaryLeakService simulates a backend that stores summaries at the wrong
// scope: summaries written for one session are read back from every later
// session, including the runner's fresh summary-isolation probe (whose ID
// ends with the probe suffix).
type summaryLeakService struct {
	faultService
}

func (s *summaryLeakService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || sess == nil || !strings.HasSuffix(key.SessionID, "-summary-isolation") {
		return sess, err
	}
	sess = sess.Clone()
	sess.Summaries = map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "leaked summary"},
	}
	return sess, nil
}

// TestEndToEndSummaryLeak injects a cross-session summary leak at the
// backend boundary and asserts the runner's isolation probe fails the run:
// the leak is invisible to snapshot comparison (the case's own sessions
// look correct), so only the fresh-probe read-back can catch it.
func TestEndToEndSummaryLeak(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-ref")
	defer ref.Close()
	inner := replaytest.NewInMemoryTarget("inmemory-cand")
	defer inner.Close()
	cand := &wrapperTarget{
		Target: inner,
		wrap: func(s session.Service) session.Service {
			return &summaryLeakService{faultService: faultService{Service: s}}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cand.Reset(ctx))
	_, err := replaytest.RunPair(ctx, cases.All(), ref, cand)
	require.Error(t, err,
		"a cross-session summary leak must fail the run via the isolation probe")
	assert.Contains(t, err.Error(), "probe session contains")
}
