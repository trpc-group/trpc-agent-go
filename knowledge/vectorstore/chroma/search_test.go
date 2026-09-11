//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

func seedDocs(t *testing.T, vs *VectorStore) {
	t.Helper()
	ctx := context.Background()
	docs := []struct {
		id, name, content string
		md                map[string]any
		emb               []float64
	}{
		{"sem", "semantic", "unrelated body", map[string]any{"category": "guide", "lang": "zh", "n": 1}, []float64{1, 0, 0}},
		{"kw", "keyword", "vector database intro", map[string]any{"category": "other", "lang": "en", "n": 5}, []float64{0, 1, 0}},
		{"both", "both", "vector database guide", map[string]any{"category": "guide", "lang": "zh", "n": 9}, []float64{0.9, 0.1, 0}},
	}
	for _, d := range docs {
		if err := vs.Add(ctx, &document.Document{ID: d.id, Name: d.name, Content: d.content, Metadata: d.md}, d.emb); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchModes(t *testing.T) {
	ctx := context.Background()

	t.Run("vector search builds Query without where_document and applies min score", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{1, 0, 0},
			Limit:      10,
			MinScore:   0.9,
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"category": "guide"}},
		})
		if err != nil {
			t.Fatalf("Search(vector) error = %v", err)
		}
		if len(res.Results) == 0 {
			t.Fatal("expected vector hits")
		}
		for _, r := range res.Results {
			if r.Score < 0.9 {
				t.Fatalf("score %v < 0.9", r.Score)
			}
		}
		if fc.lastQuery.WhereDocument != nil {
			t.Fatalf("vector Query must not set where_document: %#v", fc.lastQuery.WhereDocument)
		}
		if fc.lastQuery.NResults != 10 {
			t.Fatalf("NResults = %d", fc.lastQuery.NResults)
		}
		if fc.lastQuery.Where == nil {
			t.Fatal("vector Query should include metadata where")
		}
	})

	t.Run("filter mode requires a predicate", func(t *testing.T) {
		vs := testVectorStore(newFakeClient())
		_, err := vs.Search(ctx, &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeFilter})
		if !errors.Is(err, errEmptyFilter) {
			t.Fatalf("empty filter error = %v", err)
		}
		_, err = vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Operator: searchfilter.OperatorAnd,
					Value:    []*searchfilter.UniversalFilterCondition{nil, nil},
				},
			},
		})
		if !errors.Is(err, errEmptyFilter) {
			t.Fatalf("ineffective filter error = %v", err)
		}
	})

	t.Run("filter mode builds Get with ids and metadata where", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Limit:      4,
			Filter: &vectorstore.SearchFilter{
				IDs:      []string{"kw", "missing"},
				Metadata: map[string]any{"category": "other"},
			},
		})
		if err != nil || len(res.Results) != 1 || res.Results[0].Document.ID != "kw" {
			t.Fatalf("id+metadata filter = %#v %v", res, err)
		}
		if fc.getCalls == 0 || fc.lastGet.WhereDocument != nil {
			t.Fatalf("filter Get = %#v calls=%d", fc.lastGet, fc.getCalls)
		}
	})

	t.Run("filter mode AND condition", func(t *testing.T) {
		vs := testVectorStore(newFakeClient())
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Operator: searchfilter.OperatorAnd,
					Value: []*searchfilter.UniversalFilterCondition{
						{Field: "category", Operator: searchfilter.OperatorEqual, Value: "guide"},
						{Field: "lang", Operator: searchfilter.OperatorEqual, Value: "zh"},
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Results) != 2 {
			t.Fatalf("and filter hits = %d", len(res.Results))
		}
	})

	t.Run("filter mode numeric gt", func(t *testing.T) {
		vs := testVectorStore(newFakeClient())
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field: "n", Operator: searchfilter.OperatorGreaterThan, Value: 3,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, r := range res.Results {
			ids[r.Document.ID] = true
		}
		if !ids["kw"] || !ids["both"] || ids["sem"] {
			t.Fatalf("gt filter = %v", ids)
		}
	})

	t.Run("vector and filter modes pass content predicates to classic APIs", func(t *testing.T) {
		contentFilter := &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field: "content", Operator: searchfilter.OperatorLike, Value: "guide",
			},
		}
		fc := newFakeClient()
		vs := testVectorStore(fc)
		seedDocs(t, vs)

		vectorRes, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{1, 0, 0},
			Filter:     contentFilter,
		})
		if err != nil {
			t.Fatalf("Search(vector content) error = %v", err)
		}
		if !reflect.DeepEqual(fc.lastQuery.WhereDocument, map[string]any{"$contains": "guide"}) {
			t.Fatalf("vector where_document = %#v", fc.lastQuery.WhereDocument)
		}
		if got := searchResultIDs(vectorRes); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("vector content ids = %v, want [both]", got)
		}

		filterRes, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter:     contentFilter,
		})
		if err != nil {
			t.Fatalf("Search(filter content) error = %v", err)
		}
		if !reflect.DeepEqual(fc.lastGet.WhereDocument, map[string]any{"$contains": "guide"}) {
			t.Fatalf("filter where_document = %#v", fc.lastGet.WhereDocument)
		}
		if got := searchResultIDs(filterRes); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("filter content ids = %v, want [both]", got)
		}
	})

	t.Run("contradictory ids short-circuit every mode", func(t *testing.T) {
		for _, mode := range []vectorstore.SearchMode{
			vectorstore.SearchModeVector,
			vectorstore.SearchModeKeyword,
			vectorstore.SearchModeHybrid,
			vectorstore.SearchModeFilter,
		} {
			t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
				fc := newFakeClient()
				vs := testVectorStore(
					fc,
					WithSparseSearch(stubSparseEmbedder{indices: []int{1}, values: []float64{1}}),
				)
				res, err := vs.Search(ctx, &vectorstore.SearchQuery{
					SearchMode: mode,
					Query:      "vector",
					Vector:     []float64{1, 0, 0},
					Filter: &vectorstore.SearchFilter{
						IDs: []string{"a"},
						FilterCondition: &searchfilter.UniversalFilterCondition{
							Field: "id", Operator: searchfilter.OperatorEqual, Value: "b",
						},
					},
				})
				if err != nil {
					t.Fatalf("Search() error = %v", err)
				}
				if len(res.Results) != 0 {
					t.Fatalf("results = %#v, want empty", res.Results)
				}
				if fc.queryCalls != 0 || fc.searchCalls != 0 || fc.getCalls != 0 {
					t.Fatalf("query/search/get calls = %d/%d/%d, want zero", fc.queryCalls, fc.searchCalls, fc.getCalls)
				}
			})
		}
	})
}

