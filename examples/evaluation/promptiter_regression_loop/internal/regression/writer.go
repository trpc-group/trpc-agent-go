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
	description  string
	targetPath   string
	data         []byte
	mode         os.FileMode
	mustExist    bool
	preserveMode bool
	tempPath     string
	backupPath   string
	published    bool
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

// WritePromptAndReports writes an accepted prompt together with the required
// audit reports. Publication is serialized within this process and rolled back
// on ordinary I/O failures, but is not atomic across crashes or processes.
func WritePromptAndReports(
	promptPath string,
	outputDir string,
	targetSurfaceID string,
	report *Report,
) error {
	reportWriteMu.Lock()
	defer reportWriteMu.Unlock()
	return writePromptAndReports(
		promptPath, outputDir, targetSurfaceID, report, osReportFileOps,
	)
}

func writeReports(
	outputDir string,
	report *Report,
	fileOps reportFileOps,
) error {
	artifacts, err := reportArtifacts(outputDir, report)
	if err != nil {
		return err
	}
	return publishArtifactSet(artifacts, fileOps)
}

func writePromptAndReports(
	promptPath string,
	outputDir string,
	targetSurfaceID string,
	report *Report,
	fileOps reportFileOps,
) error {
	prompt, err := acceptedPromptArtifact(promptPath, targetSurfaceID, report)
	if err != nil {
		return err
	}
	artifacts, err := reportArtifacts(outputDir, report)
	if err != nil {
		return err
	}
	artifacts = append([]reportArtifact{prompt}, artifacts...)
	if err := validateDistinctArtifactTargets(artifacts); err != nil {
		return err
	}
	return publishArtifactSet(artifacts, fileOps)
}

func acceptedPromptArtifact(
	promptPath string,
	targetSurfaceID string,
	report *Report,
) (reportArtifact, error) {
	if strings.TrimSpace(promptPath) == "" {
		return reportArtifact{}, errors.New("prompt path is empty")
	}
	if strings.TrimSpace(targetSurfaceID) == "" {
		return reportArtifact{}, errors.New("target surface id is empty")
	}
	if report == nil {
		return reportArtifact{}, errors.New("report is nil")
	}
	if report.Run.Status != RunStatusCompleted {
		return reportArtifact{}, errors.New("accepted prompt requires a completed run")
	}
	if !report.ShouldWriteBack {
		return reportArtifact{}, errors.New("report does not authorize prompt writeback")
	}
	if report.WritebackProfile == nil {
		return reportArtifact{}, errors.New("writeback profile is nil")
	}
	if report.WritebackProfile.SurfaceID != targetSurfaceID {
		return reportArtifact{}, errors.New("writeback profile surface id does not match target")
	}
	if strings.TrimSpace(report.WritebackProfile.Text) == "" {
		return reportArtifact{}, errors.New("writeback profile text is empty")
	}
	text := strings.TrimRight(report.WritebackProfile.Text, "\r\n") + "\n"
	return reportArtifact{
		description:  "accepted prompt",
		targetPath:   filepath.Clean(promptPath),
		data:         []byte(text),
		mustExist:    true,
		preserveMode: true,
	}, nil
}

func reportArtifacts(outputDir string, report *Report) ([]reportArtifact, error) {
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("output directory is empty")
	}
	if report == nil {
		return nil, errors.New("report is nil")
	}
	if err := os.MkdirAll(outputDir, directoryMode); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	jsonData, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON report: %w", err)
	}
	markdownData, err := renderMarkdown(report)
	if err != nil {
		return nil, fmt.Errorf("render Markdown report: %w", err)
	}
	return []reportArtifact{
		{
			description: "Markdown report",
			targetPath:  filepath.Join(outputDir, markdownReportName),
			data:        markdownData,
			mode:        fileMode,
		},
		{
			description: "JSON report",
			targetPath:  filepath.Join(outputDir, jsonReportName),
			data:        append(jsonData, '\n'),
			mode:        fileMode,
		},
	}, nil
}

