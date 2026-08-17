//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"fmt"
	"testing"
)

var toolCallbackBenchmarkSink *BeforeToolResult

func BenchmarkCallbacksRunBeforeTool(b *testing.B) {
	for _, callbackCount := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("callbacks=%d", callbackCount), func(b *testing.B) {
			callbacks := NewCallbacks()
			for i := 0; i < callbackCount; i++ {
				callbacks.BeforeTool = append(
					callbacks.BeforeTool,
					func(context.Context, *BeforeToolArgs) (*BeforeToolResult, error) {
						return nil, nil
					},
				)
			}
			args := &BeforeToolArgs{
				ToolCallID: "benchmark-call",
				ToolName:   "benchmark_tool",
			}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := callbacks.RunBeforeTool(ctx, args)
				if err != nil {
					b.Fatal(err)
				}
				toolCallbackBenchmarkSink = result
			}
		})
	}
}
