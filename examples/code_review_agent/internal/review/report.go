//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type stagedReport struct {
	tempDir  string
	finalDir string
}

func (s stagedReport) cleanup() { _ = os.RemoveAll(s.tempDir) }

func (s stagedReport) commit() error {
	if _, err := os.Stat(s.finalDir); err == nil {
		return fmt.Errorf("report directory already exists: %s", s.finalDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(s.tempDir, s.finalDir)
}

func publish(report Report, outputDir string) (Report, ReportPaths, error) {
	report, paths, staged, err := stageReport(report, outputDir)
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	defer staged.cleanup()
	if err := staged.commit(); err != nil {
		return Report{}, ReportPaths{}, err
	}
	return report, paths, nil
}

func stageReport(report Report, outputDir string) (Report, ReportPaths, stagedReport, error) {
	taskDir := filepath.Join(outputDir, report.Task.ID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return Report{}, ReportPaths{}, stagedReport{}, err
	}
	finalDir := filepath.Join(taskDir, "report")
	tempDir, err := os.MkdirTemp(taskDir, ".report-stage-*")
	if err != nil {
		return Report{}, ReportPaths{}, stagedReport{}, err
	}
	staged := stagedReport{tempDir: tempDir, finalDir: finalDir}
	fail := func(err error) (Report, ReportPaths, stagedReport, error) {
		staged.cleanup()
		return Report{}, ReportPaths{}, stagedReport{}, err
	}
	jsonPath := filepath.Join(finalDir, "review_report.json")
	markdownPath := filepath.Join(finalDir, "review_report.md")
	report.Artifacts = append(report.Artifacts,
		Artifact{Name: "review_report.json", Path: jsonPath, MIMEType: "application/json"},
		Artifact{Name: "review_report.md", Path: markdownPath, MIMEType: "text/markdown"},
	)
	for attempts := 0; attempts < 8; attempts++ {
		jsonData, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fail(err)
		}
		jsonData = append(jsonData, '\n')
		markdown := renderMarkdown(report)
		if len(jsonData) > maxArtifactBytes || len(markdown) > maxArtifactBytes {
			return fail(errors.New("report exceeds artifact limit"))
		}
		jsonIndex, markdownIndex := len(report.Artifacts)-2, len(report.Artifacts)-1
		if report.Artifacts[jsonIndex].SizeBytes == int64(len(jsonData)) && report.Artifacts[markdownIndex].SizeBytes == int64(len(markdown)) {
			if err := atomicWrite(filepath.Join(tempDir, "review_report.json"), jsonData); err != nil {
				return fail(err)
			}
			if err := atomicWrite(filepath.Join(tempDir, "review_report.md"), markdown); err != nil {
				return fail(err)
			}
			return report, ReportPaths{JSON: jsonPath, Markdown: markdownPath}, staged, nil
		}
		report.Artifacts[jsonIndex].SizeBytes = int64(len(jsonData))
		report.Artifacts[markdownIndex].SizeBytes = int64(len(markdown))
	}
	return fail(errors.New("report artifact sizes did not converge"))
}

func atomicWrite(target string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(target), ".review-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(name, target)
}

func renderMarkdown(report Report) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Code Review Report\n\nTask: %s\n\nStatus: %s\n\nMode: %s\n\n", markdownText(report.Task.ID), markdownText(string(report.Task.Status)), markdownText(string(report.Mode)))
	fmt.Fprintf(&b, "## Summary\n\n- Findings: %d\n- Warnings: %d\n- Needs human review: %d\n- Files changed: %d\n- Sandbox runs: %d\n\n", len(report.Findings), len(report.Warnings), len(report.NeedsHumanReview), report.Input.FilesChanged, len(report.SandboxRuns))
	renderFindingTable(&b, "Findings", report.Findings)
	renderFindingTable(&b, "Warnings", report.Warnings)
	renderFindingTable(&b, "Human review", report.NeedsHumanReview)
	b.WriteString("## Governance decisions\n\n")
	for _, item := range report.PermissionDecisions {
		fmt.Fprintf(&b, "- %s: %s", markdownText(item.Command), markdownText(string(item.Action)))
		if item.Reason != "" {
			fmt.Fprintf(&b, " - %s", markdownText(item.Reason))
		}
		b.WriteByte('\n')
	}
	b.WriteString("## Filter decisions\n\n")
	for _, item := range report.FilterDecisions {
		fmt.Fprintf(&b, "- %s: %s to %s - %s\n", markdownText(item.Fingerprint), markdownText(string(item.Action)), markdownText(item.TargetBucket), markdownText(item.Reason))
	}
	b.WriteString("\n## Sandbox summary\n\n")
	for _, run := range report.SandboxRuns {
		fmt.Fprintf(&b, "- %s %s: %s; exit=%d; timeout=%t; duration=%dms; error=%s\n", markdownText(run.Command), markdownText(stringsJoin(run.Args)), markdownText(string(run.Status)), run.ExitCode, run.TimedOut, run.DurationMS, markdownText(string(run.ErrorType)))
	}
	b.WriteString("\n## Monitoring\n\n")
	fmt.Fprintf(&b, "- Total duration: %dms\n- Sandbox duration: %dms\n- Tool calls: %d\n- Permission denies: %d\n- Permission asks: %d\n", report.Metrics.TotalDurationMS, report.Metrics.SandboxDurationMS, report.Metrics.ToolCallCount, report.Metrics.PermissionDenyCount, report.Metrics.PermissionAskCount)
	keys := make([]string, 0, len(report.Metrics.SeverityDistribution))
	for key := range report.Metrics.SeverityDistribution {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&b, "- Severity %s: %d\n", markdownText(key), report.Metrics.SeverityDistribution[key])
	}
	fmt.Fprintf(&b, "\n## Conclusion\n\n%s\n", markdownText(report.Conclusion))
	return b.Bytes()
}

func renderFindingTable(b *bytes.Buffer, title string, values []Finding) {
	fmt.Fprintf(b, "## %s\n\n", markdownText(title))
	if len(values) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("| Severity | Category | Location | Issue | Recommendation |\n|---|---|---|---|---|\n")
	for _, item := range values {
		fmt.Fprintf(b, "| %s | %s | %s:%d | %s - %s | %s |\n", escapeCell(string(item.Severity)), escapeCell(item.Category), escapeCell(item.File), item.Line, escapeCell(item.Title), escapeCell(item.Evidence), escapeCell(item.Recommendation))
	}
	b.WriteString("\n")
}

func markdownText(value string) string {
	value = redact(value)
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "`", "&#96;")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func escapeCell(value string) string {
	value = markdownText(value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "|", "\\|")
}

func stringsJoin(values []string) string {
	var b bytes.Buffer
	for index, value := range values {
		if index > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(value)
	}
	return b.String()
}
