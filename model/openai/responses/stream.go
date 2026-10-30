//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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
	eventResponseCancelled             = "response.cancelled"
	eventResponseOutputTextDelta       = "response.output_text.delta"
	eventResponseRefusalDelta          = "response.refusal.delta"
	eventResponseFunctionCallArgsDelta = "response.function_call_arguments.delta"
	eventResponseFunctionCallArgsDone  = "response.function_call_arguments.done"
	eventResponseOutputItemAdded       = "response.output_item.added"
	eventResponseOutputItemDone        = "response.output_item.done"
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
	if err := m.client.Execute(ctx, http.MethodPost, "responses", params, &raw, streamOpts...); err != nil {
		if ctx.Err() == nil {
			emit(apiErrorResponse(err))
		}
		return
	}
	if raw == nil || raw.Body == nil {
		emit(apiErrorResponse(errNilStreamBody))
		return
	}
	defer raw.Body.Close()

	acc := &streamAccumulator{doneItems: make(map[int64]responses.ResponseOutputItemUnion)}
	decoder := ssestream.NewDecoder(raw)
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
			emit(apiErrorResponse(fmt.Errorf("openai/responses: decode stream event: %w", err)))
			return
		}
		if event.Type == "" {
			emit(apiErrorResponse(errors.New("openai/responses: stream event has no type")))
			return
		}
		m.runStreamCallback(ctx, &params, &event)
		terminal, keepGoing := handleStreamEvent(m.name, acc, &event, emit)
		if !keepGoing || terminal {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err := decoder.Err(); err != nil && !errors.Is(err, io.EOF) {
		emit(apiErrorResponse(fmt.Errorf("openai/responses: read stream: %w", err)))
		return
	}
	emit(apiErrorResponse(errors.New("openai/responses: stream ended before a terminal response event")))
}

var errNilStreamBody = errors.New("openai/responses: empty stream body")

type streamAccumulator struct {
	id        string
	created   int64
	doneItems map[int64]responses.ResponseOutputItemUnion
	// outputObserved means a non-empty preview or native output event was
	// seen. It is evidence only: it is not a transcript, a native item, or a
	// tool call.
	outputObserved bool
}

func handleStreamEvent(
	modelName string,
	acc *streamAccumulator,
	event *responses.ResponseStreamEventUnion,
	emit func(*model.Response) bool,
) (bool, bool) {
	switch event.Type {
	case eventResponseCreated, eventResponseInProgress:
		if event.Response.ID != "" {
			acc.id = event.Response.ID
			acc.created = createdUnix(&event.Response)
		}
		return false, true
	case eventResponseOutputTextDelta, eventResponseRefusalDelta:
		if event.Delta == "" {
			return false, true
		}
		acc.outputObserved = true
		return false, emit(projectPartial(modelName, acc, event.Delta, ""))
	case eventResponseReasoningSummaryDelta, eventResponseReasoningTextDelta:
		if event.Delta == "" {
			return false, true
		}
		acc.outputObserved = true
		return false, emit(projectPartial(modelName, acc, "", event.Delta))
	case eventResponseOutputItemDone:
		index := event.OutputIndex
		if index < 0 {
			return true, emit(apiErrorResponse(errors.New("openai/responses: output_item.done has a negative output_index")))
		}
		if _, exists := acc.doneItems[index]; exists {
			return true, emit(apiErrorResponse(fmt.Errorf("openai/responses: duplicate output_item.done at index %d", index)))
		}
		acc.doneItems[index] = event.Item
		return false, true
	case eventResponseCompleted, eventResponseIncomplete, eventResponseFailed, eventResponseCancelled, eventError:
		return handleTerminalStreamEvent(modelName, acc, event, emit)
	case eventResponseOutputItemAdded:
		// The item stays private until response.completed. Its presence still
		// shows that native output was produced.
		if outputItemObserved(event.Item) {
			acc.outputObserved = true
		}
		return false, true
	case eventResponseFunctionCallArgsDelta:
		if event.Delta != "" {
			acc.outputObserved = true
		}
		return false, true
	case eventResponseFunctionCallArgsDone:
		// Arguments stay private until response.completed supplies the complete
		// item. A call name, argument payload, or item ID still shows that a
		// function call was produced.
		if event.Name != "" || event.Arguments != "" || event.ItemID != "" {
			acc.outputObserved = true
		}
		return false, true
	default:
		// Non-terminal extension and keepalive events do not affect the projection.
		return false, true
	}
}

