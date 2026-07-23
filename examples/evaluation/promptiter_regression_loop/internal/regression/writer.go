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
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

const (
	jsonReportName             = "optimization_report.json"
	markdownReportName         = "optimization_report.md"
	fileMode                   = 0o644
	directoryMode              = 0o755
	minimumMarkdownFenceLength = 3
)

type reportArtifact struct {
	label      string
	name       string
	data       []byte
	tempPath   string
	backupPath string
	published  bool
}

type reportFileOps struct {
	lstat  func(string) (os.FileInfo, error)
	rename func(string, string) error
	remove func(string) error
}

var osReportFileOps = reportFileOps{
	lstat:  os.Lstat,
	rename: os.Rename,
	remove: os.Remove,
}

var reportWriteMu sync.Mutex

// WriteReports writes the required JSON and Markdown audit reports.
// Writes are serialized within this process. The two fixed report paths cannot
// provide atomic visibility across process crashes or independent processes.
func WriteReports(outputDir string, report *Report) error {
	reportWriteMu.Lock()
	defer reportWriteMu.Unlock()
	return writeReports(outputDir, report, osReportFileOps)
}

func writeReports(outputDir string, report *Report, fileOps reportFileOps) (resultErr error) {
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

	artifacts := []reportArtifact{
		{label: "Markdown", name: markdownReportName, data: markdownData},
		{label: "JSON", name: jsonReportName, data: append(jsonData, '\n')},
	}
	defer func() {
		resultErr = errors.Join(resultErr, cleanupStagedReports(artifacts, fileOps))
	}()
	for i := range artifacts {
		tempPath, err := stageReport(outputDir, artifacts[i])
		if err != nil {
			return err
		}
		artifacts[i].tempPath = tempPath
	}
	if err := backupReports(outputDir, artifacts, fileOps); err != nil {
		return errors.Join(err, rollbackReportPublish(outputDir, artifacts, fileOps))
	}
	if err := publishReports(outputDir, artifacts, fileOps); err != nil {
		return errors.Join(err, rollbackReportPublish(outputDir, artifacts, fileOps))
	}

	// Both report files are now committed. Cleanup failures must not roll back a
	// complete pair because an old generation may already be partly removed.
	if err := cleanupReportBackups(artifacts, fileOps); err != nil {
		return fmt.Errorf("reports committed but backup cleanup failed: %w", err)
	}
	return nil
}

func stageReport(outputDir string, artifact reportArtifact) (string, error) {
	file, err := os.CreateTemp(outputDir, "."+artifact.name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create staged %s report: %w", artifact.label, err)
	}
	tempPath := file.Name()
	cleanupWithError := func() error {
		var cleanupErrors []error
		if err := file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("close failed staged %s report: %w", artifact.label, err))
		}
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove failed staged %s report: %w", artifact.label, err))
		}
		return errors.Join(cleanupErrors...)
	}
	if err := file.Chmod(fileMode); err != nil {
		return "", errors.Join(
			fmt.Errorf("set staged %s report permissions: %w", artifact.label, err),
			cleanupWithError(),
		)
	}
	if _, err := io.Copy(file, bytes.NewReader(artifact.data)); err != nil {
		return "", errors.Join(
			fmt.Errorf("write staged %s report: %w", artifact.label, err),
			cleanupWithError(),
		)
	}
	if err := file.Sync(); err != nil {
		return "", errors.Join(
			fmt.Errorf("sync staged %s report: %w", artifact.label, err),
			cleanupWithError(),
		)
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(tempPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return "", errors.Join(
			fmt.Errorf("close staged %s report: %w", artifact.label, err),
			removeErr,
		)
	}
	return tempPath, nil
}

func backupReports(outputDir string, artifacts []reportArtifact, fileOps reportFileOps) error {
	for i := range artifacts {
		targetPath := filepath.Join(outputDir, artifacts[i].name)
		info, err := fileOps.lstat(targetPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing %s report: %w", artifacts[i].label, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("existing %s report is not a regular file", artifacts[i].label)
		}
		backupPath, err := unusedBackupPath(outputDir, artifacts[i].name)
		if err != nil {
			return fmt.Errorf("reserve %s report backup path: %w", artifacts[i].label, err)
		}
		if err := fileOps.rename(targetPath, backupPath); err != nil {
			return fmt.Errorf("backup existing %s report: %w", artifacts[i].label, err)
		}
		artifacts[i].backupPath = backupPath
	}
	return nil
}