func TestSparseSearch(t *testing.T) {
	ctx := context.Background()
	sparse := WithSparseSearch(stubSparseEmbedder{indices: []int{1}, values: []float64{1}})

	t.Run("uses sparse search for keyword mode", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "vector database",
			Limit:      2,
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if fc.searchCalls != 1 || fc.queryCalls != 0 || len(res.Results) != 2 {
			t.Fatalf("search/query/results = %d/%d/%d", fc.searchCalls, fc.queryCalls, len(res.Results))
		}
		for _, result := range res.Results {
			if result.Score < 0 || result.Score > 1 {
				t.Fatalf("keyword score %v is outside [0,1]", result.Score)
			}
		}
		rank := fmt.Sprintf("%#v", fc.lastSearch.Rank)
		if !strings.Contains(rank, defaultSparseEmbeddingKey) ||
			!strings.Contains(rank, "sparse_vector") {
			t.Fatalf("keyword rank = %#v", fc.lastSearch.Rank)
		}
	})

	t.Run("keyword does not fall back when Cloud search is unsupported", func(t *testing.T) {
		fc := newFakeClient()
		fc.searchErr = storage.ErrSearchNotImplemented
		vs := testVectorStore(fc, sparse)
		_, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeKeyword,
			Query:      "vector",
		})
		assertErrContains(t, err, "not supported")
		if !errors.Is(err, storage.ErrSearchNotImplemented) {
			t.Fatalf("error = %v, want ErrSearchNotImplemented", err)
		}
		if fc.queryCalls != 0 {
			t.Fatalf("keyword unexpectedly fell back to vector: %d query calls", fc.queryCalls)
		}
	})

	t.Run("applies minimum score to keyword and hybrid", func(t *testing.T) {
		for _, mode := range []vectorstore.SearchMode{
			vectorstore.SearchModeKeyword,
			vectorstore.SearchModeHybrid,
		} {
			fc := newFakeClient()
			vs := testVectorStore(fc, sparse)
			seedDocs(t, vs)
			res, err := vs.Search(ctx, &vectorstore.SearchQuery{
				SearchMode: mode,
				Query:      "vector database",
				Vector:     []float64{1, 0, 0},
				MinScore:   1.1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Results) != 0 {
				t.Fatalf("mode %d results = %#v, want none", mode, res.Results)
			}
		}
	})

	t.Run("uses Cloud search with RRF and converts score direction", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Limit:      2,
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if fc.searchCalls != 1 || fc.queryCalls != 0 {
			t.Fatalf("search/query calls = %d/%d", fc.searchCalls, fc.queryCalls)
		}
		if len(res.Results) != 2 {
			t.Fatalf("results = %#v", res.Results)
		}
		if res.Results[0].Score <= 0 || res.Results[0].Score < res.Results[1].Score {
			t.Fatalf("scores = %#v", res.Results)
		}
		if fc.lastSearch.Limit != 2 {
			t.Fatalf("limit = %d", fc.lastSearch.Limit)
		}
		rank, ok := fc.lastSearch.Rank["$sub"].(map[string]any)
		if !ok {
			t.Fatalf("rank = %#v", fc.lastSearch.Rank)
		}
		if _, ok := rank["right"].(map[string]any)["$sum"]; !ok {
			t.Fatalf("RRF rank = %#v", fc.lastSearch.Rank)
		}
		if !strings.Contains(fmt.Sprintf("%v", fc.lastSearch.Rank), defaultSparseEmbeddingKey) {
			t.Fatalf("sparse knn key missing: %#v", fc.lastSearch.Rank)
		}
		if got := collectKNNLimits(fc.lastSearch.Rank); len(got) != 2 || got[0] != defaultMinCandidates || got[1] != defaultMinCandidates {
			t.Fatalf("knn limits = %v, want [%d %d]", got, defaultMinCandidates, defaultMinCandidates)
		}
		if got := collectKNNDefaults(fc.lastSearch.Rank); len(got) != 2 || got[0] != defaultMinCandidates || got[1] != defaultMinCandidates {
			t.Fatalf("knn defaults = %v, want [%d %d]", got, defaultMinCandidates, defaultMinCandidates)
		}
		if got := collectRRFWeights(fc.lastSearch.Rank); len(got) != 2 || got[0] != defaultDenseWeight || got[1] != defaultSparseWeight {
			t.Fatalf("rrf weights = %v, want [%v %v]", got, defaultDenseWeight, defaultSparseWeight)
		}
	})

	t.Run("uses configured Cloud RRF weights", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse, WithHybridWeights(0.9, 0.1))
		seedDocs(t, vs)
		if _, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
		}); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if got := collectRRFWeights(fc.lastSearch.Rank); len(got) != 2 || got[0] != 0.9 || got[1] != 0.1 {
			t.Fatalf("rrf weights = %v, want [0.9 0.1]", got)
		}
	})

	t.Run("caps KNN candidates at max request records", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse, WithMaxRequestRecords(50))
		seedDocs(t, vs)
		if _, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Limit:      10,
		}); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if got := collectKNNLimits(fc.lastSearch.Rank); len(got) != 2 || got[0] != 50 || got[1] != 50 {
			t.Fatalf("knn limits = %v, want [50 50]", got)
		}
	})

	t.Run("uses WithSparseSearchKey", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse, WithSparseSearchKey("lexical"))
		seedDocs(t, vs)
		if _, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
		}); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if !strings.Contains(fmt.Sprintf("%v", fc.lastSearch.Rank), "lexical") {
			t.Fatalf("custom sparse key missing: %#v", fc.lastSearch.Rank)
		}
	})

	t.Run("configured sparse search does not silently fall back", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		fc.searchErr = storage.ErrSearchNotImplemented
		before := fc.queryCalls
		_, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
		})
		if !errors.Is(err, storage.ErrSearchNotImplemented) {
			t.Fatalf("error = %v, want ErrSearchNotImplemented", err)
		}
		if fc.queryCalls != before {
			t.Fatalf("unexpected vector fallback: queryCalls=%d", fc.queryCalls)
		}

		fc.searchErr = errors.New("permission denied")
		_, err = vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("error = %v, want permission denied", err)
		}
	})

	t.Run("unconfigured sparse search uses dense vector search", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if fc.searchCalls != 0 || fc.queryCalls != 1 || len(res.Results) == 0 {
			t.Fatalf("search/query/results = %d/%d/%d", fc.searchCalls, fc.queryCalls, len(res.Results))
		}
	})

	t.Run("empty query uses dense vector search", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Vector:     []float64{1, 0, 0},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if fc.searchCalls != 0 || fc.queryCalls != 1 || len(res.Results) == 0 {
			t.Fatalf("search/query/results = %d/%d/%d", fc.searchCalls, fc.queryCalls, len(res.Results))
		}
	})

	t.Run("content filter uses #document and is not dropped", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Limit:      10,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field: "content", Operator: searchfilter.OperatorLike, Value: "guide",
				},
			},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if fc.searchCalls != 1 {
			t.Fatalf("searchCalls = %d", fc.searchCalls)
		}
		wantFilter := map[string]any{"#document": map[string]any{"$contains": "guide"}}
		if !reflect.DeepEqual(fc.lastSearch.Filter, wantFilter) {
			t.Fatalf("filter = %#v, want %#v", fc.lastSearch.Filter, wantFilter)
		}
		if _, ok := fc.lastSearch.Filter["$contains"]; ok {
			t.Fatalf("must not send classic where_document: %#v", fc.lastSearch.Filter)
		}
		if got := searchResultIDs(res); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("ids = %v, want [both]", got)
		}
	})

	t.Run("content not-like excludes matching documents", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field: "content", Operator: searchfilter.OperatorNotLike, Value: "guide",
				},
			},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		wantFilter := map[string]any{"#document": map[string]any{"$not_contains": "guide"}}
		if !reflect.DeepEqual(fc.lastSearch.Filter, wantFilter) {
			t.Fatalf("filter = %#v, want %#v", fc.lastSearch.Filter, wantFilter)
		}
		if got := searchResultIDs(res); !reflect.DeepEqual(got, []string{"kw", "sem"}) {
			t.Fatalf("ids = %v, want [kw sem]", got)
		}
	})

	t.Run("metadata and content filters are ANDed", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Filter: &vectorstore.SearchFilter{
				Metadata: map[string]any{"category": "guide"},
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field: "content", Operator: searchfilter.OperatorLike, Value: "vector",
				},
			},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		andParts, ok := fc.lastSearch.Filter["$and"].([]any)
		if !ok || len(andParts) != 2 {
			t.Fatalf("filter = %#v", fc.lastSearch.Filter)
		}
		if !reflect.DeepEqual(andParts[0], map[string]any{"category": map[string]any{"$eq": "guide"}}) {
			t.Fatalf("metadata clause = %#v", andParts[0])
		}
		if !reflect.DeepEqual(andParts[1], map[string]any{"#document": map[string]any{"$contains": "vector"}}) {
			t.Fatalf("document clause = %#v", andParts[1])
		}
		if got := searchResultIDs(res); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("ids = %v, want [both]", got)
		}
	})

	t.Run("ids and content filters both apply", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Filter: &vectorstore.SearchFilter{
				IDs: []string{"sem", "both"},
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field: "content", Operator: searchfilter.OperatorLike, Value: "vector",
				},
			},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if !reflect.DeepEqual(fc.lastSearch.IDs, []string{"sem", "both"}) {
			t.Fatalf("ids param = %v", fc.lastSearch.IDs)
		}
		if !reflect.DeepEqual(fc.lastSearch.Filter, map[string]any{"#document": map[string]any{"$contains": "vector"}}) {
			t.Fatalf("filter = %#v", fc.lastSearch.Filter)
		}
		if got := searchResultIDs(res); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("ids = %v, want [both]", got)
		}
	})

	t.Run("and of two content filters", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, sparse)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeHybrid,
			Query:      "vector database",
			Vector:     []float64{1, 0, 0},
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Operator: searchfilter.OperatorAnd,
					Value: []*searchfilter.UniversalFilterCondition{
						{Field: "content", Operator: searchfilter.OperatorLike, Value: "vector"},
						{Field: "content", Operator: searchfilter.OperatorLike, Value: "guide"},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		wantFilter := map[string]any{"$and": []any{
			map[string]any{"#document": map[string]any{"$contains": "vector"}},
			map[string]any{"#document": map[string]any{"$contains": "guide"}},
		}}
		if !reflect.DeepEqual(fc.lastSearch.Filter, wantFilter) {
			t.Fatalf("filter = %#v, want %#v", fc.lastSearch.Filter, wantFilter)
		}
		if got := searchResultIDs(res); !reflect.DeepEqual(got, []string{"both"}) {
			t.Fatalf("ids = %v, want [both]", got)
		}
	})
}

