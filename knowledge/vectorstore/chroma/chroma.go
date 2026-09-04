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
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

var (
	errDocumentRequired      = errors.New("chroma: document is required")
	errDocumentIDRequired    = errors.New("chroma: document ID is required")
	errCollectionRequired    = errors.New("chroma: collection name is required")
	errVectorDimMismatch     = errors.New("chroma: embedding dimension mismatch")
	errNotFound              = errors.New("chroma: document not found")
	errRecordIndexOutOfRange = errors.New("chroma: record index out of range")
	errCollectionNotFound    = errors.New("chroma: collection not found and auto-create is disabled")
	errEmptyFilter           = errors.New("chroma: filter search requires IDs, metadata, or FilterCondition")
)

// VectorStore implements vectorstore.VectorStore with a Chroma backend.
type VectorStore struct {
	client          storage.ClientInterface
	opts            options
	filterConverter searchfilter.Converter[filterSelectors]
}

var _ vectorstore.VectorStore = (*VectorStore)(nil)

// New constructs a VectorStore and binds it to a Chroma 1.5.3 or newer
// collection. The context controls collection initialization and is not
// retained after New returns. WithAPIKey infers Cloud tenant and database
// from identity when they are omitted. WithSparseSearch plus auto-create
// writes a client-embedded sparse vector index on newly created collections.
func New(ctx context.Context, opts ...Option) (*VectorStore, error) {
	opt := defaultOptions
	for _, o := range opts {
		o(&opt)
	}
	if err := validateOptions(&opt); err != nil {
		return nil, err
	}

	builderOpts, err := resolveClientBuilderOpts(opt)
	if err != nil {
		return nil, err
	}
	if opt.sparseSearch {
		builderOpts = append(builderOpts, storage.WithSparseVectorIndex(opt.sparseSearchKey))
		// The built-in Cloud SPLADE embedder declares its registry entry in
		// auto-created schemas, mirroring the official clients. Custom
		// embedders keep the default "unknown" declaration.
		if splade, ok := opt.sparseEmbedder.(*CloudSpladeEmbedder); ok {
			name, config := splade.functionDeclaration()
			builderOpts = append(builderOpts, storage.WithSparseVectorIndexFunction(name, config))
		}
	}
	c, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("chroma: build client: %w", err)
	}
	vs := &VectorStore{
		client:          c,
		opts:            opt,
		filterConverter: newChromaFilterConverter(),
	}

	if err := vs.initCollection(ctx); err != nil {
		if closeErr := vs.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("chroma close client after init failure: %w", closeErr))
		}
		return nil, err
	}
	return vs, nil
}

// resolveClientBuilderOpts returns the storage client options.
func resolveClientBuilderOpts(opt options) ([]storage.ClientBuilderOpt, error) {
	if opt.instanceName != "" {
		bo, ok := storage.GetChromaInstance(opt.instanceName)
		if !ok {
			return nil, fmt.Errorf("chroma: instance %q not registered", opt.instanceName)
		}
		return bo, nil
	}
	return collectClientBuilderOpts(opt)
}

// collectClientBuilderOpts assembles client options from the store configuration.
func collectClientBuilderOpts(o options) ([]storage.ClientBuilderOpt, error) {
	if o.baseURL == "" {
		return nil, errors.New("chroma: must specify WithInstanceName or WithBaseURL")
	}
	bo := []storage.ClientBuilderOpt{storage.WithBaseURL(o.baseURL)}
	if o.tenant != "" {
		bo = append(bo, storage.WithTenant(o.tenant))
	}
	if o.database != "" {
		bo = append(bo, storage.WithDatabase(o.database))
	}
	if len(o.headers) > 0 {
		bo = append(bo, storage.WithHeaders(o.headers))
	}
	if o.bearerToken != "" {
		bo = append(bo, storage.WithHeaders(map[string]string{
			"Authorization": "Bearer " + o.bearerToken,
		}))
	}
	if o.authToken != "" {
		bo = append(bo, storage.WithAPIKey(o.authToken))
	}
	if len(o.extraOptions) > 0 {
		bo = append(bo, storage.WithExtraOptions(o.extraOptions...))
	}
	return bo, nil
}

