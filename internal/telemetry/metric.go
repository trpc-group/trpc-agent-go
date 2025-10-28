package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// MeterProvider is the global OpenTelemetry meter provider used for creating meters.
	// It defaults to a no-op implementation and should be initialized with a real provider.
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()

	// ChatMeter is the meter used for recording chat-related metrics.
	ChatMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameChat)

	// ChatMetricTRPCAgentGoClientRequestCnt records the number of chat requests made.
	ChatMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}

	// ChatMetricGenAIClientTokenUsage records the distribution of token usage (both input and output tokens).
	ChatMetricGenAIClientTokenUsage metric.Int64Histogram = noop.Int64Histogram{}

	// ChatMetricGenAIClientOperationDuration records the distribution of total chat operation durations in seconds.
	ChatMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}

	// ChatMetricTRPCAgentGoClientTimeToFirstToken records the distribution of time to first token latency in seconds.
	// This measures the time from request start until the first token is received.
	ChatMetricTRPCAgentGoClientTimeToFirstToken metric.Float64Histogram = noop.Float64Histogram{}

	// ChatMetricTRPCAgentGoClientTimePerOutputToken records the distribution of average time per output token in seconds.
	// This metric measures the decode phase performance by calculating (total_duration - time_to_first_token) / (output_tokens - first_token_count).
	ChatMetricTRPCAgentGoClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}
)

// IncChatRequestCnt increments the chat request counter by 1 with the provided model name and session attributes.
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

// RecordChatTimePerOutputTokenDuration records the average time per output token for a chat operation.
// The duration represents the time spent per token during the decode phase of LLM inference.
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

// RecordChatInputTokenUsage records the number of input (prompt) tokens used in a chat operation.
func RecordChatInputTokenUsage(ctx context.Context, modelName string, sess *session.Session, usage int64) {
	recordChatTokenUsage(ctx, modelName, sess, usage, metrics.KeyTRPCAgentGoInputTokenType)
}

// RecordChatOutputTokenUsage records the number of output (completion) tokens generated in a chat operation.
func RecordChatOutputTokenUsage(ctx context.Context, modelName string, sess *session.Session, usage int64) {
	recordChatTokenUsage(ctx, modelName, sess, usage, metrics.KeyTRPCAgentGoOutputTokenType)
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

// RecordChatTimeToFirstTokenDuration records the time taken from request start until the first token is received.
// This metric is important for measuring the prefill/prompt processing latency of LLM inference.
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

// RecordChatRequestDuration records the total duration of a chat request from start to completion.
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

var (
	// ExecuteToolMeter is the meter used for recording tool execution metrics.
	ExecuteToolMeter = MeterProvider.Meter(metrics.MeterNameExecuteTool)

	// ExecuteToolMetricTRPCAgentGoClientRequestCnt records the number of tool execution requests made.
	ExecuteToolMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}

	// ExecuteToolMetricGenAIClientOperationDuration records the distribution of tool execution durations in seconds.
	ExecuteToolMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
)

// IncExecuteToolRequestCnt increments the tool execution request counter by 1 with the provided model name, tool name, and session attributes.
func IncExecuteToolRequestCnt(ctx context.Context, modelName string, toolName string, sess *session.Session) {
	appName, userID, sessionID := sessionInfo(sess)
	ExecuteToolMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1,
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationExecuteTool),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyGenAIToolName, toolName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}

// RecordExecuteToolOperationDuration records the duration of a tool execution operation.
func RecordExecuteToolOperationDuration(ctx context.Context, modelName string, toolName string, sess *session.Session, duration time.Duration) {
	appName, userID, sessionID := sessionInfo(sess)
	ExecuteToolMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attribute.String(KeyGenAIOperationName, OperationExecuteTool),
			attribute.String(KeyGenAISystem, modelName),
			attribute.String(KeyGenAIToolName, toolName),
			attribute.String(KeyTRPCAgentGoAppName, appName),
			attribute.String(KeyTRPCAgentGoUserID, userID),
			attribute.String(KeyGenAIConversationID, sessionID),
		))
}
