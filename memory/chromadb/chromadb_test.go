//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type blockingExtractor struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockingExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*extractor.Operation, error) {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return []*extractor.Operation{{
		Type: extractor.OperationAdd, Memory: "persist before close",
	}}, nil
}

func (e *blockingExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool {
	return true
}

func (e *blockingExtractor) SetPrompt(_ string) {}

func (e *blockingExtractor) SetModel(_ model.Model) {}

func (e *blockingExtractor) Metadata() map[string]any { return nil }

func TestValidateCollectionMetric(t *testing.T) {
	cosine := &vectorIndexConfig{Space: "cosine"}
	l2 := &vectorIndexConfig{Space: "l2"}
	tests := []struct {
		name          string
		configuration collectionConfiguration
		wantError     string
	}{
		{name: "HNSW cosine", configuration: collectionConfiguration{HNSW: cosine}},
		{name: "SPANN cosine", configuration: collectionConfiguration{SPANN: cosine}},
		{
			name:          "multiple indexes",
			configuration: collectionConfiguration{HNSW: cosine, SPANN: cosine},
			wantError:     "multiple active",
		},
		{name: "no index", configuration: collectionConfiguration{}, wantError: "no active"},
		{name: "L2", configuration: collectionConfiguration{HNSW: l2}, wantError: "must be cosine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCollectionMetric(tt.configuration)
			if tt.wantError == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestNewService_ValidatesCollectionDimensionAndMarker(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeChroma)
		wantError string
	}{
		{
			name: "dimension mismatch",
			configure: func(fake *fakeChroma) {
				dimension := 4
				fake.dimension = &dimension
			},
			wantError: "dimension mismatch",
		},
		{
			name: "backend marker conflict",
			configure: func(fake *fakeChroma) {
				fake.metadata["trpc_backend"] = "another-backend"
			},
			wantError: "different backend",
		},
		{
			name: "schema marker conflict",
			configure: func(fake *fakeChroma) {
				fake.metadata[metadataSchemaVersionKey] = 2
			},
			wantError: "unsupported",
		},
		{
			name:      "unmarked collection is allowed",
			configure: func(_ *fakeChroma) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeChroma()
			defer fake.close()
			tt.configure(fake)

			service, err := NewService(
				WithBaseURL(fake.server.URL),
				WithEmbedder(&testEmbedder{dimension: 3}),
			)
			if tt.wantError == "" {
				require.NoError(t, err)
				require.NoError(t, service.Close())
				return
			}
			assert.Nil(t, service)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestNewService_AutoCreatesMarkedCosineCollection(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	fake.collectionExists = false

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.True(t, fake.collectionExists)
	assert.Equal(t, "cosine", fake.metric)
	assert.Equal(t, collectionBackend, fake.metadata["trpc_backend"])
	version, err := int64Value(fake.metadata[metadataSchemaVersionKey])
	require.NoError(t, err)
	assert.Equal(t, schemaVersion, version)
}

func TestNewService_MissingCollectionFailsWhenAutoCreateDisabled(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	fake.collectionExists = false

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
		WithAutoCreateCollection(false),
	)

	assert.Nil(t, service)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestService_CloseWaitsForInflightRequest(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.requestHook = func(operation string) {
		if operation == "get" {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := service.ReadMemories(
			context.Background(),
			memory.UserKey{AppName: "app", UserID: "user"},
			0,
		)
		readDone <- readErr
	}()
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight request completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-readDone)
	require.NoError(t, <-closeDone)

	_, err = service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		0,
	)
	assert.ErrorIs(t, err, errServiceClosed)
	assert.NoError(t, service.Close())
}

func TestService_CloseDrainsWorkerBeforeClosingHTTPClient(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	extractor := &blockingExtractor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
		WithExtractor(extractor),
		WithAsyncMemoryNum(1),
	)
	require.NoError(t, err)
	sess := session.NewSession("app", "user", "session")
	sess.Events = append(sess.Events, event.Event{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewUserMessage("remember this"),
		}}},
	})
	require.NoError(t, service.EnqueueAutoMemoryJob(context.Background(), sess))
	<-extractor.entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned before the worker drained")
	case <-time.After(20 * time.Millisecond):
	}
	close(extractor.release)
	require.NoError(t, <-closeDone)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Len(t, fake.records, 1)
}

