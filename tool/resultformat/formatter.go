//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package resultformat formats tool results for model-visible tool messages.
package resultformat

import (
	"context"
	"fmt"
	"reflect"
)

// Formatter formats a final tool result as model-visible message content.
// Implementations may be called concurrently and must provide their own
// synchronization when they hold mutable state.
type Formatter interface {
	Format(ctx context.Context, result any) (string, error)
}

// FormatterFunc adapts a typed formatting function to Formatter. The result is
// checked against T before the function is called. Nil results are accepted
// when T can hold nil.
type FormatterFunc[T any] func(context.Context, T) (string, error)

// Format implements Formatter.
func (f FormatterFunc[T]) Format(
	ctx context.Context,
	result any,
) (string, error) {
	if f == nil {
		return "", fmt.Errorf("resultformat: FormatterFunc is nil")
	}
	typed, ok := result.(T)
	if !ok {
		if result != nil || !isNilable[T]() {
			return "", fmt.Errorf(
				"resultformat: FormatterFunc expected %s, got %T",
				typeName[T](),
				result,
			)
		}
		typed = *new(T)
	}
	return f(ctx, typed)
}

func typeName[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().String()
}

func isNilable[T any]() bool {
	switch reflect.TypeOf((*T)(nil)).Elem().Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
