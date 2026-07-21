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
	seenBackends, err := validateConsensusBackends(caseName, result.ComparableBackends, knownBackends)
	if err != nil {
		return err
	}
	seenPairs, err := validateConsensusPairs(caseName, result, diffs, seenBackends)
	if err != nil {
		return err
	}
	if err := validateConsensusEvidence(caseName, diffs, knownBackends, seenBackends, seenPairs); err != nil {
		return err
	}
	wantVerdict, wantOutliers := classifyConsensus(result.ComparableBackends, result.Pairs)
	if result.Verdict != wantVerdict || !slices.Equal(result.Outliers, wantOutliers) {
		return fmt.Errorf("replaytest: case %q consensus verdict is inconsistent", caseName)
	}
	return nil
}

func validateConsensusBackends(
	caseName string,
	backends []string,
	knownBackends map[string]struct{},
) (map[string]struct{}, error) {
	if !sort.StringsAreSorted(backends) {
		return nil, fmt.Errorf("replaytest: case %q consensus backends are not sorted", caseName)
	}
	seenBackends := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if _, ok := knownBackends[backend]; !ok {
			return nil, fmt.Errorf("replaytest: case %q consensus names unknown backend %q", caseName, backend)
		}
		if _, exists := seenBackends[backend]; exists {
			return nil, fmt.Errorf("replaytest: case %q consensus repeats backend %q", caseName, backend)
		}
		seenBackends[backend] = struct{}{}
	}
	return seenBackends, nil
}

type consensusPairKey struct {
	backendA string
	backendB string
}

func validateConsensusPairs(
	caseName string,
	result ConsensusResult,
	diffs []Diff,
	seenBackends map[string]struct{},
) (map[consensusPairKey]struct{}, error) {
	wantPairs := len(result.ComparableBackends) * (len(result.ComparableBackends) - 1) / 2
	if len(result.Pairs) != wantPairs {
		return nil, fmt.Errorf(
			"replaytest: case %q consensus has %d pairs, want %d",
			caseName,
			len(result.Pairs),
			wantPairs,
		)
	}
	seenPairs := make(map[consensusPairKey]struct{}, len(result.Pairs))
	for _, pair := range result.Pairs {
		key, err := validateConsensusPair(caseName, pair, diffs, seenBackends, seenPairs)
		if err != nil {
			return nil, err
		}
		seenPairs[key] = struct{}{}
	}
	return seenPairs, nil
}

func validateConsensusPair(
	caseName string,
	pair PairComparison,
	diffs []Diff,
	seenBackends map[string]struct{},
	seenPairs map[consensusPairKey]struct{},
) (consensusPairKey, error) {
	key := consensusPairKey{backendA: pair.BackendA, backendB: pair.BackendB}
	if pair.BackendA >= pair.BackendB {
		return key, fmt.Errorf("replaytest: case %q consensus pair is not ordered", caseName)
	}
	if _, ok := seenBackends[pair.BackendA]; !ok {
		return key, fmt.Errorf("replaytest: case %q consensus pair names unavailable backend %q", caseName, pair.BackendA)
	}
	if _, ok := seenBackends[pair.BackendB]; !ok {
		return key, fmt.Errorf("replaytest: case %q consensus pair names unavailable backend %q", caseName, pair.BackendB)
	}
	if _, exists := seenPairs[key]; exists {
		return key, fmt.Errorf("replaytest: case %q consensus repeats pair %q/%q", caseName, pair.BackendA, pair.BackendB)
	}
	blocking, allowed := countPairDiffs(diffs, key)
	if blocking != pair.BlockingDiffs || allowed != pair.AllowedDiffs {
		return key, fmt.Errorf("replaytest: case %q consensus pair counters do not add up", caseName)
	}
	return key, nil
}

func countPairDiffs(diffs []Diff, key consensusPairKey) (blocking, allowed int) {
	for _, diff := range diffs {
		if diff.BackendA != key.backendA || diff.BackendB != key.backendB {
			continue
		}
		if diff.Allowed {
			allowed++
		} else {
			blocking++
		}
	}
	return blocking, allowed
}

func validateConsensusEvidence(
	caseName string,
	diffs []Diff,
	knownBackends map[string]struct{},
	seenBackends map[string]struct{},
	seenPairs map[consensusPairKey]struct{},
) error {
	exclusionEvidence := make(map[string]struct{})
	for _, diff := range diffs {
		if diff.BackendA == diff.BackendB {
			if err := validateExclusionEvidence(caseName, diff, seenBackends); err != nil {
				return err
			}
			exclusionEvidence[diff.BackendA] = struct{}{}
			continue
		}
		if err := validatePairEvidence(caseName, diff, seenPairs); err != nil {
			return err
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
	return nil
}

func validateExclusionEvidence(caseName string, diff Diff, seenBackends map[string]struct{}) error {
	if _, comparable := seenBackends[diff.BackendA]; comparable {
		return fmt.Errorf(
			"replaytest: case %q comparable backend %q has a self diff",
			caseName,
			diff.BackendA,
		)
	}
	validExecution := diff.Path == "/execution" && !diff.Allowed
	_, capabilityEvidence := capabilityFromEvidencePath(diff.Path)
	validCapability := capabilityEvidence && diff.Allowed
	if !validExecution && !validCapability {
		return fmt.Errorf(
			"replaytest: case %q backend %q has invalid exclusion evidence",
			caseName,
			diff.BackendA,
		)
	}
	return nil
}

func validatePairEvidence(caseName string, diff Diff, seenPairs map[consensusPairKey]struct{}) error {
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
	key := consensusPairKey{backendA: diff.BackendA, backendB: diff.BackendB}
	if _, exists := seenPairs[key]; !exists {
		return fmt.Errorf(
			"replaytest: case %q diff names pair %q/%q outside the consensus matrix",
			caseName,
			diff.BackendA,
			diff.BackendB,
		)
	}
	return nil
}
