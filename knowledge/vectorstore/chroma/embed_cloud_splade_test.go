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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNewCloudSpladeEmbedder(t *testing.T) {
	if _, err := NewCloudSpladeEmbedder(""); err == nil {
		t.Fatal("empty API key should fail")
	}
	e, err := NewCloudSpladeEmbedder(" key ")
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	if e.apiKey != "key" {
		t.Fatalf("apiKey = %q, want trimmed key", e.apiKey)
	}
	if e.baseURL != defaultSpladeBaseURL || e.model != defaultSpladeModel {
		t.Fatalf("defaults = %q %q", e.baseURL, e.model)
	}
	e, err = NewCloudSpladeEmbedder("key", WithSpladeBaseURL("http://localhost:1/"), WithSpladeModel(" m "))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	if e.baseURL != "http://localhost:1" || e.model != "m" {
		t.Fatalf("options = %q %q", e.baseURL, e.model)
	}
	if _, err := NewCloudSpladeEmbedder("key", WithSpladeBaseURL(" ")); err == nil {
		t.Fatal("blank base URL should fail")
	}
}

// newSpladeServer returns a test server that mimics the embed_sparse API and
// records the last request.
func newSpladeServer(t *testing.T, handler http.HandlerFunc) *CloudSpladeEmbedder {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	embedder, err := NewCloudSpladeEmbedder("test-key", WithSpladeBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	return embedder
}

func TestCloudSpladeEmbed(t *testing.T) {
	var gotPath, gotToken, gotModel string
	var gotBody spladeRequest
	embedder := newSpladeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("x-chroma-token")
		gotModel = r.Header.Get("x-chroma-embedding-model")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		// Deliberately unsorted indices with one duplicate.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []map[string]any{
				{"indices": []int{900, 100, 100, 500}, "values": []float64{2, 1, 3, 4}},
			},
			"num_tokens": 3,
		})
	})
	vec, err := embedder.EmbedDocument(context.Background(), "hello database")
	if err != nil {
		t.Fatalf("EmbedDocument: %v", err)
	}
	if gotPath != "/embed_sparse" || gotToken != "test-key" || gotModel != defaultSpladeModel {
		t.Fatalf("request = %s %q %q", gotPath, gotToken, gotModel)
	}
	if len(gotBody.Texts) != 1 || gotBody.Texts[0] != "hello database" || gotBody.FetchTokens != "false" {
		t.Fatalf("request body = %#v", gotBody)
	}
	// Duplicate 100 merges to 1+3=4; output sorted ascending.
	wantIndices := []int{100, 500, 900}
	wantValues := []float64{4, 4, 2}
	if len(vec.Indices) != len(wantIndices) {
		t.Fatalf("indices = %#v", vec.Indices)
	}
	for i := range wantIndices {
		if vec.Indices[i] != wantIndices[i] || vec.Values[i] != wantValues[i] {
			t.Fatalf("vector = %#v %#v, want %#v %#v", vec.Indices, vec.Values, wantIndices, wantValues)
		}
	}
	// The wire helper accepts the vector.
	if _, err := sparseVectorValue(vec); err != nil {
		t.Fatalf("sparseVectorValue: %v", err)
	}

	// Queries share the document pipeline.
	queryVec, err := embedder.EmbedQuery(context.Background(), "database")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(queryVec.Indices) != len(vec.Indices) {
		t.Fatalf("query vector = %#v, want same as document vector", queryVec)
	}
}

func TestCloudSpladeEmbedErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "bad request", status: 400, body: `{"error":"Sparse embeddings not supported"}`, wantErr: "400"},
		{name: "unauthorized", status: 401, body: `{"error":"unauthorized"}`, wantErr: "401"},
		{name: "rate limited", status: 429, body: `{"error":"too many"}`, wantErr: "429"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder := newSpladeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := embedder.EmbedQuery(context.Background(), "text")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("wrong embedding count", func(t *testing.T) {
		embedder := newSpladeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": []any{}, "num_tokens": 0})
		})
		if _, err := embedder.EmbedQuery(context.Background(), "text"); err == nil {
			t.Fatal("empty embeddings should fail")
		}
	})

	t.Run("index value mismatch", func(t *testing.T) {
		embedder := newSpladeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"embeddings": []map[string]any{{"indices": []int{1, 2}, "values": []float64{1}}},
			})
		})
		if _, err := embedder.EmbedQuery(context.Background(), "text"); err == nil {
			t.Fatal("mismatched lengths should fail")
		}
	})

	t.Run("invalid index", func(t *testing.T) {
		embedder := newSpladeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"embeddings": []map[string]any{{"indices": []int{-1}, "values": []float64{1}}},
			})
		})
		if _, err := embedder.EmbedQuery(context.Background(), "text"); err == nil {
			t.Fatal("negative index should fail")
		}
	})
}

