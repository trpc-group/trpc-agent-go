//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGraphCompletionCaptureContextHelpers(t *testing.T) {
	require.False(t, ShouldCaptureGraphCompletion(nil))
	require.True(t, ShouldCaptureGraphCompletion(WithGraphCompletionCapture(nil)))
	ctx := WithGraphCompletionCapture(context.Background())
	require.False(t, ShouldCaptureGraphCompletion(WithoutGraphCompletionCapture(ctx)))
	require.False(t, ShouldCaptureGraphCompletion(WithoutGraphCompletionCapture(nil)))
}

func TestIsGraphCompletionEventAndVisibleCompletionEvent(t *testing.T) {
	require.False(t, IsGraphCompletionEvent(nil))
	require.False(t, IsGraphCompletionEvent(&event.Event{}))
	raw := &event.Event{
		Response: &model.Response{
			Object: ObjectTypeGraphExecution,
			Done:   true,
		},
	}
	require.True(t, IsGraphCompletionEvent(raw))
	require.False(t, IsVisibleGraphCompletionEvent(nil))
	require.False(t, IsVisibleGraphCompletionEvent(raw))
	visible, ok := VisibleGraphCompletionEvent(raw)
	require.True(t, ok)
	require.True(t, IsVisibleGraphCompletionEvent(visible))
}

func TestVisibleGraphCompletionEvent_ReturnsFalseForNonCompletion(t *testing.T) {
	visible, ok := VisibleGraphCompletionEvent(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
	})
	require.False(t, ok)
	require.Nil(t, visible)
}

func TestVisibleGraphCompletionEvent_AddsCompletionMetadataWhenMissing(t *testing.T) {
	raw := &event.Event{
		Response: &model.Response{
			Object: "graph.execution",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("manual-final"),
			}},
		},
		StateDelta: map[string][]byte{
			"child_state": []byte(`"child-state"`),
		},
	}

	visible, ok := VisibleGraphCompletionEvent(raw)
	require.True(t, ok)
	require.True(t, IsVisibleGraphCompletionEvent(visible))
	require.Equal(t, model.ObjectTypeChatCompletion, visible.Object)
	require.Equal(t, []byte("{}"), visible.StateDelta[MetadataKeyCompletion])
}

func TestVisibleGraphCompletionEventWithDedup_DedupsByAssistantChoicesWhenResponseIDEmpty(
	t *testing.T,
) {
	finishReason := "stop"
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:        1,
				Message:      model.NewAssistantMessage("answer"),
				FinishReason: &finishReason,
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.True(t, IsVisibleGraphCompletionEvent(visible))
	require.Empty(t, visible.Response.Choices)
	require.Equal(t, []byte(`"answer"`), visible.StateDelta[StateKeyLastResponse])
}

func TestVisibleGraphCompletionEventWithDedup_DoesNotDedupWhenLaterChoicesDiffer(
	t *testing.T,
) {
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{
				{
					Index:   0,
					Message: model.NewAssistantMessage("answer"),
				},
				{
					Index:   1,
					Message: model.NewAssistantMessage("other"),
				},
			},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.Len(t, visible.Response.Choices, 1)
	require.Equal(t, "answer", visible.Response.Choices[0].Message.Content)
}

func TestVisibleGraphCompletionEventWithDedup_DedupsByResponseID(t *testing.T) {
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse:   "answer",
			StateKeyLastResponseID: "resp-1",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.Empty(t, visible.Response.Choices)
}

func TestVisibleGraphCompletionEventWithDedup_DoesNotFallbackToSignatureWhenResponseIDDiffers(
	t *testing.T,
) {
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse:   "answer",
			StateKeyLastResponseID: "resp-2",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.Len(t, visible.Response.Choices, 1)
	require.Equal(t, "answer", visible.Response.Choices[0].Message.Content)
	require.Equal(t, []byte(`"resp-2"`), visible.StateDelta[StateKeyLastResponseID])
}

func TestVisibleGraphCompletionEventsForForwarding_PreservesFullResponseForCallbacks(
	t *testing.T,
) {
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse:   "answer",
			StateKeyLastResponseID: "resp-1",
		}),
	)

	visible, fullRespEvent, ok := VisibleGraphCompletionEventsForForwarding(
		raw,
		emitted,
	)
	require.True(t, ok)
	require.Empty(t, visible.Response.Choices)
	require.NotNil(t, fullRespEvent)
	require.Len(t, fullRespEvent.Response.Choices, 1)
	require.Equal(t, "answer", fullRespEvent.Response.Choices[0].Message.Content)
}

