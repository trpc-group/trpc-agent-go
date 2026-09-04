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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

type configuredFakeClient struct {
	*fakeClient
	info      storage.CollectionInfo
	batchSize int
}

type initContextKey struct{}

type contextCheckingClient struct {
	*fakeClient
	value any
}

func (f *contextCheckingClient) GetOrCreateCollection(
	ctx context.Context,
	_ string,
	_ map[string]any,
) error {
	f.value = ctx.Value(initContextKey{})
	return ctx.Err()
}

func (f *configuredFakeClient) CollectionInfo() storage.CollectionInfo {
	return f.info
}

func (f *configuredFakeClient) MaxBatchSize(context.Context) (int, error) {
	return f.batchSize, nil
}

func TestNewNamedInstanceAndMissingCollection(t *testing.T) {
	fc := newFakeClient()
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return fc, nil
	})
	defer storage.SetClientBuilder(oldBuilder)

	storage.RegisterChromaInstance("named", storage.WithBaseURL("http://ignored"))
	defer storage.UnregisterChromaInstance("named")
	vs, err := New(context.Background(), WithInstanceName("named"), WithCollection("col"), WithIndexDimension(3))
	if err != nil {
		t.Fatalf("New named: %v", err)
	}
	if fc.getOrCreateCalls != 1 {
		t.Fatalf("GetOrCreate calls = %d", fc.getOrCreateCalls)
	}
	_ = vs.Close()

	if _, err := New(context.Background(), WithInstanceName("missing"), WithCollection("col"), WithIndexDimension(3)); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("missing instance err = %v", err)
	}

	fc2 := newFakeClient()
	fc2.missing = true
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return fc2, nil
	})
	_, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3), WithAutoCreateCollection(false))
	if err == nil || !errors.Is(err, errCollectionNotFound) && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing collection err = %v", err)
	}
	if fc2.getCollectionCalls != 1 {
		t.Fatalf("GetCollection calls = %d", fc2.getCollectionCalls)
	}
}

func TestCollectionConfigurationValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		info storage.CollectionInfo
		want string
	}{
		{name: "metric", info: storage.CollectionInfo{Metric: "l2", Dimension: 3}, want: "cosine is required"},
		{name: "empty metric", info: storage.CollectionInfo{Dimension: 3}, want: "did not report a cosine HNSW or SPANN index"},
		{name: "dimension", info: storage.CollectionInfo{Metric: "cosine", Dimension: 4}, want: "dimension mismatch"},
		{
			name: "missing sparse index",
			info: storage.CollectionInfo{
				Metric:      "cosine",
				Dimension:   3,
				SchemaKnown: true,
			},
			want: "has no sparse vector index",
		},
		{
			name: "unknown sparse schema",
			info: storage.CollectionInfo{Metric: "cosine", Dimension: 3},
			want: "did not report its schema",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &configuredFakeClient{fakeClient: newFakeClient(), info: tt.info}
			opts := testOptions()
			if strings.Contains(tt.name, "sparse") {
				WithSparseSearch(stubSparseEmbedder{})(&opts)
			}
			vs := &VectorStore{client: client, opts: opts, filterConverter: newChromaFilterConverter()}
			err := vs.initCollection(context.Background())
			assertErrContains(t, err, tt.want)
		})
	}

	client := &configuredFakeClient{
		fakeClient: newFakeClient(),
		info: storage.CollectionInfo{
			Metric:                "cosine",
			Dimension:             3,
			SchemaKnown:           true,
			SparseVectorIndexKeys: []string{"lexical"},
		},
	}
	vs := &VectorStore{
		client: client,
		opts: testOptions(
			WithSparseSearch(stubSparseEmbedder{}),
			WithSparseSearchKey("lexical"),
		),
		filterConverter: newChromaFilterConverter(),
	}
	if err := vs.initCollection(context.Background()); err != nil {
		t.Fatalf("matching sparse schema: %v", err)
	}
}

func TestNewErrorPaths(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return nil, errors.New("build fail")
	})
	defer storage.SetClientBuilder(oldBuilder)
	_, err := New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3))
	assertErrContains(t, err, "build fail")

	fc := newFakeClient()
	fc.getOrCreateErr = errors.New("init fail")
	fc.closeErr = errors.New("close fail")
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return fc, nil
	})
	_, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3))
	assertErrContains(t, err, "init fail")
	assertErrContains(t, err, "close fail")

	fc2 := newFakeClient()
	fc2.getCollectionErr = errors.New("permission denied")
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return fc2, nil
	})
	_, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3), WithAutoCreateCollection(false))
	assertErrContains(t, err, "permission denied")
}

