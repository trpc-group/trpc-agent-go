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

func TestChatCapturePolicy_DisablesExplicitFields(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	policy := ChatCapturePolicy(ChatPayloadCapture{
		Request: ChatRequestCapture{
			InputMessagesOTel: CaptureBool(false),
		},
		Response: ChatResponseCapture{
			OutputMessagesOTel: CaptureBool(false),
		},
	})
	if len(policy.Attributes.Disabled) != 2 {
		t.Fatalf("expected 2 disabled selectors, got %d", len(policy.Attributes.Disabled))
	}
}

func TestSetPayloadPolicy_FromChatCapture(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(ChatCapturePolicy(ChatPayloadCapture{
		Request: ChatRequestCapture{InputMessagesOTel: CaptureBool(false)},
	}))
	if itelemetry.AllowAttribute(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel) {
		t.Fatal("expected otel input messages to be disabled")
	}
}

func TestWithChatCapture_MergesDisabledRules(t *testing.T) {
	opts := &options{}
	WithChatCapture(ChatPayloadCapture{
		Request: ChatRequestCapture{InputMessagesOTel: CaptureBool(false)},
	})(opts)
	WithPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Disabled: []AttributeSelector{{Operation: "chat", Key: semconvtrace.KeyLLMResponse}},
		},
	})(opts)
	if opts.payloadPolicy == nil {
		t.Fatal("expected payload policy to be set")
	}
	if len(opts.payloadPolicy.Attributes.Disabled) != 2 {
		t.Fatalf("expected merged disabled rules, got %d", len(opts.payloadPolicy.Attributes.Disabled))
	}
}

func TestCaptureBoolNilDefaultsEnabled(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(ChatCapturePolicy(ChatPayloadCapture{}))
	if !itelemetry.AllowAttribute(itelemetry.OperationChat, semconvtrace.KeyLLMRequest) {
		t.Fatal("expected nil capture fields to remain enabled")
	}
}

func TestGetPayloadPolicy_RoundTrip(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	want := PayloadPolicy{
		Attributes: AttributeRules{
			Enabled: []AttributeSelector{
				{Operation: "chat", Key: semconvtrace.KeyGenAIInputMessages},
			},
			Disabled: []AttributeSelector{
				{Operation: "chat", Key: semconvtrace.KeyGenAIInputMessagesOTel},
			},
		},
		InlineMaxBytes: 4096,
		OverflowMode:   OverflowOmit,
	}
	SetPayloadPolicy(want)
	got := GetPayloadPolicy()
	if got.InlineMaxBytes != want.InlineMaxBytes {
		t.Fatalf("inline max bytes: got %d want %d", got.InlineMaxBytes, want.InlineMaxBytes)
	}
	if got.OverflowMode != want.OverflowMode {
		t.Fatalf("overflow mode: got %v want %v", got.OverflowMode, want.OverflowMode)
	}
	if len(got.Attributes.Enabled) != 1 || got.Attributes.Enabled[0].Key != want.Attributes.Enabled[0].Key {
		t.Fatalf("enabled rules: got %+v", got.Attributes.Enabled)
	}
	if len(got.Attributes.Disabled) != 1 || got.Attributes.Disabled[0].Key != want.Attributes.Disabled[0].Key {
		t.Fatalf("disabled rules: got %+v", got.Attributes.Disabled)
	}
}

func TestWithInlineMaxBytesAndOverflowMode(t *testing.T) {
	opts := &options{}
	WithInlineMaxBytes(8192)(opts)
	WithOverflowMode(OverflowOmit)(opts)
	if opts.payloadPolicy == nil {
		t.Fatal("expected payload policy to be initialized")
	}
	if opts.payloadPolicy.InlineMaxBytes != 8192 {
		t.Fatalf("inline max bytes: got %d want 8192", opts.payloadPolicy.InlineMaxBytes)
	}
	if opts.payloadPolicy.OverflowMode != OverflowOmit {
		t.Fatalf("overflow mode: got %v want %v", opts.payloadPolicy.OverflowMode, OverflowOmit)
	}
}

func TestWithPayloadPolicy_InitialAssignment(t *testing.T) {
	opts := &options{}
	WithPayloadPolicy(PayloadPolicy{InlineMaxBytes: 1024})(opts)
	if opts.payloadPolicy == nil || opts.payloadPolicy.InlineMaxBytes != 1024 {
		t.Fatalf("expected initial payload policy, got %+v", opts.payloadPolicy)
	}
}

func TestWithPayloadPolicy_MergesEnabledAndInlineMaxBytes(t *testing.T) {
	opts := &options{}
	WithPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Enabled: []AttributeSelector{{Operation: "chat", Key: semconvtrace.KeyLLMRequest}},
		},
		InlineMaxBytes: 100,
	})(opts)
	WithPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Enabled: []AttributeSelector{{Operation: "chat", Key: semconvtrace.KeyLLMResponse}},
		},
		InlineMaxBytes: 200,
		OverflowMode:   OverflowTruncate,
	})(opts)
	if len(opts.payloadPolicy.Attributes.Enabled) != 2 {
		t.Fatalf("expected merged enabled rules, got %d", len(opts.payloadPolicy.Attributes.Enabled))
	}
	if opts.payloadPolicy.InlineMaxBytes != 200 {
		t.Fatalf("inline max bytes: got %d want 200", opts.payloadPolicy.InlineMaxBytes)
	}
	if opts.payloadPolicy.OverflowMode != OverflowTruncate {
		t.Fatalf("overflow mode: got %v want %v", opts.payloadPolicy.OverflowMode, OverflowTruncate)
	}
}

func TestWithChatCapture_PreservesOverflowMode(t *testing.T) {
	opts := &options{}
	WithOverflowMode(OverflowOmit)(opts)
	WithChatCapture(ChatPayloadCapture{
		Request: ChatRequestCapture{InputMessagesOTel: CaptureBool(false)},
	})(opts)
	if opts.payloadPolicy.OverflowMode != OverflowOmit {
		t.Fatalf("overflow mode: got %v want %v", opts.payloadPolicy.OverflowMode, OverflowOmit)
	}
}

func TestStart_WithPayloadPolicy(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	ctx := context.Background()
	clean, err := Start(ctx,
		WithEndpoint("localhost:4317"),
		WithChatCapture(ChatPayloadCapture{
			Request: ChatRequestCapture{InputMessagesOTel: CaptureBool(false)},
		}),
	)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = clean() })

	policy := GetPayloadPolicy()
	if len(policy.Attributes.Disabled) != 1 {
		t.Fatalf("expected disabled otel input rule, got %+v", policy.Attributes.Disabled)
	}
	if itelemetry.AllowAttribute(itelemetry.OperationChat, semconvtrace.KeyGenAIInputMessagesOTel) {
		t.Fatal("expected Start to install payload policy disabling otel input messages")
	}
}