// initCollection binds the client to the configured collection, creating it
// if auto-create is enabled, and validates the distance metric and dimension.
func (vs *VectorStore) initCollection(ctx context.Context) error {
	if vs.opts.autoCreateCollection {
		if err := vs.client.GetOrCreateCollection(ctx, vs.opts.collection, nil); err != nil {
			return err
		}
	} else if err := vs.client.GetCollection(ctx, vs.opts.collection); err != nil {
		if errors.Is(err, storage.ErrCollectionNotFound) || storage.IsNotFound(err) {
			return fmt.Errorf("%w: %s", errCollectionNotFound, vs.opts.collection)
		}
		return err
	}
	info := vs.client.CollectionInfo()
	metric := strings.ToLower(info.Metric)
	if metric == "" {
		return fmt.Errorf("chroma: collection %q did not report a cosine HNSW or SPANN index", vs.opts.collection)
	}
	if metric != "cosine" {
		return fmt.Errorf("chroma: collection %q uses %s distance; cosine is required", vs.opts.collection, info.Metric)
	}
	if info.Dimension > 0 && info.Dimension != vs.opts.indexDimension {
		return fmt.Errorf("%w: collection=%d configured=%d", errVectorDimMismatch, info.Dimension, vs.opts.indexDimension)
	}
	if vs.opts.sparseSearch {
		if !info.SchemaKnown {
			return fmt.Errorf("chroma: collection %q did not report its schema", vs.opts.collection)
		}
		if !slices.Contains(info.SparseVectorIndexKeys, vs.opts.sparseSearchKey) {
			return fmt.Errorf(
				"chroma: collection %q has no sparse vector index on %q",
				vs.opts.collection,
				vs.opts.sparseSearchKey,
			)
		}
	}
	return nil
}

// Add upserts a document and its embedding.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if err := validateEmbedding(embedding, vs.opts.indexDimension, true); err != nil {
		return err
	}
	rec, err := vs.docToRecord(doc, embedding, time.Now().Unix())
	if err != nil {
		return err
	}
	if err := vs.attachSparseEmbedding(ctx, rec, doc.Content); err != nil {
		return err
	}
	existing, err := vs.client.Get(ctx, storage.GetParams{
		IDs:     []string{doc.ID},
		Include: includeMetadataOnlyFields,
	})
	if err != nil {
		return fmt.Errorf("chroma get before upsert: %w", err)
	}
	// Re-Add of an existing ID must delete omitted metadata keys. A new ID
	// comes back empty, so skip marker work on that ingest path.
	if existing != nil && len(existing.IDs) > 0 && len(existing.Metadatas) > 0 {
		vs.markAbsentMetadataNil(existing.Metadatas[0], rec)
	}
	if err := vs.client.Upsert(ctx, rec); err != nil {
		return fmt.Errorf("chroma upsert: %w", err)
	}
	return nil
}

// Get returns a document and its embedding by ID.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocumentIDRequired
	}
	res, err := vs.client.Get(ctx, storage.GetParams{
		IDs:     []string{id},
		Include: includeRecordFields,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("chroma get: %w", err)
	}
	if res == nil || len(res.IDs) == 0 {
		return nil, nil, errNotFound
	}
	return vs.recordToDoc(res, 0)
}

// Update replaces an existing document. An empty embedding preserves the stored vector.
//
// Chroma's /update endpoint is a partial update: request fields omitted via
// JSON omitempty keep their stored values. To provide VectorStore replacement
// semantics, metadata keys absent from doc are sent as null deletion markers.
// An empty embedding reuses the stored vector.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if err := validateEmbedding(embedding, vs.opts.indexDimension, false); err != nil {
		return err
	}
	res, err := vs.client.Get(ctx, storage.GetParams{
		IDs:     []string{doc.ID},
		Include: includeRecordFields,
	})
	if err != nil {
		return fmt.Errorf("chroma get before update: %w", err)
	}
	if res == nil || len(res.IDs) == 0 {
		return errNotFound
	}
	existing, existingEmb, err := vs.recordToDoc(res, 0)
	if err != nil {
		return err
	}
	if len(embedding) == 0 {
		embedding = existingEmb
	}
	docCopy := *doc
	doc = &docCopy
	if doc.CreatedAt.IsZero() && existing != nil {
		doc.CreatedAt = existing.CreatedAt
	}
	rec, err := vs.docToRecord(doc, embedding, time.Now().Unix())
	if err != nil {
		return err
	}
	if err := vs.attachSparseEmbedding(ctx, rec, doc.Content); err != nil {
		return err
	}
	if len(res.Metadatas) > 0 {
		vs.markAbsentMetadataNil(res.Metadatas[0], rec)
	}
	if err := vs.client.Update(ctx, rec); err != nil {
		return fmt.Errorf("chroma update: %w", err)
	}
	return nil
}

