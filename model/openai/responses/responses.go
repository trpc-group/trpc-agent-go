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
	"context"
	"errors"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

// Model implements model.Model using the official OpenAI Responses API.
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

// New creates a Responses API adapter. The root trpc-agent-go module is
// unchanged; this type lives in a nested Go 1.25 module.
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
	return model.Info{Name: m.name}
}

// GenerateContent implements model.Model.
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
	m.runRequestCallback(ctx, &params)
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
	m.runResponseCallback(ctx, &params, resp)
	if errResp := responseAPIError(resp); errResp != nil {
		emit(errResp)
		return
	}
	emit(projectResponse(resp.ID, m.name, createdUnix(resp), resp, false, true, "", ""))
}

func (m *Model) requestOptions(request *model.Request) []option.RequestOption {
	var opts []option.RequestOption
	for key, value := range m.extraFields {
		if key == "tool_choice" {
			continue
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	if request == nil {
		return opts
	}
	for key, value := range request.ExtraFields {
		if key == "tool_choice" {
			continue
		}
		opts = append(opts, option.WithJSONSet(key, value))
	}
	for key, value := range request.Headers {
		opts = append(opts, option.WithHeader(key, value))
	}
	return opts
}

func (m *Model) runRequestCallback(ctx context.Context, params *responses.ResponseNewParams) {
	if m.requestCallback == nil {
		return
	}
	defer imodel.RecoverCallbackPanic(ctx, "responses request callback")
	m.requestCallback(ctx, params)
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

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ model.Model = (*Model)(nil)
