package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (

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
	MetricTRPCAgentGoClientTimeToFirstToken = "trpc_agent_go.client.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.
	// MetricTRPCAgentGoClientTimePerOutputToken represents the time per output token for client.
	MetricTRPCAgentGoClientTimePerOutputToken = "trpc_agent_go.client.time_per_output_token" // #nosec G101 - this is a metric key name, not a credential.

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
)

var (
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()

	ChatMeter                                     metric.Meter            = MeterProvider.Meter(MeterNameChat)
	ChatMetricTRPCAgentGoClientRequestCnt         metric.Int64Counter     = noop.Int64Counter{}
	ChatMetricGenAIClientTokenUsage               metric.Int64Histogram   = noop.Int64Histogram{}
	ChatMetricGenAIClientOperationDuration        metric.Float64Histogram = noop.Float64Histogram{}
	ChatMetricTRPCAgentGoClientTimeToFirstToken   metric.Float64Histogram = noop.Float64Histogram{}
	ChatMetricTRPCAgentGoClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}

	ExecuteToolMeter                              metric.Meter            = MeterProvider.Meter(MeterNameExecuteTool)
	ExecuteToolMetricTRPCAgentGoClientRequestCnt  metric.Int64Counter     = noop.Int64Counter{}
	ExecuteToolMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
)

func IncChatRequestCnt(ctx context.Context, modelName string, sess *session.Session) {
	appName, userID, sessionID := sessionInfo(sess)
	ChatMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1,
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationChat),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}

func RecordChatTimePerOutputTokenDuration(ctx context.Context, modelName string, sess *session.Session, duration time.Duration) {
	appName, userID, sessionID := sessionInfo(sess)
	ChatMetricTRPCAgentGoClientTimePerOutputToken.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationChat),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}

func RecordChatInputTokenUsage(ctx context.Context, modelName string, sess *session.Session, usage int64) {
	recordChatTokenUsage(ctx, modelName, sess, usage, "input")
}

func RecordChatOutputTokenUsage(ctx context.Context, modelName string, sess *session.Session, usage int64) {
	recordChatTokenUsage(ctx, modelName, sess, usage, "output")
}

func recordChatTokenUsage(ctx context.Context, modelName string, sess *session.Session, usage int64, tokenType string) {
	appName, userID, sessionID := sessionInfo(sess)
	ChatMetricGenAIClientTokenUsage.Record(ctx, usage,
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationChat),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
			attribute.String(KeyGenAITokenType, tokenType),
		))
}

func RecordChatTimeToFirstTokenDuration(ctx context.Context, modelName string, sess *session.Session, duration time.Duration) {
	appName, userID, sessionID := sessionInfo(sess)
	ChatMetricTRPCAgentGoClientTimeToFirstToken.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationChat),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}

func RecordChatRequestDuration(ctx context.Context, modelName string, sess *session.Session, duration time.Duration) {
	appName, userID, sessionID := sessionInfo(sess)
	ChatMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationChat),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}

func sessionInfo(sess *session.Session) (appName, userID, sessionID string) {
	if sess != nil {
		return sess.AppName, sess.UserID, sess.ID
	}
	return "", "", ""
}
