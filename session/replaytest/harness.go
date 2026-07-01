//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Harness runs replay cases across named backends.
type Harness struct {
	backends   []NamedBackend
	reference  string
	mode       ComparisonMode
	normalizer *Normalizer
	comparator *Comparator
}

// NewHarness creates a replay harness.
func NewHarness(opts HarnessOpts) *Harness {
	defaults := DefaultHarnessOpts()
	if opts.ComparisonMode == "" {
		opts.ComparisonMode = defaults.ComparisonMode
	}
	if opts.ReferenceBackend == "" {
		opts.ReferenceBackend = defaults.ReferenceBackend
	}
	return &Harness{
		reference:  opts.ReferenceBackend,
		mode:       opts.ComparisonMode,
		normalizer: NewNormalizer(),
		comparator: NewComparator(),
	}
}

// AddBackend adds one backend to the harness.
func (h *Harness) AddBackend(b NamedBackend) {
	if b.Name == "" {
		b.Name = b.Profile.Name
	}
	h.backends = append(h.backends, b)
}

// Run executes cases and returns an aggregated report.
func (h *Harness) Run(cases []ReplayCase) (*Report, error) {
	var results []CaseResult
	for _, tc := range cases {
		caseResult, err := h.runCase(tc)
		if err != nil {
			return nil, err
		}
		results = append(results, caseResult)
	}
	return BuildReport(results, h.backendNames(), h.reference), nil
}

func (h *Harness) runCase(tc ReplayCase) (CaseResult, error) {
	caseResult := CaseResult{CaseName: tc.Name}
	raw := map[string]*SessionSnapshot{}
	profiles := map[string]BackendProfile{}
	for _, backend := range h.backends {
		missing := MissingCapabilities(tc.RequiredCaps, backend.Profile)
		if tc.RequiredCaps.NeedsMemory && backend.MemoryService == nil {
			missing = append(missing, UnsupportedFeature{
				Backend: backend.Name,
				Feature: "memory",
				Impact:  "case skipped",
			})
		}
		if len(missing) > 0 {
			caseResult.Comparisons = append(caseResult.Comparisons, ComparisonResult{
				BackendA:    backend.Name,
				Status:      StatusSkipped,
				SkipReason:  SkipReasonUnsupportedFeature,
				Unsupported: missing,
			})
			continue
		}
		snapshot, err := executeCase(context.Background(), tc, backend)
		if err != nil {
			return caseResult, err
		}
		normalized, err := h.normalizer.Normalize(snapshot)
		if err != nil {
			return caseResult, err
		}
		raw[backend.Name] = normalized
		profiles[backend.Name] = backend.Profile
	}
	comparisons := h.compareSnapshots(tc, raw, profiles)
	caseResult.Comparisons = append(caseResult.Comparisons, comparisons...)
	caseResult.OverallStatus = overallStatus(caseResult.Comparisons)
	return caseResult, nil
}

func (h *Harness) compareSnapshots(
	tc ReplayCase,
	snapshots map[string]*SessionSnapshot,
	profiles map[string]BackendProfile,
) []ComparisonResult {
	if len(snapshots) <= 1 {
		if len(snapshots) == 1 {
			return []ComparisonResult{{Status: StatusPassed}}
		}
		return nil
	}
	var comparisons []ComparisonResult
	if h.mode == ComparisonPairs {
		names := sortedKeys(snapshots)
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				cmp := h.comparator.Compare(
					snapshots[names[i]], snapshots[names[j]], tc.AllowedDiffs,
					profiles[names[i]], profiles[names[j]],
				)
				comparisons = append(comparisons, cmp)
			}
		}
		return comparisons
	}
	refName := h.reference
	if _, ok := snapshots[refName]; !ok {
		for name := range snapshots {
			refName = name
			break
		}
	}
	for name, snapshot := range snapshots {
		if name == refName {
			continue
		}
		cmp := h.comparator.Compare(
			snapshots[refName], snapshot, tc.AllowedDiffs,
			profiles[refName], profiles[name],
		)
		cmp.Reference = refName
		comparisons = append(comparisons, cmp)
	}
	if len(comparisons) == 0 {
		comparisons = append(comparisons, ComparisonResult{Status: StatusPassed, Reference: refName})
	}
	return comparisons
}

func executeCase(ctx context.Context, tc ReplayCase, backend NamedBackend) (*SessionSnapshot, error) {
	exec := &caseExecutor{
		backend:  backend,
		sessions: map[session.Key]*session.Session{},
		snapshot: &SessionSnapshot{BackendName: backend.Name},
	}
	for _, step := range tc.Steps {
		if err := exec.execute(ctx, step); err != nil {
			return nil, fmt.Errorf("%s %s: %w", tc.Name, step.LogicalKey(), err)
		}
	}
	if exec.snapshot.Session == nil {
		for key := range exec.sessions {
			if err := exec.captureSession(ctx, key); err != nil {
				return nil, err
			}
			break
		}
	}
	return exec.snapshot, nil
}

type caseExecutor struct {
	backend  NamedBackend
	sessions map[session.Key]*session.Session
	snapshot *SessionSnapshot
}

