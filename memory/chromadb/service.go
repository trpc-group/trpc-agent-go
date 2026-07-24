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
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

var errServiceClosed = errors.New("chromadb memory service is closed")

// Service stores and searches memories through a ChromaDB REST server.
//
// The Service owns HTTP resources that it creates, but it never closes a client or
// transport supplied through WithHTTPClient.
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

	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a client-server ChromaDB memory service and validates its collection.
//
// Construction performs ChromaDB REST preflight and collection requests. ChromaDB must
// run separately, either locally, remotely, or in Chroma Cloud. Embeddings are generated
// by the configured tRPC-Agent-Go embedder rather than a server-side embedding function.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultServiceOpts()
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	if err := normalizeAndValidateOptions(&opts); err != nil {
		return nil, err
	}
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	client := newAPIClient(opts)
	service := &Service{
		opts:   opts,
		client: client,
	}
	if err := service.initialize(); err != nil {
		client.closeIdleConnections()
		return nil, err
	}
	service.initializeTools()
	return service, nil
}

// initialize verifies server capabilities, resolves the database, and prepares
// the collection before the Service becomes visible to callers.
func (svc *Service) initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultInitTimeout)
	defer cancel()

	database, err := svc.resolveDatabase(ctx)
	if err != nil {
		return fmt.Errorf("resolve chromadb scope: %w", err)
	}
	preflight, err := svc.client.preflightChecks(ctx)
	if err != nil {
		return fmt.Errorf("load chromadb preflight checks: %w", err)
	}
	if preflight.MaxBatchSize <= 0 {
		return fmt.Errorf("invalid chromadb max batch size: %d", preflight.MaxBatchSize)
	}
	svc.maxBatchSize = preflight.MaxBatchSize

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
	}
	return nil
}

// resolveDatabase determines the tenant and database from explicit options or identity.
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

// uniqueDatabase accepts identity inference only when exactly one database is available.
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

// loadOrCreateCollection resolves the configured collection and creates it when allowed.
func (svc *Service) loadOrCreateCollection(
	ctx context.Context,
	database databaseRef,
) (*collectionResponse, error) {
	collection, err := svc.client.getCollection(ctx, database, svc.opts.collectionName)
	if err == nil {
		return collection, nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return nil, fmt.Errorf("get chromadb collection: %w", err)
	}
	if !svc.opts.autoCreateCollection {
		return nil, fmt.Errorf("get chromadb collection: %w", err)
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
		return nil, fmt.Errorf("create chromadb collection: %w", err)
	}
	return collection, nil
}

// validateCollection checks identity, ownership markers, index settings, and dimension.
func (svc *Service) validateCollection(collection *collectionResponse) error {
	if collection == nil {
		return errors.New("chromadb collection response has no id")
	}
	if err := validateCollectionID(collection.ID); err != nil {
		return err
	}
	if collection.Name != svc.opts.collectionName {
		return fmt.Errorf(
			"chromadb collection name mismatch: expected %s, got %s",
			svc.opts.collectionName,
			collection.Name,
		)
	}
	if !collection.Dimension.present {
		return errors.New("chromadb collection response is missing dimension")
	}
	if err := validateCollectionMetric(collection.ConfigurationJSON); err != nil {
		return err
	}
	if err := validateCollectionMetadata(collection.Metadata); err != nil {
		return err
	}
	if err := validateCollectionSchema(collection.Schema); err != nil {
		return err
	}
	var dimension *int
	if !collection.Dimension.null {
		dimension = &collection.Dimension.value
	}
	return svc.resolveIndexDimension(dimension)
}

// validateCollectionID rejects malformed collection UUIDs returned by the server.
func validateCollectionID(id string) error {
	if len(id) != 36 {
		return fmt.Errorf("chromadb collection id is not a UUID: %q", id)
	}
	for i := 0; i < len(id); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if id[i] != '-' {
				return fmt.Errorf("chromadb collection id is not a UUID: %q", id)
			}
			continue
		}
		if !isHexDigit(id[i]) {
			return fmt.Errorf("chromadb collection id is not a UUID: %q", id)
		}
	}
	return nil
}

// isHexDigit reports whether value is an ASCII hexadecimal digit.
func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

// validateCollectionMetric requires exactly one active cosine vector index.
func validateCollectionMetric(configuration collectionConfiguration) error {
	active := configuration.HNSW
	if configuration.HNSW != nil && configuration.SPANN != nil {
		return errors.New("chromadb collection has multiple active vector indexes")
	}
	if active == nil {
		active = configuration.SPANN
	}
	if active == nil {
		return errors.New("chromadb collection has no active vector index")
	}
	if !strings.EqualFold(active.Space, "cosine") {
		return fmt.Errorf("chromadb collection metric must be cosine, got %s", active.Space)
	}
	return nil
}