func TestVisibleGraphCompletionEventsForForwarding_UsesVisibleEventWhenChoicesRemain(
	t *testing.T,
) {
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)

	visible, fullRespEvent, ok := VisibleGraphCompletionEventsForForwarding(raw, nil)
	require.True(t, ok)
	require.NotNil(t, visible)
	require.Same(t, visible, fullRespEvent)
}

func TestVisibleGraphCompletionEventsForForwardingWithAuthor_RestoresAuthor(
	t *testing.T,
) {
	raw := NewGraphCompletionEvent(
		WithCompletionEventInvocationID("inv-1"),
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)

	visible, fullRespEvent, ok := VisibleGraphCompletionEventsForForwardingWithAuthor(
		raw,
		nil,
		"child-agent",
	)
	require.True(t, ok)
	require.NotNil(t, visible)
	require.Equal(t, "child-agent", visible.Author)
	require.NotNil(t, fullRespEvent)
	require.Equal(t, "child-agent", fullRespEvent.Author)
}

func TestVisibleGraphCompletionEventsForForwardingWithAuthor_NonCompletion(t *testing.T) {
	visible, fullRespEvent, ok := VisibleGraphCompletionEventsForForwardingWithAuthor(
		&event.Event{
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
			},
		},
		nil,
		"child-agent",
	)
	require.False(t, ok)
	require.Nil(t, visible)
	require.Nil(t, fullRespEvent)
}

func TestShouldSuppressGraphCompletionEvent(t *testing.T) {
	raw := NewGraphCompletionEvent()
	require.False(t, ShouldSuppressGraphCompletionEvent(context.Background(), nil, raw))
	invocation := &agent.Invocation{}
	require.False(t, ShouldSuppressGraphCompletionEvent(context.Background(), invocation, raw))
	agent.WithDisableGraphCompletionEvent(true)(&invocation.RunOptions)
	require.True(t, ShouldSuppressGraphCompletionEvent(context.Background(), invocation, raw))
	require.False(t, ShouldSuppressGraphCompletionEvent(WithGraphCompletionCapture(context.Background()), invocation, raw))
	require.False(t, ShouldSuppressGraphCompletionEvent(context.Background(), invocation, &event.Event{}))
}

func TestCompletionResponseIDFromStateDelta(t *testing.T) {
	require.Empty(t, completionResponseIDFromStateDelta(nil))
	require.Empty(t, completionResponseIDFromStateDelta(map[string][]byte{
		StateKeyLastResponseID: []byte("{"),
	}))
	require.Equal(t, "resp-1", completionResponseIDFromStateDelta(map[string][]byte{
		StateKeyLastResponseID: []byte(`"resp-1"`),
	}))
}

func TestRecordAssistantResponseID_IgnoresUnsupportedEvents(t *testing.T) {
	emitted := map[string]struct{}{"keep": {}}
	require.Equal(t, emitted, RecordAssistantResponseID(emitted, nil))
	require.Equal(t, emitted, RecordAssistantResponseID(emitted, &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}))
	require.Equal(t, emitted, RecordAssistantResponseID(emitted, &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("question"),
			}},
		},
	}))
}

func TestVisibleGraphCompletionDedupHelpers_FalseBranches(t *testing.T) {
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)
	visible, ok := VisibleGraphCompletionEvent(raw)
	require.True(t, ok)
	require.False(t, shouldClearVisibleGraphCompletionChoices(nil, nil))
	require.False(t, shouldClearVisibleGraphCompletionChoices(&event.Event{
		Response: &model.Response{},
	}, map[string]struct{}{}))
	require.False(t, shouldClearVisibleGraphCompletionChoices(visible, map[string]struct{}{}))
	require.False(t, visibleGraphCompletionNeedsFullResponseSnapshot(nil, visible))
	require.False(t, visibleGraphCompletionNeedsFullResponseSnapshot(raw, &event.Event{
		Response: &model.Response{IsPartial: true},
	}))
	require.False(t, visibleGraphCompletionNeedsFullResponseSnapshot(raw, visible))
}

func TestAssistantChoiceSignature(t *testing.T) {
	require.Empty(t, assistantChoiceSignature(nil))
	require.Empty(t, assistantChoiceSignature([]model.Choice{{
		Message: model.NewUserMessage("user"),
	}}))
	require.Equal(
		t,
		`[{"role":"assistant","content":"answer"}]`,
		assistantChoiceSignature([]model.Choice{{
			Message: model.NewAssistantMessage("answer"),
		}}),
	)
	require.Equal(
		t,
		`[{"role":"assistant","content":"answer"},{"role":"assistant","content":"alt"}]`,
		assistantChoiceSignature([]model.Choice{
			{Message: model.NewAssistantMessage("answer")},
			{Message: model.NewAssistantMessage("alt")},
		}),
	)
}
