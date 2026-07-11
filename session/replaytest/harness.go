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

// executeOnBackend runs the replay operations on a single backend.
func (h *Harness) executeOnBackend(
	ctx context.Context,
	bf BackendFactory,
	key session.Key,
	userKey session.UserKey,
	tc ReplayCase,
) (*BackendSnapshot, error) {
	// Create session service.
	if bf.CreateSession == nil {
		return nil, fmt.Errorf("backend %q: CreateSession is nil", bf.Name)
	}
	sessSvc, err := bf.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}
	defer sessSvc.Close()

	// Create track service (may be nil or same as session service).
	var trackSvc session.TrackService
	if ts, ok := sessSvc.(session.TrackService); ok {
		trackSvc = ts
	} else if bf.CreateTrack != nil {
		raw, err := bf.CreateTrack()
		if err != nil {
			return nil, fmt.Errorf("create track service: %w", err)
		}
		trackSvc = raw
		// If the track service is a different instance, close it separately.
		if c, ok := raw.(interface{ Close() error }); ok {
			defer c.Close()
		}
	}

	// Create memory service.
	var memSvc memory.Service
	if bf.CreateMemory != nil && !tc.SkipMemories {
		raw, err := bf.CreateMemory()
		if err != nil {
			return nil, fmt.Errorf("create memory service: %w", err)
		}
		memSvc = raw
		defer memSvc.Close()
	}

	// Create the session.
	sess, err := sessSvc.CreateSession(ctx, key, nil)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Track simulate-write-error state.
	simulateError := false

	// Execute operations.
	for _, op := range tc.Operations {
		if op.SimulateWriteError {
			simulateError = true
			continue
		}
		switch op.Type {
		case OpAppendEvent:
			if op.Event == nil {
				return nil, fmt.Errorf("append_event: event is nil")
			}
			e := op.Event
			if e.ID == "" {
				e.ID = uuid.NewString()
			}
			if e.Timestamp.IsZero() {
				e.Timestamp = time.Now()
			}
			if simulateError {
				// Skip this write to simulate failure, then retry.
				simulateError = false
				// Retry the write.
				if err := sessSvc.AppendEvent(ctx, sess, e); err != nil {
					return nil, fmt.Errorf("append_event (retry): %w", err)
				}
				continue
			}
			if err := sessSvc.AppendEvent(ctx, sess, e); err != nil {
				return nil, fmt.Errorf("append_event: %w", err)
			}

		case OpUpdateSessionState:
			if err := sessSvc.UpdateSessionState(ctx, key, op.StateMap); err != nil {
				return nil, fmt.Errorf("update_session_state: %w", err)
			}

		case OpDeleteSessionState:
			delState := session.StateMap{op.StateKey: nil}
			if err := sessSvc.UpdateSessionState(ctx, key, delState); err != nil {
				return nil, fmt.Errorf("delete_session_state: %w", err)
			}

		case OpAppendTrackEvent:
			if trackSvc == nil {
				continue // unsupported
			}
			if op.TrackEvent == nil {
				return nil, fmt.Errorf("append_track_event: event is nil")
			}
			if err := trackSvc.AppendTrackEvent(ctx, sess, op.TrackEvent); err != nil {
				return nil, fmt.Errorf("append_track_event: %w", err)
			}

		case OpCreateSummary:
			if err := sessSvc.CreateSessionSummary(ctx, sess, op.SummaryFilterKey, op.SummaryForce); err != nil {
				return nil, fmt.Errorf("create_summary: %w", err)
			}

		case OpAddMemory:
			if memSvc == nil {
				continue
			}
			memKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
			if err := memSvc.AddMemory(ctx, memKey, op.MemoryContent, op.MemoryTopics); err != nil {
				return nil, fmt.Errorf("add_memory: %w", err)
			}

		case OpUpdateMemory:
			if memSvc == nil {
				continue
			}
			mk := memory.Key{AppName: key.AppName, UserID: key.UserID, MemoryID: op.MemoryID}
			if err := memSvc.UpdateMemory(ctx, mk, op.MemoryContent, op.MemoryTopics); err != nil {
				return nil, fmt.Errorf("update_memory: %w", err)
			}

		case OpDeleteMemory:
			if memSvc == nil {
				continue
			}
			mk := memory.Key{AppName: key.AppName, UserID: key.UserID, MemoryID: op.MemoryID}
			if err := memSvc.DeleteMemory(ctx, mk); err != nil {
				return nil, fmt.Errorf("delete_memory: %w", err)
			}

		case OpClearMemories:
			if memSvc == nil {
				continue
			}
			memKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
			if err := memSvc.ClearMemories(ctx, memKey); err != nil {
				return nil, fmt.Errorf("clear_memories: %w", err)
			}
		}
	}

	// Read back the final state from the backend.
	got, err := sessSvc.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get_session: %w", err)
	}
	if got == nil {
		return nil, fmt.Errorf("get_session returned nil")
	}

	snap := &BackendSnapshot{
		BackendName: bf.Name,
		SessionID:   key.SessionID,
		Events:      got.Events,
		State:       got.State,
		Summaries:   got.Summaries,
		Tracks:      got.Tracks,
	}

	// Read memories.
	if memSvc != nil && !tc.SkipMemories {
		memKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
		memories, err := memSvc.ReadMemories(ctx, memKey, 100)
		if err != nil {
			return nil, fmt.Errorf("read_memories: %w", err)
		}
		snap.Memories = memories
	}

	return snap, nil
}
