//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"encoding/json"
	"time"
)

// BuildReport aggregates case results into a Report with summary counts.
func BuildReport(results []CaseResult) Report {
	var diffs []Diff
	caseSet := make(map[string]struct{})
	backendSet := make(map[string]struct{})
	allowed := 0
	for _, result := range results {
		caseSet[result.CaseName] = struct{}{}
		backendSet[result.Backend] = struct{}{}
		for _, diff := range result.Diffs {
			diffs = append(diffs, diff)
			if diff.AllowedDiff {
				allowed++
			}
		}
	}
	return Report{
		GeneratedAt: time.Now().UTC(),
		Cases:       sortedKeys(caseSet),
		Diffs:       diffs,
		Results:     results,
		Summary: ReportSummary{
			CasesRun:         len(caseSet),
			BackendsRun:      len(backendSet),
			DiffCount:        len(diffs),
			AllowedDiffCount: allowed,
		},
	}
}

// MarshalReport serializes a Report to indented JSON.
func MarshalReport(report Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}
