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
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Harness orchestrates the execution of replay cases across multiple backends
// and generates cross-backend difference reports.
type Harness struct {
	cases      []ReplayCase
	comparator *Comparator
	reporter   *Reporter
	normalizer *Normalizer
}

// NewHarness creates a new Harness.
func NewHarness() *Harness {
	return &Harness{
		comparator: NewComparator(),
		reporter:   NewReporter(),
		normalizer: NewNormalizer(),
	}
}

// AddCase adds a replay case to the harness.
func (h *Harness) AddCase(c ReplayCase) {
	h.cases = append(h.cases, c)
}

// AddCases adds multiple replay cases to the harness.
func (h *Harness) AddCases(cases []ReplayCase) {
	h.cases = append(h.cases, cases...)
}

// Run executes all registered cases across all enabled backends and returns
// a diff report. It compares results pairwise between backends.
func (h *Harness) Run(ctx context.Context) (*DiffReport, error) {
	backends := GetBackends()
	enabled := make([]BackendFactory, 0, len(backends))
	for _, b := range backends {
		if b.Enabled {
			enabled = append(enabled, b)
		}
	}

	if len(enabled) < 2 {
		return nil, fmt.Errorf("need at least 2 enabled backends, got %d", len(enabled))
	}

	// Execute all cases on all backends.
	type caseResult struct {
		caseName string
		results  map[string]*BackendResult
		errors   map[string]error
	}

	var allResults []caseResult

	for _, c := range h.cases {
		results := make(map[string]*BackendResult)
		errs := make(map[string]error)

		for _, b := range enabled {
			sessSvc, memSvc, err := b.New()
			if err != nil {
				errs[b.Name] = fmt.Errorf("create services: %w", err)
				continue
			}

			start := time.Now()
			res, err := executeOps(ctx, sessSvc, memSvc, c.Ops)
			elapsed := time.Since(start)

			sessSvc.Close()
			memSvc.Close()

			if err != nil {
				errs[b.Name] = err
				continue
			}
			res.BackendName = b.Name
			res.Duration = elapsed
			results[b.Name] = res
		}

		allResults = append(allResults, caseResult{
			caseName: c.Name,
			results:  results,
			errors:   errs,
		})
	}

	// Compare results pairwise.
	caseDiffs := make(map[string][]DiffEntry)

	for _, cr := range allResults {
		backendNames := make([]string, 0, len(cr.results))
		for name := range cr.results {
			backendNames = append(backendNames, name)
		}
		sort.Strings(backendNames)

		// Compare each pair of backends.
		for i := 0; i < len(backendNames); i++ {
			for j := i + 1; j < len(backendNames); j++ {
				nameA := backendNames[i]
				nameB := backendNames[j]
				resA := cr.results[nameA]
				resB := cr.results[nameB]

				diffs := h.comparator.Compare(cr.caseName, resA, resB)
				caseDiffs[cr.caseName] = append(caseDiffs[cr.caseName], diffs...)
			}
		}
	}

	// Generate report.
	report := h.reporter.GenerateReport(caseDiffs)
	return report, nil
}

// RunTrap executes a single case on a single backend, applies a trap injection,
// and compares the trapped result against the expected baseline.
// Returns the diff entries found by the comparator.
func (h *Harness) RunTrap(ctx context.Context, c ReplayCase, trap TrapInjector) (*DiffReport, error) {
	backends := GetBackends()
	enabled := make([]BackendFactory, 0, len(backends))
	for _, b := range backends {
		if b.Enabled {
			enabled = append(enabled, b)
		}
	}

	if len(enabled) < 1 {
		return nil, fmt.Errorf("need at least 1 enabled backend for trap mode")
	}

	// Use the first enabled backend as the baseline.
	b := enabled[0]
	sessSvc, memSvc, err := b.New()
	if err != nil {
		return nil, fmt.Errorf("create services: %w", err)
	}
	defer sessSvc.Close()
	defer memSvc.Close()

	// Execute the case.
	baseline, err := executeOps(ctx, sessSvc, memSvc, c.Ops)
	if err != nil {
		return nil, fmt.Errorf("execute ops: %w", err)
	}
	baseline.BackendName = b.Name

	// Clone the result and apply the trap.
	trapped := cloneResult(baseline)
	trapped.BackendName = b.Name + "_trapped"
	trap.Inject(trapped)

	// Create a baseline result with the original name, and a trapped result.
	baselineCopy := cloneResult(baseline)
	baselineCopy.BackendName = b.Name

	// Compare trapped vs baseline.
	diffs := h.comparator.Compare(c.Name, baselineCopy, trapped)

	// Check if the trap was detected.
	caseDiffs := map[string][]DiffEntry{c.Name: diffs}
	report := h.reporter.GenerateReport(caseDiffs)

	return report, nil
}

