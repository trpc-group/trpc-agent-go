//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace

import (
	"context"
	"testing"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestWithAttributePolicy_BuildsRule(t *testing.T) {
	opts := &options{}
	WithSpanAttributePolicy(
		WithAttributePolicy(OperationChat, AttrInputMessagesOTel, Drop()),
		WithAttributePolicy(OperationWorkflow, AttrWorkflowRequest, Truncate(1024)),
	)(opts)
	if opts.spanAttributePolicy == nil {
		t.Fatal("expected span attribute policy")
	}
	if len(opts.spanAttributePolicy.rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(opts.spanAttributePolicy.rules))
	}
}

func TestSetSpanAttributePolicy_FromDropRule(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	policy := SpanAttributePolicy{}
	WithAttributePolicy(OperationChat, AttrInputMessagesOTel, Drop())(&policy)
	SetSpanAttributePolicy(policy)
	if itelemetry.Resolve(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel).Action != itelemetry.AttributeDrop {
		t.Fatal("expected drop rule to be installed")
	}
}

func TestGetSpanAttributePolicy_RoundTrip(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	want := SpanAttributePolicy{}
	WithAttributePolicy(OperationInvokeAgent, AttrOutputMessages, Omit())(&want)
	WithAttributePolicy(OperationInvokeAgent, AttrOutputMessages, MaxBytes(4096))(&want)
	SetSpanAttributePolicy(want)
	got := GetSpanAttributePolicy()
	if len(got.rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(got.rules))
	}
}

func TestTruncate_SetsTruncateAction(t *testing.T) {
	rule := attributeRule{action: itelemetry.AttributeCapture}
	Truncate(2048)(&rule)
	if rule.action != itelemetry.AttributeTruncate || rule.maxBytes != 2048 {
		t.Fatalf("unexpected rule %+v", rule)
	}
}

func TestStart_WithSpanAttributePolicy(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	ctx := context.Background()
	clean, err := Start(ctx,
		WithEndpoint("localhost:4317"),
		WithSpanAttributePolicy(
			WithAttributePolicy(OperationChat, AttrInputMessagesOTel, Drop()),
		),
	)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if itelemetry.Resolve(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel).Action != itelemetry.AttributeDrop {
		t.Fatal("expected Start to install span attribute policy")
	}
	if err := clean(); err != nil {
		t.Fatalf("clean returned error: %v", err)
	}
	if itelemetry.Resolve(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel).Action != itelemetry.AttributeCapture {
		t.Fatal("expected clean to restore previous policy")
	}
}

func TestStart_FailedInitDoesNotInstallPolicy(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(SpanAttributePolicy{})
	ctx := context.Background()
	clean, err := Start(ctx,
		WithProtocol("http"),
		WithEndpoint("localhost:4318"),
		WithEndpointURL("http:///bad"),
		WithSpanAttributePolicy(
			WithAttributePolicy(OperationChat, AttrInputMessagesOTel, Drop()),
		),
	)
	if clean != nil {
		_ = clean()
	}
	if err == nil {
		t.Fatal("expected Start to fail with invalid endpoint URL")
	}
	if itelemetry.Resolve(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel).Action != itelemetry.AttributeCapture {
		t.Fatal("expected failed Start not to install span attribute policy")
	}
}
