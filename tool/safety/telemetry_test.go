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
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSetSpanAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	ctx, span := tp.Tracer("test").Start(context.Background(), "scan")

	report := sampleReport()
	report.Backend = BackendHostExec
	SetSpanAttributes(ctx, report)
	span.End()

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("want 1 span, got %d", len(ended))
	}
	got := map[string]string{}
	for _, a := range ended[0].Attributes() {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[AttrDecision] != string(DecisionDeny) {
		t.Errorf("%s=%q want deny", AttrDecision, got[AttrDecision])
	}
	if got[AttrRiskLevel] != string(RiskCritical) {
		t.Errorf("%s=%q want critical", AttrRiskLevel, got[AttrRiskLevel])
	}
	if got[AttrBackend] != string(BackendHostExec) {
		t.Errorf("%s=%q want hostexec", AttrBackend, got[AttrBackend])
	}
	if got[AttrRuleID] != "cmd.dangerous_delete" {
		t.Errorf("%s=%q want cmd.dangerous_delete", AttrRuleID, got[AttrRuleID])
	}
}

func TestSetSpanAttributesNoSpanNoPanic(t *testing.T) {
	// No recording span in context: must be a safe no-op.
	SetSpanAttributes(context.Background(), sampleReport())
}
