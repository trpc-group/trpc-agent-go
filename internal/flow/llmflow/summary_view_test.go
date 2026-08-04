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
}

func (c fixedSummaryViewTokenCounter) CountTokens(
	context.Context,
	model.Message,
) (int, error) {
	return c.tokens, nil
}

func (c fixedSummaryViewTokenCounter) CountTokensRange(
	context.Context,
	[]model.Message,
	int,
	int,
) (int, error) {
	return c.tokens, nil
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
