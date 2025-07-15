package tcvector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

var (
	fieldID           = "id"
	fieldUpdatedAt    = "updated_at"
	fieldCreatedAt    = "created_at"
	fieldName         = "name"
	fieldContent      = "content"
	fieldVector       = "vector"
	fieldSparseVector = "sparse_vector"
	fieldMetadata     = "metadata"
	defaultLimit      = 10
)

// VectorStore is the vector store for tcvectordb.
type VectorStore struct {
	pool          *tcvectordb.RpcClientPool
	option        options
	sparseEncoder encoder.SparseEncoder
}

// New creates a new tcvectordb vector store.
func New(opts ...Option) (*VectorStore, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}
	if option.url == "" {
		return nil, errors.New("tcvectordb url is required")
	}
	if option.database == "" {
		return nil, errors.New("tcvectordb database is required")
	}
	if option.collection == "" {
		return nil, errors.New("tcvectordb collection is required")
	}

	clientPool, err := tcvectordb.NewRpcClientPool(option.url, option.username, option.password, nil)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb new rpc client pool: %w", err)
	}
	if err := initVectorDB(clientPool, option); err != nil {
		return nil, err
	}

	var sparseEncoder encoder.SparseEncoder = nil
	if option.enableTSVector {
		sparseEncoder, err = encoder.NewBM25Encoder(&encoder.BM25EncoderParams{Bm25Language: option.language})
		if err != nil {
			return nil, fmt.Errorf("tcvectordb new bm25 encoder: %w", err)
		}
	}

	if clientPool, ok := clientPool.(*tcvectordb.RpcClientPool); ok {
		return &VectorStore{pool: clientPool, option: option, sparseEncoder: sparseEncoder}, nil
	}
	return nil, errors.New("tcvectordb client pool is not a RpcClientPool")
}

func initVectorDB(clientPool tcvectordb.VdbClient, options options) error {
	_, err := clientPool.CreateDatabaseIfNotExists(context.Background(), options.database)
	if err != nil {
		return fmt.Errorf("tcvectordb create database: %w", err)
	}

	exists, err := clientPool.ExistsCollection(context.Background(), options.database, options.collection)
	if err != nil {
		return fmt.Errorf("tcvectordb create database: %w", err)
	}
	if exists {
		log.Infof("tcvectordb collection %s:%s already exists", options.database, options.collection)
		return nil
	}

	indexes := tcvectordb.Indexes{}
	indexes.FilterIndex = append(indexes.FilterIndex, tcvectordb.FilterIndex{
		FieldName: fieldID,
		IndexType: tcvectordb.PRIMARY,
		FieldType: tcvectordb.String,
	})
	indexes.VectorIndex = append(indexes.VectorIndex, tcvectordb.VectorIndex{
		FilterIndex: tcvectordb.FilterIndex{
			FieldName: fieldVector,
			IndexType: tcvectordb.HNSW,
			FieldType: tcvectordb.Vector,
			ElemType:  tcvectordb.Double,
		},
		Dimension:  uint32(options.indexDimension),
		MetricType: tcvectordb.COSINE,
		Params: &tcvectordb.HNSWParam{
			M:              32,
			EfConstruction: 400,
		},
	})
	if options.enableTSVector {
		indexes.SparseVectorIndex = append(indexes.SparseVectorIndex, tcvectordb.SparseVectorIndex{
			FieldName:  fieldSparseVector,
			FieldType:  tcvectordb.SparseVector,
			IndexType:  tcvectordb.SPARSE_INVERTED,
			MetricType: tcvectordb.IP,
		})
	}

	if _, err := clientPool.CreateCollectionIfNotExists(
		context.Background(),
		options.database,
		options.collection,
		options.sharding,
		options.replicas,
		"trpc-agent-go documents storage collection",
		indexes,
		nil,
	); err != nil {
		return fmt.Errorf("tcvectordb create collection: %w", err)
	}

	return nil
}

