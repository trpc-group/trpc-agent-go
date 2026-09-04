//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chroma provides storage-layer infrastructure for a Chroma HTTP server.
//
// It talks to the Chroma v2 REST API directly over net/http and provides:
//
//   - ClientInterface, a collection-scoped seam used by the knowledge vector store.
//   - a ClientBuilder with HTTP URL, tenant/database, and auth options.
//   - a registry for referring to named Chroma instances from the knowledge
//     vector store.
//
// The payload types below mirror the Chroma wire format, so filters, metadata,
// and embeddings pass through to JSON without an intermediate object model.
//
// ClientInterface also provides a seam for injecting test clients.
package chroma

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"sync"
)

// ErrSearchNotImplemented reports that the bound Chroma server does not
// implement the /search rank-expression API. Callers may use errors.Is to
// decide whether falling back to a compatible search path is safe.
var ErrSearchNotImplemented = errors.New("chroma: search operation is not implemented")

// ClientInterface is the collection-scoped Chroma API used by the vector store.
//
// GetOrCreateCollection / GetCollection bind subsequent record operations to a
// collection. After binding, Add, Get, Update, Upsert, Delete, Query, Search,
// and Count run against that collection.
type ClientInterface interface {
	// Heartbeat checks whether the Chroma server is reachable.
	Heartbeat(ctx context.Context) error

	// GetOrCreateCollection binds the client to a collection, creating a cosine
	// HNSW collection with metadata when needed. Chroma Cloud may expose the
	// created collection as a cosine SPANN index. When the client was built
	// with WithSparseVectorIndex, a newly created collection also receives a
	// sparse vector index on that metadata field.
	GetOrCreateCollection(ctx context.Context, name string, metadata map[string]any) error
	// GetCollection binds the client to an existing collection.
	GetCollection(ctx context.Context, name string) error
	// DeleteCollection deletes a collection by name.
	DeleteCollection(ctx context.Context, name string) error

	// Add inserts records into the bound collection.
	Add(ctx context.Context, rec RecordBatch) error
	// Get retrieves records from the bound collection.
	Get(ctx context.Context, p GetParams) (*GetResult, error)
	// Update updates existing records in the bound collection.
	Update(ctx context.Context, rec RecordBatch) error
	// Upsert inserts or updates records in the bound collection.
	Upsert(ctx context.Context, rec RecordBatch) error
	// Delete deletes selected records from the bound collection.
	Delete(ctx context.Context, p DeleteParams) error

	// Query uses the classic dense-vector /query endpoint.
	Query(ctx context.Context, p QueryParams) (*QueryResult, error)

	// Search executes a rank-expression search against the bound collection.
	// Self-hosted servers that do not implement /search return
	// ErrSearchNotImplemented.
	Search(ctx context.Context, p SearchParams) (*SearchResult, error)

	// Count returns the number of records in the bound collection.
	Count(ctx context.Context) (int, error)

	// CollectionInfo returns the bound collection's server-reported configuration.
	CollectionInfo() CollectionInfo

	// MaxBatchSize returns the server-advertised maximum write batch size.
	MaxBatchSize(ctx context.Context) (int, error)

	// Close releases HTTP resources created by the implementation. It does not
	// close a client or transport supplied by the caller.
	Close() error
}

// RecordBatch is one Add/Update/Upsert payload. Slices are aligned by index.
type RecordBatch struct {
	// IDs contains the Chroma record IDs.
	IDs []string
	// Embeddings contains one vector per ID when the operation writes vectors.
	Embeddings [][]float32
	// Documents contains one document body per ID when supplied.
	Documents []string
	// Metadatas contains one metadata map per ID when supplied.
	Metadatas []map[string]any
}

// GetParams selects records for Get. Where filters metadata; WhereDocument
// filters document text. Limit and Offset are optional pagination.
type GetParams struct {
	// IDs limits the result to specific record IDs.
	IDs []string
	// Where is a Chroma metadata filter.
	Where map[string]any
	// WhereDocument is a Chroma document-content filter.
	WhereDocument map[string]any
	// Include selects response fields such as documents and embeddings.
	Include []string
	// Limit caps the returned records when non-nil.
	Limit *int
	// Offset skips records for paginated reads when non-nil.
	Offset *int
}

