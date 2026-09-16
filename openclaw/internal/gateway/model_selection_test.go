//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

func TestServer_RequestModelSelection(t *testing.T) {
	r := &runOptionsRunner{}
	s, err := New(
		r,
		WithSelectableModels("fast", "strong"),
		WithRunOptionResolver(func(
			ctx context.Context,
			input RunOptionInput,
		) (context.Context, []agent.RunOption, error) {
			if input.ModelName == "" {
				return ctx, nil, nil
			}
			return ctx, []agent.RunOption{
				agent.WithModelName(input.ModelName),
			}, nil
		}),
	)
	require.NoError(t, err)

	rsp, status := s.ProcessMessage(context.Background(), gwproto.MessageRequest{
		From:  "u1",
		Text:  "hello",
		Model: "strong",
	})
	require.Equal(t, http.StatusOK, status)
	require.Nil(t, rsp.Error)
	require.Equal(t, "strong", r.opts.ModelName)
}

func TestServer_RequestModelSelectionRejectsUnknown(t *testing.T) {
	r := &runOptionsRunner{}
	s, err := New(r, WithSelectableModels("fast", "strong"))
	require.NoError(t, err)

	req := gwproto.MessageRequest{
		From:  "u1",
		Text:  "hello",
		Model: "unknown",
	}
	rsp, status := s.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidModel, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, `"unknown"`)

	stream, apiErr, status := s.StreamMessage(context.Background(), req)
	require.Nil(t, stream)
	require.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, apiErr)
	require.Equal(t, errTypeInvalidModel, apiErr.Type)
}

func TestServer_RequestModelWithoutSelectableModelsReachesResolver(
	t *testing.T,
) {
	r := &runOptionsRunner{}
	s, err := New(
		r,
		WithRunOptionResolver(func(
			ctx context.Context,
			input RunOptionInput,
		) (context.Context, []agent.RunOption, error) {
			return ctx, []agent.RunOption{
				agent.WithModelName(input.ModelName),
			}, nil
		}),
	)
	require.NoError(t, err)

	rsp, status := s.ProcessMessage(context.Background(), gwproto.MessageRequest{
		From:  "u1",
		Text:  "hello",
		Model: "resolver-owned",
	})
	require.Equal(t, http.StatusOK, status)
	require.Nil(t, rsp.Error)
	require.Equal(t, "resolver-owned", r.opts.ModelName)
}

func TestWithSelectableModelsNormalizesNames(t *testing.T) {
	opts := newOptions(WithSelectableModels(" fast ", " ", "strong"))
	require.Equal(
		t,
		map[string]struct{}{"fast": {}, "strong": {}},
		opts.selectableModels,
	)

	opts = newOptions(WithSelectableModels())
	require.Nil(t, opts.selectableModels)
}
