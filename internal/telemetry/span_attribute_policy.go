//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"sync/atomic"
)

// AttributeAction controls how a span attribute value is produced.
type AttributeAction int

const (
	// AttributeCapture writes the full serialized value (default).
	AttributeCapture AttributeAction = iota
	// AttributeDrop skips marshaling and does not write the attribute.
	AttributeDrop
	// AttributeOmit writes an omitted envelope without original content.
	AttributeOmit
	// AttributeTruncate writes a truncated envelope when MaxBytes is exceeded.
	AttributeTruncate
)

// AttributeRule is an immutable span attribute production rule.
type AttributeRule struct {
	Operation string
	Key       string
	Action    AttributeAction
	MaxBytes  int64
}

// SpanAttributePolicy controls production-side span attribute behavior.
type SpanAttributePolicy struct {
	rules []AttributeRule
}

var spanAttributePolicy atomic.Pointer[SpanAttributePolicy]

// SetSpanAttributePolicy installs the global span attribute policy. Zero value restores defaults.
func SetSpanAttributePolicy(policy SpanAttributePolicy) {
	if policy.isEmpty() {
		spanAttributePolicy.Store(nil)
		return
	}
	p := policy.clone()
	spanAttributePolicy.Store(&p)
}

// CurrentSpanAttributePolicy returns the installed span attribute policy.
func CurrentSpanAttributePolicy() SpanAttributePolicy {
	p := spanAttributePolicy.Load()
	if p == nil {
		return SpanAttributePolicy{}
	}
	return p.clone()
}

// InstallSpanAttributePolicy installs policy and returns a restore function for the previous policy.
func InstallSpanAttributePolicy(policy SpanAttributePolicy) (restore func()) {
	prev := spanAttributePolicy.Load()
	SetSpanAttributePolicy(policy)
	return func() {
		spanAttributePolicy.Store(prev)
	}
}

// Resolve returns the effective rule for operation/key. No match yields default Capture.
func Resolve(operation, key string) AttributeRule {
	p := spanAttributePolicy.Load()
	if p == nil {
		return AttributeRule{Action: AttributeCapture}
	}
	return p.resolve(operation, key)
}

func (p SpanAttributePolicy) isEmpty() bool {
	return len(p.rules) == 0
}

func (p SpanAttributePolicy) clone() SpanAttributePolicy {
	if len(p.rules) == 0 {
		return SpanAttributePolicy{}
	}
	out := make([]AttributeRule, len(p.rules))
	copy(out, p.rules)
	return SpanAttributePolicy{rules: out}
}

func (p *SpanAttributePolicy) resolve(operation, key string) AttributeRule {
	for i := len(p.rules) - 1; i >= 0; i-- {
		r := p.rules[i]
		if r.Key != key {
			continue
		}
		if r.Operation != "" && r.Operation != operation {
			continue
		}
		return r
	}
	return AttributeRule{Action: AttributeCapture}
}

// Rules returns a copy of the configured rules.
func (p SpanAttributePolicy) Rules() []AttributeRule {
	if len(p.rules) == 0 {
		return nil
	}
	out := make([]AttributeRule, len(p.rules))
	copy(out, p.rules)
	return out
}

// AppendAttributeRule appends a rule; later rules override earlier rules for the same operation/key.
func AppendAttributeRule(policy SpanAttributePolicy, rule AttributeRule) SpanAttributePolicy {
	out := policy.clone()
	out.rules = append(out.rules, rule)
	return out
}
