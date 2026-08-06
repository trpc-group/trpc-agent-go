//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSetSpanAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "scan")
	report := NewScanner(DefaultPolicy()).Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	})
	SetSpanAttributes(ctx, report)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, string(DecisionDeny), attrs[AttrDecision])
	require.Equal(t, string(RiskCritical), attrs[AttrRiskLevel])
	require.Equal(t, ruleDangerousDelete, attrs[AttrRuleID])
	require.Equal(t, string(BackendWorkspaceExec), attrs[AttrBackend])
}

func TestPermissionPolicyTelemetryOption(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "permission")
	pp := NewPermissionPolicy(
		WithPolicy(DefaultPolicy()),
		WithTelemetry(true),
	)
	decision, err := pp.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, string(DecisionDeny), attrs[AttrDecision])
	require.Equal(t, ruleDangerousDelete, attrs[AttrRuleID])
}
