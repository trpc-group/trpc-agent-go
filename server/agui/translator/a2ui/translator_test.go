//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2ui

import (
	"context"
	"errors"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

type fakeTranslator struct {
	events       [][]aguievents.Event
	translateErr error
}

type finalizingFakeTranslator struct {
	*fakeTranslator
	finalizationEvents [][]aguievents.Event
}

func (f *fakeTranslator) Translate(ctx context.Context, event *agentevent.Event) ([]aguievents.Event, error) {
	_ = ctx
	_ = event
	if f.translateErr != nil {
		return nil, f.translateErr
	}
	if len(f.events) == 0 {
		return nil, nil
	}
	chunk := f.events[0]
	f.events = f.events[1:]
	return chunk, nil
}

func (f *finalizingFakeTranslator) PostRunFinalizationEvents(
	ctx context.Context,
) ([]aguievents.Event, error) {
	_ = ctx
	if len(f.finalizationEvents) == 0 {
		return nil, nil
	}
	chunk := f.finalizationEvents[0]
	f.finalizationEvents = f.finalizationEvents[1:]
	return chunk, nil
}

func makeTextEvents() [][]aguievents.Event {
	return [][]aguievents.Event{
		{
			aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			aguievents.NewTextMessageContentEvent("msg-1", `{"type":"a"}`+"\n"+`{"type":"b"}`+"\n"+`{"type":"c"}`),
			aguievents.NewTextMessageEndEvent("msg-1"),
		},
	}
}

func makeStreamingTextEvents() [][]aguievents.Event {
	return [][]aguievents.Event{
		{
			aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
		},
		{
			aguievents.NewTextMessageContentEvent("msg-1", `{"type":"a"}`+"\n"+`{"type":"b"}`),
		},
		{
			aguievents.NewTextMessageEndEvent("msg-1"),
		},
	}
}

func TestNewFactory(t *testing.T) {
	inner := &fakeTranslator{
		events: makeTextEvents(),
	}
	var receivedOpts []translator.Option
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, topts ...translator.Option) (translator.Translator, error) {
		receivedOpts = append(receivedOpts, topts...)
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
	}, translator.WithGraphNodeLifecycleActivityEnabled(true))
	assert.NoError(t, err)
	assert.NotNil(t, translated)
	assert.Len(t, receivedOpts, 1)
}

func TestNewFactoryNilInnerFactory(t *testing.T) {
	factory := NewFactory(nil, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
	})
	assert.Nil(t, translated)
	assert.EqualError(t, err, "inner translator factory is nil")
}

func TestA2UITranslatorConvertsTextMessagesToRawEvents(t *testing.T) {
	inner := &fakeTranslator{
		events: makeTextEvents(),
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 3)
	first, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	second, ok := out[1].(*aguievents.RawEvent)
	assert.True(t, ok)
	third, ok := out[2].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.NotNil(t, first.Source)
	assert.Equal(t, defaultA2UISource, *first.Source)
	assert.Equal(t, "a", first.Event.(map[string]any)["type"])
	assert.Equal(t, "b", second.Event.(map[string]any)["type"])
	assert.Equal(t, "c", third.Event.(map[string]any)["type"])
}

func TestA2UITranslatorConvertsStreamingTextMessagesAcrossCalls(t *testing.T) {
	inner := &fakeTranslator{
		events: makeStreamingTextEvents(),
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "a", out[0].(*aguievents.RawEvent).Event.(map[string]any)["type"])
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "b", out[0].(*aguievents.RawEvent).Event.(map[string]any)["type"])
}

func TestA2UITranslatorReturnsErrorAfterNonTextInSameTranslateCall(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewRunStartedEvent("thread-1", "run-1"),
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"orphan"}`),
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"ok"}`),
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.EqualError(t, err, "text message content event received but not receiving text message")
}

func TestA2UITranslatorHandlesContentAndEndInSameTranslateCall(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"single"}`),
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	raw, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.Equal(t, "single", raw.Event.(map[string]any)["type"])
}

func TestA2UITranslatorPostRunFinalizationConvertsInnerFinalizerEvents(t *testing.T) {
	inner := &finalizingFakeTranslator{
		fakeTranslator: &fakeTranslator{
			events: [][]aguievents.Event{
				{
					aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
					aguievents.NewTextMessageContentEvent("msg-1", `{"type":"final"}`),
				},
			},
		},
		finalizationEvents: [][]aguievents.Event{
			{
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Empty(t, out)
	finalizer, ok := translated.(translator.PostRunFinalizingTranslator)
	assert.True(t, ok)
	out, err = finalizer.PostRunFinalizationEvents(context.Background())
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	raw, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.Equal(t, "final", raw.Event.(map[string]any)["type"])
}

func TestA2UITranslatorPostRunFinalizationHandlesFinalizerTextStream(t *testing.T) {
	inner := &finalizingFakeTranslator{
		fakeTranslator: &fakeTranslator{},
		finalizationEvents: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"streamed"}`+"\n"+`{"type":"tail"}`),
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	finalizer, ok := translated.(translator.PostRunFinalizingTranslator)
	assert.True(t, ok)
	out, err := finalizer.PostRunFinalizationEvents(context.Background())
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	first, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	second, ok := out[1].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.Equal(t, "streamed", first.Event.(map[string]any)["type"])
	assert.Equal(t, "tail", second.Event.(map[string]any)["type"])
}

