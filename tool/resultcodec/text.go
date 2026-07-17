//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// replacementChar substitutes invalid UTF-8 bytes, matching how the JSON and XML
// codecs sanitize output.
const replacementChar = "\uFFFD"

// Text returns a Codec for results that are already text. It handles strings,
// byte text, json.RawMessage, named string and byte-slice types, and the
// standard encoding.TextMarshaler interface. A nil result encodes to the empty
// string.
//
// Text does not infer layout for structured results; any other type returns a
// clear error. Use Custom for business-specific templates, field layout, or
// truncation.
//
// Like the JSON and XML codecs, Text guarantees valid UTF-8 output: invalid
// byte sequences are replaced with U+FFFD.
func Text() Codec {
	return textCodec{}
}

type textCodec struct{}

// Encode returns the textual form of a text-typed result as valid UTF-8.
func (textCodec) Encode(_ context.Context, result any) (string, error) {
	switch v := result.(type) {
	case nil:
		return "", nil
	case string:
		return toValidUTF8(v), nil
	case json.RawMessage:
		return toValidUTF8(string(v)), nil
	case []byte:
		return toValidUTF8(string(v)), nil
	case encoding.TextMarshaler:
		b, err := v.MarshalText()
		if err != nil {
			return "", err
		}
		return toValidUTF8(string(b)), nil
	}
	// Handle named string types and byte slices via reflection so results such
	// as `type Status string` are treated as text.
	rv := reflect.ValueOf(result)
	switch rv.Kind() {
	case reflect.String:
		return toValidUTF8(rv.String()), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return toValidUTF8(string(rv.Bytes())), nil
		}
	}
	return "", fmt.Errorf(
		"resultcodec: Text cannot encode %T; use Custom for structured results",
		result,
	)
}

// toValidUTF8 returns s unchanged when it is already valid UTF-8, otherwise a
// copy with each invalid byte sequence replaced by U+FFFD.
func toValidUTF8(s string) string {
	return strings.ToValidUTF8(s, replacementChar)
}
