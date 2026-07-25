//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Runner executes replay cases against isolated backend instances.
type Runner struct {
	BaselineBackend string
	Backends        []BackendFactory
	Now             func() time.Time
}

// Run executes every case, normalizes each backend snapshot, and compares all
// enabled backends with BaselineBackend.
func (r Runner) Run(
	ctx context.Context,
	cases []ReplayCase,
) (*RunResult, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	result := &RunResult{
		Report: &DiffReport{
			SchemaVersion:   ReportSchemaVersion,
			GeneratedAt:     r.now().UTC(),
			BaselineBackend: r.BaselineBackend,
		},
		Snapshots: make(map[string]map[string]*Snapshot, len(cases)),
	}

	caseNames := make(map[string]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateReplayCase(replayCase); err != nil {
			return nil, err
		}
		if _, exists := caseNames[replayCase.Name]; exists {
			return nil, fmt.Errorf(
				"duplicate replay case %s",
				replayCase.Name,
			)
		}
		caseNames[replayCase.Name] = struct{}{}
		snapshots := make(map[string]*Snapshot, len(r.Backends))
		durations := make(map[string]time.Duration, len(r.Backends))
		result.Snapshots[replayCase.Name] = snapshots

		for _, factory := range r.Backends {
			if factory.Open == nil {
				continue
			}
			started := time.Now()
			snapshot, err := runBackendCase(ctx, factory, replayCase)
			if err != nil {
				return nil, fmt.Errorf(
					"run case %s on backend %s: %w",
					replayCase.Name,
					factory.Name,
					err,
				)
			}
			snapshots[factory.Name] = snapshot
			durations[factory.Name] = time.Since(started)
		}

		baseline := snapshots[r.BaselineBackend]
		if baseline == nil {
			return nil, fmt.Errorf(
				"baseline backend %s is unavailable for case %s",
				r.BaselineBackend,
				replayCase.Name,
			)
		}
		for _, factory := range r.Backends {
			if factory.Name == r.BaselineBackend {
				continue
			}
			if factory.Open == nil {
				comparison := disabledComparison(replayCase, factory)
				result.Report.Cases = append(
					result.Report.Cases,
					comparison,
				)
				continue
			}

			target := snapshots[factory.Name]
			differences := CompareSnapshots(
				replayCase.Name,
				baseline,
				target,
				replayCase.AllowedDiffs,
			)
			differences = append(
				differences,
				validateExpectations(replayCase, target)...,
			)
			comparison := CaseComparison{
				Case:        replayCase.Name,
				SessionID:   replayCase.Key.SessionID,
				Backend:     factory.Name,
				Status:      ComparisonPassed,
				DurationMS:  durations[factory.Name].Milliseconds(),
				Differences: differences,
				Unsupported: target.Unsupported,
			}
			if hasDisallowed(differences) {
				comparison.Status = ComparisonFailed
			}
			result.Report.Cases = append(
				result.Report.Cases,
				comparison,
			)
		}
	}
	summarizeReport(result.Report)
	return result, nil
}