func validateDistinctArtifactTargets(artifacts []reportArtifact) error {
	seen := make(map[string]string, len(artifacts))
	type existingTarget struct {
		description string
		info        os.FileInfo
	}
	existing := make([]existingTarget, 0, len(artifacts))
	for i := range artifacts {
		targetPath, err := filepath.Abs(filepath.Clean(artifacts[i].targetPath))
		if err != nil {
			return fmt.Errorf("resolve %s target: %w", artifacts[i].description, err)
		}
		key := targetPath
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		if existing, ok := seen[key]; ok {
			return fmt.Errorf(
				"%s target conflicts with %s target",
				artifacts[i].description,
				existing,
			)
		}
		seen[key] = artifacts[i].description

		info, err := os.Stat(targetPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf(
				"inspect %s target identity: %w",
				artifacts[i].description,
				err,
			)
		}
		for _, previous := range existing {
			if os.SameFile(previous.info, info) {
				return fmt.Errorf(
					"%s target conflicts with %s target",
					artifacts[i].description,
					previous.description,
				)
			}
		}
		existing = append(existing, existingTarget{
			description: artifacts[i].description,
			info:        info,
		})
	}
	return nil
}

func publishArtifactSet(
	artifacts []reportArtifact,
	fileOps reportFileOps,
) (resultErr error) {
	if len(artifacts) == 0 {
		return errors.New("artifact set is empty")
	}
	if err := prepareArtifacts(artifacts, fileOps); err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, cleanupStagedArtifacts(artifacts, fileOps))
	}()
	for i := range artifacts {
		tempPath, err := stageArtifact(artifacts[i])
		if err != nil {
			return err
		}
		artifacts[i].tempPath = tempPath
	}
	if err := backupArtifacts(artifacts, fileOps); err != nil {
		return errors.Join(err, rollbackArtifactPublish(artifacts, fileOps))
	}
	if err := publishArtifacts(artifacts, fileOps); err != nil {
		return errors.Join(err, rollbackArtifactPublish(artifacts, fileOps))
	}

	// All artifacts are now committed. Cleanup failures must not roll back a
	// complete set because an old generation may already be partly removed.
	if err := cleanupArtifactBackups(artifacts, fileOps); err != nil {
		return fmt.Errorf("artifacts committed but backup cleanup failed: %w", err)
	}
	return nil
}

func prepareArtifacts(artifacts []reportArtifact, fileOps reportFileOps) error {
	for i := range artifacts {
		info, err := fileOps.lstat(artifacts[i].targetPath)
		if errors.Is(err, os.ErrNotExist) {
			if artifacts[i].mustExist {
				return fmt.Errorf(
					"existing %s does not exist", artifacts[i].description,
				)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing %s: %w", artifacts[i].description, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf(
				"existing %s is not a regular file", artifacts[i].description,
			)
		}
		if artifacts[i].preserveMode {
			artifacts[i].mode = info.Mode().Perm()
		}
	}
	return nil
}

func stageArtifact(artifact reportArtifact) (string, error) {
	targetDir := filepath.Dir(artifact.targetPath)
	name := filepath.Base(artifact.targetPath)
	file, err := os.CreateTemp(targetDir, "."+name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create staged %s: %w", artifact.description, err)
	}
	tempPath := file.Name()
	cleanupWithError := func() error {
		var cleanupErrors []error
		if err := file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("close failed staged %s: %w", artifact.description, err))
		}
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove failed staged %s: %w", artifact.description, err))
		}
		return errors.Join(cleanupErrors...)
	}
	if err := file.Chmod(artifact.mode); err != nil {
		return "", errors.Join(
			fmt.Errorf("set staged %s permissions: %w", artifact.description, err),
			cleanupWithError(),
		)
	}
	if _, err := io.Copy(file, bytes.NewReader(artifact.data)); err != nil {
		return "", errors.Join(
			fmt.Errorf("write staged %s: %w", artifact.description, err),
			cleanupWithError(),
		)
	}
	if err := file.Sync(); err != nil {
		return "", errors.Join(
			fmt.Errorf("sync staged %s: %w", artifact.description, err),
			cleanupWithError(),
		)
	}
	if err := file.Close(); err != nil {
		removeErr := os.Remove(tempPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		return "", errors.Join(
			fmt.Errorf("close staged %s: %w", artifact.description, err),
			removeErr,
		)
	}
	return tempPath, nil
}

