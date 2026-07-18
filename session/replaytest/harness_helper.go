// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"fmt"
)

// collectCaseSnapshots runs the case on each capable backend and returns
// normalized snapshots keyed by backend name.
func (h *Harness) collectCaseSnapshots(
	ctx context.Context,
	tc ReplayCase,
	cr *CaseResult,
) (map[string]*Snapshot, map[string]BackendProfile, error) {
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
			return nil, nil, err
		}
		norm, err := h.normalizer.Normalize(raw)
		if err != nil {
			return nil, nil, err
		}
		snaps[b.Name] = norm
		profiles[b.Name] = b.Profile
	}
	return snaps, profiles, nil
}

// buildComparisonPairs selects backend pairs according to ComparisonMode.
func buildComparisonPairs(
	mode ComparisonMode,
	reference string,
	snaps map[string]*Snapshot,
) [][2]string {
	switch mode {
	case ComparisonAllPairs:
		return allPairs(snaps)
	default:
		return referencePairs(reference, snaps)
	}
}

func allPairs(snaps map[string]*Snapshot) [][2]string {
	names := make([]string, 0, len(snaps))
	for n := range snaps {
		names = append(names, n)
	}
	var pairs [][2]string
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			pairs = append(pairs, [2]string{names[i], names[j]})
		}
	}
	return pairs
}

func referencePairs(reference string, snaps map[string]*Snapshot) [][2]string {
	ref := reference
	if _, ok := snaps[ref]; !ok {
		// pick first as reference
		for n := range snaps {
			ref = n
			break
		}
	}
	var pairs [][2]string
	for n := range snaps {
		if n == ref {
			continue
		}
		pairs = append(pairs, [2]string{ref, n})
	}
	return pairs
}

// compareSnapshotPairs runs the comparator for each backend pair.
func (h *Harness) compareSnapshotPairs(
	tc ReplayCase,
	snaps map[string]*Snapshot,
	profiles map[string]BackendProfile,
	pairs [][2]string,
) []Diff {
	var diffs []Diff
	for _, p := range pairs {
		d := h.comparator.Compare(tc, snaps[p[0]], snaps[p[1]], profiles[p[0]], profiles[p[1]])
		diffs = append(diffs, d...)
	}
	return diffs
}

// finalizeCaseStatus sets passed/failed/skipped on the case result from diffs.
func finalizeCaseStatus(cr *CaseResult, diffs []Diff) {
	cr.Diffs = diffs
	if hasErrorDiff(diffs) {
		cr.Status = StatusFailed
		return
	}
	if cr.Status == StatusSkipped {
		// keep skipped if any backend skipped and no errors
		return
	}
	cr.Status = StatusPassed
}

// applySingleBackendStatus sets status when only one backend produced a snapshot.
func applySingleBackendStatus(cr *CaseResult) {
	// Single-backend self-check: pass when only one backend executed,
	// but keep StatusSkipped if any other backend was capability-skipped.
	if cr.Status != StatusSkipped {
		cr.Status = StatusPassed
	}
}
