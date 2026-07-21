//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestStrictTraceReplayRequiresCompleteEvidence(t *testing.T) {
	evaluate := func(t *testing.T, output FakeOutput) (*EvaluationSummary, error) {
		t.Helper()
		evaluator, err := NewLocalEvaluator([]MetricConfig{{
			MetricName: metricFinalResponse,
			Threshold:  1,
			Weight:     1,
		}}, "baseline", "trace")
		require.NoError(t, err)
		set := &EvalSet{
			EvalSetID:     "strict-trace",
			PassThreshold: testScore(1),
			EvalCases: []EvalCase{newFailureCase(
				"case", "answer", nil, Expectations{}, output,
			)},
		}
		prompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
		return evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	}

	summary, err := evaluate(t, completeStrictTraceOutput())
	require.NoError(t, err)
	assert.True(t, summary.Cases[0].Passed)

	multiRetrieval := completeStrictTraceOutput()
	multiTrace := append([]TraceStep(nil), multiRetrieval.Trace[:2]...)
	multiTrace = append(
		multiTrace,
		TraceStep{
			StepID: "retrieval-initial", Kind: "retrieval", Status: "completed", ElapsedMS: traceMilliseconds(5),
			Output: map[string]any{"documents": 0, "facts": []string{}, "matchedFacts": 0},
		},
	)
	multiRetrieval.Trace = append(multiTrace, multiRetrieval.Trace[2:]...)
	_, err = evaluate(t, multiRetrieval)
	require.NoError(t, err, "only the final retrieval step should be the cumulative output summary")

	tests := []struct {
		name      string
		mutate    func(*FakeOutput)
		wantError string
	}{
		{
			name: "decorative route only",
			mutate: func(output *FakeOutput) {
				output.Trace = output.Trace[:1]
				output.Usage.ModelCalls = 0
				output.Usage.ToolCalls = 0
				output.Usage.LatencyMS = 1
				output.Tools = nil
				output.RetrievedFacts = nil
				output.RetrievedDocuments = 0
			},
			wantError: "must end with a final_response",
		},
		{
			name: "missing route evidence",
			mutate: func(output *FakeOutput) {
				output.Trace = output.Trace[1:]
			},
			wantError: "has no trace evidence",
		},
		{
			name: "missing tool result",
			mutate: func(output *FakeOutput) {
				output.Trace[1].Output = nil
			},
			wantError: "trace output for tool",
		},
		{
			name: "retrieval facts mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[2].Output["facts"] = []string{"different"}
			},
			wantError: "facts do not match",
		},
		{
			name: "model calls mismatch",
			mutate: func(output *FakeOutput) {
				output.Usage.ModelCalls = 2
			},
			wantError: "trace model calls",
		},
		{
			name: "tool calls mismatch",
			mutate: func(output *FakeOutput) {
				output.Usage.ToolCalls = 2
			},
			wantError: "trace tool calls",
		},
		{
			name: "missing elapsed",
			mutate: func(output *FakeOutput) {
				output.Trace[1].ElapsedMS = nil
			},
			wantError: "has no elapsedMs",
		},
		{
			name: "non-monotonic elapsed",
			mutate: func(output *FakeOutput) {
				output.Trace[2].ElapsedMS = traceMilliseconds(1)
			},
			wantError: "non-monotonic",
		},
		{
			name: "terminal latency mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[3].ElapsedMS = traceMilliseconds(9)
			},
			wantError: "does not match usage latency",
		},
		{
			name: "terminal response mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[3].Message = "different"
			},
			wantError: "does not match output response",
		},
		{
			name: "missing terminal usage evidence",
			mutate: func(output *FakeOutput) {
				output.Trace[3].Usage = nil
			},
			wantError: "terminal trace usage",
		},
		{
			name: "terminal usage mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[3].Usage.InputTokens++
			},
			wantError: "terminal trace usage",
		},
		{
			name: "missing rubric score evidence",
			mutate: func(output *FakeOutput) {
				output.Trace[3].RubricScore = nil
			},
			wantError: "rubric score",
		},
		{
			name: "rubric reason mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[3].RubricReason = "different"
			},
			wantError: "rubric reason",
		},
		{
			name: "spoofed route kind",
			mutate: func(output *FakeOutput) {
				output.Trace[0].Kind = "not_route"
			},
			wantError: "has no trace evidence",
		},
		{
			name: "spoofed retrieval kind",
			mutate: func(output *FakeOutput) {
				output.Trace[2].Kind = "knowledge_guess"
			},
			wantError: "retrieval output has no trace evidence",
		},
		{
			name: "spoofed failure status",
			mutate: func(output *FakeOutput) {
				output.Trace[0].Status = "not_failed"
			},
			wantError: "unknown status",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := completeStrictTraceOutput()
			test.mutate(&output)
			_, err := evaluate(t, output)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func completeStrictTraceOutput() FakeOutput {
	usage := Usage{ModelCalls: 1, ToolCalls: 1, InputTokens: 12, OutputTokens: 4, CostUSD: 0.01, LatencyMS: 10}
	rubricScore := 0.9
	return FakeOutput{
		Response:           "answer",
		Route:              "specialist",
		RubricScore:        &rubricScore,
		RubricReason:       "grounded answer",
		RetrievedFacts:     []string{"fact-a"},
		RetrievedDocuments: 1,
		Tools: []*evalset.Tool{{
			Name:      "lookup",
			Arguments: map[string]any{"id": "A100"},
			Result:    map[string]any{"status": "ok"},
		}},
		Trace: []TraceStep{
			{StepID: "route", Kind: "route", Name: "specialist", Status: "completed", ElapsedMS: traceMilliseconds(1)},
			{
				StepID: "tool", Kind: "tool", Name: "lookup", Status: "completed", ElapsedMS: traceMilliseconds(4),
				Input: map[string]any{"id": "A100"}, Output: map[string]any{"status": "ok"},
			},
			{
				StepID: "retrieval", Kind: "retrieval", Name: "knowledge", Status: "completed", ElapsedMS: traceMilliseconds(6),
				Output: map[string]any{"documents": 1, "facts": []string{"fact-a"}, "matchedFacts": 1},
			},
			{
				StepID: "final", Kind: "final_response", Status: "completed", ElapsedMS: traceMilliseconds(10),
				Message: "answer", RubricScore: &rubricScore, RubricReason: "grounded answer", Usage: &usage,
			},
		},
		Usage: usage,
	}
}

func TestStrictTraceReplayAuditsFailedExecutionEvidence(t *testing.T) {
	evaluate := func(t *testing.T, output FakeOutput) (*EvaluationSummary, error) {
		t.Helper()
		evaluator, err := NewLocalEvaluator([]MetricConfig{{MetricName: metricFinalResponse, Threshold: 1, Weight: 1}}, "baseline", "trace")
		require.NoError(t, err)
		set := &EvalSet{
			EvalSetID:     "failed-trace",
			PassThreshold: testScore(1),
			EvalCases: []EvalCase{newFailureCase(
				"case", "answer", nil, Expectations{}, output,
			)},
		}
		prompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
		return evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	}

	valid := completeFailedStrictTraceOutput()
	summary, err := evaluate(t, valid)
	require.NoError(t, err)
	assert.True(t, summary.Cases[0].HardFail)
	assert.Equal(t, "tool timeout", summary.Cases[0].Error)

	tests := []struct {
		name      string
		mutate    func(*FakeOutput)
		wantError string
	}{
		{
			name: "failed tool cannot be terminal output",
			mutate: func(output *FakeOutput) {
				output.Trace = output.Trace[:2]
				output.Usage.ModelCalls = 0
				output.Usage.LatencyMS = 4
				output.Trace[1].Usage = &output.Usage
			},
			wantError: "must end with a final_response",
		},
		{
			name: "failed tool remains part of trajectory",
			mutate: func(output *FakeOutput) {
				output.Tools = nil
			},
			wantError: "trace has 1 tool calls",
		},
		{
			name: "failed tool arguments mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[1].Input["id"] = "different"
			},
			wantError: "does not match output arguments",
		},
		{
			name: "failure followed by success",
			mutate: func(output *FakeOutput) {
				output.Trace[2].Status = "completed"
				output.Trace[2].Message = ""
			},
			wantError: "not immediately followed by a failed terminal response",
		},
		{
			name: "error message mismatch",
			mutate: func(output *FakeOutput) {
				output.Trace[2].Message = "different failure"
			},
			wantError: "does not match output error",
		},
		{
			name: "route output without evidence",
			mutate: func(output *FakeOutput) {
				output.Trace = output.Trace[1:]
			},
			wantError: "has no trace evidence",
		},
		{
			name: "retrieval output without evidence",
			mutate: func(output *FakeOutput) {
				output.RetrievedDocuments = 1
				output.RetrievedFacts = []string{"unsupported"}
			},
			wantError: "retrieval output has no trace evidence",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := completeFailedStrictTraceOutput()
			test.mutate(&output)
			_, err := evaluate(t, output)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func completeFailedStrictTraceOutput() FakeOutput {
	usage := Usage{ModelCalls: 1, ToolCalls: 1, InputTokens: 7, OutputTokens: 0, CostUSD: 0.002, LatencyMS: 5}
	return FakeOutput{
		Error: "tool timeout",
		Route: "specialist",
		Tools: []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{"id": "A100"}}},
		Trace: []TraceStep{
			{StepID: "route", Kind: "route", Name: "specialist", Status: "completed", ElapsedMS: traceMilliseconds(1)},
			{
				StepID: "tool", Kind: "tool", Name: "lookup", Status: "timeout", ElapsedMS: traceMilliseconds(4),
				Input: map[string]any{"id": "A100"},
			},
			{
				StepID: "final", Kind: "final_response", Status: "failed", ElapsedMS: traceMilliseconds(5),
				Message: "tool timeout", Usage: &usage,
			},
		},
		Usage: usage,
	}
}

func traceMilliseconds(value int64) *int64 {
	return &value
}