func TestNewRequiresURL(t *testing.T) {
	_, err := New(context.Background(), WithCollection("col"), WithIndexDimension(3))
	if err == nil || !strings.Contains(err.Error(), "WithInstanceName or WithBaseURL") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewPropagatesInitializationContext(t *testing.T) {
	client := &contextCheckingClient{fakeClient: newFakeClient()}
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(oldBuilder)

	ctx := context.WithValue(context.Background(), initContextKey{}, "trace-value")
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := New(
		ctx,
		WithBaseURL("http://127.0.0.1:8000"),
		WithCollection("col"),
		WithIndexDimension(3),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("New() error = %v, want context.Canceled", err)
	}
	if client.value != "trace-value" {
		t.Fatalf("initialization context value = %#v", client.value)
	}
	if client.closeCalls != 1 {
		t.Fatalf("client close calls = %d, want 1", client.closeCalls)
	}
}

func TestAddGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	doc := &document.Document{
		ID:      "d1",
		Name:    "guide",
		Content: "hello chroma",
		Metadata: map[string]any{
			"category": "guide",
			"nested":   map[string]any{"a": 1},
		},
	}
	emb := []float64{0.1, 0.2, 0.3}
	if err := vs.Add(ctx, doc, emb); err != nil {
		t.Fatal(err)
	}
	if fc.upsertCalls != 1 || !reflect.DeepEqual(fc.lastUpsert.IDs, []string{"d1"}) {
		t.Fatalf("Upsert request = %#v calls=%d", fc.lastUpsert, fc.upsertCalls)
	}
	if len(fc.lastUpsert.Embeddings) != 1 || len(fc.lastUpsert.Embeddings[0]) != 3 {
		t.Fatalf("Upsert embeddings = %#v", fc.lastUpsert.Embeddings)
	}
	got, gotEmb, err := vs.Get(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fc.lastGet.IDs, []string{"d1"}) {
		t.Fatalf("Get IDs = %#v", fc.lastGet.IDs)
	}
	if got.ID != "d1" || got.Name != "guide" || got.Content != "hello chroma" {
		t.Fatalf("doc = %#v", got)
	}
	if got.Metadata["category"] != "guide" {
		t.Fatalf("metadata = %#v", got.Metadata)
	}
	if _, ok := got.Metadata["nested"]; !ok {
		t.Fatalf("nested metadata missing: %#v", got.Metadata)
	}
	if len(gotEmb) != len(emb) {
		t.Fatalf("emb len = %d", len(gotEmb))
	}
	for i := range emb {
		if diff := gotEmb[i] - emb[i]; diff > 1e-5 || diff < -1e-5 {
			t.Fatalf("emb[%d] = %v want %v", i, gotEmb[i], emb[i])
		}
	}
}

func TestAddRejectsReservedMetadataKeys(t *testing.T) {
	for _, key := range []string{metaName, metaCreatedAt, metaUpdatedAt, metaJSON} {
		t.Run(key, func(t *testing.T) {
			fc := newFakeClient()
			vs := testVectorStore(fc)
			err := vs.Add(context.Background(), &document.Document{
				ID:       "doc-1",
				Metadata: map[string]any{key: "caller-value"},
			}, []float64{1, 0, 0})
			assertErrContains(t, err, "conflicts with a reserved document field")
			if fc.lastUpsert.IDs != nil {
				t.Fatalf("upsert called for conflicting metadata: %#v", fc.lastUpsert)
			}
		})
	}
}

