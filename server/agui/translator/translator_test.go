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
	"context"
	"encoding/json"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTranslateNilEvent(t *testing.T) {
	translator := New(context.Background(), "thread", "run")

	_, err := translator.Translate(context.Background(), nil)
	assert.Error(t, err)

	_, err = translator.Translate(context.Background(), &agentevent.Event{})
	assert.Error(t, err)
}

func TestTranslateErrorResponse(t *testing.T) {
	translator := New(context.Background(), "thread", "run")
	rsp := &model.Response{Error: &model.ResponseError{Message: "boom"}}

	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: rsp})
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	runErr, ok := events[0].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, "boom", runErr.Message)
	assert.Equal(t, "run", runErr.RunID())
}

func TestTextMessageEventStreamingAndCompletion(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{ID: "bad", Object: "unknown", Choices: []model.Choice{{}}}

	_, err := translator.textMessageEvent(rsp)
	assert.Error(t, err)
}

func TestTextMessageEventChunkFinishReasonEndsStream(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	firstChunk := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "hi"},
		}},
	}
	initialEvents, err := translator.textMessageEvent(firstChunk)
	assert.NoError(t, err)
	assert.Len(t, initialEvents, 2)
	assert.True(t, translator.receivingMessage)

	reason := "stop"
	finishChunk := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta:        model.Message{Role: model.RoleAssistant},
			FinishReason: &reason,
		}},
	}
	events, err := translator.textMessageEvent(finishChunk)
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	end, ok := events[0].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", end.MessageID)
	assert.False(t, translator.receivingMessage)
}

func TestTextMessageEventChunkWithContentAndFinishReason(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	reason := "stop"
	chunk := &model.Response{
		ID:     "msg-finish",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta:        model.Message{Role: model.RoleAssistant, Content: "done"},
			FinishReason: &reason,
		}},
	}
	events, err := translator.textMessageEvent(chunk)
	assert.NoError(t, err)
	assert.Len(t, events, 3)

	start, ok := events[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-finish", start.MessageID)

	content, ok := events[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "done", content.Delta)

	end, ok := events[2].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-finish", end.MessageID)
	assert.False(t, translator.receivingMessage)
}

func TestGraphModelMetadataProducesText(t *testing.T) {
	tr, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	meta := graph.ModelExecutionMetadata{Output: "hello from graph", ResponseID: "resp-1"}
	b, _ := json.Marshal(meta)
	evt := &agentevent.Event{
		ID:         "evt-model",
		StateDelta: map[string][]byte{graph.MetadataKeyModel: b},
	}
	evts, err := tr.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, evts, 3) // start + content + end
	start, ok := evts[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, meta.ResponseID, start.MessageID)
	content, ok := evts[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "hello from graph", content.Delta)
}

func TestGraphModelEventsDeduplicatedByResponseID(t *testing.T) {
	tr := New(context.Background(), "thread", "run")

	rsp := &model.Response{
		ID:     "resp-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "graph output",
			},
		}},
		Done: true,
	}
	first, err := tr.Translate(context.Background(), &agentevent.Event{Response: rsp})
	assert.NoError(t, err)
	assert.NotEmpty(t, first)

	meta := graph.ModelExecutionMetadata{
		Output:     "graph output",
		ResponseID: rsp.ID,
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)
	graphEvt := &agentevent.Event{
		ID: "dup-graph",
		StateDelta: map[string][]byte{
			graph.MetadataKeyModel: raw,
		},
	}
	dups, err := tr.Translate(context.Background(), graphEvt)
	assert.NoError(t, err)
	assert.Len(t, dups, 0)
}

func TestGraphToolMetadataStartCompleteAndSkipDuplicateToolResponse(t *testing.T) {
	tr, ok := New(context.Background(), "thread", "run").(*translator)
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
	evs, err := tr.Translate(context.Background(), startEvt)
	assert.NoError(t, err)
	assert.Len(t, evs, 3) // start + args + end

	doneEvt := &agentevent.Event{ID: "evt-done", StateDelta: map[string][]byte{graph.MetadataKeyTool: bDone}}
	// Provide dummy response to avoid nil-response error when metadata has no events.
	doneEvt.Response = &model.Response{Choices: []model.Choice{{}}}
	evs2, err := tr.Translate(context.Background(), doneEvt)
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
	evs3, err := tr.Translate(context.Background(), toolRsp)
	assert.NoError(t, err)
	assert.Len(t, evs3, 1) // result from tool.response; end already emitted at start phase
	result, ok := evs3[0].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, "call-1", result.ToolCallID)
}

