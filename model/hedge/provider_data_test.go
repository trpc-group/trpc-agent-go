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
	"encoding/json"
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

// requestProjectionModel requires a request when mapping a native call to its
// registered tool name. Routing must not fall back to request-free projection.
type requestProjectionModel struct{ providerDataInfoModel }

func (requestProjectionModel) ProjectProviderData(*model.ProviderData) (model.Message, error) {
	return model.Message{}, errors.New("request tools required")
}

func (requestProjectionModel) ProjectProviderDataWithRequest(req *model.Request, data *model.ProviderData) (model.Message, error) {
	if string(data.Payload) != `[{"type":"function_call"}]` {
		return model.Message{}, errors.New("invalid native call")
	}
	return model.NewAssistantMessage(req.Messages[0].Content), nil
}

func TestRequestAwareProjectionSurvivesRouting(t *testing.T) {
	info := providerDataInfoModel{info: model.Info{Name: "native", ProviderDataProtocol: "test.native"}}
	routed, err := New(WithCandidates(info, requestProjectionModel{info}))
	require.NoError(t, err)
	projector, ok := routed.(model.ProviderDataRequestProjector)
	require.True(t, ok)
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("bound-tool")}}
	data := &model.ProviderData{Protocol: "test.native", Model: "native", Payload: json.RawMessage(`[{"type":"function_call"}]`)}
	message, err := projector.ProjectProviderDataWithRequest(request, data)
	require.NoError(t, err)
	require.Equal(t, "bound-tool", message.Content)
	_, err = projector.ProjectProviderData(data)
	require.ErrorContains(t, err, "request tools required")
	data.Payload = json.RawMessage(`[]`)
	_, err = projector.ProjectProviderDataWithRequest(request, data)
	require.ErrorContains(t, err, "invalid native call", "candidate projection failures reach the caller")
}
