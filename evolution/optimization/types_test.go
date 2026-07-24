//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

func TestValidateRequestRejectsDatasetLeakageAndInvalidPromotionData(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Request)
		message string
	}{
		{name: "missing seed", mutate: func(req *Request) { req.Seed = nil }, message: "seed"},
		{name: "invalid seed", mutate: func(req *Request) { req.Seed.Description = "" }, message: "description"},
		{name: "missing dataset id", mutate: func(req *Request) { req.Dataset.ID = "" }, message: "dataset id"},
		{name: "missing version", mutate: func(req *Request) { req.Dataset.Version = "" }, message: "dataset version"},
		{name: "missing feedback", mutate: func(req *Request) { req.Dataset.Feedback = nil }, message: "feedback split"},
		{name: "missing validation", mutate: func(req *Request) { req.Dataset.Validation = nil }, message: "validation split"},
		{name: "empty case id", mutate: func(req *Request) { req.Dataset.Feedback[0].ID = "" }, message: "empty case id"},
		{name: "cross split duplicate", mutate: func(req *Request) {
			req.Dataset.Validation[0].ID = req.Dataset.Feedback[0].ID
		}, message: "appears in both"},
		{name: "promotion feedback minimum", mutate: func(req *Request) {
			req.Submit = true
			req.Dataset.Feedback = req.Dataset.Feedback[:9]
		}, message: "feedback cases"},
		{name: "promotion validation minimum", mutate: func(req *Request) {
			req.Submit = true
			req.Dataset.Validation = req.Dataset.Validation[:9]
		}, message: "validation cases"},
		{name: "promotion holdout minimum", mutate: func(req *Request) {
			req.Submit = true
			req.Dataset.Holdout = req.Dataset.Holdout[:9]
		}, message: "holdout cases"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := Request{
				Seed: testSeedSpec(),
				Dataset: Dataset{
					ID:         "dataset",
					Version:    "v1",
					Feedback:   makeCases("feedback", 10),
					Validation: makeCases("validation", 10),
					Holdout:    makeCases("holdout", 10),
				},
			}
			test.mutate(&req)
			require.ErrorContains(t, validateRequest(req), test.message)
		})
	}
}

func TestValidateSpecRejectsMalformedOrOversizedCandidates(t *testing.T) {
	tests := []struct {
		name    string
		spec    *evolution.SkillSpec
		message string
	}{
		{name: "nil", spec: nil, message: "nil spec"},
		{name: "name", spec: &evolution.SkillSpec{Description: "d", WhenToUse: "w", Steps: []string{"s"}}, message: "name"},
		{name: "description", spec: &evolution.SkillSpec{Name: "n", WhenToUse: "w", Steps: []string{"s"}}, message: "description"},
		{name: "when", spec: &evolution.SkillSpec{Name: "n", Description: "d", Steps: []string{"s"}}, message: "when_to_use"},
		{name: "steps", spec: &evolution.SkillSpec{Name: "n", Description: "d", WhenToUse: "w"}, message: "step"},
		{name: "empty step", spec: &evolution.SkillSpec{Name: "n", Description: "d", WhenToUse: "w", Steps: []string{" "}}, message: "step 0"},
		{name: "oversized", spec: &evolution.SkillSpec{Name: "n", Description: strings.Repeat("x", maxSpecChars), WhenToUse: "w", Steps: []string{"s"}}, message: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, validateSpec(test.spec), test.message)
		})
	}
	require.NoError(t, validateSpec(testSeedSpec()))
	assert.Equal(t, 7, specSize(&evolution.SkillSpec{
		Name:        "界",
		Description: "说明",
		WhenToUse:   "用",
		Steps:       []string{"步骤"},
		Pitfalls:    []string{"坑"},
	}))
}

func TestNewEvaluationBatchValidatesEvaluatorContract(t *testing.T) {
	cases := []Case{{ID: "one"}, {ID: "two"}}
	tests := []struct {
		name        string
		evaluations []Evaluation
		message     string
	}{
		{name: "wrong length", evaluations: []Evaluation{{CaseID: "one", Score: 1}}, message: "expected 2"},
		{name: "unexpected id", evaluations: []Evaluation{{CaseID: "one", Score: 1}, {CaseID: "other", Score: 1}}, message: "unexpected"},
		{name: "duplicate id", evaluations: []Evaluation{{CaseID: "one", Score: 1}, {CaseID: "one", Score: 1}}, message: "duplicate"},
		{name: "nan score", evaluations: []Evaluation{{CaseID: "one", Score: math.NaN()}, {CaseID: "two", Score: 1}}, message: "within [0, 1]"},
		{name: "infinite score", evaluations: []Evaluation{{CaseID: "one", Score: math.Inf(1)}, {CaseID: "two", Score: 1}}, message: "within [0, 1]"},
		{name: "negative score", evaluations: []Evaluation{{CaseID: "one", Score: -1}, {CaseID: "two", Score: 1}}, message: "within [0, 1]"},
		{name: "large score", evaluations: []Evaluation{{CaseID: "one", Score: 2}, {CaseID: "two", Score: 1}}, message: "within [0, 1]"},
		{name: "infinite objective", evaluations: []Evaluation{{CaseID: "one", Score: 1, Objectives: map[string]float64{"latency": math.Inf(1)}}, {CaseID: "two", Score: 1}}, message: "objective"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newEvaluationBatch(cases, test.evaluations)
			require.ErrorContains(t, err, test.message)
		})
	}

	batch, err := newEvaluationBatch(cases, []Evaluation{
		{CaseID: "two", Score: 0.5},
		{CaseID: "one", Score: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, []string{batch.ordered[0].CaseID, batch.ordered[1].CaseID})
}

func TestInternalComponentAndCloneHelpers(t *testing.T) {
	assert.Equal(t, "description", componentDescription.String())
	assert.Equal(t, "when_to_use", componentWhenToUse.String())
	assert.Equal(t, "steps", componentSteps.String())
	assert.Equal(t, "pitfalls", componentPitfalls.String())
	assert.Equal(t, "unknown", component(99).String())
	assert.Nil(t, cloneSpec(nil))
	assert.Nil(t, cloneCases(nil))
	assert.Nil(t, cloneStringMap(nil))
	assert.Nil(t, cloneFloatMap(nil))
}
