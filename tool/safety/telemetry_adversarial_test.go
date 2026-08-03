//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCustomRuleIdentifiersAndBackendCannotLeakToTelemetry(t *testing.T) {
	const (
		ruleSecret    = "rule-secret-must-not-leak"
		backendSecret = "backend-secret-must-not-leak"
	)
	var audit bytes.Buffer
	guard, err := New(
		testPolicy(),
		WithAuditSink(NewJSONLAuditSink(&audit)),
		WithRule(RuleFunc(func(context.Context, Request, Policy) []Match {
			return []Match{{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelHigh,
				RuleID:         "token=" + ruleSecret,
				Evidence:       "custom rule matched",
				Recommendation: "do not execute",
			}}
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("safety-adversarial").Start(context.Background(), "scan")
	request := commandRequest(BackendWorkspace, "echo safe")
	request.Backend = Backend("password=" + backendSecret)
	report, err := guard.Scan(ctx, request)
	span.End()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	serialized := fmt.Sprintf("%+v\n%s\n%v", report, audit.String(), recorder.Ended()[0].Attributes())
	if strings.Contains(serialized, ruleSecret) || strings.Contains(serialized, backendSecret) {
		t.Fatalf("report/audit/telemetry leaked custom input: %s", serialized)
	}
	if report.Backend != BackendUnknown {
		t.Fatalf("unrecognized backend = %q, want %q", report.Backend, BackendUnknown)
	}
	if !report.Redacted || !strings.Contains(report.RuleID, RedactedValue) {
		t.Fatalf("custom rule id was not redacted: %+v", report)
	}
}