func searchResultIDs(res *vectorstore.SearchResult) []string {
	if res == nil {
		return nil
	}
	ids := make([]string, 0, len(res.Results))
	for _, r := range res.Results {
		if r != nil && r.Document != nil {
			ids = append(ids, r.Document.ID)
		}
	}
	return ids
}

func collectKNNLimits(v any) []int {
	var out []int
	switch x := v.(type) {
	case map[string]any:
		if knn, ok := x["$knn"].(map[string]any); ok {
			if n, ok := knn["limit"].(int); ok {
				out = append(out, n)
			}
		}
		for _, child := range x {
			out = append(out, collectKNNLimits(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, collectKNNLimits(child)...)
		}
	}
	return out
}

func collectKNNDefaults(v any) []int {
	var out []int
	switch x := v.(type) {
	case map[string]any:
		if knn, ok := x["$knn"].(map[string]any); ok {
			if n, ok := knn["default"].(int); ok {
				out = append(out, n)
			}
		}
		for _, child := range x {
			out = append(out, collectKNNDefaults(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, collectKNNDefaults(child)...)
		}
	}
	return out
}

func collectRRFWeights(v any) []float64 {
	var out []float64
	switch x := v.(type) {
	case map[string]any:
		if div, ok := x["$div"].(map[string]any); ok {
			if left, ok := div["left"].(map[string]any); ok {
				switch n := left["$val"].(type) {
				case int:
					out = append(out, float64(n)/float64(defaultRRFOffset))
				case float64:
					out = append(out, n/float64(defaultRRFOffset))
				}
			}
		}
		for _, child := range x {
			out = append(out, collectRRFWeights(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, collectRRFWeights(child)...)
		}
	}
	return out
}

func TestSearchErrors(t *testing.T) {
	ctx := context.Background()
	sparse := WithSparseSearch(stubSparseEmbedder{indices: []int{1}, values: []float64{1}})
	tests := []struct {
		name string
		vs   *VectorStore
		q    *vectorstore.SearchQuery
		want string
	}{
		{name: "nil query", vs: testVectorStore(newFakeClient()), q: nil, want: "query is required"},
		{name: "unsupported mode", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: 99, Vector: []float64{1, 0, 0}}, want: "unsupported SearchMode"},
		{name: "vector dim mismatch", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeVector, Vector: []float64{1}}, want: "dimension mismatch"},
		{name: "vector zero embedding", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeVector, Vector: []float64{0, 0, 0}}, want: "zero vector"},
		{name: "keyword requires query", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeKeyword}, want: "keyword is required"},
		{name: "hybrid dim mismatch", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeHybrid, Vector: []float64{1}}, want: "dimension mismatch"},
		{name: "query rpc error", vs: testVectorStore(&fakeClient{records: map[string]*memRecord{}, queryErr: errors.New("rpc down")}), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeVector, Vector: []float64{1, 0, 0}}, want: "rpc down"},
		{name: "vector bad filter", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeVector, Vector: []float64{1, 0, 0}, Filter: &vectorstore.SearchFilter{Metadata: map[string]any{"$bad": 1}}}, want: "field name is invalid"},
		{name: "keyword requires sparse search", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeKeyword, Query: "vector"}, want: "requires WithSparseSearch"},
		{name: "keyword search rpc", vs: testVectorStore(&fakeClient{records: map[string]*memRecord{}, searchErr: errors.New("kw down")}, sparse), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeKeyword, Query: "vector"}, want: "kw down"},
		{name: "keyword bad filter", vs: testVectorStore(newFakeClient(), sparse), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeKeyword, Query: "vector", Filter: &vectorstore.SearchFilter{Metadata: map[string]any{"$bad": 1}}}, want: "field name is invalid"},
		{name: "filter get rpc", vs: testVectorStore(&fakeClient{records: map[string]*memRecord{}, getErr: errors.New("filter down")}), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeFilter, Filter: &vectorstore.SearchFilter{IDs: []string{"id"}}}, want: "filter down"},
		{name: "filter bad field", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeFilter, Filter: &vectorstore.SearchFilter{Metadata: map[string]any{"$bad": 1}}}, want: "field name is invalid"},
		{name: "hybrid vector fallback query rpc", vs: testVectorStore(&fakeClient{records: map[string]*memRecord{}, queryErr: errors.New("vec down")}), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeHybrid, Query: "vector", Vector: []float64{1, 0, 0}}, want: "vec down"},
		{name: "hybrid vector fallback bad filter", vs: testVectorStore(newFakeClient()), q: &vectorstore.SearchQuery{SearchMode: vectorstore.SearchModeHybrid, Query: "vector", Vector: []float64{1, 0, 0}, Filter: &vectorstore.SearchFilter{Metadata: map[string]any{"$bad": 1}}}, want: "field name is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.vs.Search(ctx, tt.q)
			assertErrContains(t, err, tt.want)
		})
	}
}

