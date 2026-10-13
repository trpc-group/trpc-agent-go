//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package json provides number-preserving JSON decoding for knowledge documents.
package json

import (
	"bytes"
	"encoding/json"
)

// Unmarshal decodes a single JSON value, preserving numbers in interface values
// as json.Number. Like json.Unmarshal, it rejects trailing non-whitespace content
// before decoding into dest.
func Unmarshal(raw []byte, dest any) error {
	var value json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	return decoder.Decode(dest)
}
