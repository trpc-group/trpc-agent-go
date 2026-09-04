//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func searchRow(id string, distance float64) []any {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return []any{id, "n", "c", []float64{1, 2, 3}, "{}", now, now, distance}
}

func TestSearchNilQuery(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	_, err := vs.Search(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search query is required")
}

func TestSearchUnsupportedMode(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{SearchMode: 99})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported SearchMode")
}

func TestSearchByVector(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			searchRow("doc1", 0.1),
			searchRow("doc2", 0.5),
		}), nil
	}
	vs := vsWithClient(c)
	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1, 2, 3},
		Limit:      5,
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 2)
	assert.Equal(t, "doc1", res.Results[0].Document.ID)
	// cosineDistance 0.1 -> score 0.95 (1 - 0.1/2).
	assert.InDelta(t, 0.95, res.Results[0].Score, 1e-9)
	// The SQL uses cosineDistance and orders ASC.
	require.Len(t, c.queryCalls, 1)
	assert.Contains(t, c.queryCalls[0].query, "cosineDistance(embedding, ?)")
	assert.Contains(t, c.queryCalls[0].query, "ORDER BY _distance ASC")
	assert.Contains(t, c.queryCalls[0].query, "LIMIT ?")
}

func TestSearchByVectorDimMismatch(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1, 2},
	})
	assert.ErrorIs(t, err, errVectorDimMismatch)
}

func TestSearchByVectorMinScore(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{
			searchRow("doc1", 0.1),
			searchRow("doc2", 1.5),
		}), nil
	}
	vs := vsWithClient(c)
	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1, 2, 3},
		Limit:      5,
		MinScore:   0.5,
	})
	require.NoError(t, err)
	// score 0.95 (doc1) passes, score 0.25 (doc2) filtered out.
	require.Len(t, res.Results, 1)
	assert.Equal(t, "doc1", res.Results[0].Document.ID)
}

func TestSearchByFilter(t *testing.T) {
	// No constraints -> empty result.
	vs := vsWithClient(&mockClient{})
	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeFilter})
	require.NoError(t, err)
	assert.Nil(t, res.Results)

	// With IDs.
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "n", "c", []float64{1, 2, 3}, "{}", time.Now(), time.Now()}}), nil
	}
	vs = vsWithClient(c)
	res, err = vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Limit:      5,
		Filter:     &vectorstore.SearchFilter{IDs: []string{"doc1"}},
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	assert.Equal(t, "doc1", res.Results[0].Document.ID)
	assert.Equal(t, 0.0, res.Results[0].Score)
	assert.Contains(t, c.queryCalls[0].query, "id IN (?)")
}

func TestSearchByKeyword(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeKeyword})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyword is required")

	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "n", "c", []float64{1, 2, 3}, "{}", time.Now(), time.Now()}}), nil
	}
	vs = vsWithClient(c)
	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
		Query:      "hello",
		Limit:      5,
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	assert.Contains(t, c.queryCalls[0].query, "positionCaseInsensitive(content, ?) > 0")
}

func TestSearchByHybrid(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeHybrid})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is required")

	_, err = vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Query:      "hello",
		Vector:     []float64{1, 2},
	})
	assert.ErrorIs(t, err, errVectorDimMismatch)

	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{searchRow("doc1", 0.1)}), nil
	}
	vs = vsWithClient(c)
	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Query:      "hello",
		Vector:     []float64{1, 2, 3},
		Limit:      5,
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	// hybrid combines keyword prefilter + vector ranking.
	assert.Contains(t, c.queryCalls[0].query, "positionCaseInsensitive(content, ?) > 0")
	assert.Contains(t, c.queryCalls[0].query, "cosineDistance(embedding, ?)")
	// args order: vector, query, limit.
	assert.Equal(t, []float64{1, 2, 3}, c.queryCalls[0].args[0])
	assert.Equal(t, "hello", c.queryCalls[0].args[1])
}

func TestApplyMinScore(t *testing.T) {
	docs := []*vectorstore.ScoredDocument{{Score: 0.1}, {Score: 0.9}}
	assert.Len(t, applyMinScore(docs, 0), 2)
	assert.Len(t, applyMinScore(docs, 0.5), 1)
	assert.Len(t, applyMinScore(docs, -1), 2)
}