// GetResult is a Get response. Slices are aligned by index. The JSON tags
// match the Chroma wire format so responses decode without a conversion step.
type GetResult struct {
	// IDs contains the returned record IDs.
	IDs []string `json:"ids"`
	// Documents contains document bodies aligned with IDs.
	Documents []string `json:"documents"`
	// Embeddings contains vectors aligned with IDs.
	Embeddings [][]float32 `json:"embeddings"`
	// Metadatas contains metadata maps aligned with IDs.
	Metadatas []map[string]any `json:"metadatas"`
	// URIs contains source URIs aligned with IDs.
	URIs []string `json:"uris"`
}

// QueryParams is a vector Query request. QueryEmbeddings is required.
type QueryParams struct {
	// QueryEmbeddings contains the vectors to search for and is required.
	QueryEmbeddings [][]float32
	// NResults limits the number of results per query when positive.
	NResults int
	// IDs limits the search to specific record IDs.
	IDs []string
	// Where is a Chroma metadata filter.
	Where map[string]any
	// WhereDocument is a Chroma document-content filter.
	WhereDocument map[string]any
	// Include selects response fields such as documents and distances.
	Include []string
}

// QueryResult is a Query response. Nested slices follow Chroma's query-group
// layout: the outer slice is one group per query embedding. The JSON tags
// match the Chroma wire format so responses decode without a conversion step.
type QueryResult struct {
	// IDs contains one record ID slice per query embedding.
	IDs [][]string `json:"ids"`
	// Documents contains document bodies grouped by query embedding.
	Documents [][]string `json:"documents"`
	// Embeddings contains result vectors grouped by query embedding.
	Embeddings [][][]float32 `json:"embeddings"`
	// Metadatas contains metadata maps grouped by query embedding.
	Metadatas [][]map[string]any `json:"metadatas"`
	// Distances contains result distances grouped by query embedding.
	Distances [][]float32 `json:"distances"`
	// URIs contains source URIs grouped by query embedding.
	URIs [][]string `json:"uris"`

	// DistanceValid distinguishes a JSON null distance from a real zero.
	// It is populated by the default HTTP client.
	DistanceValid [][]bool `json:"-"`
}

// SearchParams is a Chroma /search request. Filter is the Search API where
// expression; when IDs is non-empty it is combined with Filter as an ID $in
// predicate. Rank is a rank-expression tree, normally built from $knn nodes.
// Limit controls the final result count.
type SearchParams struct {
	// IDs limits the search to specific record IDs.
	IDs []string
	// Filter is a Chroma Search API where expression.
	Filter map[string]any
	// Rank is a Chroma rank-expression tree.
	Rank map[string]any
	// Limit limits the final result count when positive.
	Limit int
	// Select selects response fields.
	Select []string
}

// SearchResult is a /search response. Nested slices follow Chroma's
// search-payload layout: the outer slice is one group per search payload. The
// JSON tags match the Chroma wire format so responses decode without a
// conversion step.
type SearchResult struct {
	// IDs contains one record ID slice per search payload.
	IDs [][]string `json:"ids"`
	// Documents contains document bodies grouped by search payload.
	Documents [][]*string `json:"documents"`
	// Embeddings contains result vectors grouped by search payload.
	Embeddings [][][]float32 `json:"embeddings"`
	// Metadatas contains metadata maps grouped by search payload.
	Metadatas [][]map[string]any `json:"metadatas"`
	// Scores contains rank scores grouped by search payload. Lower is better.
	Scores [][]*float32 `json:"scores"`
	// Select contains the selected fields for each search payload.
	Select [][]string `json:"select"`
}

// DeleteParams selects records for Delete by IDs, metadata Where, and/or
// WhereDocument. At least one selector is required by Chroma.
type DeleteParams struct {
	// IDs selects records by ID.
	IDs []string
	// Where selects records by metadata.
	Where map[string]any
	// WhereDocument selects records by document content.
	WhereDocument map[string]any
}