func TestTextMessageEventEmptyResponse(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)
	events, err := translator.textMessageEvent(nil)
	assert.Empty(t, events)
	assert.NoError(t, err)
	events, err = translator.textMessageEvent(&model.Response{})
	assert.Empty(t, events)
	assert.NoError(t, err)
}

func TestToolCallAndResultEvents(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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
	tr, ok := New(context.Background(), "thread", "run").(*translator)
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
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

	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: rsp})
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)
	rsp := &model.Response{
		ID:     "final",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "done"},
		}},
		Done: true,
	}

	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: rsp})
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)
	chunkRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "partial"},
		}},
		IsPartial: true,
	}

	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: chunkRsp})
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

	events, err = translator.Translate(context.Background(), &agentevent.Event{Response: runCompletionRsp})
	assert.NoError(t, err)
	assert.Len(t, events, 2)

	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), events[0])

	finished, ok := events[1].(*aguievents.RunFinishedEvent)
	assert.True(t, ok)
	assert.Equal(t, "thread", finished.ThreadID())
	assert.Equal(t, "run", finished.RunID())
}

func TestTranslateToolResultResponse(t *testing.T) {
	translator := New(context.Background(), "thread", "run")

	_, err := translator.Translate(context.Background(), &agentevent.Event{Response: &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "partial"},
		}},
	}})
	assert.NoError(t, err)

	events, err := translator.Translate(context.Background(), &agentevent.Event{
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
	translator := New(context.Background(), "thread", "run")

	chunkRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "hi"},
		}},
	}
	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: chunkRsp})
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
	events, err = translator.Translate(context.Background(), &agentevent.Event{Response: toolCallRsp})
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
	events, err = translator.Translate(context.Background(), &agentevent.Event{ID: "evt-call-1-result", Response: toolResultRsp})
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
	events, err = translator.Translate(context.Background(), &agentevent.Event{Response: finalRsp})
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
	events, err = translator.Translate(context.Background(), &agentevent.Event{Response: runCompletionRsp})
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
	translator := New(context.Background(), "thread", "run")
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
	events, err := translator.Translate(context.Background(), &agentevent.Event{Response: toolResultRsp})
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
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)
	events, err := translator.toolCallEvent(nil)
	assert.Empty(t, events)
	assert.NoError(t, err)
	events, err = translator.toolResultEvent(nil, "")
	assert.Empty(t, events)
	assert.NoError(t, err)
}

func TestGraphToolEventsDeduplicatedByToolID(t *testing.T) {
	tr := New(context.Background(), "thread", "run")

	toolCall := model.ToolCall{
		ID: "call-1",
		Function: model.FunctionDefinitionParam{
			Name:      "transfer_to_agent",
			Arguments: []byte(`{"agent_name":"math-graph","message":"计算"}`),
		},
	}
	callEvent := &agentevent.Event{
		Response: &model.Response{
			ID:     "resp-123",
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{toolCall},
				},
			}},
		},
	}

	first, err := tr.Translate(context.Background(), callEvent)
	assert.NoError(t, err)
	assert.NotEmpty(t, first)

	meta := graph.ToolExecutionMetadata{
		ToolName:   "generate_experiment_report",
		ToolID:     toolCall.ID,
		ResponseID: callEvent.Response.ID,
		Phase:      graph.ToolExecutionPhaseStart,
		Input:      `{"exp_group_id":1}`,
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)

	evt := &agentevent.Event{
		ID: "evt-tool",
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: raw,
		},
	}

	translated, err := tr.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, translated, 0)
}

