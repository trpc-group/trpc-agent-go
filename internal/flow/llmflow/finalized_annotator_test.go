//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Annotators must see the request as the provider will, not as preprocessing left
// it.
//
// A before-model callback receives a mutable Request and does replace entries in
// Tools — the toolsearch plugin injects its own tools there. An annotator that ran
// during preprocessing would compute from a tool surface that is neither what the
// model is shown nor what the function-call processor later executes.
func TestFinalizedRequestAnnotatorsSeeCallbackChanges(t *testing.T) {
	callbacks := model.NewCallbacks()
	callbacks.RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		if args.Request.Tools == nil {
			args.Request.Tools = map[string]tool.Tool{}
		}
		args.Request.Tools["added_by_callback"] = &mockLongRunnerTool{
			name: "added_by_callback",
		}
		delete(args.Request.Tools, "removed_by_callback")
		return nil, nil
	})

	var seen []string
	f := New(nil, nil, Options{
		ModelCallbacks: callbacks,
		FinalizedRequestAnnotators: []FinalizedRequestAnnotator{
			nil, // a nil entry must be skipped rather than panic
			func(_ context.Context, _ *agent.Invocation, req *model.Request) {
				for name := range req.Tools {
					seen = append(seen, name)
				}
			},
		},
	})

	modelStub := &mockModel{responses: []*model.Response{{Done: true}}}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&minimalAgent{}),
		agent.WithInvocationModel(modelStub),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"kept":                &mockLongRunnerTool{name: "kept"},
			"removed_by_callback": &mockLongRunnerTool{name: "removed_by_callback"},
		},
	}

	_, seq, _, err := f.callLLM(context.Background(), inv, req, inv.Model)
	require.NoError(t, err)
	if seq != nil {
		seq(func(*model.Response) bool { return false })
	}

	require.ElementsMatch(t, []string{"kept", "added_by_callback"}, seen)
}

// A flow with no annotators configured must behave exactly as before.
func TestFinalizedRequestAnnotatorsAbsent(t *testing.T) {
	f := New(nil, nil, Options{})
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	f.annotateFinalizedRequest(context.Background(), nil, req)
	require.Len(t, req.Messages, 1)
}

// Retries are requests too.
//
// contextWithModelRetryCallbacks binds another runBeforeModelCallbacks pass that
// runs after callLLM's single annotateFinalizedRequest call, and that pass can
// change the request — the call-limit finalization retry drops Tools entirely
// before asking again. Annotators that ran only on the first attempt would leave
// the retry carrying an annotation describing a tool surface it no longer has.
func TestFinalizedRequestAnnotatorsRunAfterRetryCallbacks(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		delete(args.Request.Tools, "dropped_by_retry")
		return nil, nil
	})

	var seen [][]string
	f := New(nil, nil, Options{
		ModelCallbacks: callbacks,
		FinalizedRequestAnnotators: []FinalizedRequestAnnotator{
			func(_ context.Context, _ *agent.Invocation, req *model.Request) {
				names := make([]string, 0, len(req.Tools))
				for name := range req.Tools {
					names = append(names, name)
				}
				sort.Strings(names)
				seen = append(seen, names)
			},
		},
	})

	ctx := contextWithModelRetryCallbacks(
		context.Background(),
		f,
		agent.NewInvocation(),
		modelRetryTestBinder{},
	)
	bound, ok := ctx.Value(modelRetryTestCallbacksKey{}).(modelRetryTestCallbacks)
	require.True(t, ok)

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("retry")},
		Tools: map[string]tool.Tool{
			"kept":             &mockLongRunnerTool{name: "kept"},
			"dropped_by_retry": &mockLongRunnerTool{name: "dropped_by_retry"},
		},
	}
	_, customResponse, err := bound.before(ctx, req)
	require.NoError(t, err)
	require.Nil(t, customResponse)

	require.Equal(t, [][]string{{"kept"}}, seen,
		"the retry's annotators must see the surface its callbacks left")
}

// A callback that answers the request itself means nothing reaches the model, so
// there is no finalized request to annotate.
func TestFinalizedRequestAnnotatorsSkipShortCircuitedRetries(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		_ *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		return &model.BeforeModelResult{
			CustomResponse: &model.Response{ID: "short-circuit"},
		}, nil
	})

	var annotated int
	f := New(nil, nil, Options{
		ModelCallbacks: callbacks,
		FinalizedRequestAnnotators: []FinalizedRequestAnnotator{
			func(context.Context, *agent.Invocation, *model.Request) { annotated++ },
		},
	})

	ctx := contextWithModelRetryCallbacks(
		context.Background(),
		f,
		agent.NewInvocation(),
		modelRetryTestBinder{},
	)
	bound, ok := ctx.Value(modelRetryTestCallbacksKey{}).(modelRetryTestCallbacks)
	require.True(t, ok)

	_, customResponse, err := bound.before(
		ctx,
		&model.Request{Messages: []model.Message{model.NewUserMessage("retry")}},
	)
	require.NoError(t, err)
	require.NotNil(t, customResponse)
	require.Zero(t, annotated)
}
