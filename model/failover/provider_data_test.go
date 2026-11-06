//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package failover

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

func TestNewNativeProviderDataCandidates(t *testing.T) {
	native := providerDataInfoModel{info: model.Info{Name: "gpt-5", ProviderDataProtocol: "openai.responses"}}

	routed, err := New(WithCandidates(native, native))
	require.NoError(t, err)
	require.Equal(t, native.info.ProviderDataProtocol, routed.Info().ProviderDataProtocol)

	_, err = New(WithCandidates(
		providerDataInfoModel{info: model.Info{Name: "chat-a"}},
		providerDataInfoModel{info: model.Info{Name: "chat-b"}},
	))
	require.NoError(t, err, "heterogeneous legacy candidates remain supported")

	_, err = New(WithCandidates(native, providerDataInfoModel{info: model.Info{Name: "gpt-5"}}))
	require.ErrorContains(t, err, "failover: provider data requires")
}
