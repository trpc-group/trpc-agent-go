//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteReports atomically writes machine-readable and human-readable reports.
func WriteReports(outputDir string, report *Report) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON report: %w", err)
	}
	if err := atomicWrite(filepath.Join(outputDir, "optimization_report.json"), append(payload, '\n')); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(outputDir, "optimization_report.md"), []byte(renderMarkdown(report))); err != nil {
		return err
	}
	return nil
}

// WriteAcceptedPrompt writes one accepted text surface and rejects unsafe writeback.
func WriteAcceptedPrompt(path, surfaceID string, report *Report) error {
	if report == nil || !report.Accepted || report.AcceptedProfile == nil {
		return fmt.Errorf("no accepted profile is available for writeback")
	}
	for _, override := range report.AcceptedProfile.Overrides {
		if override.SurfaceID != surfaceID {
			continue
		}
		if override.Value.Text == nil {
			return fmt.Errorf("accepted surface %q is not text", surfaceID)
		}
		return atomicWrite(path, []byte(*override.Value.Text))
	}
	return fmt.Errorf("accepted profile does not contain surface %q", surfaceID)
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".optimization-report-*")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary report: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}

func renderMarkdown(report *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PromptIter Optimization Report\n\n")
	fmt.Fprintf(&b, "- Run status: `%s`\n- Baseline score: `%.4f`\n- Accepted: `%t`\n\n", report.RunStatus, report.BaselineScore, report.Accepted)
	b.WriteString("| Round | Candidate | Gate delta | New failures | Regressions | Decision |\n")
	b.WriteString("|---:|---:|---:|---:|---:|---|\n")
	for _, round := range report.Rounds {
		fmt.Fprintf(&b, "| %d | %.4f | %+.4f | %d | %d | %s |\n", round.Round,
			round.DeltaFromAccepted.CandidateScore, round.DeltaFromAccepted.ScoreDelta,
			round.DeltaFromAccepted.NewFailures, round.DeltaFromAccepted.Regressions,
			map[bool]string{true: "accept", false: "reject"}[round.Decision.Accepted])
	}
	b.WriteString("\n## Final decision\n\n")
	for _, reason := range report.FinalReasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	return b.String()
}
