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
	"fmt"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
)

// OverflowOmitPlaceholder is written in Omit overflow mode.
const OverflowOmitPlaceholder = "<omitted: payload exceeded inline_max_bytes>"

// PayloadEnvelope is the JSON shape for overflowed span attribute values.
type PayloadEnvelope struct {
	Truncated     bool   `json:"truncated,omitempty"`
	Omitted       bool   `json:"omitted,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	SHA256        string `json:"sha256"`
	OriginalBytes int64  `json:"original_bytes"`
}

// appendStringAttribute adds a marshaled string attribute when allowed by payload policy.
// If notSerializable is empty, marshal/format failures are skipped (best-effort attributes).
// If notSerializable is non-empty, failures write that placeholder instead.
func appendStringAttribute(
	attrs []attribute.KeyValue,
	operation, key, notSerializable string,
	marshal func() ([]byte, error),
) []attribute.KeyValue {
	if !AllowAttribute(operation, key) {
		return attrs
	}
	bts, err := marshal()
	if err != nil {
		return appendOnFailure(attrs, key, notSerializable)
	}
	value, err := formatPayloadValue(bts)
	if err != nil {
		return appendOnFailure(attrs, key, notSerializable)
	}
	return append(attrs, attribute.String(key, value))
}

func appendOnFailure(attrs []attribute.KeyValue, key, notSerializable string) []attribute.KeyValue {
	if notSerializable == "" {
		return attrs
	}
	return append(attrs, attribute.String(key, notSerializable))
}

func formatPayloadValue(bts []byte) (string, error) {
	limit := InlineMaxBytes()
	if limit <= 0 || int64(len(bts)) <= limit {
		return string(bts), nil
	}
	return applyOverflow(bts, limit, OverflowModeValue())
}

func applyOverflow(bts []byte, limit int64, mode OverflowMode) (string, error) {
	sum := sha256.Sum256(bts)
	envelope := PayloadEnvelope{
		SHA256:        hex.EncodeToString(sum[:]),
		OriginalBytes: int64(len(bts)),
	}
	switch mode {
	case OverflowOmit:
		envelope.Omitted = true
	default:
		envelope.Truncated = true
		envelope.Prefix = utf8SafePrefix(bts, int(limit))
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal payload envelope: %w", err)
	}
	return string(out), nil
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
