//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package chromadb provides a ChromaDB-backed memory service.
package chromadb

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

var errServiceClosed = errors.New("chromadb memory service is closed")

// Service is a ChromaDB-backed memory service.
type Service struct {
	opts         serviceOpts
	client       *apiClient
	collection   collectionRef
	maxBatchSize int

	dimensionMu    sync.Mutex
	indexDimension int
	writeLocks     [defaultWriteLockStripes]sync.Mutex

	lifecycleMu sync.RWMutex
	enqueueMu   sync.RWMutex
	closed      bool
	closeOnce   sync.Once
	closeErr    error

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a ChromaDB-backed memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	client, err := newAPIClient(opts)
	if err != nil {
		return nil, fmt.Errorf("create ChromaDB client: %w", err)
	}
	service := &Service{
		opts:        opts,
		client:      client,
		cachedTools: make(map[string]tool.Tool),
	}
	if err := service.initialize(); err != nil {
		client.closeIdleConnections()
		return nil, err
	}
	service.initializeTools()
	return service, nil
}

func validateOptions(opts serviceOpts) error {
	if opts.optionErr != nil {
		return opts.optionErr
	}
	if opts.baseURL == "" {
		return errors.New("base URL is required")
	}
	if opts.collectionName == "" {
		return errors.New("collection name is required")
	}
	if opts.embedder == nil {
		return errors.New("embedder is required")
	}
	if opts.apiKey != "" && opts.bearer != "" {
		return errors.New("API key and bearer token are mutually exclusive")
	}
	authHeader := customAuthHeader(opts.headers)
	if authHeader != "" && (opts.apiKey != "" || opts.bearer != "") {
		return fmt.Errorf("custom %s header conflicts with configured authentication", authHeader)
	}
	if authHeader != "" && (opts.tenant == "" || opts.database == "") {
		return errors.New("tenant and database are required with custom authentication headers")
	}
	return validateEmbeddingDimensions(opts)
}

func validateEmbeddingDimensions(opts serviceOpts) error {
	embedderDimension := opts.embedder.GetDimensions()
	if embedderDimension < 0 {
		return fmt.Errorf("embedder returned invalid dimension: %d", embedderDimension)
	}
	if opts.indexDimensionSet && embedderDimension > 0 && opts.indexDimension != embedderDimension {
		return fmt.Errorf(
			"embedding dimension mismatch: configured %d, embedder reports %d",
			opts.indexDimension,
			embedderDimension,
		)
	}
	return nil
}

func customAuthHeader(headers map[string]string) string {
	for name := range headers {
		switch {
		case strings.EqualFold(name, "Authorization"):
			return "Authorization"
		case strings.EqualFold(name, "X-Chroma-Token"):
			return "X-Chroma-Token"
		default:
			continue
		}
	}
	return ""
}

func (svc *Service) initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultInitTimeout)
	defer cancel()

	database, err := svc.resolveDatabase(ctx)
	if err != nil {
		return fmt.Errorf("resolve ChromaDB scope: %w", err)
	}
	checklist, err := svc.client.checklist(ctx)
	if err != nil {
		return fmt.Errorf("load ChromaDB pre-flight checks: %w", err)
	}
	if checklist.MaxBatchSize <= 0 {
		return fmt.Errorf("invalid ChromaDB max batch size: %d", checklist.MaxBatchSize)
	}
	svc.maxBatchSize = checklist.MaxBatchSize

	collection, err := svc.loadOrCreateCollection(ctx, database)
	if err != nil {
		return err
	}
	if err := svc.validateCollection(collection); err != nil {
		return err
	}
	svc.collection = collectionRef{
		databaseRef: database,
		id:          collection.ID,
		name:        collection.Name,
	}
	return nil
}

