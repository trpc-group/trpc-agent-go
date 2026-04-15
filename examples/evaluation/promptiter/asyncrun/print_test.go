//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"io"
	"os"
	"testing"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestPrintSummaryAllowsZeroRounds(t *testing.T) {
	result := &promptiterengine.RunResult{
		Status:             promptiterengine.RunStatusSucceeded,
		BaselineValidation: &promptiterengine.EvaluationResult{OverallScore: 0.91},
	}
	err := captureStdout(func() error {
		return printSummary(result, "./data", "./output", "initial", "candidate#instruction")
	})
	if err != nil {
		t.Fatalf("printSummary returned error: %v", err)
	}
}

func TestPrintSummaryAllowsPartialRoundData(t *testing.T) {
	result := &promptiterengine.RunResult{
		Status:             promptiterengine.RunStatusSucceeded,
		BaselineValidation: &promptiterengine.EvaluationResult{OverallScore: 0.35},
		Rounds: []promptiterengine.RoundResult{
			{Round: 1},
		},
	}
	err := captureStdout(func() error {
		return printSummary(result, "./data", "./output", "initial", "candidate#instruction")
	})
	if err != nil {
		t.Fatalf("printSummary returned error: %v", err)
	}
}

func captureStdout(fn func() error) error {
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	os.Stdout = writer
	runErr := fn()
	closeErr := writer.Close()
	os.Stdout = originalStdout
	_, copyErr := io.Copy(io.Discard, reader)
	readCloseErr := reader.Close()
	if runErr != nil {
		return runErr
	}
	if closeErr != nil {
		return closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	return readCloseErr
}
