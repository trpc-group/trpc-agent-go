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
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var errBackend = errors.New("backend failure")

// failingQueryClient fails Query, optionally only for queries matching match.
type failingQueryClient struct {
	mockClient
	match string
}

func newFailingQueryClient(match string) *failingQueryClient {
	c := &failingQueryClient{match: match}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		if c.match == "" || strings.Contains(q, c.match) {
			return nil, errBackend
		}
		return newMockRows(nil), nil
	}
	return c
}

func failingExecClient() *mockClient {
	c := &mockClient{}
	c.execFunc = func(ctx context.Context, q string, a ...any) error { return errBackend }
	return c
}

func sampleDoc() *document.Document {
	return &document.Document{
		ID:       "doc1",
		Name:     "n",
		Content:  "c",
		Metadata: map[string]any{"category": "news"},
	}
}

// TestAddUpdateDeleteBackendErrors covers the error paths of the write methods.
func TestAddUpdateDeleteBackendErrors(t *testing.T) {
	vs := vsWithClient(failingExecClient())

	err := vs.Add(context.Background(), sampleDoc(), []float64{1, 2, 3})
	require.ErrorIs(t, err, errBackend)

	err = vs.Delete(context.Background(), "doc1")
	require.ErrorIs(t, err, errBackend)

	err = vs.DeleteByFilter(context.Background(), vectorstore.WithDeleteDocumentIDs([]string{"doc1"}))
	require.ErrorIs(t, err, errBackend)

	// DeleteAll must be explicitly enabled before it reaches the backend.
	err = vs.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true))
	require.Error(t, err)

	vsAll := vsWithClient(failingExecClient(), WithAllowDestructiveDeleteAll(true))
	err = vsAll.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true))
	require.ErrorIs(t, err, errBackend)
}

// TestAddInvalidInput covers validation before any backend call.
func TestAddInvalidInput(t *testing.T) {
	vs := vsWithClient(&mockClient{})

	require.Error(t, vs.Add(context.Background(), nil, []float64{1, 2, 3}))
	require.Error(t, vs.Add(context.Background(), &document.Document{}, []float64{1, 2, 3}))
	// Dimension mismatch.
	require.Error(t, vs.Add(context.Background(), sampleDoc(), []float64{1}))
	// Update shares the same validation.
	require.Error(t, vs.Update(context.Background(), nil, []float64{1, 2, 3}))
	require.Error(t, vs.Update(context.Background(), sampleDoc(), []float64{1}))
	// Delete requires an ID.
	require.Error(t, vs.Delete(context.Background(), ""))
}

// TestGetBackendErrors covers Get failures and the not-found path.
func TestGetBackendErrors(t *testing.T) {
	vs := vsWithClient(newFailingQueryClient(""))
	_, _, err := vs.Get(context.Background(), "doc1")
	require.ErrorIs(t, err, errBackend)

	// Empty ID is rejected before querying.
	_, _, err = vs.Get(context.Background(), "")
	require.Error(t, err)

	// No rows -> not found.
	empty := &mockClient{}
	empty.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	_, _, err = vsWithClient(empty).Get(context.Background(), "missing")
	require.Error(t, err)

	// Row iteration error must surface.
	rowsErr := &mockClient{}
	rowsErr.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		r := newMockRows(nil)
		r.err = errBackend
		return r, nil
	}
	_, _, err = vsWithClient(rowsErr).Get(context.Background(), "doc1")
	require.Error(t, err)
}

// TestUpdateNotFound covers Update against a missing document.
func TestUpdateNotFound(t *testing.T) {
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	err := vsWithClient(c).Update(context.Background(), sampleDoc(), []float64{1, 2, 3})
	require.Error(t, err)
}

