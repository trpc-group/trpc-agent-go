//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// RunSpec executes a single spec against all configured backends and returns a diff report.
func RunSpec(ctx context.Context, spec *Spec, dbURL string) (*DiffReport, error) {
	report := NewDiffReport(spec)

	harness := NewHarness(spec, dbURL)
	defer harness.Close()

	if err := harness.Setup(ctx); err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}
	// Update report to reflect only backends that actually initialized.
	report.BackendsTested = spec.Backends

	if err := harness.Execute(ctx); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	sessionSnapshots, memorySnapshots, err := harness.Verify(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	normChain := DefaultNormalizerChain()
	// For concurrent specs, add an event-order normalizer so that goroutine scheduling differences across backends do not produce false order-mismatch diffs.
	if spec.HasTag("concurrent") {
		normChain.Append(&concurrentEventSorter{})
	}
	rules := MergeDiffRules(DefaultDiffRules(), spec.AllowedDiffs)
	comp := NewComparator(rules)

	if len(spec.Backends.Session) == 0 {
		return report, nil
	}
	refBackend := spec.Backends.Session[0]
	refMemBackend := refBackend
	if len(spec.Backends.Memory) > 0 {
		refMemBackend = spec.Backends.Memory[0]
	}
	// ---- session-scoped verifications (compute once, filter by path) ----
	sessionWhats := map[string]bool{}
	for _, vs := range spec.Verifies {
		switch vs.What {
		case "session_full", "events", "state", "summary", "tracks":
			sessionWhats[vs.What] = true
		}
	}
	// Track capability pre-check.
	allTrackCapable := true
	for _, name := range harness.ActiveSessionBackends {
		if !harness.TrackSupported[name] {
			allTrackCapable = false
		}
	}
	if sessionWhats["tracks"] && !allTrackCapable {
		for _, name := range harness.ActiveSessionBackends {
			if !harness.TrackSupported[name] {
				report.AddVerification(VerificationResult{
					What: "tracks", ReferenceBackend: name,
					ComparedBackend: name, Status: StatusSkip,
					SessionKey: harness.sessionKey,
					Diffs: []DiffResult{{Path: "$.tracks", Kind: DiffMissingEntry,
						Severity: SeverityInfo,
						Message:  fmt.Sprintf("backend %q does not implement TrackService; track persistence not supported", name),
					}},
				})
			}
		}
		delete(sessionWhats, "tracks")
	}

	if len(sessionWhats) > 0 {
		refSnap, ok := sessionSnapshots[refBackend]
		if ok {
			normChain.Reset()
			refNorm, err := normChain.NormalizeSession(refSnap[VerifySessionFull])
			if err != nil {
				return nil, fmt.Errorf("normalize reference session %s: %w", refBackend, err)
			}
			for _, backendName := range harness.ActiveSessionBackends {
				if backendName == refBackend {
					continue
				}
				cmpSnap, ok := sessionSnapshots[backendName]
				if !ok {
					continue
				}
				normChain.Reset()
				cmpNorm, err := normChain.NormalizeSession(cmpSnap[VerifySessionFull])
				if err != nil {
					return nil, fmt.Errorf("normalize session %s: %w", backendName, err)
				}
				allDiffs := comp.CompareSessions(refNorm, cmpNorm, refBackend, backendName)

				for what := range sessionWhats {
					scoped := filterDiffsByScope(allDiffs, what)
					vr := VerificationResult{
						What:             what,
						ReferenceBackend: refBackend,
						ComparedBackend:  backendName,
						SessionKey:       harness.sessionKey,
						Diffs:            scoped,
					}
					populateSessionLocalization(&vr, refNorm)
					if len(scoped) == 0 {
						vr.Status = StatusPass
					} else {
						vr.Status = StatusFail
					}
					report.AddVerification(vr)
				}

				// Summary existence check (once per backend pair).
				for _, vs := range spec.Verifies {
					if vs.What == "summary" && len(vs.Expect) > 0 {
						expectDiffs := checkSummaryExpectations(refNorm, vs)
						if len(expectDiffs) > 0 {
							expectVr := VerificationResult{
								What: "summary_expect", ReferenceBackend: refBackend,
								ComparedBackend: backendName, SessionKey: harness.sessionKey,
								Diffs: expectDiffs, Status: StatusFail,
							}
							populateSessionLocalization(&expectVr, refNorm)
							report.AddVerification(expectVr)
						}
					}
				}
			}
		}
	}

	// ---- non-session verifications (memories, memory_search) ----
	for _, verifySpec := range spec.Verifies {
		switch verifySpec.What {
		case "session_full", "events", "state", "summary", "tracks":
			// Handled above.
		case "memories", "memory_search":
			refMemSnap, ok := memorySnapshots[refMemBackend]
			if !ok {
				continue
			}
			normChain.Reset()
			refMemNorm, err := normChain.NormalizeMemory(refMemSnap[VerifyMemories])
			if err != nil {
				return nil, fmt.Errorf("normalize reference memory %s: %w", refMemBackend, err)
			}
			for _, backendName := range spec.Backends.Memory {
				if backendName == refMemBackend {
					continue
				}
				cmpMemSnap, ok := memorySnapshots[backendName]
				if !ok {
					continue
				}
				normChain.Reset()
				cmpMemNorm, err := normChain.NormalizeMemory(cmpMemSnap[VerifyMemories])
				if err != nil {
					return nil, fmt.Errorf("normalize memory %s: %w", backendName, err)
				}

				basePath := "$.memories"
				leftEntries := refMemNorm.Memories
				rightEntries := cmpMemNorm.Memories
				if verifySpec.What == "memory_search" {
					basePath = "$.search_results"
					normChain.Reset()
					refSearchNorm, err := normChain.NormalizeMemory(refMemSnap[VerifyMemorySearch])
					if err != nil {
						return nil, fmt.Errorf("normalize reference search: %w", err)
					}
					normChain.Reset()
					cmpSearchNorm, err := normChain.NormalizeMemory(cmpMemSnap[VerifyMemorySearch])
					if err != nil {
						return nil, fmt.Errorf("normalize compared search: %w", err)
					}
					leftEntries = refSearchNorm.SearchResults
					rightEntries = cmpSearchNorm.SearchResults
				}
				diffs := comp.CompareMemories(leftEntries, rightEntries, basePath)
				vr := VerificationResult{
					What:             verifySpec.What,
					ReferenceBackend: refMemBackend,
					ComparedBackend:  backendName,
					SessionKey:       harness.sessionKey,
					Diffs:            diffs,
				}
				// Populate memory localization fields from reference snapshot.
				populateMemoryLocalization(&vr, refMemNorm)
				if len(diffs) == 0 {
					vr.Status = StatusPass
				} else {
					vr.Status = StatusFail
				}
				report.AddVerification(vr)
			}
		}
	}

	report.Finalize()
	return report, nil
}

