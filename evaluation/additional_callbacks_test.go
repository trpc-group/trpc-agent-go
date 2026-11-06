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
	for _, owner := range []string{"evaluator", "service", "none"} {
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
				WithAdditionalCallbacks(callbacks("discarded")),
			}
			var defaults []string
			if owner == "service" {
				svc, err := local.New(stubRunner{}, service.WithCallbacks(base))
				require.NoError(t, err)
				options = append(options, WithEvaluationService(svc), WithCallbacks(nil))
				defaults = []string{"base"}
			} else if owner == "evaluator" {
				options = append(options, WithCallbacks(base))
				defaults = []string{"base"}
			} else {
				options = append(options, WithCallbacks(nil))
			}
			options = append(options, WithAdditionalCallbacks(nil), WithAdditionalCallbacks(constructor))
			evaluator, err := New("app", stubRunner{}, options...)
			require.NoError(t, err)
			defer evaluator.Close()
			for _, run := range []struct {
				name    string
				options []Option
				want    []string
			}{
				{"append", []Option{WithAdditionalCallbacks(nil), WithAdditionalCallbacks(call)}, append(defaults, "constructor", "call")},
				{"replace", []Option{WithCallbacks(replacement)}, []string{"replacement"}},
				{"replace then append", []Option{WithCallbacks(replacement), WithAdditionalCallbacks(call)}, []string{"replacement", "call"}},
				{"append then replace", []Option{WithAdditionalCallbacks(call), WithCallbacks(replacement)}, []string{"replacement"}},
				{"replace append replace", []Option{WithCallbacks(replacement), WithAdditionalCallbacks(call), WithCallbacks(base)}, []string{"base"}},
				{"append twice", []Option{WithAdditionalCallbacks(call), WithAdditionalCallbacks(replacement)}, append(defaults, "constructor", "call", "replacement")},
				{"nil replacement", []Option{WithAdditionalCallbacks(call), WithCallbacks(nil)}, defaults},
				{"nil replacement then append", []Option{WithCallbacks(nil), WithAdditionalCallbacks(call)}, append(defaults, "call")},
				{"empty replacement", []Option{WithCallbacks(service.NewCallbacks())}, nil},
				{"empty replacement then append", []Option{WithCallbacks(service.NewCallbacks()), WithAdditionalCallbacks(call)}, []string{"call"}},
				{"nil append", []Option{WithAdditionalCallbacks(nil)}, append(defaults, "constructor")},
				{"next call uses constructor", nil, append(defaults, "constructor")},
			} {
				t.Run(run.name, func(t *testing.T) {
					var want []string
					for _, stage := range []string{"inference", "evaluation"} {
						for _, name := range run.want {
							want = append(want, name+":"+stage)
						}
					}
					visited = nil
					result, err := evaluator.Evaluate(ctx, "set", run.options...)
					require.NoError(t, err)
					require.Len(t, result.EvalCases, 1)
					require.Equal(t, want, visited, "callbacks must compose in order without leaking across calls")
				})
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
