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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// apiPathPrefix is the Chroma v2 REST root.
	apiPathPrefix = "/api/v2"

	// defaultTenant and defaultDatabase mirror the Chroma server defaults.
	defaultTenant   = "default_tenant"
	defaultDatabase = "default_database"

	cosineSpace = "cosine"

	// authTokenHeader carries the static auth token.
	authTokenHeader = "X-Chroma-Token"

	// maxErrorBody caps how much of a failed response is kept in the error.
	maxErrorBody = 4 << 10
	maxDrainBody = 64 << 10

	defaultRequestTimeout = 60 * time.Second
)

// httpClient implements ClientInterface against the Chroma v2 REST API.
//
// Requests are plain JSON, so ClientInterface payloads map onto the wire
// format directly: Where filters, metadata, and embeddings are forwarded
// as-is rather than being converted into an intermediate object model.
//
// collectionID is set by GetOrCreateCollection or GetCollection. Record
// operations address a collection by ID, not by name, and fail with
// ErrCollectionNotBound until one of those calls succeeds.
type httpClient struct {
	baseURL        string
	tenant         string
	database       string
	headers        map[string]string
	hc             HTTPClient
	ownedTransport *http.Transport
	bindingMu      sync.RWMutex
	collectionID   string
	collection     CollectionInfo

	scopeMu              sync.Mutex
	inferScope           bool
	sparseVectorIndexKey string
	sparseVectorIndexFn  map[string]any
}

// Ensure httpClient implements ClientInterface.
var _ ClientInterface = (*httpClient)(nil)

// defaultClientBuilder constructs an HTTP client for a Chroma server. Tenant
// and database fall back to the Chroma defaults unless APIKey is set and
// either value is empty; in that case the first collection call resolves them
// from /auth/identity. APIKey is sent as X-Chroma-Token.
func defaultClientBuilder(opts ...ClientBuilderOpt) (ClientInterface, error) {
	o := &ClientBuilderOpts{}
	for _, f := range opts {
		f(o)
	}
	if o.BaseURL == "" {
		return nil, ErrMissingBaseURL
	}
	base, err := normalizeBaseURL(o.BaseURL)
	if err != nil {
		return nil, err
	}
	inferScope := o.APIKey != "" && (o.Tenant == "" || o.Database == "")
	tenant, database := o.Tenant, o.Database
	if !inferScope {
		tenant = orDefault(tenant, defaultTenant)
		database = orDefault(database, defaultDatabase)
	}
	c := &httpClient{
		baseURL:              base,
		tenant:               tenant,
		database:             database,
		hc:                   o.HTTPClient,
		inferScope:           inferScope,
		sparseVectorIndexKey: o.SparseVectorIndexKey,
		sparseVectorIndexFn:  o.SparseVectorIndexFunction,
	}
	if o.APIKey != "" || len(o.Headers) > 0 {
		c.headers = make(map[string]string, len(o.Headers)+1)
		for k, v := range o.Headers {
			c.headers[http.CanonicalHeaderKey(k)] = v
		}
		if o.APIKey != "" {
			c.headers[http.CanonicalHeaderKey(authTokenHeader)] = o.APIKey
		}
	}
	if c.hc == nil {
		// Use a private pool so Close never affects http.DefaultTransport users.
		if transport, ok := http.DefaultTransport.(*http.Transport); ok {
			c.ownedTransport = transport.Clone()
			c.hc = &http.Client{Transport: c.ownedTransport, CheckRedirect: c.checkRedirect}
		} else {
			// A replaced default transport is caller-owned and must not be closed.
			c.hc = &http.Client{Transport: http.DefaultTransport, CheckRedirect: c.checkRedirect}
		}
	}
	return c, nil
}

// checkRedirect strips custom headers when a redirect leaves the original
// origin. Go forwards all non-builtin headers on redirect, which would leak
// X-Chroma-Token and other caller-provided headers to the redirect target.
func (c *httpClient) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 {
		origin := via[0].URL
		if strings.EqualFold(req.URL.Scheme, origin.Scheme) && strings.EqualFold(req.URL.Host, origin.Host) {
			return nil
		}
	}
	for k := range c.headers {
		req.Header.Del(k)
	}
	return nil
}