func (svc *Service) resolveDatabase(ctx context.Context) (databaseRef, error) {
	database := databaseRef{
		tenant:   svc.opts.tenant,
		database: svc.opts.database,
	}
	if svc.opts.apiKey != "" && (database.tenant == "" || database.database == "") {
		identity, err := svc.client.identity(ctx)
		if err != nil {
			return databaseRef{}, err
		}
		if database.tenant == "" {
			if identity.Tenant == "" || identity.Tenant == "*" {
				return databaseRef{}, errors.New("identity did not provide a unique tenant")
			}
			database.tenant = identity.Tenant
		}
		if database.database == "" {
			resolved, err := uniqueDatabase(identity.Databases)
			if err != nil {
				return databaseRef{}, err
			}
			database.database = resolved
		}
	}
	if database.tenant == "" {
		database.tenant = defaultTenant
	}
	if database.database == "" {
		database.database = defaultDatabase
	}
	return database, nil
}

func uniqueDatabase(databases []string) (string, error) {
	unique := ""
	for _, database := range databases {
		if database == "" || database == "*" {
			continue
		}
		if unique != "" && unique != database {
			return "", errors.New("identity provides multiple databases; configure one explicitly")
		}
		unique = database
	}
	if unique == "" {
		return "", errors.New("identity did not provide a unique database")
	}
	return unique, nil
}

func (svc *Service) loadOrCreateCollection(
	ctx context.Context,
	database databaseRef,
) (*collectionResponse, error) {
	collection, err := svc.client.getCollection(ctx, database, svc.opts.collectionName)
	if err == nil {
		return collection, nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return nil, fmt.Errorf("get ChromaDB collection: %w", err)
	}
	if !svc.opts.autoCreateCollection {
		return nil, fmt.Errorf("get ChromaDB collection: %w", err)
	}
	request := createCollectionRequest{
		Name: svc.opts.collectionName,
		Metadata: map[string]any{
			"trpc_backend":           collectionBackend,
			metadataSchemaVersionKey: schemaVersion,
		},
		Configuration: createCollectionConfiguration{
			HNSW: vectorIndexConfig{Space: "cosine"},
		},
		GetOrCreate: true,
	}
	collection, err = svc.client.createCollection(ctx, database, request)
	if err != nil {
		return nil, fmt.Errorf("create ChromaDB collection: %w", err)
	}
	return collection, nil
}

func (svc *Service) validateCollection(collection *collectionResponse) error {
	if collection == nil || collection.ID == "" {
		return errors.New("ChromaDB collection response has no id")
	}
	if collection.Name != svc.opts.collectionName {
		return fmt.Errorf(
			"ChromaDB collection name mismatch: expected %s, got %s",
			svc.opts.collectionName,
			collection.Name,
		)
	}
	if err := validateCollectionMetric(collection.ConfigurationJSON); err != nil {
		return err
	}
	if err := validateCollectionMetadata(collection.Metadata); err != nil {
		return err
	}
	return svc.resolveIndexDimension(collection.Dimension)
}

func validateCollectionMetric(configuration collectionConfiguration) error {
	active := configuration.HNSW
	if configuration.HNSW != nil && configuration.SPANN != nil {
		return errors.New("ChromaDB collection has multiple active vector indexes")
	}
	if active == nil {
		active = configuration.SPANN
	}
	if active == nil {
		return errors.New("ChromaDB collection has no active vector index")
	}
	if !strings.EqualFold(active.Space, "cosine") {
		return fmt.Errorf("ChromaDB collection metric must be cosine, got %s", active.Space)
	}
	return nil
}

func validateCollectionMetadata(metadata map[string]any) error {
	if backend, ok := metadata["trpc_backend"]; ok {
		text, ok := backend.(string)
		if !ok || text != collectionBackend {
			return fmt.Errorf("ChromaDB collection is owned by a different backend: %v", backend)
		}
	}
	if version, ok := metadata[metadataSchemaVersionKey]; ok {
		parsed, err := int64Value(version)
		if err != nil {
			return fmt.Errorf("decode ChromaDB collection schema version: %w", err)
		}
		if parsed != schemaVersion {
			return fmt.Errorf("unsupported ChromaDB collection schema version: %d", parsed)
		}
	}
	return nil
}

