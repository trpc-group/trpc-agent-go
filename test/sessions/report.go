//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MutationResult records whether deliberate corruption was detected.
type MutationResult struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Detected    bool         `json:"detected"`
	Differences []Difference `json:"differences,omitempty"`
}

// CaseReport combines backend runs, comparisons, and mutation checks.
type CaseReport struct {
	CaseID      string             `json:"case_id"`
	Description string             `json:"description,omitempty"`
	Status      string             `json:"status"`
	Error       string             `json:"error,omitempty"`
	Runs        []ReplayResult     `json:"runs"`
	Comparisons []ComparisonResult `json:"comparisons"`
	Mutations   []MutationResult   `json:"mutations,omitempty"`
}

// ReportSummary contains acceptance-oriented aggregate metrics.
type ReportSummary struct {
	CaseCount             int     `json:"case_count"`
	BackendCount          int     `json:"backend_count"`
	ComparisonCount       int     `json:"comparison_count"`
	UnexpectedDiffCount   int     `json:"unexpected_diff_count"`
	AllowedDiffCount      int     `json:"allowed_diff_count"`
	MutationCount         int     `json:"mutation_count"`
	DetectedMutationCount int     `json:"detected_mutation_count"`
	MutationDetectionRate float64 `json:"mutation_detection_rate"`
	DurationMS            int64   `json:"duration_ms"`
}

// ReplayReport is the generated JSON artifact.
type ReplayReport struct {
	Status      string        `json:"status"`
	Error       string        `json:"error,omitempty"`
	GeneratedAt time.Time     `json:"generated_at"`
	Summary     ReportSummary `json:"summary"`
	Cases       []CaseReport  `json:"cases"`
}

// BuildReport computes report status and metrics.
func BuildReport(startedAt time.Time, cases []CaseReport) ReplayReport {
	report := ReplayReport{
		Status: "passed", GeneratedAt: time.Now().UTC(),
		Cases: cases, Summary: ReportSummary{CaseCount: len(cases)},
	}
	backendNames := make(map[string]struct{})
	for ci := range report.Cases {
		item := &report.Cases[ci]
		item.Status = "passed"
		if item.Error != "" {
			item.Status = "failed"
		}
		for _, run := range item.Runs {
			backendNames[run.Backend] = struct{}{}
			if run.Error != "" {
				item.Status = "failed"
			}
		}
		for _, comparison := range item.Comparisons {
			report.Summary.ComparisonCount++
			for _, diff := range comparison.Differences {
				if diff.Allowed {
					report.Summary.AllowedDiffCount++
				} else {
					report.Summary.UnexpectedDiffCount++
					item.Status = "failed"
				}
			}
		}
		for _, mutation := range item.Mutations {
			report.Summary.MutationCount++
			if mutation.Detected {
				report.Summary.DetectedMutationCount++
			} else {
				item.Status = "failed"
			}
		}
		if item.Status != "passed" {
			report.Status = "failed"
		}
	}
	if report.Error != "" {
		report.Status = "failed"
	}
	report.Summary.BackendCount = len(backendNames)
	if report.Summary.MutationCount > 0 {
		report.Summary.MutationDetectionRate =
			float64(report.Summary.DetectedMutationCount) /
				float64(report.Summary.MutationCount)
	}
	report.Summary.DurationMS = time.Since(startedAt).Milliseconds()
	return report
}

// WriteReport atomically writes an indented JSON report.
func WriteReport(path string, report ReplayReport) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".replay-report-*.json")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("encode report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temporary report: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}

// FormatReportSummary returns a compact test failure message.
func FormatReportSummary(report ReplayReport) string {
	return fmt.Sprintf(
		"status=%s cases=%d backends=%d unexpected_diffs=%d mutations=%d/%d duration=%dms",
		report.Status,
		report.Summary.CaseCount,
		report.Summary.BackendCount,
		report.Summary.UnexpectedDiffCount,
		report.Summary.DetectedMutationCount,
		report.Summary.MutationCount,
		report.Summary.DurationMS,
	)
}
