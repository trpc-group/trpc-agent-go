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
	report.BackendsTested = BackendConfig{
		Session: harness.ActiveSessionBackends,
		Memory:  harness.ActiveMemoryBackends,
	}
	report.SkippedBackends = harness.SkippedBackends

	if err := harness.Execute(ctx); err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	// Determine search query from spec verifications (memory_search).
	// All memory_search verifications must agree on the query;
	// inconsistent queries would compare results from different datasets.
	searchQuery := defaultSearchQuery
	for _, vs := range spec.Verifies {
		if vs.What != "memory_search" || len(vs.Params) == 0 {
			continue
		}
		var sq struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(vs.Params, &sq); err != nil || sq.Query == "" {
			continue
		}
		if searchQuery == defaultSearchQuery {
			searchQuery = sq.Query
		} else if sq.Query != searchQuery {
			return nil, fmt.Errorf("inconsistent memory_search queries: %q vs %q — all verifications must use the same query", searchQuery, sq.Query)
		}
	}
	sessionSnapshots, memorySnapshots, err := harness.Verify(ctx, searchQuery)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	normChain := DefaultNormalizerChain()
	rules := MergeDiffRules(DefaultDiffRules(), spec.AllowedDiffs)
	comp := NewComparator(rules)

	// Select reference backends from the active list, not the spec list.
	// If only memory backends are available (no sessions), we can still run memory-scoped verifications.
	if len(harness.ActiveSessionBackends) == 0 && len(harness.ActiveMemoryBackends) == 0 {
		report.Finalize()
		return report, nil
	}
	refBackend := ""
	if len(harness.ActiveSessionBackends) > 0 {
		refBackend = harness.ActiveSessionBackends[0]
	}
	refMemBackend := refBackend
	if len(harness.ActiveMemoryBackends) > 0 {
		refMemBackend = harness.ActiveMemoryBackends[0]
	}

	recordSingleBackendSkips(report, spec, harness, refBackend, refMemBackend)

	if len(harness.ActiveSessionBackends) >= 2 {
		if err := runSessionVerifications(report, harness, comp, normChain, spec, sessionSnapshots, refBackend); err != nil {
			return nil, err
		}
	}
	if len(harness.ActiveMemoryBackends) >= 2 {
		if err := runMemoryVerifications(report, harness, comp, normChain, spec, memorySnapshots, refMemBackend); err != nil {
			return nil, err
		}
	}

	report.Finalize()
	return report, nil
}

// recordSingleBackendSkips adds skip verifications when there are not enough
// active backends to perform a cross-backend comparison.
func recordSingleBackendSkips(report *DiffReport, spec *Spec, h *Harness, refSess, refMem string) {
	if len(h.ActiveSessionBackends) < 2 {
		for _, vs := range spec.Verifies {
			if isSessionWhat(vs.What) {
				report.AddVerification(VerificationResult{
					What: vs.What, ReferenceBackend: refSess,
					ComparedBackend: "", Status: StatusSkip,
					Diffs: []DiffResult{{Path: "$",
						Kind: DiffMissingEntry, Severity: SeverityInfo,
						Message: "only one active session backend, cannot cross-compare",
					}},
				})
			}
		}
	}
	if len(h.ActiveMemoryBackends) < 2 {
		for _, vs := range spec.Verifies {
			if isMemoryWhat(vs.What) {
				report.AddVerification(VerificationResult{
					What: vs.What, ReferenceBackend: refMem,
					ComparedBackend: "", Status: StatusSkip,
					Diffs: []DiffResult{{Path: "$",
						Kind: DiffMissingEntry, Severity: SeverityInfo,
						Message: "only one active memory backend, cannot cross-compare",
					}},
				})
			}
		}
	}
}

func isSessionWhat(w string) bool {
	switch w {
	case "session_full", "events", "state", "summary", "tracks":
		return true
	}
	return false
}

func isMemoryWhat(w string) bool {
	switch w {
	case "memories", "memory_search":
		return true
	}
	return false
}

