//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summarystate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryfork"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyRequestTransform_PreservedRebasesViewAndFork(t *testing.T) {
	inv := agent.NewInvocation()
	history := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
		}},
	}
	before := []model.Message{
		model.NewSystemMessage("stable"),
		history,
	}
	req := &model.Request{Messages: append([]model.Message(nil), before...)}
	summaryview.AttachProjection(inv, &summaryview.View{
		ContentRequestLength: len(before),
		Items: []summaryview.Item{{
			Message:      history,
			RequestIndex: 1,
		}},
	})
	summaryview.Finalize(inv, req, 10)
	summaryfork.Attach(inv, req)

	req.Messages[1].Content = " "
	customFork := &model.Request{
		Messages: append(append([]model.Message(nil), req.Messages...), model.NewAssistantMessage("reply")),
	}
	ok := ApplyRequestTransform(
		inv,
		modelrequest.TokenTailoringChange{
			Record: modelrequest.TokenTailoringRecord{
				BeforeMessages: 2,
				AfterMessages:  2,
				Provenance:     modelrequest.TokenTailoringProvenancePreserved,
			},
			Preserved: &modelrequest.PreservedTransform{
				Before: before,
				After:  req.Messages,
			},
		},
		req,
		func() *model.Request { return customFork },
	)

	require.True(t, ok)
	view, found := summaryview.Snapshot(inv)
	require.True(t, found)
	require.True(t, view.Bound)
	require.Equal(t, " ", view.Items[0].Message.Content)
	fork, found := summaryfork.Request(inv)
	require.True(t, found)
	require.Equal(t, customFork.Messages, fork.Messages)
}

func TestApplyRequestTransform_InvalidatesBothOnUnsafeAndStaysMonotonic(t *testing.T) {
	inv := agent.NewInvocation()
	history := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
		}},
	}
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("stable"),
		history,
	}}
	summaryview.AttachProjection(inv, &summaryview.View{
		ContentRequestLength: len(req.Messages),
		Items: []summaryview.Item{{
			Message:      history,
			RequestIndex: 1,
		}},
	})
	summaryview.Finalize(inv, req, 10)
	summaryfork.Attach(inv, req)

	// Unsafe transform invalidates both snapshots.
	ok := ApplyRequestTransform(
		inv,
		modelrequest.TokenTailoringChange{
			Record: modelrequest.TokenTailoringRecord{
				BeforeMessages: 2,
				AfterMessages:  2,
				Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
			},
		},
		req,
		nil,
	)
	require.False(t, ok)
	view, found := summaryview.Snapshot(inv)
	require.True(t, found)
	require.False(t, view.Bound)
	_, found = summaryfork.Request(inv)
	require.False(t, found)

	// A later preserved transform cannot revive summaryview or summaryfork.
	before := append([]model.Message(nil), req.Messages...)
	req.Messages[1].Content = " "
	ok = ApplyRequestTransform(
		inv,
		modelrequest.TokenTailoringChange{
			Record: modelrequest.TokenTailoringRecord{
				BeforeMessages: 2,
				AfterMessages:  2,
				Provenance:     modelrequest.TokenTailoringProvenancePreserved,
			},
			Preserved: &modelrequest.PreservedTransform{
				Before: before,
				After:  req.Messages,
			},
		},
		req,
		nil,
	)
	require.False(t, ok)
	view, found = summaryview.Snapshot(inv)
	require.True(t, found)
	require.False(t, view.Bound)
	_, found = summaryfork.Request(inv)
	require.False(t, found)
}

func TestApplyRequestTransform_RequiresValidPreservedSnapshots(t *testing.T) {
	before := []model.Message{model.NewUserMessage("before")}
	after := []model.Message{model.NewUserMessage("after")}
	valid := modelrequest.TokenTailoringChange{
		Record: modelrequest.TokenTailoringRecord{
			BeforeMessages: 1,
			AfterMessages:  1,
			Provenance:     modelrequest.TokenTailoringProvenancePreserved,
		},
		Preserved: &modelrequest.PreservedTransform{
			Before: before,
			After:  after,
		},
	}
	require.True(t, canRebaseRequestTransform(
		valid,
		&model.Request{Messages: after},
	))
	require.True(t, canRebaseRequestTransform(
		valid,
		&model.Request{Messages: []model.Message{model.NewUserMessage("after")}},
	))

	noPreserved := valid
	noPreserved.Preserved = nil
	require.False(t, canRebaseRequestTransform(
		noPreserved,
		&model.Request{Messages: after},
	))

	mismatchedLen := valid
	mismatchedLen.Record.AfterMessages = 2
	mismatchedLen.Preserved = &modelrequest.PreservedTransform{
		Before: before,
		After:  []model.Message{after[0], after[0]},
	}
	require.False(t, canRebaseRequestTransform(
		mismatchedLen,
		&model.Request{Messages: mismatchedLen.Preserved.After},
	))

	staleAfter := valid
	staleAfter.Preserved = &modelrequest.PreservedTransform{
		Before: before,
		After:  []model.Message{model.NewUserMessage("stale")},
	}
	require.False(t, canRebaseRequestTransform(
		staleAfter,
		&model.Request{Messages: after},
	))
	require.False(t, canRebaseRequestTransform(
		valid,
		&model.Request{Messages: []model.Message{
			model.NewUserMessage("after"),
			model.NewUserMessage("extra"),
		}},
	))
}
