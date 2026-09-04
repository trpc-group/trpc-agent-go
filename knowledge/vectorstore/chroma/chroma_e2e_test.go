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
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

// e2eDim is the embedding dimension used by the end-to-end tests.
const e2eDim = 4

// e2eConfig holds the Chroma connection settings read from the environment.
type e2eConfig struct {
	baseURL  string
	apiKey   string
	tenant   string
	database string
}

// loadE2EConfig reads the Chroma connection settings from the environment and
// skips the test when no API key is configured. E2E tests run against a real
// Chroma deployment (typically Chroma Cloud):
//
//	CHROMA_HOST      Chroma HTTP origin, for example https://api.trychroma.com
//	CHROMA_API_KEY   API key sent as X-Chroma-Token (required)
//	CHROMA_TENANT    tenant; empty resolves from the Cloud identity
//	CHROMA_DATABASE  database; empty resolves from the Cloud identity
func loadE2EConfig(t *testing.T) e2eConfig {
	t.Helper()
	cfg := e2eConfig{
		baseURL:  os.Getenv("CHROMA_HOST"),
		apiKey:   strings.TrimSpace(os.Getenv("CHROMA_API_KEY")),
		tenant:   strings.TrimSpace(os.Getenv("CHROMA_TENANT")),
		database: strings.TrimSpace(os.Getenv("CHROMA_DATABASE")),
	}
	if cfg.apiKey == "" {
		t.Skip("CHROMA_API_KEY is not set; skipping Chroma e2e test")
	}
	if cfg.baseURL == "" {
		cfg.baseURL = "https://api.trychroma.com"
	}
	if !strings.Contains(cfg.baseURL, "://") {
		cfg.baseURL = "https://" + cfg.baseURL
	}
	return cfg
}

// newE2EStore constructs a VectorStore bound to a fresh unique collection and
// registers cleanup that deletes the collection and closes the store.
func newE2EStore(t *testing.T, cfg e2eConfig, opts ...Option) *VectorStore {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	base := []Option{
		WithBaseURL(cfg.baseURL),
		WithAPIKey(cfg.apiKey),
		WithIndexDimension(e2eDim),
		WithCollection(fmt.Sprintf("e2e-%d", time.Now().UnixNano())),
		WithSparseSearch(),
	}
	if cfg.tenant != "" {
		base = append(base, WithTenant(cfg.tenant))
	}
	if cfg.database != "" {
		base = append(base, WithDatabase(cfg.database))
	}
	vs, err := New(ctx, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		deleteE2ECollection(t, cleanupCtx, cfg, vs.opts.collection)
		vs.Close()
	})
	return vs
}

// deleteE2ECollection removes the collection used by an e2e test.
func deleteE2ECollection(t *testing.T, ctx context.Context, cfg e2eConfig, name string) {
	t.Helper()
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithBaseURL(cfg.baseURL),
		storage.WithAPIKey(cfg.apiKey),
	}
	if cfg.tenant != "" {
		builderOpts = append(builderOpts, storage.WithTenant(cfg.tenant))
	}
	if cfg.database != "" {
		builderOpts = append(builderOpts, storage.WithDatabase(cfg.database))
	}
	c, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		t.Logf("cleanup: build client: %v", err)
		return
	}
	defer c.Close()
	if err := c.DeleteCollection(ctx, name); err != nil {
		t.Logf("cleanup: delete collection %q: %v", name, err)
	}
}

// e2eDoc builds a document with the given ID, content, vector, and metadata.
func e2eDoc(id, name, content string, vector []float64, metadata map[string]any) *document.Document {
	return &document.Document{
		ID:       id,
		Name:     name,
		Content:  content,
		Metadata: metadata,
	}
}

// docFixture pairs a shared e2e document with its embedding vector.
type docFixture struct {
	doc    *document.Document
	vector []float64
}