// Add stores a document with its embedding vector.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc.ID == "" {
		return fmt.Errorf("tcvectordb document id is required")
	}
	if len(embedding) != int(vs.option.indexDimension) {
		return fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(embedding))
	}
	embedding32 := covertToVector32(embedding)
	now := time.Now().Unix()
	fields := map[string]tcvectordb.Field{
		fieldName:      {Val: doc.Name},
		fieldContent:   {Val: doc.Content},
		fieldCreatedAt: {Val: now},
		fieldUpdatedAt: {Val: now},
		fieldMetadata:  {Val: doc.Metadata},
	}

	if vs.option.enableTSVector {
		sparseVector, err := vs.sparseEncoder.EncodeText(doc.Content)
		if err != nil {
			return fmt.Errorf("tcvectordb bm25 encode text: %w", err)
		}
		fields[fieldSparseVector] = tcvectordb.Field{Val: sparseVector}
	}

	tcDoc := tcvectordb.Document{
		Id:     doc.ID,
		Vector: embedding32,
		Fields: fields,
	}
	if _, err := vs.pool.Upsert(
		ctx,
		vs.option.database,
		vs.option.collection,
		[]tcvectordb.Document{tcDoc},
	); err != nil {
		return fmt.Errorf("tcvectordb upsert document: %w", err)
	}
	return nil
}

// Get retrieves a document by ID along with its embedding.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, fmt.Errorf("tcvectordb document id is required")
	}
	result, err := vs.pool.Query(
		ctx,
		vs.option.database,
		vs.option.collection,
		[]string{id},
		&tcvectordb.QueryDocumentParams{
			RetrieveVector: true,
			Limit:          1,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("tcvectordb get document: %w", err)
	}
	if result.AffectedCount == 0 || len(result.Documents) == 0 {
		return nil, nil, fmt.Errorf("tcvectordb not found document id: %s", id)
	}
	if result.AffectedCount > 1 {
		return nil, nil, fmt.Errorf("tcvectordb get multiple documents, affected count: %d", result.AffectedCount)
	}

	tcDoc := result.Documents[0]
	doc, err := covertToDocument(tcDoc)
	if err != nil {
		return nil, nil, fmt.Errorf("tcvectordb covert to document: %w", err)
	}
	embedding := make([]float64, len(tcDoc.Vector))
	for i, v := range tcDoc.Vector {
		embedding[i] = float64(v)
	}
	return doc, embedding, nil
}

// Update modifies an existing document and its embedding.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc.ID == "" {
		return fmt.Errorf("tcvectordb document id is required")
	}
	if len(embedding) != int(vs.option.indexDimension) {
		return fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(embedding))
	}

	updateFields := map[string]tcvectordb.Field{}
	updateFields[fieldUpdatedAt] = tcvectordb.Field{Val: time.Now().Unix()}
	if len(doc.Name) > 0 {
		updateFields[fieldName] = tcvectordb.Field{Val: doc.Name}
	}
	if len(doc.Content) > 0 {
		updateFields[fieldContent] = tcvectordb.Field{Val: doc.Content}
		if vs.option.enableTSVector {
			sparseVector, err := vs.sparseEncoder.EncodeText(doc.Content)
			if err != nil {
				return fmt.Errorf("tcvectordb bm25 encode text: %w", err)
			}
			updateFields[fieldSparseVector] = tcvectordb.Field{Val: sparseVector}
		}
	}
	if len(doc.Metadata) > 0 {
		updateFields[fieldMetadata] = tcvectordb.Field{Val: doc.Metadata}
	}

	embedding32 := covertToVector32(embedding)
	result, err := vs.pool.Update(ctx, vs.option.database, vs.option.collection, tcvectordb.UpdateDocumentParams{
		QueryIds:     []string{doc.ID},
		UpdateVector: embedding32,
		UpdateFields: updateFields,
	})
	if err != nil {
		return fmt.Errorf("tcvectordb update document: %w", err)
	}
	if result.AffectedCount == 0 {
		return fmt.Errorf("tcvectordb not found document, affected count: %d, id: %s", result.AffectedCount, doc.ID)
	}
	return nil
}

