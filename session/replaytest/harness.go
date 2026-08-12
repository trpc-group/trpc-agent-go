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
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// Harness options
// ---------------------------------------------------------------------------

// HarnessOption configures the replay harness.
type HarnessOption func(*harnessConfig)

type harnessConfig struct {
	backends  []BackendFactory
	appName   string
	userID    string
	sessionID string
}

// WithBackends adds extra backend factories to the harness.
func WithBackends(factories ...BackendFactory) HarnessOption {
	return func(c *harnessConfig) {
		c.backends = append(c.backends, factories...)
	}
}

// WithSessionKey overrides the default session key.
func WithSessionKey(app, user, sess string) HarnessOption {
	return func(c *harnessConfig) {
		c.appName = app
		c.userID = user
		c.sessionID = sess
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// Harness runs replay cases against multiple backends and compares results.
type Harness struct {
	cfg harnessConfig
}

// NewHarness creates a harness with the given options.
func NewHarness(opts ...HarnessOption) *Harness {
	cfg := harnessConfig{
		backends:  DefaultBackends(),
		appName:   "replay-test",
		userID:    "user-1",
		sessionID: "",
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Harness{cfg: cfg}
}

// Run executes all provided cases across all backends and returns a report.
func (h *Harness) Run(ctx context.Context, cases []ReplayCase) (*ReplayReport, error) {
	var results []CaseResult
	for _, tc := range cases {
		cr, err := h.runCase(ctx, tc)
		if err != nil {
			return nil, fmt.Errorf("case %q: %w", tc.Name, err)
		}
		results = append(results, *cr)
	}
	names := make([]string, len(h.cfg.backends))
	for i, b := range h.cfg.backends {
		names[i] = b.Name
	}
	return BuildReport(results, names), nil
}

// runCase runs a single case across all backends and compares.
func (h *Harness) runCase(ctx context.Context, tc ReplayCase) (*CaseResult, error) {
	sessID := h.cfg.sessionID
	if sessID == "" {
		sessID = "replay-" + uuid.NewString()
	}
	key := session.Key{
		AppName:   h.cfg.appName,
		UserID:    h.cfg.userID,
		SessionID: sessID,
	}
	userKey := session.UserKey{AppName: h.cfg.appName, UserID: h.cfg.userID}

	// Run against each backend, collecting snapshots.
	type backendResult struct {
		snap *BackendSnapshot
		err  error
	}
	bResults := make([]backendResult, len(h.cfg.backends))

	for i, bf := range h.cfg.backends {
		snap, err := h.executeOnBackend(ctx, bf, key, userKey, tc)
		bResults[i] = backendResult{snap: snap, err: err}
	}

	// Compare all pairs against the first (base) backend.
	cr := &CaseResult{
		CaseName: tc.Name,
	}
	baseResult := bResults[0]
	if baseResult.err != nil {
		return nil, fmt.Errorf("base backend %q failed: %w", h.cfg.backends[0].Name, baseResult.err)
	}
	baseSnap := baseResult.snap

	for i := 1; i < len(h.cfg.backends); i++ {
		other := bResults[i]
		if other.err != nil {
			cr.Differences = append(cr.Differences, DiffEntry{
				SessionID:      sessID,
				FieldPath:      "backend_error",
				BaseBackend:    h.cfg.backends[0].Name,
				BaseValue:      "ok",
				CompareBackend: h.cfg.backends[i].Name,
				CompareValue:   other.err.Error(),
				Explanation:    "backend failed to execute",
			})
			cr.HasDiff = true
			cr.DiffCount++
			cr.BackendPairs = append(cr.BackendPairs, [2]string{h.cfg.backends[0].Name, h.cfg.backends[i].Name})
			continue
		}
		cmp := NewComparator(h.cfg.backends[0].Name)
		diffs := cmp.Compare(baseSnap, other.snap)
		allowedCount := 0
		realDiffs := 0
		for _, d := range diffs {
			if d.AllowedDiff {
				allowedCount++
			} else {
				realDiffs++
			}
		}
		cr.Differences = append(cr.Differences, diffs...)
		cr.DiffCount += realDiffs
		cr.AllowedDiffCount += allowedCount
		if realDiffs > 0 {
			cr.HasDiff = true
		}
		cr.BackendPairs = append(cr.BackendPairs, [2]string{h.cfg.backends[0].Name, h.cfg.backends[i].Name})
	}
	return cr, nil
}

// opContext carries the per-backend services used while executing
// replay operations on one backend.
type opContext struct {
	sessSvc  session.Service
	trackSvc session.TrackService
	memSvc   memory.Service
	sess     *session.Session
	key      session.Key
	// ownsTrack reports whether trackSvc was created separately from
	// sessSvc and therefore needs its own Close call.
	ownsTrack bool
	// simulateError is set by an OpSimulateWriteError step and consumed
	// by the next write operation to model a failed-then-retried write.
	simulateError bool
}

// executeOnBackend runs the replay operations on a single backend.
func (h *Harness) executeOnBackend(
	ctx context.Context,
	bf BackendFactory,
	key session.Key,
	userKey session.UserKey,
	tc ReplayCase,
) (*BackendSnapshot, error) {
	oc, err := h.setupBackend(ctx, bf, key, tc)
	if err != nil {
		return nil, err
	}
	defer h.closeBackend(oc)

	for _, op := range tc.Operations {
		if err := h.applyOperation(ctx, oc, op); err != nil {
			return nil, err
		}
	}

	return h.readSnapshot(ctx, oc, bf.Name, tc.SkipMemories)
}

// setupBackend creates the session, track, and memory services for one
// backend factory and initializes the replay session.
func (h *Harness) setupBackend(
	ctx context.Context,
	bf BackendFactory,
	key session.Key,
	tc ReplayCase,
) (*opContext, error) {
	if bf.CreateSession == nil {
		return nil, fmt.Errorf("backend %q: CreateSession is nil", bf.Name)
	}
	sessSvc, err := bf.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}
	oc := &opContext{sessSvc: sessSvc, key: key}

	// Track service: reuse the session service when it implements
	// TrackService, otherwise create a dedicated one.
	if ts, ok := sessSvc.(session.TrackService); ok {
		oc.trackSvc = ts
	} else if bf.CreateTrack != nil {
		raw, err := bf.CreateTrack()
		if err != nil {
			return nil, fmt.Errorf("create track service: %w", err)
		}
		oc.trackSvc = raw
		oc.ownsTrack = true
	}

	if bf.CreateMemory != nil && !tc.SkipMemories {
		raw, err := bf.CreateMemory()
		if err != nil {
			return nil, fmt.Errorf("create memory service: %w", err)
		}
		oc.memSvc = raw
	}

	sess, err := sessSvc.CreateSession(ctx, key, nil)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	oc.sess = sess
	return oc, nil
}

// closeBackend releases backend services that own their resources.
func (h *Harness) closeBackend(oc *opContext) {
	if oc.sessSvc != nil {
		oc.sessSvc.Close()
	}
	if oc.ownsTrack {
		if c, ok := oc.trackSvc.(interface{ Close() error }); ok {
			c.Close()
		}
	}
	if oc.memSvc != nil {
		oc.memSvc.Close()
	}
}

// applyOperation executes one replay operation against the backend.
func (h *Harness) applyOperation(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if op.SimulateWriteError {
		oc.simulateError = true
		return nil
	}
	switch op.Type {
	case OpAppendEvent:
		return h.appendEventOp(ctx, oc, op)
	case OpUpdateSessionState:
		return h.updateSessionStateOp(ctx, oc, op)
	case OpDeleteSessionState:
		return h.deleteSessionStateOp(ctx, oc, op)
	case OpAppendTrackEvent:
		return h.appendTrackEventOp(ctx, oc, op)
	case OpCreateSummary:
		return h.createSummaryOp(ctx, oc, op)
	case OpAddMemory:
		return h.addMemoryOp(ctx, oc, op)
	case OpUpdateMemory:
		return h.updateMemoryOp(ctx, oc, op)
	case OpDeleteMemory:
		return h.deleteMemoryOp(ctx, oc, op)
	case OpClearMemories:
		return h.clearMemoriesOp(ctx, oc, op)
	default:
		return nil
	}
}

func (h *Harness) appendEventOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if op.Event == nil {
		return fmt.Errorf("append_event: event is nil")
	}
	e := op.Event
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	// A simulated write error drops the first attempt and retries once,
	// modeling a backend recovering from a transient failure.
	if oc.simulateError {
		oc.simulateError = false
		if err := oc.sessSvc.AppendEvent(ctx, oc.sess, e); err != nil {
			return fmt.Errorf("append_event (retry): %w", err)
		}
		return nil
	}
	if err := oc.sessSvc.AppendEvent(ctx, oc.sess, e); err != nil {
		return fmt.Errorf("append_event: %w", err)
	}
	return nil
}

func (h *Harness) updateSessionStateOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if err := oc.sessSvc.UpdateSessionState(ctx, oc.key, op.StateMap); err != nil {
		return fmt.Errorf("update_session_state: %w", err)
	}
	return nil
}

