//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metrics defines metric name constants following OpenTelemetry semantic conventions.
package metrics

const (
	// KeyGenAITokenType represents the type of token.
	KeyGenAITokenType = "gen_ai.token.type" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoInputTokenType represents the type of input token.
	KeyTRPCAgentGoInputTokenType = "input" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoOutputTokenType represents the type of output token.
	KeyTRPCAgentGoOutputTokenType = "output" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoInputCachedTokenType represents the cached portion of input(prompt) tokens.
	KeyTRPCAgentGoInputCachedTokenType = "input_cached" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoInputCacheReadTokenType represents tokens read from prompt cache (Anthropic).
	KeyTRPCAgentGoInputCacheReadTokenType = "input_cache_read" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoInputCacheCreationTokenType represents tokens used to create prompt cache (Anthropic).
	KeyTRPCAgentGoInputCacheCreationTokenType = "input_cache_creation" // #nosec G101 - this is a metric key name, not a credential.
	// KeyTRPCAgentGoStream represents the stream of the response.
	KeyTRPCAgentGoStream = "trpc_agent_go.is_stream" // #nosec G101 - this is a metric key name, not a credential.
	// KeyMetricName represents the name of the metric.
	KeyMetricName = "metric.name"

	/////////////// client ////////////////////////

	// MetricGenAIClientTokenUsage represents the usage of client token.
	MetricGenAIClientTokenUsage = "gen_ai.client.token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientInputTokenUsage represents the usage of input token.
	MetricTRPCAgentGoClientInputTokenUsage = "trpc_agent_go.client.input_token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientOutputTokenUsage represents the usage of output token.
	MetricTRPCAgentGoClientOutputTokenUsage = "trpc_agent_go.client.output_token.usage" // #nosec G101 - this is a metric key name, not a credential.

	// MetricGenAIClientOperationDuration represents the duration of client operation.
	MetricGenAIClientOperationDuration = "gen_ai.client.operation.duration"
	// MetricTRPCAgentGoClientTimeToFirstToken represents the time to first token for client.
	// Note: This metric will be reported alongside MetricGenAIServerTimeToFirstToken with the same value.
	MetricTRPCAgentGoClientTimeToFirstToken = "trpc_agent_go.client.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientTimePerOutputToken represents the time per output token for client.
	MetricTRPCAgentGoClientTimePerOutputToken = "trpc_agent_go.client.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientOutputTokenPerTime represents the output token per time for client.
	MetricTRPCAgentGoClientOutputTokenPerTime = "trpc_agent_go.client.output_token_per_time" // #nosec G101 - this is a metric key name, not a credential.

	// MetricTRPCAgentGoClientRequestCnt represents the request count for client.
	MetricTRPCAgentGoClientRequestCnt = "trpc_agent_go.client.request_cnt"

	////////////////////////// server ////////////////////////

	// MetricGenAIServerRequestDuration represents the duration of server request.
	MetricGenAIServerRequestDuration = "gen_ai.server.request.duration"
	// MetricGenAIServerTimeToFirstToken represents the time to first token for server.
	MetricGenAIServerTimeToFirstToken = "gen_ai.server.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricGenAIServerTimePerOutputToken represents the time per output token for server.
	MetricGenAIServerTimePerOutputToken = "gen_ai.server.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.

	////////////////////////// meters ////////////////////////

	// MeterNameChat is the meter name for chat operations.
	MeterNameChat = "trpc_agent_go.internal.chat"
	// MeterNameExecuteTool is the meter name for tool execution operations.
	MeterNameExecuteTool = "trpc_agent_go.internal.execute_tool"
	// MeterNameInvokeAgent is the meter name for invoke agent operations.
	MeterNameInvokeAgent = "trpc_agent_go.internal.invoke_agent"
)