func (svc *Service) resolveIndexDimension(collectionDimension *int) error {
	dimension := svc.opts.indexDimension
	if dimension == 0 {
		dimension = svc.opts.embedder.GetDimensions()
	}
	if collectionDimension != nil && dimension > 0 && *collectionDimension != dimension {
		return fmt.Errorf(
			"embedding dimension mismatch: collection %d, expected %d",
			*collectionDimension,
			dimension,
		)
	}
	if collectionDimension != nil {
		dimension = *collectionDimension
	}
	svc.indexDimension = dimension
	return nil
}

func (svc *Service) initializeTools() {
	svc.precomputedTools = imemory.BuildToolsList(
		svc.opts.extractor,
		svc.opts.toolCreators,
		svc.opts.enabledTools,
		svc.opts.toolExposed,
		svc.opts.toolHidden,
		svc.cachedTools,
	)
	if svc.opts.extractor == nil {
		return
	}
	imemory.ConfigureExtractorEnabledTools(svc.opts.extractor, svc.opts.enabledTools)
	config := imemory.AutoMemoryConfig{
		Extractor:                svc.opts.extractor,
		AsyncMemoryNum:           svc.opts.asyncMemoryNum,
		MemoryQueueSize:          svc.opts.memoryQueueSize,
		MemoryJobTimeout:         svc.opts.memoryJobTimeout,
		DisableOnExternalContext: svc.opts.disableAutoMemoryOnExternalContext,
		EnabledTools:             svc.opts.enabledTools,
	}
	svc.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, svc)
	svc.autoMemoryWorker.Start()
}

func (svc *Service) beginOperation() error {
	svc.lifecycleMu.RLock()
	if svc.closed {
		svc.lifecycleMu.RUnlock()
		return errServiceClosed
	}
	return nil
}

func (svc *Service) endOperation() {
	svc.lifecycleMu.RUnlock()
}

// Tools returns the memory tools exposed by the service configuration.
func (svc *Service) Tools() []tool.Tool {
	return slices.Clone(svc.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an automatic memory extraction job.
func (svc *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	svc.enqueueMu.RLock()
	defer svc.enqueueMu.RUnlock()
	if err := svc.beginOperation(); err != nil {
		return err
	}
	defer svc.endOperation()
	if svc.autoMemoryWorker == nil {
		return nil
	}
	return svc.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close stops background workers and releases owned HTTP resources.
func (svc *Service) Close() error {
	svc.closeOnce.Do(func() {
		svc.enqueueMu.Lock()
		defer svc.enqueueMu.Unlock()
		if svc.autoMemoryWorker != nil {
			svc.autoMemoryWorker.Stop()
		}
		svc.lifecycleMu.Lock()
		defer svc.lifecycleMu.Unlock()
		svc.closed = true
		if svc.client != nil {
			svc.client.closeIdleConnections()
		}
	})
	return svc.closeErr
}

const (
	defaultCollectionName       = "memories"
	defaultTenant               = "default_tenant"
	defaultDatabase             = "default_database"
	defaultMaxResults           = 10
	defaultSimilarityThreshold  = 0.30
	defaultHybridCandidateLimit = 1000
	defaultRequestTimeout       = 10 * time.Second
	defaultInitTimeout          = 30 * time.Second
	defaultReadPageSize         = 300
	defaultWriteLockStripes     = 256
)

var defaultOptions = serviceOpts{
	collectionName:       defaultCollectionName,
	autoCreateCollection: true,
	maxResults:           defaultMaxResults,
	similarityThreshold:  defaultSimilarityThreshold,
	hybridCandidateLimit: defaultHybridCandidateLimit,
	memoryLimit:          imemory.DefaultMemoryLimit,
	timeout:              defaultRequestTimeout,
	toolCreators:         imemory.AllToolCreators,
	enabledTools:         imemory.DefaultEnabledTools,
	asyncMemoryNum:       imemory.DefaultAsyncMemoryNum,
	memoryQueueSize:      imemory.DefaultMemoryQueueSize,
	memoryJobTimeout:     imemory.DefaultMemoryJobTimeout,
}

type serviceOpts struct {
	baseURL    string
	apiKey     string
	bearer     string
	headers    map[string]string
	tenant     string
	database   string
	httpClient *http.Client
	timeout    time.Duration

	collectionName       string
	autoCreateCollection bool
	indexDimension       int
	indexDimensionSet    bool
	embedder             embedder.Embedder

	maxResults           int
	similarityThreshold  float64
	hybridCandidateLimit int
	memoryLimit          int
	softDelete           bool

	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

	extractor                          extractor.MemoryExtractor
	asyncMemoryNum                     int
	memoryQueueSize                    int
	memoryJobTimeout                   time.Duration
	disableAutoMemoryOnExternalContext bool
	optionErr                          error
}

func (o serviceOpts) clone() serviceOpts {
	opts := o
	opts.headers = maps.Clone(o.headers)
	opts.toolCreators = maps.Clone(o.toolCreators)
	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)
	opts.userExplicitlySet = make(map[string]struct{})
	return opts
}

func (o *serviceOpts) setError(err error) {
	if o.optionErr == nil {
		o.optionErr = err
	}
}

// ServiceOpt configures a ChromaDB memory service.
type ServiceOpt func(*serviceOpts)

// WithBaseURL sets the ChromaDB server base URL.
func WithBaseURL(baseURL string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.baseURL = strings.TrimSpace(baseURL)
	}
}

// WithAPIKey sets the ChromaDB API key sent in X-Chroma-Token.
func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.apiKey = strings.TrimSpace(apiKey)
	}
}