func TestTranslateSubagentGraph_Stream(t *testing.T) {
	translator := New(context.Background(), "thread", "run")

	const (
		chatMessageID      = "chat-msg"
		transferToolCallID = "call-transfer"
		transferResultID   = "transfer-result"
		graphResponseID    = "graph-response"
		graphModelText     = "我需要先计算乘法部分，然后再进行加法运算。让我分步计算："
		toolResponseID     = "calc-result"
	)

	graphModelMeta, err := json.Marshal(graph.ModelExecutionMetadata{
		Output:     graphModelText,
		ResponseID: graphResponseID,
	})
	assert.NoError(t, err)
	toolMeta := graph.ToolExecutionMetadata{
		ToolName:   "calculator",
		ToolID:     "call-00-hyBMVOPvZ",
		ResponseID: graphResponseID,
		Phase:      graph.ToolExecutionPhaseStart,
		Input:      `{"operation":"multiply","a":456,"b":456}`,
	}
	rawToolMeta, err := json.Marshal(toolMeta)
	assert.NoError(t, err)

	chatEvent := &agentevent.Event{
		Response: &model.Response{
			ID:     chatMessageID,
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "我来帮你计算这个数学表达式。让我调用数学图表代理来处理这个计算。",
					ToolCalls: []model.ToolCall{{
						ID: transferToolCallID,
						Function: model.FunctionDefinitionParam{
							Name:      "transfer_to_agent",
							Arguments: []byte(`{"agent_name":"math-graph","message":"计算123+456*456"}`),
						},
					}},
				},
			}},
			Done: true,
		},
	}
	transferResult := &agentevent.Event{
		ID: transferResultID,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					ToolID:  transferToolCallID,
					Content: `{"success":true,"message":"Transfer initiated to agent 'math-graph'"}`,
				},
			}},
		},
	}
	graphModelEvent := &agentevent.Event{
		StateDelta: map[string][]byte{
			graph.MetadataKeyModel: graphModelMeta,
		},
	}
	toolMetaEvent := &agentevent.Event{
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: rawToolMeta,
		},
	}
	calcResult := &agentevent.Event{
		ID: toolResponseID,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					ToolID:  toolMeta.ToolID,
					Content: `{"operation":"multiply","a":456,"b":456,"result":207936}`,
				},
			}},
		},
	}
	runCompletion := &agentevent.Event{
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	}

	events := []*agentevent.Event{
		chatEvent,
		transferResult,
		graphModelEvent,
		toolMetaEvent,
		calcResult,
		runCompletion,
	}

	var translated []aguievents.Event
	for _, evt := range events {
		evs, err := translator.Translate(context.Background(), evt)
		assert.NoError(t, err)
		translated = append(translated, evs...)
	}
	assert.Len(t, translated, 15)

	start, ok := translated[0].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, chatMessageID, start.MessageID)

	content, ok := translated[1].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, chatEvent.Choices[0].Message.Content, content.Delta)

	end, ok := translated[2].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, chatMessageID, end.MessageID)

	callStart, ok := translated[3].(*aguievents.ToolCallStartEvent)
	assert.True(t, ok)
	assert.NotEmpty(t, chatEvent.Choices[0].Message.ToolCalls)
	expectedToolCall := chatEvent.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, expectedToolCall.ID, callStart.ToolCallID)
	assert.Equal(t, expectedToolCall.Function.Name, callStart.ToolCallName)
	if assert.NotNil(t, callStart.ParentMessageID) {
		assert.Equal(t, chatMessageID, *callStart.ParentMessageID)
	}

	callArgs, ok := translated[4].(*aguievents.ToolCallArgsEvent)
	assert.True(t, ok)
	assert.Equal(t, expectedToolCall.ID, callArgs.ToolCallID)
	assert.Equal(t, string(expectedToolCall.Function.Arguments), callArgs.Delta)

	callEnd, ok := translated[5].(*aguievents.ToolCallEndEvent)
	assert.True(t, ok)
	assert.Equal(t, expectedToolCall.ID, callEnd.ToolCallID)

	transfer, ok := translated[6].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, transferResult.ID, transfer.MessageID)
	assert.Equal(t, expectedToolCall.ID, transfer.ToolCallID)
	assert.Equal(t, transferResult.Choices[0].Message.Content, transfer.Content)

	modelStart, ok := translated[7].(*aguievents.TextMessageStartEvent)
	assert.True(t, ok)
	assert.Equal(t, graphResponseID, modelStart.MessageID)

	modelContent, ok := translated[8].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, graphResponseID, modelContent.MessageID)
	assert.Equal(t, graphModelText, modelContent.Delta)

	modelEnd, ok := translated[9].(*aguievents.TextMessageEndEvent)
	assert.True(t, ok)
	assert.Equal(t, graphResponseID, modelEnd.MessageID)

	callStart, ok = translated[10].(*aguievents.ToolCallStartEvent)
	assert.True(t, ok)
	assert.Equal(t, toolMeta.ToolID, callStart.ToolCallID)
	assert.Equal(t, toolMeta.ToolName, callStart.ToolCallName)
	assert.NotNil(t, callStart.ParentMessageID)
	assert.Equal(t, toolMeta.ResponseID, *callStart.ParentMessageID)

	callArgs, ok = translated[11].(*aguievents.ToolCallArgsEvent)
	assert.True(t, ok)
	assert.Equal(t, toolMeta.ToolID, callArgs.ToolCallID)
	assert.Equal(t, toolMeta.Input, callArgs.Delta)

	callEnd, ok = translated[12].(*aguievents.ToolCallEndEvent)
	assert.True(t, ok)
	assert.Equal(t, toolMeta.ToolID, callEnd.ToolCallID)

	transfer, ok = translated[13].(*aguievents.ToolCallResultEvent)
	assert.True(t, ok)
	assert.Equal(t, calcResult.ID, transfer.MessageID)
	assert.Equal(t, toolMeta.ToolID, transfer.ToolCallID)
	assert.Equal(t, calcResult.Choices[0].Message.Content, transfer.Content)

	runFinished, ok := translated[14].(*aguievents.RunFinishedEvent)
	assert.True(t, ok)
	assert.Equal(t, "thread", runFinished.ThreadID())
	assert.Equal(t, "run", runFinished.RunID())
}

