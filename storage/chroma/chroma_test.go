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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type fakeStorageClient struct{}

func (fakeStorageClient) Heartbeat(context.Context) error { return nil }
func (fakeStorageClient) GetOrCreateCollection(context.Context, string, map[string]any) error {
	return nil
}
func (fakeStorageClient) GetCollection(context.Context, string) error { return nil }
func (fakeStorageClient) DeleteCollection(context.Context, string) error {
	return nil
}
func (fakeStorageClient) Add(context.Context, RecordBatch) error { return nil }
func (fakeStorageClient) Get(context.Context, GetParams) (*GetResult, error) {
	return &GetResult{}, nil
}
func (fakeStorageClient) Update(context.Context, RecordBatch) error { return nil }
func (fakeStorageClient) Upsert(context.Context, RecordBatch) error { return nil }
func (fakeStorageClient) Delete(context.Context, DeleteParams) error {
	return nil
}
func (fakeStorageClient) Query(context.Context, QueryParams) (*QueryResult, error) {
	return &QueryResult{}, nil
}
func (fakeStorageClient) Search(context.Context, SearchParams) (*SearchResult, error) {
	return nil, ErrSearchNotImplemented
}
func (fakeStorageClient) Count(context.Context) (int, error) { return 0, nil }
func (fakeStorageClient) CollectionInfo() CollectionInfo     { return CollectionInfo{} }
func (fakeStorageClient) MaxBatchSize(context.Context) (int, error) {
	return 1000, nil
}
func (fakeStorageClient) Close() error { return nil }

var _ ClientInterface = fakeStorageClient{}

type closeTrackingHTTPClient struct {
	closes int
}

func (*closeTrackingHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}

func (c *closeTrackingHTTPClient) CloseIdleConnections() {
	c.closes++
}

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...ClientBuilderOpt) *httpClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	opts = append([]ClientBuilderOpt{WithBaseURL(srv.URL)}, opts...)
	cli, err := defaultClientBuilder(opts...)
	if err != nil {
		t.Fatalf("defaultClientBuilder() error = %v", err)
	}
	return cli.(*httpClient)
}

func boundTestClient(t *testing.T, h http.HandlerFunc) *httpClient {
	t.Helper()
	c := newTestClient(t, h)
	c.collectionID = "col-1"
	return c
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func assertRequest(t *testing.T, r *http.Request, method, path string) {
	t.Helper()
	if r.Method != method || r.URL.EscapedPath() != path {
		t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.EscapedPath(), method, path)
	}
}

func TestDefaultClientBuilder(t *testing.T) {
	if _, err := defaultClientBuilder(); !errors.Is(err, ErrMissingBaseURL) {
		t.Fatalf("missing BaseURL error = %v, want ErrMissingBaseURL", err)
	}
	if _, err := defaultClientBuilder(WithBaseURL("localhost:8000")); err == nil {
		t.Fatal("BaseURL without scheme: want error")
	}

	custom := &http.Client{}
	cli, err := defaultClientBuilder(
		WithBaseURL("http://127.0.0.1:8000/"),
		WithTenant("tenant"),
		WithDatabase("database"),
		WithAPIKey("token"),
		WithHeaders(map[string]string{"X-Test": "yes"}),
		WithHTTPClient(custom),
		WithExtraOptions("extension"),
		WithSparseVectorIndex(" sparse_embedding "),
	)
	if err != nil {
		t.Fatalf("defaultClientBuilder() error = %v", err)
	}
	c := cli.(*httpClient)
	if c.baseURL != "http://127.0.0.1:8000/api/v2" ||
		c.tenant != "tenant" || c.database != "database" {
		t.Fatalf("client config = %#v", c)
	}
	if c.sparseVectorIndexKey != "sparse_embedding" {
		t.Fatalf("sparse vector index key = %q", c.sparseVectorIndexKey)
	}
	if c.hc != custom || c.headers[authTokenHeader] != "token" || c.headers["X-Test"] != "yes" {
		t.Fatalf("client dependencies/headers = %#v, %v", c.hc, c.headers)
	}
	if c.ownedTransport != nil {
		t.Fatal("injected HTTP client must not create an owned transport")
	}

	owned, err := defaultClientBuilder(WithBaseURL("http://127.0.0.1:8000"))
	if err != nil {
		t.Fatalf("default owned client error = %v", err)
	}
	ownedClient := owned.(*httpClient)
	if ownedClient.ownedTransport == nil {
		t.Fatal("default HTTP client must use an owned transport")
	}

	defaults, err := defaultClientBuilder(WithBaseURL("polaris://chroma-service"))
	if err != nil {
		t.Fatalf("service-discovery BaseURL error = %v", err)
	}
	d := defaults.(*httpClient)
	if d.baseURL != "polaris://chroma-service/api/v2" ||
		d.tenant != defaultTenant || d.database != defaultDatabase {
		t.Fatalf("default config = %#v", d)
	}

	inferred, err := defaultClientBuilder(
		WithBaseURL("http://127.0.0.1:8000"),
		WithAPIKey("token"),
	)
	if err != nil {
		t.Fatalf("API key client error = %v", err)
	}
	ic := inferred.(*httpClient)
	if !ic.inferScope || ic.tenant != "" || ic.database != "" {
		t.Fatalf("API key client should defer tenant inference: %#v", ic)
	}
}