// normalizeBaseURL trims trailing slashes and appends the v2 API prefix so
// callers may pass either a bare origin or a full API root.
func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("chroma: invalid base url %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("chroma: base url %q must include scheme and host", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("chroma: base url %q must not include query or fragment", raw)
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, apiPathPrefix) {
		p += apiPathPrefix
	}
	u.Path = p
	return u.String(), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// statusError is a non-2xx Chroma response. It is kept as a distinct type so
// IsNotFound can test the status code instead of matching on message text.
type statusError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *statusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("chroma: %s %s: status %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("chroma: %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// do sends body as JSON and decodes the response into out. A nil body sends no
// payload; a nil out discards the response. Calls without a context deadline
// receive a bounded default timeout.
func (c *httpClient) do(ctx context.Context, method, path string, body, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRequestTimeout)
		defer cancel()
	}
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("chroma: encode %s request: %w", path, err)
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("chroma: build %s request: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("chroma: %s %s: %w", method, path, err)
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("chroma: %s %s: HTTP client returned an empty response", method, path)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBody))
		return &statusError{
			Method: method,
			Path:   path,
			Status: resp.StatusCode,
			Body:   strings.TrimSpace(string(b)),
		}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("chroma: decode %s response: %w", path, err)
	}
	return nil
}

// ensureScope fills tenant and database from /auth/identity when an API key
// was provided without an explicit scope. Identity is a Cloud endpoint;
// self-hosted deployments should set tenant and database explicitly.
func (c *httpClient) ensureScope(ctx context.Context) error {
	c.scopeMu.Lock()
	defer c.scopeMu.Unlock()
	if !c.inferScope {
		return nil
	}
	var identity identityResponse
	if err := c.do(ctx, http.MethodGet, "/auth/identity", nil, &identity); err != nil {
		return fmt.Errorf("chroma: resolve identity: %w", err)
	}
	if c.tenant == "" {
		if identity.Tenant == "" || identity.Tenant == "*" {
			return errors.New("chroma: identity did not provide a unique tenant")
		}
		c.tenant = identity.Tenant
	}
	if c.database == "" {
		database, err := uniqueDatabase(identity.Databases)
		if err != nil {
			return err
		}
		c.database = database
	}
	c.inferScope = false
	return nil
}

// uniqueDatabase accepts identity inference only when exactly one database is available.
func uniqueDatabase(databases []string) (string, error) {
	unique := ""
	for _, database := range databases {
		if database == "" || database == "*" {
			continue
		}
		if unique != "" && unique != database {
			return "", errors.New("chroma: identity provides multiple databases; configure one explicitly")
		}
		unique = database
	}
	if unique == "" {
		return "", errors.New("chroma: identity did not provide a unique database")
	}
	return unique, nil
}

// collectionsPath is the tenant- and database-scoped collections endpoint.
func (c *httpClient) collectionsPath() string {
	return "/tenants/" + url.PathEscape(c.tenant) +
		"/databases/" + url.PathEscape(c.database) + "/collections"
}

// recordPath addresses an operation using a collection ID snapshot captured by
// the caller. This prevents a concurrent rebind from changing the destination
// between the bound check and request construction.
func (c *httpClient) recordPath(id, op string) string {
	return c.collectionsPath() + "/" + url.PathEscape(id) + "/" + op
}

func (c *httpClient) boundCollectionID() string {
	c.bindingMu.RLock()
	defer c.bindingMu.RUnlock()
	return c.collectionID
}

// Heartbeat checks that the Chroma HTTP server is reachable.
func (c *httpClient) Heartbeat(ctx context.Context) error {
	if err := c.do(ctx, http.MethodGet, "/heartbeat", nil, nil); err != nil {
		return fmt.Errorf("chroma: heartbeat: %w", err)
	}
	return nil
}

// collectionModel is the part of Chroma's collection response we consume.
type collectionModel struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Dimension         *int                    `json:"dimension"`
	ConfigurationJSON collectionConfiguration `json:"configuration_json"`
	Schema            *map[string]any         `json:"schema"`
}

type collectionConfiguration struct {
	HNSW  *indexConfiguration `json:"hnsw"`
	SPANN *indexConfiguration `json:"spann"`
}

