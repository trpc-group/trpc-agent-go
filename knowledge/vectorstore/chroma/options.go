//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chroma provides a vector store implementation backed by Chroma.
package chroma

import (
	"fmt"
	"maps"
	"math"
	"strings"
)

const (
	defaultMaxResults       = 10
	defaultIndexDimension   = 1536
	defaultRRFOffset        = 60
	defaultCandidateRatio   = 3
	defaultMinCandidates    = 100
	defaultDenseWeight      = 0.5
	defaultSparseWeight     = 0.5
	defaultMaxUpdateRecords = 100000
	// defaultMaxRequestRecords matches Chroma Cloud's per-request record limit.
	defaultMaxRequestRecords = 300
	// defaultSparseEmbeddingKey is Chroma's example sparse vector metadata field.
	defaultSparseEmbeddingKey = "sparse_embedding"
)

var (
	includeRecordFields       = []string{"documents", "metadatas", "embeddings"}
	includeQueryFields        = []string{"documents", "metadatas", "distances"}
	includeMetadataOnlyFields = []string{"metadatas"}
	includeIDOnlyFields       = []string{}
	// includeSearchFields serves filter search, which returns documents and
	// metadata but never uses the stored embeddings.
	includeSearchFields = []string{"documents", "metadatas"}
)

const (
	metaName      = "name"
	metaCreatedAt = "created_at"
	metaUpdatedAt = "updated_at"
	metaJSON      = "_json"
)

// reservedMetadataKeys is a list of metadata keys that are reserved for internal use.
var reservedMetadataKeys = []string{metaName, metaCreatedAt, metaUpdatedAt, metaJSON}

type options struct {
	collection           string
	indexDimension       int
	baseURL              string
	instanceName         string
	tenant               string
	database             string
	authToken            string
	bearerToken          string
	headers              map[string]string
	autoCreateCollection bool
	extraOptions         []any
	maxResults           int
	maxRequestRecords    int
	maxUpdateRecords     int
	sparseSearch         bool
	sparseSearchKey      string
	hybridDenseWeight    float64
	hybridSparseWeight   float64
	sparseEmbedder       SparseEmbedder
}

var defaultOptions = options{
	indexDimension:       defaultIndexDimension,
	maxResults:           defaultMaxResults,
	maxRequestRecords:    defaultMaxRequestRecords,
	maxUpdateRecords:     defaultMaxUpdateRecords,
	autoCreateCollection: true,
	hybridDenseWeight:    defaultDenseWeight,
	hybridSparseWeight:   defaultSparseWeight,
}

// Option configures a VectorStore.
type Option func(*options)

// WithCollection sets the required Chroma collection name.
func WithCollection(name string) Option {
	return func(o *options) { o.collection = name }
}

// WithIndexDimension sets the vector dimension, which must match the embedding output.
func WithIndexDimension(dim int) Option {
	return func(o *options) { o.indexDimension = dim }
}

// WithBaseURL sets the Chroma HTTP address used when no named instance is selected.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithChromaInstance selects a named client registered with
// storage.RegisterChromaInstance. A named instance takes priority over the
// other connection options, which are ignored.
func WithChromaInstance(name string) Option {
	return func(o *options) { o.instanceName = name }
}

// WithTenant sets the Chroma tenant. Empty uses the server default; with
// WithAPIKey it is resolved from the Cloud identity when the identity reports
// a tenant. It is ignored with WithChromaInstance.
func WithTenant(tenant string) Option {
	return func(o *options) { o.tenant = tenant }
}

// WithDatabase sets the Chroma database. Empty uses the server default; with
// WithAPIKey a database-scoped key resolves it from the Cloud identity, which
// requires exactly one database. It is ignored with WithChromaInstance.
func WithDatabase(database string) Option {
	return func(o *options) { o.database = database }
}

// WithAPIKey sets the Chroma Cloud API key sent as X-Chroma-Token. When the
// tenant or database is empty it is resolved from /auth/identity, so a
// database-scoped key makes both options optional; an identity that reports no
// database or several databases is an error. Ignored with WithChromaInstance.
func WithAPIKey(apiKey string) Option {
	return func(o *options) { o.authToken = strings.TrimSpace(apiKey) }
}

// WithBearerToken sets an Authorization bearer token for an authentication
// gateway. It is ignored with WithChromaInstance.
func WithBearerToken(token string) Option {
	return func(o *options) { o.bearerToken = strings.TrimSpace(token) }
}

// WithHeaders merges HTTP headers sent on every request. Custom authentication
// headers conflict with WithAPIKey and WithBearerToken and require tenant and
// database.
func WithHeaders(headers map[string]string) Option {
	copied := maps.Clone(headers)
	return func(o *options) {
		if len(copied) == 0 {
			return
		}
		if o.headers == nil {
			o.headers = make(map[string]string, len(copied))
		}
		for k, v := range copied {
			o.headers[k] = v
		}
	}
}

// WithAutoCreateCollection controls whether New creates a missing collection.
// It defaults to true and, with WithSparseSearch, also adds the sparse index.
func WithAutoCreateCollection(enable bool) Option {
	return func(o *options) { o.autoCreateCollection = enable }
}

// WithMaxResults sets the default Search limit.
func WithMaxResults(n int) Option {
	return func(o *options) { o.maxResults = n }
}