func backupArtifacts(artifacts []reportArtifact, fileOps reportFileOps) error {
	for i := range artifacts {
		info, err := fileOps.lstat(artifacts[i].targetPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing %s: %w", artifacts[i].description, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf(
				"existing %s is not a regular file", artifacts[i].description,
			)
		}
		backupPath, err := unusedBackupPath(artifacts[i].targetPath)
		if err != nil {
			return fmt.Errorf(
				"reserve %s backup path: %w", artifacts[i].description, err,
			)
		}
		if err := fileOps.rename(artifacts[i].targetPath, backupPath); err != nil {
			return fmt.Errorf("backup existing %s: %w", artifacts[i].description, err)
		}
		artifacts[i].backupPath = backupPath
	}
	return nil
}

func unusedBackupPath(targetPath string) (string, error) {
	targetDir := filepath.Dir(targetPath)
	name := filepath.Base(targetPath)
	file, err := os.CreateTemp(targetDir, "."+name+".backup-*")
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

func publishArtifacts(artifacts []reportArtifact, fileOps reportFileOps) error {
	for i := range artifacts {
		if err := fileOps.rename(artifacts[i].tempPath, artifacts[i].targetPath); err != nil {
			return fmt.Errorf("publish %s: %w", artifacts[i].description, err)
		}
		artifacts[i].tempPath = ""
		artifacts[i].published = true
	}
	return nil
}

func rollbackArtifactPublish(artifacts []reportArtifact, fileOps reportFileOps) error {
	var rollbackErrors []error
	for i := len(artifacts) - 1; i >= 0; i-- {
		if !artifacts[i].published {
			continue
		}
		if err := fileOps.remove(artifacts[i].targetPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors,
				fmt.Errorf(
					"remove published %s during rollback: %w",
					artifacts[i].description,
					err,
				))
			continue
		}
		artifacts[i].published = false
	}
	for i := len(artifacts) - 1; i >= 0; i-- {
		if artifacts[i].backupPath == "" {
			continue
		}
		if artifacts[i].published {
			continue
		}
		if err := fileOps.rename(
			artifacts[i].backupPath,
			artifacts[i].targetPath,
		); err != nil {
			rollbackErrors = append(rollbackErrors,
				fmt.Errorf(
					"restore %s backup: %w", artifacts[i].description, err,
				))
			continue
		}
		artifacts[i].backupPath = ""
	}
	return errors.Join(rollbackErrors...)
}

func cleanupArtifactBackups(artifacts []reportArtifact, fileOps reportFileOps) error {
	var cleanupErrors []error
	for i := range artifacts {
		if artifacts[i].backupPath == "" {
			continue
		}
		if err := fileOps.remove(artifacts[i].backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove %s backup: %w", artifacts[i].description, err))
			continue
		}
		artifacts[i].backupPath = ""
	}
	return errors.Join(cleanupErrors...)
}

func cleanupStagedArtifacts(artifacts []reportArtifact, fileOps reportFileOps) error {
	var cleanupErrors []error
	for i := range artifacts {
		if artifacts[i].tempPath != "" {
			if err := fileOps.remove(artifacts[i].tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors,
					fmt.Errorf("remove staged %s: %w", artifacts[i].description, err))
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