// markAbsentMetadataNil sends Chroma null deletion markers for stored metadata
// keys that are not in the replacement record.
func (vs *VectorStore) markAbsentMetadataNil(existing map[string]any, rec storage.RecordBatch) {
	if len(existing) == 0 || len(rec.Metadatas) == 0 || rec.Metadatas[0] == nil {
		return
	}
	for key := range existing {
		if _, ok := rec.Metadatas[0][key]; !ok {
			rec.Metadatas[0][key] = nil
		}
	}
}

// Delete removes one document by ID.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocumentIDRequired
	}
	if err := vs.client.Delete(ctx, storage.DeleteParams{IDs: []string{id}}); err != nil {
		return fmt.Errorf("chroma delete: %w", err)
	}
	return nil
}

// Close delegates cleanup to the storage client.
func (vs *VectorStore) Close() error {
	if vs.client == nil {
		return nil
	}
	return vs.client.Close()
}

// DeleteByFilter deletes documents by IDs, metadata map, or DeleteAll.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	cfg := vectorstore.ApplyDeleteOptions(opts...)
	if cfg.DeleteAll {
		if len(cfg.DocumentIDs) > 0 || len(cfg.Filter) > 0 {
			return errors.New("chroma: WithDeleteAll cannot be combined with WithDeleteDocumentIDs or WithDeleteFilter")
		}
		return vs.deleteAll(ctx)
	}
	if len(cfg.DocumentIDs) == 0 && len(cfg.Filter) == 0 {
		return errors.New("chroma: DeleteByFilter requires DocumentIDs, Filter, or DeleteAll")
	}
	p := storage.DeleteParams{IDs: cfg.DocumentIDs}
	if len(cfg.Filter) > 0 {
		where, err := vs.metadataMapToWhere(cfg.Filter)
		if err != nil {
			return err
		}
		p.Where = where
	}
	if len(p.IDs) > 0 {
		return vs.deleteIDBatches(ctx, p)
	}
	return vs.deleteMatching(ctx, p.Where)
}

// deleteAll removes all records by repeatedly reading a batch from offset 0
// and deleting it. The batch respects both the server write limit and the
// configured request limit. The collection itself is preserved.
func (vs *VectorStore) deleteAll(ctx context.Context) error {
	return vs.deleteMatching(ctx, nil)
}

// deleteMatching repeatedly deletes one server-sized ID batch matching where.
func (vs *VectorStore) deleteMatching(ctx context.Context, where map[string]any) error {
	batchSize, err := vs.maxBatchSize(ctx)
	if err != nil {
		return err
	}
	if batchSize > vs.opts.maxRequestRecords {
		batchSize = vs.opts.maxRequestRecords
	}
	var previous []string
	for {
		ids, err := vs.loadIDBatch(ctx, where, batchSize)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if sameIDSet(ids, previous) {
			return errors.New("chroma: delete made no progress")
		}
		if err := vs.client.Delete(ctx, storage.DeleteParams{IDs: ids}); err != nil {
			return fmt.Errorf("chroma delete: %w", err)
		}
		previous = append(previous[:0], ids...)
	}
}

func sameIDSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, id := range a {
		seen[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}

// deleteIDBatches splits an ID selector at the server write limit.
func (vs *VectorStore) deleteIDBatches(ctx context.Context, p storage.DeleteParams) error {
	batchSize, err := vs.maxBatchSize(ctx)
	if err != nil {
		return err
	}
	if batchSize > vs.opts.maxRequestRecords {
		batchSize = vs.opts.maxRequestRecords
	}
	for start := 0; start < len(p.IDs); start += batchSize {
		end := min(start+batchSize, len(p.IDs))
		batch := p
		batch.IDs = p.IDs[start:end]
		if err := vs.client.Delete(ctx, batch); err != nil {
			return fmt.Errorf("chroma delete: %w", err)
		}
	}
	return nil
}

// loadIDBatch reads up to limit matching record IDs from offset 0.
func (vs *VectorStore) loadIDBatch(ctx context.Context, where map[string]any, limit int) ([]string, error) {
	lim := limit
	off := 0
	res, err := vs.client.Get(ctx, storage.GetParams{
		Include: includeIDOnlyFields,
		Limit:   &lim,
		Offset:  &off,
		Where:   where,
	})
	if err != nil {
		return nil, fmt.Errorf("chroma list ids: %w", err)
	}
	if res == nil {
		return nil, nil
	}
	return res.IDs, nil
}

// maxBatchSize returns the server-advertised maximum write batch size.
func (vs *VectorStore) maxBatchSize(ctx context.Context) (int, error) {
	n, err := vs.client.MaxBatchSize(ctx)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errors.New("chroma: max batch size must be positive")
	}
	return n, nil
}

