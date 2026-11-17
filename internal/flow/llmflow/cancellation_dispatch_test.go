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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
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