func TestAddReplacesMetadata(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	if err := vs.Add(ctx, &document.Document{
		ID: "d1", Content: "one", Metadata: map[string]any{"keep": "yes", "drop": "old"},
	}, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := vs.Add(ctx, &document.Document{
		ID: "d1", Content: "two", Metadata: map[string]any{"keep": "yes"},
	}, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if value, ok := fc.lastUpsert.Metadatas[0]["drop"]; !ok || value != nil {
		t.Fatalf("drop deletion marker = %#v", fc.lastUpsert.Metadatas[0])
	}
	got, _, err := vs.Get(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "two" || got.Metadata["keep"] != "yes" {
		t.Fatalf("upserted = %#v", got)
	}
	if _, ok := got.Metadata["drop"]; ok {
		t.Fatalf("stale metadata key remained: %#v", got.Metadata)
	}
}

func TestAddNewIDSkipsDeletionMarkers(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	if err := vs.Add(ctx, &document.Document{
		ID: "new", Content: "c", Metadata: map[string]any{"keep": "yes"},
	}, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if fc.getCalls != 1 {
		t.Fatalf("new ID still needs an existence Get, getCalls = %d", fc.getCalls)
	}
	if !reflect.DeepEqual(fc.lastGet.Include, includeMetadataOnlyFields) {
		t.Fatalf("pre-upsert include = %#v", fc.lastGet.Include)
	}
	for key, value := range fc.lastUpsert.Metadatas[0] {
		if value == nil {
			t.Fatalf("new ID sent deletion marker for %q: %#v", key, fc.lastUpsert.Metadatas[0])
		}
	}
}

func TestCRUDErrors(t *testing.T) {
	ctx := context.Background()
	vs := testVectorStore(newFakeClient())
	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{name: "add nil", run: func() error { return vs.Add(ctx, nil, []float64{1, 2, 3}) }, want: errDocumentRequired},
		{name: "add empty id", run: func() error { return vs.Add(ctx, &document.Document{}, []float64{1, 2, 3}) }, want: errDocumentIDRequired},
		{name: "add dim", run: func() error { return vs.Add(ctx, &document.Document{ID: "x"}, []float64{1}) }, want: errVectorDimMismatch},
		{name: "get empty", run: func() error { _, _, err := vs.Get(ctx, ""); return err }, want: errDocumentIDRequired},
		{name: "get missing", run: func() error { _, _, err := vs.Get(ctx, "missing"); return err }, want: errNotFound},
		{name: "update nil", run: func() error { return vs.Update(ctx, nil, nil) }, want: errDocumentRequired},
		{name: "update empty id", run: func() error { return vs.Update(ctx, &document.Document{}, nil) }, want: errDocumentIDRequired},
		{name: "update dim", run: func() error { return vs.Update(ctx, &document.Document{ID: "x"}, []float64{1}) }, want: errVectorDimMismatch},
		{name: "update missing", run: func() error { return vs.Update(ctx, &document.Document{ID: "missing"}, nil) }, want: errNotFound},
		{name: "delete empty", run: func() error { return vs.Delete(ctx, "") }, want: errDocumentIDRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	fcNil := newFakeClient()
	fcNil.getNil = true
	if _, _, err := testVectorStore(fcNil).Get(ctx, "id"); !errors.Is(err, errNotFound) {
		t.Fatalf("nil get = %v", err)
	}
	if err := vs.Add(ctx, &document.Document{ID: "ch", Metadata: map[string]any{"fn": func() {}}}, []float64{1, 0, 0}); err == nil {
		t.Fatal("unmarshallable metadata should fail")
	}
}

func TestUpdateExisting(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	created := time.Unix(1_700_000_000, 0)
	doc := &document.Document{
		ID: "d1", Name: "old", Content: "c", CreatedAt: created,
		Metadata: map[string]any{"old": "value"},
	}
	if err := vs.Add(ctx, doc, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	t.Run("empty embedding is sent so Chroma keeps the stored vector", func(t *testing.T) {
		update := &document.Document{
			ID: "d1", Name: "new", Content: "c2",
			Metadata: map[string]any{"replacement": "value"},
		}
		if err := vs.Update(ctx, update, nil); err != nil {
			t.Fatal(err)
		}
		if !update.CreatedAt.IsZero() {
			t.Fatalf("Update mutated caller document: %#v", update)
		}
		if fc.updateCalls != 1 {
			t.Fatalf("updateCalls = %d", fc.updateCalls)
		}
		if len(fc.lastUpdate.Embeddings) == 0 || len(fc.lastUpdate.Embeddings[0]) != 3 {
			t.Fatalf("Update must send preserved embeddings, got %#v", fc.lastUpdate.Embeddings)
		}
		if fc.lastUpdate.Embeddings[0][0] < 0.9 {
			t.Fatalf("preserved embedding = %v", fc.lastUpdate.Embeddings[0])
		}
		if ts, _ := fc.lastUpdate.Metadatas[0][metaCreatedAt].(int64); ts != created.Unix() {
			t.Fatalf("created_at = %v, want preserved", fc.lastUpdate.Metadatas[0][metaCreatedAt])
		}
		if value, ok := fc.lastUpdate.Metadatas[0]["old"]; !ok || value != nil {
			t.Fatalf("old metadata deletion marker = %#v", fc.lastUpdate.Metadatas[0])
		}
		got, emb, err := vs.Get(ctx, "d1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "new" || got.Content != "c2" {
			t.Fatalf("got = %#v", got)
		}
		if _, ok := got.Metadata["old"]; ok || got.Metadata["replacement"] != "value" {
			t.Fatalf("metadata replacement = %#v", got.Metadata)
		}
		if len(emb) != 3 || emb[0] < 0.9 {
			t.Fatalf("emb preserved = %v", emb)
		}
	})

	t.Run("new embedding overwrites", func(t *testing.T) {
		if err := vs.Update(ctx, &document.Document{ID: "d1", Name: "new", Content: "c2"}, []float64{0, 1, 0}); err != nil {
			t.Fatal(err)
		}
		if len(fc.lastUpdate.Embeddings) == 0 || fc.lastUpdate.Embeddings[0][1] < 0.9 {
			t.Fatalf("new embedding = %#v", fc.lastUpdate.Embeddings)
		}
	})
}

func TestRPCErrorWrapping(t *testing.T) {
	ctx := context.Background()
	rpcErr := errors.New("network")
	tests := []struct {
		name string
		set  func(*fakeClient)
		run  func(*VectorStore) error
		want string
	}{
		{
			name: "add upsert",
			set:  func(f *fakeClient) { f.upsertErr = rpcErr },
			run: func(vs *VectorStore) error {
				return vs.Add(ctx, &document.Document{ID: "id", Content: "c"}, []float64{1, 0, 0})
			},
			want: "network",
		},
		{
			name: "add get before upsert",
			set:  func(f *fakeClient) { f.getErr = rpcErr },
			run: func(vs *VectorStore) error {
				return vs.Add(ctx, &document.Document{ID: "id", Content: "c"}, []float64{1, 0, 0})
			},
			want: "get before upsert",
		},
		{
			name: "get",
			set:  func(f *fakeClient) { f.getErr = rpcErr },
			run:  func(vs *VectorStore) error { _, _, err := vs.Get(ctx, "id"); return err },
			want: "network",
		},
		{
			name: "delete",
			set:  func(f *fakeClient) { f.deleteErr = rpcErr },
			run:  func(vs *VectorStore) error { return vs.Delete(ctx, "id") },
			want: "network",
		},
		{
			name: "count",
			set:  func(f *fakeClient) { f.countErr = rpcErr },
			run:  func(vs *VectorStore) error { _, err := vs.Count(ctx); return err },
			want: "network",
		},
		{
			name: "delete by filter rpc",
			set:  func(f *fakeClient) { f.deleteErr = rpcErr },
			run: func(vs *VectorStore) error {
				return vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"id"}))
			},
			want: "network",
		},
		{
			name: "get metadata rpc",
			set:  func(f *fakeClient) { f.getErr = rpcErr },
			run:  func(vs *VectorStore) error { _, err := vs.GetMetadata(ctx); return err },
			want: "network",
		},
		{
			name: "get metadata limited rpc",
			set:  func(f *fakeClient) { f.getErr = rpcErr },
			run: func(vs *VectorStore) error {
				_, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(1))
				return err
			},
			want: "network",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := newFakeClient()
			tt.set(fc)
			assertErrContains(t, tt.run(testVectorStore(fc)), tt.want)
		})
	}

	t.Run("update rpc after get", func(t *testing.T) {
		fc := newFakeClient()
		vs := testVectorStore(fc)
		if err := vs.Add(ctx, &document.Document{ID: "id", Content: "c"}, []float64{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		fc.updateErr = rpcErr
		assertErrContains(t, vs.Update(ctx, &document.Document{ID: "id", Content: "n"}, []float64{1, 0, 0}), "network")
	})
}

func TestCloseError(t *testing.T) {
	fc := newFakeClient()
	fc.closeErr = errors.New("boom")
	vs := testVectorStore(fc)
	if err := vs.Close(); err == nil || err.Error() != "boom" {
		t.Fatalf("close = %v", err)
	}
	empty := &VectorStore{}
	if err := empty.Close(); err != nil {
		t.Fatalf("nil client close = %v", err)
	}
}

func TestDeleteByFilter(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	_ = vs.Add(ctx, &document.Document{ID: "a", Content: "a", Metadata: map[string]any{"category": "guide"}}, []float64{1, 0, 0})
	_ = vs.Add(ctx, &document.Document{ID: "b", Content: "b", Metadata: map[string]any{"category": "guide"}}, []float64{0, 1, 0})
	_ = vs.Add(ctx, &document.Document{ID: "c", Content: "c", Metadata: map[string]any{"category": "other"}}, []float64{0, 0, 1})

	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"a", "b"})); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fc.lastDelete.IDs, []string{"a", "b"}) {
		t.Fatalf("delete ids = %#v", fc.lastDelete.IDs)
	}
	if _, _, err := vs.Get(ctx, "a"); !errors.Is(err, errNotFound) {
		t.Fatalf("a still there: %v", err)
	}

	if err := vs.DeleteByFilter(ctx); err == nil {
		t.Fatal("delete no selector")
	}
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatalf("explicit DeleteAll failed: %v", err)
	}

	vsEmpty := testVectorStore(newFakeClient())
	if err := vsEmpty.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}

	fcDel := newFakeClient()
	fcDel.getErr = errors.New("list fail")
	if err := testVectorStore(fcDel).DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err == nil || !strings.Contains(err.Error(), "list fail") {
		t.Fatalf("deleteAll list err = %v", err)
	}
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true), vectorstore.WithDeleteDocumentIDs([]string{"c"})); err == nil {
		t.Fatal("DeleteAll combined with IDs should fail")
	}
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true), vectorstore.WithDeleteFilter(map[string]any{"category": "guide"})); err == nil {
		t.Fatal("DeleteAll combined with metadata filter should fail")
	}

	vs2 := testVectorStore(newFakeClient())
	_ = vs2.Add(ctx, &document.Document{ID: "x", Content: "x"}, []float64{1, 0, 0})
	if err := vs2.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vs2.Get(ctx, "x"); !errors.Is(err, errNotFound) {
		t.Fatalf("delete all left x: %v", err)
	}

	vs3 := testVectorStore(newFakeClient())
	_ = vs3.Add(ctx, &document.Document{ID: "g", Content: "g", Metadata: map[string]any{"category": "guide"}}, []float64{1, 0, 0})
	if err := vs3.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{"category": "guide"})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vs3.Get(ctx, "g"); !errors.Is(err, errNotFound) {
		t.Fatalf("filter delete left g: %v", err)
	}

	fcNil := newFakeClient()
	if err := testVectorStore(fcNil).DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}
	fcNil.getNil = true
	if err := testVectorStore(fcNil).DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}

	fcPage := newFakeClient()
	vsPage := testVectorStore(fcPage)
	_ = vsPage.Add(ctx, &document.Document{ID: "p", Content: "p"}, []float64{1, 0, 0})
	fcPage.fullPageGets = 1
	if err := vsPage.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAllBatchesIDs(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	for _, id := range []string{"d1", "d2", "d3"} {
		_ = vs.Add(ctx, &document.Document{ID: id, Content: "c"}, []float64{1, 0, 0})
	}
	// A batch size of 2 requires two delete requests for three records.
	fc.batchSize = 2
	before := fc.deleteCalls
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatal(err)
	}
	if calls := fc.deleteCalls - before; calls != 2 {
		t.Fatalf("delete calls = %d, want 2", calls)
	}
	if len(fc.lastDelete.IDs) != 1 {
		t.Fatalf("last batch = %#v, want the 1-ID remainder", fc.lastDelete.IDs)
	}
	for _, id := range []string{"d1", "d2", "d3"} {
		if _, _, err := vs.Get(ctx, id); !errors.Is(err, errNotFound) {
			t.Fatalf("%s still there: %v", id, err)
		}
	}
}