func (e *caseExecutor) execute(ctx context.Context, step ReplayStep) error {
	switch s := step.(type) {
	case AppendEventStep:
		key := inferSessionKey(s.Event, s.Key)
		sess, err := e.ensureSession(ctx, key)
		if err != nil {
			return err
		}
		evt := *s.Event
		event.WithTag(s.Key)(&evt)
		if err := event.SetExtension(&evt, replayEventKeyExtension, s.Key); err != nil {
			return err
		}
		return e.backend.SessionService.AppendEvent(ctx, sess, &evt)
	case UpdateStateStep:
		return e.updateState(ctx, s)
	case AddMemoryStep:
		if e.backend.MemoryService == nil {
			return nil
		}
		if err := e.backend.MemoryService.AddMemory(ctx, s.UserKey, s.Memory, s.Topics); err != nil {
			return err
		}
		e.snapshot.Memories, _ = e.backend.MemoryService.ReadMemories(ctx, s.UserKey, 0)
	case SearchMemoryStep:
		if e.backend.MemoryService == nil {
			return nil
		}
		memories, err := e.backend.MemoryService.SearchMemories(ctx, s.UserKey, s.Query)
		if err != nil {
			return err
		}
		if s.Limit > 0 && len(memories) > s.Limit {
			memories = memories[:s.Limit]
		}
		e.snapshot.MemSearchResults = memories
	case CreateSummaryStep:
		sess, err := e.ensureSession(ctx, s.SessionKey)
		if err != nil {
			return err
		}
		if latest, err := e.backend.SessionService.GetSession(ctx, s.SessionKey); err == nil && latest != nil {
			sess = latest
			e.sessions[s.SessionKey] = latest
		}
		if s.Async {
			return e.backend.SessionService.EnqueueSummaryJob(ctx, sess, s.FilterKey, s.Force)
		}
		return e.backend.SessionService.CreateSessionSummary(ctx, sess, s.FilterKey, s.Force)
	case AppendTrackStep:
		sess, err := e.ensureSession(ctx, s.SessionKey)
		if err != nil {
			return err
		}
		trackSvc, ok := e.backend.SessionService.(session.TrackService)
		if !ok {
			return nil
		}
		return trackSvc.AppendTrackEvent(ctx, sess, s.Event)
	case GetSessionStep:
		return e.captureSession(ctx, s.SessionKey)
	case ListAppStatesStep:
		state, err := e.backend.SessionService.ListAppStates(ctx, s.AppName)
		if err != nil {
			return err
		}
		e.snapshot.AppStates = state
	case ListUserStatesStep:
		state, err := e.backend.SessionService.ListUserStates(ctx, s.UserKey)
		if err != nil {
			return err
		}
		e.snapshot.UserStates = state
	}
	return nil
}

func (e *caseExecutor) updateState(ctx context.Context, step UpdateStateStep) error {
	switch step.Scope {
	case ScopeApp:
		if step.DeleteKey != "" {
			return e.backend.SessionService.DeleteAppState(ctx, step.AppName, step.DeleteKey)
		}
		return e.backend.SessionService.UpdateAppState(ctx, step.AppName, step.State)
	case ScopeUser:
		if step.DeleteKey != "" {
			return e.backend.SessionService.DeleteUserState(ctx, step.UserKey, step.DeleteKey)
		}
		return e.backend.SessionService.UpdateUserState(ctx, step.UserKey, step.State)
	default:
		if _, err := e.ensureSession(ctx, step.SessionKey); err != nil {
			return err
		}
		return e.backend.SessionService.UpdateSessionState(ctx, step.SessionKey, step.State)
	}
}

func (e *caseExecutor) ensureSession(ctx context.Context, key session.Key) (*session.Session, error) {
	if sess, ok := e.sessions[key]; ok {
		return sess, nil
	}
	sess, err := e.backend.SessionService.CreateSession(ctx, key, nil)
	if err != nil {
		if got, getErr := e.backend.SessionService.GetSession(ctx, key); getErr == nil {
			e.sessions[key] = got
			return got, nil
		}
		return nil, err
	}
	e.sessions[key] = sess
	return sess, nil
}

func (e *caseExecutor) captureSession(ctx context.Context, key session.Key) error {
	sess, err := e.backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	e.sessions[key] = sess
	e.snapshot.Session = sess
	e.snapshot.SummaryMap = sess.Summaries
	e.snapshot.TrackEvents = map[string]*session.TrackEvents{}
	for track, events := range sess.Tracks {
		e.snapshot.TrackEvents[string(track)] = events
	}
	return nil
}

func inferSessionKey(evt *event.Event, fallback string) session.Key {
	if evt != nil && evt.InvocationID != "" {
		return session.Key{AppName: "replaytest", UserID: "user", SessionID: "session"}
	}
	return session.Key{AppName: "replaytest", UserID: "user", SessionID: fallback}
}

func overallStatus(comparisons []ComparisonResult) string {
	if len(comparisons) == 0 {
		return StatusPassed
	}
	seen := map[string]bool{}
	for _, cmp := range comparisons {
		if cmp.Status == "" {
			seen[StatusPassed] = true
			continue
		}
		seen[cmp.Status] = true
	}
	if len(seen) == 1 {
		for status := range seen {
			return status
		}
	}
	if seen[StatusFailed] {
		return StatusFailed
	}
	return StatusMixed
}

func (h *Harness) backendNames() []string {
	names := make([]string, 0, len(h.backends))
	for _, backend := range h.backends {
		names = append(names, backend.Name)
	}
	return names
}
