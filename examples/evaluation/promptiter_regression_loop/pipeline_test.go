// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

var sampleTimingPattern = regexp.MustCompile(`"time_to_first_token":[0-9]+`)

func TestRunPipelineEndToEnd(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("data", "promptiter-regression-app", "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixed := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	inputs, err := loadInputSnapshot(cfg)
	if err != nil {
		t.Fatalf("loadInputSnapshot() error = %v", err)
	}
	report, err := runPipeline(ctx, cfg, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("runPipeline() error = %v", err)
	}
	if report.Run.Status != "succeeded" || len(report.Rounds) != 3 {
		t.Fatalf("pipeline report is incomplete: status=%q rounds=%d", report.Run.Status, len(report.Rounds))
	}
	wantInputSHA256 := inputs.sha256
	if report.Run.InputSHA256 != wantInputSHA256 || report.Run.ID != runID(fixed, wantInputSHA256) {
		t.Fatalf("run identity = %q/%q, want input fingerprint %q",
			report.Run.ID, report.Run.InputSHA256, wantInputSHA256)
	}
	if report.BaselineTrain.OverallScore != 1.0/3.0 || report.BaselineValidation.OverallScore != 2.0/3.0 {
		t.Fatalf("baseline scores = %v/%v, want 1/3 and 2/3",
			report.BaselineTrain.OverallScore, report.BaselineValidation.OverallScore)
	}
	wantGate := []bool{true, false, false}
	for index, accepted := range wantGate {
		if report.Rounds[index].Gate.Accepted != accepted {
			t.Errorf("round %d gate accepted = %t, want %t", index+1, report.Rounds[index].Gate.Accepted, accepted)
		}
	}
	if report.Rounds[2].Train.OverallScore != 1 || report.Rounds[2].Validation.OverallScore != 2.0/3.0 {
		t.Fatal("overfit attempt did not improve train while regressing validation")
	}
	if math.Abs(report.Rounds[1].Delta.ScoreDelta) > 1e-9 ||
		math.Abs(report.Rounds[1].BaselineDelta.ScoreDelta-1.0/3.0) > 1e-9 {
		t.Fatalf("round 2 deltas = accepted %.4f, baseline %.4f; want 0 and 1/3",
			report.Rounds[1].Delta.ScoreDelta, report.Rounds[1].BaselineDelta.ScoreDelta)
	}
	if math.Abs(report.Rounds[2].Delta.ScoreDelta+1.0/3.0) > 1e-9 ||
		math.Abs(report.Rounds[2].BaselineDelta.ScoreDelta) > 1e-9 {
		t.Fatalf("round 3 deltas = accepted %.4f, baseline %.4f; want -1/3 and 0",
			report.Rounds[2].Delta.ScoreDelta, report.Rounds[2].BaselineDelta.ScoreDelta)
	}
	if report.SelectedAttempt != 1 || report.SelectedCandidate == nil ||
		report.SelectedCandidate.Text != candidateOneInstruction || !report.ShouldWriteBack {
		t.Fatalf("selected candidate = attempt %d %+v, want accepted attempt 1",
			report.SelectedAttempt, report.SelectedCandidate)
	}
	paths, err := regression.WriteReports(t.TempDir(), report)
	if err != nil {
		t.Fatalf("WriteReports() error = %v", err)
	}
	if paths.JSONPath == "" || paths.MarkdownPath == "" {
		t.Fatalf("report paths are incomplete: %+v", paths)
	}
	assertSampleReports(t, report, fixed)
}

func TestRunPipelineSupportsCustomConfigDirectory(t *testing.T) {
	configDir := copyPipelineInputFixture(t)
	cfg, err := loadConfig(filepath.Join(configDir, "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if filepath.Base(cfg.DataDir) == cfg.AppName {
		t.Fatalf("test requires config directory %q to differ from app name %q",
			cfg.DataDir, cfg.AppName)
	}
	fixed := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	report, err := runPipeline(context.Background(), cfg, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("runPipeline() error = %v", err)
	}
	if report.Run.Status != "succeeded" || report.SelectedAttempt != 1 {
		t.Fatalf("custom config pipeline report = status %q, selected attempt %d",
			report.Run.Status, report.SelectedAttempt)
	}
}

func TestRunPipelineUsesImmutableInputSnapshot(t *testing.T) {
	configDir := copyPipelineInputFixture(t)
	cfg, err := loadConfig(filepath.Join(configDir, "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	inputs, err := loadInputSnapshot(cfg)
	if err != nil {
		t.Fatalf("loadInputSnapshot() error = %v", err)
	}
	for _, name := range []string{
		"baseline_prompt.txt",
		"train.evalset.json",
		"validation.evalset.json",
		"metrics.json",
		"promptiter.json",
	} {
		appendInvalidInput(t, filepath.Join(configDir, name))
	}
	fixed := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	report, err := runPipelineWithSnapshot(
		context.Background(), cfg, inputs, func() time.Time { return fixed },
	)
	if err != nil {
		t.Fatalf("runPipelineWithSnapshot() error = %v", err)
	}
	if report.Run.Status != "succeeded" || report.Run.InputSHA256 != inputs.sha256 {
		t.Fatalf("snapshot pipeline report = status %q, fingerprint %q; want succeeded/%q",
			report.Run.Status, report.Run.InputSHA256, inputs.sha256)
	}
}

func TestRunPipelineHonorsCanceledContext(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("data", "promptiter-regression-app", "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := runPipeline(ctx, cfg, time.Now)
	if report != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("runPipeline() = (%+v, %v), want nil, context.Canceled", report, err)
	}
}

func TestHandleRuntimeClosePreservesSucceededReport(t *testing.T) {
	selected := &regression.PromptRecord{SurfaceID: "candidate#instruction", Text: "accepted"}
	report := &regression.Report{
		Run:               regression.RunMetadata{Status: "succeeded"},
		SelectedAttempt:   1,
		SelectedCandidate: selected,
		ShouldWriteBack:   true,
	}
	closeErr := errors.New("close runtime")
	resultErr := handleRuntimeClose(report, time.Time{}, time.Now, nil, closeErr)
	if !errors.Is(resultErr, closeErr) {
		t.Fatalf("handleRuntimeClose() error = %v, want close error", resultErr)
	}
	if report.Run.Status != "succeeded" || report.SelectedAttempt != 1 ||
		report.SelectedCandidate != selected || !report.ShouldWriteBack {
		t.Fatalf("cleanup changed succeeded report: %+v", report)
	}
}

func TestPrintCompletionOnlyForSuccessfulRun(t *testing.T) {
	paths := regression.ReportPaths{JSONPath: "report.json", MarkdownPath: "report.md"}
	report := &regression.Report{ShouldWriteBack: true}
	tests := []struct {
		name        string
		pipelineErr error
		writeErr    error
		wantOutput  string
	}{
		{
			name: "success",
			wantOutput: "PromptIter regression loop completed\n" +
				"JSON report: report.json\n" +
				"Markdown report: report.md\n" +
				"Write back: true\n",
		},
		{name: "pipeline failure", pipelineErr: errors.New("pipeline failed")},
		{name: "write failure", writeErr: errors.New("write failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			printCompletion(&output, paths, report, test.pipelineErr, test.writeErr)
			if got := output.String(); got != test.wantOutput {
				t.Fatalf("printCompletion() output = %q, want %q", got, test.wantOutput)
			}
		})
	}
}

func TestRequireSingleRoundRejectsMissingBaselineValidation(t *testing.T) {
	evaluationResult := &promptiterengine.EvaluationResult{}
	result := &promptiterengine.RunResult{
		Status: promptiterengine.RunStatusSucceeded,
		Rounds: []promptiterengine.RoundResult{{
			OutputProfile: &promptiter.Profile{},
			Train:         evaluationResult,
			Validation:    evaluationResult,
			Acceptance:    &promptiterengine.AcceptanceDecision{},
		}},
	}
	_, err := requireSingleRound(result, 1)
	if err == nil || err.Error() != "PromptIter attempt 1 baseline validation is missing" {
		t.Fatalf("requireSingleRound() error = %v, want missing baseline validation", err)
	}
}

func TestPatchRecordsRejectsUnsupportedSurfaceValue(t *testing.T) {
	_, err := patchRecords(&promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: "candidate#skill.example",
		Value:     astructure.SurfaceValue{Skills: []astructure.SkillRef{{}}},
	}}})
	if err == nil {
		t.Fatal("patchRecords() error = nil, want unsupported surface value error")
	}
}

func assertSampleReports(t *testing.T, report *regression.Report, startedAt time.Time) {
	t.Helper()
	if len(report.Run.InputSHA256) < 12 {
		t.Fatalf("input fingerprint %q is too short for sample identity", report.Run.InputSHA256)
	}
	report.Run.ID = "sample-" + report.Run.InputSHA256[:12]
	report.Run.StartedAt = startedAt
	report.Run.Duration = 0
	report.Usage.Duration = 0
	clearEvaluationDurations(report.BaselineTrain)
	clearEvaluationDurations(report.BaselineValidation)
	for index := range report.Rounds {
		round := &report.Rounds[index]
		clearEvaluationDurations(round.Train)
		clearEvaluationDurations(round.Validation)
		round.Usage.Duration = 0
		round.Duration = 0
	}

	var jsonReport bytes.Buffer
	if err := regression.WriteJSON(&jsonReport, report); err != nil {
		t.Fatalf("WriteJSON() sample error = %v", err)
	}
	wantJSON, err := os.ReadFile(filepath.Join("sample", "optimization_report.json"))
	if err != nil {
		t.Fatalf("read JSON sample: %v", err)
	}
	if !bytes.Equal(jsonReport.Bytes(), wantJSON) {
		t.Fatal("JSON sample does not match the deterministic pipeline report")
	}

	var markdownReport bytes.Buffer
	if err := regression.WriteMarkdown(&markdownReport, report); err != nil {
		t.Fatalf("WriteMarkdown() sample error = %v", err)
	}
	wantMarkdown, err := os.ReadFile(filepath.Join("sample", "optimization_report.md"))
	if err != nil {
		t.Fatalf("read Markdown sample: %v", err)
	}
	if !bytes.Equal(markdownReport.Bytes(), wantMarkdown) {
		t.Fatal("Markdown sample does not match the deterministic pipeline report")
	}
}

func clearEvaluationDurations(result *regression.EvaluationResult) {
	result.ExecutionTime = 0
	result.Usage.Duration = 0
	for index := range result.Cases {
		evalCase := &result.Cases[index]
		evalCase.Trace.Usage.Duration = 0
		for stepIndex := range evalCase.Trace.Steps {
			step := &evalCase.Trace.Steps[stepIndex]
			step.Output = sampleTimingPattern.ReplaceAllString(step.Output, `"time_to_first_token":0`)
		}
	}
}

func copyPipelineInputFixture(t *testing.T) string {
	t.Helper()
	sourceDir := filepath.Join("data", "promptiter-regression-app")
	configDir := filepath.Join(t.TempDir(), "custom-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create custom config directory: %v", err)
	}
	for _, name := range []string{
		"baseline_prompt.txt",
		"train.evalset.json",
		"validation.evalset.json",
		"metrics.json",
		"promptiter.json",
	} {
		data, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("read fixture %q: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(configDir, name), data, 0o600); err != nil {
			t.Fatalf("write fixture %q: %v", name, err)
		}
	}
	return configDir
}

func appendInvalidInput(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open input %q for mutation: %v", path, err)
	}
	if _, err := file.WriteString("\ninvalid"); err != nil {
		_ = file.Close()
		t.Fatalf("mutate input %q: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close mutated input %q: %v", path, err)
	}
}