type indexConfiguration struct {
	Space string `json:"space"`
}

type createCollectionConfiguration struct {
	// Chroma Cloud may materialize this HNSW cosine request as a SPANN cosine index.
	HNSW indexConfiguration `json:"hnsw"`
}

type identityResponse struct {
	Tenant    string   `json:"tenant"`
	Databases []string `json:"databases"`
}

func (m collectionModel) info() CollectionInfo {
	info := CollectionInfo{ID: m.ID, Name: m.Name}
	if m.Dimension != nil {
		info.Dimension = *m.Dimension
	}
	hnsw := m.ConfigurationJSON.HNSW
	spann := m.ConfigurationJSON.SPANN
	switch {
	case hnsw != nil && spann != nil:
		if strings.EqualFold(hnsw.Space, spann.Space) {
			info.Metric = hnsw.Space
		}
	case hnsw != nil:
		info.Metric = hnsw.Space
	case spann != nil:
		info.Metric = spann.Space
	}
	if m.Schema != nil {
		info.SchemaKnown = true
		info.SparseVectorIndexKeys = sparseVectorIndexKeys(*m.Schema)
	}
	return info
}

func sparseVectorIndexKeys(schema map[string]any) []string {
	keys, ok := schema["keys"].(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for key, raw := range keys {
		config, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sparseVector, ok := config["sparse_vector"].(map[string]any)
		if !ok {
			continue
		}
		index, ok := sparseVector["sparse_vector_index"].(map[string]any)
		if !ok {
			continue
		}
		enabled, _ := index["enabled"].(bool)
		if enabled {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func (c *httpClient) bind(cm collectionModel) {
	c.bindingMu.Lock()
	c.collectionID = cm.ID
	c.collection = cm.info()
	c.bindingMu.Unlock()
}

// CollectionInfo returns a snapshot of the bound collection configuration.
func (c *httpClient) CollectionInfo() CollectionInfo {
	c.bindingMu.RLock()
	defer c.bindingMu.RUnlock()
	return c.collection
}

// GetOrCreateCollection binds the client to name, creating a cosine HNSW
// collection when it does not exist. Chroma Cloud may expose the created
// collection as a cosine SPANN index. When sparseVectorIndexKey is set, a
// newly created collection also includes a sparse vector index on that field.
func (c *httpClient) GetOrCreateCollection(ctx context.Context, name string, metadata map[string]any) error {
	if name == "" {
		return errors.New("chroma: collection name is required")
	}
	if err := c.ensureScope(ctx); err != nil {
		return err
	}
	req := struct {
		Name          string                         `json:"name"`
		GetOrCreate   bool                           `json:"get_or_create"`
		Metadata      map[string]any                 `json:"metadata,omitempty"`
		Configuration *createCollectionConfiguration `json:"configuration,omitempty"`
		Schema        map[string]any                 `json:"schema,omitempty"`
	}{
		Name:        name,
		GetOrCreate: true,
		Metadata:    metadata,
	}
	if c.sparseVectorIndexKey != "" {
		req.Schema = sparseVectorCollectionSchema(c.sparseVectorIndexKey, c.sparseVectorIndexFn)
	} else {
		req.Configuration = &createCollectionConfiguration{
			HNSW: indexConfiguration{Space: cosineSpace},
		}
	}

	var cm collectionModel
	if err := c.do(ctx, http.MethodPost, c.collectionsPath(), req, &cm); err != nil {
		return fmt.Errorf("chroma: get or create collection %q: %w", name, err)
	}
	if cm.ID == "" {
		return fmt.Errorf("chroma: get or create collection %q: response has no id", name)
	}
	c.bind(cm)
	return nil
}

const documentSchemaKey = "#document"

// sparseVectorCollectionSchema mirrors Chroma 1.5.3's serialized default
// Schema with a cosine, caller-embedded dense index and one caller-embedded
// sparse index. A complete schema is required because Chroma does not merge a
// partial keys-only schema with its defaults. The sparse index declares an
// unknown embedding function, which is the server's canonical form for a
// sparse index whose vectors are provided by the caller. embeddingFunction,
// when non-empty, replaces that declaration with a known registry function
// and names #document as its source, matching the official clients.
func sparseVectorCollectionSchema(key string, embeddingFunction map[string]any) map[string]any {
	disabled := func(config map[string]any) map[string]any {
		return map[string]any{"enabled": false, "config": config}
	}
	enabled := func(config map[string]any) map[string]any {
		return map[string]any{"enabled": true, "config": config}
	}
	unknownEmbedding := map[string]any{"embedding_function": map[string]any{"type": "unknown"}}
	sparseConfig := unknownEmbedding
	if len(embeddingFunction) > 0 {
		sparseConfig = map[string]any{
			"embedding_function": embeddingFunction,
			"source_key":         documentSchemaKey,
		}
	}
	denseDefaults := map[string]any{
		"space":              cosineSpace,
		"embedding_function": map[string]any{"type": "legacy"},
	}
	denseKey := map[string]any{
		"space":              cosineSpace,
		"source_key":         documentSchemaKey,
		"embedding_function": map[string]any{"type": "legacy"},
	}
	return map[string]any{
		"defaults": map[string]any{
			"string": map[string]any{
				"fts_index":             disabled(map[string]any{}),
				"string_inverted_index": enabled(map[string]any{}),
			},
			"float_list": map[string]any{
				"vector_index": disabled(denseDefaults),
			},
			"sparse_vector": map[string]any{
				"sparse_vector_index": disabled(unknownEmbedding),
			},
			"int": map[string]any{
				"int_inverted_index": enabled(map[string]any{}),
			},
			"float": map[string]any{
				"float_inverted_index": enabled(map[string]any{}),
			},
			"bool": map[string]any{
				"bool_inverted_index": enabled(map[string]any{}),
			},
		},
		"keys": map[string]any{
			documentSchemaKey: map[string]any{
				"string": map[string]any{
					"fts_index":             enabled(map[string]any{}),
					"string_inverted_index": disabled(map[string]any{}),
				},
			},
			"#embedding": map[string]any{
				"float_list": map[string]any{
					"vector_index": enabled(denseKey),
				},
			},
			key: map[string]any{
				"sparse_vector": map[string]any{
					"sparse_vector_index": enabled(sparseConfig),
				},
			},
		},
	}
}

// GetCollection binds the client to an existing collection. Missing
// collections return ErrCollectionNotFound.
func (c *httpClient) GetCollection(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("chroma: collection name is required")
	}
	if err := c.ensureScope(ctx); err != nil {
		return err
	}
	var cm collectionModel
	path := c.collectionsPath() + "/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodGet, path, nil, &cm); err != nil {
		if IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrCollectionNotFound, name)
		}
		return fmt.Errorf("chroma: get collection %q: %w", name, err)
	}
	if cm.ID == "" {
		return fmt.Errorf("chroma: get collection %q: response has no id", name)
	}
	c.bind(cm)
	return nil
}