func TestIdentityScopeInference(t *testing.T) {
	t.Run("infers unique tenant and database", func(t *testing.T) {
		var identityCalls, collectionCalls int
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/identity":
				identityCalls++
				assertRequest(t, r, http.MethodGet, r.URL.EscapedPath())
				if r.Header.Get(authTokenHeader) != "token" {
					t.Fatalf("identity auth = %q", r.Header.Get(authTokenHeader))
				}
				writeJSON(t, w, identityResponse{
					Tenant:    "cloud-tenant",
					Databases: []string{"*", "cloud-db"},
				})
			case "/api/v2/tenants/cloud-tenant/databases/cloud-db/collections":
				collectionCalls++
				assertRequest(t, r, http.MethodPost, r.URL.EscapedPath())
				writeJSON(t, w, collectionModel{ID: "col-1", Name: "docs"})
			default:
				http.Error(w, "unexpected "+r.URL.Path, http.StatusInternalServerError)
			}
		}, WithAPIKey("token"))
		if err := c.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		if err := c.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("second GetOrCreateCollection() error = %v", err)
		}
		if identityCalls != 1 || collectionCalls != 2 {
			t.Fatalf("identity/collection calls = %d/%d", identityCalls, collectionCalls)
		}
		if c.tenant != "cloud-tenant" || c.database != "cloud-db" {
			t.Fatalf("resolved scope = %s/%s", c.tenant, c.database)
		}
	})

	t.Run("keeps explicit tenant and infers database", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/identity":
				writeJSON(t, w, identityResponse{
					Tenant:    "other-tenant",
					Databases: []string{"cloud-db"},
				})
			case "/api/v2/tenants/explicit/databases/cloud-db/collections/docs":
				writeJSON(t, w, collectionModel{ID: "col-1", Name: "docs"})
			default:
				http.Error(w, "unexpected "+r.URL.Path, http.StatusInternalServerError)
			}
		}, WithAPIKey("token"), WithTenant("explicit"))
		if err := c.GetCollection(context.Background(), "docs"); err != nil {
			t.Fatalf("GetCollection() error = %v", err)
		}
		if c.tenant != "explicit" || c.database != "cloud-db" {
			t.Fatalf("resolved scope = %s/%s", c.tenant, c.database)
		}
	})

	t.Run("rejects missing unique database", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, identityResponse{Tenant: "cloud-tenant", Databases: []string{"a", "b"}})
		}, WithAPIKey("token"))
		err := c.GetOrCreateCollection(context.Background(), "docs", nil)
		if err == nil || !strings.Contains(err.Error(), "multiple databases") {
			t.Fatalf("error = %v, want multiple databases", err)
		}
	})
}

func TestBuilderAndRegistryInjection(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer SetClientBuilder(oldBuilder)

	called := false
	SetClientBuilder(func(opts ...ClientBuilderOpt) (ClientInterface, error) {
		called = true
		cfg := &ClientBuilderOpts{}
		for _, opt := range opts {
			opt(cfg)
		}
		if cfg.BaseURL != "http://registered" || cfg.Tenant != "tenant" ||
			!reflect.DeepEqual(cfg.ExtraOptions, []any{"extension"}) {
			t.Fatalf("builder options = %#v", cfg)
		}
		return fakeStorageClient{}, nil
	})

	oldRegistry := registry
	registry = map[string][]ClientBuilderOpt{}
	defer func() { registry = oldRegistry }()

	opts := []ClientBuilderOpt{
		WithBaseURL("http://registered"),
		WithTenant("tenant"),
		WithExtraOptions("extension"),
	}
	RegisterChromaInstance("primary", opts...)
	opts[0] = WithBaseURL("http://mutated")
	got, ok := GetChromaInstance("primary")
	if !ok {
		t.Fatal("registered instance not found")
	}
	if _, err := GetClientBuilder()(got...); err != nil || !called {
		t.Fatalf("injected builder: called=%v err=%v", called, err)
	}

	got[0] = WithBaseURL("http://changed")
	again, _ := GetChromaInstance("primary")
	cfg := &ClientBuilderOpts{}
	for _, opt := range again {
		opt(cfg)
	}
	if cfg.BaseURL != "http://registered" {
		t.Fatalf("registry returned shared option slice: %#v", cfg)
	}
	headers := map[string]string{"X-Test": "original"}
	RegisterChromaInstance("headers", WithHeaders(headers))
	headers["X-Test"] = "mutated"
	headerOpts, _ := GetChromaInstance("headers")
	headerCfg := &ClientBuilderOpts{}
	headerOpts[0](headerCfg)
	if headerCfg.Headers["X-Test"] != "original" {
		t.Fatalf("registered headers were aliased: %#v", headerCfg.Headers)
	}
	if _, ok := GetChromaInstance("missing"); ok {
		t.Fatal("missing registry entry found")
	}

	// A nil builder is ignored.
	SetClientBuilder(nil)
	if _, err := GetClientBuilder()(again...); err != nil {
		t.Fatalf("nil builder replaced current builder: %v", err)
	}
}

