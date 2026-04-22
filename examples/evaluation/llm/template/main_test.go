//
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
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type stubEvaluator struct {
	result    *evaluation.EvaluationResult
	evaluate  error
	closeErr  error
	closed    bool
	evalSetID string
}

func (s *stubEvaluator) Evaluate(ctx context.Context,
	evalSetID string, opt ...evaluation.Option) (*evaluation.EvaluationResult, error) {
	s.evalSetID = evalSetID
	return s.result, s.evaluate
}

func (s *stubEvaluator) Close() error {
	s.closed = true
	return s.closeErr
}

func TestRunExamplePrintsSummary(t *testing.T) {
	stub := &stubEvaluator{
		result: &evaluation.EvaluationResult{
			AppName:       appName,
			EvalSetID:     "template-basic",
			OverallStatus: status.EvalStatusPassed,
			EvalCases: []*evaluation.EvaluationCaseResult{
				{
					EvalCaseID:    "capital_of_france",
					OverallStatus: status.EvalStatusPassed,
				},
			},
		},
	}
	output := captureStdout(t, func() {
		err := runExample(context.Background(), func(gotAppName string, opts runOptions) (exampleEvaluator, error) {
			assert.Equal(t, appName, gotAppName)
			assert.Equal(t, "template-basic", opts.EvalSetID)
			return stub, nil
		}, runOptions{
			OutputDir: "./output",
			EvalSetID: "template-basic",
		})
		require.NoError(t, err)
	})
	assert.True(t, stub.closed)
	assert.Equal(t, "template-basic", stub.evalSetID)
	assert.Contains(t, output, "Template evaluation completed with local storage")
	assert.Contains(t, output, "Case capital_of_france -> passed")
	assert.Contains(t, output, "Results saved under: ./output")
}

func TestRunExampleReturnsFactoryError(t *testing.T) {
	err := runExample(context.Background(), func(string, runOptions) (exampleEvaluator, error) {
		return nil, errors.New("boom")
	}, runOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create evaluator")
}

func TestRunExampleReturnsEvaluateError(t *testing.T) {
	stub := &stubEvaluator{evaluate: errors.New("evaluate failed")}
	err := runExample(context.Background(), func(string, runOptions) (exampleEvaluator, error) {
		return stub, nil
	}, runOptions{EvalSetID: "template-basic"})
	require.Error(t, err)
	assert.True(t, stub.closed)
	assert.Contains(t, err.Error(), "evaluate")
}

func TestNewLocalEvaluator(t *testing.T) {
	dataDir := filepath.Join("data")
	outputDir := t.TempDir()
	evaluator, err := newLocalEvaluator(appName, runOptions{
		DataDir:   dataDir,
		OutputDir: outputDir,
		ModelName: "gpt-5.2",
		EvalSetID: "template-basic",
	})
	require.NoError(t, err)
	require.NotNil(t, evaluator)
	assert.NoError(t, evaluator.Close())
}

func TestNewQAAgent(t *testing.T) {
	assert.NotNil(t, newQAAgent("gpt-5.2", true))
}

func TestPointerHelpers(t *testing.T) {
	require.NotNil(t, intPtr(64))
	assert.Equal(t, 64, *intPtr(64))
	require.NotNil(t, floatPtr(0.5))
	assert.InDelta(t, 0.5, *floatPtr(0.5), 1e-9)
}

func TestPrintSummary(t *testing.T) {
	output := captureStdout(t, func() {
		printSummary(&evaluation.EvaluationResult{
			AppName:       appName,
			EvalSetID:     "template-basic",
			OverallStatus: status.EvalStatusPassed,
			EvalCases: []*evaluation.EvaluationCaseResult{
				{
					EvalCaseID:    "capital_of_france",
					OverallStatus: status.EvalStatusPassed,
				},
			},
		}, "./output")
	})
	assert.Contains(t, output, "App: template-eval-app")
	assert.Contains(t, output, "Eval Set: template-basic")
	assert.Contains(t, output, "Overall Status: passed")
	assert.Contains(t, output, "Runs: 0")
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origin := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writer
	defer func() {
		os.Stdout = origin
	}()
	fn()
	require.NoError(t, writer.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	require.NoError(t, err)
	return buf.String()
}