func TestLimitOrDefault(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	assert.Equal(t, 5, vs.limitOrDefault(5))
	assert.Equal(t, vs.option.maxResults, vs.limitOrDefault(0))
	assert.Equal(t, vs.option.maxResults, vs.limitOrDefault(-1))
}

func TestDeleteByFilter(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)

	// Requires IDs / filter / delete-all.
	err := vs.DeleteByFilter(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires DocumentIDs, Filter, or DeleteAll")

	// By IDs.
	err = vs.DeleteByFilter(context.Background(), vectorstore.WithDeleteDocumentIDs([]string{"a", "b"}))
	require.NoError(t, err)
	require.Len(t, c.execCalls, 1)
	assert.Contains(t, c.execCalls[0].query, "ALTER TABLE docs DELETE")
	assert.Contains(t, c.execCalls[0].query, "id IN (?, ?)")
	assert.Equal(t, []any{"a", "b"}, c.execCalls[0].args)

	// By filter.
	vs2 := vsWithClient(&mockClient{}, WithFilterFields(FilterFieldSpec{Name: "category", Type: FilterFieldString}))
	err = vs2.DeleteByFilter(context.Background(), vectorstore.WithDeleteFilter(map[string]any{"category": "news"}))
	require.NoError(t, err)

	// DeleteAll without allow -> error.
	vs3 := vsWithClient(&mockClient{})
	err = vs3.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destructive")

	// DeleteAll with allow.
	vs4 := vsWithClient(&mockClient{}, WithAllowDestructiveDeleteAll(true))
	err = vs4.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true))
	require.NoError(t, err)

	// DeleteAll combined with IDs -> error.
	err = vs4.DeleteByFilter(context.Background(),
		vectorstore.WithDeleteAll(true), vectorstore.WithDeleteDocumentIDs([]string{"a"}))
	require.Error(t, err)
}

func TestCount(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{uint64(42)}}), nil
	}
	vs := vsWithClient(c, WithFilterFields(FilterFieldSpec{Name: "category", Type: FilterFieldString}))
	n, err := vs.Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 42, n)

	// With filter.
	n, err = vs.Count(context.Background(), vectorstore.WithCountFilter(map[string]any{"category": "news"}))
	require.NoError(t, err)
	assert.Equal(t, 42, n)
	assert.Contains(t, c.queryCalls[1].query, "WHERE (category = 'news')")
}

func TestGetMetadata(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", `{"category":"news"}`}}), nil
	}
	vs := vsWithClient(c)
	out, err := vs.GetMetadata(context.Background(), vectorstore.WithGetMetadataIDs([]string{"doc1"}))
	require.NoError(t, err)
	require.Contains(t, out, "doc1")
	assert.Equal(t, "news", out["doc1"].Metadata["category"])

	// Error on invalid options.
	_, err = vs.GetMetadata(context.Background(), vectorstore.WithGetMetadataLimit(0))
	require.Error(t, err)
}

// TestGetMetadataClauseOrder asserts the generated SQL keeps WHERE before
// ORDER BY. Appending the filter after ORDER BY is a syntax error that
// ClickHouse rejects outright.
func TestGetMetadataClauseOrder(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", `{"category":"news"}`}}), nil
	}
	vs := vsWithClient(c)
	_, err := vs.GetMetadata(context.Background(),
		vectorstore.WithGetMetadataIDs([]string{"doc1"}),
		vectorstore.WithGetMetadataLimit(10),
	)
	require.NoError(t, err)
	require.NotEmpty(t, c.queryCalls)
	q := c.queryCalls[0].query
	whereIdx := strings.Index(q, "WHERE")
	orderIdx := strings.Index(q, "ORDER BY")
	require.NotEqual(t, -1, whereIdx, "query must contain WHERE: %s", q)
	require.NotEqual(t, -1, orderIdx, "query must contain ORDER BY: %s", q)
	assert.Less(t, whereIdx, orderIdx, "WHERE must precede ORDER BY: %s", q)
	assert.Less(t, orderIdx, strings.Index(q, "LIMIT"), "ORDER BY must precede LIMIT: %s", q)
}