func unusedBackupPath(outputDir, reportName string) (string, error) {
	file, err := os.CreateTemp(outputDir, "."+reportName+".backup-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		removeErr := os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return "", errors.Join(
			fmt.Errorf("close backup placeholder: %w", err),
			removeErr,
		)
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func publishReports(outputDir string, artifacts []reportArtifact, fileOps reportFileOps) error {
	for i := range artifacts {
		targetPath := filepath.Join(outputDir, artifacts[i].name)
		if err := fileOps.rename(artifacts[i].tempPath, targetPath); err != nil {
			return fmt.Errorf("publish %s report: %w", artifacts[i].label, err)
		}
		artifacts[i].tempPath = ""
		artifacts[i].published = true
	}
	return nil
}

func rollbackReportPublish(outputDir string, artifacts []reportArtifact, fileOps reportFileOps) error {
	var rollbackErrors []error
	for i := len(artifacts) - 1; i >= 0; i-- {
		if !artifacts[i].published {
			continue
		}
		targetPath := filepath.Join(outputDir, artifacts[i].name)
		if err := fileOps.remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors,
				fmt.Errorf("remove published %s report during rollback: %w", artifacts[i].label, err))
			continue
		}
		artifacts[i].published = false
	}
	restoreJSON := true
	for i := range artifacts {
		if artifacts[i].backupPath == "" {
			continue
		}
		if artifacts[i].name == jsonReportName && !restoreJSON {
			continue
		}
		targetPath := filepath.Join(outputDir, artifacts[i].name)
		if err := fileOps.rename(artifacts[i].backupPath, targetPath); err != nil {
			rollbackErrors = append(rollbackErrors,
				fmt.Errorf("restore %s report backup: %w", artifacts[i].label, err))
			if artifacts[i].name == markdownReportName {
				restoreJSON = false
			}
			continue
		}
		artifacts[i].backupPath = ""
	}
	return errors.Join(rollbackErrors...)
}

func cleanupReportBackups(artifacts []reportArtifact, fileOps reportFileOps) error {
	var cleanupErrors []error
	for i := range artifacts {
		if artifacts[i].backupPath == "" {
			continue
		}
		if err := fileOps.remove(artifacts[i].backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove %s report backup: %w", artifacts[i].label, err))
			continue
		}
		artifacts[i].backupPath = ""
	}
	return errors.Join(cleanupErrors...)
}

func cleanupStagedReports(artifacts []reportArtifact, fileOps reportFileOps) error {
	var cleanupErrors []error
	for i := range artifacts {
		if artifacts[i].tempPath != "" {
			if err := fileOps.remove(artifacts[i].tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors,
					fmt.Errorf("remove staged %s report: %w", artifacts[i].label, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func renderMarkdown(report *Report) ([]byte, error) {
	tmpl, err := template.New("optimization-report").Funcs(template.FuncMap{
		"codeBlock": markdownCodeBlock,
		"decision":  decisionLabel,
		"join":      joinMarkdownLines,
		"line":      markdownLine,
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

func joinMarkdownLines(values []string) string {
	return markdownLine(strings.Join(values, "; "))
}

func markdownLine(value string) string {
	replacer := strings.NewReplacer(
		"\r\n", " ", "\r", " ", "\n", " ",
		"\\", "\\\\", "`", "\\`", "<", "\\<", ">", "\\>", "[", "\\[", "]", "\\]",
	)
	return replacer.Replace(value)
}

const markdownTemplate = `# Prompt Optimization Report

## Decision

- Selected candidate decision: {{decision .Decision}}
- Should write back accepted prompt: {{.ShouldWriteBack}}
- Decision reasons: {{join .Decision.Reasons}}
- Run status: {{.Run.Status}}
{{if .Run.Error}}- Run error: {{line .Run.Error}}
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
- Original baseline delta: {{printf "%.4f" .Delta.ScoreDelta}}
- Gate delta vs accepted baseline: {{printf "%.4f" .RegressionGateDecision.ScoreDelta}}
- Regression gate: {{decision .RegressionGateDecision}}
- Reasons: {{join .RegressionGateDecision.Reasons}}
- Candidate prompt:

{{codeBlock .CandidatePrompt.Text}}
{{range .Delta.Cases}}  - {{line .CaseID}}: {{.Kind}} ({{printf "%+.4f" .ScoreDelta}})
{{end}}
{{end}}
## Usage

- Monetary cost available: {{.Usage.MonetaryCostAvailable}}
- Monetary cost: {{printf "%.4f" .Usage.MonetaryCost}}
- Token usage available: {{.Usage.TokenUsageAvailable}}
- Prompt tokens: {{.Usage.PromptTokens}}
- Completion tokens: {{.Usage.CompletionTokens}}
- Total tokens: {{.Usage.TotalTokens}}
- Model calls: {{.Usage.ModelCalls}}
- Tool calls: {{.Usage.ToolCalls}}
- Aggregate evaluation latency: {{ms .Usage.Duration}} ms
`
