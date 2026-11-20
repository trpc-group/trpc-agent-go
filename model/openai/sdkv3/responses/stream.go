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

	acc := &streamAccumulator{}
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
	reasoning map[string]*reasoningCandidates
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
		if outputItemObserved(event.Item) {
			acc.outputObserved = true
		}
		acc.recordReasoning(event)
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
		var legacy struct {
			Name string `json:"name"`
		}
		// New SDK versions no longer declare name on this event. Preserve
		// detection of output from compatible services that still send it.
		_ = json.Unmarshal([]byte(event.RawJSON()), &legacy)
		if legacy.Name != "" || event.Arguments != "" || event.ItemID != "" {
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
		if len(resp.Output) == 0 && acc.outputObserved {
			return true, emit(apiErrorResponse(errors.New("openai/responses: response.completed output is empty after streamed output")))
		}
		acc.selectReplayReasoning(resp.Output)
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

// A nil entry marks an ambiguous match. Slots are used only to disambiguate
// providers that reuse a reasoning ID.
type reasoningCandidates struct {
	unique  *responses.ResponseOutputItemUnion
	byIndex map[int64]*responses.ResponseOutputItemUnion
}

func validReasoning(item *responses.ResponseOutputItemUnion) bool {
	var native nativeItem
	return json.Unmarshal([]byte(item.RawJSON()), &native) == nil && validateNativeItem(native, true) == nil
}

func (acc *streamAccumulator) recordReasoning(event *responses.ResponseStreamEventUnion) {
	if event.Item.Type != outputTypeReasoning || event.Item.EncryptedContent == "" ||
		!event.Item.JSON.EncryptedContent.Valid() || !validReasoning(&event.Item) {
		return
	}
	if acc.reasoning == nil {
		acc.reasoning = make(map[string]*reasoningCandidates)
	}
	item := event.Item
	candidates := acc.reasoning[item.ID]
	if candidates == nil {
		candidates = &reasoningCandidates{unique: &item, byIndex: make(map[int64]*responses.ResponseOutputItemUnion)}
		acc.reasoning[item.ID] = candidates
	} else {
		candidates.unique = nil
	}
	if !event.JSON.OutputIndex.Valid() || event.OutputIndex < 0 {
		return
	}
	index := event.OutputIndex
	if _, exists := candidates.byIndex[index]; exists {
		candidates.byIndex[index] = nil
	} else {
		candidates.byIndex[index] = &item
	}
}

// selectReplayReasoning keeps the terminal order and replaces only complete,
// identifiable reasoning items with the provider's replayable done items.
func (acc *streamAccumulator) selectReplayReasoning(output []responses.ResponseOutputItemUnion) {
	if len(acc.reasoning) == 0 {
		return
	}
	counts := make(map[string]int)
	for i := range output {
		if output[i].Type == outputTypeReasoning {
			counts[output[i].ID]++
		}
	}
	for i := range output {
		final := &output[i]
		candidates := acc.reasoning[final.ID]
		if final.Type != outputTypeReasoning || candidates == nil || !validReasoning(final) {
			continue
		}
		if counts[final.ID] == 1 && candidates.unique != nil {
			*final = *candidates.unique
			continue
		}
		if done := candidates.byIndex[int64(i)]; done != nil && sameReasoningExceptCiphertext(done, final) {
			*final = *done
		}
	}
}

func sameReasoningExceptCiphertext(a, b *responses.ResponseOutputItemUnion) bool {
	fields := func(item *responses.ResponseOutputItemUnion) map[string]any {
		var values map[string]any
		decoder := json.NewDecoder(strings.NewReader(item.RawJSON()))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil
		}
		delete(values, "encrypted_content")
		return values
	}
	left, right := fields(a), fields(b)
	return left != nil && right != nil && reflect.DeepEqual(left, right)
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