func TestService_ToolsReturnsCopy(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	first := service.Tools()
	require.NotEmpty(t, first)
	first[0] = nil
	second := service.Tools()

	assert.NotNil(t, second[0])
}

func TestOptions_Defaults(t *testing.T) {
	opts := defaultOptions.clone()

	assert.Equal(t, defaultCollectionName, opts.collectionName)
	assert.True(t, opts.autoCreateCollection)
	assert.Equal(t, 10, opts.maxResults)
	assert.InDelta(t, 0.30, opts.similarityThreshold, 0.0001)
	assert.Equal(t, 1000, opts.memoryLimit)
	assert.Equal(t, 1000, opts.hybridCandidateLimit)
	assert.Equal(t, 10*time.Second, opts.timeout)
}

func TestOptions_HTTPHeadersAreCopied(t *testing.T) {
	headers := map[string]string{"X-Custom": "before"}
	opts := defaultOptions.clone()
	WithHTTPHeaders(headers)(&opts)

	headers["X-Custom"] = "after"
	headers["X-Added"] = "value"

	assert.Equal(t, "before", opts.headers["X-Custom"])
	assert.NotContains(t, opts.headers, "X-Added")
}

func TestNewService_RejectsInvalidOptions(t *testing.T) {
	embedder := &testEmbedder{dimension: 3}
	tests := []struct {
		name    string
		options []ServiceOpt
		match   string
	}{
		{name: "base URL missing", options: []ServiceOpt{WithEmbedder(embedder)}, match: "base URL"},
		{name: "embedder missing", options: []ServiceOpt{WithBaseURL("http://localhost")}, match: "embedder"},
		{
			name: "invalid scheme",
			options: []ServiceOpt{
				WithBaseURL("ftp://localhost"), WithEmbedder(embedder),
			},
			match: "scheme",
		},
		{
			name: "authentication conflict",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithAPIKey("key"), WithBearerToken("token"),
			},
			match: "mutually exclusive",
		},
		{
			name: "custom auth needs scope",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithHTTPHeaders(map[string]string{"Authorization": "custom"}),
			},
			match: "tenant and database",
		},
		{
			name: "threshold",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithSimilarityThreshold(1.01),
			},
			match: "similarity threshold",
		},
		{
			name: "threshold NaN",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithSimilarityThreshold(math.NaN()),
			},
			match: "similarity threshold",
		},
		{
			name: "dimension mismatch",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithIndexDimension(4),
			},
			match: "embedding dimension mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.options...)
			assert.Nil(t, service)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestNewService_InfersAPIKeyScopeFromIdentity(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithAPIKey("key"),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, "tenant-a", service.collection.tenant)
	assert.Equal(t, "database-a", service.collection.database)
}

func TestNewService_CustomAuthUsesExplicitScope(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithHTTPHeaders(map[string]string{"Authorization": "Custom secret"}),
		WithTenant("tenant-custom"),
		WithDatabase("database-custom"),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, "tenant-custom", service.collection.tenant)
	assert.Equal(t, "database-custom", service.collection.database)
}

func ExampleNewService() {
	embedder := openaiembedder.New(
		openaiembedder.WithModel("text-embedding-3-small"),
	)
	service, err := NewService(
		WithBaseURL("http://localhost:8000"),
		WithCollectionName("memories"),
		WithEmbedder(embedder),
	)
	if err != nil {
		return
	}
	defer service.Close()
}

