//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

func TestInferExpectedsForEvalValidationErrors(t *testing.T) {
	ctx := context.Background()
	svc := &local{expectedRunner: &fakeRunner{}}
	inferenceResult := &service.InferenceResult{SessionID: "session"}
	evalCase := &evalset.EvalCase{EvalID: "case", SessionInput: &evalset.SessionInput{AppName: "app", UserID: "demo-user", State: map[string]any{}}}
	tests := []struct {
		name    string
		actuals []*evalset.Invocation
		want    string
	}{
		{name: "empty_actuals", actuals: []*evalset.Invocation{}, want: "actual invocations are empty"},
		{name: "nil_actual", actuals: []*evalset.Invocation{nil}, want: "actual invocation is nil at index 0"},
		{name: "nil_user_content", actuals: []*evalset.Invocation{{InvocationID: "inv-1"}}, want: "actual invocation user content is nil at index 0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.inferExpectedsForEval(ctx, inferenceResult, evalCase, tc.actuals)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestInferExpectedsForEvalPropagatesRunnerError(t *testing.T) {
	ctx := context.Background()
	svc := &local{expectedRunner: &fakeRunner{err: errors.New("run failed")}}
	inferenceResult := &service.InferenceResult{SessionID: "session"}
	evalCase := &evalset.EvalCase{EvalID: "case", SessionInput: &evalset.SessionInput{AppName: "app", UserID: "demo-user", State: map[string]any{}}}
	_, err := svc.inferExpectedsForEval(ctx, inferenceResult, evalCase, []*evalset.Invocation{makeInvocation("inv-1", "prompt")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run expected runner")
	assert.Contains(t, err.Error(), "run failed")
}
