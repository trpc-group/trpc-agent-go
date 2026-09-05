//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryview

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// projectionFor builds a two-item projection over the given history messages.
func projectionFor(history []model.Message, requestLength int) *View {
	items := make([]Item, len(history))
	for i := range history {
		items[i] = Item{
			Message:      history[i],
			Boundary:     Boundary{EventID: "event-" + string(rune('a'+i))},
			RequestIndex: i,
		}
	}
	return &View{
		SessionID:            "session",
		ContentRequestLength: requestLength,
		Items:                items,
	}
}

func TestBindingReasonRecordsStageThatDecidedBinding(t *testing.T) {
	history := []model.Message{
		model.NewUserMessage("first"),
		model.NewAssistantMessage("second"),
	}

	t.Run("projection before finalize is not finalized", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.False(t, view.Bound)
		require.Equal(t, BindingReasonNotFinalized, view.BindingReason)
	})

	t.Run("finalize against matching request binds", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		Finalize(invocation, &model.Request{Messages: history}, 128)

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.True(t, view.Bound)
		require.Equal(t, BindingReasonBound, view.BindingReason)
	})

	t.Run("final request mismatch is reported as such", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		Finalize(invocation, &model.Request{Messages: []model.Message{
			model.NewUserMessage("rewritten by a provider adapter"),
		}}, 128)

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.False(t, view.Bound)
		require.Equal(t, BindingReasonRequestMismatch, view.BindingReason)
	})

	t.Run("explicit invalidation survives finalize", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		InvalidateBinding(invocation)

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.Equal(t, BindingReasonInvalidated, view.BindingReason)

		// Token tailoring invalidates before the request is finalized. The
		// later finalize must not overwrite the recorded cause even though the
		// projection would still match the request.
		Finalize(invocation, &model.Request{Messages: history}, 128)
		view, ok = Snapshot(invocation)
		require.True(t, ok)
		require.False(t, view.Bound)
		require.Equal(t, BindingReasonInvalidated, view.BindingReason)
	})

	t.Run("transform provenance mismatch is reported as such", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))

		after := []model.Message{model.NewUserMessage("compacted")}
		require.False(t, RebaseAfterTransform(invocation, history, after, nil))

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.False(t, view.Bound)
		require.Equal(t, BindingReasonTransformMismatch, view.BindingReason)
	})

	t.Run("rebase failure is distinguished from provenance mismatch", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))

		// Provenance covers the transform output, but the second projected
		// item has no output message, so the view cannot be rebased.
		after := []model.Message{model.NewUserMessage("first")}
		require.False(t, RebaseAfterTransform(
			invocation, history, after, []int{0},
		))

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.False(t, view.Bound)
		require.Equal(t, BindingReasonRebaseFailed, view.BindingReason)
	})

	t.Run("successful rebase rebinds the view", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))

		after := []model.Message{history[0], history[1]}
		require.True(t, RebaseAfterTransform(
			invocation, history, after, []int{0, 1},
		))

		view, ok := Snapshot(invocation)
		require.True(t, ok)
		require.True(t, view.Bound)
		require.Equal(t, BindingReasonBound, view.BindingReason)
	})
}

func TestBindingFromContextReportsViewState(t *testing.T) {
	history := []model.Message{
		model.NewUserMessage("first"),
		model.NewAssistantMessage("second"),
	}

	t.Run("absent view", func(t *testing.T) {
		binding := BindingFromContext(context.Background())
		require.False(t, binding.Present)
		require.False(t, binding.Bound)
		require.Equal(t, BindingReasonAbsent, binding.Reason)
		require.Zero(t, binding.Items)
	})

	t.Run("nil context", func(t *testing.T) {
		binding := BindingFromContext(nil)
		require.False(t, binding.Present)
		require.Equal(t, BindingReasonAbsent, binding.Reason)
	})

	t.Run("bound view", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		Finalize(invocation, &model.Request{Messages: history}, 4_096)
		view, ok := Snapshot(invocation)
		require.True(t, ok)

		binding := BindingFromContext(
			ContextWithView(context.Background(), view),
		)
		require.True(t, binding.Present)
		require.True(t, binding.Bound)
		require.Equal(t, BindingReasonBound, binding.Reason)
		require.Equal(t, 2, binding.Items)
		require.Equal(t, 4_096, binding.RequestTokens)
	})

	t.Run("unbound view keeps request tokens", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		InvalidateBinding(invocation)
		Finalize(invocation, &model.Request{Messages: history}, 4_096)
		view, ok := Snapshot(invocation)
		require.True(t, ok)

		binding := BindingFromContext(
			ContextWithView(context.Background(), view),
		)
		require.True(t, binding.Present)
		require.False(t, binding.Bound)
		require.Equal(t, BindingReasonInvalidated, binding.Reason)
		require.Equal(t, 4_096, binding.RequestTokens)
	})

	t.Run("view built without a recorded reason", func(t *testing.T) {
		// Views produced before a binding decision was recorded must not be
		// reported with an empty reason.
		binding := BindingFromContext(ContextWithView(
			context.Background(),
			&View{SessionID: "session", Items: []Item{{}}},
		))
		require.True(t, binding.Present)
		require.Equal(t, BindingReasonNotFinalized, binding.Reason)
	})
}