func TestDeleteByFilterBatchesExplicitIDs(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	fc.batchSize = 2
	vs := testVectorStore(fc)
	for _, id := range []string{"d1", "d2", "d3"} {
		if err := vs.Add(ctx, &document.Document{ID: id, Content: "c"}, []float64{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}
	before := fc.deleteCalls
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"d1", "d2", "d3"})); err != nil {
		t.Fatal(err)
	}
	if calls := fc.deleteCalls - before; calls != 2 {
		t.Fatalf("delete calls = %d, want 2", calls)
	}
	if !reflect.DeepEqual(fc.lastDelete.IDs, []string{"d3"}) {
		t.Fatalf("last delete IDs = %#v", fc.lastDelete.IDs)
	}
}

func TestDeleteAllRejectsDeleteWithoutProgress(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	if err := vs.Add(ctx, &document.Document{ID: "d1", Content: "c"}, []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	fc.deleteNoop = true

	err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
	if err == nil || !strings.Contains(err.Error(), "made no progress") {
		t.Fatalf("DeleteAll() error = %v", err)
	}
	if fc.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", fc.deleteCalls)
	}
}

func TestSameIDSetIgnoresOrder(t *testing.T) {
	if !sameIDSet([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("sameIDSet should ignore result ordering")
	}
	if sameIDSet([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("sameIDSet should reject different IDs")
	}
}

func TestUpdateByFilterAndCountMetadata(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	_ = vs.Add(ctx, &document.Document{ID: "1", Name: "n", Content: "c", Metadata: map[string]any{"category": "guide", "n": 1}}, []float64{1, 0, 0})
	_ = vs.Add(ctx, &document.Document{ID: "2", Name: "n", Content: "c", Metadata: map[string]any{"category": "guide", "n": 2}}, []float64{0, 1, 0})
	_ = vs.Add(ctx, &document.Document{ID: "3", Name: "n", Content: "c", Metadata: map[string]any{"category": "other", "n": 9}}, []float64{0, 0, 1})

	n, err := vs.Count(ctx, vectorstore.WithCountFilter(map[string]any{"category": "guide"}))
	if err != nil || n != 2 {
		t.Fatalf("count = %d %v", n, err)
	}
	if fc.lastGet.Limit == nil || *fc.lastGet.Limit != defaultMaxRequestRecords {
		t.Fatalf("count filter Get limit = %#v, want %d", fc.lastGet.Limit, defaultMaxRequestRecords)
	}

	updated, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "renamed", "metadata.category": "new"}),
	)
	if err != nil || updated != 1 {
		t.Fatalf("update = %d %v", updated, err)
	}
	got, _, _ := vs.Get(ctx, "1")
	if got.Name != "renamed" || got.Metadata["category"] != "new" {
		t.Fatalf("updated doc = %#v", got)
	}
	condUpdated, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
			Field: "category", Operator: searchfilter.OperatorEqual, Value: "other",
		}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"content": "updated", "embedding": []float64{0, 0, 1}}),
	)
	if err != nil || condUpdated != 1 {
		t.Fatalf("condition update = %d %v", condUpdated, err)
	}
	t.Run("metadata representation changes do not retain stale scalar", func(t *testing.T) {
		complexValue := map[string]any{"nested": "value"}
		n, err := vs.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"metadata.category": complexValue}),
		)
		if err != nil || n != 1 {
			t.Fatalf("scalar to complex update = %d, %v", n, err)
		}
		got, _, err := vs.Get(ctx, "1")
		if err != nil || !reflect.DeepEqual(got.Metadata["category"], complexValue) {
			t.Fatalf("scalar to complex round trip = %#v, %v", got, err)
		}
		if value, ok := fc.lastUpdate.Metadatas[0]["category"]; !ok || value != nil {
			t.Fatalf("stale scalar deletion marker = %#v", fc.lastUpdate.Metadatas[0])
		}
	})

	t.Run("UpdateByFilter rewrites matches in one batch", func(t *testing.T) {
		fcBatch := newFakeClient()
		vsBatch := testVectorStore(fcBatch)
		if err := vsBatch.Add(ctx, &document.Document{ID: "a", Content: "a", Metadata: map[string]any{"n": 1}}, []float64{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		if err := vsBatch.Add(ctx, &document.Document{ID: "b", Content: "b", Metadata: map[string]any{"n": 2}}, []float64{0, 1, 0}); err != nil {
			t.Fatal(err)
		}
		before := fcBatch.updateCalls
		n, err := vsBatch.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
				Field: "n", Operator: searchfilter.OperatorGreaterThan, Value: -1,
			}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "batched"}),
		)
		if err != nil || n != 2 {
			t.Fatalf("batch update = %d %v", n, err)
		}
		if calls := fcBatch.updateCalls - before; calls != 1 {
			t.Fatalf("update calls = %d, want 1", calls)
		}
		if !reflect.DeepEqual(fcBatch.lastUpdate.IDs, []string{"a", "b"}) {
			t.Fatalf("batch IDs = %#v", fcBatch.lastUpdate.IDs)
		}
	})

	t.Run("UpdateByFilter respects server batch size", func(t *testing.T) {
		fc := newFakeClient()
		client := &configuredFakeClient{fakeClient: fc, batchSize: 2}
		vsBatch := &VectorStore{client: client, opts: testOptions(), filterConverter: newChromaFilterConverter()}
		for _, id := range []string{"a", "b", "c"} {
			_ = vsBatch.Add(ctx, &document.Document{ID: id, Content: id}, []float64{1, 0, 0})
		}
		n, err := vsBatch.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"a", "b", "c"}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "batched"}),
		)
		if err != nil || n != 3 || fc.updateCalls != 2 {
			t.Fatalf("batch update = %d calls=%d err=%v", n, fc.updateCalls, err)
		}
	})

	t.Run("UpdateByFilter rejects oversized match set before writing", func(t *testing.T) {
		fc := newFakeClient()
		vsLimited := testVectorStore(fc, WithMaxUpdateRecords(2))
		for _, id := range []string{"a", "b", "c"} {
			_ = vsLimited.Add(ctx, &document.Document{ID: id, Content: id}, []float64{1, 0, 0})
		}
		n, err := vsLimited.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"a", "b", "c"}),
			vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "blocked"}),
		)
		if err == nil || !strings.Contains(err.Error(), "more than 2 records") {
			t.Fatalf("UpdateByFilter() = %d, %v, want match limit error", n, err)
		}
		if fc.updateCalls != 0 {
			t.Fatalf("update calls = %d, want 0", fc.updateCalls)
		}
	})

	if _, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"id": "x"}),
	); err == nil {
		t.Fatal("id update should fail")
	}
	if _, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"created_at": 1}),
	); err == nil {
		t.Fatal("created_at update should fail")
	}
	vsSparse := testVectorStore(fc, WithSparseSearch(stubSparseEmbedder{}))
	if _, err := vsSparse.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"metadata." + defaultSparseEmbeddingKey: "user value",
		}),
	); err == nil {
		t.Fatal("configured sparse metadata update should fail")
	}
	missingN, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"missing"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "x"}),
	)
	if err != nil || missingN != 0 {
		t.Fatalf("missing update = %d %v", missingN, err)
	}
}

