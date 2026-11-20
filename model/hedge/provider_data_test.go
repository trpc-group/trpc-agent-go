//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hedge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type providerDataInfoModel struct{ info model.Info }

func (m providerDataInfoModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, errors.New("unused")
}

func (m providerDataInfoModel) Info() model.Info { return m.info }

type providerHistoryModel struct {
	providerDataInfoModel
	protocol string
}

func (m providerHistoryModel) SupportsProviderHistory(protocol string) bool {
	return protocol == m.protocol
}

func TestProviderHistoryRequiresSupportFromEveryCandidate(t *testing.T) {
	info := model.Info{Name: "gpt-5", ProviderDataProtocol: "openai.responses"}
	supporting := providerHistoryModel{providerDataInfoModel{info}, "openai.responses"}
	otherProtocol := providerHistoryModel{providerDataInfoModel{info}, "other"}
	legacy := providerDataInfoModel{info}
	for _, tt := range []struct {
		name       string
		candidates []model.Model
		want       bool
	}{
		{"all support", []model.Model{supporting, supporting}, true},
		{"secondary does not implement", []model.Model{supporting, legacy}, false},
		{"primary does not implement", []model.Model{legacy, supporting}, false},
		{"secondary supports another protocol", []model.Model{supporting, otherProtocol}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			routed, err := New(WithCandidates(tt.candidates...))
			require.NoError(t, err)
			consumer, ok := routed.(model.ProviderHistoryConsumer)
			require.True(t, ok)
			require.Equal(t, tt.want, consumer.SupportsProviderHistory("openai.responses"))
		})
	}
}

func TestNewNativeProviderDataCandidates(t *testing.T) {
	native := providerDataInfoModel{info: model.Info{Name: "gpt-5", ProviderDataProtocol: "openai.responses"}}

	routed, err := New(WithCandidates(native, native))
	require.NoError(t, err)
	require.Equal(t, native.info.Name, routed.Info().Name)
	require.Equal(t, native.info.ProviderDataProtocol, routed.Info().ProviderDataProtocol)

	_, err = New(WithCandidates(
		providerDataInfoModel{info: model.Info{Name: "chat-a"}},
		providerDataInfoModel{info: model.Info{Name: "chat-b"}},
	), WithName("logical"))
	require.NoError(t, err, "heterogeneous legacy candidates remain supported")

	_, err = New(WithCandidates(native, providerDataInfoModel{info: model.Info{Name: "gpt-5"}}))
	require.ErrorContains(t, err, "hedge: provider data requires")

	_, err = New(WithCandidates(native, native), WithName("logical"))
	require.ErrorContains(t, err, "logical name to match")
}
