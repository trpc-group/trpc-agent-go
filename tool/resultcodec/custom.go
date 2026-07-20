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
	"fmt"
	"reflect"
	"runtime/debug"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Custom returns a Codec that delegates to a typed encoder. The tool result is
// asserted to T before calling encode; a type mismatch returns a clear error so
// business code never has to assert `any` itself. A panic inside encode is
// recovered and converted to an error, matching the framework's tool and
// callback panic-protection convention.
func Custom[T any](encode func(context.Context, T) (string, error)) Codec {
	return &customCodec[T]{encode: encode}
}

type customCodec[T any] struct {
	encode func(context.Context, T) (string, error)
}

// Encode asserts result to T and runs the typed encoder under panic protection.
func (c *customCodec[T]) Encode(ctx context.Context, result any) (s string, err error) {
	if c.encode == nil {
		return "", fmt.Errorf("resultcodec: Custom encoder is nil")
	}
	typed, ok := result.(T)
	if !ok {
		// A nil result cannot be asserted to T directly (a nil interface has no
		// dynamic type), but it is a valid value for nilable type parameters
		// such as interfaces, pointers, maps, and slices.
		if result != nil || !nilableType[T]() {
			return "", fmt.Errorf(
				"resultcodec: Custom encoder expected %s, got %T",
				typeString[T](), result,
			)
		}
		typed = *new(T)
	}
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				ctx,
				log.PanicPrefix+" resultcodec Custom encoder panic: %v\n%s",
				r, string(debug.Stack()),
			)
			err = fmt.Errorf("resultcodec: Custom encoder panic: %v", r)
		}
	}()
	out, encErr := c.encode(ctx, typed)
	if encErr != nil {
		return "", encErr
	}
	// Enforce the built-in codec UTF-8 guarantee for the encoder's output too.
	return toValidUTF8(out), nil
}

// typeString returns the type name of T for error messages, working even when
// the zero value of T is nil (for example interfaces).
func typeString[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().String()
}

// nilableType reports whether T can hold a nil value.
func nilableType[T any]() bool {
	switch reflect.TypeOf((*T)(nil)).Elem().Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Map,
		reflect.Slice, reflect.Chan, reflect.Func:
		return true
	default:
		return false
	}
}
