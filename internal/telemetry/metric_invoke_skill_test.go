//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestInvokeSkillMetricAttributes(t *testing.T) {
	attrs := InvokeSkillMetricAttributes{
		SkillName: "code_review",
		SkillID:   "skill_123",
		UserID:    "user-1",
		AgentID:   "agent-1",
		AgentName: "review-agent",
		Error:     errors.New("boom"),
	}.toAttributes()

	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIOperationName, OperationInvokeSkill)
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAISkillName, "code_review")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAISkillID, "skill_123")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIUserID, "user-1")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIAgentID, "agent-1")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIAgentName, "review-agent")
	requireMetricAttr(t, attrs, semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType)
	requireNoMetricAttr(t, attrs, semconvtrace.KeyGenAIConversationID)
	requireNoMetricAttr(t, attrs, semconvtrace.KeyGenAIAppName)
	requireNoMetricAttr(t, attrs, semconvtrace.KeyGenAIInvokeSkillRequest)
	requireNoMetricAttr(t, attrs, semconvtrace.KeyGenAIInvokeSkillResponse)
}

func TestInvokeSkillMetricAttributes_UnknownRequiredDimensions(t *testing.T) {
	attrs := InvokeSkillMetricAttributes{}.toAttributes()
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAISkillName, "_unknown")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAISkillID, "_unknown")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIUserID, "_unknown")
	requireMetricAttr(t, attrs, semconvtrace.KeyGenAIAgentID, "_unknown")
}

func requireMetricAttr(t *testing.T, attrs []attribute.KeyValue, key, value string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			require.Equal(t, value, attr.Value.AsString())
			return
		}
	}
	t.Fatalf("missing metric attribute %s", key)
}

func requireNoMetricAttr(t *testing.T, attrs []attribute.KeyValue, key string) {
	t.Helper()
	for _, attr := range attrs {
		require.NotEqual(t, key, string(attr.Key))
	}
}
