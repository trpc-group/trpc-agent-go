//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package status

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestSummarizePrecedence(t *testing.T) {

	statuses := []evalstatus.EvalStatus{
		evalstatus.EvalStatusNotEvaluated,
		evalstatus.EvalStatusPassed,
		evalstatus.EvalStatusFailed,
	}
	result, err := Summarize(statuses)
	assert.NoError(t, err)
	assert.Equal(t, evalstatus.EvalStatusFailed, result)
}

func TestSummarizeFallback(t *testing.T) {

	statuses := []evalstatus.EvalStatus{
		evalstatus.EvalStatusNotEvaluated,
		evalstatus.EvalStatusPassed,
	}
	result, err := Summarize(statuses)
	assert.NoError(t, err)
	assert.Equal(t, evalstatus.EvalStatusPassed, result)

	result, err = Summarize([]evalstatus.EvalStatus{
		evalstatus.EvalStatusNotEvaluated,
	})
	assert.NoError(t, err)
	assert.Equal(t, evalstatus.EvalStatusNotEvaluated, result)
}

func TestSummarizeUnexpectedStatus(t *testing.T) {

	_, err := Summarize([]evalstatus.EvalStatus{evalstatus.EvalStatusUnknown})
	assert.Error(t, err)
}

func TestSummarizeMetricsStatus(t *testing.T) {

	metrics := []*evalresult.EvalMetricResult{
		nil,
		{EvalStatus: evalstatus.EvalStatusPassed},
		{EvalStatus: evalstatus.EvalStatusFailed},
	}
	result, err := SummarizeMetricsStatus(metrics)
	assert.NoError(t, err)
	assert.Equal(t, evalstatus.EvalStatusFailed, result)
}