func TestGetMetadataCountAndUpdateErrors(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	_ = vs.Add(ctx, &document.Document{ID: "1", Name: "n", Content: "c", Metadata: map[string]any{"category": "guide", "n": 1}}, []float64{1, 0, 0})
	_ = vs.Add(ctx, &document.Document{ID: "2", Name: "n", Content: "c", Metadata: map[string]any{"category": "guide", "n": 2}}, []float64{0, 1, 0})
	_ = vs.Add(ctx, &document.Document{ID: "3", Name: "n", Content: "c", Metadata: map[string]any{"category": "other", "n": 9}}, []float64{0, 0, 1})

	mds, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(2), vectorstore.WithGetMetadataOffset(0))
	if err != nil || len(mds) != 2 {
		t.Fatalf("get metadata = %d %v", len(mds), err)
	}
	page, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(-1))
	if err != nil || len(page) != 3 {
		t.Fatalf("paginated metadata = %d %v", len(page), err)
	}
	byID, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"2"}))
	if err != nil || len(byID) != 1 {
		t.Fatalf("metadata by id = %d %v", len(byID), err)
	}
	if _, err := vs.Count(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataFilter(map[string]any{"category": "guide"}), vectorstore.WithGetMetadataLimit(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := vs.Count(ctx, vectorstore.WithCountFilter(map[string]any{"$bad": 1})); err == nil {
		t.Fatal("bad count filter")
	}
	fcCnt := newFakeClient()
	fcCnt.getErr = errors.New("count fail")
	if _, err := testVectorStore(fcCnt).Count(ctx, vectorstore.WithCountFilter(map[string]any{"n": 1})); err == nil || !strings.Contains(err.Error(), "count fail") {
		t.Fatalf("count filter err = %v", err)
	}
	if err := vs.collectMetadata(map[string]vectorstore.DocumentMetadata{}, nil); err != nil {
		t.Fatalf("collectMetadata nil = %v", err)
	}

	if _, err := vs.UpdateByFilter(ctx); err == nil {
		t.Fatal("update by filter requires selector")
	}
	if _, err := vs.UpdateByFilter(ctx, vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"})); err == nil {
		t.Fatal("update by filter requires updates")
	}
	if _, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
			Field: "category", Operator: searchfilter.OperatorLike, Value: "g",
		}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "x"}),
	); err == nil {
		t.Fatal("like condition should fail")
	}

	fcUp := newFakeClient()
	_ = testVectorStore(fcUp).Add(ctx, &document.Document{ID: "1", Content: "c"}, []float64{1, 0, 0})
	fcUp.getErr = errors.New("get for update")
	if _, err := testVectorStore(fcUp).UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "x"}),
	); err == nil || !strings.Contains(err.Error(), "get for update") {
		t.Fatalf("update get err = %v", err)
	}

	fcUp2 := newFakeClient()
	vsUp := testVectorStore(fcUp2)
	_ = vsUp.Add(ctx, &document.Document{ID: "1", Content: "c"}, []float64{1, 0, 0})
	fcUp2.updateErr = errors.New("update fail")
	if _, err := vsUp.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "x"}),
	); err == nil || !strings.Contains(err.Error(), "update fail") {
		t.Fatalf("update rpc err = %v", err)
	}

	if _, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": 1}),
	); err == nil {
		t.Fatal("update by filter bad name type")
	}
	if _, err := vs.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"metadata.ch": make(chan int)}),
	); err == nil {
		t.Fatal("update by filter nested marshal")
	}

	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{"$bad": 1})); err == nil {
		t.Fatal("invalid delete filter")
	}
	if _, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataFilter(map[string]any{"$bad": 1})); err == nil {
		t.Fatal("invalid metadata filter")
	}
	if _, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(0)); err == nil {
		t.Fatal("limit 0")
	}

	fcNil := newFakeClient()
	fcNil.getNil = true
	if n, err := testVectorStore(fcNil).Count(ctx, vectorstore.WithCountFilter(map[string]any{"n": 1})); err != nil || n != 0 {
		t.Fatalf("nil get count = %d %v", n, err)
	}

	fcPage := newFakeClient()
	fcPage.fullPageGets = 1
	pageAll, err := testVectorStore(fcPage).GetMetadata(ctx, vectorstore.WithGetMetadataLimit(-1))
	if err != nil || len(pageAll) != defaultMaxRequestRecords {
		t.Fatalf("full-page metadata = %d %v", len(pageAll), err)
	}
	fcRepeat := newFakeClient()
	fcRepeat.repeatPage = true
	_, err = testVectorStore(fcRepeat).Count(ctx, vectorstore.WithCountFilter(map[string]any{"n": 1}))
	if err == nil || !strings.Contains(err.Error(), "pagination made no progress") {
		t.Fatalf("repeated pagination error = %v", err)
	}
	if fcRepeat.getCalls != 2 {
		t.Fatalf("repeated pagination get calls = %d, want 2", fcRepeat.getCalls)
	}
	fcPageNil := newFakeClient()
	fcPageNil.getNil = true
	emptyMD, err := testVectorStore(fcPageNil).GetMetadata(ctx, vectorstore.WithGetMetadataLimit(-1))
	if err != nil || len(emptyMD) != 0 {
		t.Fatalf("nil get metadata = %d %v", len(emptyMD), err)
	}
	limitedNil, err := testVectorStore(fcPageNil).GetMetadata(ctx, vectorstore.WithGetMetadataLimit(3))
	if err != nil || len(limitedNil) != 0 {
		t.Fatalf("limited nil metadata = %d %v", len(limitedNil), err)
	}

	fcDelAll := newFakeClient()
	vsDel := testVectorStore(fcDelAll)
	_ = vsDel.Add(ctx, &document.Document{ID: "z", Content: "z"}, []float64{1, 0, 0})
	fcDelAll.deleteErr = errors.New("delete all fail")
	if err := vsDel.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err == nil || !strings.Contains(err.Error(), "delete all fail") {
		t.Fatalf("deleteAll rpc = %v", err)
	}

	docNilMD := &document.Document{ID: "n"}
	emb := []float64{1, 0, 0}
	if err := applyUpdates(docNilMD, &emb, map[string]any{"metadata.x": 1}, 3); err != nil || docNilMD.Metadata["x"].(int) != 1 {
		t.Fatalf("apply nil metadata = %#v %v", docNilMD.Metadata, err)
	}
}

