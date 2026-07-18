// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Harness runs replay cases across named backends.
type Harness struct {
	backends   []NamedBackend
	opts       HarnessOpts
	normalizer *Normalizer
	comparator *Comparator
}

// NewHarness creates a replay harness.
func NewHarness(opts HarnessOpts) *Harness {
	d := DefaultHarnessOpts()
	if opts.ComparisonMode == "" {
		opts.ComparisonMode = d.ComparisonMode
	}
	if opts.ReferenceBackend == "" {
		opts.ReferenceBackend = d.ReferenceBackend
	}
	if opts.Mode == "" {
		opts.Mode = d.Mode
	}
	return &Harness{
		opts:       opts,
		normalizer: NewNormalizer(),
		comparator: NewComparator(),
	}
}

// AddBackend registers a backend for replay execution.
func (h *Harness) AddBackend(b NamedBackend) {
	if b.Name == "" {
		b.Name = b.Profile.Name
	}
	h.backends = append(h.backends, b)
}

// Run executes cases and returns an aggregated report.
func (h *Harness) Run(ctx context.Context, cases []ReplayCase) (*Report, error) {
	if err := validateBackends(h.backends); err != nil {
		return nil, err
	}
	for _, tc := range cases {
		if err := validateAllowedDiffs(tc); err != nil {
			return nil, err
		}
	}
	results := make([]CaseResult, 0, len(cases))
	var flat []Diff
	for _, tc := range cases {
		cr, err := h.runCase(ctx, tc)
		if err != nil {
			return nil, err
		}
		results = append(results, cr)
		flat = append(flat, cr.Diffs...)
	}
	return BuildReport(results, flat, h.backendNames(), h.opts), nil
}

func (h *Harness) backendNames() []string {
	names := make([]string, 0, len(h.backends))
	for _, b := range h.backends {
		names = append(names, b.Name)
	}
	return names
}

func (h *Harness) runCase(ctx context.Context, tc ReplayCase) (CaseResult, error) {
	cr := CaseResult{CaseName: tc.Name, Status: StatusPassed}
	snaps := map[string]*Snapshot{}
	profiles := map[string]BackendProfile{}

	for _, b := range h.backends {
		missing := MissingCaps(tc.RequiredCaps, b.Profile)
		if tc.RequiredCaps.NeedsMemory && b.MemoryService == nil {
			missing = append(missing, "memory")
		}
		if len(missing) > 0 {
			cr.Status = StatusSkipped
			cr.Skipped = fmt.Sprintf("unsupported: %v on %s", missing, b.Name)
			continue
		}
		raw, err := executeCase(ctx, tc, b)
		if err != nil {
			return cr, err
		}
		norm, err := h.normalizer.Normalize(raw)
		if err != nil {
			return cr, err
		}
		snaps[b.Name] = norm
		profiles[b.Name] = b.Profile
	}

	if len(snaps) == 0 {
		if cr.Status == "" {
			cr.Status = StatusSkipped
		}
		return cr, nil
	}

	// Single-backend self-check: pass when only one backend executed,
	// but keep StatusSkipped if any other backend was capability-skipped.
	if len(snaps) == 1 {
		if cr.Status != StatusSkipped {
			cr.Status = StatusPassed
		}
		return cr, nil
	}

	var pairs [][2]string
	switch h.opts.ComparisonMode {
	case ComparisonAllPairs:
		names := make([]string, 0, len(snaps))
		for n := range snaps {
			names = append(names, n)
		}
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				pairs = append(pairs, [2]string{names[i], names[j]})
			}
		}
	default:
		ref := h.opts.ReferenceBackend
		if _, ok := snaps[ref]; !ok {
			// pick first as reference
			for n := range snaps {
				ref = n
				break
			}
		}
		for n := range snaps {
			if n == ref {
				continue
			}
			pairs = append(pairs, [2]string{ref, n})
		}
	}

	var diffs []Diff
	for _, p := range pairs {
		d := h.comparator.Compare(tc, snaps[p[0]], snaps[p[1]], profiles[p[0]], profiles[p[1]])
		diffs = append(diffs, d...)
	}
	cr.Diffs = diffs
	if hasErrorDiff(diffs) {
		cr.Status = StatusFailed
	} else if cr.Status == StatusSkipped {
		// keep skipped if any backend skipped and no errors
	} else {
		cr.Status = StatusPassed
	}
	return cr, nil
}

