//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package function

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type concurrencyInput struct{}

type concurrencyOutput struct{}

func concurrencyFn(context.Context, concurrencyInput) (concurrencyOutput, error) {
	return concurrencyOutput{}, nil
}

func concurrencyStreamFn(context.Context, concurrencyInput) (*tool.StreamReader, error) {
	return nil, nil
}

// The default must stay true. A function tool now always satisfies
// tool.ConcurrencyAware, so a false default would take every existing function
// tool off the parallel path without its author changing anything.
func TestFunctionToolConcurrencySafeDefaultsToTrue(t *testing.T) {
	ft := NewFunctionTool(concurrencyFn, WithName("f"), WithDescription("d"))
	if !ft.IsConcurrencySafe() {
		t.Error("a function tool must default to concurrency-safe")
	}
	if !tool.MetadataOf(tool.Tool(ft)).ConcurrencySafe {
		t.Error("the default must also resolve as safe through tool.MetadataOf")
	}
}

func TestFunctionToolConcurrencySafeOptIn(t *testing.T) {
	tests := []struct {
		name string
		safe bool
	}{
		{"declared unsafe", false},
		{"declared safe", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := NewFunctionTool(
				concurrencyFn,
				WithName("f"),
				WithDescription("d"),
				WithConcurrencySafe(tt.safe),
			)
			if got := ft.IsConcurrencySafe(); got != tt.safe {
				t.Fatalf("IsConcurrencySafe() = %v, want %v", got, tt.safe)
			}
			if got := tool.MetadataOf(tool.Tool(ft)).ConcurrencySafe; got != tt.safe {
				t.Fatalf("tool.MetadataOf().ConcurrencySafe = %v, want %v", got, tt.safe)
			}
		})
	}
}

// The streamable tool shares the option type, so it must share the behavior.
func TestStreamableFunctionToolConcurrencySafe(t *testing.T) {
	st := NewStreamableFunctionTool[concurrencyInput, concurrencyOutput](
		concurrencyStreamFn,
		WithName("s"),
		WithDescription("d"),
	)
	if !st.IsConcurrencySafe() {
		t.Error("a streamable function tool must default to concurrency-safe")
	}

	unsafe := NewStreamableFunctionTool[concurrencyInput, concurrencyOutput](
		concurrencyStreamFn,
		WithName("s"),
		WithDescription("d"),
		WithConcurrencySafe(false),
	)
	if tool.MetadataOf(tool.Tool(unsafe)).ConcurrencySafe {
		t.Error("WithConcurrencySafe(false) must reach tool.MetadataOf")
	}
}
