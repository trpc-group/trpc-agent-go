//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

const (
	thinkEventTypeStart   thinkEventType = "think_start"
	thinkEventTypeContent thinkEventType = "think_content"
	thinkEventTypeEnd     thinkEventType = "think_end"
	thinkingInitial       thinkState     = "thinking_initial"
	thinkingReceiving     thinkState     = "thinking_receiving"
	thinkingCompleted     thinkState     = "thinking_completed"
)

type thinkEventType string

type thinkState string

type thinkTranslator struct {
	state thinkState
	inner translator.Translator
}

func newTranslator(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
	return &thinkTranslator{
		state: thinkingInitial,
		inner: translator.New(ctx, input.ThreadID, input.RunID),
	}
}

func (t *thinkTranslator) Translate(ctx context.Context, event *event.Event) ([]aguievents.Event, error) {
	var events []aguievents.Event
	switch t.state {
	case thinkingInitial:
		thinkContent := extractThinkContent(event)
		if thinkContent != "" {
			events = append(events,
				aguievents.NewCustomEvent(string(thinkEventTypeStart)),
				aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(thinkContent)),
			)
			t.state = thinkingReceiving
		}
	case thinkingReceiving:
		thinkContent := extractThinkContent(event)
		if thinkContent != "" {
			events = append(events,
				aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(thinkContent)),
			)
		}
		if extractContent(event) != "" {
			events = append(events, aguievents.NewCustomEvent(string(thinkEventTypeEnd)))
			t.state = thinkingCompleted
		}
	default:
	}
	innerEvents, err := t.inner.Translate(ctx, event)
	if err != nil {
		return nil, err
	}
	events = append(events, innerEvents...)
	return events, nil
}

func extractThinkContent(event *event.Event) string {
	if len(event.Choices) == 0 {
		return ""
	}
	if *isStream {
		return event.Choices[0].Delta.ReasoningContent
	}
	return event.Choices[0].Message.ReasoningContent
}

func extractContent(event *event.Event) string {
	if len(event.Choices) == 0 {
		return ""
	}
	if *isStream {
		return event.Choices[0].Delta.Content
	}
	return event.Choices[0].Message.Content
}