func validateBackends(backends []NamedBackend) error {
	seen := map[string]struct{}{}
	for i, b := range backends {
		name := b.Name
		if name == "" {
			name = b.Profile.Name
		}
		if name == "" {
			return fmt.Errorf("backend[%d]: empty name", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate backend name %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateAllowedDiffs(tc ReplayCase) error {

	for i, rule := range tc.AllowedDiffs {
		switch rule.Rule {
		case RuleIgnore, RuleWithinDelta, RuleNotEmpty, RuleSameType:
			// ok
		case "":
			return fmt.Errorf("case %q AllowedDiffs[%d]: empty rule is not allowed; use %q explicitly", tc.Name, i, RuleIgnore)
		default:
			return fmt.Errorf("case %q AllowedDiffs[%d]: unknown rule %q", tc.Name, i, rule.Rule)
		}
	}
	return nil
}

func hasErrorDiff(diffs []Diff) bool {
	for _, d := range diffs {
		if !d.Allowed {
			return true
		}
	}
	return false
}

func executeCase(ctx context.Context, tc ReplayCase, backend NamedBackend) (*Snapshot, error) {
	ex := &caseExecutor{
		backend:  backend,
		sessions: map[session.Key]*session.Session{},
		snapshot: &Snapshot{Backend: backend.Name},
	}
	for _, step := range tc.Steps {
		if err := ex.execute(ctx, step); err != nil {
			return nil, fmt.Errorf("%s %s: %w", tc.Name, step.Key(), err)
		}
	}
	if ex.snapshot.Session == nil {
		for key := range ex.sessions {
			if err := ex.captureSession(ctx, key); err != nil {
				return nil, err
			}
			break
		}
	}
	return ex.snapshot, nil
}

type caseExecutor struct {
	backend  NamedBackend
	sessions map[session.Key]*session.Session
	snapshot *Snapshot
	mu       sync.Mutex // protects sessions map under ParallelGroupStep
}

func (e *caseExecutor) execute(ctx context.Context, step Step) error {
	switch s := step.(type) {
	case AppendEventStep:
		return e.appendEvent(ctx, s)
	case UpdateStateStep:
		return e.updateState(ctx, s)
	case AddMemoryStep:
		return e.addMemory(ctx, s)
	case CaptureMemoryStep:
		return e.captureMemory(ctx, s)
	case CreateSummaryStep:
		return e.createSummary(ctx, s)
	case WaitSummaryStep:
		return e.waitSummary(ctx, s)
	case AppendTrackStep:
		return e.appendTrack(ctx, s)
	case GetSessionStep:
		return e.getSession(ctx, s)
	case ListAppStatesStep:
		return e.listAppStates(ctx, s)
	case ListUserStatesStep:
		return e.listUserStates(ctx, s)
	case ReloadSessionStep:
		return e.reloadSession(ctx, s)
	case ParallelGroupStep:
		return e.parallelGroup(ctx, s)
	default:
		return fmt.Errorf("unknown step type %T", step)
	}
}

func (e *caseExecutor) ensureSession(ctx context.Context, key session.Key) (*session.Session, error) {
	e.mu.Lock()
	if sess, ok := e.sessions[key]; ok && sess != nil {
		e.mu.Unlock()
		return sess, nil
	}
	e.mu.Unlock()
	// Prefer backend as source of truth outside the lock (may block).
	if existing, err := e.backend.SessionService.GetSession(ctx, key); err == nil && existing != nil {
		e.mu.Lock()
		e.sessions[key] = existing
		e.mu.Unlock()
		return existing, nil
	}
	sess, err := e.backend.SessionService.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	// Another worker may have created/cached concurrently; prefer existing cache.
	if cached, ok := e.sessions[key]; ok && cached != nil {
		e.mu.Unlock()
		return cached, nil
	}
	e.sessions[key] = sess
	e.mu.Unlock()
	return sess, nil
}

func (e *caseExecutor) appendEvent(ctx context.Context, step AppendEventStep) error {
	key := step.SessionKey
	if key.SessionID == "" {
		if e.snapshot.SessionID != "" {
			key = session.Key{AppName: DefaultApp, UserID: DefaultUser, SessionID: e.snapshot.SessionID}
		} else if len(e.sessions) > 0 {
			for k := range e.sessions {
				key = k
				break
			}
		} else {
			key = session.Key{AppName: DefaultApp, UserID: DefaultUser, SessionID: "session-auto"}
		}
	}
	sess, err := e.ensureSession(ctx, key)
	if err != nil {
		return err
	}
	e.snapshot.SessionID = key.SessionID
	evt := *step.Event
	event.WithTag(step.StepKey)(&evt)
	logical := step.LogicalKey
	if logical == "" {
		logical = step.StepKey
	}
	if err := event.SetExtension(&evt, EventLogicalKeyExtension, logical); err != nil {
		return err
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = FixedTimestamp
	}
	return e.backend.SessionService.AppendEvent(ctx, sess, &evt)
}

func (e *caseExecutor) updateState(ctx context.Context, step UpdateStateStep) error {
	switch step.Scope {
	case "app":
		if step.DeleteKey != "" {
			return e.backend.SessionService.DeleteAppState(ctx, step.AppName, step.DeleteKey)
		}
		return e.backend.SessionService.UpdateAppState(ctx, step.AppName, step.State)
	case "user":
		if step.DeleteKey != "" {
			return e.backend.SessionService.DeleteUserState(ctx, step.UserKey, step.DeleteKey)
		}
		return e.backend.SessionService.UpdateUserState(ctx, step.UserKey, step.State)
	default:
		if _, err := e.ensureSession(ctx, step.SessionKey); err != nil {
			return err
		}
		e.snapshot.SessionID = step.SessionKey.SessionID
		return e.backend.SessionService.UpdateSessionState(ctx, step.SessionKey, step.State)
	}
}

func (e *caseExecutor) addMemory(ctx context.Context, step AddMemoryStep) error {
	if e.backend.MemoryService == nil {
		return fmt.Errorf("memory service required")
	}
	return e.backend.MemoryService.AddMemory(ctx, step.UserKey, step.Memory, step.Topics)
}

func (e *caseExecutor) captureMemory(ctx context.Context, step CaptureMemoryStep) error {
	if e.backend.MemoryService == nil {
		return fmt.Errorf("memory service required")
	}
	limit := step.Limit
	if limit <= 0 {
		limit = 100
	}
	mems, err := e.backend.MemoryService.ReadMemories(ctx, step.UserKey, limit)
	if err != nil {
		return err
	}
	e.snapshot.Memories = mems
	return nil
}

func (e *caseExecutor) createSummary(ctx context.Context, step CreateSummaryStep) error {
	sess, err := e.ensureSession(ctx, step.SessionKey)
	if err != nil {
		return err
	}
	if latest, err := e.backend.SessionService.GetSession(ctx, step.SessionKey); err == nil && latest != nil {
		sess = latest
		e.sessions[step.SessionKey] = latest
	}
	e.snapshot.SessionID = step.SessionKey.SessionID
	if step.Async {
		return e.backend.SessionService.EnqueueSummaryJob(ctx, sess, step.FilterKey, step.Force)
	}
	return e.backend.SessionService.CreateSessionSummary(ctx, sess, step.FilterKey, step.Force)
}

func (e *caseExecutor) waitSummary(ctx context.Context, step WaitSummaryStep) error {
	timeout := step.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	poll := step.PollInterval
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		sess, err := e.backend.SessionService.GetSession(ctx, step.SessionKey)
		if err != nil {
			return err
		}
		e.sessions[step.SessionKey] = sess
		if sess != nil {
			sess.SummariesMu.RLock()
			sum := sess.Summaries[step.FilterKey]
			sess.SummariesMu.RUnlock()
			if sum != nil && sum.Summary != "" {
				return e.captureSession(ctx, step.SessionKey)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for summary filter=%q", step.FilterKey)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (e *caseExecutor) appendTrack(ctx context.Context, step AppendTrackStep) error {
	sess, err := e.ensureSession(ctx, step.SessionKey)
	if err != nil {
		return err
	}
	if latest, err := e.backend.SessionService.GetSession(ctx, step.SessionKey); err == nil && latest != nil {
		sess = latest
		e.sessions[step.SessionKey] = latest
	}
	e.snapshot.SessionID = step.SessionKey.SessionID
	ts, ok := e.backend.SessionService.(session.TrackService)
	if !ok {
		return fmt.Errorf("backend does not implement session.TrackService")
	}
	return ts.AppendTrackEvent(ctx, sess, step.Event)
}

func (e *caseExecutor) getSession(ctx context.Context, step GetSessionStep) error {
	e.snapshot.SessionID = step.SessionKey.SessionID
	return e.captureSession(ctx, step.SessionKey)
}

func (e *caseExecutor) captureSession(ctx context.Context, key session.Key) error {
	sess, err := e.backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.sessions[key] = sess
	e.mu.Unlock()
	e.snapshot.Session = sess
	e.snapshot.SessionID = key.SessionID
	return nil
}

func (e *caseExecutor) reloadSession(ctx context.Context, step ReloadSessionStep) error {
	key := step.SessionKey
	e.mu.Lock()
	delete(e.sessions, key)
	e.mu.Unlock()
	sess, err := e.backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("reload_session: session not found: %s", key.SessionID)
	}
	e.mu.Lock()
	e.sessions[key] = sess
	e.mu.Unlock()
	e.snapshot.Session = sess
	e.snapshot.SessionID = key.SessionID
	return nil
}

func (e *caseExecutor) parallelGroup(ctx context.Context, step ParallelGroupStep) error {
	if len(step.Branches) == 0 {
		return nil
	}
	var start sync.WaitGroup
	start.Add(1)
	errCh := make(chan error, len(step.Branches))
	var workers sync.WaitGroup
	for _, branch := range step.Branches {
		br := branch
		workers.Add(1)
		go func() {
			defer workers.Done()
			start.Wait() // barrier: all workers start together
			for _, st := range br {
				if err := e.execute(ctx, st); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	start.Done()
	workers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *caseExecutor) listAppStates(ctx context.Context, step ListAppStatesStep) error {
	st, err := e.backend.SessionService.ListAppStates(ctx, step.AppName)
	if err != nil {
		return err
	}
	e.snapshot.AppState = st
	return nil
}

func (e *caseExecutor) listUserStates(ctx context.Context, step ListUserStatesStep) error {
	st, err := e.backend.SessionService.ListUserStates(ctx, step.UserKey)
	if err != nil {
		return err
	}
	e.snapshot.UserState = st
	return nil
}
