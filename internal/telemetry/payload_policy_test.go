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

func TestCurrentPayloadPolicy(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	if got := CurrentPayloadPolicy(); got.InlineMaxBytes != 0 {
		t.Fatalf("expected empty policy, got %+v", got)
	}

	want := PayloadPolicy{InlineMaxBytes: 512, OverflowMode: OverflowOmit}
	SetPayloadPolicy(want)
	got := CurrentPayloadPolicy()
	if got.InlineMaxBytes != want.InlineMaxBytes || got.OverflowMode != want.OverflowMode {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestMergeAttributeRules(t *testing.T) {
	dst := AttributeRules{
		Enabled:  []AttributeSelector{{Key: "a"}},
		Disabled: []AttributeSelector{{Key: "b"}},
	}
	src := AttributeRules{
		Enabled:  []AttributeSelector{{Key: "c"}},
		Disabled: []AttributeSelector{{Key: "d"}},
	}
	got := MergeAttributeRules(dst, src)
	if len(got.Enabled) != 2 || len(got.Disabled) != 2 {
		t.Fatalf("expected merged rules, got %+v", got)
	}
	if got.Enabled[1].Key != "c" || got.Disabled[1].Key != "d" {
		t.Fatalf("unexpected merge order: %+v", got)
	}
}

func TestOverflowModeValue_Default(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	if got := OverflowModeValue(); got != OverflowTruncate {
		t.Fatalf("expected default overflow mode, got %v", got)
	}
}
