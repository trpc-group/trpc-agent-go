//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	iflow "trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fixedSummaryViewTokenCounter struct {
	tokens int
	err    error
}

func (c fixedSummaryViewTokenCounter) CountTokens(
	context.Context,
	model.Message,
) (int, error) {
	return c.tokens, c.err
}

func (c fixedSummaryViewTokenCounter) CountTokensRange(
	context.Context,
	[]model.Message,
	int,
	int,
) (int, error) {
	return c.tokens, c.err
}

func TestFinalizeSummaryViewUsesFinalRequest(t *testing.T) {
	invocation := agent.NewInvocation()
	summaryview.AttachProjection(invocation, &summaryview.View{
		ContentRequestLength: 1,
		Items: []summaryview.Item{{
			Message:      model.NewUserMessage("visible"),
			RequestIndex: 0,
		}},
	})
	request := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("added after content processing"),
		model.NewUserMessage("visible"),
	}}
	counter := fixedSummaryViewTokenCounter{tokens: 42}
	flow := &Flow{requestProcessors: []iflow.RequestProcessor{
		processor.NewContentRequestProcessor(
			processor.WithContextCompactionTokenCounter(counter),
		),
	}}

	finalizeSummaryView(
		context.Background(),
		invocation,
		request,
		flow.summaryViewTokenCounter(),
	)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.True(t, view.Bound)
	require.Equal(t, 42, view.RequestTokens)
	require.Equal(t, 1, view.Items[0].RequestIndex)
}

func TestFinalizeSummaryViewHandlesUnavailableInputs(t *testing.T) {
	counter := fixedSummaryViewTokenCounter{tokens: 42}
	require.NotPanics(t, func() {
		finalizeSummaryView(context.Background(), nil, &model.Request{}, counter)
	})

	nilRequest := agent.NewInvocation()
	finalizeSummaryView(context.Background(), nilRequest, nil, counter)
	_, ok := summaryview.Snapshot(nilRequest)
	require.False(t, ok)

	missingProjection := agent.NewInvocation()
	finalizeSummaryView(
		context.Background(),
		missingProjection,
		&model.Request{Messages: []model.Message{
			model.NewUserMessage("visible"),
		}},
		counter,
	)
	_, ok = summaryview.Snapshot(missingProjection)
	require.False(t, ok)
}

func TestFinalizeSummaryViewLeavesProjectionOnCountFailure(t *testing.T) {
	invocation := agent.NewInvocation()
	summaryview.AttachProjection(invocation, &summaryview.View{
		ContentRequestLength: 1,
		Items: []summaryview.Item{{
			Message:      model.NewUserMessage("visible"),
			RequestIndex: 0,
		}},
	})

	finalizeSummaryView(
		context.Background(),
		invocation,
		&model.Request{Messages: []model.Message{
			model.NewUserMessage("visible"),
		}},
		fixedSummaryViewTokenCounter{err: errors.New("count failed")},
	)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.False(t, view.Bound)
	require.Zero(t, view.RequestTokens)
}

func TestFinalizeSummaryViewUsesDefaultCounter(t *testing.T) {
	invocation := agent.NewInvocation()
	summaryview.AttachProjection(invocation, &summaryview.View{
		ContentRequestLength: 1,
		Items: []summaryview.Item{{
			Message:      model.NewUserMessage("visible"),
			RequestIndex: 0,
		}},
	})

	finalizeSummaryView(
		context.Background(),
		invocation,
		&model.Request{Messages: []model.Message{
			model.NewUserMessage("visible"),
		}},
		nil,
	)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.True(t, view.Bound)
	require.Positive(t, view.RequestTokens)
}