func (r Runner) validate() error {
	if len(r.Backends) < 2 {
		return fmt.Errorf("at least two backends are required")
	}
	if r.BaselineBackend == "" {
		return fmt.Errorf("baseline backend is required")
	}
	names := make(map[string]struct{}, len(r.Backends))
	var baseline *BackendFactory
	for i := range r.Backends {
		factory := &r.Backends[i]
		if factory.Name == "" {
			return fmt.Errorf("backend name is required")
		}
		if _, exists := names[factory.Name]; exists {
			return fmt.Errorf("duplicate backend %s", factory.Name)
		}
		names[factory.Name] = struct{}{}
		if factory.Name == r.BaselineBackend {
			baseline = factory
		}
	}
	if baseline == nil {
		return fmt.Errorf(
			"baseline backend %s is not registered",
			r.BaselineBackend,
		)
	}
	if baseline.Open == nil {
		return fmt.Errorf(
			"baseline backend %s is disabled: %s",
			r.BaselineBackend,
			baseline.DisabledReason,
		)
	}
	return nil
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func validateReplayCase(replayCase ReplayCase) error {
	if replayCase.Name == "" {
		return fmt.Errorf("replay case name is required")
	}
	if err := replayCase.Key.CheckSessionKey(); err != nil {
		return fmt.Errorf(
			"replay case %s has invalid session key: %w",
			replayCase.Name,
			err,
		)
	}
	return nil
}

func disabledComparison(
	replayCase ReplayCase,
	factory BackendFactory,
) CaseComparison {
	reason := factory.DisabledReason
	if reason == "" {
		reason = "backend factory is not enabled"
	}
	return CaseComparison{
		Case:      replayCase.Name,
		SessionID: replayCase.Key.SessionID,
		Backend:   factory.Name,
		Status:    ComparisonUnsupported,
		Unsupported: []UnsupportedFeature{{
			Feature:     Feature("backend"),
			Reason:      reason,
			AllowedDiff: true,
		}},
	}
}

func summarizeReport(report *DiffReport) {
	report.Summary.CaseComparisons = len(report.Cases)
	for _, comparison := range report.Cases {
		switch comparison.Status {
		case ComparisonPassed:
			report.Summary.Passed++
		case ComparisonFailed:
			report.Summary.Failed++
		case ComparisonUnsupported:
			report.Summary.Unsupported++
		}
		for _, diff := range comparison.Differences {
			if diff.AllowedDiff {
				report.Summary.AllowedDiffs++
			} else {
				report.Summary.DisallowedDiffs++
			}
		}
	}
}

func hasDisallowed(differences []Difference) bool {
	for _, difference := range differences {
		if !difference.AllowedDiff {
			return true
		}
	}
	return false
}

type executionState struct {
	mu               sync.Mutex
	memoryRefs       map[string]string
	stateTransitions []StateTransition
	memorySearches   []MemorySearchSnapshot
	summaryHistory   []SummarySnapshot
	summaryRevisions map[string]int
	recoveries       []RecoverySnapshot
}

type executionObservations struct {
	stateTransitions []StateTransition
	memorySearches   []MemorySearchSnapshot
	summaryHistory   []SummarySnapshot
	recoveries       []RecoverySnapshot
}

func newExecutionState() *executionState {
	return &executionState{
		memoryRefs:       make(map[string]string),
		summaryRevisions: make(map[string]int),
	}
}

func (s *executionState) setMemoryRef(ref, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memoryRefs[ref] = id
}

func (s *executionState) memoryRef(ref string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.memoryRefs[ref]
	return id, ok
}

func (s *executionState) addStateTransition(transition StateTransition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateTransitions = append(s.stateTransitions, transition)
}

func (s *executionState) addMemorySearch(search MemorySearchSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memorySearches = append(s.memorySearches, search)
}

func (s *executionState) addSummary(
	filterKey string,
	item *session.Summary,
) {
	if item == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summaryRevisions[filterKey]++
	revision := s.summaryRevisions[filterKey]
	s.summaryHistory = append(
		s.summaryHistory,
		NormalizeSummary("", filterKey, item, revision),
	)
}

func (s *executionState) summaryRevision(filterKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryRevisions[filterKey]
}

func (s *executionState) addRecovery(recovery RecoverySnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoveries = append(s.recoveries, recovery)
}

func (s *executionState) snapshot(
	sessionID string,
) executionObservations {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := executionObservations{
		stateTransitions: append(
			[]StateTransition(nil),
			s.stateTransitions...,
		),
		memorySearches: append(
			[]MemorySearchSnapshot(nil),
			s.memorySearches...,
		),
		summaryHistory: make(
			[]SummarySnapshot,
			len(s.summaryHistory),
		),
		recoveries: append(
			[]RecoverySnapshot(nil),
			s.recoveries...,
		),
	}
	copy(out.summaryHistory, s.summaryHistory)
	for i := range out.summaryHistory {
		out.summaryHistory[i].SessionID = sessionID
		out.summaryHistory[i].ID = sessionID + ":" +
			out.summaryHistory[i].FilterKey
	}
	return out
}

func runBackendCase(
	ctx context.Context,
	factory BackendFactory,
	replayCase ReplayCase,
) (snapshot *Snapshot, err error) {
	backend, err := factory.Open(ctx, replayCase)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, fmt.Errorf("backend factory returned nil")
	}
	defer closeReplayBackend(backend, &err)
	if err := configureReplayBackend(backend, factory); err != nil {
		return nil, err
	}

	key := replayCase.Key
	userKey := memory.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}
	defer cleanReplayBackend(ctx, backend, key, userKey, &err)
	if err := resetReplayBackend(ctx, backend, key, userKey); err != nil {
		return nil, err
	}
	if _, err := backend.Session.CreateSession(
		ctx,
		key,
		replayCase.InitialState,
	); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	state := newExecutionState()
	for i, operation := range replayCase.Operations {
		path := fmt.Sprintf("operations[%d]", i)
		if err := executeWithRetry(
			ctx,
			backend,
			replayCase,
			state,
			operation,
			path,
			i,
		); err != nil {
			return nil, err
		}
	}

	snapshot, err = buildSnapshot(
		ctx,
		backend,
		replayCase,
		state,
	)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func configureReplayBackend(
	backend *Backend,
	factory BackendFactory,
) error {
	if backend.Name == "" {
		backend.Name = factory.Name
	}
	if backend.Session == nil {
		return fmt.Errorf("session service is nil")
	}
	if backend.Capabilities == (Capabilities{}) {
		backend.Capabilities = factory.Capabilities
	}
	if backend.Capabilities.Memory && backend.Memory == nil {
		return fmt.Errorf("memory capability has no service")
	}
	return nil
}