// WithMaxRequestRecords caps records per Chroma call. Get operations page
// beyond it; Vector Query and /search are capped because they have no cursor.
func WithMaxRequestRecords(n int) Option {
	return func(o *options) { o.maxRequestRecords = n }
}

// WithMaxUpdateRecords caps how many records one UpdateByFilter call may match.
// The match set is fixed before writes begin.
func WithMaxUpdateRecords(n int) Option {
	return func(o *options) { o.maxUpdateRecords = n }
}

// WithSparseSearch enables keyword and hybrid search. The server must implement
// Chroma's /search API. The embedder is optional: omitting it uses the built-in
// CloudSpladeEmbedder and requires WithAPIKey. When auto-create is enabled, New
// creates the sparse index on a new collection; existing collections must
// already have one.
func WithSparseSearch(embedder ...SparseEmbedder) Option {
	return func(o *options) {
		o.sparseSearch = true
		for _, e := range embedder {
			if e != nil {
				o.sparseEmbedder = e
				break
			}
		}
	}
}

// WithSparseSearchKey sets the metadata key used for sparse vectors. It
// defaults to "sparse_embedding" and only applies with WithSparseSearch.
func WithSparseSearchKey(key string) Option {
	return func(o *options) { o.sparseSearchKey = strings.TrimSpace(key) }
}

// WithHybridSearchWeights sets the dense and sparse RRF weights. New rejects
// negative, non-finite, or both-zero values and normalizes valid weights to 1.
func WithHybridSearchWeights(denseWeight, sparseWeight float64) Option {
	return func(o *options) {
		o.hybridDenseWeight = denseWeight
		o.hybridSparseWeight = sparseWeight
	}
}

// WithExtraOptions passes opaque values to a custom ClientBuilder. The default
// HTTP builder ignores them.
func WithExtraOptions(opts ...any) Option {
	return func(o *options) {
		o.extraOptions = append(o.extraOptions, opts...)
	}
}

func validateOptions(o *options) error {
	if o.collection == "" {
		return errCollectionRequired
	}
	if o.indexDimension <= 0 {
		return fmt.Errorf("chroma: indexDimension must be > 0, got %d", o.indexDimension)
	}
	if o.maxResults <= 0 {
		return fmt.Errorf("chroma: maxResults must be > 0, got %d", o.maxResults)
	}
	if o.maxRequestRecords <= 0 {
		return fmt.Errorf("chroma: max request records must be > 0, got %d", o.maxRequestRecords)
	}
	if o.maxUpdateRecords <= 0 {
		return fmt.Errorf("chroma: max update records must be > 0, got %d", o.maxUpdateRecords)
	}
	if o.sparseSearch {
		if o.sparseEmbedder == nil {
			if o.authToken == "" {
				return fmt.Errorf(
					"chroma: sparse search without an embedder uses the built-in Cloud SPLADE embedder and requires WithAPIKey, or pass an explicit SparseEmbedder to WithSparseSearch",
				)
			}
			embedder, err := NewCloudSpladeEmbedder(o.authToken)
			if err != nil {
				return err
			}
			o.sparseEmbedder = embedder
		}
		if o.sparseSearchKey == "" {
			o.sparseSearchKey = defaultSparseEmbeddingKey
		}
		if strings.HasPrefix(o.sparseSearchKey, "#") ||
			strings.HasPrefix(o.sparseSearchKey, "$") ||
			isReservedKey(o.sparseSearchKey) {
			return fmt.Errorf("chroma: sparse search key %q is reserved", o.sparseSearchKey)
		}
	}
	if o.hybridDenseWeight < 0 || o.hybridSparseWeight < 0 ||
		math.IsNaN(o.hybridDenseWeight) || math.IsNaN(o.hybridSparseWeight) ||
		math.IsInf(o.hybridDenseWeight, 0) || math.IsInf(o.hybridSparseWeight, 0) ||
		o.hybridDenseWeight+o.hybridSparseWeight <= 0 {
		return fmt.Errorf("chroma: hybrid weights must be finite, non-negative, and not both zero")
	}
	total := o.hybridDenseWeight + o.hybridSparseWeight
	o.hybridDenseWeight /= total
	o.hybridSparseWeight /= total
	if o.instanceName == "" {
		if err := validateAuthentication(o); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthentication(o *options) error {
	if o.authToken != "" && o.bearerToken != "" {
		return fmt.Errorf("chroma: API key and bearer token are mutually exclusive")
	}
	var hasAuthorization, hasChromaToken bool
	for name := range o.headers {
		hasAuthorization = hasAuthorization || strings.EqualFold(name, "Authorization")
		hasChromaToken = hasChromaToken || strings.EqualFold(name, "X-Chroma-Token")
	}
	if hasAuthorization && hasChromaToken {
		return fmt.Errorf("chroma: custom authorization and x-chroma-token headers are mutually exclusive")
	}
	if (hasAuthorization || hasChromaToken) &&
		(o.authToken != "" || o.bearerToken != "") {
		return fmt.Errorf("chroma: custom authentication header conflicts with configured authentication")
	}
	if (hasAuthorization || hasChromaToken) && (o.tenant == "" || o.database == "") {
		return fmt.Errorf("chroma: tenant and database are required with custom authentication headers")
	}
	return nil
}
