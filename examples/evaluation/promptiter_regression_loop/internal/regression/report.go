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
	"sort"
	"strings"
	"unicode/utf8"
)

const reportSchemaVersion = "promptiter-regression/v1"

// ReportPaths identifies one atomically published report generation.
type ReportPaths struct {
	Directory    string
	JSONPath     string
	MarkdownPath string
}

// NewReport creates the initial audit artifact before optimization rounds run.
func NewReport(
	metadata RunMetadata,
	baselineTrain *EvaluationResult,
	baselineValidation *EvaluationResult,
	attribution AttributionResult,
) (*Report, error) {
	report := &Report{
		SchemaVersion:       reportSchemaVersion,
		Run:                 metadata,
		BaselineTrain:       baselineTrain,
		BaselineValidation:  baselineValidation,
		BaselineAttribution: attribution,
		Rounds:              []RoundReport{},
		Decision:            GateDecision{Accepted: false, Reasons: []string{"no candidate has passed the release gate"}},
		Usage:               Usage{Measured: true},
	}
	if err := validateReportBase(report); err != nil {
		return nil, err
	}
	report.Usage = AddUsage(baselineTrain.Usage, baselineValidation.Usage)
	return report, nil
}

// AppendRound adds one sequential optimization attempt to the audit.
func AppendRound(report *Report, round RoundReport) error {
	if report == nil {
		return errors.New("report is nil")
	}
	wantAttempt := len(report.Rounds) + 1
	if round.Attempt != wantAttempt {
		return fmt.Errorf("round attempt is %d, want %d", round.Attempt, wantAttempt)
	}
	if err := validateRound(round); err != nil {
		return err
	}
	report.Rounds = append(report.Rounds, round)
	report.Usage = AddUsage(report.Usage, round.Usage)
	if round.Gate.Accepted {
		candidate := round.CandidatePrompt
		report.SelectedAttempt = round.Attempt
		report.SelectedCandidate = &candidate
		report.ShouldWriteBack = true
		report.Decision = round.Gate
	}
	return nil
}

// FinalizeReport records terminal lifecycle state without changing a previously
// selected candidate after a later rejected attempt.
func FinalizeReport(report *Report, runErr error) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if runErr != nil {
		report.Run.Status = "failed"
		report.Run.Error = runErr.Error()
		report.ShouldWriteBack = false
		report.SelectedAttempt = 0
		report.SelectedCandidate = nil
		report.Decision = GateDecision{
			Accepted: false,
			Reasons:  []string{"pipeline failed; prompt writeback is disabled"},
		}
		return nil
	}
	report.Run.Status = "succeeded"
	report.Run.Error = ""
	if report.SelectedCandidate == nil {
		report.ShouldWriteBack = false
		if len(report.Rounds) > 0 {
			report.Decision = report.Rounds[len(report.Rounds)-1].Gate
		}
	}
	return validateReport(report)
}

// WriteJSON writes an indented machine-readable audit report.
func WriteJSON(writer io.Writer, report *Report) error {
	if writer == nil {
		return errors.New("JSON report writer is nil")
	}
	if err := validateReport(report); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode JSON report: %w", err)
	}
	return nil
}

