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
		return false, emit(projectPartial(modelName, acc, event.Delta, ""))
	case eventResponseReasoningSummaryDelta, eventResponseReasoningTextDelta:
		if event.Delta == "" {
			return false, true
		}
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
	case eventResponseCompleted:
		resp := event.Response
		if resp.Status != responses.ResponseStatusCompleted {
			return true, emit(responseStatusError(&resp))
		}
		if err := validateCompletedStreamItems(acc.doneItems, resp.Output); err != nil {
			return true, emit(apiErrorResponse(err))
		}
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
		return true, emit(apiErrorResponse(errors.New(message)))
	case eventResponseOutputItemAdded,
		eventResponseFunctionCallArgsDelta,
		eventResponseFunctionCallArgsDone:
		// Function call arguments stay private until response.completed supplies
		// the complete item and its final status.
		return false, true
	default:
		// Non-terminal extension and keepalive events do not affect the projection.
		return false, true
	}
}

func validateCompletedStreamItems(
	doneItems map[int64]responses.ResponseOutputItemUnion,
	finalItems []responses.ResponseOutputItemUnion,
) error {
	if len(doneItems) == 0 {
		return nil
	}
	if len(doneItems) != len(finalItems) {
		return fmt.Errorf("openai/responses: response.completed has %d output items, but output_item.done covered %d", len(finalItems), len(doneItems))
	}
	for index, done := range doneItems {
		if index < 0 || int64(len(finalItems)) <= index {
			return fmt.Errorf("openai/responses: output_item.done index %d is outside response.completed output", index)
		}
		var doneValue, finalValue any
		if err := json.Unmarshal([]byte(done.RawJSON()), &doneValue); err != nil {
			return fmt.Errorf("openai/responses: decode output_item.done at index %d: %w", index, err)
		}
		if err := json.Unmarshal([]byte(finalItems[index].RawJSON()), &finalValue); err != nil {
			return fmt.Errorf("openai/responses: decode response.completed item %d: %w", index, err)
		}
		if !reflect.DeepEqual(doneValue, finalValue) {
			return fmt.Errorf("openai/responses: output_item.done at index %d differs from response.completed", index)
		}
	}
	return nil
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
