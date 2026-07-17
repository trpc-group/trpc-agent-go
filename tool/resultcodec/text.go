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

// Encode returns the textual form of a text-typed result as valid UTF-8. A panic
// from a business MarshalText is recovered and returned as an error.
func (textCodec) Encode(_ context.Context, result any) (s string, err error) {
	defer recoverCodecPanic("Text", &err)
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
		// A typed-nil pointer still matches this case; calling MarshalText on it
		// may panic, so treat nilable nil values as the empty string.
		if isNilValue(v) {
			return "", nil
		}
		b, mErr := v.MarshalText()
		if mErr != nil {
			return "", mErr
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

// isNilValue reports whether v holds a nil value for a nilable kind, so callers
// can avoid invoking methods on a typed-nil receiver.
func isNilValue(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
