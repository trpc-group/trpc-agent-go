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
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

type idleTrackingTransport struct {
	base       http.RoundTripper
	mu         sync.Mutex
	closeCalls int
}

func (t *idleTrackingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(request)
}

func (t *idleTrackingTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeCalls++
}

func (t *idleTrackingTransport) closes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeCalls
}

type testEmbedder struct {
	dimension int
	values    map[string][]float64
	err       error
	mu        sync.Mutex
	calls     int
}

func (e *testEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
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

func (e *testEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
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
	collectionID     string
	collectionName   string
	dimension        *int
	omitDimension    bool
	metric           string
	index            string
	metadata         map[string]any
	schema           *collectionSchema
	maxBatch         int
	requestHook      func(string)
	status           map[string]int
	addAfterWrite    int
	addFailuresLeft  int
	deleteCount      *int
}

func newFakeChroma() *fakeChroma {
	fake := &fakeChroma{
		records:          make(map[string]*fakeRecord),
		collectionExists: true,
		collectionID:     "00000000-0000-0000-0000-000000000001",
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
	response := map[string]any{
		"id":                 f.collectionID,
		"name":               f.collectionName,
		"configuration_json": configuration,
		"metadata":           cloneAnyMap(f.metadata),
		"tenant":             defaultTenant,
		"database":           defaultDatabase,
		"schema":             f.schema,
	}
	if !f.omitDimension {
		response["dimension"] = f.dimension
	}
	writeTestJSON(writer, http.StatusOK, response)
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
	if f.addFailuresLeft > 0 {
		f.addFailuresLeft--
		writeTestJSON(writer, f.addAfterWrite, map[string]any{
			"error": "response_lost", "message": "simulated response loss",
		})
		return
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
	deleted := len(ids)
	if f.deleteCount != nil {
		deleted = *f.deleteCount
	}
	writeTestJSON(writer, http.StatusOK, map[string]any{"deleted": deleted})
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
		return []string{}
	}
	end := len(ids)
	if limit != nil && start+*limit < end {
		end = start + *limit
	}
	return ids[start:end]
}

func dereferenceStrings(values *[]string) []string {
	if values == nil {
		return []string{}
	}
	result := make([]string, len(*values))
	copy(result, *values)
	return result
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
