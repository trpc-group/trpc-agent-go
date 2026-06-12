//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace

import (
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// OverflowMode controls overflow handling for large serialized payloads.
type OverflowMode int

const (
	// OverflowTruncate keeps a UTF-8 safe prefix and wraps it in PayloadEnvelope.
	OverflowTruncate OverflowMode = iota
	// OverflowOmit writes a placeholder envelope without message content.
	OverflowOmit
)

// OverflowOmitPlaceholder is the human-readable placeholder used in OverflowOmit mode.
const OverflowOmitPlaceholder = itelemetry.OverflowOmitPlaceholder

// AttributeSelector identifies an attribute production rule.
type AttributeSelector struct {
	Operation string
	Key       string
}

// AttributeRules controls which attributes are produced at span creation time.
type AttributeRules struct {
	Enabled  []AttributeSelector
	Disabled []AttributeSelector
}

// PayloadPolicy controls production-side span attribute payload behavior.
// Zero value preserves current behavior.
type PayloadPolicy struct {
	Attributes     AttributeRules
	InlineMaxBytes int64
	OverflowMode   OverflowMode
}

// PayloadEnvelope is the JSON shape written when InlineMaxBytes is exceeded.
type PayloadEnvelope struct {
	Truncated     bool   `json:"truncated,omitempty"`
	Omitted       bool   `json:"omitted,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	SHA256        string `json:"sha256"`
	OriginalBytes int64  `json:"original_bytes"`
}

// ChatPayloadCapture selects which chat payload attributes are marshaled.
// Use CaptureBool to set explicit enable/disable; nil fields default to enabled.
type ChatPayloadCapture struct {
	Request  ChatRequestCapture
	Response ChatResponseCapture
}

// ChatRequestCapture maps to chat request serialization points in buildRequestAttributes.
type ChatRequestCapture struct {
	LLMRequest        *bool
	InputMessages     *bool
	InputMessagesOTel *bool
	ToolDefinitions   *bool
}

// ChatResponseCapture maps to chat response serialization points in buildResponseAttributes.
type ChatResponseCapture struct {
	LLMResponse        *bool
	OutputMessages     *bool
	OutputMessagesOTel *bool
}

// CaptureBool returns a pointer to b for ChatCapture fields.
func CaptureBool(b bool) *bool {
	return &b
}

// ChatCapturePolicy converts ChatPayloadCapture into a PayloadPolicy AttributeRules blacklist.
func ChatCapturePolicy(capture ChatPayloadCapture) PayloadPolicy {
	return PayloadPolicy{Attributes: chatCaptureRules(capture)}
}

// SetPayloadPolicy installs the global payload policy.
func SetPayloadPolicy(policy PayloadPolicy) {
	itelemetry.SetPayloadPolicy(toInternalPayloadPolicy(policy))
}

// GetPayloadPolicy returns the currently installed payload policy.
func GetPayloadPolicy() PayloadPolicy {
	return fromInternalPayloadPolicy(itelemetry.CurrentPayloadPolicy())
}

// WithPayloadPolicy registers a payload policy during trace.Start.
func WithPayloadPolicy(policy PayloadPolicy) Option {
	return func(opts *options) {
		opts.applyPayloadPolicy(policy)
	}
}

// WithChatCapture registers chat payload capture rules during trace.Start.
func WithChatCapture(capture ChatPayloadCapture) Option {
	return WithPayloadPolicy(ChatCapturePolicy(capture))
}

// WithInlineMaxBytes sets the inline payload byte limit during trace.Start.
func WithInlineMaxBytes(n int64) Option {
	return func(opts *options) {
		opts.ensurePayloadPolicy()
		opts.payloadPolicy.InlineMaxBytes = n
	}
}

// WithOverflowMode sets overflow handling during trace.Start.
func WithOverflowMode(mode OverflowMode) Option {
	return func(opts *options) {
		opts.ensurePayloadPolicy()
		opts.payloadPolicy.OverflowMode = mode
	}
}

func (o *options) ensurePayloadPolicy() {
	if o.payloadPolicy == nil {
		p := PayloadPolicy{}
		o.payloadPolicy = &p
	}
}

func (o *options) applyPayloadPolicy(policy PayloadPolicy) {
	if o.payloadPolicy == nil {
		copy := policy
		o.payloadPolicy = &copy
		return
	}
	merged := *o.payloadPolicy
	merged.Attributes = mergeAttributeRules(merged.Attributes, policy.Attributes)
	if policy.InlineMaxBytes != 0 {
		merged.InlineMaxBytes = policy.InlineMaxBytes
	}
	merged.OverflowMode = policy.OverflowMode
	o.payloadPolicy = &merged
}

func mergeAttributeRules(dst, src AttributeRules) AttributeRules {
	out := dst
	if len(src.Enabled) > 0 {
		out.Enabled = append(append([]AttributeSelector{}, out.Enabled...), src.Enabled...)
	}
	if len(src.Disabled) > 0 {
		out.Disabled = append(append([]AttributeSelector{}, out.Disabled...), src.Disabled...)
	}
	return out
}

func chatCaptureRules(capture ChatPayloadCapture) AttributeRules {
	var disabled []AttributeSelector
	const op = itelemetry.OperationChat
	appendIfDisabled := func(key string, enabled *bool) {
		if enabled != nil && !*enabled {
			disabled = append(disabled, AttributeSelector{Operation: op, Key: key})
		}
	}
	appendIfDisabled(semconvtrace.KeyLLMRequest, capture.Request.LLMRequest)
	appendIfDisabled(semconvtrace.KeyGenAIInputMessages, capture.Request.InputMessages)
	appendIfDisabled(semconvtrace.KeyGenAIInputMessagesOTel, capture.Request.InputMessagesOTel)
	appendIfDisabled(semconvtrace.KeyGenAIRequestToolDefinitions, capture.Request.ToolDefinitions)
	appendIfDisabled(semconvtrace.KeyLLMResponse, capture.Response.LLMResponse)
	appendIfDisabled(semconvtrace.KeyGenAIOutputMessages, capture.Response.OutputMessages)
	appendIfDisabled(semconvtrace.KeyGenAIOutputMessagesOTel, capture.Response.OutputMessagesOTel)
	return AttributeRules{Disabled: disabled}
}

func toInternalPayloadPolicy(policy PayloadPolicy) itelemetry.PayloadPolicy {
	return itelemetry.PayloadPolicy{
		Attributes:     toInternalAttributeRules(policy.Attributes),
		InlineMaxBytes: policy.InlineMaxBytes,
		OverflowMode:   itelemetry.OverflowMode(policy.OverflowMode),
	}
}

func fromInternalPayloadPolicy(policy itelemetry.PayloadPolicy) PayloadPolicy {
	return PayloadPolicy{
		Attributes:     fromInternalAttributeRules(policy.Attributes),
		InlineMaxBytes: policy.InlineMaxBytes,
		OverflowMode:   OverflowMode(policy.OverflowMode),
	}
}

func toInternalAttributeRules(rules AttributeRules) itelemetry.AttributeRules {
	enabled := make([]itelemetry.AttributeSelector, len(rules.Enabled))
	for i, s := range rules.Enabled {
		enabled[i] = itelemetry.AttributeSelector{Operation: s.Operation, Key: s.Key}
	}
	disabled := make([]itelemetry.AttributeSelector, len(rules.Disabled))
	for i, s := range rules.Disabled {
		disabled[i] = itelemetry.AttributeSelector{Operation: s.Operation, Key: s.Key}
	}
	return itelemetry.AttributeRules{Enabled: enabled, Disabled: disabled}
}

func fromInternalAttributeRules(rules itelemetry.AttributeRules) AttributeRules {
	enabled := make([]AttributeSelector, len(rules.Enabled))
	for i, s := range rules.Enabled {
		enabled[i] = AttributeSelector{Operation: s.Operation, Key: s.Key}
	}
	disabled := make([]AttributeSelector, len(rules.Disabled))
	for i, s := range rules.Disabled {
		disabled[i] = AttributeSelector{Operation: s.Operation, Key: s.Key}
	}
	return AttributeRules{Enabled: enabled, Disabled: disabled}
}