func TestCloudSpladeCustomModel(t *testing.T) {
	var gotModel string
	embedder := newSpladeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotModel = r.Header.Get("x-chroma-embedding-model")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []map[string]any{{"indices": []int{1}, "values": []float64{1}}},
		})
	})
	custom, err := NewCloudSpladeEmbedder("key", WithSpladeModel("naver/efficient-splade-VI-BT-large-query"))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	embedder.model = custom.model
	if _, err := embedder.EmbedQuery(context.Background(), "text"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if gotModel != "naver/efficient-splade-VI-BT-large-query" {
		t.Fatalf("model header = %q", gotModel)
	}
	// The schema declaration reflects the custom model.
	name, config := custom.functionDeclaration()
	if name != "chroma-cloud-splade" || config["model"] != "naver/efficient-splade-VI-BT-large-query" {
		t.Fatalf("function declaration = %q %#v", name, config)
	}
}

func TestSpladeVectorBoundaries(t *testing.T) {
	vec, err := spladeVector([]int{0, 1 << 30}, []float64{1, 2})
	if err != nil {
		t.Fatalf("spladeVector: %v", err)
	}
	if vec.Indices[0] != 0 || vec.Indices[1] != 1<<30 {
		t.Fatalf("indices = %#v", vec.Indices)
	}
	if _, err := spladeVector([]int{1 << 31}, []float64{1}); err == nil {
		t.Fatal("index beyond int32 should fail")
	}
	if _, err := spladeVector([]int{1}, []float64{}); err == nil {
		t.Fatal("length mismatch should fail")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWithSpladeHTTPClient(t *testing.T) {
	// A nil client is ignored and the default is kept.
	e, err := NewCloudSpladeEmbedder("key", WithSpladeHTTPClient(nil))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	if e.httpClient == nil {
		t.Fatal("default HTTP client must be set")
	}

	var calls int
	custom := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"embeddings":[{"indices":[1],"values":[1.0]}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	e, err = NewCloudSpladeEmbedder("key",
		WithSpladeBaseURL("http://splade.test"), WithSpladeHTTPClient(custom))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	if _, err := e.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if calls != 1 {
		t.Fatalf("custom client calls = %d, want 1", calls)
	}
}

func TestSpladeCrossOriginRedirectStripsToken(t *testing.T) {
	var targetToken string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetToken = r.Header.Get(spladeTokenHeader)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []map[string]any{{"indices": []int{1}, "values": []float64{1}}},
		})
	}))
	defer target.Close()

	var originToken string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originToken = r.Header.Get(spladeTokenHeader)
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	embedder, err := NewCloudSpladeEmbedder("test-key", WithSpladeBaseURL(origin.URL))
	if err != nil {
		t.Fatalf("NewCloudSpladeEmbedder: %v", err)
	}
	if _, err := embedder.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if originToken != "test-key" {
		t.Fatalf("origin token header = %q, want %q", originToken, "test-key")
	}
	if targetToken != "" {
		t.Fatalf("redirect target received token %q, want stripped", targetToken)
	}
}

func TestCloudSpladeEmbedderConcurrentUse(t *testing.T) {
	embedder := newSpladeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []map[string]any{{"indices": []int{1, 2}, "values": []float64{0.5, 1.5}}},
		})
	})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := embedder.EmbedDocument(ctx, "doc"); err != nil {
				t.Errorf("EmbedDocument: %v", err)
			}
			if _, err := embedder.EmbedQuery(ctx, "query"); err != nil {
				t.Errorf("EmbedQuery: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestWithSparseSearchDefaultsToCloudSplade(t *testing.T) {
	o := testOptions(WithAPIKey("key"), WithSparseSearch())
	if err := validateOptions(&o); err != nil {
		t.Fatalf("validateOptions: %v", err)
	}
	if _, ok := o.sparseEmbedder.(*CloudSpladeEmbedder); !ok {
		t.Fatalf("default embedder = %T, want *CloudSpladeEmbedder", o.sparseEmbedder)
	}

	// Without an API key the default is rejected with guidance.
	o = testOptions(WithSparseSearch())
	err := validateOptions(&o)
	if err == nil || !strings.Contains(err.Error(), "WithAPIKey") {
		t.Fatalf("err = %v, want guidance about WithAPIKey", err)
	}

	// The first non-nil embedder wins over the default.
	o = testOptions(WithAPIKey("key"), WithSparseSearch(nil, stubSparseEmbedder{}, nil))
	if err := validateOptions(&o); err != nil {
		t.Fatalf("validateOptions: %v", err)
	}
	if _, isSplade := o.sparseEmbedder.(*CloudSpladeEmbedder); isSplade {
		t.Fatal("the first non-nil embedder should win over the default")
	}
}