func TestChromaInstanceRegistry(t *testing.T) {
	const (
		first  = "chroma-registry-first"
		second = "chroma-registry-second"
	)
	defer func() {
		UnregisterChromaInstance(first)
		UnregisterChromaInstance(second)
	}()

	UnregisterChromaInstance(first)

	RegisterChromaInstance(first, WithBaseURL("http://first"))
	RegisterChromaInstance(second, WithBaseURL("http://second"))
	names := ListChromaInstances()
	if !containsName(names, first) || !containsName(names, second) {
		t.Fatalf("ListChromaInstances() = %v, want %q and %q", names, first, second)
	}

	got, ok := GetChromaInstance(first)
	if !ok {
		t.Fatalf("GetChromaInstance(%q) missing", first)
	}
	cfg := &ClientBuilderOpts{}
	for _, opt := range got {
		opt(cfg)
	}
	if cfg.BaseURL != "http://first" {
		t.Fatalf("first instance BaseURL = %q", cfg.BaseURL)
	}

	UnregisterChromaInstance(first)
	if _, ok := GetChromaInstance(first); ok {
		t.Fatal("unregistered instance still present")
	}
	names = ListChromaInstances()
	if containsName(names, first) || !containsName(names, second) {
		t.Fatalf("ListChromaInstances() after unregister = %v", names)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestUnboundOperations(t *testing.T) {
	ctx := context.Background()
	c := &httpClient{}
	ops := map[string]func() error{
		"add":    func() error { return c.Add(ctx, RecordBatch{IDs: []string{"1"}}) },
		"upsert": func() error { return c.Upsert(ctx, RecordBatch{IDs: []string{"1"}}) },
		"update": func() error { return c.Update(ctx, RecordBatch{IDs: []string{"1"}}) },
		"get":    func() error { _, err := c.Get(ctx, GetParams{}); return err },
		"delete": func() error { return c.Delete(ctx, DeleteParams{IDs: []string{"1"}}) },
		"query": func() error {
			_, err := c.Query(ctx, QueryParams{QueryEmbeddings: [][]float32{{1}}})
			return err
		},
		"count": func() error { _, err := c.Count(ctx); return err },
		"search": func() error {
			_, err := c.Search(ctx, SearchParams{Rank: map[string]any{"$knn": map[string]any{}}})
			return err
		},
	}
	for name, run := range ops {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, ErrCollectionNotBound) {
				t.Fatalf("error = %v, want ErrCollectionNotBound", err)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	t.Run("builds rank-expression payload and decodes records", func(t *testing.T) {
		score := float32(-0.25)
		document := "vector database"
		c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPost,
				"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/search")
			body := decodeBody(t, r)
			searches, ok := body["searches"].([]any)
			if !ok || len(searches) != 1 {
				t.Fatalf("searches = %#v", body["searches"])
			}
			search, _ := searches[0].(map[string]any)
			filter, ok := search["filter"].(map[string]any)
			if !ok {
				t.Fatalf("filter = %#v", body["filter"])
			}
			combined, ok := filter["$and"].([]any)
			if !ok || len(combined) != 2 {
				t.Fatalf("id filter = %#v", filter)
			}
			idFilter, _ := combined[0].(map[string]any)
			idValues, _ := idFilter["#id"].(map[string]any)
			if !reflect.DeepEqual(idValues["$in"], []any{"a", "b"}) {
				t.Fatalf("id filter = %#v", filter)
			}
			if !reflect.DeepEqual(combined[1], map[string]any{"category": "guide"}) {
				t.Fatalf("filter = %#v", filter)
			}
			rank, ok := search["rank"].(map[string]any)
			if !ok {
				t.Fatalf("rank = %#v", body["rank"])
			}
			if _, ok := rank["$knn"]; !ok {
				t.Fatalf("rank = %#v", rank)
			}
			limit, ok := search["limit"].(map[string]any)
			if !ok || limit["limit"] != float64(10) || limit["offset"] != float64(0) {
				t.Fatalf("limit = %#v", body["limit"])
			}
			sel, ok := search["select"].(map[string]any)
			if !ok || !reflect.DeepEqual(sel["keys"], []any{"#document", "#score"}) {
				t.Fatalf("select = %#v", body["select"])
			}
			writeJSON(t, w, SearchResult{
				IDs:       [][]string{{"doc-1"}},
				Documents: [][]*string{{&document}},
				Metadatas: [][]map[string]any{{{"category": "guide"}}},
				Scores:    [][]*float32{{&score}},
				Select:    [][]string{{"#document", "#score"}},
			})
		})
		res, err := c.Search(context.Background(), SearchParams{
			IDs:    []string{"a", "b"},
			Filter: map[string]any{"category": "guide"},
			Rank:   map[string]any{"$knn": map[string]any{"query": []float32{0.1}}},
			Limit:  10,
			Select: []string{"#document", "#score"},
		})
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if res.IDs[0][0] != "doc-1" || res.Documents[0][0] == nil ||
			*res.Documents[0][0] != document || res.Scores[0][0] == nil ||
			*res.Scores[0][0] != score {
			t.Fatalf("result = %#v", res)
		}
	})

	t.Run("requires rank and recognizes unsupported endpoints", func(t *testing.T) {
		c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Search operation is not implemented for local executor", http.StatusNotImplemented)
		})
		if _, err := c.Search(context.Background(), SearchParams{}); err == nil {
			t.Fatal("missing rank: want error")
		}
		_, err := c.Search(context.Background(), SearchParams{Rank: map[string]any{"$knn": map[string]any{}}})
		if err == nil || !IsNotImplemented(err) {
			t.Fatalf("Search() error = %v, want not implemented", err)
		}

		c = boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]string{"detail": "Not Found"})
		})
		_, err = c.Search(context.Background(), SearchParams{Rank: map[string]any{"$knn": map[string]any{}}})
		if err == nil || !IsNotImplemented(err) {
			t.Fatalf("Search() route 404 error = %v, want not implemented", err)
		}

		c = boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "collection missing", http.StatusNotFound)
		})
		_, err = c.Search(context.Background(), SearchParams{Rank: map[string]any{"$knn": map[string]any{}}})
		if err == nil || IsNotImplemented(err) {
			t.Fatalf("Search() resource 404 error = %v, want a non-fallback error", err)
		}
	})

	t.Run("rejects unexpected payload result count", func(t *testing.T) {
		c := boundTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, SearchResult{IDs: [][]string{{"a"}, {"b"}}})
		})
		_, err := c.Search(context.Background(), SearchParams{
			Rank: map[string]any{"$knn": map[string]any{"query": []float32{1}}},
		})
		if err == nil || !strings.Contains(err.Error(), "expected one payload result, got 2") {
			t.Fatalf("Search() error = %v", err)
		}
	})
}