// TestVectorStoreE2E exercises the full VectorStore contract against a real
// Chroma deployment: add, get, count, metadata reads, all four search modes,
// update, filtered update, delete, filtered delete, delete-all, and close.
func TestVectorStoreE2E(t *testing.T) {
	cfg := loadE2EConfig(t)
	vs := newE2EStore(t, cfg)
	ctx := context.Background()

	fixtures := []docFixture{
		{e2eDoc("doc-1", "golang-notes", "Golang concurrency with goroutine and channel patterns",
			nil, map[string]any{"category": "tech", "level": 1}),
			[]float64{1, 0, 0, 0}},
		{e2eDoc("doc-2", "chroma-notes", "Chroma vector database with cosine similarity search",
			nil, map[string]any{"category": "tech", "level": 2}),
			[]float64{0.9, 0.1, 0, 0}},
		{e2eDoc("doc-3", "pasta-recipe", "Tomato pasta recipe with basil and olive oil",
			nil, map[string]any{"category": "food", "level": 3}),
			[]float64{0, 0, 1, 0}},
	}

	t.Run("add and get", func(t *testing.T) {
		for _, f := range fixtures {
			if err := vs.Add(ctx, f.doc, f.vector); err != nil {
				t.Fatalf("Add %s: %v", f.doc.ID, err)
			}
		}
		doc, emb, err := vs.Get(ctx, "doc-1")
		if err != nil {
			t.Fatalf("Get doc-1: %v", err)
		}
		if doc.Name != "golang-notes" {
			t.Errorf("name = %q, want golang-notes", doc.Name)
		}
		if doc.Content != fixtures[0].doc.Content {
			t.Errorf("content = %q, want %q", doc.Content, fixtures[0].doc.Content)
		}
		if doc.Metadata["category"] != "tech" {
			t.Errorf("category = %v, want tech", doc.Metadata["category"])
		}
		if len(emb) != e2eDim {
			t.Errorf("embedding dim = %d, want %d", len(emb), e2eDim)
		}
		if doc.CreatedAt.IsZero() || doc.UpdatedAt.IsZero() {
			t.Error("created_at/updated_at should be populated")
		}
		// Adding the same ID again upserts without error.
		if err := vs.Add(ctx, fixtures[0].doc, fixtures[0].vector); err != nil {
			t.Fatalf("re-Add doc-1: %v", err)
		}
	})

	t.Run("get missing document", func(t *testing.T) {
		_, _, err := vs.Get(ctx, "does-not-exist")
		if err == nil {
			t.Fatal("Get on missing ID should fail")
		}
	})

	t.Run("count", func(t *testing.T) {
		n, err := vs.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 3 {
			t.Errorf("count = %d, want 3", n)
		}
		n, err = vs.Count(ctx, vectorstore.WithCountFilter(map[string]any{"category": "tech"}))
		if err != nil {
			t.Fatalf("Count with filter: %v", err)
		}
		if n != 2 {
			t.Errorf("filtered count = %d, want 2", n)
		}
	})

	t.Run("get metadata", func(t *testing.T) {
		md, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"doc-1", "doc-3"}))
		if err != nil {
			t.Fatalf("GetMetadata by IDs: %v", err)
		}
		if len(md) != 2 {
			t.Fatalf("GetMetadata returned %d entries, want 2", len(md))
		}
		if md["doc-3"].Metadata["category"] != "food" {
			t.Errorf("doc-3 category = %v, want food", md["doc-3"].Metadata["category"])
		}

		md, err = vs.GetMetadata(ctx,
			vectorstore.WithGetMetadataFilter(map[string]any{"category": "tech"}),
			vectorstore.WithGetMetadataLimit(1))
		if err != nil {
			t.Fatalf("GetMetadata with filter and limit: %v", err)
		}
		if len(md) != 1 {
			t.Errorf("limited GetMetadata returned %d entries, want 1", len(md))
		}
	})

	t.Run("vector search", func(t *testing.T) {
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			Vector:     []float64{1, 0, 0, 0},
			Limit:      2,
			SearchMode: vectorstore.SearchModeVector,
		})
		if err != nil {
			t.Fatalf("vector search: %v", err)
		}
		if len(res.Results) == 0 {
			t.Fatal("vector search returned no results")
		}
		if res.Results[0].Document.ID != "doc-1" {
			t.Errorf("top hit = %s, want doc-1", res.Results[0].Document.ID)
		}
		if res.Results[0].Score <= 0.9 {
			t.Errorf("top score = %f, want > 0.9", res.Results[0].Score)
		}

		// Metadata filter narrows the candidates.
		res, err = vs.Search(ctx, &vectorstore.SearchQuery{
			Vector:     []float64{1, 0, 0, 0},
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
			Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"category": "food"}},
		})
		if err != nil {
			t.Fatalf("vector search with metadata filter: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].Document.ID != "doc-3" {
			t.Errorf("filtered vector search = %+v, want only doc-3", scoredIDs(res))
		}

		// ID filter narrows the candidates.
		res, err = vs.Search(ctx, &vectorstore.SearchQuery{
			Vector:     []float64{1, 0, 0, 0},
			Limit:      10,
			SearchMode: vectorstore.SearchModeVector,
			Filter:     &vectorstore.SearchFilter{IDs: []string{"doc-2"}},
		})
		if err != nil {
			t.Fatalf("vector search with ID filter: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].Document.ID != "doc-2" {
			t.Errorf("ID-filtered vector search = %+v, want only doc-2", scoredIDs(res))
		}

		// MinScore drops weak matches: doc-2 sits at cosine similarity ~0.994
		// against the query, so only the exact-match doc-1 survives 0.999.
		res, err = vs.Search(ctx, &vectorstore.SearchQuery{
			Vector:     []float64{1, 0, 0, 0},
			Limit:      10,
			MinScore:   0.999,
			SearchMode: vectorstore.SearchModeVector,
		})
		if err != nil {
			t.Fatalf("vector search with min score: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].Document.ID != "doc-1" {
			t.Errorf("min-score vector search = %+v, want only doc-1", scoredIDs(res))
		}
	})

	t.Run("filter search", func(t *testing.T) {
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field:    "category",
					Operator: searchfilter.OperatorEqual,
					Value:    "tech",
				},
			},
		})
		if err != nil {
			t.Fatalf("filter search eq: %v", err)
		}
		if len(res.Results) != 2 {
			t.Errorf("eq filter returned %d results, want 2", len(res.Results))
		}

		res, err = vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Field:    "level",
					Operator: searchfilter.OperatorGreaterThan,
					Value:    1,
				},
			},
		})
		if err != nil {
			t.Fatalf("filter search gt: %v", err)
		}
		if len(res.Results) != 2 {
			t.Errorf("gt filter returned %d results, want 2", len(res.Results))
		}

		res, err = vs.Search(ctx, &vectorstore.SearchQuery{
			SearchMode: vectorstore.SearchModeFilter,
			Filter: &vectorstore.SearchFilter{
				FilterCondition: &searchfilter.UniversalFilterCondition{
					Operator: searchfilter.OperatorAnd,
					Value: []*searchfilter.UniversalFilterCondition{
						{Field: "category", Operator: searchfilter.OperatorEqual, Value: "tech"},
						{Field: "level", Operator: searchfilter.OperatorGreaterThanOrEqual, Value: 2},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("filter search and: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].Document.ID != "doc-2" {
			t.Errorf("and filter = %+v, want only doc-2", scoredIDs(res))
		}
	})

	t.Run("keyword search", func(t *testing.T) {
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			Query:      "goroutine channel concurrency",
			Limit:      3,
			SearchMode: vectorstore.SearchModeKeyword,
		})
		if err != nil {
			t.Fatalf("keyword search: %v", err)
		}
		if len(res.Results) == 0 {
			t.Fatal("keyword search returned no results")
		}
		if res.Results[0].Document.ID != "doc-1" {
			t.Logf("keyword search top hit = %s (want doc-1), results = %+v",
				res.Results[0].Document.ID, scoredIDs(res))
		}
	})

	t.Run("hybrid search", func(t *testing.T) {
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{
			Query:      "tomato pasta",
			Vector:     []float64{0, 0, 1, 0},
			Limit:      3,
			SearchMode: vectorstore.SearchModeHybrid,
		})
		if err != nil {
			t.Fatalf("hybrid search: %v", err)
		}
		if len(res.Results) == 0 {
			t.Fatal("hybrid search returned no results")
		}
		if res.Results[0].Document.ID != "doc-3" {
			t.Logf("hybrid search top hit = %s (want doc-3), results = %+v",
				res.Results[0].Document.ID, scoredIDs(res))
		}
	})

	t.Run("update", func(t *testing.T) {
		updated := e2eDoc("doc-1", "golang-notes-v2", "Golang scheduler and goroutine internals",
			nil, map[string]any{"category": "tech", "level": 5})
		// An empty embedding preserves the stored vector.
		if err := vs.Update(ctx, updated, nil); err != nil {
			t.Fatalf("Update: %v", err)
		}
		doc, emb, err := vs.Get(ctx, "doc-1")
		if err != nil {
			t.Fatalf("Get after update: %v", err)
		}
		if doc.Name != "golang-notes-v2" || doc.Content != updated.Content {
			t.Errorf("updated doc = (%q, %q)", doc.Name, doc.Content)
		}
		if doc.Metadata["level"] != float64(5) && doc.Metadata["level"] != 5 {
			t.Errorf("updated level = %v (%T), want 5", doc.Metadata["level"], doc.Metadata["level"])
		}
		if len(emb) != e2eDim || emb[0] != 1 {
			t.Errorf("embedding changed after empty-embedding update: %v", emb)
		}

		// Updating with a new embedding replaces the vector.
		if err := vs.Update(ctx, updated, []float64{0.8, 0.2, 0, 0}); err != nil {
			t.Fatalf("Update with embedding: %v", err)
		}
		_, emb, err = vs.Get(ctx, "doc-1")
		if err != nil {
			t.Fatalf("Get after embedding update: %v", err)
		}
		if math.Abs(emb[0]-0.8) > 1e-6 {
			t.Errorf("embedding = %v, want first component 0.8", emb)
		}

		// Updating a missing document fails.
		if err := vs.Update(ctx, e2eDoc("missing", "x", "x", nil, nil), nil); err == nil {
			t.Error("Update on missing document should fail")
		}
	})

	t.Run("update by filter", func(t *testing.T) {
		n, err := vs.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
				Field:    "category",
				Operator: searchfilter.OperatorEqual,
				Value:    "tech",
			}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"metadata.reviewed": true}),
		)
		if err != nil {
			t.Fatalf("UpdateByFilter: %v", err)
		}
		if n != 2 {
			t.Errorf("UpdateByFilter updated %d records, want 2", n)
		}
		md, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"doc-2"}))
		if err != nil {
			t.Fatalf("GetMetadata after UpdateByFilter: %v", err)
		}
		if md["doc-2"].Metadata["reviewed"] != true {
			t.Errorf("doc-2 reviewed = %v, want true", md["doc-2"].Metadata["reviewed"])
		}

		// Reserved keys cannot be updated.
		if _, err := vs.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc-1"}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"id": "other"}),
		); err == nil {
			t.Error("UpdateByFilter with reserved key id should fail")
		}

		// Content updates rewrite the stored document body.
		n, err = vs.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc-2"}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"content": "Chroma Cloud vector search notes"}),
		)
		if err != nil {
			t.Fatalf("UpdateByFilter content: %v", err)
		}
		if n != 1 {
			t.Errorf("content UpdateByFilter updated %d records, want 1", n)
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := vs.Delete(ctx, "doc-3"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, _, err := vs.Get(ctx, "doc-3"); err == nil {
			t.Error("Get after Delete should fail")
		}
		n, err := vs.Count(ctx)
		if err != nil {
			t.Fatalf("Count after delete: %v", err)
		}
		if n != 2 {
			t.Errorf("count after delete = %d, want 2", n)
		}
	})

	t.Run("delete by filter", func(t *testing.T) {
		if err := vs.Add(ctx, e2eDoc("doc-4", "tmp", "temporary document one",
			nil, map[string]any{"category": "tmp"}), []float64{0, 1, 0, 0}); err != nil {
			t.Fatalf("Add doc-4: %v", err)
		}
		if err := vs.Add(ctx, e2eDoc("doc-5", "tmp", "temporary document two",
			nil, map[string]any{"category": "tmp"}), []float64{0, 0, 0, 1}); err != nil {
			t.Fatalf("Add doc-5: %v", err)
		}

		// Delete by IDs.
		if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"doc-4"})); err != nil {
			t.Fatalf("DeleteByFilter IDs: %v", err)
		}
		if _, _, err := vs.Get(ctx, "doc-4"); err == nil {
			t.Error("Get doc-4 after DeleteByFilter IDs should fail")
		}

		// Delete by metadata filter.
		if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{"category": "tmp"})); err != nil {
			t.Fatalf("DeleteByFilter metadata: %v", err)
		}
		if _, _, err := vs.Get(ctx, "doc-5"); err == nil {
			t.Error("Get doc-5 after DeleteByFilter metadata should fail")
		}

		// DeleteAll cannot be combined with other selectors.
		if err := vs.DeleteByFilter(ctx,
			vectorstore.WithDeleteAll(true),
			vectorstore.WithDeleteDocumentIDs([]string{"doc-1"}),
		); err == nil {
			t.Error("DeleteByFilter with DeleteAll and IDs should fail")
		}
	})

	t.Run("delete all", func(t *testing.T) {
		if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
			t.Fatalf("DeleteAll: %v", err)
		}
		n, err := vs.Count(ctx)
		if err != nil {
			t.Fatalf("Count after DeleteAll: %v", err)
		}
		if n != 0 {
			t.Errorf("count after DeleteAll = %d, want 0", n)
		}
	})

	t.Run("close", func(t *testing.T) {
		if err := vs.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

// TestVectorStoreE2EMissingCollection verifies that binding to a missing
// collection fails when auto-create is disabled.
func TestVectorStoreE2EMissingCollection(t *testing.T) {
	cfg := loadE2EConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := []Option{
		WithBaseURL(cfg.baseURL),
		WithAPIKey(cfg.apiKey),
		WithIndexDimension(e2eDim),
		WithCollection(fmt.Sprintf("e2e-missing-%d", time.Now().UnixNano())),
		WithAutoCreateCollection(false),
	}
	if cfg.tenant != "" {
		opts = append(opts, WithTenant(cfg.tenant))
	}
	if cfg.database != "" {
		opts = append(opts, WithDatabase(cfg.database))
	}
	if _, err := New(ctx, opts...); err == nil {
		t.Fatal("New on a missing collection with auto-create disabled should fail")
	}
}

// scoredIDs returns the IDs of scored documents for test diagnostics.
func scoredIDs(res *vectorstore.SearchResult) []string {
	var ids []string
	for _, r := range res.Results {
		ids = append(ids, r.Document.ID)
	}
	return ids
}
