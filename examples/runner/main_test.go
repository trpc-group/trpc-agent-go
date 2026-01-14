//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/go-openapi/testify/v2/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	localevalresult "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	localevalset "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	localmetric "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestMain(m *testing.M) {
	if !flag.Parsed() {
		flag.Parse()
	}
	*modelName = os.Getenv("MODEL_NAME")
	os.Exit(m.Run())
}

func TestTool(t *testing.T) {
	tests := []struct {
		name      string
		evalSetID string
	}{
		{
			name:      "calculator",
			evalSetID: "calculator_tool",
		},
		{
			name:      "currenttime",
			evalSetID: "currenttime_tool",
		},
		{
			name:      "compound_interest",
			evalSetID: "compound_interest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &multiTurnChat{
				modelName: *modelName,
				streaming: *streaming,
				variant:   *variant,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			err := chat.setup(ctx)
			assert.NoError(t, err)
			defer chat.runner.Close()
			evaluationDir := "evaluation"
			localEvalSetManager := localevalset.New(evalset.WithBaseDir(evaluationDir))
			localMetricManager := localmetric.New(metric.WithBaseDir(evaluationDir))
			localEvalResultManager := localevalresult.New(evalresult.WithBaseDir(evaluationDir))
			evaluator, err := evaluation.New(
				appName,
				chat.runner,
				evaluation.WithEvalSetManager(localEvalSetManager),
				evaluation.WithMetricManager(localMetricManager),
				evaluation.WithEvalResultManager(localEvalResultManager),
			)
			assert.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, evaluator.Close())
			})
			result, err := evaluator.Evaluate(ctx, tt.evalSetID)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			resultData, err := json.MarshalIndent(result, "", "  ")
			assert.NoError(t, err)
			assert.Equal(t, status.EvalStatusPassed, result.OverallStatus, string(resultData))
		})
	}
}

func TestRubric_CompoundInterest_FinalAnswerPresent(t *testing.T) {
	chat := &multiTurnChat{
		modelName: *modelName,
		streaming: *streaming,
		variant:   *variant,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err := chat.setup(ctx)
	assert.NoError(t, err)
	defer chat.runner.Close()
	evaluationDir := "evaluation"
	localEvalSetManager := localevalset.New(evalset.WithBaseDir(evaluationDir))
	localMetricManager := localmetric.New(metric.WithBaseDir(evaluationDir))
	localEvalResultManager := localevalresult.New(evalresult.WithBaseDir(evaluationDir))
	evaluator, err := evaluation.New(
		appName,
		chat.runner,
		evaluation.WithEvalSetManager(localEvalSetManager),
		evaluation.WithMetricManager(localMetricManager),
		evaluation.WithEvalResultManager(localEvalResultManager),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, evaluator.Close())
	})
	result, err := evaluator.Evaluate(ctx, "compound_interest_rubric")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	resultData, err := json.MarshalIndent(result, "", "  ")
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus, string(resultData))
}