func TestIsNotImplementedRequiresSearchPath(t *testing.T) {
	get404 := &statusError{
		Path:   "/tenants/t/databases/d/collections/c/get",
		Status: http.StatusNotFound,
		Body:   `{"detail":"Not Found"}`,
	}
	if IsNotImplemented(get404) {
		t.Fatal("Get 404 must not be treated as unimplemented search")
	}
	search404 := &statusError{
		Path:   "/tenants/t/databases/d/collections/c/search",
		Status: http.StatusNotFound,
		Body:   `{"detail": "Not Found"}`,
	}
	if !IsNotImplemented(search404) {
		t.Fatal("Search route 404 should be unimplemented")
	}
	gateway404 := &statusError{
		Path:   "/tenants/t/databases/d/collections/c/search",
		Status: http.StatusNotFound,
		Body:   "404 page not found",
	}
	if !IsNotImplemented(gateway404) {
		t.Fatal("gateway Search route 404 should be unimplemented")
	}
	resource404 := &statusError{
		Path:   "/tenants/t/databases/d/collections/c/search",
		Status: http.StatusNotFound,
		Body:   "collection missing",
	}
	if IsNotImplemented(resource404) {
		t.Fatal("collection 404 must not be treated as an unimplemented search route")
	}
}

func TestHeartbeatAndClose(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodGet, "/api/v2/heartbeat")
			writeJSON(t, w, map[string]any{"nanosecond heartbeat": 1})
		})
		if err := c.Heartbeat(context.Background()); err != nil {
			t.Fatalf("Heartbeat() error = %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	t.Run("injected client remains caller-owned", func(t *testing.T) {
		custom := &closeTrackingHTTPClient{}
		cli, err := defaultClientBuilder(
			WithBaseURL("http://127.0.0.1:8000"),
			WithHTTPClient(custom),
		)
		if err != nil {
			t.Fatalf("defaultClientBuilder() error = %v", err)
		}
		if err := cli.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if custom.closes != 0 {
			t.Fatalf("injected HTTP client close calls = %d, want 0", custom.closes)
		}
	})

	t.Run("server error", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		})
		if err := c.Heartbeat(context.Background()); err == nil {
			t.Fatal("Heartbeat() error = nil, want server error")
		}
	})
}