func TestUnseenGetResultPreservesAlignment(t *testing.T) {
	seen := map[string]struct{}{"seen": {}}
	page := unseenGetResult(&storage.GetResult{
		IDs:        []string{"seen", "new-1", "new-1", "new-2"},
		Documents:  []string{"seen-doc", "doc-1", "duplicate-doc", "doc-2"},
		Embeddings: [][]float32{{0}, {1}, {9}, {2}},
		Metadatas:  []map[string]any{{"n": 0}, {"n": 1}, {"n": 9}, {"n": 2}},
		URIs:       []string{"seen-uri", "uri-1", "duplicate-uri", "uri-2"},
	}, seen)

	if !reflect.DeepEqual(page.IDs, []string{"new-1", "new-2"}) ||
		!reflect.DeepEqual(page.Documents, []string{"doc-1", "doc-2"}) ||
		!reflect.DeepEqual(page.Embeddings, [][]float32{{1}, {2}}) ||
		!reflect.DeepEqual(page.URIs, []string{"uri-1", "uri-2"}) ||
		page.Metadatas[0]["n"] != 1 || page.Metadatas[1]["n"] != 2 {
		t.Fatalf("unseen page = %#v", page)
	}

	trimmed := trimGetResult(page, 1)
	if len(trimmed.IDs) != 1 || trimmed.IDs[0] != "new-1" ||
		trimmed.Documents[0] != "doc-1" || trimmed.Embeddings[0][0] != 1 {
		t.Fatalf("trimGetResult = %#v", trimmed)
	}
	if trimGetResult(page, 8) != page {
		t.Fatal("trimGetResult longer than page should return the same result")
	}
}

