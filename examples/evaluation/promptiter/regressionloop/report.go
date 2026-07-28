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
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	reportSchemaVersion    = 1
	reportStatusSucceeded  = "succeeded"
	reportStatusFailed     = "failed"
	maxReportTextBytes     = 4096
	reportTruncationMarker = "… [truncated]"
)

var (
	sensitiveAssignmentPattern = regexp.MustCompile(
		`(?i)(["']?(?:api[_ -]?key|authorization|token|secret|password|credential)["']?\s*[:=]\s*)(?:(?:bearer\s+)?(?:"[^"]*"|'[^']*'|[^\s,;}\]]+))`,
	)
	bearerCredentialPattern = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`)
	providerKeyPattern      = regexp.MustCompile(`(?i)\b(?:sk|rk)-[a-z0-9_-]{8,}\b`)
)

type effectiveRole struct {
	Model      string   `json:"model"`
	BaseURL    string   `json:"baseURL,omitempty"`
	APIKeyEnv  string   `json:"apiKeyEnv,omitempty"`
	InputPerM  *float64 `json:"inputPerMillion,omitempty"`
	OutputPerM *float64 `json:"outputPerMillion,omitempty"`
	MaxRetries int      `json:"maxRetries"`
}

type roundReport struct {
	Number          int               `json:"number"`
	CandidatePrompt string            `json:"candidatePrompt,omitempty"`
	CandidateSource string            `json:"candidateSource,omitempty"`
	Delta           snapshotDelta     `json:"delta"`
	Gate            gateDecision      `json:"gate"`
	Attributions    []caseAttribution `json:"attributions,omitempty"`
	Usage           usageSummary      `json:"usage"`
	Error           string            `json:"error,omitempty"`
}

type regressionReport struct {
	SchemaVersion     int                         `json:"schemaVersion"`
	RunID             string                      `json:"runId"`
	Status            string                      `json:"status"`
	Accepted          bool                        `json:"accepted"`
	Mode              runMode                     `json:"mode"`
	StructureID       string                      `json:"structureId"`
	Fingerprints      map[string]string           `json:"fingerprints"`
	Roles             map[string]effectiveRole    `json:"roles"`
	Baseline          evaluationSnapshot          `json:"baseline"`
	Rounds            []roundReport               `json:"rounds"`
	Usage             usageSummary                `json:"usage"`
	AttributionCounts map[attributionCategory]int `json:"attributionCounts,omitempty"`
	TerminalError     string                      `json:"terminalError,omitempty"`
}

func renderJSON(report regressionReport) ([]byte, error) {
	stable := prepareReport(report)
	contents, err := json.MarshalIndent(stable, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	return append(contents, '\n'), nil
}

func renderMarkdown(report regressionReport) ([]byte, error) {
	report = prepareReport(report)
	var output bytes.Buffer
	fmt.Fprintf(&output, "# PromptIter Regression Report\n\n")
	fmt.Fprintf(&output, "- Run: `%s`\n", markdownCell(report.RunID))
	fmt.Fprintf(&output, "- Status: `%s`\n", markdownCell(report.Status))
	fmt.Fprintf(&output, "- Decision: **%s**\n\n", map[bool]string{true: "ACCEPT", false: "REJECT"}[report.Accepted])
	for _, round := range report.Rounds {
		fmt.Fprintf(&output, "## Round %d\n\n", round.Number)
		output.WriteString("| Case | Metric | Change | Score delta |\n")
		output.WriteString("|---|---|---|---:|\n")
		for _, delta := range round.Delta.Metrics {
			fmt.Fprintf(&output, "| %s | %s | %s | %.6f |\n",
				markdownCell(delta.Key.EvalCaseID), markdownCell(delta.Key.MetricName),
				markdownCell(string(delta.Kind)), delta.ScoreDelta)
		}
		output.WriteString("\n### Candidate prompt\n\n")
		fence := codeFence(round.CandidatePrompt)
		fmt.Fprintf(&output, "%stext\n%s\n%s\n\n", fence, round.CandidatePrompt, fence)
		output.WriteString("### Gate checks\n\n| Check | Result | Reason |\n|---|---|---|\n")
		for _, check := range round.Gate.Checks {
			result := "pass"
			if !check.Passed {
				result = "fail"
			}
			fmt.Fprintf(&output, "| %s | %s | %s |\n", markdownCell(check.ID), result, markdownCell(check.Reason))
		}
		output.WriteByte('\n')
	}
	return append(bytes.TrimRight(output.Bytes(), "\n"), '\n'), nil
}

func stableReport(report regressionReport) regressionReport {
	report.Rounds = append([]roundReport(nil), report.Rounds...)
	sort.SliceStable(report.Rounds, func(i, j int) bool { return report.Rounds[i].Number < report.Rounds[j].Number })
	for i := range report.Rounds {
		round := &report.Rounds[i]
		round.Delta.Metrics = append([]metricDelta(nil), round.Delta.Metrics...)
		sort.SliceStable(round.Delta.Metrics, func(i, j int) bool {
			left, right := round.Delta.Metrics[i].Key, round.Delta.Metrics[j].Key
			if left.EvalSetID != right.EvalSetID {
				return left.EvalSetID < right.EvalSetID
			}
			if left.EvalCaseID != right.EvalCaseID {
				return left.EvalCaseID < right.EvalCaseID
			}
			return left.MetricName < right.MetricName
		})
		round.Gate.Checks = append([]gateCheck(nil), round.Gate.Checks...)
		sort.SliceStable(round.Gate.Checks, func(i, j int) bool { return round.Gate.Checks[i].ID < round.Gate.Checks[j].ID })
		round.Attributions = append([]caseAttribution(nil), round.Attributions...)
		for j := range round.Attributions {
			round.Attributions[j].Secondary = append(
				[]attribution(nil), round.Attributions[j].Secondary...,
			)
		}
		sort.SliceStable(round.Attributions, func(i, j int) bool {
			return round.Attributions[i].EvalCaseID < round.Attributions[j].EvalCaseID
		})
	}
	report.Baseline.Cases = append([]caseResult(nil), report.Baseline.Cases...)
	sort.SliceStable(report.Baseline.Cases, func(i, j int) bool {
		if report.Baseline.Cases[i].EvalSetID != report.Baseline.Cases[j].EvalSetID {
			return report.Baseline.Cases[i].EvalSetID < report.Baseline.Cases[j].EvalSetID
		}
		return report.Baseline.Cases[i].EvalCaseID < report.Baseline.Cases[j].EvalCaseID
	})
	for i := range report.Baseline.Cases {
		evalCase := &report.Baseline.Cases[i]
		evalCase.Metrics = append([]metricResult(nil), evalCase.Metrics...)
		sort.SliceStable(evalCase.Metrics, func(i, j int) bool {
			return evalCase.Metrics[i].Name < evalCase.Metrics[j].Name
		})
	}
	return report
}

func prepareReport(report regressionReport) regressionReport {
	report = stableReport(report)
	report.TerminalError = sanitizeReportText(report.TerminalError)
	for i := range report.Rounds {
		round := &report.Rounds[i]
		round.CandidatePrompt = sanitizeReportText(round.CandidatePrompt)
		round.Error = sanitizeReportText(round.Error)
		for j := range round.Gate.Checks {
			check := &round.Gate.Checks[j]
			check.Reason = sanitizeReportText(check.Reason)
			if observed, ok := check.Observed.(string); ok {
				check.Observed = sanitizeReportText(observed)
			}
		}
		for j := range round.Attributions {
			item := &round.Attributions[j]
			item.Primary.Evidence = sanitizeReportText(item.Primary.Evidence)
			for k := range item.Secondary {
				item.Secondary[k].Evidence = sanitizeReportText(item.Secondary[k].Evidence)
			}
		}
	}
	for i := range report.Baseline.Cases {
		evalCase := &report.Baseline.Cases[i]
		evalCase.ExecutionError = sanitizeReportText(evalCase.ExecutionError)
		for j := range evalCase.Metrics {
			evalCase.Metrics[j].Reason = sanitizeReportText(evalCase.Metrics[j].Reason)
		}
	}
	return report
}

func sanitizeReportText(value string) string {
	value = sensitiveAssignmentPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = bearerCredentialPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = providerKeyPattern.ReplaceAllString(value, "[REDACTED]")
	if len(value) <= maxReportTextBytes {
		return value
	}
	limit := maxReportTextBytes - len(reportTruncationMarker)
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit] + reportTruncationMarker
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func codeFence(value string) string {
	longest, current := 0, 0
	for _, char := range value {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	if longest < 3 {
		longest = 2
	}
	return strings.Repeat("`", longest+1)
}