func TestIntegration_ChromaDB159(t *testing.T) {
	baseURL := os.Getenv("CHROMADB_INTEGRATION_URL")
	if baseURL == "" {
		t.Skip("CHROMADB_INTEGRATION_URL is not set")
	}
	options := []ServiceOpt{
		WithBaseURL(baseURL),
		WithCollectionName("trpc_agent_go_memory_integration"),
		WithEmbedder(&testEmbedder{
			dimension: 3,
			values: map[string][]float64{
				"integration memory": {1, 0, 0},
				"integration query":  {1, 0, 0},
			},
		}),
		WithMemoryLimit(0),
	}
	if tenant := os.Getenv("CHROMADB_INTEGRATION_TENANT"); tenant != "" {
		options = append(options, WithTenant(tenant))
	}
	if database := os.Getenv("CHROMADB_INTEGRATION_DATABASE"); database != "" {
		options = append(options, WithDatabase(database))
	}
	service, err := NewService(options...)
	require.NoError(t, err)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "integration", UserID: "chromadb-1.5.9"}
	require.NoError(t, service.ClearMemories(ctx, userKey))
	t.Cleanup(func() {
		_ = service.ClearMemories(context.Background(), userKey)
		_ = service.Close()
	})

	require.NoError(t, service.AddMemory(ctx, userKey, "integration memory", []string{"test"}))
	results, err := service.SearchMemories(ctx, userKey, "integration query")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "integration memory", results[0].Memory.Memory)
	assert.InDelta(t, 1, results[0].Score, 0.0001)
}

type testEmbedder struct {
	dimension int
	values    map[string][]float64
	err       error
}

func (e *testEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
	if e.err != nil {
		return nil, e.err
	}
	if value, ok := e.values[text]; ok {
		return append([]float64(nil), value...), nil
	}
	result := make([]float64, e.dimension)
	if len(result) > 0 {
		result[0] = 1
	}
	return result, nil
}

func (e *testEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	value, err := e.GetEmbedding(ctx, text)
	return value, nil, err
}

func (e *testEmbedder) GetDimensions() int {
	return e.dimension
}

type fakeRecord struct {
	document  *string
	embedding []float32
	metadata  map[string]any
}

type fakeChroma struct {
	mu               sync.Mutex
	server           *httptest.Server
	records          map[string]*fakeRecord
	collectionExists bool
	collectionName   string
	dimension        *int
	metric           string
	index            string
	metadata         map[string]any
	maxBatch         int
	requestHook      func(string)
	status           map[string]int
}

func newFakeChroma() *fakeChroma {
	fake := &fakeChroma{
		records:          make(map[string]*fakeRecord),
		collectionExists: true,
		collectionName:   defaultCollectionName,
		metric:           "cosine",
		index:            "hnsw",
		metadata:         map[string]any{},
		maxBatch:         300,
		status:           make(map[string]int),
	}
	fake.server = httptest.NewServer(fake)
	return fake
}

func (f *fakeChroma) close() {
	f.server.Close()
}

