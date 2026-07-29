//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var reportWriteMu sync.Mutex

// BuildReport aggregates case results without losing mixed or inconclusive states.
func BuildReport(baseline string, backends []string, cases []CaseReport) Report {
	report := Report{
		Version: 1, GeneratedAt: time.Now().UTC(), Baseline: baseline,
		Backends: append([]string(nil), backends...), TotalCases: len(cases),
		Cases: append([]CaseReport(nil), cases...),
	}
	for _, result := range cases {
		switch result.Status {
		case StatusPassed:
			report.PassedCases++
		case StatusFailed:
			report.FailedCases++
		case StatusSkipped:
			report.SkippedCases++
		case StatusMixed:
			report.MixedCases++
		case StatusInconclusive:
			report.Inconclusive++
		default:
			report.FailedCases++
		}
		for _, diff := range result.Diffs {
			if diff.AllowedDiff {
				report.AllowedDiffs++
			} else {
				report.BlockingDiffs++
			}
		}
	}
	return report
}

// Healthy reports whether the aggregate has no failed or inconclusive comparisons.
func (r Report) Healthy() bool {
	return r.FailedCases == 0 && r.BlockingDiffs == 0 && r.Inconclusive == 0
}

// WriteReport writes an indented JSON report through one process-wide writer,
// fsyncs the temporary file, and publishes it with rename.
func WriteReport(path string, report Report) error {
	if path == "" {
		return fmt.Errorf("report path is empty")
	}
	reportWriteMu.Lock()
	defer reportWriteMu.Unlock()
	if report.Version == 0 {
		report.Version = 1
	}
	if report.GeneratedAt.IsZero() {
		report.GeneratedAt = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal replay report: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create replay report directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".replay-report-*.tmp")
	if err != nil {
		return fmt.Errorf("create replay report temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set replay report permissions: %w", err)
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write replay report: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync replay report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close replay report: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		// Windows cannot replace an existing destination with os.Rename.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace replay report: remove destination: %w", removeErr)
		}
		if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
			return fmt.Errorf("publish replay report: %w", renameErr)
		}
	}
	return nil
}
