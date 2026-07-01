//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"encoding/json"
	"os"
	"time"
)

// ResultEntry is one classified diff in the report.
type ResultEntry struct {
	Backend       string  `json:"backend"`
	Category      string  `json:"category"`
	Locator       Locator `json:"locator"`
	FieldPath     string  `json:"fieldPath"`
	BaselineValue string  `json:"baselineValue,omitempty"`
	CompareValue  string  `json:"compareValue,omitempty"`
	Verdict       Verdict `json:"verdict"`
	Explanation   string  `json:"explanation,omitempty"`
}

// CaseReport aggregates all classified diffs for one replay case.
type CaseReport struct {
	Case      string        `json:"case"`
	SessionID string        `json:"sessionID"`
	Results   []ResultEntry `json:"results"`
}

// ReportSummary is the roll-up count across all cases.
type ReportSummary struct {
	Cases        int `json:"cases"`
	Compared     int `json:"compared"`
	Unsupported  int `json:"unsupported"`
	RealDiffs    int `json:"realDiffs"`
	AllowedDiffs int `json:"allowedDiffs"`
}

// Report is the top-level diff report emitted by the harness.
type Report struct {
	Mode            string        `json:"mode"`
	GeneratedAt     string        `json:"generatedAt"`
	BaselineBackend string        `json:"baselineBackend"`
	Backends        []string      `json:"backends"`
	Summary         ReportSummary `json:"summary"`
	Cases           []CaseReport  `json:"cases"`
}

// NewReport creates an empty report for the given mode, baseline, and backends.
func NewReport(mode, baseline string, backends []string) *Report {
	return &Report{
		Mode:            mode,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		BaselineBackend: baseline,
		Backends:        backends,
	}
}

// AddCase appends a case report and updates the roll-up counters by verdict.
func (r *Report) AddCase(cr CaseReport) {
	r.Cases = append(r.Cases, cr)
	r.Summary.Cases++
	for _, res := range cr.Results {
		r.Summary.Compared++
		switch res.Verdict {
		case VerdictInconsistent:
			r.Summary.RealDiffs++
		case VerdictAllowedDiff:
			r.Summary.AllowedDiffs++
		case VerdictUnsupported:
			r.Summary.Unsupported++
		}
	}
}

// HasInconsistent reports whether the named case has any real inconsistency.
func (r *Report) HasInconsistent(caseName string) bool {
	for _, c := range r.Cases {
		if c.Case != caseName {
			continue
		}
		for _, res := range c.Results {
			if res.Verdict == VerdictInconsistent {
				return true
			}
		}
	}
	return false
}

// InconsistentCount returns the total number of real inconsistencies.
func (r *Report) InconsistentCount() int {
	return r.Summary.RealDiffs
}

// WriteJSON writes the report as indented JSON to path.
func (r *Report) WriteJSON(path string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
