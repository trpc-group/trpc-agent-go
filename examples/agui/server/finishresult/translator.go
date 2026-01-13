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
	"fmt"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

type finishResultTranslator struct {
	lastFinishReason string
	inner            translator.Translator
}

func newTranslator(ctx context.Context, input *adapter.RunAgentInput,
	opts ...translator.Option) (translator.Translator, error) {
	inner, err := translator.New(ctx, input.ThreadID, input.RunID, opts...)
	if err != nil {
		return nil, fmt.Errorf("create inner translator: %w", err)
	}
	return &finishResultTranslator{
		inner: inner,
	}, nil
}

func (t *finishResultTranslator) Translate(ctx context.Context, event *event.Event) ([]aguievents.Event, error) {
	if len(event.Choices) > 0 && event.Choices[0].FinishReason != nil {
		t.lastFinishReason = *event.Choices[0].FinishReason
	}
	aguiEvents, err := t.inner.Translate(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("inner translator: %w", err)
	}
	for _, e := range aguiEvents {
		if e.Type() == aguievents.EventTypeRunFinished {
			if aguiEvent, ok := e.(*aguievents.RunFinishedEvent); ok {
				aguiEvent.Result = t.lastFinishReason
			}
		}
	}
	return aguiEvents, nil
}
