//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakemodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestLoadFixtureRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.json")
	require.NoError(t, os.WriteFile(valid, []byte(`{"candidate":{"default":"x"}}`), 0o644))
	if _, err := LoadFixture(valid); err != nil {
		t.Fatalf("valid fixture should load, got %v", err)
	}

	// A misspelled key ("inputContain" instead of "inputContains") must error rather than being
	// silently dropped into a fixture that behaves unexpectedly.
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`{"candidate":{"answers":[{"inputContain":"x","answer":"y"}]}}`), 0o644))
	if _, err := LoadFixture(bad); err == nil {
		t.Fatalf("expected error for unknown field, got nil")
	}
}

func TestLoadFixtureRejectsTrailingData(t *testing.T) {
	dir := t.TempDir()

	// A second JSON object concatenated after the first must not be silently ignored.
	dup := filepath.Join(dir, "dup.json")
	require.NoError(t, os.WriteFile(dup, []byte(`{"candidate":{"default":"x"}}{"candidate":{"default":"y"}}`), 0o644))
	if _, err := LoadFixture(dup); err == nil {
		t.Fatalf("expected error for trailing JSON value, got nil")
	}

	// Arbitrary trailing garbage after a valid value must also error.
	garbage := filepath.Join(dir, "garbage.json")
	require.NoError(t, os.WriteFile(garbage, []byte(`{"candidate":{"default":"x"}} not-json`), 0o644))
	if _, err := LoadFixture(garbage); err == nil {
		t.Fatalf("expected error for trailing garbage, got nil")
	}
}

func generate(t *testing.T, m *ScriptedModel, instruction, input string) string {
	t.Helper()
	req := &model.Request{Messages: []model.Message{
		{Role: model.RoleSystem, Content: instruction},
		{Role: model.RoleUser, Content: input},
	}}
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	var last string
	for resp := range ch {
		require.NotNil(t, resp)
		require.Len(t, resp.Choices, 1)
		last = resp.Choices[0].Message.Content
	}
	return last
}

func TestScriptedModelMatchesRuleAndIsDeterministic(t *testing.T) {
	m := NewScriptedModel("candidate", CandidateScript{
		Answers: []AnswerRule{
			{InstructionContains: "STRICT", InputContains: "refund", Answer: "category: billing"},
			{InputContains: "refund", Answer: "billing maybe?"},
		},
		Default: "unknown",
	})
	// The strict instruction produces the exact-format answer.
	got := generate(t, m, "You classify. STRICT format required.", "I want a refund")
	assert.Equal(t, "category: billing", got)
	// Deterministic: same inputs, same output across repeated calls.
	assert.Equal(t, got, generate(t, m, "You classify. STRICT format required.", "I want a refund"))
	// Weaker instruction falls to the second rule (different, worse answer).
	assert.Equal(t, "billing maybe?", generate(t, m, "You classify.", "I want a refund"))
	// No rule matches -> default.
	assert.Equal(t, "unknown", generate(t, m, "You classify.", "hello"))
}

func TestDeterministicOptimizerTransitions(t *testing.T) {
	opt := DeterministicOptimizer{Transitions: []Transition{
		{FromContains: "vague", ToInstruction: "STRICT: reply with 'category: <x>'", Reason: "make format explicit"},
	}}
	text := "a vague instruction"
	surface := &astructure.Surface{SurfaceID: "candidate#instruction", NodeID: "candidate", Type: astructure.SurfaceTypeInstruction, Value: astructure.SurfaceValue{Text: &text}}
	res, err := opt.Optimize(context.Background(), &optimizer.Request{
		Surface:  surface,
		Gradient: &promptiter.AggregatedSurfaceGradient{SurfaceID: "candidate#instruction"},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Patch)
	require.NotNil(t, res.Patch.Value.Text)
	assert.Equal(t, "STRICT: reply with 'category: <x>'", *res.Patch.Value.Text)
	assert.Equal(t, "make format explicit", res.Patch.Reason)

	// No matching transition -> proposes current text with a non-empty reason.
	stable := "already strict"
	surface2 := &astructure.Surface{SurfaceID: "candidate#instruction", NodeID: "candidate", Type: astructure.SurfaceTypeInstruction, Value: astructure.SurfaceValue{Text: &stable}}
	res2, err := opt.Optimize(context.Background(), &optimizer.Request{
		Surface:  surface2,
		Gradient: &promptiter.AggregatedSurfaceGradient{SurfaceID: "candidate#instruction"},
	})
	require.NoError(t, err)
	require.NotNil(t, res2.Patch.Value.Text)
	assert.Equal(t, "already strict", *res2.Patch.Value.Text)
	assert.NotEmpty(t, res2.Patch.Reason)
}

func TestDeterministicBackwarderAndAggregator(t *testing.T) {
	bw := DeterministicBackwarder{TargetSurfaceID: "candidate#instruction"}
	res, err := bw.Backward(context.Background(), &backwarder.Request{
		EvalSetID:  "train",
		EvalCaseID: "c1",
		StepID:     "s1",
		Surfaces: []astructure.Surface{
			{SurfaceID: "candidate#instruction", NodeID: "candidate", Type: astructure.SurfaceTypeInstruction},
			{SurfaceID: "other#instruction", NodeID: "other", Type: astructure.SurfaceTypeInstruction},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Gradients, 1)
	assert.Equal(t, "candidate#instruction", res.Gradients[0].SurfaceID)
	assert.Equal(t, promptiter.LossSeverityP1, res.Gradients[0].Severity)

	agg := DeterministicAggregator{}
	aggRes, err := agg.Aggregate(context.Background(), &aggregator.Request{
		SurfaceID: "candidate#instruction",
		NodeID:    "candidate",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: res.Gradients,
	})
	require.NoError(t, err)
	require.NotNil(t, aggRes.Gradient)
	assert.Equal(t, "candidate#instruction", aggRes.Gradient.SurfaceID)
	require.Len(t, aggRes.Gradient.Gradients, 1)
}