// DeleteCollection removes a collection by name.
func (c *httpClient) DeleteCollection(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("chroma: collection name is required")
	}
	if err := c.ensureScope(ctx); err != nil {
		return err
	}
	path := c.collectionsPath() + "/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		if IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrCollectionNotFound, name)
		}
		return fmt.Errorf("chroma: delete collection %q: %w", name, err)
	}
	c.bindingMu.Lock()
	if c.collection.Name == name || c.collection.ID == name {
		c.collectionID = ""
		c.collection = CollectionInfo{}
	}
	c.bindingMu.Unlock()
	return nil
}

// recordRequest is the shared Add/Upsert/Update payload. Slices align by index.
type recordRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float32      `json:"embeddings,omitempty"`
	Documents  []string         `json:"documents,omitempty"`
	Metadatas  []map[string]any `json:"metadatas,omitempty"`
}

// writeRecords posts a record batch to op, one of add, upsert, or update.
func (c *httpClient) writeRecords(ctx context.Context, op string, rec RecordBatch) error {
	id := c.boundCollectionID()
	if id == "" {
		return ErrCollectionNotBound
	}
	if err := validateRecordBatch(op, rec); err != nil {
		return err
	}
	req := recordRequest{
		IDs:        rec.IDs,
		Embeddings: rec.Embeddings,
		Documents:  rec.Documents,
		Metadatas:  rec.Metadatas,
	}
	if err := c.do(ctx, http.MethodPost, c.recordPath(id, op), req, nil); err != nil {
		return fmt.Errorf("chroma: %s: %w", op, err)
	}
	return nil
}

