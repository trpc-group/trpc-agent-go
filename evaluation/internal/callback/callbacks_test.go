//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package callback

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type ctxKey struct{}

func TestRunBeforeInferenceSet_EmptyResultReturnsNil(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("empty", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return &service.BeforeInferenceSetResult{}, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestRunBeforeInferenceSet_KeepsContextFromEarlierCallbackWhenLaterNil(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("first", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			next := context.WithValue(ctx, ctxKey{}, "value")
			return &service.BeforeInferenceSetResult{Context: next}, nil
		},
	})
	callbacks.Register("second", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return nil, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Context)
	assert.Equal(t, "value", result.Context.Value(ctxKey{}))
}

func TestRunBeforeInferenceSet_DropsEarlierContextWhenLastResultEmpty(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("first", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			next := context.WithValue(ctx, ctxKey{}, "value")
			return &service.BeforeInferenceSetResult{Context: next}, nil
		},
	})
	callbacks.Register("second", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return &service.BeforeInferenceSetResult{}, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestWrapCallbackError(t *testing.T) {
	assert.Nil(t, wrapCallbackError("point", 0, "name", nil))

	sentinel := errors.New("boom")
	err := wrapCallbackError("BeforeInferenceSet", 2, "component", sentinel)
	assert.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "BeforeInferenceSet callback[2] (component)")
}

