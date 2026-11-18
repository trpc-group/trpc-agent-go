//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

func TestCancellationDispatch_DoesNotStartModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &Flow{}
	inv := agent.NewInvocation()
	channelModel := &mockModel{}
	_, err := f.generateContentSeq(ctx, inv, testLLMRequest(), channelModel)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, channelModel.requests)
	iterModel := &mockIterModel{}
	_, err = f.generateContentSeq(ctx, inv, testLLMRequest(), iterModel)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, iterModel.GenerateContentIterCalled)
	_, err = f.runOneStepWithResume(ctx, inv, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCancellationDispatch_ModelCallbacks(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, pluginBefore := range []bool{false, true} {
			for _, name := range []string{"pre_cancelled", "before_cancelled", "inherited", "background", "without_cancel", "cancelled_replacement", "expired_replacement", "success"} {
				t.Run(fmt.Sprintf("iter=%t/plugin_before=%t/%s", iter, pluginBefore, name), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					beforeCalls := 0
					callbacks := model.NewCallbacks()
					before := func(ctx context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						beforeCalls++
						switch name {
						case "before_cancelled":
							cancel()
						case "inherited":
							cancel()
							return &model.BeforeModelResult{Context: context.WithValue(ctx, struct{}{}, "value")}, nil
						case "background":
							cancel()
							return &model.BeforeModelResult{Context: context.Background()}, nil
						case "without_cancel":
							cancel()
							return &model.BeforeModelResult{Context: context.WithoutCancel(ctx)}, nil
						case "cancelled_replacement":
							replacement, stop := context.WithCancel(context.Background())
							stop()
							return &model.BeforeModelResult{Context: replacement}, nil
						case "expired_replacement":
							replacement, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
							defer stop()
							return &model.BeforeModelResult{Context: replacement}, nil
						}
						return nil, nil
					}
					mgr := plugin.MustNewManager(&hookPlugin{name: "cancel-model", reg: func(r *plugin.Registry) {
						if pluginBefore {
							r.BeforeModel(before)
						}
					}})
					if !pluginBefore {
						callbacks.RegisterBeforeModel(before)
					}
					inv := agent.NewInvocation(agent.WithInvocationPlugins(mgr))
					ctx = agent.NewInvocationContext(ctx, inv)
					if name == "pre_cancelled" {
						cancel()
					}
					channelModel := &mockModel{}
					var m model.Model = channelModel
					called := func() bool { return len(channelModel.requests) > 0 }
					if iter {
						iterModel := &mockIterModel{IterSeq: func(func(*model.Response) bool) {}}
						m = iterModel
						called = func() bool { return iterModel.GenerateContentIterCalled }
						t.Cleanup(func() { require.False(t, iterModel.GenerateContentCalled) })
					}
					f := &Flow{modelCallbacks: callbacks}
					_, seq, _, err := f.callLLM(ctx, inv, testLLMRequest(), m)
					if seq != nil {
						seq(func(*model.Response) bool { return true })
					}
					wantCall := name == "background" || name == "without_cancel" || name == "success"
					if wantCall {
						require.NoError(t, err)
					} else if name == "expired_replacement" {
						require.ErrorIs(t, err, context.DeadlineExceeded)
					} else {
						require.ErrorIs(t, err, context.Canceled)
					}
					require.Equal(t, wantCall, called())
					if name == "pre_cancelled" {
						require.Zero(t, beforeCalls)
					} else {
						require.Equal(t, 1, beforeCalls)
					}
				})
			}
		}
	}
}
