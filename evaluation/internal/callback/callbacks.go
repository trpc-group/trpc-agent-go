//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package callback

import (
	"context"
	"fmt"
	"runtime/debug"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func wrapCallbackError(point string, idx int, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s callback[%d] (%s): %w", point, idx, name, err)
}

func callCallbackWithRecovery[Args any, Result any, CallbackFn ~func(context.Context, *Args) (*Result, error)](
	ctx context.Context,
	point string,
	idx int,
	name string,
	callback CallbackFn,
	args *Args,
) (*Result, error) {
	var result *Result
	var err error
	callCallbackWithRecoveryInto(ctx, point, idx, name, callback, args, &result, &err)
	return result, err
}

func callCallbackWithRecoveryInto[Args any, Result any, CallbackFn ~func(context.Context, *Args) (*Result, error)](
	ctx context.Context,
	point string,
	idx int,
	name string,
	callback CallbackFn,
	args *Args,
	resultp **Result,
	errp *error,
) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		stack := debug.Stack()
		log.ErrorfContext(ctx, "%s (callback: %s, idx: %d): %v\n%s", point, name, idx, recovered, string(stack))
		*errp = fmt.Errorf("callback panic: %v", recovered)
	}()
	result, err := callback(ctx, args)
	*resultp = result
	*errp = err
}

func runCallbacks[Args any, Result any, CallbackFn ~func(context.Context, *Args) (*Result, error)](
	ctx *context.Context,
	callbacks []service.NamedCallback[CallbackFn],
	args *Args,
	point string,
	getContext func(*Result) context.Context,
) (*Result, error) {
	if len(callbacks) == 0 {
		return nil, nil
	}
	var lastResult *Result
	for idx, named := range callbacks {
		result, err := callCallbackWithRecovery(*ctx, point, idx, named.Name, named.Callback, args)
		if err != nil {
			return nil, wrapCallbackError(point, idx, named.Name, err)
		}
		if result != nil {
			lastResult = result
		}
		if getContext != nil {
			if next := getContext(result); next != nil {
				*ctx = next
			}
		}
	}
	return lastResult, nil
}

// RunBeforeInferenceSet runs all before inference set callbacks in order.
func RunBeforeInferenceSet(ctx context.Context, callbacks *service.Callbacks, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.BeforeInferenceSet, args, "BeforeInferenceSet",
		func(result *service.BeforeInferenceSetResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute BeforeInferenceSet callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunAfterInferenceSet runs all after inference set callbacks in order.
func RunAfterInferenceSet(ctx context.Context, callbacks *service.Callbacks, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.AfterInferenceSet, args, "AfterInferenceSet",
		func(result *service.AfterInferenceSetResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute AfterInferenceSet callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunBeforeInferenceCase runs all before inference case callbacks in order.
func RunBeforeInferenceCase(ctx context.Context, callbacks *service.Callbacks, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.BeforeInferenceCase, args, "BeforeInferenceCase",
		func(result *service.BeforeInferenceCaseResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute BeforeInferenceCase callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunAfterInferenceCase runs all after inference case callbacks in order.
func RunAfterInferenceCase(ctx context.Context, callbacks *service.Callbacks, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.AfterInferenceCase, args, "AfterInferenceCase",
		func(result *service.AfterInferenceCaseResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute AfterInferenceCase callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunBeforeEvaluateSet runs all before evaluate set callbacks in order.
func RunBeforeEvaluateSet(ctx context.Context, callbacks *service.Callbacks, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.BeforeEvaluateSet, args, "BeforeEvaluateSet",
		func(result *service.BeforeEvaluateSetResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute BeforeEvaluateSet callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunAfterEvaluateSet runs all after evaluate set callbacks in order.
func RunAfterEvaluateSet(ctx context.Context, callbacks *service.Callbacks, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.AfterEvaluateSet, args, "AfterEvaluateSet",
		func(result *service.AfterEvaluateSetResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute AfterEvaluateSet callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunBeforeEvaluateCase runs all before evaluate case callbacks in order.
func RunBeforeEvaluateCase(ctx context.Context, callbacks *service.Callbacks, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.BeforeEvaluateCase, args, "BeforeEvaluateCase",
		func(result *service.BeforeEvaluateCaseResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute BeforeEvaluateCase callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}

// RunAfterEvaluateCase runs all after evaluate case callbacks in order.
func RunAfterEvaluateCase(ctx context.Context, callbacks *service.Callbacks, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
	if callbacks == nil {
		return nil, nil
	}
	result, err := runCallbacks(&ctx, callbacks.AfterEvaluateCase, args, "AfterEvaluateCase",
		func(result *service.AfterEvaluateCaseResult) context.Context {
			if result == nil {
				return nil
			}
			return result.Context
		},
	)
	if err != nil {
		return nil, fmt.Errorf("execute AfterEvaluateCase callbacks: %w", err)
	}
	if result != nil && result.Context == nil {
		return nil, nil
	}
	return result, nil
}