// handleTerminalStreamEvent projects a terminal stream event. A response is
// committed only for a valid completed snapshot.
func handleTerminalStreamEvent(
	modelName string,
	acc *streamAccumulator,
	event *responses.ResponseStreamEventUnion,
	emit func(*model.Response) bool,
) (bool, bool) {
	switch event.Type {
	case eventResponseCompleted:
		resp := event.Response
		if resp.Status != responses.ResponseStatusCompleted {
			return true, emit(responseStatusError(&resp))
		}
		// Done-item mismatches keep their existing errors. When no done item
		// was recorded, an empty snapshot cannot override output already seen.
		// The preview is not promoted into a message, native item, or tool call.
		if len(acc.doneItems) == 0 && len(resp.Output) == 0 && acc.outputObserved {
			return true, emit(apiErrorResponse(errors.New("openai/responses: response.completed output is empty after streamed output")))
		}
		output, err := completedStreamItems(acc.doneItems, resp.Output)
		if err != nil {
			return true, emit(apiErrorResponse(err))
		}
		resp.Output = output
		final, err := projectResponse(&resp, modelName)
		if err != nil {
			return true, emit(apiErrorResponse(err))
		}
		return true, emit(final)
	case eventResponseIncomplete, eventResponseFailed, eventResponseCancelled, eventError:
		if event.Response.Status != "" {
			return true, emit(responseStatusError(&event.Response))
		}
		message := event.Message
		if message == "" {
			message = "responses stream did not complete"
		}
		result := apiErrorResponse(errors.New(message))
		result.Error.Code = strPtr(event.Code)
		result.Error.Param = strPtr(event.Param)
		return true, emit(result)
	default:
		return false, true
	}
}

// completedStreamItems validates the terminal snapshot and selects replayable
// reasoning from output_item.done, as required by the streaming API. Opaque
// ciphertext can differ in the terminal response; every other field must agree.
func completedStreamItems(
	doneItems map[int64]responses.ResponseOutputItemUnion,
	finalItems []responses.ResponseOutputItemUnion,
) ([]responses.ResponseOutputItemUnion, error) {
	if len(doneItems) == 0 {
		return finalItems, nil
	}
	if len(doneItems) != len(finalItems) {
		return nil, fmt.Errorf("openai/responses: response.completed has %d output items, but output_item.done covered %d", len(finalItems), len(doneItems))
	}
	output := append([]responses.ResponseOutputItemUnion(nil), finalItems...)
	for index, done := range doneItems {
		if index < 0 || int64(len(finalItems)) <= index {
			return nil, fmt.Errorf("openai/responses: output_item.done index %d is outside response.completed output", index)
		}
		var doneValue, finalValue any
		if err := json.Unmarshal([]byte(done.RawJSON()), &doneValue); err != nil {
			return nil, fmt.Errorf("openai/responses: decode output_item.done at index %d: %w", index, err)
		}
		if err := json.Unmarshal([]byte(finalItems[index].RawJSON()), &finalValue); err != nil {
			return nil, fmt.Errorf("openai/responses: decode response.completed item %d: %w", index, err)
		}
		if done.Type == outputTypeReasoning && finalItems[index].Type == outputTypeReasoning {
			doneMap, _ := doneValue.(map[string]any)
			finalMap, _ := finalValue.(map[string]any)
			if encrypted, ok := doneMap["encrypted_content"].(string); ok && encrypted != "" {
				delete(doneMap, "encrypted_content")
				delete(finalMap, "encrypted_content")
				output[index] = done
			}
		}
		if !reflect.DeepEqual(doneValue, finalValue) {
			return nil, fmt.Errorf("openai/responses: output_item.done at index %d differs from response.completed", index)
		}
	}
	return output, nil
}

func outputItemObserved(item responses.ResponseOutputItemUnion) bool {
	return item.Type != "" || item.ID != "" || item.CallID != "" || item.Name != ""
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
