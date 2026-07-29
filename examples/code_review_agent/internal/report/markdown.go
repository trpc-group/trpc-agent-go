//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"bytes"
	"fmt"
	"html"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func renderMarkdown(value review.Report) []byte {
	var output bytes.Buffer
	output.Grow(1024 + len(value.Findings)*512)
	fmt.Fprintf(&output, "# Code Review Report\n\n")
	fmt.Fprintf(&output, "- Task: `%s`\n", escapeInline(value.Task.ID))
	fmt.Fprintf(&output, "- Mode: `%s`\n", value.Task.Mode)
	fmt.Fprintf(&output, "- Findings: %d\n", value.Metrics.FindingTotal)
	fmt.Fprintf(&output, "- Warnings: %d\n", value.Metrics.WarningCount)
	fmt.Fprintf(&output, "- Needs human review: %d\n\n", value.Metrics.HumanReviewCount)
	fmt.Fprintf(&output, "## Conclusion\n\n%s\n\n", escapeParagraph(value.Conclusion))
	renderMonitoringSummary(&output, value)
	renderSandboxRuns(&output, value.SandboxRuns)
	renderGovernanceDecisions(&output, value.GovernanceDecisions)
	fmt.Fprintf(&output, "## Findings\n\n")
	if len(value.Findings) == 0 {
		fmt.Fprintf(&output, "No findings.\n")
		return output.Bytes()
	}
	fmt.Fprintf(&output, "| Severity | Layer | Location | Rule | Title |\n")
	fmt.Fprintf(&output, "| --- | --- | --- | --- | --- |\n")
	for _, finding := range value.Findings {
		location := fmt.Sprintf("%s:%d", finding.File, finding.Line)
		if finding.EndLine > finding.Line {
			location = fmt.Sprintf("%s:%d-%d", finding.File, finding.Line, finding.EndLine)
		}
		fmt.Fprintf(
			&output,
			"| %s | %s | `%s` | `%s` | %s |\n",
			escapeCell(string(finding.Severity)),
			escapeCell(string(finding.Layer)),
			escapeInline(location),
			escapeInline(finding.RuleID),
			escapeCell(finding.Title),
		)
	}
	for index, finding := range value.Findings {
		fmt.Fprintf(&output, "\n### %d. %s\n\n", index+1, escapeHeading(finding.Title))
		fmt.Fprintf(&output, "- Disposition: `%s`\n", finding.Disposition)
		fmt.Fprintf(&output, "- Confidence: `%s`\n", finding.Confidence)
		fmt.Fprintf(&output, "- Fingerprint: `%s`\n", finding.Fingerprint)
		fmt.Fprintf(&output, "- Evidence: %s\n", escapeParagraph(finding.Evidence))
		fmt.Fprintf(&output, "- Recommendation: %s\n", escapeParagraph(finding.Recommendation))
	}
	return output.Bytes()
}

func renderMonitoringSummary(output *bytes.Buffer, value review.Report) {
	fmt.Fprintf(output, "## Monitoring Summary\n\n")
	fmt.Fprintf(output, "- Total duration: `%s`\n", value.Metrics.TotalDuration)
	fmt.Fprintf(output, "- Sandbox duration: `%s`\n", value.Metrics.SandboxDuration)
	fmt.Fprintf(output, "- Tool invocations: %d\n", value.Metrics.ToolInvocations)
	fmt.Fprintf(output, "- Permission blocks: %d\n", value.Metrics.PermissionBlocks)
	fmt.Fprintf(output, "- Severity counts:")
	for _, severity := range []review.Severity{
		review.SeverityCritical, review.SeverityHigh, review.SeverityMedium,
		review.SeverityLow, review.SeverityInfo,
	} {
		fmt.Fprintf(output, " `%s=%d`", severity, value.Metrics.SeverityCounts[severity])
	}
	fmt.Fprintln(output)
	errorTypes := make([]string, 0, len(value.Metrics.ErrorTypeCounts))
	for errorType := range value.Metrics.ErrorTypeCounts {
		errorTypes = append(errorTypes, errorType)
	}
	sort.Strings(errorTypes)
	if len(errorTypes) == 0 {
		fmt.Fprintln(output, "- Error types: none")
	} else {
		fmt.Fprint(output, "- Error types:")
		for _, errorType := range errorTypes {
			fmt.Fprintf(output, " `%s=%d`", escapeInline(errorType),
				value.Metrics.ErrorTypeCounts[errorType])
		}
		fmt.Fprintln(output)
	}
	fmt.Fprintln(output)
}

func renderSandboxRuns(output *bytes.Buffer, runs []review.SandboxRun) {
	fmt.Fprintf(output, "## Sandbox Runs\n\n")
	if len(runs) == 0 {
		fmt.Fprintln(output, "No sandbox runs.")
		fmt.Fprintln(output)
		return
	}
	fmt.Fprintln(output, "| Command | Status | Duration | Exit | Truncated |")
	fmt.Fprintln(output, "| --- | --- | --- | --- | --- |")
	for _, run := range runs {
		exit := "n/a"
		if run.ExitCode != nil {
			exit = fmt.Sprintf("%d", *run.ExitCode)
		}
		fmt.Fprintf(output, "| %s | %s | `%s` | `%s` | %t |\n",
			escapeCell(run.Command), escapeCell(string(run.Status)), run.Duration,
			escapeInline(exit), run.Truncated)
	}
	fmt.Fprintln(output)
}

func renderGovernanceDecisions(output *bytes.Buffer, decisions []review.GovernanceDecision) {
	fmt.Fprintf(output, "## Governance Decisions\n\n")
	if len(decisions) == 0 {
		fmt.Fprintln(output, "No governance decisions.")
		fmt.Fprintln(output)
		return
	}
	fmt.Fprintln(output, "| Kind | Tool | Action | Rule | Reason |")
	fmt.Fprintln(output, "| --- | --- | --- | --- | --- |")
	for _, decision := range decisions {
		fmt.Fprintf(output, "| %s | %s | %s | %s | %s |\n",
			escapeCell(string(decision.Kind)), escapeCell(decision.Tool),
			escapeCell(string(decision.Action)), escapeCell(decision.Rule),
			escapeCell(decision.Reason))
	}
	fmt.Fprintln(output)
}

func escapeInline(value string) string {
	return escapeMarkdown(value)
}

func escapeCell(value string) string {
	value = escapeInline(value)
	value = strings.ReplaceAll(value, "|", "&#124;")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func escapeParagraph(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = escapeMarkdown(value)
	return strings.ReplaceAll(value, "\n", "<br>")
}

func escapeHeading(value string) string {
	return escapeParagraph(value)
}

var markdownEscaper = strings.NewReplacer(
	"\\", "&#92;",
	"`", "&#96;",
	"*", "&#42;",
	"_", "&#95;",
	"{", "&#123;",
	"}", "&#125;",
	"[", "&#91;",
	"]", "&#93;",
	"(", "&#40;",
	")", "&#41;",
	"#", "&#35;",
	"+", "&#43;",
	"-", "&#45;",
	".", "&#46;",
	"!", "&#33;",
	"|", "&#124;",
	">", "&#62;",
)

func escapeMarkdown(value string) string {
	return markdownEscaper.Replace(html.EscapeString(value))
}