func TestTranslateSubagentGraph_NonStream(t *testing.T) {
	translator := New(context.Background(), "thread", "run")

	const (
		chatResponseID        = "c4ee0e1b-4cd2-4d82-b17f-c58a59c9670b"
		transferToolCallID    = "call_00_287uVx8smsOO32bh1Eo8uTqL"
		transferResultEventID = "6922b48d-394a-40d5-b335-9486438417e3"
		graphResponseID       = "17e29cd5-c36a-4060-8783-753fcdee95b1"
		calculatorToolCallID  = "call_00_zP4ACTLaYJs8vKlyX4ggjesm"
	)

	chatEvent := &agentevent.Event{
		ID: "d4663c7f-7bd4-46f6-aa10-75e4bd75a0af",
		Response: &model.Response{
			ID:     chatResponseID,
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "我来帮你计算这个数学表达式。让我把这个问题转给专门处理数学计算的工具。",
					ToolCalls: []model.ToolCall{{
						ID: transferToolCallID,
						Function: model.FunctionDefinitionParam{
							Name:      "transfer_to_agent",
							Arguments: []byte(`{"agent_name": "math-graph", "message": "计算123+456*456"}`),
						},
					}},
				},
			}},
			Done: true,
		},
	}

	transferResult := &agentevent.Event{
		ID: transferResultEventID,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  `{"success":true,"message":"Transfer initiated to agent 'math-graph'","target_agent":"math-graph","transfer_type":"agent_handoff"}`,
					ToolID:   transferToolCallID,
					ToolName: "transfer_to_agent",
				},
			}},
		},
	}

	graphModelMeta, err := json.Marshal(graph.ModelExecutionMetadata{
		ModelName:  "deepseek-chat",
		NodeID:     "A",
		ResponseID: graphResponseID,
		Phase:      graph.ModelExecutionPhaseComplete,
		Output:     "我来帮你计算这个数学表达式。根据运算优先级，乘法应该先于加法进行。\n\n首先计算乘法部分：456 × 456",
	})
	assert.NoError(t, err)
	graphModelEvent := &agentevent.Event{
		ID: "0703a61c-a841-48cb-b129-4d59b9796421",
		StateDelta: map[string][]byte{
			graph.MetadataKeyModel: graphModelMeta,
		},
	}

	toolMetaStart, err := json.Marshal(graph.ToolExecutionMetadata{
		ToolName:   "calculator",
		ToolID:     calculatorToolCallID,
		ResponseID: graphResponseID,
		Phase:      graph.ToolExecutionPhaseStart,
		Input:      `{"operation": "multiply", "a": 456, "b": 456}`,
	})
	assert.NoError(t, err)
	graphToolEvent := &agentevent.Event{
		ID: "d576cef8-e004-4f8b-91f1-b8acaf66ee6f",
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: toolMetaStart,
		},
	}

	toolResult := &agentevent.Event{
		ID: "0ab06378-28d1-4964-8e45-d691c41bfff3",
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   calculatorToolCallID,
					ToolName: "calculator",
					Content:  `{"operation":"multiply","a":456,"b":456,"result":207936}`,
				},
			}},
		},
	}

	runCompletion := &agentevent.Event{
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	}

	events := []*agentevent.Event{
		chatEvent,
		transferResult,
		graphModelEvent,
		graphToolEvent,
		toolResult,
		runCompletion,
	}

	var translated []aguievents.Event
	for _, evt := range events {
		evs, err := translator.Translate(context.Background(), evt)
		assert.NoError(t, err)
		translated = append(translated, evs...)
	}

	assert.NotEmpty(t, translated)
	var (
		chatStarts         int
		graphStarts        int
		transferToolStarts int
		calcToolStarts     int
		runFinished        int
	)

	for _, ev := range translated {
		switch v := ev.(type) {
		case *aguievents.TextMessageStartEvent:
			switch v.MessageID {
			case chatResponseID:
				chatStarts++
			case graphResponseID:
				graphStarts++
			}
		case *aguievents.ToolCallStartEvent:
			switch v.ToolCallID {
			case transferToolCallID:
				transferToolStarts++
				if assert.NotNil(t, v.ParentMessageID) {
					assert.Equal(t, chatResponseID, *v.ParentMessageID)
				}
			case calculatorToolCallID:
				calcToolStarts++
				if assert.NotNil(t, v.ParentMessageID) {
					assert.Equal(t, graphResponseID, *v.ParentMessageID)
				}
			}
		case *aguievents.RunFinishedEvent:
			runFinished++
		}
	}

	assert.Equal(t, 1, chatStarts)
	assert.Equal(t, 1, graphStarts)
	assert.Equal(t, 1, transferToolStarts)
	assert.Equal(t, 1, calcToolStarts)
	assert.Equal(t, 1, runFinished)
}

