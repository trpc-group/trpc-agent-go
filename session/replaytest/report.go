//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"io"
	"time"
)

// Reporter writes replay reports.
type Reporter struct {
	writer io.Writer
}

// NewReporter creates a JSON reporter writing to w.
func NewReporter(w io.Writer) *Reporter {
	return &Reporter{writer: w}
}

// Write writes report as indented JSON.
func (r *Reporter) Write(report *Report) error {
	enc := json.NewEncoder(r.writer)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// BuildReport aggregates case results into a report.
func BuildReport(results []CaseResult, backends []string, reference string) *Report {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Reference:   reference,
		Backends:    append([]string(nil), backends...),
		TotalCases:  len(results),
		Results:     append([]CaseResult(nil), results...),
	}
	for _, result := range results {
		switch result.OverallStatus {
		case StatusPassed:
			report.PassedCases++
		case StatusSkipped:
			report.SkippedCases++
		case StatusFailed:
			report.FailedCases++
		default:
			report.FailedCases++
		}
		for _, cmp := range result.Comparisons {
			report.Unsupported = append(report.Unsupported, cmp.Unsupported...)
			for _, diff := range cmp.Diffs {
				report.TotalDiffs++
				if diff.Severity == SeverityAllowed {
					report.AllowedDiffs++
				} else {
					report.ErrorDiffs++
				}
			}
		}
	}
	return report
}