func (f *fakeChroma) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	operation := fakeOperation(request)
	if f.requestHook != nil {
		f.requestHook(operation)
	}
	if status := f.status[operation]; status != 0 {
		writeTestJSON(writer, status, map[string]any{"error": "test", "message": operation})
		return
	}
	switch operation {
	case "preflight":
		writeTestJSON(writer, http.StatusOK, map[string]any{"max_batch_size": f.maxBatch})
	case "identity":
		writeTestJSON(writer, http.StatusOK, map[string]any{
			"user_id": "test", "tenant": "tenant-a", "databases": []string{"database-a"},
		})
	case "get_collection":
		f.handleGetCollection(writer)
	case "create_collection":
		f.handleCreateCollection(writer, request)
	case "collection":
		f.writeCollection(writer)
	case "add":
		f.handleAdd(writer, request)
	case "update":
		f.handleUpdate(writer, request)
	case "get":
		f.handleGet(writer, request)
	case "query":
		f.handleQuery(writer, request)
	case "delete":
		f.handleDelete(writer, request)
	default:
		writeTestJSON(writer, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}

func fakeOperation(request *http.Request) string {
	path := request.URL.Path
	switch {
	case path == "/api/v2/pre-flight-checks":
		return "preflight"
	case path == "/api/v2/auth/identity":
		return "identity"
	case strings.HasSuffix(path, "/add"):
		return "add"
	case strings.HasSuffix(path, "/update"):
		return "update"
	case strings.HasSuffix(path, "/get"):
		return "get"
	case strings.HasSuffix(path, "/query"):
		return "query"
	case strings.HasSuffix(path, "/delete"):
		return "delete"
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/collections"):
		return "create_collection"
	case request.Method == http.MethodGet && strings.Contains(path, "/collections/"):
		return "get_collection"
	default:
		return "unknown"
	}
}

func (f *fakeChroma) writeCollection(writer http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	configuration := map[string]any{"hnsw": nil, "spann": nil}
	configuration[f.index] = map[string]any{"space": f.metric}
	writeTestJSON(writer, http.StatusOK, map[string]any{
		"id":                 "collection-id",
		"name":               f.collectionName,
		"configuration_json": configuration,
		"metadata":           cloneAnyMap(f.metadata),
		"dimension":          f.dimension,
		"tenant":             defaultTenant,
		"database":           defaultDatabase,
	})
}

func (f *fakeChroma) handleGetCollection(writer http.ResponseWriter) {
	f.mu.Lock()
	exists := f.collectionExists
	f.mu.Unlock()
	if !exists {
		writeTestJSON(writer, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	f.writeCollection(writer)
}

func (f *fakeChroma) handleCreateCollection(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var payload createCollectionRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	f.collectionExists = true
	f.collectionName = payload.Name
	f.metadata = cloneAnyMap(payload.Metadata)
	f.metric = payload.Configuration.HNSW.Space
	f.index = "hnsw"
	f.mu.Unlock()
	f.writeCollection(writer)
}

func (f *fakeChroma) handleAdd(writer http.ResponseWriter, request *http.Request) {
	var payload addRecordsRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, id := range payload.IDs {
		if _, exists := f.records[id]; exists {
			continue
		}
		f.records[id] = &fakeRecord{
			document:  cloneStringPointer(payload.Documents[i]),
			embedding: append([]float32(nil), payload.Embeddings[i]...),
			metadata:  cloneAnyMap(payload.Metadatas[i]),
		}
		if f.dimension == nil {
			dimension := len(payload.Embeddings[i])
			f.dimension = &dimension
		}
	}
	writeTestJSON(writer, http.StatusCreated, map[string]any{})
}

func (f *fakeChroma) handleUpdate(writer http.ResponseWriter, request *http.Request) {
	var payload updateRecordsRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, id := range payload.IDs {
		record := f.records[id]
		if record == nil {
			continue
		}
		applyFakeUpdate(record, payload, i)
	}
	writeTestJSON(writer, http.StatusOK, map[string]any{})
}

func applyFakeUpdate(record *fakeRecord, payload updateRecordsRequest, index int) {
	if index < len(payload.Embeddings) {
		record.embedding = append([]float32(nil), payload.Embeddings[index]...)
	}
	if index < len(payload.Documents) {
		record.document = cloneStringPointer(payload.Documents[index])
	}
	if index >= len(payload.Metadatas) {
		return
	}
	for key, value := range payload.Metadatas[index] {
		if value == nil {
			delete(record.metadata, key)
			continue
		}
		record.metadata[key] = value
	}
}

func (f *fakeChroma) handleGet(writer http.ResponseWriter, request *http.Request) {
	var payload getRecordsRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.filteredIDs(payload.IDs, payload.Where)
	ids = paginateIDs(ids, payload.Offset, payload.Limit)
	response := map[string]any{"ids": ids, "include": dereferenceStrings(payload.Include)}
	f.includeRecords(response, ids, dereferenceStrings(payload.Include))
	writeTestJSON(writer, http.StatusOK, response)
}

func (f *fakeChroma) handleQuery(writer http.ResponseWriter, request *http.Request) {
	var payload queryRecordsRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.filteredIDs(nil, payload.Where)
	distances := make(map[string]float64, len(ids))
	for _, id := range ids {
		distances[id] = cosineDistance(payload.QueryEmbeddings[0], f.records[id].embedding)
	}
	sort.Slice(ids, func(i, j int) bool {
		if distances[ids[i]] != distances[ids[j]] {
			return distances[ids[i]] < distances[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > payload.NResults {
		ids = ids[:payload.NResults]
	}
	response := map[string]any{"ids": [][]string{ids}, "include": payload.Include}
	f.includeQueryRecords(response, ids, payload.Include, distances)
	writeTestJSON(writer, http.StatusOK, response)
}

func (f *fakeChroma) handleDelete(writer http.ResponseWriter, request *http.Request) {
	var payload deleteRecordsRequest
	if err := decodeTestJSON(request, &payload); err != nil {
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{"message": err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.filteredIDs(payload.IDs, payload.Where)
	if payload.Limit != nil && len(ids) > *payload.Limit {
		ids = ids[:*payload.Limit]
	}
	for _, id := range ids {
		delete(f.records, id)
	}
	writeTestJSON(writer, http.StatusOK, map[string]any{"deleted": len(ids)})
}

func (f *fakeChroma) filteredIDs(ids []string, where map[string]any) []string {
	candidates := ids
	if len(candidates) == 0 {
		candidates = make([]string, 0, len(f.records))
		for id := range f.records {
			candidates = append(candidates, id)
		}
	}
	result := make([]string, 0, len(candidates))
	for _, id := range candidates {
		record := f.records[id]
		if record != nil && matchesFakeWhere(record.metadata, where) {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (f *fakeChroma) includeRecords(response map[string]any, ids, includes []string) {
	if containsString(includes, "documents") {
		documents := make([]*string, len(ids))
		for i, id := range ids {
			documents[i] = cloneStringPointer(f.records[id].document)
		}
		response["documents"] = documents
	}
	if containsString(includes, "metadatas") {
		metadatas := make([]map[string]any, len(ids))
		for i, id := range ids {
			metadatas[i] = cloneAnyMap(f.records[id].metadata)
		}
		response["metadatas"] = metadatas
	}
}

func (f *fakeChroma) includeQueryRecords(
	response map[string]any,
	ids, includes []string,
	distances map[string]float64,
) {
	inner := make(map[string]any)
	f.includeRecords(inner, ids, includes)
	if documents, ok := inner["documents"]; ok {
		response["documents"] = []any{documents}
	}
	if metadatas, ok := inner["metadatas"]; ok {
		response["metadatas"] = []any{metadatas}
	}
	if containsString(includes, "distances") {
		values := make([]float64, len(ids))
		for i, id := range ids {
			values[i] = distances[id]
		}
		response["distances"] = [][]float64{values}
	}
}

func matchesFakeWhere(metadata map[string]any, where map[string]any) bool {
	if len(where) == 0 {
		return true
	}
	for key, raw := range where {
		switch key {
		case "$and":
			return everyFakeClause(metadata, raw)
		case "$or":
			return anyFakeClause(metadata, raw)
		default:
			return matchesFakeComparison(metadata[key], raw)
		}
	}
	return true
}

func everyFakeClause(metadata map[string]any, raw any) bool {
	for _, clause := range fakeClauses(raw) {
		if !matchesFakeWhere(metadata, clause) {
			return false
		}
	}
	return true
}

func anyFakeClause(metadata map[string]any, raw any) bool {
	for _, clause := range fakeClauses(raw) {
		if matchesFakeWhere(metadata, clause) {
			return true
		}
	}
	return false
}

func fakeClauses(raw any) []map[string]any {
	values, _ := raw.([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if clause, ok := value.(map[string]any); ok {
			result = append(result, clause)
		}
	}
	return result
}

func matchesFakeComparison(actual, raw any) bool {
	comparison, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for operator, expected := range comparison {
		switch operator {
		case "$eq":
			return equalFakeValues(actual, expected)
		case "$gte":
			return fakeNumber(actual) >= fakeNumber(expected)
		case "$lte":
			return fakeNumber(actual) <= fakeNumber(expected)
		default:
			return false
		}
	}
	return false
}

func equalFakeValues(left, right any) bool {
	leftNumber, leftOK := numericFakeValue(left)
	rightNumber, rightOK := numericFakeValue(right)
	if leftOK && rightOK {
		return leftNumber == rightNumber
	}
	return left == right
}

func numericFakeValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func fakeNumber(value any) float64 {
	number, _ := numericFakeValue(value)
	return number
}

func cosineDistance(left, right []float32) float64 {
	var dot, leftNorm, rightNorm float64
	for i := range left {
		l := float64(left[i])
		r := float64(right[i])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 1
	}
	return 1 - dot/(math.Sqrt(leftNorm)*math.Sqrt(rightNorm))
}

func paginateIDs(ids []string, offset, limit *int) []string {
	start := 0
	if offset != nil {
		start = *offset
	}
	if start >= len(ids) {
		return nil
	}
	end := len(ids)
	if limit != nil && start+*limit < end {
		end = start + *limit
	}
	return ids[start:end]
}

func dereferenceStrings(values *[]string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), (*values)...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func decodeTestJSON(request *http.Request, output any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.UseNumber()
	return decoder.Decode(output)
}

func writeTestJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}
