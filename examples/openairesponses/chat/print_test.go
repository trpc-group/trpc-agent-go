//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"testing"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPrintEventsCompletedText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		streaming bool
		deltas    []string
		completed string
		want      string
	}{
		{name: "matching", streaming: true, deltas: []string{"Hello", " world"}, completed: "Hello world", want: "Assistant: Hello world\n"},
		{name: "revised", streaming: true, deltas: []string{"A"}, completed: "B", want: "Assistant: A\nAssistant (completed): B\n"},
		{name: "missing_suffix", streaming: true, deltas: []string{"Hello"}, completed: "Hello world", want: "Assistant: Hello\nAssistant (completed): Hello world\n"},
		{name: "shorter_completion", streaming: true, deltas: []string{"Hello world"}, completed: "Hello", want: "Assistant: Hello world\nAssistant (completed): Hello\n"},
		{name: "multiple_revised_deltas", streaming: true, deltas: []string{"He", "llo"}, completed: "Goodbye", want: "Assistant: Hello\nAssistant (completed): Goodbye\n"},
		{name: "no_text_preview", streaming: true, deltas: []string{""}, completed: "Hello", want: "Assistant: Hello\n"},
		{name: "non_streaming", completed: "Hello", want: "Assistant: Hello\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan *event.Event, len(tc.deltas)+1)
			for _, delta := range tc.deltas {
				ch <- &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: delta}}}}}
			}
			ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: tc.completed}}}}}
			close(ch)
			var out bytes.Buffer
			printEvents(&out, ch, tc.streaming)
			if got := out.String(); got != tc.want {
				t.Fatalf("printEvents() = %q, want %q", got, tc.want)
			}
		})
	}
}
