//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func writeReports(outputDir string, report *optimizationReport) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON report: %w", err)
	}
	jsonData = append(jsonData, '\n')
	if err := atomicWrite(filepath.Join(outputDir, "optimization_report.json"), jsonData, 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := atomicWrite(
		filepath.Join(outputDir, "optimization_report.md"),
		[]byte(renderMarkdown(report)),
		0o644,
	); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".optimization-report-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func renderMarkdown(report *optimizationReport) string {
	var out bytes.Buffer
	decision := "REJECTED"
	if report.Gate.Accepted {
		decision = "ACCEPTED"
	}
	fmt.Fprintf(&out, "# Prompt Optimization Report\n\n")
	fmt.Fprintf(&out, "- Decision: **%s**\n", decision)
	fmt.Fprintf(&out, "- Mode: %s\n", markdownInlineCode(report.Mode))
	fmt.Fprintf(&out, "- Seed: `%d`\n", report.Seed)
	fmt.Fprintf(&out, "- Model: %s\n", markdownInlineCode(report.Model.Provider+"/"+report.Model.Name))
	fmt.Fprintf(&out, "- Fingerprint: `%s`\n", report.DeterministicFingerprint)
	fmt.Fprintf(&out, "- Duration: `%d ms`\n\n", report.DurationMillis)

	fmt.Fprintf(&out, "## Validation summary\n\n")
	fmt.Fprintf(&out, "| Metric | Baseline | Candidate | Delta |\n")
	fmt.Fprintf(&out, "|---|---:|---:|---:|\n")
	fmt.Fprintf(&out, "| Mean score | %.4f | %.4f | %+.4f |\n",
		report.Comparison.BaselineMeanScore,
		report.Comparison.CandidateMeanScore,
		report.Comparison.MeanScoreGain,
	)
	fmt.Fprintf(&out, "| Pass^%d rate | %.4f | %.4f | %+.4f |\n\n",
		report.Comparison.PassK,
		report.Comparison.BaselinePassPowerKRate,
		report.Comparison.CandidatePassPowerKRate,
		report.Comparison.CandidatePassPowerKRate-report.Comparison.BaselinePassPowerKRate,
	)
	fmt.Fprintf(&out, "Paired bootstrap 90%% CI: `[%.4f, %.4f]`.\n\n",
		report.Gate.ConfidenceInterval.Lower,
		report.Gate.ConfidenceInterval.Upper,
	)

	fmt.Fprintf(&out, "## Gate checks\n\n")
	fmt.Fprintf(&out, "| Check | Result | Observed | Requirement |\n")
	fmt.Fprintf(&out, "|---|---|---:|---:|\n")
	for _, check := range report.Gate.Checks {
		status := "PASS"
		if !check.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&out, "| %s | %s | %.4f | %s %.4f |\n",
			markdownTableCell(check.Name), status, check.Observed,
			markdownTableCell(check.Operator), check.Threshold)
	}

	fmt.Fprintf(&out, "\n## Per-case delta\n\n")
	fmt.Fprintf(&out, "| Case | Critical | Baseline | Candidate | Delta | Pass^%d |\n", report.Comparison.PassK)
	fmt.Fprintf(&out, "|---|---|---:|---:|---:|---|\n")
	for _, delta := range report.Comparison.Deltas {
		fmt.Fprintf(&out, "| %s | %t | %.4f | %.4f | %+.4f | %t -> %t |\n",
			markdownTableCell(delta.ID), delta.Critical, delta.BaselineMeanScore, delta.CandidateMeanScore,
			delta.ScoreDelta, delta.BaselinePassPowerK, delta.CandidatePassPowerK)
	}

	fmt.Fprintf(&out, "\n## Failure attribution\n\n")
	renderAttributionGroup(&out, "Train baseline", report.AttributionSummary.TrainBaseline)
	renderAttributionGroup(&out, "Train candidate", report.AttributionSummary.TrainCandidate)
	renderAttributionGroup(&out, "Validation baseline", report.AttributionSummary.ValidationBaseline)
	renderAttributionGroup(&out, "Validation candidate", report.AttributionSummary.ValidationCandidate)

	fmt.Fprintf(&out, "\n## Audit and anti-overfitting notes\n\n")
	fmt.Fprintf(&out, "PromptIter receives only the training set. The final decision uses the independent validation set, ")
	fmt.Fprintf(&out, "%d repeated runs, hard-failure vetoes, critical-case protection, Pass^k stability, a paired bootstrap interval, and resource budgets.\n\n",
		report.Comparison.PassK)
	prompt := strings.TrimSpace(report.SelectedPrompt)
	fence := markdownCodeFence(prompt)
	fmt.Fprintf(&out, "Selected prompt:\n\n%stext\n%s\n%s\n", fence, prompt, fence)
	return out.String()
}

func markdownTableCell(value string) string {
	value = html.EscapeString(value)
	return strings.NewReplacer(
		"\\", "\\\\",
		"|", "\\|",
		"`", "\\`",
		"\r\n", "<br>",
		"\r", "<br>",
		"\n", "<br>",
	).Replace(value)
}

func markdownInlineCode(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	fence := strings.Repeat("`", longestBacktickRun(value)+1)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		value = " " + value + " "
	}
	return fence + value + fence
}

func markdownCodeFence(value string) string {
	length := longestBacktickRun(value) + 1
	if length < 3 {
		length = 3
	}
	return strings.Repeat("`", length)
}

func longestBacktickRun(value string) int {
	longest, current := 0, 0
	for _, char := range value {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}

func renderAttributionGroup(out *bytes.Buffer, name string, summary map[FailureCategory]int) {
	fmt.Fprintf(out, "### %s\n\n", markdownTableCell(name))
	if len(summary) == 0 {
		fmt.Fprintln(out, "- No failed cases.")
		fmt.Fprintln(out)
		return
	}
	categories := make([]string, 0, len(summary))
	for category := range summary {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	for _, category := range categories {
		fmt.Fprintf(out, "- %s: %d\n", markdownInlineCode(category), summary[FailureCategory(category)])
	}
	fmt.Fprintln(out)
}