// populateSessionLocalization extracts summary filter keys and track names from the reference session snapshot for diff report localization.
func populateSessionLocalization(vr *VerificationResult, snap *SessionSnapshot) {
	if snap == nil || snap.Session == nil {
		return
	}
	// Collect summary filter keys.
	if len(snap.Session.Summaries) > 0 {
		keys := make([]string, 0, len(snap.Session.Summaries))
		for k := range snap.Session.Summaries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		vr.SummaryFilterKey = strings.Join(keys, ", ")
	}
	// Collect track names.
	if len(snap.Session.Tracks) > 0 {
		names := make([]string, 0, len(snap.Session.Tracks))
		for k := range snap.Session.Tracks {
			names = append(names, string(k))
		}
		sort.Strings(names)
		vr.TrackName = strings.Join(names, ", ")
	}
}

// populateMemoryLocalization extracts memory IDs from the reference memory snapshot for diff report localization.
func populateMemoryLocalization(vr *VerificationResult, snap *MemorySnapshot) {
	if snap == nil {
		return
	}
	entries := snap.Memories
	if len(entries) == 0 {
		entries = snap.SearchResults
	}
	if len(entries) > 0 {
		ids := make([]string, 0, len(entries))
		for _, e := range entries {
			if e != nil && e.ID != "" {
				ids = append(ids, e.ID)
			}
		}
		sort.Strings(ids)
		vr.MemoryID = strings.Join(ids, ", ")
	}
}

