//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"
	"sync"
	"time"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

const testDimension = 4

var testOptions = options{
	collectionName:     defaultCollectionName,
	dimension:          testDimension,
	distance:           DistanceCosine,
	hnswM:              defaultHNSWM,
	hnswEfConstruct:    defaultHNSWEfConstruct,
	maxResults:         defaultMaxResults,
	maxRetries:         defaultMaxRetries,
	baseRetryDelay:     1 * time.Millisecond,
	maxRetryDelay:      10 * time.Millisecond,
	prefetchMultiplier: defaultPrefetchMultiplier,
}

var testOptionsWithBM25 = options{
	collectionName:     defaultCollectionName,
	dimension:          testDimension,
	distance:           DistanceCosine,
	hnswM:              defaultHNSWM,
	hnswEfConstruct:    defaultHNSWEfConstruct,
	maxResults:         defaultMaxResults,
	maxRetries:         defaultMaxRetries,
	baseRetryDelay:     1 * time.Millisecond,
	maxRetryDelay:      10 * time.Millisecond,
	prefetchMultiplier: defaultPrefetchMultiplier,
	bm25Enabled:        true,
}

func newTestVectorStore(mock *mockClient) *VectorStore {
	return &VectorStore{
		client:          mock,
		ownsClient:      true,
		opts:            testOptions,
		filterConverter: newFilterConverter(),
		retryCfg: retryConfig{
			maxRetries:     testOptions.maxRetries,
			baseRetryDelay: testOptions.baseRetryDelay,
			maxRetryDelay:  testOptions.maxRetryDelay,
		},
	}
}

func newTestVectorStoreWithBM25(mock *mockClient) *VectorStore {
	return &VectorStore{
		client:          mock,
		ownsClient:      true,
		opts:            testOptionsWithBM25,
		filterConverter: newFilterConverter(),
		retryCfg: retryConfig{
			maxRetries:     testOptionsWithBM25.maxRetries,
			baseRetryDelay: testOptionsWithBM25.baseRetryDelay,
			maxRetryDelay:  testOptionsWithBM25.maxRetryDelay,
		},
	}
}

type mockClient struct {
	mu sync.RWMutex

	// Storage
	documents map[string]*mockDoc

	// Function overrides
	CollectionExistsFn  func(ctx context.Context, name string) (bool, error)
	GetCollectionInfoFn func(ctx context.Context, name string) (*qdrant.CollectionInfo, error)
	CreateCollectionFn  func(ctx context.Context, req *qdrant.CreateCollection) error
	DeleteCollectionFn  func(ctx context.Context, name string) error
	UpsertFn            func(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	GetFn               func(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error)
	DeleteFn            func(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	SetPayloadFn        func(ctx context.Context, req *qdrant.SetPayloadPoints) (*qdrant.UpdateResult, error)
	QueryFn             func(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	CountFn             func(ctx context.Context, req *qdrant.CountPoints) (uint64, error)
	ScrollFn            func(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error)
	CloseFn             func() error

	// Call counters
	upsertCalls           int
	getCalls              int
	deleteCalls           int
	deleteCollectionCalls int
	setPayloadCalls       int
	queryCalls            int
	countCalls            int
	scrollCalls           int
	closeCalls            int

	// Error injection
	upsertError           error
	getError              error
	deleteError           error
	deleteCollectionError error
	setPayloadError       error
	queryError            error
	countError            error
	scrollError           error
}

type mockDoc struct {
	doc       *document.Document
	embedding []float64
}

func newMockClient() *mockClient {
	return &mockClient{
		documents: make(map[string]*mockDoc),
	}
}

func (m *mockClient) CollectionExists(ctx context.Context, name string) (bool, error) {
	if m.CollectionExistsFn != nil {
		return m.CollectionExistsFn(ctx, name)
	}
	return true, nil
}

func (m *mockClient) CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error {
	if m.CreateCollectionFn != nil {
		return m.CreateCollectionFn(ctx, req)
	}
	return nil
}

func (m *mockClient) DeleteCollection(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCollectionCalls++

	if m.deleteCollectionError != nil {
		return m.deleteCollectionError
	}
	if m.DeleteCollectionFn != nil {
		return m.DeleteCollectionFn(ctx, name)
	}

	// Clear all documents (simulate collection deletion)
	m.documents = make(map[string]*mockDoc)
	return nil
}

func (m *mockClient) GetCollectionInfo(ctx context.Context, name string) (*qdrant.CollectionInfo, error) {
	if m.GetCollectionInfoFn != nil {
		return m.GetCollectionInfoFn(ctx, name)
	}
	return &qdrant.CollectionInfo{
		Config: &qdrant.CollectionConfig{
			Params: &qdrant.CollectionParams{
				VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
					Size:     testDimension,
					Distance: qdrant.Distance_Cosine,
				}),
			},
		},
	}, nil
}

func (m *mockClient) CreateFieldIndex(ctx context.Context, req *qdrant.CreateFieldIndexCollection) (*qdrant.UpdateResult, error) {
	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.upsertCalls++

	if m.upsertError != nil {
		return nil, m.upsertError
	}
	if m.UpsertFn != nil {
		return m.UpsertFn(ctx, req)
	}

	for _, pt := range req.Points {
		id := pointIDToStr(pt.Id)
		m.documents[id] = &mockDoc{
			doc: &document.Document{
				ID:      getPayloadString(pt.Payload, fieldID),
				Name:    getPayloadString(pt.Payload, fieldName),
				Content: getPayloadString(pt.Payload, fieldContent),
			},
			embedding: extractVectorFromInput(pt.Vectors),
		}
	}

	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) Get(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.getCalls++

	if m.getError != nil {
		return nil, m.getError
	}
	if m.GetFn != nil {
		return m.GetFn(ctx, req)
	}

	var results []*qdrant.RetrievedPoint
	for _, id := range req.Ids {
		idStr := pointIDToStr(id)
		if doc, ok := m.documents[idStr]; ok {
			results = append(results, &qdrant.RetrievedPoint{
				Id: id,
				Payload: map[string]*qdrant.Value{
					fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
					fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
					fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
				},
				Vectors: &qdrant.VectorsOutput{
					VectorsOptions: &qdrant.VectorsOutput_Vector{
						Vector: &qdrant.VectorOutput{
							Vector: &qdrant.VectorOutput_Dense{
								Dense: &qdrant.DenseVector{Data: toFloat32Slice(doc.embedding)},
							},
						},
					},
				},
			})
		}
	}

	return results, nil
}

func (m *mockClient) Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCalls++

	if m.deleteError != nil {
		return nil, m.deleteError
	}
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, req)
	}

	if selector := req.Points.GetPoints(); selector != nil {
		for _, id := range selector.Ids {
			delete(m.documents, pointIDToStr(id))
		}
	}

	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) SetPayload(ctx context.Context, req *qdrant.SetPayloadPoints) (*qdrant.UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.setPayloadCalls++

	if m.setPayloadError != nil {
		return nil, m.setPayloadError
	}
	if m.SetPayloadFn != nil {
		return m.SetPayloadFn(ctx, req)
	}

	// Update payload for matching documents
	if selector := req.PointsSelector.GetPoints(); selector != nil {
		for _, id := range selector.Ids {
			idStr := pointIDToStr(id)
			if doc, ok := m.documents[idStr]; ok {
				// Merge new payload into existing document metadata
				if doc.doc.Metadata == nil {
					doc.doc.Metadata = make(map[string]any)
				}
				for key, val := range req.Payload {
					doc.doc.Metadata[key] = convertValueToAny(val)
				}
			}
		}
	}

	return &qdrant.UpdateResult{}, nil
}

