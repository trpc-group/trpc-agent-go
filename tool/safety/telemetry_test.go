//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestGuard_EmitsOTelAttributes(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("tool-safety-test").Start(context.Background(), "permission.check")
	g := safety.NewGuard()
	dec, err := g.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call-otel-1",
		Arguments:  []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	span.End()

	ended := recorder.Ended()
	require.NotEmpty(t, ended)
	got := map[string]string{}
	var blocked bool
	for _, a := range ended[0].Attributes() {
		switch string(a.Key) {
		case safety.AttrBlocked:
			blocked = a.Value.AsBool()
		default:
			got[string(a.Key)] = a.Value.AsString()
		}
	}
	require.Equal(t, "deny", got[safety.AttrDecision])
	require.NotEmpty(t, got[safety.AttrRiskLevel])
	require.NotEmpty(t, got[safety.AttrRuleID])
	require.Equal(t, string(safety.BackendWorkspace), got[safety.AttrBackend])
	require.True(t, blocked)
	require.Equal(t, "call-otel-1", got[safety.AttrToolCallID])
}
