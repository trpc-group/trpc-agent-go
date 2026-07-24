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
	"sort"
	"strings"
	"time"
)

// DiffReport is the complete diff report for a replay test run.
type DiffReport struct {
	// GeneratedAt is the timestamp when the report was generated.
	GeneratedAt time.Time `json:"generatedAt"`
	// Summary is a human-readable summary of the findings.
	Summary string `json:"summary"`
	// TotalCases is the total number of cases run.
	TotalCases int `json:"totalCases"`
	// TotalDiffs is the total number of differences found.
	TotalDiffs int `json:"totalDiffs"`
	// AllowedDiffs is the number of differences within allowed tolerance.
	AllowedDiffs int `json:"allowedDiffs"`
	// UnallowedDiffs is the number of differences outside allowed tolerance.
	UnallowedDiffs int `json:"unallowedDiffs"`
	// CaseResults maps case name to per-case diff summary.
	CaseResults map[string]*CaseDiffSummary `json:"caseResults"`
	// Diffs is the full list of differences found.
	Diffs []DiffEntry `json:"diffs"`
}

// CaseDiffSummary summarizes the differences for a single case.
type CaseDiffSummary struct {
	// CaseName is the name of the case.
	CaseName string `json:"caseName"`
	// BackendPairs lists the backend pairs compared.
	BackendPairs []string `json:"backendPairs"`
	// DiffCount is the total number of diffs for this case.
	DiffCount int `json:"diffCount"`
	// AllowedDiffCount is the number of allowed diffs.
	AllowedDiffCount int `json:"allowedDiffCount"`
	// UnallowedDiffCount is the number of unallowed diffs.
	UnallowedDiffCount int `json:"unallowedDiffCount"`
}

// Reporter generates diff reports from comparison results.
type Reporter struct {
	normalizer *Normalizer
}

// NewReporter creates a new Reporter.
func NewReporter() *Reporter {
	return &Reporter{
		normalizer: NewNormalizer(),
	}
}

// GenerateReport creates a DiffReport from a collection of case diffs.
func (r *Reporter) GenerateReport(caseResults map[string][]DiffEntry) *DiffReport {
	report := &DiffReport{
		GeneratedAt: time.Now().UTC(),
		TotalCases:  len(caseResults),
		CaseResults: make(map[string]*CaseDiffSummary, len(caseResults)),
	}

	// Sort case names for deterministic output.
	caseNames := make([]string, 0, len(caseResults))
	for name := range caseResults {
		caseNames = append(caseNames, name)
	}
	sort.Strings(caseNames)

	var allDiffs []DiffEntry

	for _, name := range caseNames {
		diffs := caseResults[name]
		allDiffs = append(allDiffs, diffs...)

		// Collect unique backend pairs.
		pairSet := make(map[string]bool)
		allowedCount := 0
		for _, d := range diffs {
			pair := fmt.Sprintf("%s <-> %s", d.BackendA, d.BackendB)
			pairSet[pair] = true
			if d.AllowedDiff {
				allowedCount++
			}
		}
		pairs := make([]string, 0, len(pairSet))
		for p := range pairSet {
			pairs = append(pairs, p)
		}
		sort.Strings(pairs)

		report.CaseResults[name] = &CaseDiffSummary{
			CaseName:           name,
			BackendPairs:       pairs,
			DiffCount:          len(diffs),
			AllowedDiffCount:   allowedCount,
			UnallowedDiffCount: len(diffs) - allowedCount,
		}
	}

	report.Diffs = allDiffs
	report.TotalDiffs = len(allDiffs)

	allowedCount := 0
	for _, d := range allDiffs {
		if d.AllowedDiff {
			allowedCount++
		}
	}
	report.AllowedDiffs = allowedCount
	report.UnallowedDiffs = len(allDiffs) - allowedCount

	report.Summary = r.buildSummary(report)

	return report
}

// buildSummary generates a human-readable summary string.
func (r *Reporter) buildSummary(report *DiffReport) string {
	var b strings.Builder

	b.WriteString("Replay Consistency Test Report\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n", report.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Total cases: %d\n", report.TotalCases))
	b.WriteString(fmt.Sprintf("Total diffs: %d (allowed: %d, unallowed: %d)\n",
		report.TotalDiffs, report.AllowedDiffs, report.UnallowedDiffs))

	if report.UnallowedDiffs > 0 {
		b.WriteString(fmt.Sprintf("\n⚠️  %d unallowed difference(s) detected!\n", report.UnallowedDiffs))
		// Group unallowed diffs by case.
		caseDiffCount := make(map[string]int)
		for _, d := range report.Diffs {
			if !d.AllowedDiff {
				caseDiffCount[d.CaseName]++
			}
		}
		caseNames := make([]string, 0, len(caseDiffCount))
		for name := range caseDiffCount {
			caseNames = append(caseNames, name)
		}
		sort.Strings(caseNames)
		for _, name := range caseNames {
			b.WriteString(fmt.Sprintf("  - %s: %d unallowed diff(s)\n", name, caseDiffCount[name]))
		}
	} else {
		b.WriteString("\n✅ All backends are consistent (within allowed tolerances).\n")
	}

	return b.String()
}

// ToJSON serializes the report to indented JSON.
func (r *Reporter) ToJSON(report *DiffReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// ToText generates a human-readable text report.
func (r *Reporter) ToText(report *DiffReport) string {
	var b strings.Builder

	b.WriteString(report.Summary)
	b.WriteString("\n")

	if len(report.Diffs) == 0 {
		b.WriteString("No differences found.\n")
		return b.String()
	}

	b.WriteString("\n--- Detailed Differences ---\n\n")

	// Group diffs by case.
	caseDiffs := make(map[string][]DiffEntry)
	for _, d := range report.Diffs {
		caseDiffs[d.CaseName] = append(caseDiffs[d.CaseName], d)
	}

	caseNames := make([]string, 0, len(caseDiffs))
	for name := range caseDiffs {
		caseNames = append(caseNames, name)
	}
	sort.Strings(caseNames)

	for _, name := range caseNames {
		b.WriteString(fmt.Sprintf("=== Case: %s ===\n", name))
		for i, d := range caseDiffs[name] {
			mark := " "
			if !d.AllowedDiff {
				mark = "⚠"
			}
			b.WriteString(fmt.Sprintf("  %s [%d] %s\n", mark, i+1, d.FieldPath))
			b.WriteString(fmt.Sprintf("       %s <-> %s\n", d.BackendA, d.BackendB))
			b.WriteString(fmt.Sprintf("       Baseline: %v\n", d.Baseline))
			b.WriteString(fmt.Sprintf("       Actual:   %v\n", d.Actual))
			if d.DiffReason != "" {
				b.WriteString(fmt.Sprintf("       Reason: %s\n", d.DiffReason))
			}
			if d.SessionID != "" {
				b.WriteString(fmt.Sprintf("       SessionID: %s\n", d.SessionID))
			}
			if d.SummaryKey != "" {
				b.WriteString(fmt.Sprintf("       SummaryKey: %s\n", d.SummaryKey))
			}
			if d.TrackName != "" {
				b.WriteString(fmt.Sprintf("       TrackName: %s\n", d.TrackName))
			}
			if d.MemoryID != "" {
				b.WriteString(fmt.Sprintf("       MemoryID: %s\n", d.MemoryID))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}
