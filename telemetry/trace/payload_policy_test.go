package trace

import (
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