// WriteMarkdown writes a human-readable audit report with dynamic code fences
// so optimizer-generated prompts cannot escape their code block.
func WriteMarkdown(writer io.Writer, report *Report) error {
	if writer == nil {
		return errors.New("Markdown report writer is nil")
	}
	if err := validateReport(report); err != nil {
		return err
	}
	var buffer bytes.Buffer
	fmt.Fprintln(&buffer, "# PromptIter Regression Report")
	fmt.Fprintln(&buffer)
	fmt.Fprintf(&buffer, "- Run: `%s`\n", markdownInline(report.Run.ID))
	fmt.Fprintf(&buffer, "- Status: `%s`\n", markdownInline(report.Run.Status))
	fmt.Fprintf(&buffer, "- Mode: `%s`\n", markdownInline(report.Run.Mode))
	fmt.Fprintf(&buffer, "- Baseline train score: `%.4f`\n", report.BaselineTrain.OverallScore)
	fmt.Fprintf(&buffer, "- Baseline validation score: `%.4f`\n", report.BaselineValidation.OverallScore)
	fmt.Fprintf(&buffer, "- Write back: `%t`\n", report.ShouldWriteBack)
	fmt.Fprintf(&buffer, "- Selected attempt: `%d`\n", report.SelectedAttempt)
	fmt.Fprintln(&buffer)
	fmt.Fprintln(&buffer, "## Final Decision")
	fmt.Fprintln(&buffer)
	fmt.Fprintf(&buffer, "**Accepted: %t**\n\n", report.Decision.Accepted)
	for _, reason := range report.Decision.Reasons {
		fmt.Fprintf(&buffer, "- %s\n", markdownText(reason))
	}
	if report.SelectedCandidate != nil {
		fmt.Fprintln(&buffer)
		fmt.Fprintf(&buffer, "Selected surface: `%s`\n\n", markdownInline(report.SelectedCandidate.SurfaceID))
		writeFenced(&buffer, report.SelectedCandidate.Text)
	}
	fmt.Fprintln(&buffer)
	fmt.Fprintln(&buffer, "## Attempts")
	fmt.Fprintln(&buffer)
	fmt.Fprintln(&buffer, "| Attempt | Train | Validation | Accepted delta | Baseline delta | PromptIter advanced | Release gate |")
	fmt.Fprintln(&buffer, "| ---: | ---: | ---: | ---: | ---: | :---: | :---: |")
	for _, round := range report.Rounds {
		fmt.Fprintf(&buffer, "| %d | %.4f | %.4f | %+.4f | %+.4f | %t | %t |\n",
			round.Attempt, round.Train.OverallScore, round.Validation.OverallScore,
			round.Delta.ScoreDelta, round.BaselineDelta.ScoreDelta,
			round.PromptIterAccepted, round.Gate.Accepted)
	}
	for _, round := range report.Rounds {
		fmt.Fprintln(&buffer)
		fmt.Fprintf(&buffer, "### Attempt %d\n\n", round.Attempt)
		fmt.Fprintln(&buffer, "Candidate prompt:")
		fmt.Fprintln(&buffer)
		writeFenced(&buffer, round.CandidatePrompt.Text)
		fmt.Fprintln(&buffer)
		fmt.Fprintln(&buffer, "Gate reasons:")
		fmt.Fprintln(&buffer)
		for _, reason := range round.Gate.Reasons {
			fmt.Fprintf(&buffer, "- %s\n", markdownText(reason))
		}
		fmt.Fprintln(&buffer)
		acceptedCases := make(map[string]CaseDelta, len(round.Delta.Cases))
		for _, acceptedCase := range round.Delta.Cases {
			acceptedCases[acceptedCase.CaseID] = acceptedCase
		}
		fmt.Fprintln(&buffer, "| Case | Baseline | Accepted | Candidate | Baseline delta | Accepted delta | Transition |")
		fmt.Fprintln(&buffer, "| --- | ---: | ---: | ---: | ---: | ---: | --- |")
		for _, evalCase := range round.Delta.Cases {
			baselineCase := findCaseDelta(round.BaselineDelta, evalCase.CaseID)
			acceptedCase := acceptedCases[evalCase.CaseID]
			fmt.Fprintf(&buffer, "| %s | %.4f | %.4f | %.4f | %+.4f | %+.4f | %s |\n",
				markdownTable(evalCase.CaseID), baselineCase.BaselineScore,
				acceptedCase.BaselineScore, evalCase.CandidateScore,
				baselineCase.ScoreDelta, acceptedCase.ScoreDelta,
				markdownTable(string(baselineCase.Kind)))
		}
	}
	fmt.Fprintln(&buffer)
	fmt.Fprintln(&buffer, "## Failure Attribution")
	fmt.Fprintln(&buffer)
	fmt.Fprintf(&buffer, "Baseline failures: `%d`; classified without fallback: `%d`.\n\n",
		report.BaselineAttribution.Summary.TotalFailures,
		report.BaselineAttribution.Summary.AttributedFailures)
	categories := make([]string, 0, len(report.BaselineAttribution.Summary.CategoryCounts))
	for category := range report.BaselineAttribution.Summary.CategoryCounts {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	for _, category := range categories {
		fmt.Fprintf(&buffer, "- `%s`: %d\n", markdownInline(category),
			report.BaselineAttribution.Summary.CategoryCounts[FailureCategory(category)])
	}
	fmt.Fprintln(&buffer)
	fmt.Fprintln(&buffer, "## Usage")
	fmt.Fprintln(&buffer)
	fmt.Fprintf(&buffer, "- Prompt tokens: `%d`\n", report.Usage.PromptTokens)
	fmt.Fprintf(&buffer, "- Completion tokens: `%d`\n", report.Usage.CompletionTokens)
	fmt.Fprintf(&buffer, "- Model calls: `%d`\n", report.Usage.ModelCalls)
	fmt.Fprintf(&buffer, "- Tool calls: `%d`\n", report.Usage.ToolCalls)
	fmt.Fprintf(&buffer, "- Measured: `%t`\n", report.Usage.Measured)
	fmt.Fprintf(&buffer, "- Run duration: `%s`\n", report.Run.Duration)
	fmt.Fprintf(&buffer, "- Audited trace duration: `%s`\n", report.Usage.Duration)
	fmt.Fprintln(&buffer, "- Cost basis: measured token and call counts; no currency estimate is assigned in fake mode")
	if _, err := writer.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

// WriteReports publishes JSON and Markdown as one run-specific directory.
// Readers never observe one file from a newer generation beside an older one.
func WriteReports(outputDir string, report *Report) (ReportPaths, error) {
	if strings.TrimSpace(outputDir) == "" {
		return ReportPaths{}, errors.New("report output directory is empty")
	}
	if err := validateReport(report); err != nil {
		return ReportPaths{}, err
	}
	if err := validateRunID(report.Run.ID); err != nil {
		return ReportPaths{}, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return ReportPaths{}, fmt.Errorf("create report output directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(outputDir, ".promptiter-report-")
	if err != nil {
		return ReportPaths{}, fmt.Errorf("create report staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	jsonPath := filepath.Join(stagingDir, "optimization_report.json")
	markdownPath := filepath.Join(stagingDir, "optimization_report.md")
	if err := writeSynced(jsonPath, func(writer io.Writer) error { return WriteJSON(writer, report) }); err != nil {
		return ReportPaths{}, err
	}
	if err := writeSynced(markdownPath, func(writer io.Writer) error { return WriteMarkdown(writer, report) }); err != nil {
		return ReportPaths{}, err
	}
	finalDir := filepath.Join(outputDir, report.Run.ID)
	if _, err := os.Stat(finalDir); err == nil {
		return ReportPaths{}, fmt.Errorf("report generation %q already exists", report.Run.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ReportPaths{}, fmt.Errorf("inspect report generation: %w", err)
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return ReportPaths{}, fmt.Errorf("publish report generation: %w", err)
	}
	published = true
	return ReportPaths{
		Directory:    finalDir,
		JSONPath:     filepath.Join(finalDir, "optimization_report.json"),
		MarkdownPath: filepath.Join(finalDir, "optimization_report.md"),
	}, nil
}

func validateReportBase(report *Report) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if report.SchemaVersion != reportSchemaVersion {
		return fmt.Errorf("unsupported report schema %q", report.SchemaVersion)
	}
	if err := validateRunID(report.Run.ID); err != nil {
		return err
	}
	if report.BaselineTrain == nil || report.BaselineValidation == nil {
		return errors.New("report baseline evaluations are incomplete")
	}
	return nil
}

func validateReport(report *Report) error {
	if err := validateReportBase(report); err != nil {
		return err
	}
	if report.Run.Status != "succeeded" && report.Run.Status != "failed" {
		return fmt.Errorf("report run status %q is not terminal", report.Run.Status)
	}
	for _, round := range report.Rounds {
		if err := validateRound(round); err != nil {
			return err
		}
	}
	if report.ShouldWriteBack {
		if report.SelectedCandidate == nil || report.SelectedAttempt <= 0 || !report.Decision.Accepted {
			return errors.New("writeback report has no accepted candidate")
		}
	}
	return nil
}

func validateRound(round RoundReport) error {
	if round.Attempt <= 0 {
		return errors.New("round attempt must be greater than zero")
	}
	if round.InputPrompt.SurfaceID == "" || round.CandidatePrompt.SurfaceID == "" {
		return fmt.Errorf("round %d prompt identity is incomplete", round.Attempt)
	}
	if round.Train == nil || round.Validation == nil || round.Delta == nil || round.BaselineDelta == nil {
		return fmt.Errorf("round %d evaluation artifacts are incomplete", round.Attempt)
	}
	if len(round.Gate.Reasons) == 0 {
		return fmt.Errorf("round %d gate has no explanation", round.Attempt)
	}
	return nil
}

func findCaseDelta(delta *DeltaSummary, caseID string) CaseDelta {
	if delta != nil {
		for _, evalCase := range delta.Cases {
			if evalCase.CaseID == caseID {
				return evalCase
			}
		}
	}
	return CaseDelta{CaseID: caseID}
}

func validateRunID(runID string) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("report run id is empty")
	}
	if runID == "." || runID == ".." || filepath.Base(runID) != runID || !utf8.ValidString(runID) {
		return fmt.Errorf("report run id %q is unsafe", runID)
	}
	return nil
}

func writeSynced(path string, render func(io.Writer) error) (resultErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close report file: %w", closeErr))
		}
	}()
	if err := render(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync report file: %w", err)
	}
	return nil
}

func writeFenced(writer io.Writer, value string) {
	fence := strings.Repeat("`", longestBacktickRun(value)+1)
	if len(fence) < 3 {
		fence = "```"
	}
	fmt.Fprintln(writer, fence+"text")
	fmt.Fprintln(writer, value)
	fmt.Fprintln(writer, fence)
}

func longestBacktickRun(value string) int {
	longest, current := 0, 0
	for _, character := range value {
		if character == '`' {
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

func markdownInline(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return strings.ReplaceAll(value, "\n", " ")
}

func markdownTable(value string) string {
	value = markdownText(value)
	return strings.ReplaceAll(value, "|", "\\|")
}