// HTTPClient sends a prepared HTTP request. It is satisfied by *http.Client.
type HTTPClient interface {
	// Do sends req and returns its HTTP response.
	Do(req *http.Request) (*http.Response, error)
}

// CollectionInfo describes the collection currently bound to a client.
type CollectionInfo struct {
	// ID is the server-assigned collection ID.
	ID string
	// Name is the collection name.
	Name string
	// Dimension is the collection vector dimension, or zero when unknown.
	Dimension int
	// Metric is the collection distance metric, or empty when unknown.
	Metric string
	// SchemaKnown reports whether the collection response included a schema.
	SchemaKnown bool
	// SparseVectorIndexKeys contains metadata keys with sparse vector indexes.
	SparseVectorIndexKeys []string
}

// ClientBuilderOpts contains options for constructing the default HTTP client.
type ClientBuilderOpts struct {
	// BaseURL is the Chroma HTTP origin, for example http://localhost:8000.
	// When HTTPClient resolves a service name, use the scheme it expects,
	// for example polaris://chroma-service.
	BaseURL string

	// HTTPClient sends the requests. Nil uses a default *http.Client.
	HTTPClient HTTPClient

	// Tenant selects a Chroma tenant. Empty uses default_tenant, or Cloud
	// identity when APIKey is set.
	Tenant string

	// Database selects a Chroma database. Empty uses default_database, or
	// Cloud identity when APIKey is set.
	Database string

	// APIKey is sent as X-Chroma-Token when non-empty. When Tenant or
	// Database is empty, the first collection call resolves them from
	// /auth/identity.
	APIKey string

	// Headers are extra HTTP headers merged onto every request.
	Headers map[string]string

	// ExtraOptions is an extension point for custom builders. The default
	// builder ignores it.
	ExtraOptions []any

	// SparseVectorIndexKey, when non-empty, is included in
	// GetOrCreateCollection as a sparse vector index on that metadata field,
	// for caller-provided sparse vectors. Existing collections are unchanged;
	// sparse indexes cannot be added after creation.
	SparseVectorIndexKey string

	// SparseVectorIndexFunction, when non-empty, is declared as the sparse
	// index's embedding function in GetOrCreateCollection, mirroring the
	// official clients' schema-declared embedding functions. Build it with
	// WithSparseVectorIndexFunction. The declaration lets clients that read
	// the schema reconstruct the function; the server does not compute
	// embeddings from it.
	SparseVectorIndexFunction map[string]any
}

// ClientBuilderOpt configures ClientBuilderOpts.
type ClientBuilderOpt func(*ClientBuilderOpts)

// WithBaseURL sets the Chroma HTTP base URL, for example http://localhost:8000.
func WithBaseURL(url string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.BaseURL = url }
}

// WithHTTPClient sets the HTTP client used for every request. This can be used
// to customize transport, service discovery, authentication, or tracing. A nil
// client is ignored and the default *http.Client is kept.
//
// An injected client keeps its own redirect policy: if it follows redirects
// across origins, it must strip sensitive headers itself. The default client
// does so for the API key and custom headers.
func WithHTTPClient(hc HTTPClient) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if hc != nil {
			o.HTTPClient = hc
		}
	}
}

// WithTenant sets the Chroma tenant. Empty uses the client default.
func WithTenant(tenant string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Tenant = tenant }
}

// WithDatabase sets the Chroma database. Empty uses the client default.
func WithDatabase(database string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Database = database }
}

// WithAPIKey sets the Chroma Cloud API key sent as X-Chroma-Token. When tenant
// or database is empty, the client resolves them from Cloud identity on the
// first collection call.
func WithAPIKey(apiKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.APIKey = strings.TrimSpace(apiKey) }
}

// WithHeaders merges extra HTTP headers onto every request.
func WithHeaders(headers map[string]string) ClientBuilderOpt {
	copied := make(map[string]string, len(headers))
	for key, value := range headers {
		copied[key] = value
	}
	return func(o *ClientBuilderOpts) {
		if len(copied) == 0 {
			return
		}
		if o.Headers == nil {
			o.Headers = make(map[string]string, len(copied))
		}
		for k, v := range copied {
			o.Headers[k] = v
		}
	}
}