func TestGraphNodeCustomEvents_CustomCategory(t *testing.T) {
	tr := New(context.Background(), "thread", "run")

	meta := graph.NodeCustomEventMetadata{
		EventType:    "my.custom.event",
		Category:     graph.NodeCustomEventCategoryCustom,
		NodeID:       "test-node",
		InvocationID: "test-invocation",
		StepNumber:   1,
		Payload:      map[string]any{"key": "value"},
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)

	evt := &agentevent.Event{
		ID:       "custom-evt-1",
		Response: &model.Response{Choices: []model.Choice{{}}},
		StateDelta: map[string][]byte{
			graph.MetadataKeyNodeCustom: raw,
		},
	}

	events, err := tr.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	customEvt, ok := events[0].(*aguievents.CustomEvent)
	assert.True(t, ok)
	assert.Equal(t, "my.custom.event", customEvt.Name)

	value, ok := customEvt.Value.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "test-node", value["nodeId"])
	assert.Equal(t, 1, value["stepNumber"])
	assert.NotNil(t, value["payload"])
}

func TestGraphNodeCustomEvents_ProgressCategory(t *testing.T) {
	tr := New(context.Background(), "thread", "run")

	meta := graph.NodeCustomEventMetadata{
		EventType:    "progress",
		Category:     graph.NodeCustomEventCategoryProgress,
		NodeID:       "processing-node",
		InvocationID: "test-invocation",
		Progress:     75.5,
		Message:      "Processing 75% complete",
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)

	evt := &agentevent.Event{
		ID:       "progress-evt-1",
		Response: &model.Response{Choices: []model.Choice{{}}},
		StateDelta: map[string][]byte{
			graph.MetadataKeyNodeCustom: raw,
		},
	}

	events, err := tr.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	customEvt, ok := events[0].(*aguievents.CustomEvent)
	assert.True(t, ok)
	assert.Equal(t, "progress", customEvt.Name)

	value, ok := customEvt.Value.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "processing-node", value["nodeId"])
	assert.Equal(t, 75.5, value["progress"])
	assert.Equal(t, "Processing 75% complete", value["message"])
}