func (m *mockClient) Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queryCalls++

	if m.queryError != nil {
		return nil, m.queryError
	}
	if m.QueryFn != nil {
		return m.QueryFn(ctx, req)
	}

	var results []*qdrant.ScoredPoint
	for id, doc := range m.documents {
		results = append(results, &qdrant.ScoredPoint{
			Id: qdrant.NewID(id),
			Payload: map[string]*qdrant.Value{
				fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
				fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
				fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
			},
			Score: 0.95,
		})
		if req.Limit != nil && len(results) >= int(*req.Limit) {
			break
		}
	}

	return results, nil
}

func (m *mockClient) Count(ctx context.Context, req *qdrant.CountPoints) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.countCalls++

	if m.countError != nil {
		return 0, m.countError
	}
	if m.CountFn != nil {
		return m.CountFn(ctx, req)
	}

	return uint64(len(m.documents)), nil
}

func (m *mockClient) Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.scrollCalls++

	if m.scrollError != nil {
		return nil, m.scrollError
	}
	if m.ScrollFn != nil {
		return m.ScrollFn(ctx, req)
	}

	var results []*qdrant.RetrievedPoint
	for id, doc := range m.documents {
		results = append(results, &qdrant.RetrievedPoint{
			Id: qdrant.NewID(id),
			Payload: map[string]*qdrant.Value{
				fieldID:      {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.ID}},
				fieldName:    {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Name}},
				fieldContent: {Kind: &qdrant.Value_StringValue{StringValue: doc.doc.Content}},
			},
		})
		if req.Limit != nil && len(results) >= int(*req.Limit) {
			break
		}
	}

	return results, nil
}

func (m *mockClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closeCalls++

	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}

// extractVectorFromInput extracts a float64 vector from Qdrant Vectors (input type).
func extractVectorFromInput(vectors *qdrant.Vectors) []float64 {
	if vectors == nil {
		return nil
	}
	v, ok := vectors.VectorsOptions.(*qdrant.Vectors_Vector)
	if !ok || v.Vector == nil {
		return nil
	}
	dense, ok := v.Vector.GetVector().(*qdrant.Vector_Dense)
	if !ok || dense.Dense == nil {
		return nil
	}
	f64 := make([]float64, len(dense.Dense.Data))
	for i, val := range dense.Dense.Data {
		f64[i] = float64(val)
	}
	return f64
}

type mockLoggerImpl struct{}

func (m *mockLoggerImpl) Debug(args ...any)                 {}
func (m *mockLoggerImpl) Debugf(format string, args ...any) {}
func (m *mockLoggerImpl) Info(args ...any)                  {}
func (m *mockLoggerImpl) Infof(format string, args ...any)  {}
func (m *mockLoggerImpl) Warn(args ...any)                  {}
func (m *mockLoggerImpl) Warnf(format string, args ...any)  {}
func (m *mockLoggerImpl) Error(args ...any)                 {}
func (m *mockLoggerImpl) Errorf(format string, args ...any) {}
func (m *mockLoggerImpl) Fatal(args ...any)                 {}
func (m *mockLoggerImpl) Fatalf(format string, args ...any) {}