// nextPageSize is the Limit for one Get request while filling want records.
// want <= 0 means unlimited and returns a full page.
func (vs *VectorStore) nextPageSize(have, want int) int {
	if want <= 0 {
		return vs.opts.maxRequestRecords
	}
	remaining := want - have
	if remaining <= 0 {
		return 0
	}
	if remaining < vs.opts.maxRequestRecords {
		return remaining
	}
	return vs.opts.maxRequestRecords
}

// forEachGetPage walks Get results until Chroma returns an empty page or
// maxRecords unique IDs have been delivered. maxRecords <= 0 reads every page.
// IDs are deduplicated across pages, and a page containing no new IDs is
// rejected as pagination without progress.
func (vs *VectorStore) forEachGetPage(
	ctx context.Context,
	base storage.GetParams,
	startOffset, maxRecords int,
	fn func(*storage.GetResult) error,
) error {
	offset := startOffset
	if offset < 0 {
		offset = 0
	}
	seen := make(map[string]struct{})
	have := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lim := vs.nextPageSize(have, maxRecords)
		if lim == 0 {
			return nil
		}
		off := offset
		p := base
		p.Limit = &lim
		p.Offset = &off
		res, err := vs.client.Get(ctx, p)
		if err != nil {
			return err
		}
		if res == nil || len(res.IDs) == 0 {
			return nil
		}
		rawCount := len(res.IDs)
		page := unseenGetResult(res, seen)
		if len(page.IDs) == 0 {
			return fmt.Errorf("chroma: get pagination made no progress at offset %d", offset)
		}
		if maxRecords > 0 {
			page = trimGetResult(page, maxRecords-have)
		}
		if err := fn(page); err != nil {
			return err
		}
		have += len(page.IDs)
		if maxRecords > 0 && have >= maxRecords {
			return nil
		}
		if rawCount < lim {
			return nil
		}
		offset += rawCount
	}
}

// trimGetResult keeps the first n aligned records.
func trimGetResult(res *storage.GetResult, n int) *storage.GetResult {
	if res == nil || n >= len(res.IDs) {
		return res
	}
	if n <= 0 {
		return &storage.GetResult{}
	}
	out := &storage.GetResult{IDs: res.IDs[:n]}
	if res.Documents != nil {
		out.Documents = res.Documents[:min(n, len(res.Documents))]
	}
	if res.Embeddings != nil {
		out.Embeddings = res.Embeddings[:min(n, len(res.Embeddings))]
	}
	if res.Metadatas != nil {
		out.Metadatas = res.Metadatas[:min(n, len(res.Metadatas))]
	}
	if res.URIs != nil {
		out.URIs = res.URIs[:min(n, len(res.URIs))]
	}
	return out
}

// unseenGetResult returns a page containing only IDs not already in seen while
// preserving response-column alignment for the retained records.
func unseenGetResult(res *storage.GetResult, seen map[string]struct{}) *storage.GetResult {
	out := &storage.GetResult{}
	for i, id := range res.IDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out.IDs = append(out.IDs, id)
		if res.Documents != nil {
			out.Documents = append(out.Documents, stringAt(res.Documents, i))
		}
		if res.Embeddings != nil {
			out.Embeddings = append(out.Embeddings, float32SliceAt(res.Embeddings, i))
		}
		if res.Metadatas != nil {
			out.Metadatas = append(out.Metadatas, metadataAt(res.Metadatas, i))
		}
		if res.URIs != nil {
			out.URIs = append(out.URIs, stringAt(res.URIs, i))
		}
	}
	return out
}

func stringAt(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return values[index]
}

func float32SliceAt(values [][]float32, index int) []float32 {
	if index >= len(values) {
		return nil
	}
	return values[index]
}

