//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/replacementtest"
)

type revisionTestEmbedder struct{}

func (revisionTestEmbedder) GetEmbedding(context.Context, string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (e revisionTestEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (revisionTestEmbedder) GetDimensions() int { return 3 }

func TestLatestTurnReplacementIntegration(t *testing.T) {
	dsn := os.Getenv("TRPC_AGENT_GO_PGVECTOR_TEST_DSN")
	if dsn == "" {
		t.Skip("set TRPC_AGENT_GO_PGVECTOR_TEST_DSN to run PGVector integration test")
	}
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			svc, err := NewService(
				WithPostgresClientDSN(dsn),
				WithEmbedder(revisionTestEmbedder{}),
				WithIndexDimension(3),
				WithTablePrefix(fmt.Sprintf("r%x_", time.Now().UnixNano())),
				WithEnableAsyncPersist(async),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, svc.Close()) })
			if async {
				replacementtest.RunAsync(t, svc)
			} else {
				replacementtest.Run(t, svc)
			}
		})
	}
}
