//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTranslateNilEvent(t *testing.T) {
	translator := New("thread", "run")

	_, err := translator.Translate(nil)
	assert.Error(t, err)

	_, err = translator.Translate(&agentevent.Event{})
	assert.Error(t, err)
}

func TestTranslateErrorResponse(t *testing.T) {
	translator := New("thread", "run")
	rsp := &model.Response{Error: &model.ResponseError{Message: "boom"}}

	events, err := translator.Translate(&agentevent.Event{Response: rsp})
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	runErr, ok := events[0].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, "boom", runErr.Message)
	assert.Equal(t, "run", runErr.RunID())
}

func TestTextMessageEventStreamingAndCompletion(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	firstChunk := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}
	chunkEvents, err := translator.textMessageEvent(firstChunk)
	assert.NoError(t, err)
	assert.Len(t, chunkEvents, 2)
	start, ok := chunkEvents[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", start.MessageID)

	completionRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}
	completionEvents, err := translator.textMessageEvent(completionRsp)
	assert.NoError(t, err)
	assert.Len(t, completionEvents, 1)
	end, ok := completionEvents[0].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", end.MessageID)
}

func TestTextMessageEventNonStream(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	nonStreamRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}

	completionEvents, err := translator.textMessageEvent(nonStreamRsp)
	assert.NoError(t, err)
	assert.Len(t, completionEvents, 3)

	start, ok := completionEvents[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", start.MessageID)

	content, ok := completionEvents[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", content.MessageID)
	assert.Equal(t, "Hello", content.Delta)

	end, ok := completionEvents[2].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", end.MessageID)
}

func TestTextMessageEventEmptyChatCompletionContent(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		ID:      "final-empty",
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant}}},
	}

	events, err := translator.textMessageEvent(rsp)
	assert.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, "final-empty", translator.lastMessageID)
}

func TestTextMessageEventInvalidObject(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{ID: "bad", Object: "unknown", Choices: []model.Choice{{}}}

	_, err := translator.textMessageEvent(rsp)
	assert.Error(t, err)
}

func TestTextMessageEventEmptyResponse(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	events, err := translator.textMessageEvent(nil)
	assert.Empty(t, events)
	assert.NoError(t, err)
	events, err = translator.textMessageEvent(&model.Response{})
	assert.Empty(t, events)
	assert.NoError(t, err)
}

func TestToolCallAndResultEvents(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	callRsp := &model.Response{
		ID: "msg-tool",
		Choices: []model.Choice{{
			Message: model.Message{ToolCalls: []model.ToolCall{{
				ID:       "call-1",
				Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{"foo":"bar"}`)},
			}}},
		}},
	}

	callEvents, err := translator.toolCallEvent(callRsp)
	assert.NoError(t, err)
	assert.Len(t, callEvents, 2)
	start, ok := callEvents[0].(*aguievents.ToolCallStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", start.ToolCallID)
	assert.Equal(t, "lookup", start.ToolCallName)
	assert.Equal(t, "msg-tool", *start.ParentMessageID)
	args, ok := callEvents[1].(*aguievents.ToolCallArgsEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", args.ToolCallID)
	assert.Equal(t, "{\"foo\":\"bar\"}", args.Delta)
	assert.Equal(t, "msg-tool", translator.lastMessageID)

	resultRsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "call-1", Content: "done"},
		}},
	}
	resultEvents, err := translator.toolResultEvent(resultRsp)
	assert.NoError(t, err)
	assert.Len(t, resultEvents, 2)
	end, ok := resultEvents[0].(*aguievents.ToolCallEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", end.ToolCallID)
	res, ok := resultEvents[1].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-tool", res.MessageID)
	assert.Equal(t, "call-1", res.ToolCallID)
	assert.Equal(t, "done", res.Content)
}

func TestTranslateToolCallResponseIncludesAllEvents(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		ID:     "msg-tool",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{{
					ID:       "tool-call",
					Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{"q":"foo"}`)},
				}},
				Content: "hello",
			}},
		},
	}

	events, err := translator.Translate(&agentevent.Event{Response: rsp})
	assert.NoError(t, err)
	assert.Len(t, events, 5)

	start, ok := events[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-tool", start.MessageID)

	content, ok := events[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-tool", content.MessageID)
	assert.Equal(t, "hello", content.Delta)

	end, ok := events[2].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-tool", end.MessageID)

	toolStart, ok := events[3].(*aguievents.ToolCallStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "tool-call", toolStart.ToolCallID)

	args, ok := events[4].(*aguievents.ToolCallArgsEvent)
	assert.True(t, ok)
	assert.Equal(t, "tool-call", args.ToolCallID)
}

func TestTranslateFinalResponse(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		ID:     "final",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "done"},
		}},
		Done: true,
	}

	events, err := translator.Translate(&agentevent.Event{Response: rsp})
	assert.NoError(t, err)
	assert.Len(t, events, 4)

	start, ok := events[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "final", start.MessageID)

	content, ok := events[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "final", content.MessageID)
	assert.Equal(t, "done", content.Delta)

	end, ok := events[2].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "final", end.MessageID)

	finished, ok := events[3].(*aguievents.RunFinishedEvent)
	assert.True(t, ok)
	assert.Equal(t, "thread", finished.ThreadID())
	assert.Equal(t, "run", finished.RunID())
}

func TestTranslateToolResultResponse(t *testing.T) {
	translator := New("thread", "run")

	_, err := translator.Translate(&agentevent.Event{Response: &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "partial"},
		}},
	}})
	assert.NoError(t, err)

	events, err := translator.Translate(&agentevent.Event{Response: &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "tool-1", Content: "done"},
		}},
	}})
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.IsType(t, (*aguievents.ToolCallEndEvent)(nil), events[0])
	result, ok := events[1].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", result.MessageID)
	assert.Equal(t, "tool-1", result.ToolCallID)
	assert.Equal(t, "done", result.Content)
}

func TestTranslateSequentialEvents(t *testing.T) {
	translator := New("thread", "run")

	chunkRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "hi"},
		}},
	}
	events, err := translator.Translate(&agentevent.Event{Response: chunkRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 3)
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), events[0])
	assert.IsType(t, (*aguievents.TextMessageContentEvent)(nil), events[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), events[2])

	toolCallRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{{
					ID:       "call-1",
					Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{"q":"foo"}`)},
				}},
			},
		}},
	}
	events, err = translator.Translate(&agentevent.Event{Response: toolCallRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.IsType(t, (*aguievents.ToolCallStartEvent)(nil), events[0])
	assert.IsType(t, (*aguievents.ToolCallArgsEvent)(nil), events[1])

	toolResultRsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "call-1", Content: "success"},
		}},
	}
	events, err = translator.Translate(&agentevent.Event{Response: toolResultRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.IsType(t, (*aguievents.ToolCallEndEvent)(nil), events[0])
	res, ok := events[1].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", res.MessageID)
	assert.Equal(t, "call-1", res.ToolCallID)

	finalRsp := &model.Response{
		ID:     "msg-2",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "done"},
		}},
		Done: true,
	}
	events, err = translator.Translate(&agentevent.Event{Response: finalRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 4)
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), events[0])
	assert.IsType(t, (*aguievents.TextMessageContentEvent)(nil), events[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), events[2])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), events[3])
}

func TestFormatToolCallArguments(t *testing.T) {
	assert.Equal(t, "", formatToolCallArguments(nil))
	assert.Equal(t, "", formatToolCallArguments([]byte{}))
	assert.Equal(t, "{\"foo\":\"bar\"}", formatToolCallArguments([]byte(`{"foo":"bar"}`)))
}