// runMemoryVerifications handles memories and memory_search verifications.
func runMemoryVerifications(
	report *DiffReport,
	harness *Harness,
	comp *Comparator,
	normChain *NormalizerChain,
	spec *Spec,
	memorySnapshots map[string]map[string]*MemorySnapshot,
	refMemBackend string,
) error {
	for _, verifySpec := range spec.Verifies {
		switch verifySpec.What {
		case "memories", "memory_search":
			refMemSnap, ok := memorySnapshots[refMemBackend]
			if !ok {
				continue
			}
			normChain.Reset()
			refMemNorm, err := normChain.NormalizeMemory(refMemSnap[VerifyMemories])
			if err != nil {
				return fmt.Errorf("normalize reference memory %s: %w", refMemBackend, err)
			}
			for _, backendName := range harness.ActiveMemoryBackends {
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
					return fmt.Errorf("normalize memory %s: %w", backendName, err)
				}

				basePath := "$.memories"
				leftEntries := refMemNorm.Memories
				rightEntries := cmpMemNorm.Memories
				if verifySpec.What == "memory_search" {
					basePath = "$.search_results"
					normChain.Reset()
					refSearchNorm, err := normChain.NormalizeMemory(refMemSnap[VerifyMemorySearch])
					if err != nil {
						return fmt.Errorf("normalize reference search: %w", err)
					}
					normChain.Reset()
					cmpSearchNorm, err := normChain.NormalizeMemory(cmpMemSnap[VerifyMemorySearch])
					if err != nil {
						return fmt.Errorf("normalize compared search: %w", err)
					}
					leftEntries = refSearchNorm.SearchResults
					rightEntries = cmpSearchNorm.SearchResults
				}
				diffs := comp.CompareMemories(leftEntries, rightEntries, basePath)
				diffs = comp.filterAllowed(diffs, refMemBackend, backendName)
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
	return nil
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
		vr.SummaryFilterKeys = keys
	}
	// Collect track names.
	if len(snap.Session.Tracks) > 0 {
		names := make([]string, 0, len(snap.Session.Tracks))
		for k := range snap.Session.Tracks {
			names = append(names, string(k))
		}
		sort.Strings(names)
		vr.TrackNames = names
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
		vr.MemoryIDs = ids
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

// scopePrefix maps a verification "what" tag to the JSONPath prefix used by the Comparator.
var scopePrefix = map[string]string{
	"events":  "$.events",
	"state":   "$.state",
	"summary": "$.summaries",
	"tracks":  "$.tracks",
}

// filterDiffsByScope returns the subset of diffs whose path starts with the scope prefix matching the given what tag.
// session_full returns all diffs; events/state/summary/tracks return only diffs under the corresponding prefix.
func filterDiffsByScope(diffs []DiffResult, what string) []DiffResult {
	if what == "session_full" {
		return diffs
	}
	prefix, ok := scopePrefix[what]
	if !ok {
		prefix = "$." + what
	}
	var out []DiffResult
	for _, d := range diffs {
		if strings.HasPrefix(d.Path, prefix) {
			out = append(out, d)
		}
	}
	return out
}

// runSessionVerifications computes CompareSessions once per backend pair and distributes diffs by scope prefix (events/state/summary/tracks) to avoid duplicate reporting across multiple what tags.
func runSessionVerifications(
	report *DiffReport,
	harness *Harness,
	comp *Comparator,
	normChain *NormalizerChain,
	spec *Spec,
	sessionSnapshots map[string]map[string]*SessionSnapshot,
	refBackend string,
) error {
	sessionWhats := map[string]bool{}
	for _, vs := range spec.Verifies {
		switch vs.What {
		case "session_full", "events", "state", "summary", "tracks":
			sessionWhats[vs.What] = true
		}
	}
	if len(sessionWhats) == 0 {
		return nil
	}
	refSnap, ok := sessionSnapshots[refBackend]
	if !ok {
		return nil
	}
	normChain.Reset()
	refNorm, err := normChain.NormalizeSession(refSnap[VerifySessionFull])
	if err != nil {
		return fmt.Errorf("normalize reference session %s: %w", refBackend, err)
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
			return fmt.Errorf("normalize session %s: %w", backendName, err)
		}
		allDiffs := comp.CompareSessions(refNorm, cmpNorm, refBackend, backendName)

		for what := range sessionWhats {
			if what == "tracks" && (!harness.TrackSupported[refBackend] || !harness.TrackSupported[backendName]) {
				report.AddVerification(VerificationResult{
					What: "tracks", ReferenceBackend: refBackend,
					ComparedBackend: backendName, Status: StatusSkip,
					SessionKey: harness.sessionKey,
					Diffs: []DiffResult{{Path: "$.tracks", Kind: DiffMissingEntry,
						Severity: SeverityInfo,
						Message:  "one or both backends do not implement TrackService",
					}},
				})
				continue
			}
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
	}
	// Summary existence check (once, outside per-backend loop).
	for _, vs := range spec.Verifies {
		if vs.What == "summary" && len(vs.Expect) > 0 {
			expectDiffs := checkSummaryExpectations(refNorm, vs)
			if len(expectDiffs) > 0 {
				expectVr := VerificationResult{
					What: "summary_expect", ReferenceBackend: refBackend,
					ComparedBackend: refBackend, SessionKey: harness.sessionKey,
					Diffs: expectDiffs, Status: StatusFail,
				}
				populateSessionLocalization(&expectVr, refNorm)
				report.AddVerification(expectVr)
			}
		}
	}
	return nil
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
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write combined report: %w", err)
	}
	return nil
}