func TestRunBeforeInferenceSet_NoRegisteredCallbacksReturnsNil(t *testing.T) {
	callbacks := &service.Callbacks{}
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}

	result, err := RunBeforeInferenceSet(context.Background(), callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestRunBeforeInferenceSet_NilCallbacksReturnsNil(t *testing.T) {
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}

	result, err := RunBeforeInferenceSet(context.Background(), nil, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestRunCallbackPoints_ContextPropagationAndEmptyLastResult(t *testing.T) {
	base := context.Background()
	want := "value"

	t.Run("AfterInferenceSet", func(t *testing.T) {
		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterInferenceSetArgs{Request: req, Results: []*service.InferenceResult{}, Error: nil}

		result, err := RunAfterInferenceSet(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterInferenceSetResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.AfterInferenceSetResult{}, nil
			},
		})

		result, err = RunAfterInferenceSet(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("BeforeInferenceCase", func(t *testing.T) {
		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeInferenceCaseArgs{Request: req, EvalCaseID: "case", SessionID: "session"}

		result, err := RunBeforeInferenceCase(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeInferenceCaseResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.BeforeInferenceCaseResult{}, nil
			},
		})

		result, err = RunBeforeInferenceCase(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("AfterInferenceCase", func(t *testing.T) {
		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		args := &service.AfterInferenceCaseArgs{Request: req, Result: infResult, Error: nil}

		result, err := RunAfterInferenceCase(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterInferenceCaseResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.AfterInferenceCaseResult{}, nil
			},
		})

		result, err = RunAfterInferenceCase(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("BeforeEvaluateSet", func(t *testing.T) {
		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeEvaluateSetArgs{Request: req}

		result, err := RunBeforeEvaluateSet(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeEvaluateSetResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.BeforeEvaluateSetResult{}, nil
			},
		})

		result, err = RunBeforeEvaluateSet(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("AfterEvaluateSet", func(t *testing.T) {
		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterEvaluateSetArgs{Request: req, Result: &service.EvalSetRunResult{AppName: "app", EvalSetID: "set"}, Error: nil}

		result, err := RunAfterEvaluateSet(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterEvaluateSetResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.AfterEvaluateSetResult{}, nil
			},
		})

		result, err = RunAfterEvaluateSet(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("BeforeEvaluateCase", func(t *testing.T) {
		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeEvaluateCaseArgs{Request: req, EvalCaseID: "case"}

		result, err := RunBeforeEvaluateCase(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeEvaluateCaseResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.BeforeEvaluateCaseResult{}, nil
			},
		})

		result, err = RunBeforeEvaluateCase(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("AfterEvaluateCase", func(t *testing.T) {
		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		evalCaseResult := &evalresult.EvalCaseResult{EvalSetID: "set", EvalID: "case"}
		args := &service.AfterEvaluateCaseArgs{
			Request:         req,
			InferenceResult: infResult,
			Result:          evalCaseResult,
			Error:           nil,
		}

		result, err := RunAfterEvaluateCase(base, nil, args)
		assert.NoError(t, err)
		assert.Nil(t, result)

		callbacks := &service.Callbacks{}
		callbacks.Register("first", &service.Callback{
			AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterEvaluateCaseResult{Context: next}, nil
			},
		})
		callbacks.Register("second", &service.Callback{
			AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return nil, nil
			},
		})
		callbacks.Register("third", &service.Callback{
			AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
				assert.Equal(t, want, ctx.Value(ctxKey{}))
				return &service.AfterEvaluateCaseResult{}, nil
			},
		})

		result, err = RunAfterEvaluateCase(base, callbacks, args)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})
}

func TestRunBeforeInferenceCase_WrapsCallbackError(t *testing.T) {
	callbacks := &service.Callbacks{}
	sentinel := errors.New("callback failed")
	callbacks.Register("bad", &service.Callback{
		BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
			return nil, sentinel
		},
	})

	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	_, err := RunBeforeInferenceCase(context.Background(), callbacks, &service.BeforeInferenceCaseArgs{
		Request:    req,
		EvalCaseID: "case",
		SessionID:  "session",
	})
	assert.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "execute BeforeInferenceCase callbacks")
	assert.Contains(t, err.Error(), "BeforeInferenceCase callback[0] (bad)")
}

func TestRunBeforeEvaluateCase_ConvertsPanicToError(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("panic", &service.Callback{
		BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
			panic("boom")
		},
	})

	req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
	_, err := RunBeforeEvaluateCase(context.Background(), callbacks, &service.BeforeEvaluateCaseArgs{
		Request:    req,
		EvalCaseID: "case",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execute BeforeEvaluateCase callbacks")
	assert.Contains(t, err.Error(), "callback panic")
	assert.Contains(t, err.Error(), "BeforeEvaluateCase callback[0] (panic)")
}

func TestRunCallbackPoints_ErrorPaths(t *testing.T) {
	base := context.Background()
	sentinel := errors.New("callback failed")

	t.Run("BeforeInferenceSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
				return nil, sentinel
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		_, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute BeforeInferenceSet callbacks")
		assert.Contains(t, err.Error(), "BeforeInferenceSet callback[0] (bad)")
	})

	t.Run("AfterInferenceSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
				return nil, sentinel
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterInferenceSetArgs{Request: req, Results: []*service.InferenceResult{}, Error: nil}
		_, err := RunAfterInferenceSet(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute AfterInferenceSet callbacks")
		assert.Contains(t, err.Error(), "AfterInferenceSet callback[0] (bad)")
	})

	t.Run("BeforeInferenceCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
				return nil, sentinel
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeInferenceCaseArgs{Request: req, EvalCaseID: "case", SessionID: "session"}
		_, err := RunBeforeInferenceCase(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute BeforeInferenceCase callbacks")
		assert.Contains(t, err.Error(), "BeforeInferenceCase callback[0] (bad)")
	})

	t.Run("AfterInferenceCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
				return nil, sentinel
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		args := &service.AfterInferenceCaseArgs{Request: req, Result: infResult, Error: nil}
		_, err := RunAfterInferenceCase(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute AfterInferenceCase callbacks")
		assert.Contains(t, err.Error(), "AfterInferenceCase callback[0] (bad)")
	})

	t.Run("BeforeEvaluateSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
				return nil, sentinel
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		_, err := RunBeforeEvaluateSet(base, callbacks, &service.BeforeEvaluateSetArgs{Request: req})
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute BeforeEvaluateSet callbacks")
		assert.Contains(t, err.Error(), "BeforeEvaluateSet callback[0] (bad)")
	})

	t.Run("AfterEvaluateSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
				return nil, sentinel
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterEvaluateSetArgs{Request: req, Result: &service.EvalSetRunResult{AppName: "app", EvalSetID: "set"}, Error: nil}
		_, err := RunAfterEvaluateSet(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute AfterEvaluateSet callbacks")
		assert.Contains(t, err.Error(), "AfterEvaluateSet callback[0] (bad)")
	})

	t.Run("BeforeEvaluateCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
				return nil, sentinel
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeEvaluateCaseArgs{Request: req, EvalCaseID: "case"}
		_, err := RunBeforeEvaluateCase(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute BeforeEvaluateCase callbacks")
		assert.Contains(t, err.Error(), "BeforeEvaluateCase callback[0] (bad)")
	})

	t.Run("AfterEvaluateCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("bad", &service.Callback{
			AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
				return nil, sentinel
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		evalCaseResult := &evalresult.EvalCaseResult{EvalSetID: "set", EvalID: "case"}
		args := &service.AfterEvaluateCaseArgs{
			Request:         req,
			InferenceResult: infResult,
			Result:          evalCaseResult,
			Error:           nil,
		}
		_, err := RunAfterEvaluateCase(base, callbacks, args)
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "execute AfterEvaluateCase callbacks")
		assert.Contains(t, err.Error(), "AfterEvaluateCase callback[0] (bad)")
	})
}

