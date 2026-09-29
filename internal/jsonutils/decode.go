//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package jsonutils provides helpers for decoding JSON from LLM or stage output
// that may be malformed or include trailing prose.
package jsonutils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
)

// decodeOnce unmarshals the first JSON value from raw, ignoring trailing content.
func decodeOnce(raw string, dest any) error {
	return json.NewDecoder(strings.NewReader(raw)).Decode(dest)
}

// decodeWithRepair tries strict decode first, then repairs malformed JSON via
// internal/jsonrepair before unmarshaling. Valid JSON never passes through Repair.
// A json.RawMessage buffer avoids mutating dest when strict decode fails partway
// through (e.g. appending to slices/maps) before repair succeeds.
func decodeWithRepair(raw string, dest any) error {
	var buf json.RawMessage
	if err := decodeOnce(raw, &buf); err == nil {
		return json.Unmarshal(buf, dest)
	}
	repaired, err := jsonrepair.Repair([]byte(raw))
	if err != nil {
		return fmt.Errorf("repair JSON: %w", err)
	}
	if err := decodeOnce(string(repaired), &buf); err != nil {
		return fmt.Errorf("decode repaired JSON: %w", err)
	}
	return json.Unmarshal(buf, dest)
}

// DecodeLeadingJSON unmarshals the first JSON value from raw LLM or stage output,
// ignoring any trailing prose, markdown fences, or footers after that value.
// Malformed JSON is repaired via internal/jsonrepair before unmarshaling.
func DecodeLeadingJSON(raw string, dest any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty JSON input")
	}
	return decodeWithRepair(raw, dest)
}

// DecodeFlexibleJSON unmarshals a JSON value from text that may contain leading
// prose or trailing footers. It tries to decode from the start first, then from
// the first '{' or '[' when models prepend explanatory text.
// Malformed JSON is repaired via internal/jsonrepair when strict decode fails.
func DecodeFlexibleJSON(raw string, dest any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty JSON input")
	}
	err := decodeWithRepair(raw, dest)
	if err == nil {
		return nil
	}
	start := strings.IndexAny(raw, "{[")
	if start < 0 {
		return fmt.Errorf("no JSON value found")
	}
	if start == 0 {
		return err
	}
	return decodeWithRepair(raw[start:], dest)
}

// UnmarshalUseNumber decodes a single JSON value, preserving numbers in interface
// values as json.Number. Like json.Unmarshal, it rejects trailing non-whitespace
// content before decoding into dest.
func UnmarshalUseNumber(raw []byte, dest any) error {
	var value json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	return decoder.Decode(dest)
}
