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

type replayInfoModel struct{ info model.Info }

func (m replayInfoModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, errors.New("unused")
}

func (m replayInfoModel) Info() model.Info { return m.info }

func TestNewNativeReplayCandidates(t *testing.T) {
	native := replayInfoModel{info: model.Info{Name: "gpt-5", ReplayProtocol: "openai.responses"}}

	routed, err := New(WithCandidates(native, native))
	require.NoError(t, err)
	require.Equal(t, native.info.ReplayProtocol, routed.Info().ReplayProtocol)

	_, err = New(WithCandidates(
		replayInfoModel{info: model.Info{Name: "chat-a"}},
		replayInfoModel{info: model.Info{Name: "chat-b"}},
	))
	require.NoError(t, err, "heterogeneous legacy candidates remain supported")

	_, err = New(WithCandidates(native, replayInfoModel{info: model.Info{Name: "gpt-5"}}))
	require.ErrorContains(t, err, "failover: native replay requires")
}