// TestBindingFromContextDoesNotCopyHistory guards the constraint that
// diagnostics never duplicate model-visible history: the reported binding
// carries counts only.
func TestBindingFromContextDoesNotCopyHistory(t *testing.T) {
	view := projectionFor([]model.Message{
		model.NewUserMessage("secret user content"),
	}, 1)
	view.Bound = true
	view.BindingReason = BindingReasonBound

	binding := BindingFromContext(ContextWithView(context.Background(), view))
	require.Equal(t, Binding{
		Present:       true,
		Bound:         true,
		Reason:        BindingReasonBound,
		Items:         1,
		RequestTokens: 0,
	}, binding)
}

func TestBindingFromInvocationReportsViewState(t *testing.T) {
	history := []model.Message{
		model.NewUserMessage("first"),
		model.NewAssistantMessage("second"),
	}

	t.Run("absent view", func(t *testing.T) {
		binding := BindingFromInvocation(agent.NewInvocation())
		require.False(t, binding.Present)
		require.False(t, binding.Bound)
		require.Equal(t, BindingReasonAbsent, binding.Reason)
		require.Zero(t, binding.Items)
	})

	t.Run("nil invocation", func(t *testing.T) {
		binding := BindingFromInvocation(nil)
		require.False(t, binding.Present)
		require.Equal(t, BindingReasonAbsent, binding.Reason)
	})

	t.Run("bound view", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		Finalize(invocation, &model.Request{Messages: history}, 4_096)

		binding := BindingFromInvocation(invocation)
		require.True(t, binding.Present)
		require.True(t, binding.Bound)
		require.Equal(t, BindingReasonBound, binding.Reason)
		require.Equal(t, 2, binding.Items)
		require.Equal(t, 4_096, binding.RequestTokens)
	})

	t.Run("unbound view keeps request tokens", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))
		InvalidateBinding(invocation)
		Finalize(invocation, &model.Request{Messages: history}, 4_096)

		binding := BindingFromInvocation(invocation)
		require.True(t, binding.Present)
		require.False(t, binding.Bound)
		require.Equal(t, BindingReasonInvalidated, binding.Reason)
		require.Equal(t, 4_096, binding.RequestTokens)
	})

	t.Run("projection before finalize is not finalized", func(t *testing.T) {
		invocation := agent.NewInvocation()
		AttachProjection(invocation, projectionFor(history, 2))

		binding := BindingFromInvocation(invocation)
		require.True(t, binding.Present)
		require.False(t, binding.Bound)
		require.Equal(t, BindingReasonNotFinalized, binding.Reason)
	})
}

// TestBindingFromInvocationDoesNotCopyHistory guards the constraint that
// diagnostics never duplicate model-visible history: the reported binding
// carries counts only.
func TestBindingFromInvocationDoesNotCopyHistory(t *testing.T) {
	invocation := agent.NewInvocation()
	view := projectionFor([]model.Message{
		model.NewUserMessage("secret user content"),
	}, 1)
	AttachProjection(invocation, view)
	Finalize(invocation, &model.Request{Messages: []model.Message{
		model.NewUserMessage("secret user content"),
	}}, 32)

	binding := BindingFromInvocation(invocation)
	require.Equal(t, Binding{
		Present:       true,
		Bound:         true,
		Reason:        BindingReasonBound,
		Items:         1,
		RequestTokens: 32,
	}, binding)
}