// Delete removes a document and its embedding.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("tcvectordb document id is required")
	}
	if _, err := vs.pool.Delete(
		ctx,
		vs.option.database,
		vs.option.collection,
		tcvectordb.DeleteDocumentParams{
			DocumentIds: []string{id},
			Limit:       1,
		},
	); err != nil {
		return fmt.Errorf("tcvectordb delete document: %w", err)
	}
	return nil
}

// Search performs similarity search and returns the most similar documents.
// Automatically chooses the appropriate search method based on query parameters.
// Tencent VectorDB not support hybrid search of structure filter and vector/sparse vector.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("tcvectordb query is required")
	}
	if !vs.option.enableTSVector && (query.SearchMode == vectorstore.SearchModeKeyword || query.SearchMode == vectorstore.SearchModeHybrid) {
		return nil, fmt.Errorf("tcvectordb: keyword or hybrid search is not supported when enableTSVector is disabled")
	}

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeKeyword:
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeHybrid:
		return vs.searchByHybrid(ctx, query)
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	default:
		return nil, fmt.Errorf("tcvectordb: invalid search mode: %d", query.SearchMode)
	}
}

// vectorSearch performs pure vector similarity search using dense embeddings
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("tcvectordb: searching with a nil or empty vector is not supported")
	}
	if len(query.Vector) != int(vs.option.indexDimension) {
		return nil, fmt.Errorf("tcvectordb vector dimension mismatch, expected: %d, got: %d", vs.option.indexDimension, len(query.Vector))
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	queryParams := tcvectordb.SearchDocumentParams{
		Limit:          int64(limit),
		RetrieveVector: true,
	}

	// Set minimum score threshold if specified
	if query.MinScore > 0 {
		radius := float32(query.MinScore)
		queryParams.Radius = &radius
	}

	vector32 := covertToVector32(query.Vector)
	searchResult, err := vs.pool.Search(
		ctx,
		vs.option.database,
		vs.option.collection,
		[][]float32{vector32},
		&queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb vector search: %w", err)
	}

	return vs.convertSearchResult(searchResult)
}

// keywordSearch performs pure keyword search using BM25 sparse vectors
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, fmt.Errorf("tcvectordb keyword is required for keyword search")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Encode the query text using BM25 for sparse vector
	querySparseVector, err := vs.sparseEncoder.EncodeText(query.Query)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb encode query text: %w", err)
	}

	queryParams := tcvectordb.HybridSearchDocumentParams{
		Limit:          &limit,
		RetrieveVector: true,
		Match: []*tcvectordb.MatchOption{
			{
				FieldName: fieldSparseVector,
				Data:      querySparseVector,
			},
		},
		// Use RRF (Reciprocal Rank Fusion) for combining scores
		Rerank: &tcvectordb.RerankOption{
			Method:    tcvectordb.RerankRrf, // Use RRF method
			FieldList: []string{fieldSparseVector},
			Weight:    []float32{1},
			RrfK:      60, // Default RRF K value
		},
	}

	searchResult, err := vs.pool.HybridSearch(
		ctx,
		vs.option.database,
		vs.option.collection,
		queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb keyword search: %w", err)
	}

	return vs.convertSearchResult(searchResult)
}

// hybridSearch performs hybrid search combining dense vector similarity and BM25 keyword matching
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("tcvectordb vector is required for hybrid search")
	}
	if query.Query == "" {
		return nil, fmt.Errorf("tcvectordb keyword is required for hybrid search")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Encode the query text using BM25 for sparse vector
	querySparseVector, err := vs.sparseEncoder.EncodeText(query.Query)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb encode query text: %w", err)
	}

	vector32 := covertToVector32(query.Vector)

	queryParams := tcvectordb.HybridSearchDocumentParams{
		Limit:          &limit,
		RetrieveVector: true,
		AnnParams: []*tcvectordb.AnnParam{
			{
				FieldName: fieldVector,
				Data:      vector32,
			},
		},
		Match: []*tcvectordb.MatchOption{
			{
				FieldName: fieldSparseVector,
				Data:      querySparseVector,
			},
		},
		// Use RRF (Reciprocal Rank Fusion) for combining scores
		Rerank: &tcvectordb.RerankOption{
			Method:    tcvectordb.RerankRrf, // Use RRF method
			FieldList: []string{fieldVector, fieldSparseVector},
			Weight:    []float32{float32(vs.option.vectorWeight), float32(vs.option.textWeight)},
			RrfK:      60, // Default RRF K value
		},
	}
	searchResult, err := vs.pool.HybridSearch(
		ctx,
		vs.option.database,
		vs.option.collection,
		queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb hybrid search: %w", err)
	}

	return vs.convertSearchResult(searchResult)
}

