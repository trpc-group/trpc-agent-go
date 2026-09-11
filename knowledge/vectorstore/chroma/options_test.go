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

	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

func testOptions(opts ...Option) options {
	o := defaultOptions
	o.collection = "docs"
	o.indexDimension = 3
	o.baseURL = "http://127.0.0.1:8000"
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// testVectorStore mirrors New: it applies option defaults and normalization.
// Validation errors are ignored so tests can construct stores with invalid
// options and assert the resulting behavior.
func testVectorStore(client storage.ClientInterface, opts ...Option) *VectorStore {
	o := testOptions(opts...)
	_ = validateOptions(&o)
	return &VectorStore{client: client, opts: o, filterConverter: newChromaFilterConverter()}
}

func assertErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*options)
		wantErr error
		contain string
	}{
		{name: "valid", mutate: func(*options) {}},
		{name: "missing collection", mutate: func(o *options) { o.collection = "" }, wantErr: errCollectionRequired},
		{name: "bad dim", mutate: func(o *options) { o.indexDimension = 0 }, contain: "indexDimension"},
		{name: "bad max", mutate: func(o *options) { o.maxResults = 0 }, contain: "maxResults"},
		{name: "bad request record limit", mutate: func(o *options) { o.maxRequestRecords = 0 }, contain: "max request records"},
		{name: "bad update record limit", mutate: func(o *options) { o.maxUpdateRecords = 0 }, contain: "max update records"},
		{name: "sparse search", mutate: func(o *options) { WithSparseSearch(stubSparseEmbedder{})(o) }},
		{name: "sparse search empty key defaults", mutate: func(o *options) {
			WithSparseSearch(stubSparseEmbedder{})(o)
			WithSparseSearchKey("")(o)
		}},
		{name: "nil sparse embedder defaults to cloud splade", mutate: func(o *options) {
			WithAPIKey("key")(o)
			WithSparseSearch(nil)(o)
		}},
		{name: "no sparse embedder without api key", mutate: func(o *options) {
			WithSparseSearch()(o)
		}, contain: "WithAPIKey"},
		{name: "reserved sparse key", mutate: func(o *options) {
			WithSparseSearch(stubSparseEmbedder{})(o)
			WithSparseSearchKey("#embedding")(o)
		}, contain: "reserved"},
		{name: "long collection name", mutate: func(o *options) { o.collection = strings.Repeat("a", 129) }},
		{name: "negative dense weight", mutate: func(o *options) {
			WithHybridWeights(-1, 2)(o)
		}, contain: "hybrid weights"},
		{name: "non-finite sparse weight", mutate: func(o *options) {
			WithHybridWeights(1, math.Inf(1))(o)
		}, contain: "hybrid weights"},
		{name: "zero weights", mutate: func(o *options) {
			WithHybridWeights(0, 0)(o)
		}, contain: "hybrid weights"},
		{name: "conflicting credentials", mutate: func(o *options) {
			WithAPIKey("key")(o)
			WithBearerToken("token")(o)
		}, contain: "mutually exclusive"},
		{name: "conflicting custom auth", mutate: func(o *options) {
			WithAPIKey("key")(o)
			WithHeaders(map[string]string{"Authorization": "custom"})(o)
		}, contain: "conflicts"},
		{name: "custom auth missing scope", mutate: func(o *options) {
			WithHeaders(map[string]string{"Authorization": "custom"})(o)
		}, contain: "tenant and database"},
		{name: "custom auth with scope", mutate: func(o *options) {
			WithHeaders(map[string]string{"Authorization": "custom"})(o)
			o.tenant = "t"
			o.database = "d"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := testOptions()
			tt.mutate(&o)
			err := validateOptions(&o)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.contain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.contain) {
					t.Fatalf("error = %v, want containing %q", err, tt.contain)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestHybridOptions(t *testing.T) {
	page := testOptions(WithMaxRequestRecords(17))
	if page.maxRequestRecords != 17 {
		t.Fatalf("max request records = %d, want 17", page.maxRequestRecords)
	}
	update := testOptions(WithMaxUpdateRecords(19))
	if update.maxUpdateRecords != 19 {
		t.Fatalf("max update records = %d, want 19", update.maxUpdateRecords)
	}

	stub := stubSparseEmbedder{indices: []int{9}, values: []float64{0.1}}
	o := testOptions(WithSparseSearch(stub), WithHybridWeights(2, 1))
	if err := validateOptions(&o); err != nil {
		t.Fatalf("valid weights should validate: %v", err)
	}
	if !o.sparseSearch ||
		o.sparseSearchKey != defaultSparseEmbeddingKey ||
		o.hybridDenseWeight != 2.0/3.0 ||
		o.hybridSparseWeight != 1.0/3.0 {
		t.Fatalf("weights = %#v", o)
	}
	o = testOptions(WithSparseSearch(stub), WithHybridWeights(0, 0))
	if err := validateOptions(&o); err == nil {
		t.Fatal("zero weights should fail validation")
	}
	o = testOptions(
		WithSparseSearch(stub),
		WithHybridWeights(-1, 2),
		WithHybridWeights(2, 1),
	)
	if err := validateOptions(&o); err != nil {
		t.Fatalf("later valid weights should override earlier error: %v", err)
	}
	o = testOptions(WithSparseSearch(stub))
	if !o.sparseSearch {
		t.Fatalf("sparse search default = %#v", o)
	}
	if err := validateOptions(&o); err != nil {
		t.Fatalf("default sparse key should validate: %v", err)
	}
	if o.sparseSearchKey != defaultSparseEmbeddingKey {
		t.Fatalf("default sparse key = %q", o.sparseSearchKey)
	}
	o = testOptions(WithSparseSearchKey("lexical"))
	if o.sparseSearch || o.sparseSearchKey != "lexical" {
		t.Fatalf("key without enable = %#v", o)
	}
	o = testOptions(WithSparseSearch(stub), WithSparseSearchKey("lexical"))
	if o.sparseSearchKey != "lexical" {
		t.Fatalf("key after WithSparseSearch = %q", o.sparseSearchKey)
	}

	o = testOptions(WithMaxResults(4), WithAutoCreateCollection(false))
	if o.maxResults != 4 || o.autoCreateCollection {
		t.Fatalf("flags = %#v", o)
	}
	o = testOptions(WithTenant("t"), WithDatabase("d"), WithBaseURL("http://x"))
	if o.tenant != "t" || o.database != "d" {
		t.Fatalf("conn opts = %#v", o)
	}
	o = testOptions(WithAPIKey("tok"))
	if o.authToken != "tok" {
		t.Fatalf("auth = %#v", o)
	}
	o = testOptions(WithAPIKey(" key "))
	if o.authToken != "key" {
		t.Fatalf("API key = %#v", o)
	}
	o = testOptions(WithBearerToken(" token "))
	if o.bearerToken != "token" {
		t.Fatalf("bearer token = %#v", o)
	}
	o = testOptions(
		WithHeaders(map[string]string{"Authorization": "Bearer token"}),
		WithHeaders(map[string]string{"X-Tenant": "tenant"}),
	)
	if o.headers["Authorization"] != "Bearer token" || o.headers["X-Tenant"] != "tenant" {
		t.Fatalf("headers = %#v", o.headers)
	}
}

func TestCollectClientBuilderOptsIncludesAuthenticationAndExtensions(t *testing.T) {
	bo, err := collectClientBuilderOpts(testOptions(
		WithAPIKey("key"),
		WithHeaders(map[string]string{"X-Custom": "value"}),
		WithExtraOptions("extension"),
	))
	if err != nil {
		t.Fatal(err)
	}
	got := &storage.ClientBuilderOpts{}
	for _, f := range bo {
		f(got)
	}
	if got.APIKey != "key" ||
		got.Headers["X-Custom"] != "value" ||
		!reflect.DeepEqual(got.ExtraOptions, []any{"extension"}) {
		t.Fatalf("authentication options not forwarded: %#v", got)
	}
}

func TestNewForwardsSparseVectorIndex(t *testing.T) {
	fc := newFakeClient()
	var got storage.ClientBuilderOpts
	old := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.ClientInterface, error) {
		got = storage.ClientBuilderOpts{}
		for _, opt := range opts {
			opt(&got)
		}
		fc.info = storage.CollectionInfo{
			Metric:                "cosine",
			SchemaKnown:           got.SparseVectorIndexKey != "",
			SparseVectorIndexKeys: []string{got.SparseVectorIndexKey},
		}
		return fc, nil
	})
	defer storage.SetClientBuilder(old)

	stub := stubSparseEmbedder{indices: []int{1}, values: []float64{1}}
	vs, err := New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3), WithSparseSearch(stub))
	if err != nil {
		t.Fatal(err)
	}
	_ = vs.Close()
	if got.SparseVectorIndexKey != defaultSparseEmbeddingKey {
		t.Fatalf("default sparse key = %q", got.SparseVectorIndexKey)
	}

	vs, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3), WithSparseSearch(stub), WithSparseSearchKey("lexical"))
	if err != nil {
		t.Fatal(err)
	}
	_ = vs.Close()
	if got.SparseVectorIndexKey != "lexical" {
		t.Fatalf("custom sparse key = %q", got.SparseVectorIndexKey)
	}
	if len(got.SparseVectorIndexFunction) != 0 {
		t.Fatalf("custom embedder declared a function: %#v", got.SparseVectorIndexFunction)
	}

	// The built-in Cloud SPLADE embedder declares its registry entry.
	splade, err := NewCloudSpladeEmbedder("key")
	if err != nil {
		t.Fatal(err)
	}
	vs, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3), WithSparseSearch(splade))
	if err != nil {
		t.Fatal(err)
	}
	_ = vs.Close()
	fn := got.SparseVectorIndexFunction
	if fn["type"] != "known" || fn["name"] != "chroma-cloud-splade" {
		t.Fatalf("splade schema function = %#v", fn)
	}
	config := fn["config"].(map[string]any)
	if config["model"] != defaultSpladeModel || config["include_tokens"] != false || config["api_key_env_var"] != "CHROMA_API_KEY" {
		t.Fatalf("splade schema function config = %#v", config)
	}

	// The default (no embedder, API key set) also declares the entry.
	vs, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithAPIKey("key"), WithCollection("col"), WithIndexDimension(3), WithSparseSearch())
	if err != nil {
		t.Fatal(err)
	}
	_ = vs.Close()
	if got.SparseVectorIndexFunction["name"] != "chroma-cloud-splade" {
		t.Fatalf("default splade schema function = %#v", got.SparseVectorIndexFunction)
	}

	vs, err = New(context.Background(), WithBaseURL("http://127.0.0.1:8000"), WithCollection("col"), WithIndexDimension(3))
	if err != nil {
		t.Fatal(err)
	}
	_ = vs.Close()
	if got.SparseVectorIndexKey != "" {
		t.Fatalf("sparse key without hybrid = %q", got.SparseVectorIndexKey)
	}

}