func resetReplayBackend(
	ctx context.Context,
	backend *Backend,
	key session.Key,
	userKey memory.UserKey,
) error {
	if err := backend.Session.DeleteSession(ctx, key); err != nil {
		return fmt.Errorf("reset session before replay: %w", err)
	}
	if backend.Memory == nil {
		return nil
	}
	if err := backend.Memory.ClearMemories(ctx, userKey); err != nil {
		return fmt.Errorf("reset memory before replay: %w", err)
	}
	return nil
}

func cleanReplayBackend(
	ctx context.Context,
	backend *Backend,
	key session.Key,
	userKey memory.UserKey,
	runErr *error,
) {
	cleanupErr := backend.Session.DeleteSession(ctx, key)
	if backend.Memory != nil {
		memoryErr := backend.Memory.ClearMemories(ctx, userKey)
		if cleanupErr == nil {
			cleanupErr = memoryErr
		}
	}
	if cleanupErr != nil && *runErr == nil {
		*runErr = fmt.Errorf("clean up backend: %w", cleanupErr)
	}
}

func closeReplayBackend(backend *Backend, runErr *error) {
	if backend.Close == nil {
		return
	}
	if closeErr := backend.Close(); closeErr != nil && *runErr == nil {
		*runErr = fmt.Errorf("close backend: %w", closeErr)
	}
}

func buildSnapshot(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	execution *executionState,
) (*Snapshot, error) {
	sess, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return nil, fmt.Errorf("read session snapshot: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session disappeared before snapshot")
	}
	rawEvents := sess.GetEvents()
	events, observed := NormalizeEvents(
		rawEvents,
		replayCase.CanonicalizeEventOrder,
	)
	observations := execution.snapshot(sess.ID)
	out := &Snapshot{
		Backend:            backend.Name,
		AppName:            sess.AppName,
		UserID:             sess.UserID,
		SessionID:          sess.ID,
		Events:             events,
		ObservedEventOrder: observed,
		State:              NormalizeState(sess.SnapshotState()),
		StateTransitions:   observations.stateTransitions,
		MemorySearches:     observations.memorySearches,
		SummaryHistory:     observations.summaryHistory,
		Recoveries:         observations.recoveries,
		Memories:           []MemorySnapshot{},
		Summaries:          make(map[string]SummarySnapshot),
		Contexts:           make(map[string]ContextSnapshot),
		Tracks:             make(map[string][]TrackEventSnapshot),
		Unsupported: unsupportedCapabilities(
			backend.Name,
			backend.Capabilities,
		),
	}
	if backend.Capabilities.Memory && backend.Memory != nil {
		entries, err := backend.Memory.ReadMemories(
			ctx,
			memory.UserKey{
				AppName: sess.AppName,
				UserID:  sess.UserID,
			},
			0,
		)
		if err != nil {
			return nil, fmt.Errorf("read memory snapshot: %w", err)
		}
		for _, entry := range entries {
			out.Memories = append(out.Memories, NormalizeMemory(entry))
		}
		sort.Slice(out.Memories, func(i, j int) bool {
			return out.Memories[i].ID < out.Memories[j].ID
		})
	}
	if backend.Capabilities.Summary {
		sess.SummariesMu.RLock()
		for filterKey, item := range sess.Summaries {
			if item == nil {
				continue
			}
			revision := execution.summaryRevision(filterKey)
			if revision == 0 {
				revision = 1
			}
			out.Summaries[filterKey] = NormalizeSummary(
				sess.ID,
				filterKey,
				item,
				revision,
			)
			out.Contexts[filterKey] = buildContextSnapshot(
				sess.ID,
				filterKey,
				item,
				rawEvents,
				observed,
			)
		}
		sess.SummariesMu.RUnlock()
	}
	if backend.Capabilities.Track {
		sess.TracksMu.RLock()
		for trackName, history := range sess.Tracks {
			if history == nil {
				continue
			}
			events := make(
				[]TrackEventSnapshot,
				0,
				len(history.Events),
			)
			for i, trackEvent := range history.Events {
				events = append(
					events,
					NormalizeTrackEvent(trackEvent, i),
				)
			}
			out.Tracks[string(trackName)] = events
		}
		sess.TracksMu.RUnlock()
	}
	return out, nil
}