// TestSearchBackendErrors covers query failures across all search modes.
func TestSearchBackendErrors(t *testing.T) {
	vs := vsWithClient(newFailingQueryClient(""))
	ctx := context.Background()

	modes := []struct {
		name  string
		query *vectorstore.SearchQuery
	}{
		{"vector", &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{1, 2, 3},
			Limit:      5,
		}},
		{"filter", &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Limit:      5,
			// Filter mode short-circuits without constraints, so one is needed
			// to reach the backend.
			Filter: &vectorstore.SearchFilter{IDs: []string{"doc1"}},
		}},
		{"keyword", &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "kw",
			Limit:      5,
		}},
		{"hybrid", &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "kw",
			Vector:     []float64{1, 2, 3},
			Limit:      5,
		}},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			_, err := vs.Search(ctx, m.query)
			require.ErrorIs(t, err, errBackend)
		})
	}
}

// TestSearchInvalidInput covers per-mode validation.
func TestSearchInvalidInput(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	ctx := context.Background()

	// Nil query.
	_, err := vs.Search(ctx, nil)
	require.Error(t, err)

	// Vector search with a wrong dimension.
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1},
		Limit:      5,
	})
	require.Error(t, err)

	// Keyword search without a query string.
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
		Limit:      5,
	})
	require.Error(t, err)

	// Hybrid search needs both a query and a well-formed vector.
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Vector:     []float64{1, 2, 3},
		Limit:      5,
	})
	require.Error(t, err)
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Query:      "kw",
		Vector:     []float64{1},
		Limit:      5,
	})
	require.Error(t, err)

	// Unknown search mode.
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchMode(9999),
		Limit:      5,
	})
	require.Error(t, err)
}

// TestSearchInvalidFilterField covers filter validation for every search mode.
func TestSearchInvalidFilterField(t *testing.T) {
	vs := vsWithClient(&mockClient{})
	ctx := context.Background()
	bad := &vectorstore.SearchFilter{
		FilterCondition: searchfilter.Equal("not_declared", "x"),
	}

	for _, mode := range []vectorstore.SearchMode{
		vectorstore.SearchModeVector,
		vectorstore.SearchModeFilter,
		vectorstore.SearchModeKeyword,
		vectorstore.SearchModeHybrid,
	} {
		q := &vectorstore.SearchQuery{
			SearchMode: mode,
			Query:      "kw",
			Vector:     []float64{1, 2, 3},
			Limit:      5,
			Filter:     bad,
		}
		_, err := vs.Search(ctx, q)
		require.Error(t, err, "mode %v must reject an undeclared filter field", mode)
	}
}

// TestCountErrors covers the Count failure paths.
func TestCountErrors(t *testing.T) {
	ctx := context.Background()

	// Backend failure.
	_, err := vsWithClient(newFailingQueryClient("")).Count(ctx)
	require.ErrorIs(t, err, errBackend)

	// No rows returned.
	empty := &mockClient{}
	empty.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	_, err = vsWithClient(empty).Count(ctx)
	require.Error(t, err)

	// Row iteration error.
	rowsErr := &mockClient{}
	rowsErr.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		r := newMockRows(nil)
		r.err = errBackend
		return r, nil
	}
	_, err = vsWithClient(rowsErr).Count(ctx)
	require.Error(t, err)

	// Undeclared filter field.
	_, err = vsWithClient(&mockClient{}).Count(ctx,
		vectorstore.WithCountFilter(map[string]any{"not_declared": "x"}))
	require.Error(t, err)
}

// TestGetMetadataErrors covers the GetMetadata failure paths.
func TestGetMetadataErrors(t *testing.T) {
	ctx := context.Background()

	_, err := vsWithClient(newFailingQueryClient("")).GetMetadata(ctx)
	require.ErrorIs(t, err, errBackend)

	_, err = vsWithClient(newFailingQueryClient("")).GetMetadata(ctx,
		vectorstore.WithGetMetadataLimit(10))
	require.ErrorIs(t, err, errBackend)

	// Row iteration error.
	rowsErr := &mockClient{}
	rowsErr.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		r := newMockRows(nil)
		r.err = errBackend
		return r, nil
	}
	_, err = vsWithClient(rowsErr).GetMetadata(ctx)
	require.Error(t, err)

	// Undeclared filter field.
	_, err = vsWithClient(&mockClient{}).GetMetadata(ctx,
		vectorstore.WithGetMetadataFilter(map[string]any{"not_declared": "x"}))
	require.Error(t, err)
}