// TestGetMetadataOffsetConstraints documents how offset interacts with limit.
// A negative limit combined with a positive offset is rejected upstream, which
// is why the unbounded path always starts paging at zero.
func TestGetMetadataOffsetConstraints(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "{}"}}), nil
	}
	vs := vsWithClient(c)

	// Unbounded retrieval with an offset is not a valid combination.
	_, err := vs.GetMetadata(context.Background(),
		vectorstore.WithGetMetadataLimit(-1),
		vectorstore.WithGetMetadataOffset(25),
	)
	require.Error(t, err)

	// A positive limit does honor the offset, which is bound as the last argument.
	_, err = vs.GetMetadata(context.Background(),
		vectorstore.WithGetMetadataLimit(10),
		vectorstore.WithGetMetadataOffset(25),
	)
	require.NoError(t, err)
	require.NotEmpty(t, c.queryCalls)
	args := c.queryCalls[0].args
	require.NotEmpty(t, args)
	assert.Equal(t, 25, args[len(args)-1])
}

// TestGetMetadataPaginationDuplicateIDs asserts pagination advances by the
// number of rows scanned. Paging on the deduplicated map size would stop early
// and silently drop later pages.
func TestGetMetadataPaginationDuplicateIDs(t *testing.T) {
	c := &mockClient{}
	page := 0
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		page++
		if page == 1 {
			// A full page whose rows collapse into a single unique ID.
			rows := make([][]any, defaultBatchSize)
			for i := range rows {
				rows[i] = []any{"dup", "{}"}
			}
			return newMockRows(rows), nil
		}
		if page == 2 {
			return newMockRows([][]any{{"doc2", "{}"}}), nil
		}
		return newMockRows(nil), nil
	}
	vs := vsWithClient(c)
	out, err := vs.GetMetadata(context.Background())
	require.NoError(t, err)
	// Without row-count paging the loop would end after page 1 and miss doc2.
	assert.Contains(t, out, "dup")
	assert.Contains(t, out, "doc2")
}

func TestGetMetadataPagination(t *testing.T) {
	c := &mockClient{}
	page := 0
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		page++
		if page == 1 {
			return newMockRows([][]any{{"doc1", "{}"}}), nil
		}
		return newMockRows(nil), nil
	}
	vs := vsWithClient(c)
	out, err := vs.GetMetadata(context.Background())
	require.NoError(t, err)
	require.Contains(t, out, "doc1")
}

func TestUpdateByFilter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		// First query returns matching IDs; subsequent queries return the doc row.
		if strings.Contains(q, "SELECT id FROM") {
			return newMockRows([][]any{{"doc1"}}), nil
		}
		return newMockRows([][]any{{"doc1", "old", "old", []float64{1, 2, 3}, "{}", now, now}}), nil
	}
	vs := vsWithClient(c)
	n, err := vs.UpdateByFilter(context.Background(),
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "new name"}))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// Validation errors.
	_, err = vs.UpdateByFilter(context.Background())
	require.Error(t, err)
}

func TestApplyUpdatesToDoc(t *testing.T) {
	doc := &document.Document{ID: "d", Name: "n", Content: "c", Metadata: map[string]any{"k": "v"}}
	updates := map[string]any{
		"name":        "new",
		"content":     "new content",
		"metadata.k2": 123,
	}
	updated, emb, err := applyUpdatesToDoc(doc, []float64{1, 2}, updates)
	require.NoError(t, err)
	assert.Equal(t, "new", updated.Name)
	assert.Equal(t, "new content", updated.Content)
	assert.Equal(t, 123, updated.Metadata["k2"])
	assert.Equal(t, "v", updated.Metadata["k"])
	assert.Equal(t, []float64{1, 2}, emb)

	// embedding update.
	updated, emb, err = applyUpdatesToDoc(doc, nil, map[string]any{"embedding": []float64{3, 4}})
	require.NoError(t, err)
	assert.Equal(t, []float64{3, 4}, emb)

	// error cases.
	_, _, err = applyUpdatesToDoc(doc, nil, map[string]any{"name": 123})
	require.Error(t, err)
	_, _, err = applyUpdatesToDoc(doc, nil, map[string]any{"embedding": "bad"})
	require.Error(t, err)
	_, _, err = applyUpdatesToDoc(doc, nil, map[string]any{"metadata.": 1})
	require.Error(t, err)
	_, _, err = applyUpdatesToDoc(doc, nil, map[string]any{"unsupported": 1})
	require.Error(t, err)
}