func buildContextSnapshot(
	sessionID string,
	filterKey string,
	summary *session.Summary,
	events []event.Event,
	observedIDs []string,
) ContextSnapshot {
	out := ContextSnapshot{
		SummaryID:   summaryID(sessionID, filterKey),
		SummaryText: summary.Summary,
	}
	cutoff := summary.CutoffTime()
	for i, item := range events {
		if i >= len(observedIDs) {
			continue
		}
		if item.Timestamp.After(cutoff) {
			out.EventsAfterCutoff = append(
				out.EventsAfterCutoff,
				observedIDs[i],
			)
			continue
		}
		out.RetainedEvents = append(out.RetainedEvents, observedIDs[i])
	}
	return out
}

func summaryID(sessionID, filterKey string) string {
	return sessionID + ":" + filterKey
}

func unsupportedCapabilities(
	backend string,
	capabilities Capabilities,
) []UnsupportedFeature {
	var unsupported []UnsupportedFeature
	for _, item := range []struct {
		feature   Feature
		supported bool
	}{
		{FeatureEvents, capabilities.Events},
		{FeatureState, capabilities.State},
		{FeatureMemory, capabilities.Memory},
		{FeatureSummary, capabilities.Summary},
		{FeatureTrack, capabilities.Track},
		{FeatureEventPaging, capabilities.EventPaging},
		{FeatureTTL, capabilities.TTL},
	} {
		if item.supported {
			continue
		}
		unsupported = append(unsupported, UnsupportedFeature{
			Feature: item.feature,
			Reason: fmt.Sprintf(
				"%s backend does not implement %s",
				backend,
				item.feature,
			),
			AllowedDiff: true,
		})
	}
	return unsupported
}

func validateExpectations(
	replayCase ReplayCase,
	snapshot *Snapshot,
) []Difference {
	if snapshot == nil {
		return nil
	}
	var differences []Difference
	add := func(
		path string,
		expected any,
		actual any,
		locator differenceLocator,
	) {
		differences = append(differences, Difference{
			Case:             replayCase.Name,
			Source:           DifferenceSourceExpectation,
			Backend:          snapshot.Backend,
			SessionID:        snapshot.SessionID,
			SummaryID:        locator.summaryID,
			SummaryFilterKey: locator.summaryFilterKey,
			TrackName:        locator.trackName,
			FieldPath:        path,
			BaselineValue:    expected,
			ComparedValue:    actual,
			Explanation:      "backend output violates case expectation",
		})
	}
	if replayCase.Expected.EventCount != nil &&
		!hasUnsupported(snapshot, FeatureEvents) &&
		len(snapshot.Events) != *replayCase.Expected.EventCount {
		add(
			"expectations.event_count",
			*replayCase.Expected.EventCount,
			len(snapshot.Events),
			differenceLocator{},
		)
	}
	if replayCase.Expected.MemoryCount != nil &&
		!hasUnsupported(snapshot, FeatureMemory) &&
		len(snapshot.Memories) != *replayCase.Expected.MemoryCount {
		add(
			"expectations.memory_count",
			*replayCase.Expected.MemoryCount,
			len(snapshot.Memories),
			differenceLocator{},
		)
	}
	if !hasUnsupported(snapshot, FeatureSummary) {
		for _, filterKey := range replayCase.Expected.SummaryFilters {
			if _, ok := snapshot.Summaries[filterKey]; ok {
				continue
			}
			add(
				fmt.Sprintf(
					`expectations.summaries[%q]`,
					filterKey,
				),
				"present",
				"missing",
				differenceLocator{
					summaryID: summaryID(
						snapshot.SessionID,
						filterKey,
					),
					summaryFilterKey: filterKey,
				},
			)
		}
	}
	if !hasUnsupported(snapshot, FeatureTrack) {
		for trackName, expected := range replayCase.Expected.TrackEventCount {
			actual := len(snapshot.Tracks[trackName])
			if actual == expected {
				continue
			}
			add(
				fmt.Sprintf(
					`expectations.tracks[%q].length`,
					trackName,
				),
				expected,
				actual,
				differenceLocator{trackName: trackName},
			)
		}
	}
	return differences
}