func TestRunCallbackPoints_ReturnsNonEmptyResult(t *testing.T) {
	base := context.Background()
	want := "value"

	t.Run("AfterInferenceSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterInferenceSetResult{Context: next}, nil
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterInferenceSetArgs{Request: req, Results: []*service.InferenceResult{}, Error: nil}
		result, err := RunAfterInferenceSet(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("BeforeInferenceCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeInferenceCaseResult{Context: next}, nil
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeInferenceCaseArgs{Request: req, EvalCaseID: "case", SessionID: "session"}
		result, err := RunBeforeInferenceCase(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("AfterInferenceCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterInferenceCaseResult{Context: next}, nil
			},
		})

		req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		args := &service.AfterInferenceCaseArgs{Request: req, Result: infResult, Error: nil}
		result, err := RunAfterInferenceCase(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("BeforeEvaluateSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeEvaluateSetResult{Context: next}, nil
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		result, err := RunBeforeEvaluateSet(base, callbacks, &service.BeforeEvaluateSetArgs{Request: req})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("AfterEvaluateSet", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterEvaluateSetResult{Context: next}, nil
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.AfterEvaluateSetArgs{Request: req, Result: &service.EvalSetRunResult{AppName: "app", EvalSetID: "set"}, Error: nil}
		result, err := RunAfterEvaluateSet(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("BeforeEvaluateCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.BeforeEvaluateCaseResult{Context: next}, nil
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		args := &service.BeforeEvaluateCaseArgs{Request: req, EvalCaseID: "case"}
		result, err := RunBeforeEvaluateCase(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})

	t.Run("AfterEvaluateCase", func(t *testing.T) {
		callbacks := &service.Callbacks{}
		callbacks.Register("only", &service.Callback{
			AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
				next := context.WithValue(ctx, ctxKey{}, want)
				return &service.AfterEvaluateCaseResult{Context: next}, nil
			},
		})

		req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set"}
		infResult := &service.InferenceResult{AppName: "app", EvalSetID: "set", EvalCaseID: "case"}
		evalCaseResult := &evalresult.EvalCaseResult{EvalSetID: "set", EvalID: "case"}
		args := &service.AfterEvaluateCaseArgs{
			Request:         req,
			InferenceResult: infResult,
			Result:          evalCaseResult,
			Error:           nil,
		}
		result, err := RunAfterEvaluateCase(base, callbacks, args)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Context)
		assert.Equal(t, want, result.Context.Value(ctxKey{}))
	})
}
