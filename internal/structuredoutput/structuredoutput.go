//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package structuredoutput builds structured output schema data from Go types.
package structuredoutput

import (
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonschema"
)

// Name returns the provider-facing structured output schema name.
func Name(name string) string {
	if name == "" {
		return "output"
	}
	return name
}

// FromType returns the schema name, generated JSON schema, and pointer type.
func FromType(examplePtr any, strict bool) (string, map[string]any, reflect.Type) {
	t := TypeOf(examplePtr)
	if t == nil {
		return "", nil, nil
	}
	genOpts := make([]jsonschema.Option, 0, 1)
	if strict {
		genOpts = append(genOpts, jsonschema.WithStrict())
	}
	gen := jsonschema.New(genOpts...)
	schema := gen.Generate(t.Elem())
	name := t.Elem().Name()
	return Name(name), schema, t
}

// TypeOf returns the pointer type used for typed structured output.
func TypeOf(examplePtr any) reflect.Type {
	if examplePtr == nil {
		return nil
	}
	rt := reflect.TypeOf(examplePtr)
	if rt.Kind() == reflect.Pointer {
		return rt
	}
	return reflect.PointerTo(rt)
}