func TestCollectionModelInfo(t *testing.T) {
	dimension := 3
	schema := sparseVectorCollectionSchema("lexical", nil)
	disabledSchema := sparseVectorCollectionSchema("disabled", nil)
	disabledKeys := disabledSchema["keys"].(map[string]any)
	disabledConfig := disabledKeys["disabled"].(map[string]any)
	disabledVector := disabledConfig["sparse_vector"].(map[string]any)
	disabledIndex := disabledVector["sparse_vector_index"].(map[string]any)
	disabledIndex["enabled"] = false
	tests := []struct {
		name string
		m    collectionModel
		want CollectionInfo
	}{
		{
			name: "hnsw configuration",
			m: collectionModel{
				ID: "id", Name: "n", Dimension: &dimension,
				ConfigurationJSON: collectionConfiguration{HNSW: &indexConfiguration{Space: cosineSpace}},
			},
			want: CollectionInfo{ID: "id", Name: "n", Dimension: 3, Metric: cosineSpace},
		},
		{
			name: "spann configuration",
			m: collectionModel{
				ID:                "id",
				ConfigurationJSON: collectionConfiguration{SPANN: &indexConfiguration{Space: cosineSpace}},
			},
			want: CollectionInfo{ID: "id", Metric: cosineSpace},
		},
		{
			name: "matching hnsw and spann configurations",
			m: collectionModel{
				ID: "id",
				ConfigurationJSON: collectionConfiguration{
					HNSW:  &indexConfiguration{Space: cosineSpace},
					SPANN: &indexConfiguration{Space: cosineSpace},
				},
			},
			want: CollectionInfo{ID: "id", Metric: cosineSpace},
		},
		{
			name: "conflicting hnsw and spann configurations",
			m: collectionModel{
				ID: "id",
				ConfigurationJSON: collectionConfiguration{
					HNSW:  &indexConfiguration{Space: cosineSpace},
					SPANN: &indexConfiguration{Space: "l2"},
				},
			},
			want: CollectionInfo{ID: "id"},
		},
		{
			name: "missing configuration",
			m:    collectionModel{ID: "id"},
			want: CollectionInfo{ID: "id"},
		},
		{
			name: "sparse schema",
			m: collectionModel{
				ID:     "id",
				Schema: &schema,
			},
			want: CollectionInfo{
				ID:                    "id",
				SchemaKnown:           true,
				SparseVectorIndexKeys: []string{"lexical"},
			},
		},
		{
			name: "disabled sparse index",
			m: collectionModel{
				ID:     "id",
				Schema: &disabledSchema,
			},
			want: CollectionInfo{
				ID:          "id",
				SchemaKnown: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.info(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("info() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCollectionBinding(t *testing.T) {
	t.Run("rejects empty collection names", func(t *testing.T) {
		c := newTestClient(t, func(http.ResponseWriter, *http.Request) {
			t.Fatal("empty collection name must fail before sending a request")
		})
		for name, call := range map[string]func() error{
			"get or create": func() error {
				return c.GetOrCreateCollection(context.Background(), "", nil)
			},
			"get": func() error {
				return c.GetCollection(context.Background(), "")
			},
		} {
			t.Run(name, func(t *testing.T) {
				err := call()
				if err == nil || !strings.Contains(err.Error(), "collection name is required") {
					t.Fatalf("error = %v, want collection name required", err)
				}
			})
		}
	})

	t.Run("get or create", func(t *testing.T) {
		var body map[string]any
		dimension := 3
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPost,
				"/api/v2/tenants/my%20tenant/databases/my%2Fdb/collections")
			body = decodeBody(t, r)
			writeJSON(t, w, collectionModel{
				ID:        "col-1",
				Name:      "docs",
				Dimension: &dimension,
				ConfigurationJSON: collectionConfiguration{
					HNSW: &indexConfiguration{Space: "l2"},
				},
			})
		}, WithTenant("my tenant"), WithDatabase("my/db"))

		metadata := map[string]any{"owner": "team"}
		if err := c.GetOrCreateCollection(context.Background(), "docs", metadata); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		if c.collectionID != "col-1" || body["name"] != "docs" || body["get_or_create"] != true {
			t.Fatalf("binding/body = %q, %v", c.collectionID, body)
		}
		if info := c.CollectionInfo(); info.Metric != "l2" || info.Dimension != 3 {
			t.Fatalf("collection info = %#v", info)
		}
		if got := body["metadata"]; !reflect.DeepEqual(got, metadata) {
			t.Fatalf("metadata = %#v, want %#v", got, metadata)
		}
		configuration := body["configuration"].(map[string]any)
		hnsw := configuration["hnsw"].(map[string]any)
		if got := hnsw["space"]; got != cosineSpace {
			t.Fatalf("configuration = %#v, want cosine HNSW", configuration)
		}
	})

	t.Run("default configuration", func(t *testing.T) {
		var body map[string]any
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			body = decodeBody(t, r)
			writeJSON(t, w, collectionModel{ID: "col-2"})
		})
		if err := c.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		if _, ok := body["metadata"]; ok {
			t.Fatalf("metadata = %#v, want omitted", body["metadata"])
		}
		if _, ok := body["schema"]; ok {
			t.Fatalf("schema = %#v, want omitted", body["schema"])
		}
		configuration := body["configuration"].(map[string]any)
		hnsw := configuration["hnsw"].(map[string]any)
		if got := hnsw["space"]; got != cosineSpace {
			t.Fatalf("configuration = %#v, want cosine HNSW", configuration)
		}
	})

	t.Run("sparse vector schema", func(t *testing.T) {
		var body map[string]any
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			body = decodeBody(t, r)
			writeJSON(t, w, collectionModel{ID: "col-sparse"})
		}, WithSparseVectorIndex("lexical"))
		if err := c.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		got, ok := body["schema"].(map[string]any)
		if !ok {
			t.Fatalf("schema = %#v, want sparse vector schema", body["schema"])
		}
		want := sparseVectorCollectionSchema("lexical", nil)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("schema = %#v, want %#v", got, want)
		}
		defaults, ok := got["defaults"].(map[string]any)
		if !ok || defaults["string"] == nil || defaults["float_list"] == nil ||
			defaults["sparse_vector"] == nil || defaults["int"] == nil ||
			defaults["float"] == nil || defaults["bool"] == nil {
			t.Fatalf("schema defaults are incomplete: %#v", defaults)
		}
		keys := got["keys"].(map[string]any)
		if keys["#document"] == nil || keys["#embedding"] == nil || keys["lexical"] == nil {
			t.Fatalf("schema keys are incomplete: %#v", keys)
		}
		sparseConfig := keys["lexical"].(map[string]any)["sparse_vector"].(map[string]any)["sparse_vector_index"].(map[string]any)["config"].(map[string]any)
		if _, ok := sparseConfig["source_key"]; ok {
			t.Fatalf("caller-embedded sparse config must not have source_key: %#v", sparseConfig)
		}
		if !reflect.DeepEqual(sparseConfig["embedding_function"], map[string]any{"type": "unknown"}) {
			t.Fatalf("sparse embedding function = %#v", sparseConfig["embedding_function"])
		}
		if _, ok := body["configuration"]; ok {
			t.Fatalf("configuration = %#v, want schema-only create request", body["configuration"])
		}

		// A declared registry function replaces the unknown declaration and
		// names #document as its source, matching the official clients.
		var declaredBody map[string]any
		fnClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			declaredBody = decodeBody(t, r)
			writeJSON(t, w, collectionModel{ID: "col-sparse-fn"})
		}, WithSparseVectorIndex("lexical"), WithSparseVectorIndexFunction("chroma-cloud-splade", map[string]any{
			"model":          "prithivida/Splade_PP_en_v1",
			"include_tokens": false,
		}))
		if err := fnClient.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		declaredKeys := declaredBody["schema"].(map[string]any)["keys"].(map[string]any)
		declaredConfig := declaredKeys["lexical"].(map[string]any)["sparse_vector"].(map[string]any)["sparse_vector_index"].(map[string]any)["config"].(map[string]any)
		wantFn := map[string]any{
			"embedding_function": map[string]any{
				"type":   "known",
				"name":   "chroma-cloud-splade",
				"config": map[string]any{"model": "prithivida/Splade_PP_en_v1", "include_tokens": false},
			},
			"source_key": "#document",
		}
		if !reflect.DeepEqual(declaredConfig, wantFn) {
			t.Fatalf("declared sparse config = %#v, want %#v", declaredConfig, wantFn)
		}

		// An empty function name is ignored and the schema declares unknown.
		var ignoredBody map[string]any
		ignoredClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			ignoredBody = decodeBody(t, r)
			writeJSON(t, w, collectionModel{ID: "col-sparse-ignored"})
		}, WithSparseVectorIndex("lexical"), WithSparseVectorIndexFunction(" ", nil))
		if err := ignoredClient.GetOrCreateCollection(context.Background(), "docs", nil); err != nil {
			t.Fatalf("GetOrCreateCollection() error = %v", err)
		}
		ignoredKeys := ignoredBody["schema"].(map[string]any)["keys"].(map[string]any)
		ignoredConfig := ignoredKeys["lexical"].(map[string]any)["sparse_vector"].(map[string]any)["sparse_vector_index"].(map[string]any)["config"].(map[string]any)
		if !reflect.DeepEqual(ignoredConfig["embedding_function"], map[string]any{"type": "unknown"}) {
			t.Fatalf("ignored function declared = %#v, want unknown", ignoredConfig["embedding_function"])
		}
		if _, ok := ignoredConfig["source_key"]; ok {
			t.Fatalf("undeclared sparse config must not have source_key: %#v", ignoredConfig)
		}
	})

	t.Run("get existing and not found", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/tenants/default_tenant/databases/default_database/collections/docs":
				assertRequest(t, r, http.MethodGet, r.URL.EscapedPath())
				writeJSON(t, w, collectionModel{ID: "col-3"})
			case "/api/v2/tenants/default_tenant/databases/default_database/collections/missing":
				http.Error(w, "missing", http.StatusNotFound)
			default:
				http.Error(w, "boom", http.StatusInternalServerError)
			}
		})
		if err := c.GetCollection(context.Background(), "docs"); err != nil || c.collectionID != "col-3" {
			t.Fatalf("GetCollection() binding = %q, %v", c.collectionID, err)
		}
		if err := c.GetCollection(context.Background(), "missing"); !errors.Is(err, ErrCollectionNotFound) {
			t.Fatalf("missing error = %v, want ErrCollectionNotFound", err)
		}
		err := c.GetCollection(context.Background(), "broken")
		if err == nil || errors.Is(err, ErrCollectionNotFound) {
			t.Fatalf("HTTP 500 error = %v", err)
		}
	})

	t.Run("delete collection clears binding", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodDelete,
				"/api/v2/tenants/default_tenant/databases/default_database/collections/docs")
			w.WriteHeader(http.StatusNoContent)
		})
		c.bind(collectionModel{ID: "col-1", Name: "docs"})
		if err := c.DeleteCollection(context.Background(), "docs"); err != nil {
			t.Fatal(err)
		}
		if c.boundCollectionID() != "" {
			t.Fatalf("binding not cleared: %#v", c.CollectionInfo())
		}
		if err := c.DeleteCollection(context.Background(), ""); err == nil {
			t.Fatal("empty collection name: want error")
		}
	})
}