func validateRecordBatch(op string, rec RecordBatch) error {
	n := len(rec.IDs)
	if n == 0 {
		return errors.New("chroma: record ids are required")
	}
	if (op == "add" || op == "upsert") && len(rec.Embeddings) != n {
		return fmt.Errorf("chroma: %s embeddings length must equal ids length", op)
	}
	for name, size := range map[string]int{
		"embeddings": len(rec.Embeddings),
		"documents":  len(rec.Documents),
		"metadatas":  len(rec.Metadatas),
	} {
		if size != 0 && size != n {
			return fmt.Errorf("chroma: %s %s length must equal ids length", op, name)
		}
	}
	if op == "add" || op == "upsert" {
		for i, embedding := range rec.Embeddings {
			if len(embedding) == 0 {
				return fmt.Errorf("chroma: %s embedding %d is required", op, i)
			}
		}
	}
	return nil
}

func (c *httpClient) Add(ctx context.Context, rec RecordBatch) error {
	return c.writeRecords(ctx, "add", rec)
}

func (c *httpClient) Upsert(ctx context.Context, rec RecordBatch) error {
	return c.writeRecords(ctx, "upsert", rec)
}

func (c *httpClient) Update(ctx context.Context, rec RecordBatch) error {
	return c.writeRecords(ctx, "update", rec)
}

// getRequest is the /get payload. Where and WhereDocument are forwarded as
// given; Chroma validates the operators.
type getRequest struct {
	IDs           []string       `json:"ids,omitempty"`
	Where         map[string]any `json:"where,omitempty"`
	WhereDocument map[string]any `json:"where_document,omitempty"`
	Include       *[]string      `json:"include,omitempty"`
	Limit         *int           `json:"limit,omitempty"`
	Offset        *int           `json:"offset,omitempty"`
}

func (c *httpClient) Get(ctx context.Context, p GetParams) (*GetResult, error) {
	id := c.boundCollectionID()
	if id == "" {
		return nil, ErrCollectionNotBound
	}
	include, err := validIncludes(p.Include)
	if err != nil {
		return nil, err
	}
	req := getRequest{
		IDs:           p.IDs,
		Where:         p.Where,
		WhereDocument: p.WhereDocument,
		Limit:         p.Limit,
		Offset:        p.Offset,
	}
	if p.Include != nil {
		req.Include = &include
	}
	var wire struct {
		IDs        []string         `json:"ids"`
		Documents  []*string        `json:"documents"`
		Embeddings [][]float32      `json:"embeddings"`
		Metadatas  []map[string]any `json:"metadatas"`
		URIs       []*string        `json:"uris"`
	}
	if err := c.do(ctx, http.MethodPost, c.recordPath(id, "get"), req, &wire); err != nil {
		return nil, fmt.Errorf("chroma: get: %w", err)
	}
	return &GetResult{
		IDs:        wire.IDs,
		Documents:  stringsFromPointers(wire.Documents),
		Embeddings: wire.Embeddings,
		Metadatas:  wire.Metadatas,
		URIs:       stringsFromPointers(wire.URIs),
	}, nil
}

// deleteRequest is the /delete payload. Chroma requires at least one selector.
type deleteRequest struct {
	IDs           []string       `json:"ids,omitempty"`
	Where         map[string]any `json:"where,omitempty"`
	WhereDocument map[string]any `json:"where_document,omitempty"`
}

func (c *httpClient) Delete(ctx context.Context, p DeleteParams) error {
	id := c.boundCollectionID()
	if id == "" {
		return ErrCollectionNotBound
	}
	if len(p.IDs) == 0 && len(p.Where) == 0 && len(p.WhereDocument) == 0 {
		return ErrDeleteSelector
	}
	req := deleteRequest{
		IDs:           p.IDs,
		Where:         p.Where,
		WhereDocument: p.WhereDocument,
	}
	if err := c.do(ctx, http.MethodPost, c.recordPath(id, "delete"), req, nil); err != nil {
		return fmt.Errorf("chroma: delete: %w", err)
	}
	return nil
}

