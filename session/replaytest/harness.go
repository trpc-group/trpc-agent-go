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
	if err := validateReferenceBackend(h.opts, h.backends); err != nil {
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
	snaps, profiles, err := h.collectCaseSnapshots(ctx, tc, &cr)
	if err != nil {
		return cr, err
	}
	if len(snaps) == 0 {
		if cr.Status == "" {
			cr.Status = StatusSkipped
		}
		return cr, nil
	}
	if len(snaps) == 1 {
		applySingleBackendStatus(&cr)
		return cr, nil
	}
	pairs, err := buildComparisonPairs(h.opts.ComparisonMode, h.opts.ReferenceBackend, snaps)
	if err != nil {
		return cr, err
	}
	diffs := h.compareSnapshotPairs(tc, snaps, profiles, pairs)
	finalizeCaseStatus(&cr, diffs)
	return cr, nil
}

func validateBackends(backends []NamedBackend) error {
	if len(backends) == 0 {
		// Empty config would leave every CaseResult at the default "passed"
		// without executing steps or comparing snapshots.
		return fmt.Errorf("no backends registered")
	}
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

// validateReferenceBackend ensures reference mode names a registered backend.
func validateReferenceBackend(opts HarnessOpts, backends []NamedBackend) error {
	if opts.ComparisonMode == ComparisonAllPairs {
		return nil
	}
	ref := opts.ReferenceBackend
	if ref == "" {
		ref = DefaultHarnessOpts().ReferenceBackend
	}
	for _, b := range backends {
		name := b.Name
		if name == "" {
			name = b.Profile.Name
		}
		if name == ref {
			return nil
		}
	}
	return fmt.Errorf("reference backend %q is not registered", ref)
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
		inFlight: map[session.Key]*sessionInit{},
		keyMu:    map[session.Key]*sync.Mutex{},
		snapshot: &Snapshot{Backend: backend.Name},
	}
	for _, step := range tc.Steps {
		if err := ex.execute(ctx, step); err != nil {
			return nil, fmt.Errorf("%s %s: %w", tc.Name, step.Key(), err)
		}
	}
	if ex.getSnapshotSession() == nil {
		for _, key := range ex.sessionKeys() {
			if err := ex.captureSession(ctx, key); err != nil {
				return nil, err
			}
			break
		}
	}
	return ex.snapshot, nil
}

// caseExecutor runs steps. sessions/snapshot are shared across ParallelGroupStep
// workers and must only be accessed under mu (except the backend itself).
type caseExecutor struct {
	backend  NamedBackend
	sessions map[session.Key]*session.Session
	inFlight map[session.Key]*sessionInit
	keyMu    map[session.Key]*sync.Mutex // serializes mutating ops per session key
	snapshot *Snapshot
	mu       sync.Mutex
}

// sessionInit coordinates a single Get/Create for one session key.
type sessionInit struct {
	done chan struct{}
	sess *session.Session
	err  error
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

func (e *caseExecutor) sessionKeys() []session.Key {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys := make([]session.Key, 0, len(e.sessions))
	for k := range e.sessions {
		keys = append(keys, k)
	}
	return keys
}

func (e *caseExecutor) getSnapshotSession() *session.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshot.Session
}

func (e *caseExecutor) setSnapshotSessionID(id string) {
	e.mu.Lock()
	e.snapshot.SessionID = id
	e.mu.Unlock()
}

func (e *caseExecutor) getSnapshotSessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshot.SessionID
}

func (e *caseExecutor) storeSession(key session.Key, sess *session.Session) {
	e.mu.Lock()
	e.sessions[key] = sess
	e.mu.Unlock()
}