func TestSearchHelpers(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	if docs, err := vs.scoredFromQuery(nil); err != nil || docs != nil {
		t.Fatalf("scoredFromQuery nil = %#v %v", docs, err)
	}
	if _, err := vs.scoredFromQuery(&storage.QueryResult{IDs: [][]string{{"x"}}}); err == nil ||
		!strings.Contains(err.Error(), `no distance for document "x"`) {
		t.Fatalf("scoredFromQuery missing distance error = %v", err)
	}
	if _, err := vs.scoredFromQuery(&storage.QueryResult{
		IDs:       [][]string{{"x"}},
		Metadatas: [][]map[string]any{{{metaJSON: "{"}}},
	}); err == nil {
		t.Fatal("scoredFromQuery corrupt _json should fail")
	}
	if _, err := vs.scoredFromQuery(&storage.QueryResult{
		IDs:           [][]string{{"x"}},
		Distances:     [][]float32{{0}},
		DistanceValid: [][]bool{{false}},
	}); err == nil || !strings.Contains(err.Error(), `null distance for document "x"`) {
		t.Fatalf("scoredFromQuery null distance error = %v", err)
	}
	if _, err := vs.scoredFromSearch(&storage.SearchResult{
		IDs:    [][]string{{"x"}},
		Scores: [][]*float32{{nil}},
	}, negativeRankScore); err == nil || !strings.Contains(err.Error(), `null score for document "x"`) {
		t.Fatalf("scoredFromSearch null score error = %v", err)
	}
	positiveWireScore := float32(1)
	if _, err := vs.scoredFromSearch(&storage.SearchResult{
		IDs:    [][]string{{"x"}},
		Scores: [][]*float32{{&positiveWireScore}},
	}, negativeRankScore); err == nil || !strings.Contains(err.Error(), "invalid score") {
		t.Fatalf("scoredFromSearch invalid score error = %v", err)
	}
	if docs, err := vs.scoredFromGet(nil, 0); err != nil || docs != nil {
		t.Fatalf("scoredFromGet nil = %#v %v", docs, err)
	}
	if cosineScore(-1) != 1 || cosineScore(1) != 0.5 || cosineScore(2) != 0 {
		t.Fatal("cosine clamp")
	}
	if docs := applyMinScore([]*vectorstore.ScoredDocument{{Score: 0.2}, {Score: 0.9}}, 0.5); len(docs) != 1 {
		t.Fatalf("applyMinScore = %#v", docs)
	}
	if got := hybridCandidateLimit(2); got != defaultMinCandidates {
		t.Fatalf("hybridCandidateLimit below minimum = %d, want %d", got, defaultMinCandidates)
	}
	if got := hybridCandidateLimit(defaultMinCandidates + 1); got != (defaultMinCandidates+1)*defaultCandidateRatio {
		t.Fatalf("hybridCandidateLimit above page size = %d, want uncapped total", got)
	}
	if got := vs.nextPageSize(0, 0); got != defaultMaxRequestRecords {
		t.Fatalf("nextPageSize unlimited = %d", got)
	}
	if got := vs.nextPageSize(0, defaultMaxRequestRecords+1); got != defaultMaxRequestRecords {
		t.Fatalf("nextPageSize first page = %d", got)
	}
	if got := vs.nextPageSize(defaultMaxRequestRecords, defaultMaxRequestRecords+1); got != 1 {
		t.Fatalf("nextPageSize remainder = %d", got)
	}
	if got := vs.nextPageSize(defaultMaxRequestRecords+1, defaultMaxRequestRecords+1); got != 0 {
		t.Fatalf("nextPageSize filled = %d", got)
	}

}

