//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestAdditionalCallbacksLifecycle exercises the public options through both
// real service stages, including defaults owned by an injected service.
func TestAdditionalCallbacksLifecycle(t *testing.T) {
	for _, owner := range []string{"evaluator", "service"} {
		t.Run(owner, func(t *testing.T) {
			ctx := context.Background()
			sets := evalsetinmemory.New()
			_, err := sets.Create(ctx, "app", "set")
			require.NoError(t, err)
			require.NoError(t, sets.AddCase(ctx, "app", "set", &evalset.EvalCase{
				EvalID: "case", EvalMode: evalset.EvalModeTrace,
				SessionInput: &evalset.SessionInput{AppName: "app", UserID: "user"},
				Conversation: []*evalset.Invocation{{
					UserContent:   &model.Message{Role: model.RoleUser, Content: "hello"},
					FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "hello"},
				}},
			}))
			var visited []string
			callbacks := func(name string) *service.Callbacks {
				return service.NewCallbacks().Register(name, &service.Callback{
					BeforeInferenceSet: func(context.Context, *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
						visited = append(visited, name+":inference")
						return nil, nil
					},
					BeforeEvaluateSet: func(context.Context, *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
						visited = append(visited, name+":evaluation")
						return nil, nil
					},
				})
			}
			base, constructor, call, replacement := callbacks("base"), callbacks("constructor"), callbacks("call"), callbacks("replacement")
			saved := evalresultinmemory.New()
			options := []Option{
				WithEvalSetManager(sets), WithEvalResultManager(saved), WithEvalCaseResultAggregator(passEvalCaseResultAggregator{}),
				WithAdditionalCallbacks(nil), WithAdditionalCallbacks(constructor),
			}
			if owner == "service" {
				svc, err := local.New(stubRunner{}, service.WithCallbacks(base))
				require.NoError(t, err)
				options = append(options, WithEvaluationService(svc))
			} else {
				options = append(options, WithCallbacks(base))
			}
			evaluator, err := New("app", stubRunner{}, options...)
			require.NoError(t, err)
			defer evaluator.Close()
			for _, run := range []struct {
				options []Option
				want    []string
			}{
				{[]Option{WithAdditionalCallbacks(nil), WithAdditionalCallbacks(call)}, []string{"base:inference", "constructor:inference", "call:inference", "base:evaluation", "constructor:evaluation", "call:evaluation"}},
				{[]Option{WithCallbacks(replacement)}, []string{"replacement:inference", "constructor:inference", "replacement:evaluation", "constructor:evaluation"}},
				{nil, []string{"base:inference", "constructor:inference", "base:evaluation", "constructor:evaluation"}},
			} {
				visited = nil
				result, err := evaluator.Evaluate(ctx, "set", run.options...)
				require.NoError(t, err)
				require.Len(t, result.EvalCases, 1)
				require.Equal(t, run.want, visited, "callbacks must compose in order without leaking across calls")
			}
			before, err := saved.List(ctx, "app")
			require.NoError(t, err)
			for _, stage := range []string{"inference", "evaluation"} {
				sentinel := errors.New(stage + " callback failed")
				fail := &service.Callback{}
				if stage == "inference" {
					fail.BeforeInferenceSet = func(context.Context, *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
						return nil, sentinel
					}
				} else {
					fail.BeforeEvaluateSet = func(context.Context, *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
						return nil, sentinel
					}
				}
				result, err := evaluator.Evaluate(ctx, "set", WithAdditionalCallbacks(service.NewCallbacks().Register("fail", fail)))
				require.ErrorIs(t, err, sentinel)
				require.Nil(t, result)
				after, err := saved.List(ctx, "app")
				require.NoError(t, err)
				require.ElementsMatch(t, before, after, "failed callbacks must prevent result persistence")
			}
		})
	}
}
