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

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestPrintSummaryAllowsPartialRoundData(t *testing.T) {
	result := &promptiterengine.RunResult{
		Structure: &astructure.Snapshot{
			StructureID: "struct_1",
		},
		Rounds: []promptiterengine.RoundResult{
			{
				Round:      1,
				Validation: &promptiterengine.EvaluationResult{OverallScore: 0.42},
			},
		},
	}
	err := captureStdout(func() error {
		return printSummary(result, "./data", "./output", "candidate#tool.lookup_record")
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
	defer func() { os.Stdout = originalStdout }()
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(io.Discard, reader)
		readCloseErr := reader.Close()
		if copyErr != nil {
			copyDone <- copyErr
			return
		}
		copyDone <- readCloseErr
	}()
	runErr := fn()
	closeErr := writer.Close()
	readCloseErr := <-copyDone
	if runErr != nil {
		return runErr
	}
	if closeErr != nil {
		return closeErr
	}
	return readCloseErr
}