// queryRequest is the /query payload.
type queryRequest struct {
	QueryEmbeddings [][]float32    `json:"query_embeddings"`
	NResults        *int           `json:"n_results,omitempty"`
	IDs             []string       `json:"ids,omitempty"`
	Where           map[string]any `json:"where,omitempty"`
	WhereDocument   map[string]any `json:"where_document,omitempty"`
	Include         *[]string      `json:"include,omitempty"`
}

// Query is the dense vector search. It uses the classic /query endpoint rather
// than the rank-expression /search endpoint.
func (c *httpClient) Query(ctx context.Context, p QueryParams) (*QueryResult, error) {
	id := c.boundCollectionID()
	if id == "" {
		return nil, ErrCollectionNotBound
	}
	if len(p.QueryEmbeddings) == 0 {
		return nil, errors.New("chroma: query embeddings are required")
	}
	include, err := validIncludes(p.Include)
	if err != nil {
		return nil, err
	}
	req := queryRequest{
		QueryEmbeddings: p.QueryEmbeddings,
		IDs:             p.IDs,
		Where:           p.Where,
		WhereDocument:   p.WhereDocument,
	}
	if p.Include != nil {
		req.Include = &include
	}
	if p.NResults > 0 {
		req.NResults = &p.NResults
	}
	var wire struct {
		IDs        [][]string         `json:"ids"`
		Documents  [][]*string        `json:"documents"`
		Embeddings [][][]float32      `json:"embeddings"`
		Metadatas  [][]map[string]any `json:"metadatas"`
		Distances  [][]*float32       `json:"distances"`
		URIs       [][]*string        `json:"uris"`
	}
	if err := c.do(ctx, http.MethodPost, c.recordPath(id, "query"), req, &wire); err != nil {
		return nil, fmt.Errorf("chroma: query: %w", err)
	}
	distances, valid := floatsFromPointers(wire.Distances)
	return &QueryResult{
		IDs:           wire.IDs,
		Documents:     nestedStringsFromPointers(wire.Documents),
		Embeddings:    wire.Embeddings,
		Metadatas:     wire.Metadatas,
		Distances:     distances,
		URIs:          nestedStringsFromPointers(wire.URIs),
		DistanceValid: valid,
	}, nil
}

// searchRequest is the /search payload. Chroma serializes Limit and Select as
// objects, and Filter directly as a where expression.
type searchRequest struct {
	Searches []searchPayload `json:"searches"`
}

type searchPayload struct {
	Filter map[string]any `json:"filter,omitempty"`
	Rank   map[string]any `json:"rank,omitempty"`
	Limit  *searchLimit   `json:"limit,omitempty"`
	Select *searchSelect  `json:"select,omitempty"`
}

type searchLimit struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset"`
}

type searchSelect struct {
	Keys []string `json:"keys"`
}

// Search executes the Chroma Cloud /search rank-expression API. Chroma uses
// distance-style scores where lower is better.
func (c *httpClient) Search(ctx context.Context, p SearchParams) (*SearchResult, error) {
	id := c.boundCollectionID()
	if id == "" {
		return nil, ErrCollectionNotBound
	}
	if p.Rank == nil {
		return nil, errors.New("chroma: search rank is required")
	}
	payload := searchPayload{
		Filter: p.Filter,
		Rank:   p.Rank,
	}
	if len(p.IDs) > 0 {
		idFilter := map[string]any{"#id": map[string]any{"$in": p.IDs}}
		if len(payload.Filter) == 0 {
			payload.Filter = idFilter
		} else {
			payload.Filter = map[string]any{"$and": []any{idFilter, payload.Filter}}
		}
	}
	if p.Limit > 0 {
		payload.Limit = &searchLimit{Limit: p.Limit}
	}
	if len(p.Select) > 0 {
		payload.Select = &searchSelect{Keys: p.Select}
	}
	req := searchRequest{Searches: []searchPayload{payload}}
	var wire SearchResult
	if err := c.do(ctx, http.MethodPost, c.recordPath(id, "search"), req, &wire); err != nil {
		if IsNotImplemented(err) {
			return nil, fmt.Errorf("chroma: search: %w", ErrSearchNotImplemented)
		}
		return nil, fmt.Errorf("chroma: search: %w", err)
	}
	if len(wire.IDs) != 1 {
		return nil, fmt.Errorf("chroma: search: expected one payload result, got %d", len(wire.IDs))
	}
	return &wire, nil
}