// TestUpdateByFilterErrors covers the UpdateByFilter failure paths.
func TestUpdateByFilterErrors(t *testing.T) {
	ctx := context.Background()
	updates := map[string]any{"name": "new"}

	// Selecting the matching IDs fails.
	_, err := vsWithClient(newFailingQueryClient("SELECT id FROM")).UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc1"}),
		vectorstore.WithUpdateByFilterUpdates(updates),
	)
	require.ErrorIs(t, err, errBackend)

	// Row iteration error while collecting IDs.
	rowsErr := &mockClient{}
	rowsErr.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		r := newMockRows(nil)
		r.err = errBackend
		return r, nil
	}
	_, err = vsWithClient(rowsErr).UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc1"}),
		vectorstore.WithUpdateByFilterUpdates(updates),
	)
	require.Error(t, err)

	// Undeclared filter field.
	_, err = vsWithClient(&mockClient{}).UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterCondition(searchfilter.Equal("not_declared", "x")),
		vectorstore.WithUpdateByFilterUpdates(updates),
	)
	require.Error(t, err)

	// No match is not an error and reports zero updates.
	empty := &mockClient{}
	empty.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	n, err := vsWithClient(empty).UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"missing"}),
		vectorstore.WithUpdateByFilterUpdates(updates),
	)
	require.NoError(t, err)
	assert.Zero(t, n)
}

// TestApplyUpdatesToDocErrors covers the update payload validation.
func TestApplyUpdatesToDocErrors(t *testing.T) {
	doc := &document.Document{ID: "d", Metadata: map[string]any{}}

	for name, updates := range map[string]map[string]any{
		"name not a string":      {"name": 1},
		"content not a string":   {"content": 1},
		"embedding wrong type":   {"embedding": "nope"},
		"unknown field":          {"totally_unknown": 1},
		"metadata key without k": {"metadata.": 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := applyUpdatesToDoc(doc, nil, updates)
			require.Error(t, err)
		})
	}
}

// TestSearchFilterModeShortCircuits asserts Filter mode returns no results
// without querying when it has no effective constraint, so an unconstrained
// call cannot turn into a full table scan.
func TestSearchFilterModeShortCircuits(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)
	ctx := context.Background()

	res, err := vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Limit:      5,
	})
	require.NoError(t, err)
	assert.Empty(t, res.Results)

	// An empty filter struct is equally unconstrained.
	res, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Limit:      5,
		Filter:     &vectorstore.SearchFilter{},
	})
	require.NoError(t, err)
	assert.Empty(t, res.Results)
	assert.Empty(t, c.queryCalls, "no query must be issued")
}

// TestFormatBetweenErrors covers the BETWEEN validation branches.
func TestFormatBetweenErrors(t *testing.T) {
	allowed := map[string]struct{}{"price": {}}

	// Undeclared field.
	_, err := formatBetween("nope", []any{1, 2}, allowed)
	require.Error(t, err)

	// Not an array.
	_, err = formatBetween("price", "not-an-array", allowed)
	require.Error(t, err)

	// Wrong element count.
	_, err = formatBetween("price", []any{1}, allowed)
	require.Error(t, err)
	_, err = formatBetween("price", []any{1, 2, 3}, allowed)
	require.Error(t, err)

	// Non-formattable bounds.
	_, err = formatBetween("price", []any{nil, 2}, allowed)
	require.Error(t, err)
	_, err = formatBetween("price", []any{1, nil}, allowed)
	require.Error(t, err)

	got, err := formatBetween("price", []any{1, 10}, allowed)
	require.NoError(t, err)
	assert.Equal(t, "price BETWEEN 1 AND 10", got)
}

