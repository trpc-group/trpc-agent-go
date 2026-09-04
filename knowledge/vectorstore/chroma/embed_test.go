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
	"math"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

type stubSparseEmbedder struct {
	indices []int
	values  []float64
	err     error
}

func (s stubSparseEmbedder) EmbedDocument(context.Context, string) (SparseVector, error) {
	return SparseVector{Indices: s.indices, Values: s.values}, s.err
}

func (s stubSparseEmbedder) EmbedQuery(context.Context, string) (SparseVector, error) {
	return SparseVector{Indices: s.indices, Values: s.values}, s.err
}

type asymmetricSparseEmbedder struct{}

func (asymmetricSparseEmbedder) EmbedDocument(context.Context, string) (SparseVector, error) {
	return SparseVector{Indices: []int{1}, Values: []float64{1}}, nil
}

func (asymmetricSparseEmbedder) EmbedQuery(context.Context, string) (SparseVector, error) {
	return SparseVector{Indices: []int{2}, Values: []float64{1}}, nil
}

func TestSparseQueryAndDocumentEmbedding(t *testing.T) {
	stub := stubSparseEmbedder{indices: []int{2, 8}, values: []float64{0.4, 0.3}}
	vs := testVectorStore(newFakeClient(), WithSparseSearch(stub))
	want := map[string]any{
		"#type":   "sparse_vector",
		"indices": []int{2, 8},
		"values":  []float64{0.4, 0.3},
	}

	got, err := vs.knnQuery(context.Background(), "query")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("knn query = %#v, want %#v", got, want)
	}

	rec := storage.RecordBatch{
		IDs:       []string{"d1"},
		Documents: []string{"document"},
		Metadatas: []map[string]any{{"kind": "primary"}},
	}
	if err := vs.attachSparseEmbedding(context.Background(), rec, "document"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Metadatas[0][defaultSparseEmbeddingKey], want) {
		t.Fatalf("sparse metadata = %#v", rec.Metadatas[0])
	}
}

func TestSparseEmbedderDistinguishesDocumentAndQuery(t *testing.T) {
	vs := testVectorStore(newFakeClient(), WithSparseSearch(asymmetricSparseEmbedder{}))
	query, err := vs.knnQuery(context.Background(), "query")
	if err != nil {
		t.Fatal(err)
	}
	queryIndices := query.(map[string]any)["indices"]
	if !reflect.DeepEqual(queryIndices, []int{2}) {
		t.Fatalf("query indices = %#v, want [2]", queryIndices)
	}
	rec := storage.RecordBatch{Metadatas: []map[string]any{{}}}
	if err := vs.attachSparseEmbedding(context.Background(), rec, "document"); err != nil {
		t.Fatal(err)
	}
	documentIndices := rec.Metadatas[0][defaultSparseEmbeddingKey].(map[string]any)["indices"]
	if !reflect.DeepEqual(documentIndices, []int{1}) {
		t.Fatalf("document indices = %#v, want [1]", documentIndices)
	}
}

