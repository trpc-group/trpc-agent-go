//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	eventResponseCreated               = "response.created"
	eventResponseInProgress            = "response.in_progress"
	eventResponseCompleted             = "response.completed"
	eventResponseIncomplete            = "response.incomplete"
	eventResponseFailed                = "response.failed"
	eventResponseOutputTextDelta       = "response.output_text.delta"
	eventResponseFunctionCallArgsDelta = "response.function_call_arguments.delta"
	eventResponseFunctionCallArgsDone  = "response.function_call_arguments.done"
	eventResponseOutputItemAdded       = "response.output_item.added"
	eventResponseReasoningSummaryDelta = "response.reasoning_summary_text.delta"
	eventResponseReasoningTextDelta    = "response.reasoning_text.delta"
	eventError                         = "error"
)

func (m *Model) handleStreaming(
	ctx context.Context,
	params responses.ResponseNewParams,
	opts []option.RequestOption,
	emit func(*model.Response) bool,
) {
	var raw *http.Response
	streamOpts := append(append([]option.RequestOption{}, opts...), option.WithJSONSet("stream", true))
	err := m.client.Execute(ctx, http.MethodPost, "responses", params, &raw, streamOpts...)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		emit(apiErrorResponse(err))
		return
	}
	if raw == nil || raw.Body == nil {
		emit(apiErrorResponse(errNilStreamBody))
		return
	}
	defer raw.Body.Close()

	acc := &streamAccumulator{}
	decoder := ssestream.NewDecoder(raw)
	emittedTerminal := false
	for decoder.Next() {
		if ctx.Err() != nil {
			return
		}
		data := bytes.TrimSpace(decoder.Event().Data)
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var event responses.ResponseStreamEventUnion
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		if event.Type == "" {
			continue
		}
		m.runStreamCallback(ctx, &params, &event)
		if emittedTerminal {
			continue
		}
		if !handleStreamEvent(m.name, acc, &event, emit) {
			return
		}
		if isTerminalEvent(event.Type) {
			emittedTerminal = true
		}
	}
	if decoder.Err() != nil && !emittedTerminal && ctx.Err() == nil {
		emit(apiErrorResponse(decoder.Err()))
	}
}

var errNilStreamBody = errString("openai/responses: empty stream body")

type errString string

func (e errString) Error() string { return string(e) }

type streamAccumulator struct {
	id        string
	created   int64
	text      strings.Builder
	reasoning strings.Builder
	calls     []model.ToolCall
	callByID  map[string]int
}

func handleStreamEvent(
	modelName string,
	acc *streamAccumulator,
	event *responses.ResponseStreamEventUnion,
	emit func(*model.Response) bool,
) bool {
	switch event.Type {
	case eventResponseCreated, eventResponseInProgress:
		if event.Response.ID != "" {
			acc.id = event.Response.ID
			acc.created = createdUnix(&event.Response)
		}
		return true
	case eventResponseOutputTextDelta:
		if event.Delta == "" {
			return true
		}
		acc.text.WriteString(event.Delta)
		return emit(projectPartial(modelName, acc, event.Delta, ""))
	case eventResponseReasoningSummaryDelta, eventResponseReasoningTextDelta:
		if event.Delta == "" {
			return true
		}
		acc.reasoning.WriteString(event.Delta)
		return emit(projectPartial(modelName, acc, "", event.Delta))
	case eventResponseOutputItemAdded:
		if event.Item.Type == outputTypeFunctionCall {
			acc.addFunctionCall(event.Item.AsFunctionCall())
		}
		return true
	case eventResponseFunctionCallArgsDelta:
		acc.appendArguments(event.ItemID, event.Delta)
		return true
	case eventResponseFunctionCallArgsDone:
		acc.setArguments(event.ItemID, event.Arguments)
		return true
	case eventResponseCompleted, eventResponseIncomplete:
		resp := event.Response
		if len(resp.Output) == 0 {
			return emit(projectAccumulator(modelName, acc, string(resp.Status), &resp))
		}
		return emit(projectResponse(resp.ID, modelName, createdUnix(&resp), &resp, false, true, "", ""))
	case eventResponseFailed, eventError:
		if errResp := responseAPIError(&event.Response); errResp != nil {
			return emit(errResp)
		}
		msg := event.Message
		if msg == "" {
			msg = "responses stream failed"
		}
		return emit(apiErrorResponse(errString(msg)))
	default:
		return true
	}
}