func TestBuildWhereClause(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
		FilterFieldSpec{Name: "score", Type: FilterFieldInt64},
	))

	// nil filter.
	where, args, err := vs.buildWhereClause(nil)
	require.NoError(t, err)
	assert.Equal(t, "", where)
	assert.Nil(t, args)

	// IDs only.
	where, args, err = vs.buildWhereClause(&vectorstore.SearchFilter{IDs: []string{"a", "b"}})
	require.NoError(t, err)
	assert.Equal(t, " WHERE (id IN (?, ?))", where)
	assert.Equal(t, []any{"a", "b"}, args)

	// A top-level OR combined with an ID set must stay grouped, otherwise AND
	// would bind tighter than OR and match rows outside the ID set.
	where, args, err = vs.buildWhereClause(&vectorstore.SearchFilter{
		IDs: []string{"a"},
		FilterCondition: searchfilter.Or(
			searchfilter.Equal("category", "news"),
			searchfilter.GreaterThan("score", 5),
		),
	})
	require.NoError(t, err)
	assert.Equal(t,
		" WHERE (id IN (?)) AND (((category = 'news') OR (score > 5)))",
		where,
	)
	assert.Equal(t, []any{"a"}, args)

	// Metadata + condition + IDs.
	where, args, err = vs.buildWhereClause(&vectorstore.SearchFilter{
		IDs:             []string{"a"},
		Metadata:        map[string]any{"category": "news"},
		FilterCondition: searchfilter.GreaterThan("score", 5),
	})
	require.NoError(t, err)
	assert.Contains(t, where, "id IN (?)")
	assert.Contains(t, where, "category = 'news'")
	assert.Contains(t, where, "score > 5")
	assert.Equal(t, []any{"a"}, args)
}

func TestSearchRowsErr(t *testing.T) {
	// A row-stream iteration error during vector search must be reported as an
	// error, not silently dropped.
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return &mockRows{current: -1, err: errors.New("iteration failed")}, nil
	}
	vs := vsWithClient(c)
	_, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1, 2, 3},
		Limit:      5,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration failed")
}

func TestSearchFilterRowsErr(t *testing.T) {
	for _, mode := range []vectorstore.SearchMode{
		vectorstore.SearchModeFilter,
		vectorstore.SearchModeKeyword,
	} {
		c := &mockClient{}
		c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
			return &mockRows{current: -1, err: errors.New("iteration failed")}, nil
		}
		vs := vsWithClient(c)
		q := &vectorstore.SearchQuery{SearchMode: mode, Limit: 5}
		switch mode {
		case vectorstore.SearchModeFilter:
			q.Filter = &vectorstore.SearchFilter{IDs: []string{"a"}}
		case vectorstore.SearchModeKeyword:
			q.Query = "hello"
		}
		_, err := vs.Search(context.Background(), q)
		require.Error(t, err, "mode %d", mode)
		assert.Contains(t, err.Error(), "iteration failed")
	}
}

func TestGetMetadataOrderBy(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	vs := vsWithClient(c)
	_, err := vs.GetMetadata(context.Background(), vectorstore.WithGetMetadataIDs([]string{"doc1"}))
	require.NoError(t, err)
	require.Len(t, c.queryCalls, 1)
	// Pagination requires a deterministic ORDER BY id.
	assert.Contains(t, c.queryCalls[0].query, "ORDER BY id")
}

func TestGetMetadataPaginationMultiPage(t *testing.T) {
	// Two full pages of defaultBatchSize rows. With ORDER BY id, every ID must
	// be returned exactly once across pages.
	c := &mockClient{}
	page := 0
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		page++
		if page > 2 {
			return newMockRows(nil), nil
		}
		offset := (page - 1) * defaultBatchSize
		rows := make([][]any, 0, defaultBatchSize)
		for i := 0; i < defaultBatchSize; i++ {
			rows = append(rows, []any{fmt.Sprintf("doc-%04d", offset+i), "{}"})
		}
		return newMockRows(rows), nil
	}
	vs := vsWithClient(c)
	out, err := vs.GetMetadata(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2*defaultBatchSize)
	// Second page is requested with an OFFSET.
	require.GreaterOrEqual(t, len(c.queryCalls), 2)
	assert.Contains(t, c.queryCalls[1].query, "OFFSET")
}