func (h *Harness) deleteSessionStateOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	delState := session.StateMap{op.StateKey: nil}
	if err := oc.sessSvc.UpdateSessionState(ctx, oc.key, delState); err != nil {
		return fmt.Errorf("delete_session_state: %w", err)
	}
	return nil
}

func (h *Harness) appendTrackEventOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if oc.trackSvc == nil {
		return nil // unsupported by this backend
	}
	if op.TrackEvent == nil {
		return fmt.Errorf("append_track_event: event is nil")
	}
	if err := oc.trackSvc.AppendTrackEvent(ctx, oc.sess, op.TrackEvent); err != nil {
		return fmt.Errorf("append_track_event: %w", err)
	}
	return nil
}

func (h *Harness) createSummaryOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if err := oc.sessSvc.CreateSessionSummary(ctx, oc.sess, op.SummaryFilterKey, op.SummaryForce); err != nil {
		return fmt.Errorf("create_summary: %w", err)
	}
	return nil
}

func (h *Harness) addMemoryOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if oc.memSvc == nil {
		return nil
	}
	memKey := memory.UserKey{AppName: oc.key.AppName, UserID: oc.key.UserID}
	if err := oc.memSvc.AddMemory(ctx, memKey, op.MemoryContent, op.MemoryTopics); err != nil {
		return fmt.Errorf("add_memory: %w", err)
	}
	return nil
}