// checkSummaryExpectations verifies that the reference session snapshot satisfies the expectations declared in the verify spec.
func checkSummaryExpectations(snap *SessionSnapshot, vs VerifySpec) []DiffResult {
	if snap == nil || snap.Session == nil {
		return nil
	}
	var expect struct {
		FilterKeys []string `json:"filter_keys"`
	}
	if len(vs.Expect) == 0 {
		return nil
	}
	if err := json.Unmarshal(vs.Expect, &expect); err != nil || len(expect.FilterKeys) == 0 {
		return nil
	}
	var diffs []DiffResult
	for _, fk := range expect.FilterKeys {
		sum, ok := snap.Session.Summaries[fk]
		if !ok || sum == nil {
			diffs = append(diffs, DiffResult{
				Path:     "$.summaries." + fk,
				Kind:     DiffMissingEntry,
				Severity: SeverityError,
				Left:     "expected",
				Right:    nil,
				Message:  fmt.Sprintf("expected summary for filter-key %q but it is missing or nil", fk),
			})
			continue
		}
		if sum.Summary == "" {
			diffs = append(diffs, DiffResult{
				Path:     "$.summaries." + fk + ".summary",
				Kind:     DiffValueMismatch,
				Severity: SeverityError,
				Left:     "expected non-empty summary",
				Right:    "(empty)",
				Message:  fmt.Sprintf("expected non-empty summary for filter-key %q", fk),
			})
		}
	}
	return diffs
}

// filterDiffsByScope returns the subset of diffs whose path starts with the scope prefix matching the given what tag.
// session_full returns all diffs; events/state/summary/tracks return only diffs under $.events/$.state/etc.
func filterDiffsByScope(diffs []DiffResult, what string) []DiffResult {
	if what == "session_full" {
		return diffs
	}
	prefix := "$." + what
	var out []DiffResult
	for _, d := range diffs {
		if strings.HasPrefix(d.Path, prefix) {
			out = append(out, d)
		}
	}
	return out
}

// RunSpecs executes multiple specs and returns all reports.
func RunSpecs(ctx context.Context, specs []*Spec, dbURL string) ([]*DiffReport, error) {
	var reports []*DiffReport
	for _, spec := range specs {
		report, err := RunSpec(ctx, spec, dbURL)
		if err != nil {
			return reports, fmt.Errorf("spec %q: %w", spec.Name, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// WriteCombinedReport writes all reports plus an aggregate summary as a single JSON file.
func WriteCombinedReport(reports []*DiffReport, path string) error {
	if len(reports) == 0 {
		return nil
	}

	combined := struct {
		Reports []*DiffReport `json:"reports"`
		Summary struct {
			TotalSpecs  int `json:"total_specs"`
			TotalPassed int `json:"total_passed"`
			TotalFailed int `json:"total_failed"`
			TotalDiffs  int `json:"total_diffs"`
		} `json:"summary"`
	}{
		Reports: reports,
	}
	for _, r := range reports {
		combined.Summary.TotalSpecs++
		if !r.HasFailures() {
			combined.Summary.TotalPassed++
		} else {
			combined.Summary.TotalFailed++
		}
		for _, v := range r.Verifications {
			combined.Summary.TotalDiffs += len(v.Diffs)
		}
	}

	data, err := json.MarshalIndent(combined, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal combined report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write combined report: %w", err)
	}
	return nil
}
