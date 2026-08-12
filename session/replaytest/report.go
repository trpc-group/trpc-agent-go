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
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// BuildReport aggregates case results into a ReplayReport.
func BuildReport(results []CaseResult, backendNames []string) *ReplayReport {
	r := &ReplayReport{
		Timestamp:    time.Now().UTC(),
		TotalCases:   len(results),
		CaseResults:  results,
		BackendNames: backendNames,
	}
	for _, cr := range results {
		if cr.HasDiff {
			r.FailCases++
		} else {
			r.PassCases++
		}
		r.TotalDiffs += cr.DiffCount
		r.AllowedDiffs += cr.AllowedDiffCount
	}
	return r
}

// WriteReport serializes the report to a JSON file.
func WriteReport(path string, report *ReplayReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// HasFailures returns true if any case has non-allowed differences.
func HasFailures(report *ReplayReport) bool {
	return report.FailCases > 0
}

// Summary returns a one-line summary of the report.
func (r *ReplayReport) Summary() string {
	return fmt.Sprintf("replay: %d/%d cases passed, %d total diffs (%d allowed)",
		r.PassCases, r.TotalCases, r.TotalDiffs, r.AllowedDiffs)
}