// WithBearerToken sets the bearer token sent in Authorization.
func WithBearerToken(token string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.bearer = strings.TrimSpace(token)
	}
}

// WithHTTPHeaders sets additional HTTP headers.
func WithHTTPHeaders(headers map[string]string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.headers = maps.Clone(headers)
	}
}

// WithTenant sets the ChromaDB tenant.
func WithTenant(tenant string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.tenant = strings.TrimSpace(tenant)
	}
}

// WithDatabase sets the ChromaDB database.
func WithDatabase(database string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.database = strings.TrimSpace(database)
	}
}

// WithHTTPClient sets the HTTP client used for ChromaDB requests.
func WithHTTPClient(client *http.Client) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.httpClient = client
	}
}

// WithTimeout sets the timeout for each ChromaDB operation.
func WithTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			opts.setError(fmt.Errorf("timeout must be positive: %s", timeout))
			return
		}
		opts.timeout = timeout
	}
}

// WithCollectionName sets the ChromaDB collection name.
func WithCollectionName(name string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.collectionName = strings.TrimSpace(name)
	}
}

// WithAutoCreateCollection controls whether a missing collection is created.
func WithAutoCreateCollection(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.autoCreateCollection = enabled
	}
}

// WithIndexDimension sets the expected embedding dimension.
func WithIndexDimension(dimension int) ServiceOpt {
	return func(opts *serviceOpts) {
		if dimension <= 0 {
			opts.setError(fmt.Errorf("index dimension must be positive: %d", dimension))
			return
		}
		opts.indexDimension = dimension
		opts.indexDimensionSet = true
	}
}

// WithEmbedder sets the embedder used for memories and search queries.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.embedder = e
	}
}

// WithMaxResults sets the maximum number of vector search results.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *serviceOpts) {
		if maxResults <= 0 {
			opts.setError(fmt.Errorf("max results must be positive: %d", maxResults))
			return
		}
		opts.maxResults = maxResults
	}
}

