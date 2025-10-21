package telemetry

const (
	// MetricGenAIClientTokenUsage is total token for openai.
	MetricGenAIClientTokenUsage = "gen_ai.client.token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientInputTokenUsage is prompt token for openai.
	MetricTRPCAgentGoClientInputTokenUsage = "trpc_agent_go.client.input_token.usage" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientOutputTokenUsage is completion token for openai.
	MetricTRPCAgentGoClientOutputTokenUsage = "trpc_agent_go.client.input_token.usage" // #nosec G101 - this is a metric key name, not a credential.

	MetricGenAIClientOperationDuration        = "gen_ai.client.operation.duration"
	MetricTRPCAgentGoClientTimeToFirstToken   = "trpc_agent_go.client.time_to_first_token"   // #nosec G101 - this is a metric key name, not a credential.
	MetricTRPCAgentGoClientTimePerOutputToken = "trpc_agent_go.client.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.

	MetricGenAIServerRequestDuration    = "gen_ai.server.request.duration"
	MetricGenAIServerTimeToFirstToken   = "gen_ai.server.time_to_first_token"   // #nosec G101 - this is a metric key name, not a credential.
	MetricGenAIServerTimePerOutputToken = "gen_ai.server.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.
)
