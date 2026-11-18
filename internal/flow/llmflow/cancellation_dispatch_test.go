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

type cancellationPreparationModel struct {
	cancel       context.CancelFunc
	infoCalls    int
	channelCalls int
}

func (m *cancellationPreparationModel) Info() model.Info {
	m.infoCalls++
	m.cancel()
	return model.Info{Name: "probe", ProviderDataProtocol: "test.native"}
}

func (m *cancellationPreparationModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	m.channelCalls++
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

type cancellationPreparationIterModel struct {
	*cancellationPreparationModel
	iterCalls int
}

func (m *cancellationPreparationIterModel) GenerateContentIter(context.Context, *model.Request) (model.Seq[*model.Response], error) {
	m.iterCalls++
	return func(func(*model.Response) bool) {}, nil
}

func TestCancellationDispatch_ModelPreparation(t *testing.T) {
	for _, iter := range []bool{false, true} {
		t.Run(fmt.Sprintf("iter=%t", iter), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			channelModel := &cancellationPreparationModel{cancel: cancel}
			iterModel := &cancellationPreparationIterModel{cancellationPreparationModel: channelModel}
			var m model.Model = channelModel
			if iter {
				m = iterModel
			}
			request := &model.Request{Messages: []model.Message{{
				Role:         model.RoleAssistant,
				ProviderData: &model.ProviderData{Protocol: "test.native", Model: "probe", Payload: []byte("{}")},
			}}}
			_, err := (&Flow{}).generateContentSeq(ctx, agent.NewInvocation(), request, m)
			require.ErrorIs(t, err, context.Canceled)
			require.Positive(t, channelModel.infoCalls)
			require.Zero(t, channelModel.channelCalls)
			require.Zero(t, iterModel.iterCalls)
		})
	}
}
