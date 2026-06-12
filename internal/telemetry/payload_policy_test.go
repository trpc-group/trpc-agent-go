package telemetry

import (
	"testing"

	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestAllowAttribute_DefaultAllowsAll(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{})
	if !AllowAttribute(OperationChat, semconvtrace.KeyLLMRequest) {
		t.Fatal("expected default policy to allow attributes")
	}
}

func TestAllowAttribute_Disabled(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Disabled: []AttributeSelector{
				{Operation: OperationChat, Key: semconvtrace.KeyGenAIInputMessagesOTel},
			},
		},
	})
	if !AllowAttribute(OperationChat, semconvtrace.KeyGenAIInputMessages) {
		t.Fatal("expected input.messages to remain enabled")
	}
	if AllowAttribute(OperationChat, semconvtrace.KeyGenAIInputMessagesOTel) {
		t.Fatal("expected input.messages.otel to be disabled")
	}
}

func TestAllowAttribute_EnabledWhitelist(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Enabled: []AttributeSelector{
				{Operation: OperationChat, Key: semconvtrace.KeyGenAIInputMessages},
			},
		},
	})
	if !AllowAttribute(OperationChat, semconvtrace.KeyGenAIInputMessages) {
		t.Fatal("expected whitelisted attribute to be allowed")
	}
	if AllowAttribute(OperationChat, semconvtrace.KeyLLMRequest) {
		t.Fatal("expected non-whitelisted attribute to be rejected")
	}
}

func TestAllowAttribute_OperationScopedDisabled(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Disabled: []AttributeSelector{
				{Key: semconvtrace.KeyGenAIInputMessagesOTel},
			},
		},
	})
	if AllowAttribute(OperationInvokeAgent, semconvtrace.KeyGenAIInputMessagesOTel) {
		t.Fatal("expected operation-agnostic disable to apply to all operations")
	}
}
