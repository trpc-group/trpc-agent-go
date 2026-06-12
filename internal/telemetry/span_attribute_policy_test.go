//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"testing"

	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestResolve_DefaultCapture(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(SpanAttributePolicy{})
	got := Resolve(OperationChat, semconvtrace.KeyLLMRequest)
	if got.Action != AttributeCapture {
		t.Fatalf("expected capture, got %v", got.Action)
	}
}

func TestResolve_Drop(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessagesOTel,
		Action:    AttributeDrop,
	}))
	got := Resolve(OperationChat, semconvtrace.KeyGenAIInputMessagesOTel)
	if got.Action != AttributeDrop {
		t.Fatalf("expected drop, got %v", got.Action)
	}
	if Resolve(OperationChat, semconvtrace.KeyGenAIInputMessages).Action != AttributeCapture {
		t.Fatal("expected unrelated key to remain capture")
	}
}

func TestResolve_OperationScoped(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationInvokeAgent,
		Key:       semconvtrace.KeyGenAIInputMessagesOTel,
		Action:    AttributeDrop,
	}))
	if Resolve(OperationInvokeAgent, semconvtrace.KeyGenAIInputMessagesOTel).Action != AttributeDrop {
		t.Fatal("expected invoke drop")
	}
	if Resolve(OperationChat, semconvtrace.KeyGenAIInputMessagesOTel).Action != AttributeCapture {
		t.Fatal("expected chat to remain capture")
	}
}

func TestResolve_LaterRuleOverrides(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	policy := AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessages,
		Action:    AttributeDrop,
	})
	policy = AppendAttributeRule(policy, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyGenAIInputMessages,
		Action:    AttributeOmit,
	})
	SetSpanAttributePolicy(policy)
	got := Resolve(OperationChat, semconvtrace.KeyGenAIInputMessages)
	if got.Action != AttributeOmit {
		t.Fatalf("expected later omit rule, got %v", got.Action)
	}
}

func TestInstallSpanAttributePolicy_Restore(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	SetSpanAttributePolicy(SpanAttributePolicy{})
	restore := InstallSpanAttributePolicy(AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationChat,
		Key:       semconvtrace.KeyLLMRequest,
		Action:    AttributeDrop,
	}))
	if Resolve(OperationChat, semconvtrace.KeyLLMRequest).Action != AttributeDrop {
		t.Fatal("expected installed drop rule")
	}
	restore()
	if Resolve(OperationChat, semconvtrace.KeyLLMRequest).Action != AttributeCapture {
		t.Fatal("expected restored default capture")
	}
}

func TestCurrentSpanAttributePolicy_RoundTrip(t *testing.T) {
	t.Cleanup(func() { SetSpanAttributePolicy(SpanAttributePolicy{}) })

	want := AppendAttributeRule(SpanAttributePolicy{}, AttributeRule{
		Operation: OperationWorkflow,
		Key:       semconvtrace.KeyGenAIWorkflowRequest,
		Action:    AttributeTruncate,
		MaxBytes:  512,
	})
	SetSpanAttributePolicy(want)
	got := CurrentSpanAttributePolicy()
	rules := got.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Action != AttributeTruncate || rules[0].MaxBytes != 512 {
		t.Fatalf("unexpected rule %+v", rules[0])
	}
}
