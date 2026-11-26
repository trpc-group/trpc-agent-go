//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsearch_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type semanticBenchmarkEmbedder struct {
	vector []float64
	calls  int
}

func (e *semanticBenchmarkEmbedder) GetEmbedding(context.Context, string) ([]float64, error) {
	e.calls++
	return e.vector, nil
}

func (e *semanticBenchmarkEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, err := e.GetEmbedding(ctx, text)
	return v, nil, err
}

func (e *semanticBenchmarkEmbedder) GetDimensions() int { return len(e.vector) }

// BenchmarkSemanticSearch measures handler time, allocations, and embedder
// calls with an in-process mock and 1536-dimensional vectors.
func BenchmarkSemanticSearch(b *testing.B) {
	for _, size := range []int{64, 1024} {
		req := &responses.ToolSearchRequest{Query: "find customer records"}
		for i := 0; i < size; i++ {
			entry := semanticEntry(fmt.Sprintf("lookup_%04d", i), "Find a customer record.")
			entry.Namespace = "crm"
			entry.NamespaceDescription = "Tools for customer records."
			entry.Tool.Declaration().InputSchema.Properties = map[string]*tool.Schema{
				"account_id": {Type: "string", Description: "Customer account identifier."},
				"region":     {Type: "string", Description: "Customer region."},
			}
			req.Candidates = append(req.Candidates, entry)
		}
		for _, warm := range []bool{false, true} {
			b.Run(fmt.Sprintf("candidates=%d/warm=%v", size, warm), func(b *testing.B) {
				ctx := context.Background()
				embeddings := &semanticBenchmarkEmbedder{vector: make([]float64, 1536)}
				for i := range embeddings.vector {
					embeddings.vector[i] = float64(i%17 + 1)
				}
				var handler responses.ToolSearchHandler
				var err error
				if warm {
					handler, err = toolsearch.NewSemantic(embeddings)
					require.NoError(b, err)
					_, err = handler(ctx, req)
					require.NoError(b, err)
				}
				embeddings.calls = 0
				b.ReportAllocs()
				for b.Loop() {
					if !warm {
						handler, err = toolsearch.NewSemantic(embeddings)
						if err != nil {
							b.Fatal(err)
						}
					}
					got, err := handler(ctx, req)
					if err != nil || len(got) != 5 {
						b.Fatalf("search returned %d results, %v", len(got), err)
					}
				}
				b.ReportMetric(float64(embeddings.calls)/float64(b.N), "embedcalls/op")
			})
		}
	}
}
