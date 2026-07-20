//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

func compareByConsensus(
	caseName string,
	snapshots map[string]Snapshot,
	allowed []AllowedDiff,
) ([]Diff, ConsensusResult, error) {
	names := make([]string, 0, len(snapshots))
	for name := range snapshots {
		names = append(names, name)
	}
	sort.Strings(names)

	result := ConsensusResult{
		ComparableBackends: names,
		Pairs:              make([]PairComparison, 0, len(names)*(len(names)-1)/2),
	}
	var diffs []Diff
	for left := 0; left < len(names); left++ {
		for right := left + 1; right < len(names); right++ {
			backendA, backendB := names[left], names[right]
			pairDiffs, err := Compare(
				caseName,
				snapshots[backendA],
				snapshots[backendB],
				allowed,
			)
			if err != nil {
				return nil, ConsensusResult{}, err
			}
			blocking, allowedCount := countDiffs(pairDiffs)
			result.Pairs = append(result.Pairs, PairComparison{
				BackendA:      backendA,
				BackendB:      backendB,
				BlockingDiffs: blocking,
				AllowedDiffs:  allowedCount,
			})
			diffs = append(diffs, pairDiffs...)
		}
	}
	result.Verdict, result.Outliers = classifyConsensus(result.ComparableBackends, result.Pairs)
	return diffs, result, nil
}

func classifyConsensus(
	backends []string,
	pairs []PairComparison,
) (ConsensusVerdict, []string) {
	if len(backends) < 2 {
		return ConsensusInsufficient, nil
	}
	allAgree := true
	for _, pair := range pairs {
		if pair.BlockingDiffs > 0 {
			allAgree = false
			break
		}
	}
	if allAgree {
		return ConsensusUnanimous, nil
	}
	if len(backends) < 3 {
		return ConsensusAmbiguous, nil
	}

	var outliers []string
	for _, candidate := range backends {
		candidateDisagrees := true
		othersAgree := true
		for _, pair := range pairs {
			involvesCandidate := pair.BackendA == candidate || pair.BackendB == candidate
			if involvesCandidate && pair.BlockingDiffs == 0 {
				candidateDisagrees = false
			}
			if !involvesCandidate && pair.BlockingDiffs > 0 {
				othersAgree = false
			}
		}
		if candidateDisagrees && othersAgree {
			outliers = append(outliers, candidate)
		}
	}
	if len(outliers) == 1 {
		return ConsensusOutlier, outliers
	}
	return ConsensusAmbiguous, nil
}

func validateConsensusResult(
	caseName string,
	result ConsensusResult,
	diffs []Diff,
	knownBackends map[string]struct{},
) error {
	if !sort.StringsAreSorted(result.ComparableBackends) {
		return fmt.Errorf("replaytest: case %q consensus backends are not sorted", caseName)
	}
	seenBackends := make(map[string]struct{}, len(result.ComparableBackends))
	for _, backend := range result.ComparableBackends {
		if _, ok := knownBackends[backend]; !ok {
			return fmt.Errorf("replaytest: case %q consensus names unknown backend %q", caseName, backend)
		}
		if _, exists := seenBackends[backend]; exists {
			return fmt.Errorf("replaytest: case %q consensus repeats backend %q", caseName, backend)
		}
		seenBackends[backend] = struct{}{}
	}
	wantPairs := len(result.ComparableBackends) * (len(result.ComparableBackends) - 1) / 2
	if len(result.Pairs) != wantPairs {
		return fmt.Errorf(
			"replaytest: case %q consensus has %d pairs, want %d",
			caseName,
			len(result.Pairs),
			wantPairs,
		)
	}
	seenPairs := make(map[string]struct{}, len(result.Pairs))
	for _, pair := range result.Pairs {
		if pair.BackendA >= pair.BackendB {
			return fmt.Errorf("replaytest: case %q consensus pair is not ordered", caseName)
		}
		if _, ok := seenBackends[pair.BackendA]; !ok {
			return fmt.Errorf("replaytest: case %q consensus pair names unavailable backend %q", caseName, pair.BackendA)
		}
		if _, ok := seenBackends[pair.BackendB]; !ok {
			return fmt.Errorf("replaytest: case %q consensus pair names unavailable backend %q", caseName, pair.BackendB)
		}
		key := pair.BackendA + "\x00" + pair.BackendB
		if _, exists := seenPairs[key]; exists {
			return fmt.Errorf("replaytest: case %q consensus repeats pair %q/%q", caseName, pair.BackendA, pair.BackendB)
		}
		seenPairs[key] = struct{}{}
		var blocking, allowed int
		for _, diff := range diffs {
			if diff.BackendA == pair.BackendA && diff.BackendB == pair.BackendB {
				if diff.Allowed {
					allowed++
				} else {
					blocking++
				}
			}
		}
		if blocking != pair.BlockingDiffs || allowed != pair.AllowedDiffs {
			return fmt.Errorf("replaytest: case %q consensus pair counters do not add up", caseName)
		}
	}
	exclusionEvidence := make(map[string]struct{})
	for _, diff := range diffs {
		if diff.BackendA == diff.BackendB {
			if _, comparable := seenBackends[diff.BackendA]; comparable {
				return fmt.Errorf(
					"replaytest: case %q comparable backend %q has a self diff",
					caseName,
					diff.BackendA,
				)
			}
			validExecution := diff.Path == "/execution" && !diff.Allowed
			validCapability := strings.HasPrefix(diff.Path, "/capabilities/") && diff.Allowed
			if !validExecution && !validCapability {
				return fmt.Errorf(
					"replaytest: case %q backend %q has invalid exclusion evidence",
					caseName,
					diff.BackendA,
				)
			}
			exclusionEvidence[diff.BackendA] = struct{}{}
			continue
		}
		if diff.Path == "/execution" || strings.HasPrefix(diff.Path, "/capabilities/") {
			return fmt.Errorf(
				"replaytest: case %q uses reserved evidence path %q inside the consensus matrix",
				caseName,
				diff.Path,
			)
		}
		if diff.BackendA >= diff.BackendB {
			return fmt.Errorf("replaytest: case %q diff names a non-canonical consensus pair", caseName)
		}
		key := diff.BackendA + "\x00" + diff.BackendB
		if _, exists := seenPairs[key]; !exists {
			return fmt.Errorf(
				"replaytest: case %q diff names pair %q/%q outside the consensus matrix",
				caseName,
				diff.BackendA,
				diff.BackendB,
			)
		}
	}
	for backend := range knownBackends {
		if _, comparable := seenBackends[backend]; comparable {
			continue
		}
		if _, explained := exclusionEvidence[backend]; !explained {
			return fmt.Errorf(
				"replaytest: case %q excludes backend %q without execution or capability evidence",
				caseName,
				backend,
			)
		}
	}
	wantVerdict, wantOutliers := classifyConsensus(result.ComparableBackends, result.Pairs)
	if result.Verdict != wantVerdict || !slices.Equal(result.Outliers, wantOutliers) {
		return fmt.Errorf("replaytest: case %q consensus verdict is inconsistent", caseName)
	}
	return nil
}