func TestGetMetadataPagesAboveCloudLimit(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	for i := 0; i < defaultMaxRequestRecords+1; i++ {
		id := fmt.Sprintf("m%03d", i)
		if err := vs.Add(ctx, &document.Document{
			ID: id, Content: "c", Metadata: map[string]any{"g": 1},
		}, []float64{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}
	gets := fc.getCalls
	md, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(defaultMaxRequestRecords+1))
	if err != nil || len(md) != defaultMaxRequestRecords+1 {
		t.Fatalf("paged metadata = %d %v", len(md), err)
	}
	if fc.getCalls-gets != 2 {
		t.Fatalf("GetMetadata pages = %d, want 2", fc.getCalls-gets)
	}
	if fc.lastGet.Limit == nil || *fc.lastGet.Limit != 1 {
		t.Fatalf("second page Limit = %#v, want 1", fc.lastGet.Limit)
	}
}

func TestGetMetadataUnlimitedPagesAboveCloudLimit(t *testing.T) {
	ctx := context.Background()
	fc := newFakeClient()
	vs := testVectorStore(fc)
	for i := 0; i < defaultMaxRequestRecords+1; i++ {
		id := fmt.Sprintf("u%03d", i)
		if err := vs.Add(ctx, &document.Document{
			ID: id, Content: "c", Metadata: map[string]any{"g": 1},
		}, []float64{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}

	gets := fc.getCalls
	md, err := vs.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(-1))
	if err != nil || len(md) != defaultMaxRequestRecords+1 {
		t.Fatalf("unlimited paged metadata = %d %v", len(md), err)
	}
	if fc.getCalls-gets != 2 {
		t.Fatalf("GetMetadata pages = %d, want 2", fc.getCalls-gets)
	}
	if fc.lastGet.Limit == nil || *fc.lastGet.Limit != defaultMaxRequestRecords {
		t.Fatalf("unlimited page Limit = %#v, want %d", fc.lastGet.Limit, defaultMaxRequestRecords)
	}
}

func TestApplyUpdatesAndHelpers(t *testing.T) {
	doc := &document.Document{ID: "1", Metadata: map[string]any{}}
	emb := []float64{1, 0, 0}
	if err := applyUpdates(doc, &emb, map[string]any{"content": "c", "embedding": []float64{0, 1, 0}, "metadata.x": 1}, 3); err != nil {
		t.Fatal(err)
	}
	if doc.Content != "c" || emb[1] != 1 || doc.Metadata["x"].(int) != 1 {
		t.Fatalf("apply = %#v %v", doc, emb)
	}
	if err := applyUpdates(doc, &emb, map[string]any{"name": 1}, 3); err == nil {
		t.Fatal("bad name type")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"content": 1}, 3); err == nil {
		t.Fatal("bad content type")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"embedding": "x"}, 3); err == nil {
		t.Fatal("bad embedding type")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"embedding": []float64{1}}, 3); err == nil {
		t.Fatal("bad embedding dim")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"metadata.": 1}, 3); err == nil {
		t.Fatal("empty metadata key")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"unknown": 1}, 3); err == nil {
		t.Fatal("unknown key")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"metadata.created_at": 1}, 3); err == nil {
		t.Fatal("reserved metadata key should be rejected")
	}
	if err := applyUpdates(doc, &emb, map[string]any{"metadata._json": "x"}, 3); err == nil {
		t.Fatal("reserved _json metadata key should be rejected")
	}
}