func TestA2UITranslatorReturnsErrorWhenTextMessageStartNestedWithoutEnd(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"first"}`),
			},
			{
				aguievents.NewTextMessageStartEvent("msg-2", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-2", `{"type":"second"}`),
			},
			{
				aguievents.NewTextMessageEndEvent("msg-2"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.EqualError(t, err, "text message start event received but already receiving text message")
}

func TestA2UITranslatorDropsNonTextEvents(t *testing.T) {
	otherEvent := aguievents.NewCustomEvent("custom", aguievents.WithValue(map[string]any{"k": "v"}))
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{otherEvent},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
}

func TestA2UITranslatorRunEventsAreAlwaysPassedThrough(t *testing.T) {
	runStarted := aguievents.NewRunStartedEvent("thread-1", "run-1")
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{runStarted},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Same(t, runStarted, out[0])
}

func TestA2UITranslatorPassThroughNonTextEventsWithOption(t *testing.T) {
	otherEvent := aguievents.NewCustomEvent("custom", aguievents.WithValue(map[string]any{"k": "v"}))
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{otherEvent},
		},
	}
	factory := NewFactory(
		func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return inner, nil
		},
		nil,
		WithPassThroughEventHook(func(_ context.Context, e aguievents.Event) bool {
			return e.Type() == aguievents.EventTypeCustom
		}),
	)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Same(t, otherEvent, out[0])
}

func TestA2UITranslatorReturnsErrorFromInnerFactory(t *testing.T) {
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return nil, errors.New("inner factory failed")
	}, nil)
	_, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.Error(t, err)
}

func TestA2UITranslatorReturnsErrorWhenInnerFactoryReturnsNilTranslator(t *testing.T) {
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return nil, nil
	}, nil)
	_, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.EqualError(t, err, "inner translator factory returned nil translator")
}

func TestA2UITranslatorReturnsErrorFromInnerTranslate(t *testing.T) {
	inner := &fakeTranslator{
		translateErr: errors.New("inner translate failed"),
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.Error(t, err)
}

func TestA2UITranslatorReturnsErrorWhenTextMessageEndWithoutStart(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.EqualError(t, err, "text message end event received but not receiving text message")
}

func TestA2UITranslatorReturnsErrorWhenTextMessageContentWithoutStart(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"orphan"}`),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.EqualError(t, err, "text message content event received but not receiving text message")
}

func TestA2UITranslatorRecoversAfterStateError(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"orphan"}`),
			},
			{
				aguievents.NewTextMessageStartEvent("msg-2", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-2", `{"type":"ok"}`),
			},
			{
				aguievents.NewTextMessageEndEvent("msg-2"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.EqualError(t, err, "text message content event received but not receiving text message")
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	raw, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.Equal(t, "ok", raw.Event.(map[string]any)["type"])
}

func TestA2UITranslatorConvertsInterleavedRunEventDuringTextStreaming(t *testing.T) {
	runStarted := aguievents.NewRunStartedEvent("thread-1", "run-1")
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"a"}`+"\n"),
			},
			{
				runStarted,
			},
			{
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"b"}`),
			},
			{
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)

	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "a", out[0].(*aguievents.RawEvent).Event.(map[string]any)["type"])

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Same(t, runStarted, out[0])

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "b", out[0].(*aguievents.RawEvent).Event.(map[string]any)["type"])

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
}

func TestA2UITranslatorResetsTextStateAfterRunFinished(t *testing.T) {
	runFinished := aguievents.NewRunFinishedEvent("thread-1", "run-1")
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
				aguievents.NewTextMessageContentEvent("msg-1", `{"type":"a"}`),
			},
			{
				runFinished,
			},
			{
				aguievents.NewTextMessageStartEvent("msg-2", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-2", `{"type":"b"}`+"\n"),
			},
			{
				aguievents.NewTextMessageEndEvent("msg-2"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)

	_, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Same(t, runFinished, out[0])

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "b", out[0].(*aguievents.RawEvent).Event.(map[string]any)["type"])

	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
}

func TestA2UITranslatorKeepsInvalidJSONAsString(t *testing.T) {
	inner := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			},
			{
				aguievents.NewTextMessageContentEvent("msg-1", "invalid-json"),
			},
			{
				aguievents.NewTextMessageEndEvent("msg-1"),
			},
		},
	}
	factory := NewFactory(func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
		return inner, nil
	}, nil)
	translated, err := factory(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	out, err := translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 0)
	out, err = translated.Translate(context.Background(), &agentevent.Event{})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	raw, ok := out[0].(*aguievents.RawEvent)
	assert.True(t, ok)
	assert.Equal(t, "invalid-json", raw.Event)
}