func TestWriteRecordsWireFormat(t *testing.T) {
	rec := RecordBatch{
		IDs:        []string{"id1"},
		Documents:  []string{"hello"},
		Embeddings: [][]float32{{0.1, 0.2}},
		Metadatas:  []map[string]any{{"k": "v"}},
	}
	for _, tt := range []struct {
		name string
		call func(*httpClient) error
	}{
		{"add", func(c *httpClient) error { return c.Add(context.Background(), rec) }},
		{"upsert", func(c *httpClient) error { return c.Upsert(context.Background(), rec) }},
		{"update", func(c *httpClient) error { return c.Update(context.Background(), rec) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assertRequest(t, r, http.MethodPost,
					"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/"+tt.name)
				want := map[string]any{
					"ids": []any{"id1"}, "documents": []any{"hello"},
					"embeddings": []any{[]any{0.1, 0.2}},
					"metadatas":  []any{map[string]any{"k": "v"}},
				}
				if got := decodeBody(t, r); !reflect.DeepEqual(got, want) {
					t.Errorf("body = %#v, want %#v", got, want)
				}
				w.WriteHeader(http.StatusCreated)
			})
			if err := tt.call(c); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}
			if err := c.writeRecords(context.Background(), tt.name, RecordBatch{}); err == nil {
				t.Fatal("empty IDs: want error")
			}
		})
	}

	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rejected", http.StatusBadRequest)
	})
	if err := c.Add(context.Background(), RecordBatch{
		IDs: []string{"1"}, Embeddings: [][]float32{{1}},
	}); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("HTTP error = %v, want server message", err)
	}
	if err := c.Add(context.Background(), RecordBatch{IDs: []string{"1"}}); err == nil {
		t.Fatal("Add without embeddings: want validation error")
	}
}

