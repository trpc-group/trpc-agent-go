package telemetry

const (
	// MetricGenAIClientTokenUsage represents the usage of client token.
	MetricGenAIClientTokenUsage = "gen_ai.client.token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientInputTokenUsage represents the usage of input token.
	MetricTRPCAgentGoClientInputTokenUsage = "trpc_agent_go.client.input_token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientOutputTokenUsage represents the usage of output token.
	MetricTRPCAgentGoClientOutputTokenUsage = "trpc_agent_go.client.output_token.usage" // #nosec G101 - this is a metric key name, not a credential.

	// MetricGenAIClientOperationDuration represents the duration of client operation.
	MetricGenAIClientOperationDuration = "gen_ai.client.operation.duration"
	// MetricTRPCAgentGoClientTimeToFirstToken represents the time to first token for client.
	MetricTRPCAgentGoClientTimeToFirstToken = "trpc_agent_go.client.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientTimePerOutputToken represents the time per output token for client.
	MetricTRPCAgentGoClientTimePerOutputToken = "trpc_agent_go.client.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.

	// MetricGenAIServerRequestDuration represents the duration of server request.
	MetricGenAIServerRequestDuration = "gen_ai.server.request.duration"
	// MetricGenAIServerTimeToFirstToken represents the time to first token for server.
	MetricGenAIServerTimeToFirstToken = "gen_ai.server.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricGenAIServerTimePerOutputToken represents the time per output token for server.
	MetricGenAIServerTimePerOutputToken = "gen_ai.server.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.
)
