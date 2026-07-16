// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"encoding/json"
	"io"
	"time"
)

// BuildReport aggregates case results into a report.
func BuildReport(results []CaseResult, flatDiffs []Diff, backends []string, opts HarnessOpts) *Report {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Mode:        opts.Mode,
		Reference:   opts.ReferenceBackend,
		Backends:    append([]string(nil), backends...),
		TotalCases:  len(results),
		Results:     append([]CaseResult(nil), results...),
		Diffs:       append([]Diff(nil), flatDiffs...),
	}
	for _, r := range results {
		switch r.Status {
		case StatusPassed:
			report.PassedCases++
		case StatusSkipped:
			report.SkippedCases++
		default:
			report.FailedCases++
		}
	}
	return report
}

// WriteReportJSON writes indented JSON to w.
func WriteReportJSON(w io.Writer, report *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// ErrorDiffCount returns the number of non-allowed diffs.
func ErrorDiffCount(diffs []Diff) int {
	n := 0
	for _, d := range diffs {
		if !d.Allowed {
			n++
		}
	}
	return n
}
