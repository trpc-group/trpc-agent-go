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

// SpanOperation identifies the telemetry operation scope for attribute rules.
type SpanOperation string

const (
	// OperationChat is the chat inference operation.
	OperationChat SpanOperation = itelemetry.OperationChat
	// OperationInvokeAgent is the agent invocation operation.
	OperationInvokeAgent SpanOperation = itelemetry.OperationInvokeAgent
	// OperationWorkflow is the workflow operation.
	OperationWorkflow SpanOperation = itelemetry.OperationWorkflow
	// OperationExecuteTool is the tool execution operation.
	OperationExecuteTool SpanOperation = itelemetry.OperationExecuteTool
)

// AttributeKey identifies a span attribute key for policy rules.
type AttributeKey string

const (
	// AttrLLMRequest is the LLM request attribute key.
	AttrLLMRequest AttributeKey = semconvtrace.KeyLLMRequest
	// AttrLLMResponse is the LLM response attribute key.
	AttrLLMResponse AttributeKey = semconvtrace.KeyLLMResponse
	// AttrInputMessages is the legacy input messages attribute key.
	AttrInputMessages AttributeKey = semconvtrace.KeyGenAIInputMessages
	// AttrInputMessagesOTel is the OTel input messages attribute key.
	AttrInputMessagesOTel AttributeKey = semconvtrace.KeyGenAIInputMessagesOTel
	// AttrOutputMessages is the legacy output messages attribute key.
	AttrOutputMessages AttributeKey = semconvtrace.KeyGenAIOutputMessages
	// AttrOutputMessagesOTel is the OTel output messages attribute key.
	AttrOutputMessagesOTel AttributeKey = semconvtrace.KeyGenAIOutputMessagesOTel
)

// SpanAttributePolicy controls production-side span attribute behavior.
// Zero value preserves current behavior.
type SpanAttributePolicy struct {
	rules []attributeRule
}

type attributeRule struct {
	operation SpanOperation
	key       AttributeKey
	action    itelemetry.AttributeAction
	maxBytes  int64
}

// AttributePolicyOption configures a SpanAttributePolicy.
type AttributePolicyOption func(*SpanAttributePolicy)

// AttributeOption configures a single attribute rule.
type AttributeOption func(*attributeRule)

// WithSpanAttributePolicy registers span attribute rules during trace.Start.
func WithSpanAttributePolicy(opts ...AttributePolicyOption) Option {
	return func(o *options) {
		o.ensureSpanAttributePolicy()
		for _, opt := range opts {
			opt(o.spanAttributePolicy)
		}
	}
}

// WithAttributeRule registers a rule for operation/key.
// Later rules override earlier rules for the same operation/key pair.
func WithAttributeRule(op SpanOperation, key AttributeKey, opts ...AttributeOption) AttributePolicyOption {
	return func(p *SpanAttributePolicy) {
		rule := attributeRule{
			operation: op,
			key:       key,
			action:    itelemetry.AttributeCapture,
		}
		for _, opt := range opts {
			opt(&rule)
		}
		p.rules = append(p.rules, rule)
	}
}

// Drop skips marshaling and does not write the attribute.
func Drop() AttributeOption {
	return func(r *attributeRule) {
		r.action = itelemetry.AttributeDrop
	}
}

// Omit writes an omitted envelope without original content.
// Without MaxBytes, marshal is skipped. With MaxBytes, JSON-backed payloads are
// still fully marshaled to compare size; only raw []byte paths can omit without marshal.
func Omit() AttributeOption {
	return func(r *attributeRule) {
		r.action = itelemetry.AttributeOmit
	}
}

// MaxBytes sets the byte threshold for Omit or Truncate actions.
func MaxBytes(n int64) AttributeOption {
	return func(r *attributeRule) {
		r.maxBytes = n
	}
}

// Truncate sets MaxBytes and truncates values that exceed the limit.
// Truncate still performs a full marshal; it only limits exported attribute size.
func Truncate(n int64) AttributeOption {
	return func(r *attributeRule) {
		r.maxBytes = n
		r.action = itelemetry.AttributeTruncate
	}
}

// SetSpanAttributePolicy installs the global span attribute policy.
func SetSpanAttributePolicy(policy SpanAttributePolicy) {
	itelemetry.SetSpanAttributePolicy(toInternalSpanAttributePolicy(policy))
}

func (o *options) ensureSpanAttributePolicy() {
	if o.spanAttributePolicy == nil {
		o.spanAttributePolicy = &SpanAttributePolicy{}
	}
}

func toInternalSpanAttributePolicy(policy SpanAttributePolicy) itelemetry.SpanAttributePolicy {
	if len(policy.rules) == 0 {
		return itelemetry.SpanAttributePolicy{}
	}
	out := itelemetry.SpanAttributePolicy{}
	for _, r := range policy.rules {
		out = itelemetry.AppendAttributeRule(out, itelemetry.AttributeRule{
			Operation: string(r.operation),
			Key:       string(r.key),
			Action:    r.action,
			MaxBytes:  r.maxBytes,
		})
	}
	return out
}
