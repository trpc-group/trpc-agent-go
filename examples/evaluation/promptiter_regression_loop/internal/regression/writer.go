//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	jsonReportName             = "optimization_report.json"
	markdownReportName         = "optimization_report.md"
	fileMode                   = 0o644
	directoryMode              = 0o755
	minimumMarkdownFenceLength = 3
)

// WriteReports writes the required JSON and Markdown audit reports.
func WriteReports(outputDir string, report *Report) error {
	if strings.TrimSpace(outputDir) == "" {
		return errors.New("output directory is empty")
	}
	if report == nil {
		return errors.New("report is nil")
	}
	if err := os.MkdirAll(outputDir, directoryMode); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal JSON report: %w", err)
	}
	markdownData, err := renderMarkdown(report)
	if err != nil {
		return fmt.Errorf("render Markdown report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, jsonReportName), append(jsonData, '\n'), fileMode); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, markdownReportName), markdownData, fileMode); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func renderMarkdown(report *Report) ([]byte, error) {
	tmpl, err := template.New("optimization-report").Funcs(template.FuncMap{
		"codeBlock": markdownCodeBlock,
		"decision":  decisionLabel,
		"join":      func(values []string) string { return strings.Join(values, "; ") },
		"ms":        Milliseconds,
	}).Parse(markdownTemplate)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, report); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decisionLabel(decision GateDecision) string {
	if decision.Accepted {
		return "ACCEPT"
	}
	return "REJECT"
}

func markdownCodeBlock(value string) string {
	fenceLength := minimumMarkdownFenceLength
	currentRun := 0
	for _, character := range value {
		if character != '`' {
			currentRun = 0
			continue
		}
		currentRun++
		if currentRun >= fenceLength {
			fenceLength = currentRun + 1
		}
	}
	fence := strings.Repeat("`", fenceLength)
	separator := "\n"
	if strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\r") {
		separator = ""
	}
	return fence + "\n" + value + separator + fence
}

const markdownTemplate = `# Prompt Optimization Report

## Decision

- Selected candidate decision: {{decision .Decision}}
- Should write back accepted prompt: {{.ShouldWriteBack}}
- Decision reasons: {{join .Decision.Reasons}}
- Run status: {{.Run.Status}}
{{if .Run.Error}}- Run error: {{.Run.Error}}
{{end}}- Seed: {{.Run.Seed}}
- Mode: {{.Run.Mode}}
- Duration: {{ms .Run.Duration}} ms

## Baseline

- Train score: {{printf "%.4f" .BaselineTrain.OverallScore}}
- Validation score: {{printf "%.4f" .BaselineValidation.OverallScore}}
- Failed metrics: {{.BaselineAttribution.Summary.TotalFailures}}

## Optimization Rounds
{{range .Rounds}}
### Attempt {{.Attempt}}

- Candidate train score: {{printf "%.4f" .Train.OverallScore}}
- Validation score: {{printf "%.4f" .Validation.OverallScore}}
- Baseline delta: {{printf "%.4f" .Delta.ScoreDelta}}
- Regression gate: {{decision .RegressionGateDecision}}
- Reasons: {{join .RegressionGateDecision.Reasons}}
- Candidate prompt:

{{codeBlock .CandidatePrompt.Text}}
{{range .Delta.Cases}}  - {{.CaseID}}: {{.Kind}} ({{printf "%+.4f" .ScoreDelta}})
{{end}}
{{end}}
## Usage

- Monetary cost available: {{.Usage.MonetaryCostAvailable}}
- Monetary cost: {{printf "%.4f" .Usage.MonetaryCost}}
- Prompt tokens: {{.Usage.PromptTokens}}
- Completion tokens: {{.Usage.CompletionTokens}}
- Total tokens: {{.Usage.TotalTokens}}
- Model calls: {{.Usage.ModelCalls}}
- Tool calls: {{.Usage.ToolCalls}}
- Aggregate evaluation latency: {{ms .Usage.Duration}} ms
`
