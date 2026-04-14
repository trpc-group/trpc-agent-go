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
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

const defaultA2UISource = "a2ui/v0.8"

// NewFactory wraps a base translator factory and applies A2UI options.
func NewFactory(innerFactory runner.TranslatorFactory, baseOpts []translator.Option, a2uiOpts ...Option) runner.TranslatorFactory {
	if innerFactory == nil {
		return func(context.Context, *adapter.RunAgentInput, ...translator.Option) (translator.Translator, error) {
			return nil, errors.New("inner translator factory is nil")
		}
	}
	return func(ctx context.Context, input *adapter.RunAgentInput, opts ...translator.Option) (translator.Translator, error) {
		allOpts := append(slices.Clone(baseOpts), opts...)
		inner, err := innerFactory(ctx, input, allOpts...)
		if err != nil {
			return nil, err
		}
		if inner == nil {
			return nil, errors.New("inner translator factory returned nil translator")
		}
		return newA2UITranslator(inner, a2uiOpts...), nil
	}
}

// a2uiTranslator adapts default translator output for A2UI streaming.
type a2uiTranslator struct {
	inner                translator.Translator
	parser               *parser
	receiving            bool
	source               string
	passThroughEventHook PassThroughEventHook
}

func newA2UITranslator(inner translator.Translator, opts ...Option) *a2uiTranslator {
	a2uiOpts := newOptions(opts...)
	return &a2uiTranslator{
		inner:                inner,
		parser:               newParser(),
		source:               defaultA2UISource,
		passThroughEventHook: a2uiOpts.passThroughEventHook,
	}
}

// Translate runs the inner translator and converts text message chunks to RAW events.
func (t *a2uiTranslator) Translate(
	ctx context.Context,
	event *event.Event,
) ([]aguievents.Event, error) {
	translated, err := t.inner.Translate(ctx, event)
	if err != nil {
		return nil, err
	}
	outEvents := make([]aguievents.Event, 0)
	for _, translatedEvent := range translated {
		switch translatedEvent.Type() {
		case aguievents.EventTypeTextMessageStart:
			if t.receiving {
				return nil, errors.New("text message start event received but already receiving text message")
			}
			t.parser.reset()
			t.receiving = true
		case aguievents.EventTypeTextMessageContent:
			if !t.receiving {
				return nil, errors.New("text message content event received but not receiving text message")
			}
			contentEvent, ok := translatedEvent.(*aguievents.TextMessageContentEvent)
			if !ok {
				return nil, fmt.Errorf("invalid text message content event: %T", translatedEvent)
			}
			lines := t.parser.append(contentEvent.Delta)
			if len(lines) == 0 {
				continue
			}
			outEvents = append(outEvents, t.toRawEvents(lines)...)
		case aguievents.EventTypeTextMessageEnd:
			if !t.receiving {
				return nil, errors.New("text message end event received but not receiving text message")
			}
			lines := t.parser.flush()
			t.receiving = false
			if len(lines) == 0 {
				continue
			}
			outEvents = append(outEvents, t.toRawEvents(lines)...)
		case aguievents.EventTypeRunStarted:
			outEvents = append(outEvents, translatedEvent)
		case aguievents.EventTypeRunFinished, aguievents.EventTypeRunError:
			t.receiving = false
			t.parser.reset()
			outEvents = append(outEvents, translatedEvent)
		default:
			if t.passThroughEventHook != nil && t.passThroughEventHook(ctx, translatedEvent) {
				outEvents = append(outEvents, translatedEvent)
			}
		}
	}
	return outEvents, nil
}

// PostRunFinalizationEvents finalizes pending A2UI text streams after a run ends.
func (t *a2uiTranslator) PostRunFinalizationEvents(ctx context.Context) ([]aguievents.Event, error) {
	finalizer, ok := t.inner.(translator.PostRunFinalizingTranslator)
	if !ok {
		return nil, nil
	}
	translated, err := finalizer.PostRunFinalizationEvents(ctx)
	outEvents := make([]aguievents.Event, 0, len(translated))
	for _, translatedEvent := range translated {
		switch translatedEvent.Type() {
		case aguievents.EventTypeTextMessageStart:
			if t.receiving {
				return nil, errors.Join(err, errors.New("text message start event received but already receiving text message"))
			}
			t.parser.reset()
			t.receiving = true
		case aguievents.EventTypeTextMessageContent:
			if !t.receiving {
				return nil, errors.Join(err, errors.New("text message content event received but not receiving text message"))
			}
			contentEvent, ok := translatedEvent.(*aguievents.TextMessageContentEvent)
			if !ok {
				return nil, errors.Join(err, fmt.Errorf("invalid text message content event: %T", translatedEvent))
			}
			lines := t.parser.append(contentEvent.Delta)
			outEvents = append(outEvents, t.toRawEvents(lines)...)
		case aguievents.EventTypeTextMessageEnd:
			if !t.receiving {
				return nil, errors.Join(err, errors.New("text message end event received but not receiving text message"))
			}
			lines := t.parser.flush()
			t.receiving = false
			outEvents = append(outEvents, t.toRawEvents(lines)...)
		default:
			if t.passThroughEventHook != nil && t.passThroughEventHook(ctx, translatedEvent) {
				outEvents = append(outEvents, translatedEvent)
			}
		}
	}
	if t.receiving {
		lines := t.parser.flush()
		t.receiving = false
		outEvents = append(outEvents, t.toRawEvents(lines)...)
	}
	return outEvents, err
}

func (t *a2uiTranslator) toRawEvents(lines []string) []aguievents.Event {
	if len(lines) == 0 {
		return nil
	}
	rawEvents := make([]aguievents.Event, 0, len(lines))
	for _, line := range lines {
		rawEvents = append(rawEvents, aguievents.NewRawEvent(
			parseA2UIJSONLine(line),
			aguievents.WithSource(t.source),
		))
	}
	return rawEvents
}

func parseA2UIJSONLine(line string) any {
	var raw any
	if err := json.Unmarshal([]byte(line), &raw); err == nil {
		return raw
	}
	return line
}