func TestGraphNodeCustomEvents_TextCategory_NotReceivingMessage(t *testing.T) {
	tr := New(context.Background(), "thread", "run")

	meta := graph.NodeCustomEventMetadata{
		EventType:    "text",
		Category:     graph.NodeCustomEventCategoryText,
		NodeID:       "streaming-node",
		InvocationID: "test-invocation",
		Message:      "Hello streaming text",
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)

	evt := &agentevent.Event{
		ID:       "text-evt-1",
		Response: &model.Response{Choices: []model.Choice{{}}},
		StateDelta: map[string][]byte{
			graph.MetadataKeyNodeCustom: raw,
		},
	}

	events, err := tr.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	// Since we're not in a message context, it should be a CustomEvent
	customEvt, ok := events[0].(*aguievents.CustomEvent)
	assert.True(t, ok)
	assert.Equal(t, "text", customEvt.Name)

	value, ok := customEvt.Value.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "streaming-node", value["nodeId"])
	assert.Equal(t, "Hello streaming text", value["content"])
}

func TestGraphNodeCustomEvents_TextCategory_WhileReceivingMessage(t *testing.T) {
	translator, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	// First, start receiving a message
	chunkRsp := &model.Response{
		ID:     "msg-1",
		Object: model.ObjectTypeChatCompletionChunk,
		Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "Hello"},
		}},
	}
	_, err := translator.textMessageEvent(chunkRsp)
	assert.NoError(t, err)
	assert.True(t, translator.receivingMessage)

	// Now send a text event while receiving message
	meta := graph.NodeCustomEventMetadata{
		EventType:    "text",
		Category:     graph.NodeCustomEventCategoryText,
		NodeID:       "streaming-node",
		InvocationID: "test-invocation",
		Message:      "Streaming text content",
	}
	raw, err := json.Marshal(meta)
	assert.NoError(t, err)

	evt := &agentevent.Event{
		ID:       "text-evt-2",
		Response: &model.Response{Choices: []model.Choice{{}}},
		StateDelta: map[string][]byte{
			graph.MetadataKeyNodeCustom: raw,
		},
	}

	events, err := translator.Translate(context.Background(), evt)
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	// Since we're in a message context, it should be a TextMessageContentEvent
	contentEvt, ok := events[0].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "msg-1", contentEvt.MessageID)
	assert.Equal(t, "Streaming text content", contentEvt.Delta)
}

func TestGraphNodeCustomEvents_InvalidMetadata(t *testing.T) {
	tr, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	evt := &agentevent.Event{
		ID: "invalid-evt",
		StateDelta: map[string][]byte{
			graph.MetadataKeyNodeCustom: []byte("invalid json"),
		},
	}

	events := tr.graphNodeCustomEvents(evt)
	assert.Len(t, events, 1)

	errEvt, ok := events[0].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Contains(t, errEvt.Message, "invalid graph node custom metadata")
}

func TestGraphNodeCustomEvents_EmptyStateDelta(t *testing.T) {
	tr, ok := New(context.Background(), "thread", "run").(*translator)
	assert.True(t, ok)

	// Test nil StateDelta
	evt := &agentevent.Event{
		ID: "empty-evt",
	}
	events := tr.graphNodeCustomEvents(evt)
	assert.Empty(t, events)

	// Test empty MetadataKeyNodeCustom
	evt2 := &agentevent.Event{
		ID:         "empty-evt-2",
		StateDelta: map[string][]byte{},
	}
	events = tr.graphNodeCustomEvents(evt2)
	assert.Empty(t, events)
}
