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
)

// Text returns a Codec for results that are already text. It handles strings,
// byte text, json.RawMessage, named string and byte-slice types, and the
// standard encoding.TextMarshaler interface. A nil result encodes to the empty
// string.
//
// Text does not infer layout for structured results; any other type returns a
// clear error. Use Custom for business-specific templates, field layout, or
// truncation.
func Text() Codec {
	return textCodec{}
}

type textCodec struct{}

// Encode returns the textual form of a text-typed result.
func (textCodec) Encode(_ context.Context, result any) (string, error) {
	switch v := result.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case json.RawMessage:
		return string(v), nil
	case []byte:
		return string(v), nil
	case encoding.TextMarshaler:
		b, err := v.MarshalText()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	// Handle named string types and byte slices via reflection so results such
	// as `type Status string` are treated as text.
	rv := reflect.ValueOf(result)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return string(rv.Bytes()), nil
		}
	}
	return "", fmt.Errorf(
		"resultcodec: Text cannot encode %T; use Custom for structured results",
		result,
	)
}
