package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestBuildRequestAttributes_PayloadPolicySkipsDisabled(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{
		Attributes: AttributeRules{
			Disabled: []AttributeSelector{
				{Operation: OperationChat, Key: semconvtrace.KeyGenAIInputMessagesOTel},
			},
		},
	})

	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	attrs := buildRequestAttributes(req)
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessagesOTel); ok {
		t.Fatal("expected otel input messages attribute to be skipped")
	}
	if _, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages); !ok {
		t.Fatal("expected legacy input messages attribute to remain")
	}
}

func TestFormatPayloadValue_Truncate(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{InlineMaxBytes: 32, OverflowMode: OverflowTruncate})
	payload := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("x", 128) + `"}]}`)
	got, err := formatPayloadValue(payload)
	if err != nil {
		t.Fatalf("formatPayloadValue: %v", err)
	}
	var envelope PayloadEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !envelope.Truncated || envelope.Omitted {
		t.Fatalf("expected truncated envelope, got %+v", envelope)
	}
	if !utf8.ValidString(envelope.Prefix) {
		t.Fatal("expected utf-8 safe prefix")
	}
	if envelope.OriginalBytes != int64(len(payload)) {
		t.Fatalf("expected original bytes %d, got %d", len(payload), envelope.OriginalBytes)
	}
	if envelope.SHA256 == "" {
		t.Fatal("expected sha256 fingerprint")
	}
}

func TestAppendStringAttribute_SkipsOnErrorWhenBestEffort(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIOutputMessages, "", func() ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	})
	if len(attrs) != 0 {
		t.Fatalf("expected best-effort attribute to be skipped on error, got %d attrs", len(attrs))
	}
}

func TestAppendStringAttribute_PlaceholderOnErrorWhenConfigured(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	attrs := appendStringAttribute(nil, OperationChat, semconvtrace.KeyGenAIInputMessages, "<not json serializable>", func() ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	})
	got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIInputMessages)
	if !ok || got != "<not json serializable>" {
		t.Fatalf("expected placeholder on error, got %q ok=%v", got, ok)
	}
}

func TestFormatPayloadValue_Omit(t *testing.T) {
	t.Cleanup(func() { SetPayloadPolicy(PayloadPolicy{}) })

	SetPayloadPolicy(PayloadPolicy{InlineMaxBytes: 16, OverflowMode: OverflowOmit})
	payload := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	got, err := formatPayloadValue(payload)
	if err != nil {
		t.Fatalf("formatPayloadValue: %v", err)
	}
	var envelope PayloadEnvelope
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !envelope.Omitted || envelope.Truncated || envelope.Prefix != "" {
		t.Fatalf("expected omitted envelope, got %+v", envelope)
	}
}