// validateCollectionMetadata rejects explicit backend or schema marker conflicts.
func validateCollectionMetadata(metadata map[string]any) error {
	if backend, ok := metadata["trpc_backend"]; ok {
		text, ok := backend.(string)
		if !ok || text != collectionBackend {
			return fmt.Errorf("chromadb collection is owned by a different backend: %v", backend)
		}
	}
	if version, ok := metadata[metadataSchemaVersionKey]; ok {
		parsed, err := int64Value(version)
		if err != nil {
			return fmt.Errorf("decode chromadb collection schema version: %w", err)
		}
		if parsed != schemaVersion {
			return fmt.Errorf("unsupported chromadb collection schema version: %d", parsed)
		}
	}
	return nil
}

type requiredSchemaIndex struct {
	key       string
	valueType string
	indexName string
}

// validateCollectionSchema ensures every filtered metadata field remains indexed.
func validateCollectionSchema(schema *collectionSchema) error {
	if schema == nil {
		return nil
	}
	if schema.Defaults == nil {
		return errors.New("chromadb collection schema is missing defaults")
	}
	if schema.Keys == nil {
		return errors.New("chromadb collection schema is missing keys")
	}
	requirements := []requiredSchemaIndex{
		{metadataAppNameKey, "string", "string_inverted_index"},
		{metadataUserIDKey, "string", "string_inverted_index"},
		{metadataKindKey, "string", "string_inverted_index"},
		{metadataUpdateTokenKey, "string", "string_inverted_index"},
		{metadataSchemaVersionKey, "int", "int_inverted_index"},
		{metadataDeletedAtKey, "int", "int_inverted_index"},
		{metadataEventTimeKey, "int", "int_inverted_index"},
		{metadataCreatedAtKey, "int", "int_inverted_index"},
		{metadataHasEventTimeKey, "bool", "bool_inverted_index"},
	}
	for _, requirement := range requirements {
		enabled, err := resolveSchemaIndex(schema, requirement)
		if err != nil {
			return err
		}
		if !enabled {
			return fmt.Errorf(
				"chromadb collection schema disables %s for metadata key %s",
				requirement.indexName,
				requirement.key,
			)
		}
	}
	return nil
}

// resolveSchemaIndex applies field-level configuration before the type default.
func resolveSchemaIndex(schema *collectionSchema, requirement requiredSchemaIndex) (bool, error) {
	if keyIndexes, ok := schema.Keys[requirement.key]; ok {
		if state, ok := keyIndexes[requirement.indexName]; ok {
			return schemaIndexEnabled(state, "key "+requirement.key)
		}
	}
	typeIndexes, ok := schema.Defaults[requirement.valueType]
	if !ok {
		return false, fmt.Errorf(
			"chromadb collection schema is missing %s defaults",
			requirement.valueType,
		)
	}
	state, ok := typeIndexes[requirement.indexName]
	if !ok {
		return false, fmt.Errorf(
			"chromadb collection schema is missing default %s",
			requirement.indexName,
		)
	}
	return schemaIndexEnabled(state, "default "+requirement.indexName)
}

// schemaIndexEnabled interprets a present schema index configuration.
func schemaIndexEnabled(state schemaIndexState, description string) (bool, error) {
	if state.Enabled == nil {
		return false, fmt.Errorf("chromadb collection schema %s is missing enabled", description)
	}
	return *state.Enabled, nil
}

// resolveIndexDimension reconciles the collection, explicit, and embedder dimensions.
func (svc *Service) resolveIndexDimension(collectionDimension *int) error {
	if collectionDimension != nil && *collectionDimension <= 0 {
		return fmt.Errorf("chromadb collection has invalid dimension: %d", *collectionDimension)
	}
	dimension := 0
	if svc.opts.indexDimension != nil {
		dimension = *svc.opts.indexDimension
	} else {
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

// initializeTools constructs the final enabled tool set once during initialization.
func (svc *Service) initializeTools() {
	cachedTools := make(map[string]tool.Tool)
	svc.precomputedTools = imemory.BuildToolsList(
		svc.opts.extractor,
		svc.opts.toolCreators,
		svc.opts.enabledTools,
		svc.opts.toolExposed,
		svc.opts.toolHidden,
		cachedTools,
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

// beginOperation admits a public storage operation unless Close has begun.
func (svc *Service) beginOperation() error {
	svc.lifecycleMu.RLock()
	if svc.closed {
		svc.lifecycleMu.RUnlock()
		return errServiceClosed
	}
	return nil
}

// endOperation releases the lifecycle read lock acquired by beginOperation.
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

// Close stops automatic memory workers and releases HTTP resources owned by the Service.
//
// Close is idempotent and does not close an HTTP client or transport supplied by the caller.
func (svc *Service) Close() error {
	svc.closeOnce.Do(func() {
		svc.enqueueMu.Lock()
		if svc.autoMemoryWorker != nil {
			svc.autoMemoryWorker.Stop()
		}
		svc.lifecycleMu.Lock()
		svc.closed = true
		if svc.client != nil {
			svc.client.closeIdleConnections()
		}
		svc.lifecycleMu.Unlock()
		svc.enqueueMu.Unlock()
	})
	return nil
}

// embed generates and validates a finite vector with the collection dimension.
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

// validateDimension initializes an inferred dimension or checks it for consistency.
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
