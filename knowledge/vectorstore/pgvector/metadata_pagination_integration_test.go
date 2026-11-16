//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestMetadataPaginationStableDuringUpdatesIntegration exercises metadata pagination against a real PostgreSQL database.
// Set PGVECTOR_TEST_DSN to opt in; the test creates and drops a uniquely named table.
func TestMetadataPaginationStableDuringUpdatesIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PGVECTOR_TEST_DSN"))
	if dsn == "" {
		t.Skip("set PGVECTOR_TEST_DSN to run the pgvector metadata pagination integration test")
	}
	for _, count := range []int{256, 257, 1024} {
		t.Run(fmt.Sprintf("documents_%d", count), func(t *testing.T) {
			runMetadataPaginationDuringUpdates(t, dsn, count)
		})
	}
}

func runMetadataPaginationDuringUpdates(t *testing.T, dsn string, targetDocumentNum int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const (
		pageSize    = 256
		nonTargetID = "pagination_other_revision"
	)
	table := fmt.Sprintf("test_metadata_pagination_%d", time.Now().UnixNano())
	vs, err := New(
		WithPGVectorClientDSN(dsn),
		WithTable(table),
		WithIndexDimension(3),
		WithEnableTSVector(false),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := vs.Close(); err != nil {
			t.Errorf("close vector store: %v", err)
		}
	})
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := vs.client.ExecContext(cleanupCtx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Errorf("drop test table: %v", err)
		}
	})

	const createdAt = int64(1_700_000_000)
	_, err = vs.client.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, name, content, embedding, metadata, created_at, updated_at)
		SELECT 'pagination_target_' || n::text,
		       'pagination test',
		       'same timestamp metadata pagination test',
		       '[0.1, 0.2, 0.3]'::vector,
		       '{"index_revision":"target","datasource_enabled":"false","index_active":"false"}'::jsonb,
		       $1,
		       $1
		FROM generate_series(1, $2) AS n`, table), createdAt, targetDocumentNum)
	require.NoError(t, err)
	_, err = vs.client.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, name, content, embedding, metadata, created_at, updated_at)
		VALUES ($1, 'pagination test', 'non-target revision', '[0.1, 0.2, 0.3]'::vector,
		        '{"index_revision":"other","datasource_enabled":"false","index_active":"false"}'::jsonb, $2, $2)`, table), nonTargetID, createdAt)
	require.NoError(t, err)

	seenTargetIDs := make(map[string]struct{}, targetDocumentNum)
	var (
		offset             int
		pageCount          int
		returnedRows       int
		repeatedTargetRows int
		updatedRows        int64
	)
	for {
		page, err := vs.GetMetadata(ctx,
			vectorstore.WithGetMetadataFilter(map[string]any{"index_revision": "target"}),
			vectorstore.WithGetMetadataLimit(pageSize),
			vectorstore.WithGetMetadataOffset(offset),
		)
		require.NoError(t, err)
		pageCount++
		if len(page) == 0 {
			break
		}
		returnedRows += len(page)

		targetIDs := make([]string, 0, len(page))
		for id, metadata := range page {
			require.Equal(t, "target", metadata.Metadata["index_revision"])
			if _, exists := seenTargetIDs[id]; exists {
				repeatedTargetRows++
			} else {
				seenTargetIDs[id] = struct{}{}
			}
			targetIDs = append(targetIDs, id)
		}

		if len(targetIDs) > 0 {
			sort.Strings(targetIDs)
			updated, err := vs.UpdateByFilter(ctx,
				vectorstore.WithUpdateByFilterDocumentIDs(targetIDs),
				vectorstore.WithUpdateByFilterUpdates(map[string]any{
					"metadata.datasource_enabled": "true",
					"metadata.index_active":       "true",
				}),
			)
			require.NoError(t, err)
			updatedRows += updated
		}

		if len(page) < pageSize {
			break
		}
		offset += len(page)
	}

	allMetadata, err := vs.GetMetadata(ctx)
	require.NoError(t, err)
	missingTargets := 0
	incorrectTargetFlags := 0
	for n := 1; n <= targetDocumentNum; n++ {
		id := fmt.Sprintf("pagination_target_%d", n)
		metadata, exists := allMetadata[id]
		if !exists {
			missingTargets++
			continue
		}
		if metadata.Metadata["datasource_enabled"] != "true" || metadata.Metadata["index_active"] != "true" {
			incorrectTargetFlags++
		}
	}
	nonTargetMetadata, nonTargetExists := allMetadata[nonTargetID]
	nonTargetChanged := !nonTargetExists || nonTargetMetadata.Metadata["index_revision"] != "other" ||
		nonTargetMetadata.Metadata["datasource_enabled"] != "false" || nonTargetMetadata.Metadata["index_active"] != "false"

	t.Logf("metadata pagination: pages=%d returned_rows=%d unique_target_ids=%d repeated_target_rows=%d updated_rows=%d missing_targets=%d incorrect_target_flags=%d non_target_changed=%t",
		pageCount, returnedRows, len(seenTargetIDs), repeatedTargetRows, updatedRows, missingTargets, incorrectTargetFlags, nonTargetChanged)

	require.Equal(t, targetDocumentNum+1, len(allMetadata))
	require.Equal(t, targetDocumentNum, returnedRows, "pagination should return each matching row exactly once")
	require.Equal(t, targetDocumentNum, len(seenTargetIDs), "pagination should visit every target document")
	require.Zero(t, repeatedTargetRows, "pagination should not revisit target documents across pages")
	require.Zero(t, missingTargets, "all target documents should remain visible after updates")
	require.Zero(t, incorrectTargetFlags, "all target documents should have both publication flags enabled")
	require.False(t, nonTargetChanged, "the non-target revision should remain unchanged")
}