func (h *Harness) updateMemoryOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if oc.memSvc == nil {
		return nil
	}
	mk := memory.Key{AppName: oc.key.AppName, UserID: oc.key.UserID, MemoryID: op.MemoryID}
	if err := oc.memSvc.UpdateMemory(ctx, mk, op.MemoryContent, op.MemoryTopics); err != nil {
		return fmt.Errorf("update_memory: %w", err)
	}
	return nil
}

func (h *Harness) deleteMemoryOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if oc.memSvc == nil {
		return nil
	}
	mk := memory.Key{AppName: oc.key.AppName, UserID: oc.key.UserID, MemoryID: op.MemoryID}
	if err := oc.memSvc.DeleteMemory(ctx, mk); err != nil {
		return fmt.Errorf("delete_memory: %w", err)
	}
	return nil
}

func (h *Harness) clearMemoriesOp(
	ctx context.Context,
	oc *opContext,
	op ReplayOperation,
) error {
	if oc.memSvc == nil {
		return nil
	}
	memKey := memory.UserKey{AppName: oc.key.AppName, UserID: oc.key.UserID}
	if err := oc.memSvc.ClearMemories(ctx, memKey); err != nil {
		return fmt.Errorf("clear_memories: %w", err)
	}
	return nil
}

// readSnapshot reads the final observable state back from the backend.
func (h *Harness) readSnapshot(
	ctx context.Context,
	oc *opContext,
	backendName string,
	skipMemories bool,
) (*BackendSnapshot, error) {
	got, err := oc.sessSvc.GetSession(ctx, oc.key)
	if err != nil {
		return nil, fmt.Errorf("get_session: %w", err)
	}
	if got == nil {
		return nil, fmt.Errorf("get_session returned nil")
	}

	snap := &BackendSnapshot{
		BackendName: backendName,
		SessionID:   oc.key.SessionID,
		Events:      got.Events,
		State:       got.State,
		Summaries:   got.Summaries,
		Tracks:      got.Tracks,
	}

	if oc.memSvc != nil && !skipMemories {
		memKey := memory.UserKey{AppName: oc.key.AppName, UserID: oc.key.UserID}
		memories, err := oc.memSvc.ReadMemories(ctx, memKey, 100)
		if err != nil {
			return nil, fmt.Errorf("read_memories: %w", err)
		}
		snap.Memories = memories
	}

	return snap, nil
}