// WithSimilarityThreshold sets the minimum cosine similarity score.
func WithSimilarityThreshold(threshold float64) ServiceOpt {
	return func(opts *serviceOpts) {
		if math.IsNaN(threshold) || threshold < 0 || threshold > 1 {
			opts.setError(fmt.Errorf("similarity threshold must be between 0 and 1: %f", threshold))
			return
		}
		opts.similarityThreshold = threshold
	}
}

// WithHybridCandidateLimit sets the local keyword-search candidate limit.
func WithHybridCandidateLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		if limit <= 0 {
			opts.setError(fmt.Errorf("hybrid candidate limit must be positive: %d", limit))
			return
		}
		opts.hybridCandidateLimit = limit
	}
}

// WithMemoryLimit sets the maximum number of active memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
	}
}

// WithSoftDelete controls whether delete operations retain tombstones.
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.softDelete = enabled
	}
}

// WithExtractor sets the extractor used for automatic memory generation.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of automatic memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the automatic memory queue size per worker.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for one automatic memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			opts.setError(fmt.Errorf("memory job timeout must be positive: %s", timeout))
			return
		}
		opts.memoryJobTimeout = timeout
	}
}

// WithDisableAutoMemoryOnExternalContext disables extraction for polluted sessions.
func WithDisableAutoMemoryOnExternalContext(disable bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.disableAutoMemoryOnExternalContext = disable
	}
}

// WithCustomTool sets and enables a custom memory tool implementation.
func WithCustomTool(name string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(name) {
			opts.setError(fmt.Errorf("invalid memory tool name: %s", name))
			return
		}
		if creator == nil {
			opts.setError(fmt.Errorf("memory tool creator is nil: %s", name))
			return
		}
		if opts.toolCreators == nil {
			opts.toolCreators = make(map[string]memory.ToolCreator)
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.toolCreators[name] = creator
		opts.enabledTools[name] = struct{}{}
		opts.userExplicitlySet[name] = struct{}{}
	}
}

// WithToolEnabled controls whether a memory tool is enabled.
func WithToolEnabled(name string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(name) {
			opts.setError(fmt.Errorf("invalid memory tool name: %s", name))
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		if enabled {
			opts.enabledTools[name] = struct{}{}
		} else {
			delete(opts.enabledTools, name)
		}
		opts.userExplicitlySet[name] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools in automatic memory mode.
func WithAutoMemoryExposedTools(names ...string) ServiceOpt {
	return func(opts *serviceOpts) {
		for _, name := range names {
			WithToolExposed(name, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled tool is returned by Tools.
func WithToolExposed(name string, exposed bool) ServiceOpt {
	return func(opts *serviceOpts) {
		if !imemory.IsValidToolName(name) {
			opts.setError(fmt.Errorf("invalid memory tool name: %s", name))
			return
		}
		if exposed {
			if opts.toolExposed == nil {
				opts.toolExposed = make(map[string]struct{})
			}
			opts.toolExposed[name] = struct{}{}
			delete(opts.toolHidden, name)
			return
		}
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		opts.toolHidden[name] = struct{}{}
		delete(opts.toolExposed, name)
	}
}

func (svc *Service) embed(ctx context.Context, text string) ([]float32, error) {
	values, err := svc.opts.embedder.GetEmbedding(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("generate embedding: %w", err)
	}
	if len(values) == 0 {
		return nil, errors.New("generate embedding: received empty embedding")
	}
	if err := svc.validateDimension(len(values)); err != nil {
		return nil, err
	}
	result := make([]float32, len(values))
	for i, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
			return nil, fmt.Errorf("embedding value %d is not a finite float32", i)
		}
		result[i] = float32(value)
	}
	return result, nil
}

func (svc *Service) validateDimension(dimension int) error {
	svc.dimensionMu.Lock()
	defer svc.dimensionMu.Unlock()
	if svc.indexDimension == 0 {
		svc.indexDimension = dimension
		return nil
	}
	if svc.indexDimension != dimension {
		return fmt.Errorf(
			"embedding dimension mismatch: expected %d, got %d",
			svc.indexDimension,
			dimension,
		)
	}
	return nil
}
