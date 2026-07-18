//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestFakeRegressionLoopEndToEnd(t *testing.T) {
	started := time.Now()
	outputDir := filepath.Join(t.TempDir(), "new-output")
	report, err := runCommand(context.Background(), commandOptions{
		paths: defaultPaths(), outputDir: outputDir, seed: 2003,
	})
	if err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}
	if time.Since(started) > 3*time.Minute {
		t.Fatalf("fake loop exceeded three minutes")
	}
	if report.Status != "accepted" || !report.Decision.WriteBackRecommended || len(report.Rounds) != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.Rounds[0].Gate.Accepted || report.Rounds[1].Gate.Accepted || !report.Rounds[2].Gate.Accepted {
		t.Fatalf("round gates = %+v, %+v, %+v", report.Rounds[0].Gate, report.Rounds[1].Gate, report.Rounds[2].Gate)
	}
	if report.Rounds[0].ValidationDelta.ScoreDelta != 0 {
		t.Fatalf("ineffective round delta = %+v", report.Rounds[0].ValidationDelta)
	}
	if report.Rounds[1].TrainDelta.ScoreDelta <= 0 || report.Rounds[1].ValidationDelta.ScoreDelta >= 0 {
		t.Fatalf("overfit round deltas = train %+v, validation %+v",
			report.Rounds[1].TrainDelta, report.Rounds[1].ValidationDelta)
	}
	if report.Rounds[2].ValidationDelta.ScoreDelta <= 0 || report.Cost.ModelCalls == 0 || report.Cost.Tokens == 0 {
		t.Fatalf("accepted round or cost = %+v, %+v", report.Rounds[2], report.Cost)
	}
	for _, name := range []string{"optimization_report.json", "optimization_report.md"} {
		path := filepath.Join(outputDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), "candidate accepted by regression gate") {
			t.Fatalf("%s does not contain decision", name)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %v", name, info.Mode().Perm())
		}
	}
}

func TestRunCommandPreservesExistingOutputDirectoryPermissions(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "existing-output")
	if err := os.Mkdir(outputDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(context.Background(), commandOptions{paths: defaultPaths(), outputDir: outputDir, seed: 2003}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("output directory permissions = %o, want 750", info.Mode().Perm())
	}
}

func TestPromptIterConsumesFailureHints(t *testing.T) {
	config, err := loadConfig(defaultPaths(), 2003)
	if err != nil {
		t.Fatal(err)
	}
	engine, _, closeRuntime, err := newPromptIterRuntime(context.Background(), config, config.candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	base := promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: config.train.EvalSetID}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: config.validation.EvalSetID}},
	}
	request := regression.CandidateRequest{Prompt: config.baseline, Round: 1, Seed: config.seed,
		Hints: []regression.FailureHint{{CaseID: "train-case-2", MetricName: "quality",
			Category: regression.FailureFinalResponse, Reason: "final response mismatch"}}}
	prompt, err := regression.GeneratePromptIter(context.Background(), engine, base, targetSurfaceID, request)
	if err != nil || prompt != config.candidates[0] {
		t.Fatalf("GeneratePromptIter() = %q, %v", prompt, err)
	}
	request.Hints[0].MetricName = "missing-metric"
	if _, err := regression.GeneratePromptIter(context.Background(), engine, base, targetSurfaceID, request); err == nil {
		t.Fatal("GeneratePromptIter() accepted a hint unrelated to evaluation failures")
	}
}

func TestDeterministicGeneratorUsesRound(t *testing.T) {
	config, err := loadConfig(defaultPaths(), 2003)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runFakeLoop(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	for index, round := range run.Rounds {
		if round.CandidatePrompt != config.candidates[index] {
			t.Fatalf("round %d prompt = %q", index+1, round.CandidatePrompt)
		}
		if round.InputPrompt != config.baseline {
			t.Fatalf("rejected prompt was promoted in round %d", index+1)
		}
	}
}