func metadataAt(values []map[string]any, index int) map[string]any {
	if index >= len(values) {
		return nil
	}
	return values[index]
}

// UpdateByFilter updates matching documents. id and created_at cannot be changed.
// Matching IDs are fixed first, then records are rewritten in server-sized batches.
func (vs *VectorStore) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	cfg, err := vectorstore.ApplyUpdateByFilterOptions(opts...)
	if err != nil {
		return 0, err
	}
	for key := range cfg.Updates {
		if key == "id" || key == metaCreatedAt ||
			vs.opts.sparseSearch && key == "metadata."+vs.opts.sparseSearchKey {
			return 0, fmt.Errorf("chroma: updates key %q cannot be changed", key)
		}
	}

	ids := append([]string(nil), cfg.DocumentIDs...)
	var where map[string]any
	var whereDocument map[string]any
	if cfg.FilterCondition != nil {
		selectors, err := vs.filterConverter.Convert(cfg.FilterCondition)
		if err != nil {
			return 0, err
		}
		if selectors.noMatch {
			return 0, nil
		}
		filter := filterSelectors{ids: ids, idsSet: len(ids) > 0}
		mergeSelectorIDs(&filter, selectors)
		if filter.noMatch {
			return 0, nil
		}
		ids = filter.ids
		where = selectors.where
		whereDocument = selectors.whereDocument
		if !filter.idsSet && len(where) == 0 && len(whereDocument) == 0 {
			return 0, errors.New("chroma: UpdateByFilter condition has no effective selectors")
		}
	}
	selector := storage.GetParams{
		IDs:           ids,
		Where:         where,
		WhereDocument: whereDocument,
		Include:       includeIDOnlyFields,
	}
	var matchedIDs []string
	if err := vs.forEachGetPage(ctx, selector, 0, 0, func(res *storage.GetResult) error {
		if len(matchedIDs)+len(res.IDs) > vs.opts.maxUpdateRecords {
			return fmt.Errorf(
				"chroma: UpdateByFilter matched more than %d records",
				vs.opts.maxUpdateRecords,
			)
		}
		matchedIDs = append(matchedIDs, res.IDs...)
		return nil
	}); err != nil {
		return 0, fmt.Errorf("chroma list ids for update: %w", err)
	}
	if len(matchedIDs) == 0 {
		return 0, nil
	}
	return vs.applyUpdateByFilter(ctx, matchedIDs, cfg.Updates)
}

// applyUpdateByFilter rewrites matched records in server-sized batches. Each
// batch also respects the configured request limit.
func (vs *VectorStore) applyUpdateByFilter(ctx context.Context, matchedIDs []string, updates map[string]any) (int64, error) {
	batchSize, err := vs.maxBatchSize(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	var updated int64
	if batchSize > vs.opts.maxRequestRecords {
		batchSize = vs.opts.maxRequestRecords
	}
	for start := 0; start < len(matchedIDs); start += batchSize {
		end := start + batchSize
		if end > len(matchedIDs) {
			end = len(matchedIDs)
		}
		n, err := vs.updateRecordsByIDs(ctx, matchedIDs[start:end], updates, now)
		if err != nil {
			return updated, err
		}
		updated += n
	}
	return updated, nil
}

// updateRecordsByIDs fetches records by IDs, applies field updates, and writes
// them back with absent-metadata null markers.
func (vs *VectorStore) updateRecordsByIDs(ctx context.Context, ids []string, updates map[string]any, now int64) (int64, error) {
	res, err := vs.client.Get(ctx, storage.GetParams{
		IDs:     ids,
		Include: includeRecordFields,
	})
	if err != nil {
		return 0, fmt.Errorf("chroma get for update: %w", err)
	}
	if res == nil {
		return 0, nil
	}
	batch := storage.RecordBatch{}
	for i := range res.IDs {
		doc, emb, err := vs.recordToDoc(res, i)
		if err != nil {
			return 0, err
		}
		if err := applyUpdates(doc, &emb, updates, vs.opts.indexDimension); err != nil {
			return 0, err
		}
		rec, err := vs.docToRecord(doc, emb, now)
		if err != nil {
			return 0, err
		}
		if err := vs.attachSparseEmbedding(ctx, rec, doc.Content); err != nil {
			return 0, err
		}
		if i < len(res.Metadatas) {
			vs.markAbsentMetadataNil(res.Metadatas[i], rec)
		}
		batch.IDs = append(batch.IDs, rec.IDs...)
		batch.Embeddings = append(batch.Embeddings, rec.Embeddings...)
		batch.Documents = append(batch.Documents, rec.Documents...)
		batch.Metadatas = append(batch.Metadatas, rec.Metadatas...)
	}
	if len(batch.IDs) == 0 {
		return 0, nil
	}
	if err := vs.client.Update(ctx, batch); err != nil {
		return 0, fmt.Errorf("chroma update: %w", err)
	}
	return int64(len(batch.IDs)), nil
}

// applyUpdates modifies document fields and embedding according to the
// updates map. Supported keys: name, content, embedding, metadata.*.
func applyUpdates(doc *document.Document, emb *[]float64, updates map[string]any, dim int) error {
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	for key, val := range updates {
		switch {
		case key == "name":
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("chroma: updates[name] must be string, got %T", val)
			}
			doc.Name = s
		case key == "content":
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("chroma: updates[content] must be string, got %T", val)
			}
			doc.Content = s
		case key == "embedding":
			v, ok := val.([]float64)
			if !ok {
				return fmt.Errorf("chroma: updates[embedding] must be []float64, got %T", val)
			}
			if err := validateEmbedding(v, dim, true); err != nil {
				return err
			}
			*emb = v
		case strings.HasPrefix(key, "metadata."):
			mdKey := key[len("metadata."):]
			if mdKey == "" {
				return fmt.Errorf("chroma: updates key %q is invalid", key)
			}
			if isReservedKey(mdKey) {
				return fmt.Errorf("chroma: updates key %q cannot be changed", key)
			}
			doc.Metadata[mdKey] = val
		default:
			return fmt.Errorf("chroma: updates key %q is not supported (allowed: name/content/embedding/metadata.*)", key)
		}
	}
	return nil
}