func TestSearchPagesGetAndPassesQueryLimit(t *testing.T) {
	ctx := context.Background()

	t.Run("filter Get pages when Limit exceeds 300", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		for i := 0; i < defaultMaxRequestRecords+1; i++ {
			id := fmt.Sprintf("f%03d", i)
			if err := vs.Add(ctx, &document.Document{
				ID: id, Content: "c", Metadata: map[string]any{"g": 1},
			}, []float64{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
		}
		gets := fc.getCalls
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Limit:      defaultMaxRequestRecords + 1,
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"g": 1}},
		})
		if err != nil || len(res.Results) != defaultMaxRequestRecords+1 {
			t.Fatalf("filter paged = %d %v", len(res.Results), err)
		}
		if fc.getCalls-gets != 2 {
			t.Fatalf("filter Get pages = %d, want 2", fc.getCalls-gets)
		}
	})

	t.Run("filter Get uses configured request limit", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc, WithMaxRequestRecords(2))
		for i := 0; i < 3; i++ {
			if err := vs.Add(ctx, &document.Document{
				ID:       fmt.Sprintf("p%03d", i),
				Content:  "c",
				Metadata: map[string]any{"g": 1},
			}, []float64{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
		}
		gets := fc.getCalls
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Limit:      3,
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"g": 1}},
		})
		if err != nil || len(res.Results) != 3 {
			t.Fatalf("filter custom page = %d %v", len(res.Results), err)
		}
		if fc.getCalls-gets != 2 {
			t.Fatalf("filter custom-page Get calls = %d, want 2", fc.getCalls-gets)
		}
		if fc.lastGet.Limit == nil || *fc.lastGet.Limit != 1 {
			t.Fatalf("second custom-page Limit = %#v, want 1", fc.lastGet.Limit)
		}
	})

	t.Run("vector Query caps Limit", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		seedDocs(t, vs)
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeVector,
			Vector:     []float64{1, 0, 0},
			Limit:      defaultMaxRequestRecords + 1,
		})
		if err != nil || len(res.Results) == 0 {
			t.Fatalf("vector Limit 301 = %#v %v", res, err)
		}
		if fc.lastQuery.NResults != defaultMaxRequestRecords {
			t.Fatalf("Query n_results = %d, want %d", fc.lastQuery.NResults, defaultMaxRequestRecords)
		}
	})

}