// WithExtraOptions appends values for a custom builder to interpret.
func WithExtraOptions(opts ...any) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.ExtraOptions = append(o.ExtraOptions, opts...)
	}
}

// WithSparseVectorIndex includes a sparse vector index in auto-created
// collections. key is the metadata field that stores the sparse embedding,
// typically "sparse_embedding". The caller supplies sparse vectors explicitly.
// An empty key is ignored. The index cannot be added to an existing collection.
func WithSparseVectorIndex(key string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.SparseVectorIndexKey = strings.TrimSpace(key)
	}
}

// WithSparseVectorIndexFunction declares the sparse index's embedding
// function in auto-created collections, mirroring the official clients: the
// schema records a known registry function ({"type": "known", "name": name,
// "config": config}) with #document as its source. name is the registered
// function name, for example "chroma-cloud-splade"; config is the function's
// serialized configuration. The declaration is metadata for clients that
// read the schema; the server does not compute embeddings from it, and the
// caller keeps providing sparse vectors explicitly. An empty name is
// ignored and the schema declares {"type": "unknown"}.
func WithSparseVectorIndexFunction(name string, config map[string]any) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		o.SparseVectorIndexFunction = map[string]any{
			"type":   "known",
			"name":   name,
			"config": maps.Clone(config),
		}
	}
}

// ClientBuilder constructs a ClientInterface from functional options.
type ClientBuilder func(opts ...ClientBuilderOpt) (ClientInterface, error)

// The process-wide client builder defaults to defaultClientBuilder from client.go.
var (
	// builderMu protects concurrent access to globalBuilder. Tests may replace
	// the builder from parallel subtests, so access must remain synchronized.
	builderMu     sync.RWMutex
	globalBuilder ClientBuilder = defaultClientBuilder
)

// SetClientBuilder replaces the process-wide ClientBuilder, typically to inject
// a test double or custom client. A nil builder is ignored.
func SetClientBuilder(b ClientBuilder) {
	if b == nil {
		return
	}
	builderMu.Lock()
	globalBuilder = b
	builderMu.Unlock()
}

// GetClientBuilder returns the active ClientBuilder.
func GetClientBuilder() ClientBuilder {
	builderMu.RLock()
	defer builderMu.RUnlock()
	return globalBuilder
}

// ErrMissingBaseURL is returned when BaseURL is empty.
var ErrMissingBaseURL = errors.New("chroma: BaseURL must be set")

// ErrCollectionNotFound is returned when GetCollection cannot find the collection.
var ErrCollectionNotFound = errors.New("chroma: collection not found")

// ErrCollectionNotBound is returned when record operations run before
// GetOrCreateCollection or GetCollection.
var ErrCollectionNotBound = errors.New("chroma: collection is not bound")

// ErrDeleteSelector is returned when Delete has no ids, where, or where_document.
var ErrDeleteSelector = errors.New("chroma: delete requires ids, where, or where_document")

// The registry stores connection options for named Chroma instances. The
// knowledge backend resolves these names in New so application code need not
// depend on a concrete HTTP URL.

var (
	registryMu sync.RWMutex
	registry   = map[string][]ClientBuilderOpt{}
)

// RegisterChromaInstance registers connection options for a named Chroma instance.
// Re-registering a name overwrites its options. The option slice is copied so
// later changes by the caller do not affect the registry.
func RegisterChromaInstance(name string, opts ...ClientBuilderOpt) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = append([]ClientBuilderOpt(nil), opts...)
}

// GetChromaInstance returns the options registered for a name. It returns
// (nil, false) when the name is absent. The returned slice is a copy and may be
// modified without affecting registry state.
func GetChromaInstance(name string) ([]ClientBuilderOpt, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	v, ok := registry[name]
	if !ok {
		return nil, false
	}
	return append([]ClientBuilderOpt(nil), v...), true
}

// UnregisterChromaInstance removes a named Chroma instance. It is a no-op when
// the name is absent.
func UnregisterChromaInstance(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, name)
}

// ListChromaInstances returns the names of all registered Chroma instances.
// The order is unspecified.
func ListChromaInstances() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