// RunAllTrap runs all registered cases with trap mode on the first enabled backend.
func (h *Harness) RunAllTrap(ctx context.Context, traps []TrapInjector) (*DiffReport, error) {
	allDiffs := make(map[string][]DiffEntry)

	for _, c := range h.cases {
		for _, trap := range traps {
			report, err := h.RunTrap(ctx, c, trap)
			if err != nil {
				return nil, fmt.Errorf("case %s / trap %s: %w", c.Name, trap.Name, err)
			}
			caseKey := fmt.Sprintf("%s / trap=%s", c.Name, trap.Name)
			allDiffs[caseKey] = report.Diffs
		}
	}

	return h.reporter.GenerateReport(allDiffs), nil
}

// cloneResult creates a deep copy of a BackendResult.
func cloneResult(r *BackendResult) *BackendResult {
	if r == nil {
		return nil
	}
	clone := &BackendResult{
		BackendName:  r.BackendName,
		Duration:     r.Duration,
		Error:        r.Error,
		SummaryTexts: make(map[string]string),
		Tracks:       make(map[session.Track]*session.TrackEvents),
	}

	if r.Session != nil {
		clone.Session = r.Session.Clone()
		// Clone() copies the session but the State map inside is still shared.
		// Deep-copy the state map.
		if r.Session.State != nil {
			clone.Session.State = make(session.StateMap, len(r.Session.State))
			for k, v := range r.Session.State {
				val := make([]byte, len(v))
				copy(val, v)
				clone.Session.State[k] = val
			}
		}
		// Deep-copy events.
		if r.Session.Events != nil {
			clone.Session.Events = make([]event.Event, len(r.Session.Events))
			for i, e := range r.Session.Events {
				clone.Session.Events[i] = *e.Clone()
			}
		}
	}

	// Deep-copy memories.
	if r.Memories != nil {
		clone.Memories = make([]*memory.Entry, len(r.Memories))
		for i, e := range r.Memories {
			if e == nil {
				continue
			}
			entry := &memory.Entry{
				ID:        e.ID,
				AppName:   e.AppName,
				UserID:    e.UserID,
				CreatedAt: e.CreatedAt,
				UpdatedAt: e.UpdatedAt,
				Score:     e.Score,
			}
			if e.Memory != nil {
				mem := &memory.Memory{
					Memory:      e.Memory.Memory,
					LastUpdated: e.Memory.LastUpdated,
					Kind:        e.Memory.Kind,
					EventTime:   e.Memory.EventTime,
					Location:    e.Memory.Location,
				}
				if e.Memory.Topics != nil {
					mem.Topics = make([]string, len(e.Memory.Topics))
					copy(mem.Topics, e.Memory.Topics)
				}
				if e.Memory.Participants != nil {
					mem.Participants = make([]string, len(e.Memory.Participants))
					copy(mem.Participants, e.Memory.Participants)
				}
				entry.Memory = mem
			}
			clone.Memories[i] = entry
		}
	}

	for k, v := range r.SummaryTexts {
		clone.SummaryTexts[k] = v
	}
	for k, v := range r.Tracks {
		clone.Tracks[k] = v
	}

	return clone
}