func TestGetWireFormatAndWherePassthrough(t *testing.T) {
	where := map[string]any{"$and": []any{
		map[string]any{"category": map[string]any{"$eq": "guide"}},
		map[string]any{"score": map[string]any{"$gte": 1.5}},
	}}
	whereDocument := map[string]any{"$contains": "hello"}
	limit, offset := 10, 5

	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodPost,
			"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/get")
		want := map[string]any{
			"ids": []any{"id1"}, "where": where, "where_document": whereDocument,
			"include": []any{"documents", "metadatas"}, "limit": 10.0, "offset": 5.0,
		}
		if got := decodeBody(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %#v, want %#v", got, want)
		}
		writeJSON(t, w, GetResult{IDs: []string{"id1"}, Documents: []string{"hello"}})
	})
	got, err := c.Get(context.Background(), GetParams{
		IDs: []string{"id1"}, Where: where, WhereDocument: whereDocument,
		Include: []string{"documents", "metadatas"}, Limit: &limit, Offset: &offset,
	})
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"id1"}) ||
		!reflect.DeepEqual(got.Documents, []string{"hello"}) {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	if _, err := validIncludes([]string{"documents", "bogus"}); err == nil {
		t.Fatal("unknown include should fail")
	}
}

func TestDeleteWireFormat(t *testing.T) {
	where := map[string]any{"k": map[string]any{"$eq": "v"}}
	whereDocument := map[string]any{"$contains": "hello"}
	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodPost,
			"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/delete")
		want := map[string]any{
			"ids": []any{"id1"}, "where": where, "where_document": whereDocument,
		}
		if got := decodeBody(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %#v, want %#v", got, want)
		}
	})
	if err := c.Delete(context.Background(), DeleteParams{
		IDs: []string{"id1"}, Where: where, WhereDocument: whereDocument,
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestDeleteRequiresSelector(t *testing.T) {
	c := boundTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("empty delete must not reach the server")
	})
	if err := c.Delete(context.Background(), DeleteParams{}); !errors.Is(err, ErrDeleteSelector) {
		t.Fatalf("empty delete = %v, want ErrDeleteSelector", err)
	}
}

func TestGetEmptyInclude(t *testing.T) {
	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got := decodeBody(t, r)
		include, _ := got["include"].([]any)
		if len(include) != 0 {
			t.Fatalf("include = %#v, want empty array", include)
		}
		writeJSON(t, w, GetResult{IDs: []string{"id1"}})
	})
	got, err := c.Get(context.Background(), GetParams{Include: []string{}})
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"id1"}) {
		t.Fatalf("Get() empty include = %#v, %v", got, err)
	}
}

func TestQueryWireFormat(t *testing.T) {
	where := map[string]any{"kind": map[string]any{"$in": []any{"a", "b"}}}
	whereDocument := map[string]any{"$contains": "text"}
	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodPost,
			"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/query")
		want := map[string]any{
			"query_embeddings": []any{[]any{0.1, 0.2}}, "n_results": 3.0,
			"ids": []any{"id1"}, "where": where, "where_document": whereDocument,
			"include": []any{"documents", "distances"},
		}
		if got := decodeBody(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("body = %#v, want %#v", got, want)
		}
		writeJSON(t, w, QueryResult{
			IDs: [][]string{{"id1"}}, Distances: [][]float32{{0.25}},
		})
	})
	got, err := c.Query(context.Background(), QueryParams{
		QueryEmbeddings: [][]float32{{0.1, 0.2}}, NResults: 3, IDs: []string{"id1"},
		Where: where, WhereDocument: whereDocument, Include: []string{"documents", "distances"},
	})
	if err != nil || !reflect.DeepEqual(got.IDs, [][]string{{"id1"}}) ||
		!reflect.DeepEqual(got.Distances, [][]float32{{0.25}}) {
		t.Fatalf("Query() = %#v, %v", got, err)
	}
	if _, err := c.Query(context.Background(), QueryParams{}); err == nil {
		t.Fatal("Query() without embeddings: want error")
	}
	if _, err := validIncludes([]string{"data"}); err == nil {
		t.Fatal("unsupported data include should fail")
	}
}

func TestCountWireFormat(t *testing.T) {
	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodGet,
			"/api/v2/tenants/default_tenant/databases/default_database/collections/col-1/count")
		fmt.Fprint(w, "42")
	})
	if got, err := c.Count(context.Background()); err != nil || got != 42 {
		t.Fatalf("Count() = %d, %v, want 42", got, err)
	}
}

