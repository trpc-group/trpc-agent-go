//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// keyString returns a KeyValue for a string attribute.
func keyString(k, v string) attribute.KeyValue {
	return attribute.String(k, v)
}

// keyBool returns a KeyValue for a bool attribute.
func keyBool(k string, v bool) attribute.KeyValue {
	return attribute.Bool(k, v)
}

// keyInt returns a KeyValue for an int attribute.
func keyInt(k string, v int) attribute.KeyValue {
	return attribute.Int(k, v)
}

// keyInt64 returns a KeyValue for an int64 attribute.
func keyInt64(k string, v int64) attribute.KeyValue {
	return attribute.Int64(k, v)
}

// keyStringSlice returns a KeyValue for a string slice attribute.
func keyStringSlice(k string, v []string) attribute.KeyValue {
	return attribute.StringSlice(k, v)
}

// Ensure trace.Span is referenced for the imports used by telemetry.go.
var _ = trace.SpanFromContext
