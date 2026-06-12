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

// OverflowMode controls how serialized payloads larger than InlineMaxBytes are written.
type OverflowMode int

const (
	OverflowTruncate OverflowMode = iota
	OverflowOmit
)

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
type PayloadPolicy struct {
	Attributes     AttributeRules
	InlineMaxBytes int64
	OverflowMode   OverflowMode
}

var payloadPolicy atomic.Pointer[PayloadPolicy]

// SetPayloadPolicy installs the global payload policy. Zero value restores defaults.
func SetPayloadPolicy(policy PayloadPolicy) {
	if isEmptyPayloadPolicy(policy) {
		payloadPolicy.Store(nil)
		return
	}
	p := policy
	payloadPolicy.Store(&p)
}

// CurrentPayloadPolicy returns the installed payload policy.
func CurrentPayloadPolicy() PayloadPolicy {
	p := payloadPolicy.Load()
	if p == nil {
		return PayloadPolicy{}
	}
	return *p
}

// AllowAttribute reports whether an attribute should be marshaled for operation/key.
func AllowAttribute(operation, key string) bool {
	p := payloadPolicy.Load()
	if p == nil {
		return true
	}
	return p.allow(operation, key)
}

// InlineMaxBytes returns the configured inline byte limit. 0 means unlimited.
func InlineMaxBytes() int64 {
	p := payloadPolicy.Load()
	if p == nil {
		return 0
	}
	return p.InlineMaxBytes
}

// OverflowModeValue returns the configured overflow mode.
func OverflowModeValue() OverflowMode {
	p := payloadPolicy.Load()
	if p == nil {
		return OverflowTruncate
	}
	return p.OverflowMode
}

func isEmptyPayloadPolicy(policy PayloadPolicy) bool {
	return len(policy.Attributes.Enabled) == 0 &&
		len(policy.Attributes.Disabled) == 0 &&
		policy.InlineMaxBytes == 0
}

func (p *PayloadPolicy) allow(operation, key string) bool {
	if p == nil {
		return true
	}
	if len(p.Attributes.Enabled) > 0 && !p.matchesAny(p.Attributes.Enabled, operation, key) {
		return false
	}
	if p.matchesAny(p.Attributes.Disabled, operation, key) {
		return false
	}
	return true
}

func (p *PayloadPolicy) matchesAny(selectors []AttributeSelector, operation, key string) bool {
	for _, s := range selectors {
		if s.Key != key {
			continue
		}
		if s.Operation != "" && s.Operation != operation {
			continue
		}
		return true
	}
	return false
}

// MergeAttributeRules appends disabled/enabled selectors from src into dst.
func MergeAttributeRules(dst, src AttributeRules) AttributeRules {
	out := dst
	if len(src.Enabled) > 0 {
		out.Enabled = append(append([]AttributeSelector{}, out.Enabled...), src.Enabled...)
	}
	if len(src.Disabled) > 0 {
		out.Disabled = append(append([]AttributeSelector{}, out.Disabled...), src.Disabled...)
	}
	return out
}
