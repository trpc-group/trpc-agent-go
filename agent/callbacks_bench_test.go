//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"fmt"
	"testing"
)

var agentCallbackBenchmarkSink *BeforeAgentResult

func BenchmarkCallbacksRunBeforeAgent(b *testing.B) {
	for _, callbackCount := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("callbacks=%d", callbackCount), func(b *testing.B) {
			callbacks := NewCallbacks()
			for i := 0; i < callbackCount; i++ {
				callbacks.BeforeAgent = append(
					callbacks.BeforeAgent,
					func(context.Context, *BeforeAgentArgs) (*BeforeAgentResult, error) {
						return nil, nil
					},
				)
			}
			args := &BeforeAgentArgs{Invocation: NewInvocation()}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := callbacks.RunBeforeAgent(ctx, args)
				if err != nil {
					b.Fatal(err)
				}
				agentCallbackBenchmarkSink = result
			}
		})
	}
}