// lockSessionKey serializes mutating backend ops that share a *session.Session
// pointer (e.g. concurrent AppendEvent on the same key). Different keys still
// run fully in parallel under ParallelGroupStep.
func (e *caseExecutor) lockSessionKey(key session.Key) func() {
	e.mu.Lock()
	m, ok := e.keyMu[key]
	if !ok {
		m = &sync.Mutex{}
		e.keyMu[key] = m
	}
	e.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (e *caseExecutor) ensureSession(ctx context.Context, key session.Key) (*session.Session, error) {
	e.mu.Lock()
	if sess, ok := e.sessions[key]; ok && sess != nil {
		e.mu.Unlock()
		return sess, nil
	}
	if init, ok := e.inFlight[key]; ok {
		e.mu.Unlock()
		<-init.done
		return init.sess, init.err
	}
	init := &sessionInit{done: make(chan struct{})}
	e.inFlight[key] = init
	e.mu.Unlock()

	// Single loader for this key: GetSession, then Create only on (nil, nil).
	existing, err := e.backend.SessionService.GetSession(ctx, key)
	if err != nil {
		init.err = err
		e.mu.Lock()
		delete(e.inFlight, key)
		e.mu.Unlock()
		close(init.done)
		return nil, err
	}
	if existing != nil {
		init.sess = existing
		e.mu.Lock()
		e.sessions[key] = existing
		delete(e.inFlight, key)
		e.mu.Unlock()
		close(init.done)
		return existing, nil
	}
	sess, err := e.backend.SessionService.CreateSession(ctx, key, session.StateMap{})
	init.sess, init.err = sess, err
	e.mu.Lock()
	if err == nil && sess != nil {
		// Prefer any concurrent cache fill; otherwise store our create result.
		if cached, ok := e.sessions[key]; ok && cached != nil {
			init.sess = cached
			sess = cached
		} else {
			e.sessions[key] = sess
		}
	}
	delete(e.inFlight, key)
	e.mu.Unlock()
	close(init.done)
	return sess, err
}

func (e *caseExecutor) appendEvent(ctx context.Context, step AppendEventStep) error {
	key := step.SessionKey
	if key.SessionID == "" {
		if sid := e.getSnapshotSessionID(); sid != "" {
			key = session.Key{AppName: DefaultApp, UserID: DefaultUser, SessionID: sid}
		} else {
			e.mu.Lock()
			if len(e.sessions) > 0 {
				for k := range e.sessions {
					key = k
					break
				}
			} else {
				key = session.Key{AppName: DefaultApp, UserID: DefaultUser, SessionID: "session-auto"}
			}
			e.mu.Unlock()
		}
	}
	sess, err := e.ensureSession(ctx, key)
	if err != nil {
		return err
	}
	e.setSnapshotSessionID(key.SessionID)
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
	// Same session pointer must not be mutated by concurrent AppendEvent.
	unlock := e.lockSessionKey(key)
	defer unlock()
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
		e.setSnapshotSessionID(step.SessionKey.SessionID)
		unlock := e.lockSessionKey(step.SessionKey)
		defer unlock()
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
	e.mu.Lock()
	e.snapshot.Memories = mems
	e.mu.Unlock()
	return nil
}

func (e *caseExecutor) createSummary(ctx context.Context, step CreateSummaryStep) error {
	sess, err := e.ensureSession(ctx, step.SessionKey)
	if err != nil {
		return err
	}
	latest, err := e.backend.SessionService.GetSession(ctx, step.SessionKey)
	if err != nil {
		return err
	}
	if latest != nil {
		sess = latest
		e.storeSession(step.SessionKey, latest)
	}
	e.setSnapshotSessionID(step.SessionKey.SessionID)
	unlock := e.lockSessionKey(step.SessionKey)
	defer unlock()
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
		e.storeSession(step.SessionKey, sess)
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
	latest, err := e.backend.SessionService.GetSession(ctx, step.SessionKey)
	if err != nil {
		return err
	}
	if latest != nil {
		sess = latest
		e.storeSession(step.SessionKey, latest)
	}
	e.setSnapshotSessionID(step.SessionKey.SessionID)
	ts, ok := e.backend.SessionService.(session.TrackService)
	if !ok {
		return fmt.Errorf("backend does not implement session.TrackService")
	}
	unlock := e.lockSessionKey(step.SessionKey)
	defer unlock()
	return ts.AppendTrackEvent(ctx, sess, step.Event)
}

func (e *caseExecutor) getSession(ctx context.Context, step GetSessionStep) error {
	e.setSnapshotSessionID(step.SessionKey.SessionID)
	return e.captureSession(ctx, step.SessionKey)
}

func (e *caseExecutor) captureSession(ctx context.Context, key session.Key) error {
	sess, err := e.backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.sessions[key] = sess
	e.snapshot.Session = sess
	e.snapshot.SessionID = key.SessionID
	e.mu.Unlock()
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
	e.snapshot.Session = sess
	e.snapshot.SessionID = key.SessionID
	e.mu.Unlock()
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
	e.mu.Lock()
	e.snapshot.AppState = st
	e.mu.Unlock()
	return nil
}

func (e *caseExecutor) listUserStates(ctx context.Context, step ListUserStatesStep) error {
	st, err := e.backend.SessionService.ListUserStates(ctx, step.UserKey)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.snapshot.UserState = st
	e.mu.Unlock()
	return nil
}
