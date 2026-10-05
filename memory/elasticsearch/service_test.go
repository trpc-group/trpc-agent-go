//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// -----------------------------------------------------------------------------
// Mock client
// -----------------------------------------------------------------------------

// mockClient is an in-memory istorage.Client with a small query evaluator
// for the subset of Elasticsearch DSL emitted by this package.
type mockClient struct {
	mu   sync.Mutex
	docs map[string]map[string]any

	indexExists     bool
	createIndexBody []byte

	// Error simulation.
	indexExistsErr   error
	createIndexErr   error
	searchErr        error
	getDocErr        error
	indexDocErr      error
	updateDocErr     error
	deleteDocErr     error
	deleteByQueryErr error
	refreshErr       error

	// searchErrFor fails only search requests whose body contains the
	// marker, letting tests fail one query leg but not the other.
	searchErrFor string
	// failSearchOnCall fails the n-th search call (1-based) across the
	// lifetime of the mock, zero means never.
	failSearchOnCall int
	searchCallCount  int
	// failGetDocOnCall fails the n-th get-document call (1-based), zero
	// means never.
	failGetDocOnCall int
	getDocCallCount  int

	// searchOverride replaces the default search evaluation, returning a
	// pre-baked raw response.
	searchOverride func(indexName string, body []byte) ([]byte, error)
}

func newMockClient() *mockClient {
	return &mockClient{
		docs:        make(map[string]map[string]any),
		indexExists: true,
	}
}

func (m *mockClient) Ping(ctx context.Context) error { return nil }

func (m *mockClient) CreateIndex(ctx context.Context, indexName string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createIndexErr != nil {
		return m.createIndexErr
	}
	if m.indexExists {
		return fmt.Errorf("index already exists")
	}
	m.indexExists = true
	m.createIndexBody = body
	return nil
}

func (m *mockClient) DeleteIndex(ctx context.Context, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexExists = false
	return nil
}

func (m *mockClient) IndexExists(ctx context.Context, indexName string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.indexExistsErr != nil {
		return false, m.indexExistsErr
	}
	return m.indexExists, nil
}

