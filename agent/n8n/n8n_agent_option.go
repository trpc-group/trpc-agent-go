//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package n8n

import (
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// Option configures the N8nAgent.
type Option func(*N8nAgent)

// WithWebhookURL sets the webhook URL of the n8n service.
func WithWebhookURL(url string) Option {
	return func(a *N8nAgent) {
		a.webhookURL = url
	}
}

// WithName sets the name of the agent.
func WithName(name string) Option {
	return func(a *N8nAgent) {
		a.name = name
	}
}

// WithDescription sets the agent description.
func WithDescription(description string) Option {
	return func(a *N8nAgent) {
		a.description = description
	}
}

// WithAuthType sets the authentication type for n8n webhook requests.
func WithAuthType(authType AuthType) Option {
	return func(a *N8nAgent) {
		a.authType = authType
	}
}

// WithAuthConfig sets the authentication configuration for n8n webhook requests.
func WithAuthConfig(config *AuthConfig) Option {
	return func(a *N8nAgent) {
		a.authConfig = config
	}
}

// WithCustomRequestConverter sets a custom request converter.
func WithCustomRequestConverter(converter RequestConverter) Option {
	return func(a *N8nAgent) {
		a.requestConverter = converter
	}
}

// WithCustomResponseConverter sets a custom response converter.
func WithCustomResponseConverter(converter ResponseConverter) Option {
	return func(a *N8nAgent) {
		a.responseConverter = converter
	}
}

// WithEnableStreaming explicitly controls whether to use streaming protocol.
// If not set (nil), the agent defaults to non-streaming.
// This option can be overridden per-run via RunOptions.Stream.
func WithEnableStreaming(enable bool) Option {
	return func(a *N8nAgent) {
		a.enableStreaming = &enable
	}
}

// WithStreamingChannelBufSize sets the buffer size of the streaming event channel.
func WithStreamingChannelBufSize(size int) Option {
	return func(a *N8nAgent) {
		a.streamingBufSize = size
	}
}

// WithStreamingRespHandler sets a handler function to process streaming responses.
func WithStreamingRespHandler(handler StreamingRespHandler) Option {
	return func(a *N8nAgent) {
		a.streamingRespHandler = handler
	}
}

// WithHTTPClient sets a custom HTTP client for the agent.
func WithHTTPClient(client *http.Client) Option {
	return func(a *N8nAgent) {
		a.httpClient = client
	}
}

// WithGetHTTPClientFunc sets a custom function to create an HTTP client for each invocation.
func WithGetHTTPClientFunc(fn func(*agent.Invocation) (*http.Client, error)) Option {
	return func(a *N8nAgent) {
		a.getHTTPClientFunc = fn
	}
}

// WithTransferStateKey appends keys from session state to transfer to the n8n request inputs.
func WithTransferStateKey(key ...string) Option {
	return func(a *N8nAgent) {
		a.transferStateKey = append(a.transferStateKey, key...)
	}
}
