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
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

// Model implements model.Model using the official OpenAI Responses API.
// It supports concurrent requests; configured callbacks must be concurrency-safe.
type Model struct {
	client            openai.Client
	name              string
	store             bool
	channelBufferSize int
	extraFields       map[string]any
	requestCallback   RequestCallbackFunc
	responseCallback  ResponseCallbackFunc
	streamCallback    StreamEventCallbackFunc
}

// New creates a Responses API adapter with client-managed history and
// store=false by default. It reads OPENAI_API_KEY when WithAPIKey is omitted.
func New(name string, opts ...Option) *Model {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}
	if o.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(o.baseURL))
	}
	clientOpts = append(clientOpts, option.WithHTTPClient(
		model.DefaultNewHTTPClient(o.httpClientOptions...),
	))
	clientOpts = append(clientOpts, o.openaiOptions...)
	return &Model{
		client:            openai.NewClient(clientOpts...),
		name:              name,
		store:             o.store,
		channelBufferSize: o.channelBufferSize,
		extraFields:       o.extraFields,
		requestCallback:   o.requestCallback,
		responseCallback:  o.responseCallback,
		streamCallback:    o.streamCallback,
	}
}

// Info implements model.Model.
func (m *Model) Info() model.Info {
	return model.Info{Name: m.name, ReplayProtocol: responseProvider}
}

// GenerateContent implements model.Model.
// The caller must keep draining the channel or cancel ctx, and must not mutate
// request until the channel closes. Only completed responses contain tool calls.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	params, err := m.buildParams(request)
	if err != nil {
		return nil, err
	}
	reqOpts := m.requestOptions(request)
	paramsToEncode := params
	if m.requestCallback == nil {
		// History is already validated by buildParams. Keep synchronous encoding
		// errors for the remaining fields without serializing that history again.
		paramsToEncode.Input = responses.ResponseNewParamsInputUnion{}
	}
	beforeCallback, err := json.Marshal(paramsToEncode)
	if err != nil {
		return nil, fmt.Errorf("openai/responses: snapshot request parameters: %w", err)
	}
	if m.requestCallback != nil {
		m.runRequestCallback(ctx, &params)
		if err := validateCallbackParams(beforeCallback, params); err != nil {
			return nil, err
		}
	}
	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)
		emit := func(resp *model.Response) bool {
			if resp == nil {
				return true
			}
			select {
			case responseChan <- resp:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if request.Stream {
			m.handleStreaming(ctx, params, reqOpts, emit)
			return
		}
		m.handleNonStreaming(ctx, params, reqOpts, emit)
	}()
	return responseChan, nil
}

func (m *Model) handleNonStreaming(
	ctx context.Context,
	params responses.ResponseNewParams,
	opts []option.RequestOption,
	emit func(*model.Response) bool,
) {
	resp, err := m.client.Responses.New(ctx, params, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		emit(apiErrorResponse(err))
		return
	}
	if resp == nil {
		emit(apiErrorResponse(errors.New("openai/responses: empty response body")))
		return
	}
	m.runResponseCallback(ctx, &params, resp)
	if errResp := responseAPIError(resp); errResp != nil {
		emit(errResp)
		return
	}
	if resp.Status != responses.ResponseStatusCompleted {
		emit(responseStatusError(resp))
		return
	}
	projected, err := projectResponse(resp, m.name)
	if err != nil {
		emit(apiErrorResponse(err))
		return
	}
	emit(projected)
}

