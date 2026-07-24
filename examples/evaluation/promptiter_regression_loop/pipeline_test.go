// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

func TestRunPipelineEndToEnd(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("data", "promptiter-regression-app", "promptiter.json"))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixed := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	report, err := runPipeline(ctx, cfg, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("runPipeline() error = %v", err)
	}
	if report.Run.Status != "succeeded" || len(report.Rounds) != 3 {
		t.Fatalf("pipeline report is incomplete: status=%q rounds=%d", report.Run.Status, len(report.Rounds))
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

func TestPatchRecordsRejectsUnsupportedSurfaceValue(t *testing.T) {
	_, err := patchRecords(&promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: "candidate#skill.example",
		Value:     astructure.SurfaceValue{Skills: []astructure.SkillRef{{}}},
	}}})
	if err == nil {
		t.Fatal("patchRecords() error = nil, want unsupported surface value error")
	}
}
