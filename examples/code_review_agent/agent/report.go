package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func BuildMonitoring(taskID string, started time.Time, findings, warnings, needsHuman []Finding, decisions []PermissionDecision, runs []SandboxRun) MonitoringSummary {
	summary := MonitoringSummary{
		TaskID:                taskID,
		TotalDurationMS:       time.Since(started).Milliseconds(),
		SeverityDistribution:  map[string]int{},
		ExceptionDistribution: map[string]int{},
		CreatedAt:             time.Now().UTC(),
		FindingCount:          len(findings),
		WarningCount:          len(warnings),
		NeedsHumanReviewCount: len(needsHuman),
		ToolCallCount:         len(runs),
	}
	for _, decision := range decisions {
		if decision.Decision != DecisionAllow {
			summary.PermissionIntercepts++
		}
	}
	for _, run := range runs {
		summary.SandboxDurationMS += run.DurationMS
		if run.ErrorType != "" {
			summary.ExceptionDistribution[run.ErrorType]++
		}
	}
	for _, f := range findings {
		summary.SeverityDistribution[f.Severity]++
	}
	for _, f := range warnings {
		summary.SeverityDistribution[f.Severity]++
	}
	for _, f := range needsHuman {
		summary.SeverityDistribution[f.Severity]++
	}
	return summary
}

func WriteReports(outDir string, report ReviewReport, redactor Redactor) (ReviewReport, []Artifact, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return report, nil, err
	}
	jsonPath := filepath.Join(outDir, "review_report.json")
	mdPath := filepath.Join(outDir, "review_report.md")
	report.ReportJSONPath = jsonPath
	report.ReportMarkdownPath = mdPath

	jsonBody, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, nil, err
	}
	jsonBody = []byte(redactor.Redact(string(jsonBody)))
	if err := os.WriteFile(jsonPath, jsonBody, 0o644); err != nil {
		return report, nil, err
	}
	mdBody := []byte(redactor.Redact(RenderMarkdown(report)))
	if err := os.WriteFile(mdPath, mdBody, 0o644); err != nil {
		return report, nil, err
	}

	artifacts := []Artifact{}
	for _, path := range []string{jsonPath, mdPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			return report, nil, err
		}
		kind := strings.TrimPrefix(filepath.Ext(path), ".")
		artifacts = append(artifacts, Artifact{
			ID:        NewID("artifact"),
			TaskID:    report.Task.ID,
			Kind:      kind,
			Path:      path,
			Bytes:     int64(len(body)),
			SHA256:    SHA256Hex(string(body)),
			CreatedAt: time.Now().UTC(),
		})
	}
	return report, artifacts, nil
}

func RenderMarkdown(report ReviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", report.Task.ID)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Task.Status)
	fmt.Fprintf(&b, "- Runtime: `%s`\n", report.Task.Runtime)
	fmt.Fprintf(&b, "- Diff SHA256: `%s`\n", report.Input.DiffSHA256)
	fmt.Fprintf(&b, "- Files: %d, Go files: %d, added lines: %d, deleted lines: %d\n\n", report.Input.FileCount, report.Input.GoFileCount, report.Input.AddedLineCount, report.Input.DeletedLineCount)

	fmt.Fprintf(&b, "## Summary\n\n%s\n\n", report.FinalConclusion)
	fmt.Fprintf(&b, "## Severity Distribution\n\n")
	for _, sev := range []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo} {
		fmt.Fprintf(&b, "- %s: %d\n", sev, report.Monitoring.SeverityDistribution[sev])
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Findings\n\n")
	if len(report.Findings) == 0 {
		fmt.Fprintf(&b, "No high-confidence findings.\n\n")
	} else {
		writeFindingList(&b, report.Findings)
	}

	fmt.Fprintf(&b, "## Warnings\n\n")
	if len(report.Warnings) == 0 {
		fmt.Fprintf(&b, "No warning-only findings.\n\n")
	} else {
		writeFindingList(&b, report.Warnings)
	}

	fmt.Fprintf(&b, "## Needs Human Review\n\n")
	if len(report.NeedsHumanReview) == 0 {
		fmt.Fprintf(&b, "No low-confidence or ask items.\n\n")
	} else {
		writeFindingList(&b, report.NeedsHumanReview)
	}

	fmt.Fprintf(&b, "## Governance Decisions\n\n")
	if len(report.PermissionDecisions) == 0 {
		fmt.Fprintf(&b, "No tool permission decisions recorded.\n\n")
	} else {
		for _, d := range report.PermissionDecisions {
			fmt.Fprintf(&b, "- `%s` %v => **%s** (%s): %s\n", d.Tool, d.Command, d.Decision, d.Risk, d.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Sandbox Runs\n\n")
	if len(report.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "No sandbox commands were requested.\n\n")
	} else {
		for _, run := range report.SandboxRuns {
			fmt.Fprintf(&b, "- `%s` %v: %s exit=%d duration=%dms", run.Runtime, run.Command, run.Status, run.ExitCode, run.DurationMS)
			if run.ErrorType != "" {
				fmt.Fprintf(&b, " error=%s", run.ErrorType)
			}
			fmt.Fprintf(&b, "\n")
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Monitoring\n\n")
	fmt.Fprintf(&b, "- Total duration: %dms\n", report.Monitoring.TotalDurationMS)
	fmt.Fprintf(&b, "- Sandbox duration: %dms\n", report.Monitoring.SandboxDurationMS)
	fmt.Fprintf(&b, "- Tool calls: %d\n", report.Monitoring.ToolCallCount)
	fmt.Fprintf(&b, "- Permission intercepts: %d\n", report.Monitoring.PermissionIntercepts)
	fmt.Fprintf(&b, "- Findings: %d, warnings: %d, human review: %d\n", report.Monitoring.FindingCount, report.Monitoring.WarningCount, report.Monitoring.NeedsHumanReviewCount)
	if len(report.Monitoring.ExceptionDistribution) > 0 {
		fmt.Fprintf(&b, "- Exceptions: %s\n", stableMap(report.Monitoring.ExceptionDistribution))
	}
	return b.String()
}

func writeFindingList(b *strings.Builder, findings []Finding) {
	for _, f := range findings {
		fmt.Fprintf(b, "- **[%s] %s** `%s:%d` (%s, confidence %.2f, rule `%s`)\n", f.Severity, f.Title, f.File, f.Line, f.Category, f.Confidence, f.RuleID)
		fmt.Fprintf(b, "  Evidence: `%s`\n", f.Evidence)
		fmt.Fprintf(b, "  Recommendation: %s\n", f.Recommendation)
	}
	fmt.Fprintf(b, "\n")
}

func stableMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func FinalConclusion(findings, warnings, needsHuman []Finding, runs []SandboxRun, decisions []PermissionDecision) string {
	if len(findings) == 0 && len(warnings) == 0 && len(needsHuman) == 0 {
		return "Review completed with no deterministic rule findings. Sandbox and governance records were still persisted for auditability."
	}
	critical := 0
	high := 0
	for _, f := range findings {
		if f.Severity == SeverityCritical {
			critical++
		}
		if f.Severity == SeverityHigh {
			high++
		}
	}
	return fmt.Sprintf("Review completed with %d findings, %d warnings, and %d items requiring human review. Critical=%d, high=%d. Address high-confidence findings before merge and inspect governance/sandbox summaries for blocked checks.", len(findings), len(warnings), len(needsHuman), critical, high)
}