// filterSearch performs filter-only search when no vector or keyword is provided
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Filter == nil || len(query.Filter.IDs) == 0 {
		return &vectorstore.SearchResult{Results: make([]*vectorstore.ScoredDocument, 0)}, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	queryParams := tcvectordb.QueryDocumentParams{
		Limit:          int64(limit),
		RetrieveVector: true,
	}
	result, err := vs.pool.Query(
		ctx,
		vs.option.database,
		vs.option.collection,
		query.Filter.IDs,
		&queryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("tcvectordb filter search: %w", err)
	}
	return vs.convertQueryResult(result)
}

// Close closes the vector store connection.
func (vs *VectorStore) Close() error {
	vs.pool.Close()
	return nil
}

// convertSearchResult converts tcvectordb search result to vectorstore result
func (vs *VectorStore) convertSearchResult(searchResult *tcvectordb.SearchDocumentResult) (*vectorstore.SearchResult, error) {
	if len(searchResult.Documents) == 0 {
		return &vectorstore.SearchResult{
			Results: make([]*vectorstore.ScoredDocument, 0),
		}, nil
	}

	if len(searchResult.Documents) > 1 {
		return nil, fmt.Errorf(
			"tcvectordb search returned multiple document lists, expected 1, got: %d",
			len(searchResult.Documents),
		)
	}

	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(searchResult.Documents[0])),
	}

	for _, tcDoc := range searchResult.Documents[0] {
		doc, err := covertToDocument(tcDoc)
		if err != nil {
			return nil, fmt.Errorf("tcvectordb convert to document: %w", err)
		}
		result.Results = append(result.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    float64(tcDoc.Score),
		})
	}

	return result, nil
}

// convertQueryResult converts tcvectordb query result to vectorstore result
func (vs *VectorStore) convertQueryResult(queryResult *tcvectordb.QueryDocumentResult) (*vectorstore.SearchResult, error) {
	result := &vectorstore.SearchResult{
		Results: make([]*vectorstore.ScoredDocument, 0, len(queryResult.Documents)),
	}

	for _, tcDoc := range queryResult.Documents {
		doc, err := covertToDocument(tcDoc)
		if err != nil {
			return nil, fmt.Errorf("tcvectordb convert to document: %w", err)
		}
		// For query results, we assign a default score of 1.0
		result.Results = append(result.Results, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    1.0,
		})
	}

	return result, nil
}

func covertToDocument(tcDoc tcvectordb.Document) (*document.Document, error) {
	doc := &document.Document{
		ID: tcDoc.Id,
	}
	if field, ok := tcDoc.Fields[fieldName]; ok {
		doc.Name = field.String()
	}
	if field, ok := tcDoc.Fields[fieldContent]; ok {
		doc.Content = field.String()
	}
	if field, ok := tcDoc.Fields[fieldCreatedAt]; ok {
		doc.CreatedAt = time.Unix(int64(field.Uint64()), 0)
	}
	if field, ok := tcDoc.Fields[fieldUpdatedAt]; ok {
		doc.UpdatedAt = time.Unix(int64(field.Uint64()), 0)
	}
	if field, ok := tcDoc.Fields[fieldMetadata]; ok {
		if metadata, ok := field.Val.(map[string]interface{}); ok {
			doc.Metadata = metadata
		}
	}

	embedding := make([]float64, len(tcDoc.Vector))
	for i, v := range tcDoc.Vector {
		embedding[i] = float64(v)
	}
	return doc, nil
}

func covertToVector32(embedding []float64) []float32 {
	vector32 := make([]float32, len(embedding))
	for i, v := range embedding {
		vector32[i] = float32(v)
	}
	return vector32
}