func TestOperationErrors(t *testing.T) {
	c := boundTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rejected", http.StatusBadRequest)
	})
	ctx := context.Background()
	ops := map[string]func() error{
		"get":    func() error { _, err := c.Get(ctx, GetParams{}); return err },
		"delete": func() error { return c.Delete(ctx, DeleteParams{IDs: []string{"id1"}}) },
		"query": func() error {
			_, err := c.Query(ctx, QueryParams{QueryEmbeddings: [][]float32{{0.1}}})
			return err
		},
		"count": func() error { _, err := c.Count(ctx); return err },
	}
	for name, run := range ops {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil || !strings.Contains(err.Error(), "rejected") {
				t.Fatalf("error = %v, want server message", err)
			}
		})
	}

	noID := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, collectionModel{Name: "docs"})
	})
	if err := noID.GetOrCreateCollection(ctx, "docs", nil); err == nil {
		t.Fatal("GetOrCreateCollection() without response ID: want error")
	}
}

// errOn4xxClient mimics an injected request handler that returns non-2xx
// responses as errors instead of exposing the HTTP response.
type errOn4xxClient struct {
	inner *http.Client
	calls int
}

func (c *errOn4xxClient) Do(req *http.Request) (*http.Response, error) {
	c.calls++
	resp, err := c.inner.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("unexpected HTTP status: %s (%s)", resp.Status, body)
}

func TestInjectedHTTPClient(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get(authTokenHeader)
		switch r.URL.Path {
		case "/api/v2/tenants/default_tenant/databases/default_database/collections/docs":
			writeJSON(t, w, collectionModel{ID: "col-1"})
		default:
			http.Error(w, "collection does not exist", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	custom := &errOn4xxClient{inner: &http.Client{}}
	cli, err := defaultClientBuilder(
		WithBaseURL(srv.URL),
		WithAPIKey("token"),
		WithTenant(defaultTenant),
		WithDatabase(defaultDatabase),
		WithHTTPClient(custom),
	)
	if err != nil {
		t.Fatalf("defaultClientBuilder() error = %v", err)
	}
	if err := cli.GetCollection(context.Background(), "docs"); err != nil {
		t.Fatalf("GetCollection() error = %v", err)
	}
	if auth != "token" || custom.calls != 1 {
		t.Fatalf("injected client calls/auth = %d/%q", custom.calls, auth)
	}
	if err := cli.GetCollection(context.Background(), "missing"); err == nil ||
		errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("custom HTTP client error = %v, want original error", err)
	}
}

func TestCrossOriginRedirectStripsHeaders(t *testing.T) {
	var targetAuth, targetCustom string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get(authTokenHeader)
		targetCustom = r.Header.Get("X-Test")
		writeJSON(t, w, collectionModel{ID: "col-1"})
	}))
	defer target.Close()

	var originAuth string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuth = r.Header.Get(authTokenHeader)
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	cli, err := defaultClientBuilder(
		WithBaseURL(origin.URL),
		WithAPIKey("token"),
		WithHeaders(map[string]string{"X-Test": "yes"}),
		WithTenant(defaultTenant),
		WithDatabase(defaultDatabase),
	)
	if err != nil {
		t.Fatalf("defaultClientBuilder() error = %v", err)
	}
	if err := cli.GetCollection(context.Background(), "docs"); err != nil {
		t.Fatalf("GetCollection() error = %v", err)
	}
	if originAuth != "token" {
		t.Fatalf("origin auth header = %q, want %q", originAuth, "token")
	}
	if targetAuth != "" || targetCustom != "" {
		t.Fatalf("redirect target received headers %q/%q, want both stripped", targetAuth, targetCustom)
	}
}

func TestHTTPProtocolEdges(t *testing.T) {
	t.Run("base URL rejects query and fragment", func(t *testing.T) {
		for _, raw := range []string{"http://localhost?token=x", "http://localhost/#fragment"} {
			if _, err := normalizeBaseURL(raw); err == nil {
				t.Fatalf("normalizeBaseURL(%q): want error", raw)
			}
		}
	})

	t.Run("auth token has deterministic precedence", func(t *testing.T) {
		c := newTestClient(t, func(http.ResponseWriter, *http.Request) {},
			WithHeaders(map[string]string{"x-chroma-token": "header"}),
			WithAPIKey("option"),
		)
		if got := c.headers[http.CanonicalHeaderKey(authTokenHeader)]; got != "option" {
			t.Fatalf("auth header = %q, want option", got)
		}
	})

	t.Run("pre-flight batch size", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodGet, "/api/v2/pre-flight-checks")
			writeJSON(t, w, map[string]any{"max_batch_size": 37})
		})
		got, err := c.MaxBatchSize(context.Background())
		if err != nil || got != 37 {
			t.Fatalf("MaxBatchSize() = %d, %v", got, err)
		}
	})

	t.Run("invalid pre-flight batch size", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, map[string]any{"max_batch_size": 0})
		})
		if _, err := c.MaxBatchSize(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "invalid max_batch_size") {
			t.Fatalf("MaxBatchSize() error = %v", err)
		}
	})

	t.Run("nullable query distance is marked invalid", func(t *testing.T) {
		c := boundTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"ids":[["a","b"]],"distances":[[null,0.25]],"documents":[[null,"b"]]}`)
		})
		got, err := c.Query(context.Background(), QueryParams{
			QueryEmbeddings: [][]float32{{1}},
			Include:         []string{"documents", "distances"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.DistanceValid[0][0] || !got.DistanceValid[0][1] || got.Documents[0][0] != "" {
			t.Fatalf("nullable query response = %#v", got)
		}
	})
}