// TestFormatLogicalErrors covers the AND/OR aggregation branches.
func TestFormatLogicalErrors(t *testing.T) {
	allowed := map[string]struct{}{"a": {}}

	// A failing subcondition propagates.
	_, err := formatLogical([]*searchfilter.UniversalFilterCondition{
		searchfilter.Equal("not_declared", 1),
	}, "AND", allowed)
	require.Error(t, err)

	// A nil subcondition is skipped rather than panicking.
	got, err := formatLogical([]*searchfilter.UniversalFilterCondition{
		nil,
		searchfilter.Equal("a", 1),
	}, "AND", allowed)
	require.NoError(t, err)
	assert.Contains(t, got, "a = 1")
}

// TestMetadataColumnsWithFilterFields covers the filter-field branch of the
// metadata column list.
func TestMetadataColumnsWithFilterFields(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
		FilterFieldSpec{Name: "score", Type: FilterFieldFloat64},
	))
	cols := vs.metadataColumns()
	assert.Equal(t, []string{"id", "metadata", "category", "score"}, cols)

	// Without filter fields only the fixed columns are selected.
	assert.Equal(t, []string{"id", "metadata"}, vsWithClient(&mockClient{}).metadataColumns())
}

// TestBuildUpdateWhereErrors covers the UpdateByFilter predicate builder.
func TestBuildUpdateWhereErrors(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
	))

	// Undeclared field.
	_, _, err := vs.buildUpdateWhere(nil, searchfilter.Equal("not_declared", "x"))
	require.Error(t, err)

	// IDs only.
	where, args, err := vs.buildUpdateWhere([]string{"a", "b"}, nil)
	require.NoError(t, err)
	assert.Contains(t, where, "id IN")
	assert.Equal(t, []any{"a", "b"}, args)

	// Condition only.
	where, _, err = vs.buildUpdateWhere(nil, searchfilter.Equal("category", "news"))
	require.NoError(t, err)
	assert.Contains(t, where, "category = 'news'")

	// Neither yields an empty predicate.
	where, _, err = vs.buildUpdateWhere(nil, nil)
	require.NoError(t, err)
	assert.Empty(t, where)
}

// TestCloseError covers Close propagating a backend failure.
func TestCloseError(t *testing.T) {
	c := &mockClient{}
	c.closeFunc = func() error { return errBackend }
	require.ErrorIs(t, vsWithClient(c).Close(), errBackend)
}

// TestNewFilterDestsAllTypes covers every declared filter field type, including
// the Float64 branch that the other tests do not exercise.
func TestNewFilterDestsAllTypes(t *testing.T) {
	vs := &VectorStore{option: options{filterFields: []FilterFieldSpec{
		{Name: "s", Type: FilterFieldString},
		{Name: "i", Type: FilterFieldInt64},
		{Name: "f", Type: FilterFieldFloat64},
	}}}
	dests := vs.newFilterDests()
	require.Len(t, dests, 3)
	require.IsType(t, new(string), dests[0])
	require.IsType(t, new(int64), dests[1])
	require.IsType(t, new(float64), dests[2])

	*(dests[0].(*string)) = "v"
	*(dests[1].(*int64)) = 7
	*(dests[2].(*float64)) = 1.5

	// Only keys already present are restored.
	md := map[string]any{"s": "", "i": float64(0), "f": float64(0)}
	vs.mergeFilterDests(md, dests)
	assert.Equal(t, "v", md["s"])
	assert.Equal(t, int64(7), md["i"])
	assert.Equal(t, 1.5, md["f"])

	// A shorter dests slice must not panic.
	vs.mergeFilterDests(map[string]any{"s": ""}, nil)
}
