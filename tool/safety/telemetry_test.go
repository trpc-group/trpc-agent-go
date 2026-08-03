//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRecordSpanWritesOnlySafetySummaryAttributes(t *testing.T) {
	const secret = "span-must-not-contain-this-secret"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("tool-safety-test").Start(context.Background(), "scan")

	RecordSpan(ctx, Report{
		Decision:       tool.PermissionActionDeny,
		RiskLevel:      RiskLevelCritical,
		RuleID:         "credential.read",
		Evidence:       "password=" + secret,
		Recommendation: "rotate " + secret,
		ToolName:       "workspace_exec",
		Command:        "cat .env --token " + secret,
		Backend:        BackendWorkspace,
		Blocked:        true,
		Redacted:       true,
		DurationMS:     13,
	})
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attributes := attributeMap(ended[0].Attributes())
	require.Len(t, attributes, 7)
	require.Equal(t, "deny", attributes[AttributeDecision].AsString())
	require.Equal(t, "critical", attributes[AttributeRiskLevel].AsString())
	require.Equal(t, "credential.read", attributes[AttributeRuleID].AsString())
	require.Equal(t, "workspace", attributes[AttributeBackend].AsString())
	require.True(t, attributes[AttributeBlocked].AsBool())
	require.True(t, attributes[AttributeRedacted].AsBool())
	require.Equal(t, int64(13), attributes[AttributeScanDurationMS].AsInt64())

	serialized := fmt.Sprint(ended[0].Attributes())
	require.NotContains(t, serialized, secret)
	require.False(t, strings.Contains(serialized, "command"))
	require.False(t, strings.Contains(serialized, "evidence"))
}

func TestRecordSpanWithoutRecordingSpanIsSafe(t *testing.T) {
	require.NotPanics(t, func() {
		RecordSpan(context.Background(), Report{
			Decision:  tool.PermissionActionAllow,
			RiskLevel: RiskLevelLow,
			Backend:   BackendCode,
		})
	})
}

func attributeMap(attributes []attribute.KeyValue) map[string]attribute.Value {
	result := make(map[string]attribute.Value, len(attributes))
	for _, item := range attributes {
		result[string(item.Key)] = item.Value
	}
	return result
}

func TestRecordSpanSanitizesDirectCallerIdentifiers(t *testing.T) {
	const secret = "direct-span-secret-value"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("tool-safety-test").Start(
		context.Background(),
		"direct-scan",
	)

	RecordSpan(ctx, Report{
		Decision:  tool.PermissionAction("token=" + secret),
		RiskLevel: RiskLevel(strings.Repeat("x", maxSafetyIdentifierRunes+1)),
		RuleID:    "password=" + secret,
		Backend:   Backend("token=" + secret),
	})
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attributes := attributeMap(ended[0].Attributes())
	serialized := fmt.Sprint(ended[0].Attributes())
	require.NotContains(t, serialized, secret)
	require.Equal(t, omittedSafetyIdentifier, attributes[AttributeRiskLevel].AsString())
	require.Equal(t, "unknown", attributes[AttributeBackend].AsString())
	require.True(t, attributes[AttributeRedacted].AsBool())
}
