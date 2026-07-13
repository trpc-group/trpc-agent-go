//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression/report"
)

func TestStrictRepeatedScenarioExperiment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	baseOutput := t.TempDir()
	tests := []struct {
		scenario string
		expected regression.Decision
	}{
		{scenario: "success", expected: regression.DecisionAccepted},
		{scenario: "no-effect", expected: regression.DecisionRejected},
		{scenario: "overfit", expected: regression.DecisionRejected},
	}
	const repetitions = 10
	started := time.Now()
	correct := 0
	for repetition := 0; repetition < repetitions; repetition++ {
		for _, test := range tests {
			runID := fmt.Sprintf("experiment-%s-%02d", test.scenario, repetition)
			result, files, err := run(
				ctx,
				test.scenario,
				runID,
				filepath.Join(baseOutput, runID),
				"data",
			)
			if err != nil {
				t.Fatalf("%s: %v", runID, err)
			}
			if len(files) != 2 {
				t.Fatalf("%s: artifact count = %d, want 2", runID, len(files))
			}
			if result.Decision != test.expected {
				t.Fatalf("%s: decision = %q, want %q", runID, result.Decision, test.expected)
			}
			correct++
		}
	}
	elapsed := time.Since(started)
	total := repetitions * len(tests)
	accuracy := float64(correct) / float64(total)
	t.Logf("strict scenario experiment: correct=%d total=%d accuracy=%.3f elapsed=%s", correct, total, accuracy, elapsed)
	if accuracy < .8 {
		t.Fatalf("scenario decision accuracy %.3f is below issue requirement 0.80", accuracy)
	}
	if elapsed > 3*time.Minute {
		t.Fatalf("fake pipeline experiment took %s, issue limit is 3m", elapsed)
	}
}

func TestNormalizedReportsAreReproducibleAcrossRuns(t *testing.T) {
	const repetitions = 10
	var expected []byte
	for repetition := 0; repetition < repetitions; repetition++ {
		result, _, err := run(
			context.Background(),
			"success",
			"reproducible-success",
			filepath.Join(t.TempDir(), fmt.Sprintf("run-%02d", repetition)),
			"data",
		)
		if err != nil {
			t.Fatal(err)
		}
		result.StartedAt = time.Time{}
		result.EndedAt = time.Time{}
		result.Usage.Latency = 0
		jsonReport, err := report.JSON(result)
		if err != nil {
			t.Fatal(err)
		}
		markdownReport, err := report.Markdown(result)
		if err != nil {
			t.Fatal(err)
		}
		combined := append(append([]byte(nil), jsonReport...), markdownReport...)
		if repetition == 0 {
			expected = combined
			continue
		}
		if !bytes.Equal(combined, expected) {
			t.Fatalf("normalized report changed on repetition %d", repetition)
		}
	}
	digest := sha256.Sum256(expected)
	t.Logf("reproducible report experiment: repetitions=%d sha256=%x", repetitions, digest)
}
