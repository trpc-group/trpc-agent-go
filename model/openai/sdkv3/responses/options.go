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
	"github.com/openai/openai-go/v3/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultChannelBufferSize = 256

// Option configures a Responses model.
type Option func(*options)

// CrossModelReasoningPolicy controls how Responses reasoning items from a
// different configured model name are replayed. Model names are compared
// exactly as configured.
type CrossModelReasoningPolicy int

const (
	// CrossModelReasoningPolicyReplay replays reasoning items intact. It is the
	// zero value and the default.
	CrossModelReasoningPolicyReplay CrossModelReasoningPolicy = iota
	// CrossModelReasoningPolicyOmitEncryptedContent omits the top-level
	// encrypted_content field from reasoning items produced by another model.
	CrossModelReasoningPolicyOmitEncryptedContent
)

type options struct {
	apiKey                    string
	baseURL                   string
	store                     bool
	crossModelReasoningPolicy CrossModelReasoningPolicy
	channelBufferSize         int
	httpClientOptions         []model.HTTPClientOption
	openaiOptions             []option.RequestOption
	extraFields               map[string]any
	toolSearch                toolSearchConfig
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

// WithCrossModelReasoningPolicy configures replay of reasoning items produced
// by a different configured Responses model name. The default is
// CrossModelReasoningPolicyReplay. Omission affects only the outgoing request;
// stored history, same-model items, and the request for new encrypted reasoning
// are preserved. When omitting, the target must accept reasoning items without
// ciphertext. A target that looks up the retained item ID may reject the request
// if the original response used store=false (the default). The last option wins.
// An unsupported policy makes GenerateContent return an error before any HTTP
// request is sent.
func WithCrossModelReasoningPolicy(policy CrossModelReasoningPolicy) Option {
	return func(o *options) { o.crossModelReasoningPolicy = policy }
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

// WithHostedToolSearch enables hosted tool search. Request tools that report
// tool.ShouldDefer, for example through deferred.Tool or deferred.ToolSet
// in the tool/deferred package, are declared with defer_loading, and the
// tool_search tool is added. Members of a ToolSet that has deferred members
// are grouped in one namespace named after the ToolSet, described by
// tool.ToolSetDescriber when the ToolSet implements it. The API searches and
// loads deferred tools within the same response, and function calls to
// loaded tools are mapped back to the registered tools and run as ordinary
// tool calls.
//
// Requests without deferred tools or tool search history are unchanged. Tool
// search requires a model that supports it. WithHostedToolSearch and
// WithClientToolSearch are mutually exclusive; the later option wins.
func WithHostedToolSearch() Option {
	return func(o *options) {
		o.toolSearch = toolSearchConfig{mode: toolSearchHosted}
	}
}

// WithClientToolSearch enables client-executed tool search even when the
// agent has no deferred tools. The handler can select deferred
// tools, replace their implementations, or discover tools in its own catalog.
// LLMAgent records the returned tools' declarations in tool_search_output
// before the next model step and borrows their execution bindings for this
// run. After a restart, an expired binding can be searched for again.
//
// Built-in search handlers are available in the responses/toolsearch package.
//
// Client tool search requires a flow that completes provider actions, such as
// LLMAgent; Graph LLM nodes report an error for a search call. GenerateContent
// returns an error when handler is nil. WithHostedToolSearch and
// WithClientToolSearch are mutually exclusive; the later option wins.
func WithClientToolSearch(handler ToolSearchHandler) Option {
	return func(o *options) {
		o.toolSearch = toolSearchConfig{mode: toolSearchClient, handler: handler}
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