func isTerminalEvent(typ string) bool {
	switch typ {
	case eventResponseCompleted, eventResponseIncomplete, eventResponseFailed, eventError:
		return true
	default:
		return false
	}
}

func projectPartial(modelName string, acc *streamAccumulator, delta, reasoningDelta string) *model.Response {
	return &model.Response{
		ID:        acc.id,
		Object:    model.ObjectTypeChatCompletionChunk,
		Created:   acc.created,
		Model:     modelName,
		Timestamp: time.Now(),
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{
				Role:             model.RoleAssistant,
				Content:          delta,
				ReasoningContent: reasoningDelta,
			},
		}},
	}
}

func projectAccumulator(modelName string, acc *streamAccumulator, status string, resp *responses.Response) *model.Response {
	finish := finishReasonStop
	if len(acc.calls) > 0 {
		finish = finishReasonToolCalls
	}
	if status == "incomplete" {
		finish = finishReasonLength
		if resp != nil && resp.IncompleteDetails.Reason == incompleteReasonContentFilter {
			finish = finishReasonContentFilter
		}
	}
	id := acc.id
	created := acc.created
	var usage *model.Usage
	if resp != nil {
		if resp.ID != "" {
			id = resp.ID
		}
		if resp.CreatedAt != 0 {
			created = createdUnix(resp)
		}
		usage = mapUsage(resp)
	}
	return &model.Response{
		ID:        id,
		Object:    model.ObjectTypeChatCompletion,
		Created:   created,
		Model:     modelName,
		Timestamp: time.Now(),
		Done:      true,
		Usage:     usage,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:             model.RoleAssistant,
				Content:          acc.text.String(),
				ReasoningContent: acc.reasoning.String(),
				ToolCalls:        acc.calls,
			},
			FinishReason: &finish,
		}},
	}
}

func (acc *streamAccumulator) addFunctionCall(call responses.ResponseFunctionToolCall) {
	if acc.callByID == nil {
		acc.callByID = make(map[string]int)
	}
	idx := len(acc.calls)
	key := call.ID
	if key == "" {
		key = call.CallID
	}
	acc.callByID[key] = idx
	if call.CallID != "" {
		acc.callByID[call.CallID] = idx
	}
	acc.calls = append(acc.calls, model.ToolCall{
		Type:  functionToolType,
		ID:    call.CallID,
		Index: intPtr(idx),
		Function: model.FunctionDefinitionParam{
			Name:      call.Name,
			Arguments: []byte(call.Arguments),
		},
	})
}

func (acc *streamAccumulator) appendArguments(itemID, delta string) {
	if delta == "" {
		return
	}
	idx, ok := acc.lookupCall(itemID)
	if !ok {
		acc.addFunctionCall(responses.ResponseFunctionToolCall{ID: itemID})
		idx = len(acc.calls) - 1
	}
	acc.calls[idx].Function.Arguments = append(acc.calls[idx].Function.Arguments, delta...)
}

func (acc *streamAccumulator) setArguments(itemID, arguments string) {
	if arguments == "" {
		return
	}
	idx, ok := acc.lookupCall(itemID)
	if !ok {
		acc.addFunctionCall(responses.ResponseFunctionToolCall{ID: itemID, Arguments: arguments})
		return
	}
	acc.calls[idx].Function.Arguments = []byte(arguments)
}

func (acc *streamAccumulator) lookupCall(itemID string) (int, bool) {
	if acc.callByID == nil {
		return 0, false
	}
	idx, ok := acc.callByID[itemID]
	return idx, ok
}
