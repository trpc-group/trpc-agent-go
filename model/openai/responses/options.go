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
	"context"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultChannelBufferSize = 256

// RequestCallbackFunc is invoked with the Responses request params
// before the HTTP call. Changes to managed history, model, storage, tool,
// include, and continuation fields are rejected. Include is already the merged
// list that will be sent. Callbacks must not retain mutable params.
type RequestCallbackFunc func(ctx context.Context, params *responses.ResponseNewParams)

// ResponseCallbackFunc observes the non-stream Responses object before mapping.
// The callback must treat its arguments as read-only and must not retain them.
type ResponseCallbackFunc func(
	ctx context.Context,
	params *responses.ResponseNewParams,
	resp *responses.Response,
)

// StreamEventCallbackFunc observes each parsed stream event before mapping.
// The callback must treat its arguments as read-only and must not retain them.
type StreamEventCallbackFunc func(
	ctx context.Context,
	params *responses.ResponseNewParams,
	event *responses.ResponseStreamEventUnion,
)

// Option configures a Responses model.
type Option func(*options)

type options struct {
	apiKey            string
	baseURL           string
	store             bool
	channelBufferSize int
	httpClientOptions []model.HTTPClientOption
	openaiOptions     []option.RequestOption
	extraFields       map[string]any
	requestCallback   RequestCallbackFunc
	responseCallback  ResponseCallbackFunc
	streamCallback    StreamEventCallbackFunc
}

func defaultOptions() options {
	return options{
		store:             false,
		channelBufferSize: defaultChannelBufferSize,
	}
}

// WithAPIKey sets the API key.
func WithAPIKey(key string) Option {
	return func(o *options) {
		o.apiKey = key
	}
}

// WithBaseURL sets the API base URL. The SDK appends "responses"
// relative to this value.
func WithBaseURL(url string) Option {
	return func(o *options) {
		o.baseURL = url
	}
}

// WithStore controls whether the provider stores the response.
// The adapter defaults to false.
func WithStore(store bool) Option {
	return func(o *options) {
		o.store = store
	}
}

// WithChannelBufferSize sets the GenerateContent channel buffer. Non-positive
// values use the default of 256.
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithHTTPClientOptions sets HTTP client options.
func WithHTTPClientOptions(httpOpts ...model.HTTPClientOption) Option {
	return func(o *options) {
		o.httpClientOptions = append(o.httpClientOptions, httpOpts...)
	}
}

// WithOpenAIOptions appends openai-go/v3 request options such as
// middleware. These are low-level SDK escape hatches; callers must not override
// managed input, tools, storage, or continuation fields through these options.
func WithOpenAIOptions(opts ...option.RequestOption) Option {
	return func(o *options) {
		o.openaiOptions = append(o.openaiOptions, opts...)
	}
}

// WithExtraFields merges extra JSON fields into every request body.
// Keys may use ordinary dot-separated JSON paths, so "reasoning.effort" is
// valid. Colon prefixes and backslash escapes are rejected.
// Managed fields such as input, tools, and previous_response_id are rejected.
//
// The exact key "include" is additive. A nil value or empty list adds no names.
// Any other value must be an array of nonempty strings: []string, []any
// containing strings, or another slice or array whose elements are strings.
// A Go string or []byte is not an include list. Names are copied; the caller's
// maps and arrays are left unchanged. The sent list is
// reasoning.encrypted_content, then names from this option, then names from
// Request.ExtraFields, keeping the first occurrence of each name. These fields
// cannot remove reasoning.encrypted_content. Nested, indexed, and escaped
// include paths are rejected. Forwarding an include name does not add logprobs
// mapping, hosted tools, or server-managed conversation state.
func WithExtraFields(fields map[string]any) Option {
	return func(o *options) {
		if o.extraFields == nil {
			o.extraFields = make(map[string]any, len(fields))
		}
		for k, v := range fields {
			o.extraFields[k] = v
		}
	}
}

// WithRequestCallback sets a callback invoked before the HTTP call.
func WithRequestCallback(cb RequestCallbackFunc) Option {
	return func(o *options) {
		o.requestCallback = cb
	}
}

// WithResponseCallback sets a callback invoked for non-stream responses.
func WithResponseCallback(cb ResponseCallbackFunc) Option {
	return func(o *options) {
		o.responseCallback = cb
	}
}

// WithStreamEventCallback sets a callback invoked for each stream event.
func WithStreamEventCallback(cb StreamEventCallbackFunc) Option {
	return func(o *options) {
		o.streamCallback = cb
	}
}