// Count returns the number of records in the bound collection. Chroma answers
// with a bare JSON number.
func (c *httpClient) Count(ctx context.Context) (int, error) {
	id := c.boundCollectionID()
	if id == "" {
		return 0, ErrCollectionNotBound
	}
	var n int
	if err := c.do(ctx, http.MethodGet, c.recordPath(id, "count"), nil, &n); err != nil {
		return 0, fmt.Errorf("chroma: count: %w", err)
	}
	return n, nil
}

// MaxBatchSize returns the server-advertised write batch limit.
func (c *httpClient) MaxBatchSize(ctx context.Context) (int, error) {
	var res struct {
		MaxBatchSize int `json:"max_batch_size"`
	}
	if err := c.do(ctx, http.MethodGet, "/pre-flight-checks", nil, &res); err != nil {
		return 0, fmt.Errorf("chroma: pre-flight checks: %w", err)
	}
	if res.MaxBatchSize <= 0 {
		return 0, errors.New("chroma: pre-flight checks returned an invalid max_batch_size")
	}
	return res.MaxBatchSize, nil
}

// Close releases idle connections owned by this client. An HTTP client supplied
// through WithHTTPClient remains owned by the caller and is not modified.
func (c *httpClient) Close() error {
	if c.ownedTransport != nil {
		c.ownedTransport.CloseIdleConnections()
	}
	return nil
}

// includeFields are the projection fields Chroma accepts. Unknown names fail
// locally so caller typos are not silently discarded.
var includeFields = map[string]bool{
	"documents":  true,
	"embeddings": true,
	"metadatas":  true,
	"distances":  true,
	"uris":       true,
}

func validIncludes(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !includeFields[n] {
			return nil, fmt.Errorf("chroma: unsupported include field %q", n)
		}
		out = append(out, n)
	}
	return out, nil
}

func stringsFromPointers(in []*string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, v := range in {
		if v != nil {
			out[i] = *v
		}
	}
	return out
}

func nestedStringsFromPointers(in [][]*string) [][]string {
	if in == nil {
		return nil
	}
	out := make([][]string, len(in))
	for i := range in {
		out[i] = stringsFromPointers(in[i])
	}
	return out
}

func floatsFromPointers(in [][]*float32) ([][]float32, [][]bool) {
	if in == nil {
		return nil, nil
	}
	values := make([][]float32, len(in))
	valid := make([][]bool, len(in))
	for i := range in {
		values[i] = make([]float32, len(in[i]))
		valid[i] = make([]bool, len(in[i]))
		for j, v := range in[i] {
			if v != nil {
				values[i][j] = *v
				valid[i][j] = true
			}
		}
	}
	return values, valid
}

// IsNotFound reports whether err is a missing-collection error. Chroma answers
// 404 for an unknown collection name.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCollectionNotFound) {
		return true
	}
	var se *statusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return true
	}
	return false
}

// IsNotImplemented reports whether err identifies an operation that the Chroma
// server does not implement. Chroma OSS servers do not register /search, which
// FastAPI reports as a route-level 404; some server implementations use 501.
func IsNotImplemented(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSearchNotImplemented) {
		return true
	}
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status == http.StatusNotImplemented {
		return true
	}
	if !strings.HasSuffix(se.Path, "/search") {
		return false
	}
	if se.Status == http.StatusNotFound {
		var response struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal([]byte(se.Body), &response) == nil &&
			strings.EqualFold(strings.TrimSpace(response.Detail), "not found") {
			return true
		}
		body := strings.ToLower(strings.TrimSpace(se.Body))
		return body == "" || body == "not found" ||
			strings.Contains(body, "404 page not found")
	}
	return se.Status == http.StatusBadGateway &&
		strings.Contains(strings.ToLower(se.Body), "not implemented")
}