func TestSparseEmbedderValidation(t *testing.T) {
	tests := []struct {
		name string
		stub stubSparseEmbedder
		want string
	}{
		{name: "error", stub: stubSparseEmbedder{err: errors.New("embed failed")}, want: "embed failed"},
		{name: "length mismatch", stub: stubSparseEmbedder{indices: []int{1, 2}, values: []float64{0.1}}, want: "2 indices and 1 values"},
		{name: "negative index", stub: stubSparseEmbedder{indices: []int{-1}, values: []float64{0.1}}, want: "invalid index"},
		{name: "duplicate index", stub: stubSparseEmbedder{indices: []int{1, 1}, values: []float64{0.1, 0.2}}, want: "strictly increasing"},
		{name: "unsorted indices", stub: stubSparseEmbedder{indices: []int{8, 2}, values: []float64{0.1, 0.2}}, want: "strictly increasing"},
		{name: "non-finite value", stub: stubSparseEmbedder{indices: []int{1}, values: []float64{math.Inf(1)}}, want: "invalid value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := testVectorStore(newFakeClient(), WithSparseSearch(tt.stub))
			_, err := vs.knnQuery(context.Background(), "q")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSparseEmbeddingIsRemovedForEmptyContent(t *testing.T) {
	vs := testVectorStore(
		newFakeClient(),
		WithSparseSearch(stubSparseEmbedder{}),
	)
	existing := map[string]any{
		defaultSparseEmbeddingKey: map[string]any{
			"#type":   "sparse_vector",
			"indices": []int{1},
			"values":  []float64{1},
		},
	}
	rec := storage.RecordBatch{Metadatas: []map[string]any{{}}}
	if err := vs.attachSparseEmbedding(context.Background(), rec, ""); err != nil {
		t.Fatal(err)
	}
	vs.markAbsentMetadataNil(existing, rec)
	if value, ok := rec.Metadatas[0][defaultSparseEmbeddingKey]; !ok || value != nil {
		t.Fatalf("sparse deletion marker = %#v, want nil", rec.Metadatas[0])
	}
}

func TestKNNQueryRequiresSparseSearch(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	if _, err := vs.knnQuery(context.Background(), "q"); err == nil ||
		!strings.Contains(err.Error(), "sparse search is not configured") {
		t.Fatalf("knnQuery without sparse search = %v", err)
	}
}

func TestAttachSparseEmbeddingErrors(t *testing.T) {
	ctx := context.Background()

	vs := testVectorStore(newFakeClient(), WithSparseSearch(stubSparseEmbedder{err: errors.New("embed failed")}))
	rec := storage.RecordBatch{IDs: []string{"d1"}, Metadatas: []map[string]any{{}}}
	if err := vs.attachSparseEmbedding(ctx, rec, "content"); err == nil ||
		!strings.Contains(err.Error(), "embed failed") {
		t.Fatalf("embed error = %v", err)
	}

	vs = testVectorStore(newFakeClient(), WithSparseSearch(stubSparseEmbedder{indices: []int{1, 2}, values: []float64{0.1}}))
	if err := vs.attachSparseEmbedding(ctx, rec, "content"); err == nil ||
		!strings.Contains(err.Error(), "2 indices and 1 values") {
		t.Fatalf("invalid sparse vector = %v", err)
	}

	vs = testVectorStore(newFakeClient(), WithSparseSearch(stubSparseEmbedder{indices: []int{1}, values: []float64{1}}))
	emptyMeta := storage.RecordBatch{IDs: []string{"d1"}}
	if err := vs.attachSparseEmbedding(ctx, emptyMeta, "content"); err != nil {
		t.Fatalf("record without metadata = %v", err)
	}
}

func TestIsSparseVectorValue(t *testing.T) {
	if !isSparseVectorValue(map[string]any{"indices": []int{1}, "values": []float64{1}}) {
		t.Fatal("indices and values without #type should be treated as a sparse vector")
	}
	for _, v := range []any{
		"not a map",
		map[string]any{},
		map[string]any{"indices": []int{1}},
		map[string]any{"values": []float64{1}},
	} {
		if isSparseVectorValue(v) {
			t.Fatalf("isSparseVectorValue(%#v) = true, want false", v)
		}
	}
}

func TestUpdateClearsSparseEmbeddingForEmptyContent(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(
		fc,
		WithSparseSearch(stubSparseEmbedder{indices: []int{1}, values: []float64{1}}),
	)
	if err := vs.Add(ctx, &document.Document{ID: "d1", Content: "old keyword"}, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := vs.Update(ctx, &document.Document{ID: "d1"}, nil); err != nil {
		t.Fatal(err)
	}
	if value, ok := fc.lastUpdate.Metadatas[0][defaultSparseEmbeddingKey]; !ok || value != nil {
		t.Fatalf("sparse deletion marker = %#v, want nil", fc.lastUpdate.Metadatas[0])
	}
}
