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
	"encoding/json"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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

func TestTextMessageEventStreamInterruptedByNewMessage(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	firstChunk := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}
	initialEvents, err := translator.textMessageEvent(firstChunk)
	assert.NoError(t, err)
	assert.Len(t, initialEvents, 2)

	secondChunk := &model.Response{
		ID:     "msg-2",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "World"},
		}},
	}
	interruptedEvents, err := translator.textMessageEvent(secondChunk)
	assert.NoError(t, err)
	assert.Len(t, interruptedEvents, 3)

	endEvent, ok := interruptedEvents[0].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", endEvent.MessageID)

	startEvent, ok := interruptedEvents[1].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-2", startEvent.MessageID)

	contentEvent, ok := interruptedEvents[2].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-2", contentEvent.MessageID)
	assert.Equal(t, "World", contentEvent.Delta)

	assert.True(t, translator.receivingMessage)
	assert.Equal(t, "msg-2", translator.lastMessageID)
}

func TestTextMessageEventStreamInterruptedByNewMessage_NonStream(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	firstChunk := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}
	initialEvents, err := translator.textMessageEvent(firstChunk)
	assert.NoError(t, err)
	assert.Len(t, initialEvents, 2)

	secondChunk := &model.Response{
		ID:     "msg-2",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "World"},
		}},
	}
	interruptedEvents, err := translator.textMessageEvent(secondChunk)
	assert.NoError(t, err)
	assert.Len(t, interruptedEvents, 4)

	endEvent, ok := interruptedEvents[0].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", endEvent.MessageID)

	startEvent, ok := interruptedEvents[1].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-2", startEvent.MessageID)

	contentEvent, ok := interruptedEvents[2].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-2", contentEvent.MessageID)
	assert.Equal(t, "World", contentEvent.Delta)

	assert.False(t, translator.receivingMessage)
	assert.Equal(t, "msg-2", translator.lastMessageID)

	endEvent, ok = interruptedEvents[3].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-2", endEvent.MessageID)
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
	assert.Equal(t, "", translator.lastMessageID)
	assert.False(t, translator.receivingMessage)
}

func TestTextMessageEventEmptyChunkDoesNotChangeState(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		ID:     "chunk-empty",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant},
		}},
	}

	events, err := translator.textMessageEvent(rsp)
	assert.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, "", translator.lastMessageID)
	assert.False(t, translator.receivingMessage)
}

func TestTextMessageEventInvalidObject(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{ID: "bad", Object: "unknown", Choices: []model.Choice{{}}}

	_, err := translator.textMessageEvent(rsp)
	assert.Error(t, err)
}

func TestGraphModelMetadataProducesText(t *testing.T) {
	tr, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	meta := graph.ModelExecutionMetadata{Output: "hello from graph"}
	b, _ := json.Marshal(meta)
	evt := &agentevent.Event{
		ID:         "evt-model",
		StateDelta: map[string][]byte{graph.MetadataKeyModel: b},
	}
	evts, err := tr.Translate(evt)
	assert.NoError(t, err)
	assert.Len(t, evts, 3) // start + content + end
	start, ok := evts[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "evt-model", start.MessageID)
	content, ok := evts[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "hello from graph", content.Delta)
}

func TestGraphToolMetadataStartCompleteAndSkipDuplicateToolResponse(t *testing.T) {
	tr, ok := New("thread", "run").(*translator)
	assert.True(t, ok)

	metaStart := graph.ToolExecutionMetadata{
		ToolName: "calculator",
		ToolID:   "call-1",
		Phase:    graph.ToolExecutionPhaseStart,
		Input:    `{"a":1}`,
	}
	metaComplete := graph.ToolExecutionMetadata{
		ToolName: "calculator",
		ToolID:   "call-1",
		Phase:    graph.ToolExecutionPhaseComplete,
		Output:   `{"result":2}`,
	}
	bStart, _ := json.Marshal(metaStart)
	bDone, _ := json.Marshal(metaComplete)

	startEvt := &agentevent.Event{ID: "evt-start", StateDelta: map[string][]byte{graph.MetadataKeyTool: bStart}}
	evs, err := tr.Translate(startEvt)
	assert.NoError(t, err)
	assert.Len(t, evs, 3) // start + args + end

	doneEvt := &agentevent.Event{ID: "evt-done", StateDelta: map[string][]byte{graph.MetadataKeyTool: bDone}}
	// Provide dummy response to avoid nil-response error when metadata has no events.
	doneEvt.Response = &model.Response{Choices: []model.Choice{{}}}
	evs2, err := tr.Translate(doneEvt)
	assert.NoError(t, err)
	assert.Len(t, evs2, 0) // complete ignored; rely on tool.response

	toolRsp := &agentevent.Event{
		ID: "tool-rsp",
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					ToolID:  "call-1",
					Content: "ignored duplicate",
				},
			}},
		},
	}
	evs3, err := tr.Translate(toolRsp)
	assert.NoError(t, err)
	assert.Len(t, evs3, 1) // result from tool.response; end already emitted at start phase
	result, ok := evs3[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", result.ToolCallID)
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
	assert.Len(t, callEvents, 3)
	start, ok := callEvents[0].(*aguievents.ToolCallStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", start.ToolCallID)
	assert.Equal(t, "lookup", start.ToolCallName)
	assert.Equal(t, "msg-tool", *start.ParentMessageID)
	args, ok := callEvents[1].(*aguievents.ToolCallArgsEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", args.ToolCallID)
	assert.Equal(t, "{\"foo\":\"bar\"}", args.Delta)
	endCall, ok := callEvents[2].(*aguievents.ToolCallEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", endCall.ToolCallID)
	assert.Equal(t, "msg-tool", translator.lastMessageID)

	resultRsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "call-1", Content: "done"},
		}},
	}
	resultEvents, err := translator.toolResultEvent(resultRsp, "event-tool-result")
	assert.NoError(t, err)
	assert.Len(t, resultEvents, 1)
	res, ok := resultEvents[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "event-tool-result", res.MessageID)
	assert.Equal(t, "call-1", res.ToolCallID)
	assert.Equal(t, "done", res.Content)
	assert.Equal(t, "event-tool-result", translator.lastMessageID)
}

