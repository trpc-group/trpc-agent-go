//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestRegressionEvalSetCanonicalConversion(t *testing.T) {
	threshold := 0.9
	canonical := &evalset.EvalSet{
		EvalSetID:   "canonical",
		Name:        "Canonical set",
		Description: "owned by evaluation/evalset",
		EvalCases: []*evalset.EvalCase{{
			EvalID:       "case",
			Conversation: []*evalset.Invocation{{InvocationID: "invocation"}},
		}},
	}
	extensions := map[string]RegressionCaseExtension{
		"case": {
			Critical: true,
			Tags:     []string{"critical"},
			FakeResponses: map[string]FakeOutput{
				"baseline": {},
			},
		},
	}

	regressionSet, err := NewRegressionEvalSet(canonical, &threshold, extensions)
	require.NoError(t, err)
	assert.Equal(t, "canonical", regressionSet.EvalSetID)
	require.Len(t, regressionSet.Cases, 1)
	assert.True(t, regressionSet.Cases[0].Critical)
	assert.Equal(t, "invocation", regressionSet.Cases[0].Conversation[0].InvocationID)

	roundTrip := regressionSet.StandardEvalSet()
	assert.Equal(t, canonical, roundTrip)
	assert.Nil(t, (*RegressionEvalSet)(nil).StandardEvalSet())
}

func TestRegressionEvalSetCanonicalConversionRejectsIncompleteExtensions(t *testing.T) {
	assert.ErrorContains(t, func() error {
		_, err := NewRegressionEvalSet(nil, nil, nil)
		return err
	}(), "nil")

	canonical := &evalset.EvalSet{
		EvalSetID: "canonical",
		EvalCases: []*evalset.EvalCase{{EvalID: "case"}},
	}
	_, err := NewRegressionEvalSet(canonical, nil, nil)
	assert.ErrorContains(t, err, "has no regression extension")

	canonical.EvalCases = []*evalset.EvalCase{nil}
	_, err = NewRegressionEvalSet(canonical, nil, map[string]RegressionCaseExtension{})
	assert.ErrorContains(t, err, "is nil")

	canonical.EvalCases = nil
	_, err = NewRegressionEvalSet(canonical, nil, map[string]RegressionCaseExtension{
		"unknown": {},
	})
	assert.ErrorContains(t, err, "unknown eval case ids")
}