// Count returns the number of matching documents.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	cfg := vectorstore.ApplyCountOptions(opts...)
	if len(cfg.Filter) == 0 {
		n, err := vs.client.Count(ctx)
		if err != nil {
			return 0, fmt.Errorf("chroma count: %w", err)
		}
		return n, nil
	}
	where, err := vs.metadataMapToWhere(cfg.Filter)
	if err != nil {
		return 0, err
	}
	n := 0
	if err := vs.forEachGetPage(ctx, storage.GetParams{Where: where, Include: includeIDOnlyFields}, 0, 0, func(res *storage.GetResult) error {
		n += len(res.IDs)
		return nil
	}); err != nil {
		return 0, fmt.Errorf("chroma count filter: %w", err)
	}
	return n, nil
}

// GetMetadata retrieves metadata for matching documents. Each Get request is
// at most the configured request limit; larger limits use pages.
func (vs *VectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
	cfg, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	var where map[string]any
	if len(cfg.Filter) > 0 {
		where, err = vs.metadataMapToWhere(cfg.Filter)
		if err != nil {
			return nil, err
		}
	}

	p := storage.GetParams{
		IDs:     cfg.IDs,
		Where:   where,
		Include: includeMetadataOnlyFields,
	}

	out := map[string]vectorstore.DocumentMetadata{}
	offset := cfg.Offset
	if offset < 0 {
		offset = 0
	}
	// Limit is either positive or -1. A zero maxRecords tells forEachGetPage
	// to keep reading until Chroma returns an empty page.
	maxRecords := 0
	if cfg.Limit > 0 {
		maxRecords = cfg.Limit
	}
	if err := vs.forEachGetPage(ctx, p, offset, maxRecords, func(res *storage.GetResult) error {
		return vs.collectMetadata(out, res)
	}); err != nil {
		return nil, fmt.Errorf("chroma get metadata: %w", err)
	}
	return out, nil
}

// collectMetadata decodes a GetResult page and appends document metadata to out.
func (vs *VectorStore) collectMetadata(out map[string]vectorstore.DocumentMetadata, res *storage.GetResult) error {
	if res == nil {
		return nil
	}
	for i, id := range res.IDs {
		doc, _, err := vs.recordToDoc(res, i)
		if err != nil {
			return err
		}
		md := map[string]any{}
		if doc != nil && doc.Metadata != nil {
			md = doc.Metadata
		}
		out[id] = vectorstore.DocumentMetadata{Metadata: md}
	}
	return nil
}
