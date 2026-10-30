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
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPrintEventsStreamingDoesNotRepeatFinalText(t *testing.T) {
	msg := model.Message{
		Role:             model.RoleAssistant,
		Content:          "visible",
		ReasoningContent: "hidden-reasoning",
	}
	if err := msg.SetReplayData(
		"openai.responses",
		"gpt-5",
		json.RawMessage(`{"encrypted_content":"opaque-secret"}`),
	); err != nil {
		t.Fatal(err)
	}
	got := collectPrint(t, true,
		&event.Event{Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID: "call_preview",
						Function: model.FunctionDefinitionParam{
							Name:      "preview_tool",
							Arguments: []byte(`{"a":1}`),
						},
					}},
				},
				Delta: model.Message{
					Content: "visible",
					ToolCalls: []model.ToolCall{{
						ID: "call_delta",
						Function: model.FunctionDefinitionParam{
							Name:      "preview_delta",
							Arguments: []byte(`{"leaked":true}`),
						},
					}},
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{
					ToolCalls: []model.ToolCall{{
						ID: "call_delta_done",
						Function: model.FunctionDefinitionParam{
							Name:      "preview_done",
							Arguments: []byte(`{"leaked":true}`),
						},
					}},
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: msg}},
		}},
	)
	const want = "Assistant: visible\n"
	if got != want {
		t.Fatalf("printEvents() = %q, want %q", got, want)
	}
}

func TestPrintEventsShowsCommittedToolCallAndResult(t *testing.T) {
	call := model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: "hidden-reasoning",
		ToolCalls: []model.ToolCall{{
			ID: "call_multiply",
			Function: model.FunctionDefinitionParam{
				Name:      "calculator",
				Arguments: []byte(`{"operation":"multiply","a":123,"b":456}`),
			},
		}},
	}
	if err := call.SetReplayData(
		"openai.responses",
		"gpt-5",
		json.RawMessage(`{"encrypted_content":"opaque-secret"}`),
	); err != nil {
		t.Fatal(err)
	}
	got := collectPrint(t, true,
		&event.Event{Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID: "call_message_preview",
						Function: model.FunctionDefinitionParam{
							Name:      "preview_message",
							Arguments: []byte(`{"a":1}`),
						},
					}},
				},
				Delta: model.Message{
					ToolCalls: []model.ToolCall{{
						ID: "call_delta_preview",
						Function: model.FunctionDefinitionParam{
							Name:      "preview_delta",
							Arguments: []byte(`{"a":2}`),
						},
					}},
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   "call_multiply",
					ToolName: "calculator",
				},
				Delta: model.Message{
					Role:     model.RoleTool,
					ToolID:   "call_multiply",
					ToolName: "calculator",
					Content:  `{"value":1}`,
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: call}},
		}},
		&event.Event{Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   "call_multiply",
					ToolName: "calculator",
					Content:  `{"value":56088}`,
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "Done."},
			}},
		}},
		&event.Event{Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Done.",
				},
			}},
		}},
	)
	const want = "" +
		"Tool call: calculator (ID: call_multiply)\n" +
		"  Args: {\"operation\":\"multiply\",\"a\":123,\"b\":456}\n" +
		"Tool result: calculator (ID: call_multiply): {\"value\":56088}\n" +
		"Assistant: Done.\n"
	if got != want {
		t.Fatalf("printEvents() = %q, want %q", got, want)
	}
	for _, hidden := range []string{
		"opaque-secret",
		"hidden-reasoning",
		"preview_message",
		"preview_delta",
		`{"value":1}`,
	} {
		if strings.Contains(got, hidden) {
			t.Fatalf("printEvents() exposed %q in %q", hidden, got)
		}
	}
}

func TestPrintEventsNonStreamingPrintsPreambleOnce(t *testing.T) {
	got := collectPrint(t, false,
		&event.Event{Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Working.",
					ToolCalls: []model.ToolCall{{
						ID: "call_multiply",
						Function: model.FunctionDefinitionParam{
							Name:      "calculator",
							Arguments: []byte(`{"operation":"multiply","a":123,"b":456}`),
						},
					}},
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleTool,
					ToolID:  "call_multiply",
					Content: `{"value":56088}`,
				},
			}},
		}},
		&event.Event{Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Done.",
				},
			}},
		}},
	)
	const want = "" +
		"Assistant: Working.\n" +
		"Tool call: calculator (ID: call_multiply)\n" +
		"  Args: {\"operation\":\"multiply\",\"a\":123,\"b\":456}\n" +
		"Tool result (ID: call_multiply): {\"value\":56088}\n" +
		"Assistant: Done.\n"
	if got != want {
		t.Fatalf("printEvents() = %q, want %q", got, want)
	}
	if strings.Count(got, "Working.") != 1 || strings.Count(got, "Done.") != 1 {
		t.Fatalf("assistant text was repeated: %q", got)
	}
}

func collectPrint(t *testing.T, streaming bool, events ...*event.Event) string {
	t.Helper()
	ch := make(chan *event.Event, len(events))
	for _, evt := range events {
		ch <- evt
	}
	close(ch)
	var buf bytes.Buffer
	printEvents(&buf, ch, streaming)
	return buf.String()
}

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

func TestPrintEventsRevisedTextBeforeToolCall(t *testing.T) {
	got := collectPrint(t, true,
		&event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: "Preview"}}}}},
		&event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant, Content: "Calculating",
			ToolCalls: []model.ToolCall{{ID: "call_add", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"operation":"add","a":1,"b":2}`)}}},
		}}}}},
		&event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "call_add", ToolName: "calculator", Content: `{"value":3}`}}}}},
		&event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: "3"}}}}},
		&event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "3"}}}}},
	)
	const want = "Assistant: Preview\n" +
		"Assistant (completed): Calculating\n" +
		"Tool call: calculator (ID: call_add)\n" +
		"  Args: {\"operation\":\"add\",\"a\":1,\"b\":2}\n" +
		"Tool result: calculator (ID: call_add): {\"value\":3}\n" +
		"Assistant: 3\n"
	if got != want {
		t.Fatalf("printEvents() = %q, want %q", got, want)
	}
}
