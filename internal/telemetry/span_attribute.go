//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// attributeEnvelope is the JSON shape for omitted or truncated span attribute values.
type attributeEnvelope struct {
	Truncated     bool   `json:"truncated,omitempty"`
	Omitted       bool   `json:"omitted,omitempty"`
	// Prefix is a UTF-8-safe byte-prefix of the original serialized value, not necessarily valid JSON.
	Prefix        string `json:"prefix,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	OriginalBytes int64  `json:"original_bytes,omitempty"`
}

// appendStringAttribute adds a marshaled string attribute according to span attribute policy.
// If notSerializable is empty, marshal/format failures are skipped (best-effort attributes).
// If notSerializable is non-empty, failures write that placeholder instead.
func appendStringAttribute(
	attrs []attribute.KeyValue,
	operation, key, notSerializable string,
	marshal func() ([]byte, error),
) []attribute.KeyValue {
	rule := Resolve(operation, key)
	if rule.Action == AttributeDrop {
		return attrs
	}
	if rule.Action == AttributeOmit && rule.MaxBytes <= 0 {
		return append(attrs, attribute.String(key, unconditionalOmitEnvelope()))
	}
	bts, err := marshal()
	if err != nil {
		return appendOnFailure(attrs, key, notSerializable)
	}
	value, ok := formatAttributeValue(bts, rule)
	if !ok {
		return attrs
	}
	return append(attrs, attribute.String(key, value))
}

// setStringAttribute sets a single marshaled string attribute on a span.
func setStringAttribute(
	span trace.Span,
	operation, key, notSerializable string,
	marshal func() ([]byte, error),
) {
	attrs := appendStringAttribute(nil, operation, key, notSerializable, marshal)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// setBytesAttribute sets a raw byte attribute according to span attribute policy without JSON marshaling.
func setBytesAttribute(span trace.Span, operation, key string, bts []byte) {
	rule := Resolve(operation, key)
	if rule.Action == AttributeDrop {
		return
	}
	if rule.Action == AttributeOmit && rule.MaxBytes <= 0 {
		span.SetAttributes(attribute.String(key, unconditionalOmitEnvelope()))
		return
	}
	value, ok := formatAttributeValue(bts, rule)
	if !ok {
		return
	}
	span.SetAttributes(attribute.String(key, value))
}

func appendOnFailure(attrs []attribute.KeyValue, key, notSerializable string) []attribute.KeyValue {
	if notSerializable == "" {
		return attrs
	}
	return append(attrs, attribute.String(key, notSerializable))
}

func formatAttributeValue(bts []byte, rule AttributeRule) (string, bool) {
	switch rule.Action {
	case AttributeDrop:
		return "", false
	case AttributeOmit:
		if rule.MaxBytes <= 0 {
			return unconditionalOmitEnvelope(), true
		}
		if int64(len(bts)) <= rule.MaxBytes {
			return string(bts), true
		}
		return overflowOmitEnvelope(bts)
	case AttributeTruncate:
		if rule.MaxBytes <= 0 || int64(len(bts)) <= rule.MaxBytes {
			return string(bts), true
		}
		return overflowTruncateEnvelope(bts, rule.MaxBytes)
	default:
		return string(bts), true
	}
}

func unconditionalOmitEnvelope() string {
	return `{"omitted":true}`
}

func overflowOmitEnvelope(bts []byte) (string, bool) {
	sum := sha256.Sum256(bts)
	envelope := attributeEnvelope{
		Omitted:       true,
		SHA256:        hex.EncodeToString(sum[:]),
		OriginalBytes: int64(len(bts)),
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", false
	}
	return string(out), true
}

func overflowTruncateEnvelope(bts []byte, limit int64) (string, bool) {
	sum := sha256.Sum256(bts)
	envelope := attributeEnvelope{
		Truncated:     true,
		Prefix:        utf8SafePrefix(bts, int(limit)),
		SHA256:        hex.EncodeToString(sum[:]),
		OriginalBytes: int64(len(bts)),
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", false
	}
	return string(out), true
}

func utf8SafePrefix(bts []byte, limit int) string {
	if limit <= 0 || len(bts) == 0 {
		return ""
	}
	if len(bts) <= limit {
		return string(bts)
	}
	cut := bts[:limit]
	for len(cut) > 0 && !utf8.Valid(cut) {
		cut = cut[:len(cut)-1]
	}
	return string(cut)
}