func (m *mockClient) IndexDoc(ctx context.Context, indexName, id string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.indexDocErr != nil {
		return m.indexDocErr
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	m.docs[id] = doc
	return nil
}

func (m *mockClient) GetDoc(ctx context.Context, indexName, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getDocCallCount++
	if m.getDocErr != nil {
		return nil, m.getDocErr
	}
	if m.failGetDocOnCall > 0 && m.getDocCallCount == m.failGetDocOnCall {
		return nil, fmt.Errorf("elasticsearch get document failed: 500: simulated")
	}
	doc, ok := m.docs[id]
	if !ok {
		return nil, fmt.Errorf("elasticsearch get document failed: 404: not found")
	}
	return json.Marshal(map[string]any{
		"found":   true,
		"_id":     id,
		"_source": doc,
	})
}

func (m *mockClient) UpdateDoc(ctx context.Context, indexName, id string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateDocErr != nil {
		return m.updateDocErr
	}
	doc, ok := m.docs[id]
	if !ok {
		return fmt.Errorf("elasticsearch update document failed: 404: not found")
	}
	var patch struct {
		Doc map[string]any `json:"doc"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		return err
	}
	for k, v := range patch.Doc {
		doc[k] = v
	}
	return nil
}

func (m *mockClient) DeleteDoc(ctx context.Context, indexName, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteDocErr != nil {
		return m.deleteDocErr
	}
	if _, ok := m.docs[id]; !ok {
		return fmt.Errorf("elasticsearch delete document failed: 404: not found")
	}
	delete(m.docs, id)
	return nil
}

func (m *mockClient) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCallCount++
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if m.failSearchOnCall > 0 && m.searchCallCount == m.failSearchOnCall {
		return nil, fmt.Errorf("elasticsearch search failed on call %d", m.searchCallCount)
	}
	if m.searchErrFor != "" && strings.Contains(string(body), m.searchErrFor) {
		return nil, fmt.Errorf("elasticsearch search failed for marker %s", m.searchErrFor)
	}
	if m.searchOverride != nil {
		return m.searchOverride(indexName, body)
	}
	return m.evaluateSearch(body)
}

func (m *mockClient) Count(ctx context.Context, indexName string, body []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.docs), nil
}

func (m *mockClient) DeleteByQuery(ctx context.Context, indexName string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteByQueryErr != nil {
		return m.deleteByQueryErr
	}
	var req searchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	for id, doc := range m.docs {
		if match, _ := evalQuery(req.Query, doc); match {
			delete(m.docs, id)
		}
	}
	return nil
}

func (m *mockClient) Refresh(ctx context.Context, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refreshErr != nil {
		return m.refreshErr
	}
	return nil
}

// mockSearchRequest is the decoded search request for the mock evaluator.
type mockSearchRequest struct {
	Query map[string]any   `json:"query"`
	Size  int              `json:"size"`
	Sort  []map[string]any `json:"sort"`
}

type mockHit struct {
	id    string
	doc   map[string]any
	score float64
}

// evaluateSearch evaluates the stored documents against the request.
func (m *mockClient) evaluateSearch(body []byte) ([]byte, error) {
	var req mockSearchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	hits := make([]mockHit, 0, len(m.docs))
	for id, doc := range m.docs {
		match, score := evalQuery(req.Query, doc)
		if match {
			hits = append(hits, mockHit{id: id, doc: doc, score: score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if cmp := compareBySortClauses(hits[i].doc, hits[j].doc, req.Sort); cmp != 0 {
			return cmp > 0
		}
		// Deterministic tie-break for score and sort ties.
		return hits[i].id < hits[j].id
	})

	if req.Size > 0 && len(hits) > req.Size {
		hits = hits[:req.Size]
	}

	response := map[string]any{
		"hits": map[string]any{
			"hits": func() []map[string]any {
				out := make([]map[string]any, 0, len(hits))
				for _, h := range hits {
					out = append(out, map[string]any{
						"_id":     h.id,
						"_score":  h.score,
						"_source": h.doc,
					})
				}
				return out
			}(),
		},
	}
	return json.Marshal(response)
}

// compareBySortClauses compares two documents by the request sort clauses.
// It returns a positive value when a ranks higher than b under the clauses.
func compareBySortClauses(a, b map[string]any, clauses []map[string]any) int {
	for _, clause := range clauses {
		for field, orderSpec := range clauses2Order(clause) {
			cmp := compareSortValues(a[field], b[field])
			if cmp == 0 {
				continue
			}
			if isDescOrder(orderSpec) {
				return cmp
			}
			return -cmp
		}
	}
	return 0
}

// clauses2Order normalizes the sort clause form {field: {order: desc}}.
func clauses2Order(clause map[string]any) map[string]any {
	for field, spec := range clause {
		if m, ok := spec.(map[string]any); ok {
			return map[string]any{field: m["order"]}
		}
		return map[string]any{field: spec}
	}
	return nil
}

func isDescOrder(spec any) bool {
	s, _ := spec.(string)
	return s == "desc"
}

// compareSortValues compares two raw JSON values. Time-like strings are
// compared chronologically, everything else lexicographically or numerically.
func compareSortValues(a, b any) int {
	if a == nil || b == nil {
		if a == b {
			return 0
		}
		if a == nil {
			return 1
		}
		return -1
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		at, aerr := time.Parse(time.RFC3339Nano, as)
		bt, berr := time.Parse(time.RFC3339Nano, bs)
		if aerr == nil && berr == nil {
			return at.Compare(bt)
		}
		return strings.Compare(as, bs)
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}
	return 0
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// evalQuery evaluates a query against a document. It returns whether the
// document matches and, when applicable, its relevance score.
func evalQuery(query map[string]any, doc map[string]any) (bool, float64) {
	for kind, spec := range query {
		switch kind {
		case "bool":
			return evalBool(spec, doc)
		case "term":
			return evalTerm(spec, doc), 0
		case "exists":
			return evalExists(spec, doc), 0
		case "range":
			return evalRange(spec, doc), 0
		case "script_score":
			return evalScriptScore(spec, doc)
		case "multi_match":
			return evalMultiMatch(spec, doc)
		default:
			return false, 0
		}
	}
	return false, 0
}

func evalBool(spec any, doc map[string]any) (bool, float64) {
	body, ok := spec.(map[string]any)
	if !ok {
		return false, 0
	}
	score := 0.0
	for _, clause := range asQueryList(body["filter"]) {
		match, s := evalQuery(clause, doc)
		if !match {
			return false, 0
		}
		score = math.Max(score, s)
	}
	for _, clause := range asQueryList(body["must"]) {
		match, s := evalQuery(clause, doc)
		if !match {
			return false, 0
		}
		score = math.Max(score, s)
	}
	for _, clause := range asQueryList(body["must_not"]) {
		match, _ := evalQuery(clause, doc)
		if match {
			return false, 0
		}
	}
	if should := asQueryList(body["should"]); len(should) > 0 {
		minimum := 1
		if n, ok := toFloat(body["minimum_should_match"]); ok {
			minimum = int(n)
		}
		matched := 0
		for _, clause := range should {
			match, s := evalQuery(clause, doc)
			if match {
				matched++
				score = math.Max(score, s)
			}
		}
		if matched < minimum {
			return false, 0
		}
	}
	return true, score
}

func asQuery(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asQueryList(v any) []map[string]any {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func evalTerm(spec any, doc map[string]any) bool {
	body, ok := spec.(map[string]any)
	if !ok {
		return false
	}
	for field, value := range body {
		docValue, present := doc[field]
		if !present {
			return false
		}
		switch expected := value.(type) {
		case string:
			if s, ok := docValue.(string); !ok || s != expected {
				return false
			}
		case []any:
			found := false
			if arr, ok := docValue.([]any); ok {
				for _, item := range arr {
					if item == valueOf(expected) {
						found = true
						break
					}
				}
			}
			if !found {
				return false
			}
		default:
			return docValue == value
		}
	}
	return true
}

func valueOf(items []any) any {
	if len(items) == 1 {
		return items[0]
	}
	return nil
}

func evalExists(spec any, doc map[string]any) bool {
	body, ok := spec.(map[string]any)
	if !ok {
		return false
	}
	field, ok := body["field"].(string)
	if !ok {
		return false
	}
	value, present := doc[field]
	if !present || value == nil {
		return false
	}
	return true
}

func evalRange(spec any, doc map[string]any) bool {
	body, ok := spec.(map[string]any)
	if !ok {
		return false
	}
	for field, rangeSpec := range body {
		r, ok := rangeSpec.(map[string]any)
		if !ok {
			return false
		}
		docValue, present := doc[field]
		if !present {
			return false
		}
		docTime, err := parseTimeValue(docValue)
		if err != nil {
			return false
		}
		if gte, ok := r["gte"]; ok {
			bound, err := parseTimeValue(gte)
			if err != nil || docTime.Before(bound) {
				return false
			}
		}
		if lte, ok := r["lte"]; ok {
			bound, err := parseTimeValue(lte)
			if err != nil || docTime.After(bound) {
				return false
			}
		}
	}
	return true
}

func parseTimeValue(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("not a time string")
	}
	return time.Parse(time.RFC3339Nano, s)
}

func evalScriptScore(spec any, doc map[string]any) (bool, float64) {
	body, ok := spec.(map[string]any)
	if !ok {
		return false, 0
	}
	if match, _ := evalQuery(asQuery(body["query"]), doc); !match {
		return false, 0
	}
	script, ok := body["script"].(map[string]any)
	if !ok {
		return false, 0
	}
	params, _ := script["params"].(map[string]any)
	rawVector, _ := params["query_vector"].([]any)
	queryVector := make([]float64, 0, len(rawVector))
	for _, v := range rawVector {
		f, _ := toFloat(v)
		queryVector = append(queryVector, f)
	}
	docVector := make([]float64, 0, len(rawVector))
	if raw, ok := doc[fieldEmbedding].([]any); ok {
		for _, v := range raw {
			f, _ := toFloat(v)
			docVector = append(docVector, f)
		}
	}
	if len(docVector) == 0 || len(docVector) != len(queryVector) {
		return true, 0.0
	}
	return true, (cosineSimilarity(queryVector, docVector) + 1.0) / 2.0
}

func evalMultiMatch(spec any, doc map[string]any) (bool, float64) {
	body, ok := spec.(map[string]any)
	if !ok {
		return false, 0
	}
	queryText, _ := body["query"].(string)
	content, _ := doc[fieldContent].(string)
	score := 0.0
	for _, term := range strings.Fields(strings.ToLower(queryText)) {
		if strings.Contains(strings.ToLower(content), term) {
			score++
		}
	}
	return score > 0, score
}

func cosineSimilarity(a, b []float64) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// -----------------------------------------------------------------------------
// Stub embedder
// -----------------------------------------------------------------------------

type stubEmbedder struct {
	mu      sync.Mutex
	dim     int
	vectors map[string][]float64
	err     error
}

func newStubEmbedder(dim int) *stubEmbedder {
	return &stubEmbedder{dim: dim, vectors: make(map[string][]float64)}
}

func (e *stubEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	if v, ok := e.vectors[text]; ok {
		return v, nil
	}
	v := make([]float64, e.dim)
	v[0] = 1
	return v, nil
}

func (e *stubEmbedder) GetEmbeddingWithUsage(
	ctx context.Context, text string,
) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, nil, err
}

func (e *stubEmbedder) GetDimensions() int {
	return e.dim
}

var _ embedder.Embedder = (*stubEmbedder)(nil)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

const testDimension = 4

func newTestService(t *testing.T, mc *mockClient, opts ...ServiceOpt) *Service {
	t.Helper()
	base := []ServiceOpt{
		WithIndexDimension(testDimension),
		WithEmbedder(newStubEmbedder(testDimension)),
		withClient(mc),
	}
	svc, err := NewService(append(base, opts...)...)
	require.NoError(t, err)
	return svc
}

func testUserKey(app, user string) memory.UserKey {
	return memory.UserKey{AppName: app, UserID: user}
}

// -----------------------------------------------------------------------------
// Construction tests
// -----------------------------------------------------------------------------

func TestNewServiceRequiresEmbedder(t *testing.T) {
	mc := newMockClient()
	_, err := NewService(withClient(mc))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")
}

func TestNewServiceCreatesIndex(t *testing.T) {
	mc := newMockClient()
	mc.indexExists = false
	_ = newTestService(t, mc)

	require.NotNil(t, mc.createIndexBody)
	var body struct {
		Mappings struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	require.NoError(t, json.Unmarshal(mc.createIndexBody, &body))

	embedding := body.Mappings.Properties[fieldEmbedding]
	assert.Equal(t, "dense_vector", embedding["type"])
	dims, ok := embedding["dims"].(float64)
	require.True(t, ok)
	assert.Equal(t, float64(testDimension), dims)
	// Default version v9 emits the indexed vector parameters.
	assert.Equal(t, true, embedding["index"])
	assert.Equal(t, "cosine", embedding["similarity"])
	for _, field := range []string{fieldMemoryID, fieldAppName, fieldUserID, fieldContent, fieldKind} {
		assert.Contains(t, body.Mappings.Properties, field)
	}
}

func TestNewServiceCreatesV7CompatibleIndex(t *testing.T) {
	mc := newMockClient()
	mc.indexExists = false
	_ = newTestService(t, mc, WithVersion("v7"))

	var body struct {
		Mappings struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	require.NoError(t, json.Unmarshal(mc.createIndexBody, &body))

	embedding := body.Mappings.Properties[fieldEmbedding]
	assert.Equal(t, "dense_vector", embedding["type"])
	// Elasticsearch 7 rejects the index and similarity mapping parameters.
	_, hasIndex := embedding["index"]
	assert.False(t, hasIndex)
	_, hasSimilarity := embedding["similarity"]
	assert.False(t, hasSimilarity)
}

func TestNewServiceSkipsIndexInit(t *testing.T) {
	mc := newMockClient()
	mc.indexExists = false
	mc.indexExistsErr = assert.AnError
	_ = newTestService(t, mc, WithSkipIndexInit(true))
}

func TestNewServiceIndexInitError(t *testing.T) {
	mc := newMockClient()
	mc.indexExists = false
	mc.createIndexErr = assert.AnError
	_, err := NewService(
		WithIndexDimension(testDimension),
		WithEmbedder(newStubEmbedder(testDimension)),
		withClient(mc),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init index failed")
}

// -----------------------------------------------------------------------------
// AddMemory tests
// -----------------------------------------------------------------------------

func TestAddMemoryStoresDocument(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	userKey := testUserKey("app", "user")
	require.NoError(t, svc.AddMemory(context.Background(), userKey, "user likes coffee", []string{"food"}))

	require.Len(t, mc.docs, 1)
	for _, doc := range mc.docs {
		assert.Equal(t, "user likes coffee", doc[fieldContent])
		assert.Equal(t, "app", doc[fieldAppName])
		assert.Equal(t, "user", doc[fieldUserID])
		assert.Equal(t, string(memory.KindFact), doc[fieldKind])
		assert.Equal(t, []any{"food"}, doc[fieldTopics])
		assert.NotContains(t, doc, fieldDeletedAt)
	}
}

func TestAddMemoryIdempotent(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	userKey := testUserKey("app", "user")
	require.NoError(t, svc.AddMemory(context.Background(), userKey, "user likes coffee", nil))
	first := mockDocCreatedAt(t, mc)

	require.NoError(t, svc.AddMemory(context.Background(), userKey, "user likes coffee", nil))
	assert.Len(t, mc.docs, 1)
	assert.Equal(t, first, mockDocCreatedAt(t, mc), "re-add must preserve created_at")
}

func TestAddMemoryReactivatesTombstone(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSoftDelete(true))

	userKey := testUserKey("app", "user")
	require.NoError(t, svc.AddMemory(context.Background(), userKey, "user likes coffee", nil))
	created := mockDocCreatedAt(t, mc)

	require.NoError(t, svc.DeleteMemory(context.Background(), memory.Key{
		AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
	}))
	require.Contains(t, mockOnlyDoc(t, mc), fieldDeletedAt)

	require.NoError(t, svc.AddMemory(context.Background(), userKey, "user likes coffee", nil))
	doc := mockOnlyDoc(t, mc)
	assert.NotContains(t, doc, fieldDeletedAt, "re-add must reactivate the tombstone")
	assert.Equal(t, created, doc[fieldCreatedAt], "reactivation must preserve created_at")
}

func TestAddMemoryScopeIsolation(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user-a"), "unique content a", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user-b"), "unique content a", nil))

	assert.Len(t, mc.docs, 2, "same content in different scopes must not collide")
}

func TestAddMemoryDimensionMismatch(t *testing.T) {
	ed := newStubEmbedder(testDimension)
	ed.vectors["mismatched"] = make([]float64, testDimension+1)
	mc := newMockClient()
	svc := newTestService(t, mc, WithEmbedder(ed))

	err := svc.AddMemory(context.Background(), testUserKey("app", "user"), "mismatched", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding dimension mismatch")
}

func TestAddMemoryEmbedderError(t *testing.T) {
	ed := newStubEmbedder(testDimension)
	ed.err = assert.AnError
	mc := newMockClient()
	svc := newTestService(t, mc, WithEmbedder(ed))

	err := svc.AddMemory(context.Background(), testUserKey("app", "user"), "anything", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate embedding failed")
}

func TestAddMemoryInvalidKey(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "app"}, "content", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrUserIDRequired)
}

// -----------------------------------------------------------------------------
// UpdateMemory tests
// -----------------------------------------------------------------------------

func TestUpdateMemoryInPlace(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	userKey := testUserKey("app", "user")
	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, userKey, "user likes coffee", nil))
	memoryID := mockOnlyMemoryID(t, mc)

	// Topics are excluded from the canonical identity, so the ID is stable.
	var result memory.UpdateResult
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: memoryID,
	}, "user likes coffee", []string{"food"}, memory.WithUpdateResult(&result)))

	assert.Equal(t, memoryID, result.MemoryID)
	require.Len(t, mc.docs, 1)
	assert.Equal(t, []any{"food"}, mockOnlyDoc(t, mc)[fieldTopics])
}

func TestUpdateMemoryRotatesID(t *testing.T) {
	for _, softDelete := range []bool{false, true} {
		t.Run(fmt.Sprintf("softDelete=%v", softDelete), func(t *testing.T) {
			mc := newMockClient()
			svc := newTestService(t, mc, WithSoftDelete(softDelete))

			userKey := testUserKey("app", "user")
			ctx := context.Background()
			require.NoError(t, svc.AddMemory(ctx, userKey, "old content", nil))
			oldID := mockOnlyMemoryID(t, mc)

			var result memory.UpdateResult
			require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
				AppName: "app", UserID: "user", MemoryID: oldID,
			}, "new content", nil, memory.WithUpdateResult(&result)))

			assert.NotEqual(t, oldID, result.MemoryID)
			target := mockDoc(t, mc, result.MemoryID)
			assert.Equal(t, "new content", target[fieldContent])
			assert.NotContains(t, target, fieldDeletedAt, "the rotation target is active")
			if softDelete {
				assert.Contains(t, mockDoc(t, mc, oldID), fieldDeletedAt,
					"rotation in soft-delete mode retains the source tombstone")
			} else {
				_, sourceLeft := mc.docs[oldID]
				assert.False(t, sourceLeft, "hard rotation deletes the source document")
			}
		})
	}
}

func TestUpdateMemoryTargetConflict(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	userKey := testUserKey("app", "user")
	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, userKey, "content one", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "content two", nil))

	var twoID string
	for id, doc := range mc.docs {
		if doc[fieldContent] == "content two" {
			twoID = id
		}
	}
	require.NotEmpty(t, twoID)

	// Updating "content two" to "content one" collides with an active target.
	err := svc.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: twoID,
	}, "content one", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestUpdateMemoryRevivesTombstonedTarget(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSoftDelete(true))

	userKey := testUserKey("app", "user")
	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, userKey, "content one", nil))
	oneID := mockOnlyMemoryID(t, mc)
	oneCreated := mockDocCreatedAt(t, mc)
	require.NoError(t, svc.AddMemory(ctx, userKey, "content two", nil))
	twoID := mockOnlyMemoryID(t, mc, oneID)

	// Soft-delete "content one", then rotate "content two" onto it.
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: oneID,
	}))
	var result memory.UpdateResult
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: twoID,
	}, "content one", nil, memory.WithUpdateResult(&result)))

	assert.Equal(t, oneID, result.MemoryID)
	assert.NotContains(t, mockDoc(t, mc, oneID), fieldDeletedAt)
	assert.Equal(t, oneCreated, mockDoc(t, mc, oneID)[fieldCreatedAt],
		"revived target keeps its original created_at")
	assert.Contains(t, mockDoc(t, mc, twoID), fieldDeletedAt,
		"soft rotation tombstones the source document")
}

func TestUpdateMemoryNotFound(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	err := svc.UpdateMemory(context.Background(), memory.Key{
		AppName: "app", UserID: "user", MemoryID: "missing",
	}, "content", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// -----------------------------------------------------------------------------
// DeleteMemory tests
// -----------------------------------------------------------------------------

func TestDeleteMemoryHard(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	userKey := testUserKey("app", "user")
	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, userKey, "user likes coffee", nil))
	memoryID := mockOnlyMemoryID(t, mc)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: memoryID,
	}))
	assert.Empty(t, mc.docs)

	// Deleting an unknown memory is a no-op.
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: "missing",
	}))
}

func TestDeleteMemorySoft(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSoftDelete(true))

	userKey := testUserKey("app", "user")
	ctx := context.Background()
	require.NoError(t, svc.AddMemory(ctx, userKey, "user likes coffee", nil))
	memoryID := mockOnlyMemoryID(t, mc)

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: memoryID,
	}))
	require.Len(t, mc.docs, 1)
	assert.Contains(t, mockOnlyDoc(t, mc), fieldDeletedAt)

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries, "tombstoned entries must not be read")
}

// -----------------------------------------------------------------------------
// ClearMemories tests
// -----------------------------------------------------------------------------

func TestClearMemoriesHard(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	ctx := context.Background()
	userA := testUserKey("app", "user-a")
	require.NoError(t, svc.AddMemory(ctx, userA, "memory a1", nil))
	require.NoError(t, svc.AddMemory(ctx, userA, "memory a2", nil))
	userB := testUserKey("app", "user-b")
	require.NoError(t, svc.AddMemory(ctx, userB, "memory b1", nil))

	require.NoError(t, svc.ClearMemories(ctx, userA))

	entries, err := svc.ReadMemories(ctx, userA, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
	entries, err = svc.ReadMemories(ctx, userB, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "other scopes must stay untouched")
}

func TestClearMemoriesSoft(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSoftDelete(true))

	ctx := context.Background()
	userA := testUserKey("app", "user-a")
	require.NoError(t, svc.AddMemory(ctx, userA, "memory a1", nil))
	require.NoError(t, svc.AddMemory(ctx, userA, "memory a2", nil))

	require.NoError(t, svc.ClearMemories(ctx, userA))

	for id := range mc.docs {
		assert.Contains(t, mc.docs[id], fieldDeletedAt)
	}
	entries, err := svc.ReadMemories(ctx, userA, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// -----------------------------------------------------------------------------
// ReadMemories tests
// -----------------------------------------------------------------------------

func TestReadMemoriesOrderingAndLimit(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	injectDoc(t, mc, "id-1", "app", "user", "content 1", base)
	injectDoc(t, mc, "id-2", "app", "user", "content 2", base.Add(time.Second))
	injectDoc(t, mc, "id-3", "app", "user", "content 3", base)
	injectDoc(t, mc, "id-x", "app", "other-user", "content x", base.Add(2*time.Second))

	entries, err := svc.ReadMemories(context.Background(), testUserKey("app", "user"), 0)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	// updated_at desc, then memory_id desc.
	assert.Equal(t, []string{"id-2", "id-3", "id-1"},
		[]string{entries[0].ID, entries[1].ID, entries[2].ID})

	entries, err = svc.ReadMemories(context.Background(), testUserKey("app", "user"), 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "id-2", entries[0].ID)
}

func TestReadMemoriesInvalidKey(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{}, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrAppNameRequired)
}

// -----------------------------------------------------------------------------
// SearchMemories tests
// -----------------------------------------------------------------------------

func TestSearchScopeIsolation(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}
	ed.vectors["target content"] = []float64{1, 0, 0, 0}

	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user-a"), "target content", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user-b"), "unrelated", nil))

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user-a"), "query")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "target content", results[0].Memory.Memory)
	assert.Equal(t, "user-a", results[0].UserID)
}

func TestSearchEmptyQuery(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	results, err := svc.SearchMemories(context.Background(), testUserKey("app", "user"), "   ")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchDenseScoresAndThreshold(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0.9))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}
	ed.vectors["aligned memory"] = []float64{0.98, 0.1, 0, 0}
	ed.vectors["orthogonal memory"] = []float64{0, 1, 0, 0}
	ed.vectors["opposite memory"] = []float64{-1, 0, 0, 0}

	for _, content := range []string{"aligned memory", "orthogonal memory", "opposite memory"} {
		require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), content, nil))
	}

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "query")
	require.NoError(t, err)
	require.Len(t, results, 1, "only results above the 0.9 threshold survive")
	assert.Equal(t, "aligned memory", results[0].Memory.Memory)
	assert.Greater(t, results[0].Score, 0.9)
	assert.LessOrEqual(t, results[0].Score, 1.0)
}

func TestSearchKindFilterAndFallback(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}
	for _, content := range []string{"fact one", "fact two", "fact three", "episode one"} {
		ed.vectors[content] = []float64{1, 0, 0, 0}
	}

	eventTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "fact one", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "fact two", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "fact three", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "episode one", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &eventTime})))

	// Without fallback only the episode matches the kind filter.
	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "query",
		memory.WithSearchOptions(memory.SearchOptions{Query: "query", Kind: memory.KindEpisode}))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, memory.KindEpisode, results[0].Memory.Kind)

	// With fallback the facts are merged back behind the requested kind.
	results, err = svc.SearchMemories(ctx, testUserKey("app", "user"), "query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "query", Kind: memory.KindEpisode, KindFallback: true,
		}))
	require.NoError(t, err)
	require.Len(t, results, 4)
	assert.Equal(t, memory.KindEpisode, results[0].Memory.Kind)
}

func TestSearchTimeRange(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}

	early := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "early episode", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &early})))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "late episode", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &late})))
	// Facts have no event_time and always satisfy a time range filter.
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "plain fact", nil))

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:     "query",
			TimeAfter: &cutoff,
		}))
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.ElementsMatch(t,
		[]string{"late episode", "plain fact"},
		[]string{results[0].Memory.Memory, results[1].Memory.Memory})
}

func TestSearchHybridRRF(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0.9))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["alice"] = []float64{1, 0, 0, 0}
	ed.vectors["alice works at acme"] = []float64{1, 0, 0, 0}
	// Orthogonal embedding: below the dense threshold but merged back by
	// the hybrid path, so the surviving result proves hybrid skips the
	// threshold.
	ed.vectors["bob likes banana"] = []float64{0, 1, 0, 0}

	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "alice works at acme", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "bob likes banana", nil))

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "alice",
		memory.WithSearchOptions(memory.SearchOptions{Query: "alice", HybridSearch: true}))
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "alice works at acme", results[0].Memory.Memory)
	assert.Less(t, results[0].Score, 0.05, "RRF scores are small rank-based values")
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestSearchMaxResultsOverride(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	for _, content := range []string{"m1", "m2", "m3", "m4", "m5"} {
		require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), content, nil))
	}

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "anything",
		memory.WithSearchOptions(memory.SearchOptions{Query: "anything", MaxResults: 2}))
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSearchDeduplicate(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}

	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"),
		"user likes coffee very much", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"),
		"user likes coffee very much today", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"),
		"user enjoys tea", nil))

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "query",
		memory.WithSearchOptions(memory.SearchOptions{Query: "query", Deduplicate: true}))
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestSearchDimensionMismatch(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = make([]float64, testDimension+1)

	_, err := svc.SearchMemories(context.Background(), testUserKey("app", "user"), "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query embedding dimension mismatch")
}

func TestSearchPrebakedResponse(t *testing.T) {
	mc := newMockClient()
	mc.searchOverride = func(indexName string, body []byte) ([]byte, error) {
		source := func(id string) map[string]any {
			return map[string]any{
				fieldMemoryID:  id,
				fieldAppName:   "app",
				fieldUserID:    "user",
				fieldContent:   "content of " + id,
				fieldKind:      string(memory.KindFact),
				fieldCreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
				fieldUpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
			}
		}
		return json.Marshal(map[string]any{
			"hits": map[string]any{
				"hits": []map[string]any{
					{"_id": "a", "_score": 0.95, "_source": source("a")},
					{"_id": "b", "_score": 0.10, "_source": source("b")},
					{"_id": "c", "_source": source("c")}, // malformed: no score
					{"_id": "d", "_score": 0.99},         // malformed: no source
					{"_id": "e", "_score": 0.99, "_source": map[string]any{
						fieldAppName: "app", // malformed: no memory_id
					}},
				},
			},
		})
	}
	svc := newTestService(t, mc, WithSimilarityThreshold(0.5), WithSkipIndexInit(true))

	results, err := svc.SearchMemories(context.Background(), testUserKey("app", "user"), "query")
	require.NoError(t, err)
	require.Len(t, results, 1, "only the hit above the threshold survives")
	assert.Equal(t, "a", results[0].ID)
	assert.InDelta(t, 0.95, results[0].Score, 1e-9)
}

func TestSearchErrorPropagation(t *testing.T) {
	mc := newMockClient()
	mc.searchErr = assert.AnError
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	_, err := svc.SearchMemories(context.Background(), testUserKey("app", "user"), "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search documents failed")
}

// -----------------------------------------------------------------------------
// Query builder tests (guard the mock evaluator against shared blind spots)
// -----------------------------------------------------------------------------

func TestBuildDenseSearchRequestStructure(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	req := svc.buildDenseSearchRequest(
		testUserKey("app", "user"),
		memory.SearchOptions{Kind: memory.KindEpisode},
		[]float32{1, 2, 3, 4},
		7,
	)
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded struct {
		Query map[string]map[string]any `json:"query"`
		Size  int                       `json:"size"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, 7, decoded.Size)

	scriptScore := decoded.Query["script_score"]
	require.NotNil(t, scriptScore)
	script, ok := scriptScore["script"].(map[string]any)
	require.True(t, ok)
	source, ok := script["source"].(string)
	require.True(t, ok)
	assert.Contains(t, source, "cosineSimilarity(params.query_vector, 'embedding')")
	assert.Contains(t, source, "/ 2.0")

	innerQuery, ok := scriptScore["query"].(map[string]any)
	require.True(t, ok)
	innerData, err := json.Marshal(innerQuery)
	require.NoError(t, err)
	assert.Contains(t, string(innerData), `"app_name"`)
	assert.Contains(t, string(innerData), `"user"`)
	assert.Contains(t, string(innerData), `"episode"`)
}

func TestBuildReadRequestSortsDeterministically(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	req := svc.buildReadRequest(testUserKey("app", "user"), 5)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"updated_at"`)
	assert.Contains(t, string(data), `"memory_id"`)
	assert.Contains(t, string(data), `"desc"`)
}

// -----------------------------------------------------------------------------
// Tools and lifecycle tests
// -----------------------------------------------------------------------------

func TestToolsExposed(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	tools := svc.Tools()
	assert.NotEmpty(t, tools)
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Declaration().Name] = true
	}
	assert.Contains(t, names, memory.SearchToolName)
}

func TestEnqueueAutoMemoryJobWithoutWorker(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	err := svc.EnqueueAutoMemoryJob(context.Background(), nil)
	assert.NoError(t, err)
}

func TestClose(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)
	assert.NoError(t, svc.Close())
}

// -----------------------------------------------------------------------------
// Construction without an injected client
// -----------------------------------------------------------------------------

func TestNewServiceCreateIndexFailure(t *testing.T) {
	// A controlled endpoint answers 400 so index creation fails
	// deterministically; the SDK client build and wrap still succeed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	_, err := NewService(
		WithIndexDimension(testDimension),
		WithEmbedder(newStubEmbedder(testDimension)),
		WithAddresses([]string{server.URL}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init index failed")
}

func TestNewServiceBuildsClientWithSkipInit(t *testing.T) {
	svc, err := NewService(
		WithIndexDimension(testDimension),
		WithEmbedder(newStubEmbedder(testDimension)),
		WithAddresses([]string{"http://127.0.0.1:1"}),
		WithSkipIndexInit(true),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, svc.Tools())
	assert.NoError(t, svc.Close())
}

// -----------------------------------------------------------------------------
// Auto memory worker construction
// -----------------------------------------------------------------------------

// stubExtractor is a minimal extractor used to exercise the auto memory
// worker wiring.
type stubExtractor struct{}

func (stubExtractor) Extract(ctx context.Context, messages []model.Message,
	existing []*memory.Entry) ([]*extractor.Operation, error) {
	return nil, nil
}

func (stubExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool { return true }

func (stubExtractor) SetPrompt(prompt string) {}

func (stubExtractor) SetModel(m model.Model) {}

func (stubExtractor) Metadata() map[string]any { return nil }

var _ extractor.MemoryExtractor = (*stubExtractor)(nil)

func TestNewServiceWithExtractorStartsWorker(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithExtractor(stubExtractor{}))
	require.NotNil(t, svc.autoMemoryWorker)
	assert.NoError(t, svc.Close())
}

// -----------------------------------------------------------------------------
// Storage error propagation
// -----------------------------------------------------------------------------

func TestAddMemoryIndexDocError(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	mc.indexDocErr = assert.AnError
	err := svc.AddMemory(context.Background(), testUserKey("app", "user"), "content", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index memory document failed")
}

func TestUpdateMemoryErrorPaths(t *testing.T) {
	t.Run("load fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)
		mc.getDocErr = assert.AnError

		err := svc.UpdateMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: "any",
		}, "content", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load memory entry failed")
	})

	t.Run("in-place update fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.indexDocErr = assert.AnError

		err := svc.UpdateMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
		}, "content", []string{"topic"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update memory entry failed")
	})

	t.Run("rotate target lookup fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "old", nil))
		mc.failGetDocOnCall = 3 // add consumes one, source lookup two, target lookup three

		err := svc.UpdateMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
		}, "new content", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "check rotated memory target failed")
	})

	t.Run("rotate target insert fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "old", nil))
		mc.indexDocErr = assert.AnError

		err := svc.UpdateMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
		}, "new content", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rotate memory entry failed")
	})
}

func TestDeleteMemoryErrorPaths(t *testing.T) {
	t.Run("lookup fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc, WithSoftDelete(true))
		mc.getDocErr = assert.AnError

		err := svc.DeleteMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: "any",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete memory entry failed")
	})

	t.Run("soft delete update fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc, WithSoftDelete(true))

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.updateDocErr = assert.AnError

		err := svc.DeleteMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete memory entry failed")
	})

	t.Run("hard delete fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.deleteDocErr = assert.AnError

		err := svc.DeleteMemory(context.Background(), memory.Key{
			AppName: "app", UserID: "user", MemoryID: mockOnlyMemoryID(t, mc),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete memory entry failed")
	})
}

func TestClearMemoriesErrorPaths(t *testing.T) {
	t.Run("hard delete by query fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.deleteByQueryErr = assert.AnError

		err := svc.ClearMemories(context.Background(), userKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "clear memories failed")
	})

	t.Run("soft delete lookup fails", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc, WithSoftDelete(true))

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.searchErr = assert.AnError

		err := svc.ClearMemories(context.Background(), userKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "clear memories failed")
	})

	t.Run("soft delete partial failure", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc, WithSoftDelete(true))

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.updateDocErr = assert.AnError

		err := svc.ClearMemories(context.Background(), userKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "clear memories failed")
	})
}

func TestClearMemoriesSoftMultipleBatches(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSoftDelete(true))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const total = 2*tombstoneBatchSize + 50
	for i := 0; i < total; i++ {
		injectDoc(t, mc, fmt.Sprintf("doc-%04d", i), "app", "user",
			fmt.Sprintf("content %d", i), base.Add(time.Duration(i)*time.Millisecond))
	}

	require.NoError(t, svc.ClearMemories(context.Background(), testUserKey("app", "user")))

	tombstoned := 0
	for _, doc := range mc.docs {
		if _, ok := doc[fieldDeletedAt]; ok {
			tombstoned++
		}
	}
	assert.Equal(t, total, tombstoned)

	entries, err := svc.ReadMemories(context.Background(), testUserKey("app", "user"), 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestFindDocScopeGuard verifies the defense-in-depth scope check on point
// lookups: a document fetched by ID but belonging to another scope is
// treated as absent.
func TestFindDocScopeGuard(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	// A document whose stored scope does not match the requested key.
	mc.docs["cross-scope-id"] = map[string]any{
		fieldMemoryID:  "cross-scope-id",
		fieldAppName:   "other-app",
		fieldUserID:    "user",
		fieldContent:   "content",
		fieldKind:      string(memory.KindFact),
		fieldEmbedding: []float64{1, 0, 0, 0},
	}

	// UpdateMemory must report not found instead of reactivating or
	// overwriting the foreign document.
	err := svc.UpdateMemory(context.Background(), memory.Key{
		AppName: "app", UserID: "user", MemoryID: "cross-scope-id",
	}, "new content", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Equal(t, "content", mc.docs["cross-scope-id"][fieldContent])
}

func TestClearMemoriesRefreshFailure(t *testing.T) {
	t.Run("hard delete refresh is best-effort", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc)

		userKey := testUserKey("app", "user")
		require.NoError(t, svc.AddMemory(context.Background(), userKey, "content", nil))
		mc.refreshErr = assert.AnError

		// A data-access role without the maintenance privilege cannot
		// refresh; the clear must still go through.
		require.NoError(t, svc.ClearMemories(context.Background(), userKey))
		assert.Empty(t, mc.docs)
	})

	t.Run("soft delete refresh fails between batches", func(t *testing.T) {
		mc := newMockClient()
		svc := newTestService(t, mc, WithSoftDelete(true))

		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < tombstoneBatchSize+1; i++ {
			injectDoc(t, mc, fmt.Sprintf("doc-%04d", i), "app", "user",
				fmt.Sprintf("content %d", i), base.Add(time.Duration(i)*time.Millisecond))
		}
		mc.refreshErr = assert.AnError

		err := svc.ClearMemories(context.Background(), testUserKey("app", "user"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refresh index failed")
		// The first batch is tombstoned before the refresh stops the loop.
		tombstoned := 0
		for _, doc := range mc.docs {
			if _, ok := doc[fieldDeletedAt]; ok {
				tombstoned++
			}
		}
		assert.Equal(t, tombstoneBatchSize, tombstoned)
	})
}

func TestReadMemoriesError(t *testing.T) {
	mc := newMockClient()
	mc.searchErr = assert.AnError
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	_, err := svc.ReadMemories(context.Background(), testUserKey("app", "user"), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list memories failed")
}

// -----------------------------------------------------------------------------
// Search edge paths
// -----------------------------------------------------------------------------

func TestSearchInvalidKey(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc)

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app"}, "query")
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrUserIDRequired)
}

func TestSearchTimeBefore(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["query"] = []float64{1, 0, 0, 0}

	early := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "early episode", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &early})))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "late episode", nil,
		memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &late})))

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:      "query",
			Kind:       memory.KindEpisode,
			TimeBefore: &cutoff,
		}))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "early episode", results[0].Memory.Memory)
}

func TestSearchKeywordLegFailureIsNotFatal(t *testing.T) {
	mc := newMockClient()
	mc.searchErrFor = "multi_match"
	svc := newTestService(t, mc, WithSimilarityThreshold(0.9))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["alice"] = []float64{1, 0, 0, 0}
	ed.vectors["alice works at acme"] = []float64{1, 0, 0, 0}
	ed.vectors["bob likes banana"] = []float64{0, 1, 0, 0}

	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "alice works at acme", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "bob likes banana", nil))

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "alice",
		memory.WithSearchOptions(memory.SearchOptions{Query: "alice", HybridSearch: true}))
	require.NoError(t, err)
	// Without the lexical leg only the dense results remain.
	require.Len(t, results, 2)
	assert.Equal(t, "alice works at acme", results[0].Memory.Memory)
	assert.Greater(t, results[0].Score, 0.9)
}

func TestSearchHybridCandidateLimit(t *testing.T) {
	mc := newMockClient()
	svc := newTestService(t, mc, WithSimilarityThreshold(0), WithHybridCandidateLimit(1))

	ctx := context.Background()
	ed := svc.opts.embedder.(*stubEmbedder)
	ed.vectors["alice"] = []float64{1, 0, 0, 0}
	ed.vectors["alice works at acme"] = []float64{1, 0, 0, 0}
	ed.vectors["bob likes banana"] = []float64{0, 1, 0, 0}

	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "alice works at acme", nil))
	require.NoError(t, svc.AddMemory(ctx, testUserKey("app", "user"), "bob likes banana", nil))

	results, err := svc.SearchMemories(ctx, testUserKey("app", "user"), "alice",
		memory.WithSearchOptions(memory.SearchOptions{Query: "alice", HybridSearch: true}))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "alice works at acme", results[0].Memory.Memory)
}

func TestSearchMalformedResponse(t *testing.T) {
	mc := newMockClient()
	mc.searchOverride = func(indexName string, body []byte) ([]byte, error) {
		return []byte("not-json"), nil
	}
	svc := newTestService(t, mc, WithSkipIndexInit(true))

	_, err := svc.SearchMemories(context.Background(), testUserKey("app", "user"), "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode search response failed")
}

// -----------------------------------------------------------------------------
// Mock helpers
// -----------------------------------------------------------------------------

func mockOnlyDoc(t *testing.T, mc *mockClient) map[string]any {
	t.Helper()
	mc.mu.Lock()
	defer mc.mu.Unlock()
	require.Len(t, mc.docs, 1)
	for _, doc := range mc.docs {
		return doc
	}
	return nil
}

func mockDoc(t *testing.T, mc *mockClient, id string) map[string]any {
	t.Helper()
	mc.mu.Lock()
	defer mc.mu.Unlock()
	doc, ok := mc.docs[id]
	require.True(t, ok, "document %s must exist", id)
	return doc
}

func mockOnlyMemoryID(t *testing.T, mc *mockClient, exclude ...string) string {
	t.Helper()
	mc.mu.Lock()
	defer mc.mu.Unlock()
	excluded := make(map[string]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}
	for id := range mc.docs {
		if _, ok := excluded[id]; !ok {
			return id
		}
	}
	t.Fatal("no memory document found")
	return ""
}

func mockDocCreatedAt(t *testing.T, mc *mockClient) any {
	t.Helper()
	return mockOnlyDoc(t, mc)[fieldCreatedAt]
}

// injectDoc stores a document directly in the mock, bypassing the service.
func injectDoc(
	t *testing.T,
	mc *mockClient,
	id, app, user, content string,
	updatedAt time.Time,
) {
	t.Helper()
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.docs[id] = map[string]any{
		fieldMemoryID:  id,
		fieldAppName:   app,
		fieldUserID:    user,
		fieldContent:   content,
		fieldKind:      string(memory.KindFact),
		fieldEmbedding: []float64{1, 0, 0, 0},
		fieldCreatedAt: updatedAt.Format(time.RFC3339Nano),
		fieldUpdatedAt: updatedAt.Format(time.RFC3339Nano),
	}
}