func TestToolResultEventDoesNotEmitEnd(t *testing.T) {
	tr, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "call-1", Content: "done"},
		}},
	}
	events, err := tr.toolResultEvent(rsp, "msg-1")
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	_, isEnd := events[0].(*aguievents.ToolCallEndEvent)
	assert.False(t, isEnd)
	res, ok := events[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", res.MessageID)
	assert.Equal(t, "call-1", res.ToolCallID)
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
	assert.Len(t, events, 6)

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
	endCall, ok := events[5].(*aguievents.ToolCallEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "tool-call", endCall.ToolCallID)
}

func TestTranslateFullResponse(t *testing.T) {
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
	assert.Len(t, events, 3)

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
}

func TestTranslateRunCompletionResponse(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	chunkRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "partial"},
		}},
		IsPartial: true,
	}

	events, err := translator.Translate(&agentevent.Event{Response: chunkRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	start, ok := events[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", start.MessageID)

	runCompletionRsp := &model.Response{
		ID:     "msg-run-completion",
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	}

	events, err = translator.Translate(&agentevent.Event{Response: runCompletionRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), events[0])

	finished, ok := events[1].(*aguievents.RunFinishedEvent)
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

	events, err := translator.Translate(&agentevent.Event{
		ID: "evt-tool-1",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{ToolID: "tool-1", Content: "done"},
			}},
		},
	})
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	result, ok := events[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "evt-tool-1", result.MessageID)
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
	assert.Len(t, events, 3)
	assert.IsType(t, (*aguievents.ToolCallStartEvent)(nil), events[0])
	assert.IsType(t, (*aguievents.ToolCallArgsEvent)(nil), events[1])
	assert.IsType(t, (*aguievents.ToolCallEndEvent)(nil), events[2])

	toolResultRsp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{ToolID: "call-1", Content: "success"},
		}},
	}
	events, err = translator.Translate(&agentevent.Event{ID: "evt-call-1-result", Response: toolResultRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	res, ok := events[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "evt-call-1-result", res.MessageID)
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
	assert.Len(t, events, 3)
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), events[0])
	assert.IsType(t, (*aguievents.TextMessageContentEvent)(nil), events[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), events[2])

	runCompletionRsp := &model.Response{
		ID:     "msg-run-completion",
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	}
	events, err = translator.Translate(&agentevent.Event{Response: runCompletionRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), events[0])
}

func TestFormatToolCallArguments(t *testing.T) {
	assert.Equal(t, "", formatToolCallArguments(nil))
	assert.Equal(t, "", formatToolCallArguments([]byte{}))
	assert.Equal(t, "{\"foo\":\"bar\"}", formatToolCallArguments([]byte(`{"foo":"bar"}`)))
}

func TestParallelToolCallResultEvents(t *testing.T) {
	translator := New("thread", "run")
	toolResultRsp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{ToolID: "call-1", Content: "result1"},
			},
			{
				Message: model.Message{ToolID: "call-2", Content: "result2"},
			},
		},
	}
	events, err := translator.Translate(&agentevent.Event{Response: toolResultRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	res1, ok := events[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", res1.ToolCallID)
	assert.Equal(t, "result1", res1.Content)
	res2, ok := events[1].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-2", res2.ToolCallID)
	assert.Equal(t, "result2", res2.Content)
}

func TestToolNilResponse(t *testing.T) {
	translator, ok := New("thread", "run").(*translator)
	assert.True(t, ok)
	events, err := translator.toolCallEvent(nil)
	assert.Empty(t, events)
	assert.NoError(t, err)
	events, err = translator.toolResultEvent(nil, "")
	assert.Empty(t, events)
	assert.NoError(t, err)
}

func TestGraphToolEventsUsesResponseID(t *testing.T) {
	tr := New("thread", "run")

	meta := graph.ToolExecutionMetadata{
		ToolName:   "generate_experiment_report",
		ToolID:     "call-1",
		ResponseID: "resp-123",
		Phase:      graph.ToolExecutionPhaseStart,
		Input:      `{"exp_group_id":1}`,
	}
	raw, err := json.Marshal(meta)
	require.NoError(t, err)

	evt := &event.Event{
		ID: "evt-tool",
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: raw,
		},
	}

	translated, err := tr.Translate(evt)
	require.NoError(t, err)
	require.Len(t, translated, 3)

	start, ok := translated[0].(*events.ToolCallStartEvent)
	require.True(t, ok)
	require.NotNil(t, start.ParentMessageID)
	require.Equal(t, meta.ResponseID, *start.ParentMessageID)
}