func (m *Model) requestOptions(request *model.Request) []option.RequestOption {
	var opts []option.RequestOption
	for key, value := range m.extraFields {
		if skipJSONExtraField(key) {
			continue
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	if request == nil {
		return opts
	}
	for key, value := range request.ExtraFields {
		if skipJSONExtraField(key) {
			continue
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	for key, value := range request.Headers {
		opts = append(opts, option.WithHeader(key, value))
	}
	return opts
}

// skipJSONExtraField reports keys consumed while building typed parameters.
// Applying them again with sjson would replace those typed values. In
// particular, a raw include value would discard the merged include list.
func skipJSONExtraField(key string) bool {
	return key == "tool_choice" || key == extraFieldInclude
}

func (m *Model) runRequestCallback(ctx context.Context, params *responses.ResponseNewParams) {
	if m.requestCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "responses request callback")
	m.requestCallback(ctx, params)
}

func validateCallbackParams(before []byte, after responses.ResponseNewParams) error {
	var oldParams, newParams map[string]json.RawMessage
	if err := json.Unmarshal(before, &oldParams); err != nil {
		return fmt.Errorf("openai/responses: decode request snapshot: %w", err)
	}
	encoded, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("openai/responses: encode callback request: %w", err)
	}
	if err := json.Unmarshal(encoded, &newParams); err != nil {
		return fmt.Errorf("openai/responses: decode callback request: %w", err)
	}
	protected := []string{
		"model", "input", "store", "include", "tools",
		"background", "previous_response_id", "conversation",
	}
	for _, key := range protected {
		if !bytes.Equal(bytes.TrimSpace(oldParams[key]), bytes.TrimSpace(newParams[key])) {
			return fmt.Errorf("openai/responses: request callback cannot change managed field %q", key)
		}
	}
	return nil
}

func (m *Model) runResponseCallback(
	ctx context.Context,
	params *responses.ResponseNewParams,
	resp *responses.Response,
) {
	if m.responseCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "responses response callback")
	m.responseCallback(ctx, params, resp)
}

func (m *Model) runStreamCallback(
	ctx context.Context,
	params *responses.ResponseNewParams,
	event *responses.ResponseStreamEventUnion,
) {
	if m.streamCallback == nil || event == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "responses stream callback")
	m.streamCallback(ctx, params, event)
}

func apiErrorResponse(err error) *model.Response {
	respErr := &model.ResponseError{
		Message: err.Error(),
		Type:    model.ErrorTypeAPIError,
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		respErr.Message = apiErr.Message
		if apiErr.Type != "" {
			respErr.Type = apiErr.Type
		}
		if apiErr.Code != "" {
			code := apiErr.Code
			respErr.Code = &code
		}
		if apiErr.Param != "" {
			param := apiErr.Param
			respErr.Param = &param
		}
	}
	return &model.Response{
		Object:    model.ObjectTypeError,
		Error:     respErr,
		Timestamp: time.Now(),
		Done:      true,
	}
}

func responseAPIError(resp *responses.Response) *model.Response {
	if resp == nil || resp.Error.Message == "" {
		return nil
	}
	code := string(resp.Error.Code)
	return &model.Response{
		ID:        resp.ID,
		Object:    model.ObjectTypeError,
		Created:   createdUnix(resp),
		Timestamp: time.Now(),
		Done:      true,
		Error: &model.ResponseError{
			Message: resp.Error.Message,
			Type:    model.ErrorTypeAPIError,
			Code:    strPtr(code),
		},
	}
}

func responseStatusError(resp *responses.Response) *model.Response {
	message := "responses request did not complete"
	if resp != nil {
		switch resp.Status {
		case responses.ResponseStatusIncomplete:
			message = "responses request was incomplete"
			if resp.IncompleteDetails.Reason != "" {
				message += ": " + string(resp.IncompleteDetails.Reason)
			}
		case responses.ResponseStatusCancelled:
			message = "responses request was cancelled"
		case responses.ResponseStatusFailed:
			message = "responses request failed"
		default:
			message += ": status " + string(resp.Status)
		}
		if resp.Error.Message != "" {
			message = resp.Error.Message
		}
	}
	result := apiErrorResponse(errors.New(message))
	if resp != nil {
		result.ID = resp.ID
		result.Created = createdUnix(resp)
		result.Model = string(resp.Model)
		result.Usage = mapUsage(resp)
		result.Error.Code = strPtr(string(resp.Error.Code))
	}
	return result
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ model.Model = (*Model)(nil)
