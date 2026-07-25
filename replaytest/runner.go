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

// RunSpec executes a single spec against all configured backends and returns
// a diff report.
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
	// For concurrent specs, add an event-order normalizer so that
	// goroutine scheduling differences across backends do not produce
	// false order-mismatch diffs.
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
	for _, verifySpec := range spec.Verifies {
		switch verifySpec.What {
		case "session_full", "events", "state", "summary", "tracks":
			refSnap, ok := sessionSnapshots[refBackend]
			if !ok {
				continue
			}
			refNorm, err := normChain.NormalizeSession(refSnap[VerifySessionFull])
			if err != nil {
				return nil, fmt.Errorf("normalize reference session %s: %w", refBackend, err)
			}
			for _, backendName := range spec.Backends.Session {
				if backendName == refBackend {
					continue
				}
				cmpSnap, ok := sessionSnapshots[backendName]
				if !ok {
					continue
				}
				cmpNorm, err := normChain.NormalizeSession(cmpSnap[VerifySessionFull])
				if err != nil {
					return nil, fmt.Errorf("normalize session %s: %w", backendName, err)
				}

				diffs := comp.CompareSessions(refNorm, cmpNorm, refBackend, backendName)
				vr := VerificationResult{
					What:             verifySpec.What,
					ReferenceBackend: refBackend,
					ComparedBackend:  backendName,
					SessionKey:       harness.sessionKey,
					Diffs:            diffs,
				}
				// Populate localization fields from reference snapshot.
				populateSessionLocalization(&vr, refNorm)
				if len(diffs) == 0 {
					vr.Status = StatusPass
				} else {
					vr.Status = StatusFail
				}
				report.AddVerification(vr)
			}

		case "memories", "memory_search":
			refMemSnap, ok := memorySnapshots[refMemBackend]
			if !ok {
				continue
			}
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
				cmpMemNorm, err := normChain.NormalizeMemory(cmpMemSnap[VerifyMemories])
				if err != nil {
					return nil, fmt.Errorf("normalize memory %s: %w", backendName, err)
				}

				basePath := "$.memories"
				if verifySpec.What == "memory_search" {
					basePath = "$.search_results"
					if refMemSnap[VerifyMemorySearch] != nil && cmpMemSnap[VerifyMemorySearch] != nil {
						refSearchNorm, _ := normChain.NormalizeMemory(refMemSnap[VerifyMemorySearch])
						cmpSearchNorm, _ := normChain.NormalizeMemory(cmpMemSnap[VerifyMemorySearch])
						refMemNorm = refSearchNorm
						cmpMemNorm = cmpSearchNorm
					}
				}

				diffs := comp.CompareMemories(refMemNorm.Memories, cmpMemNorm.Memories, basePath)
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

// populateSessionLocalization extracts summary filter keys and track names
// from the reference session snapshot for diff report localization.
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

// populateMemoryLocalization extracts memory IDs from the reference memory
// snapshot for diff report localization.
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
