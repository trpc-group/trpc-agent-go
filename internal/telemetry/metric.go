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
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()

	ChatMeter                                     metric.Meter            = MeterProvider.Meter(metrics.MeterNameChat)
	ChatMetricTRPCAgentGoClientRequestCnt         metric.Int64Counter     = noop.Int64Counter{}
	ChatMetricGenAIClientTokenUsage               metric.Int64Histogram   = noop.Int64Histogram{}
	ChatMetricGenAIClientOperationDuration        metric.Float64Histogram = noop.Float64Histogram{}
	ChatMetricTRPCAgentGoClientTimeToFirstToken   metric.Float64Histogram = noop.Float64Histogram{}
	ChatMetricTRPCAgentGoClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}
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
	recordChatTokenUsage(ctx, modelName, sess, usage, metrics.KeyTRPCAgentGoInputTokenType)
}

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

var (
	ExecuteToolMeter                              = MeterProvider.Meter(metrics.MeterNameExecuteTool)
	ExecuteToolMetricTRPCAgentGoClientRequestCnt  = noop.Int64Counter{}
	ExecuteToolMetricGenAIClientOperationDuration = noop.Float64Histogram{}
)

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
