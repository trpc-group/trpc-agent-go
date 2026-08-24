//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clickhouse provides a vector store implementation backed by ClickHouse.
//
// It implements knowledge/vectorstore.VectorStore by mapping its operations to
// ClickHouse SQL statements over a ReplacingMergeTree table. Documents are stored
// as rows whose embedding lives in an Array(Float64) column and whose metadata is
// JSON-encoded in a String column. Filter fields declared through WithFilterFields
// are additionally materialized as dedicated typed columns for efficient filtering.
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// Errors returned by vector store operations.
var (
	errDocumentRequired   = errors.New("clickhouse: document is required")
	errDocumentIDRequired = errors.New("clickhouse: document ID is required")
	errTableNameRequired  = errors.New("clickhouse: table name is required")
	errVectorDimMismatch  = errors.New("clickhouse: embedding dimension mismatch")
	errNotFound           = errors.New("clickhouse: document not found")
)

// VectorStore implements vectorstore.VectorStore with a ClickHouse backend.
type VectorStore struct {
	client storage.Client
	option options
}

// Ensure VectorStore implements vectorstore.VectorStore.
var _ vectorstore.VectorStore = (*VectorStore)(nil)

// New constructs a VectorStore and, when autoCreateTable is enabled, ensures the
// backing table exists.
//
// Choose WithDSN or WithInstanceName. WithDSN takes precedence.
func New(opts ...Option) (*VectorStore, error) {
	opt := defaultOptions
	for _, o := range opts {
		o(&opt)
	}
	if err := validateOptions(&opt); err != nil {
		return nil, err
	}

	vs := &VectorStore{option: opt}

	// Resolve the client: DSN first, then a named instance.
	var builderOpts []storage.ClientBuilderOpt
	if opt.dsn != "" {
		builderOpts = append(builderOpts, storage.WithClientBuilderDSN(opt.dsn))
		builderOpts = append(builderOpts, storage.WithExtraOptions(opt.extraOptions...))
	} else if opt.instanceName != "" {
		bo, ok := storage.GetClickHouseInstance(opt.instanceName)
		if !ok {
			return nil, fmt.Errorf("clickhouse: instance %q not registered", opt.instanceName)
		}
		builderOpts = bo
	} else {
		return nil, errors.New("clickhouse: must specify one of WithDSN / WithInstanceName")
	}

	c, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: build client: %w", err)
	}
	vs.client = c

	if opt.autoCreateTable {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := vs.initTable(ctx); err != nil {
			if closeErr := vs.Close(); closeErr != nil {
				return nil, errors.Join(err, fmt.Errorf("clickhouse close client after init failure: %w", closeErr))
			}
			return nil, err
		}
	}
	return vs, nil
}

// initTable creates the backing table when it does not exist. The statement is
// idempotent (CREATE TABLE IF NOT EXISTS) so it never rebuilds an existing table.
func (vs *VectorStore) initTable(ctx context.Context) error {
	if err := vs.client.Exec(ctx, vs.buildCreateTableSQL()); err != nil {
		return fmt.Errorf("clickhouse: create table %s: %w", vs.option.tableName, err)
	}
	return nil
}

// Add writes a document and its embedding. The embedding must match the
// configured vector dimension.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("%w: want=%d got=%d", errVectorDimMismatch, vs.option.vectorDimension, len(embedding))
	}
	r, err := vs.docToRow(doc, embedding, time.Now())
	if err != nil {
		return err
	}
	args, err := vs.insertArgs(r)
	if err != nil {
		return err
	}
	if err := vs.client.Exec(ctx, vs.buildInsertSQL(), args...); err != nil {
		return fmt.Errorf("clickhouse: insert: %w", err)
	}
	return nil
}

// Get returns a document and its embedding by ID.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocumentIDRequired
	}
	sql := fmt.Sprintf("%s WHERE %s = ? LIMIT 1", vs.buildSelectSQL(), vs.option.idFieldName)
	rows, err := vs.client.Query(ctx, sql, id)
	if err != nil {
		return nil, nil, fmt.Errorf("clickhouse: get: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		// Distinguish a genuine miss from a row-stream iteration error: the
		// latter must not be reported as not-found.
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("clickhouse: get: %w", err)
		}
		return nil, nil, errNotFound
	}
	r, err := vs.scanRow(rows, nil)
	if err != nil {
		return nil, nil, err
	}
	if r == nil || r.id == "" {
		return nil, nil, errNotFound
	}
	return vs.rowToDoc(r)
}

// Update replaces an existing document. It loads the current row to preserve
// created_at, then re-inserts a new version with a fresh updated_at so that the
// ReplacingMergeTree engine collapses the old row.
//
// When embedding is empty, the existing vector is preserved.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return errDocumentRequired
	}
	if doc.ID == "" {
		return errDocumentIDRequired
	}
	if len(embedding) > 0 && len(embedding) != vs.option.vectorDimension {
		return fmt.Errorf("%w: want=%d got=%d", errVectorDimMismatch, vs.option.vectorDimension, len(embedding))
	}

	// Load the existing row to preserve created_at and, when embedding is empty,
	// the existing vector.
	existing, existingEmbedding, err := vs.Get(ctx, doc.ID)
	if err != nil {
		return err
	}
	if len(embedding) == 0 {
		embedding = existingEmbedding
	}
	now := time.Now()
	r, err := vs.docToRow(doc, embedding, now)
	if err != nil {
		return err
	}
	if !existing.CreatedAt.IsZero() {
		r.createdAt = existing.CreatedAt
	}
	args, err := vs.insertArgs(r)
	if err != nil {
		return err
	}
	if err := vs.client.Exec(ctx, vs.buildInsertSQL(), args...); err != nil {
		return fmt.Errorf("clickhouse: update: %w", err)
	}
	return nil
}

// Delete removes one document by ID using an ALTER TABLE DELETE mutation.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocumentIDRequired
	}
	sql := fmt.Sprintf("ALTER TABLE %s DELETE WHERE %s = ?", vs.option.tableName, vs.option.idFieldName)
	if err := vs.client.Exec(ctx, sql, id); err != nil {
		return fmt.Errorf("clickhouse: delete: %w", err)
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

// buildCreateTableSQL builds the idempotent CREATE TABLE statement, including
// dedicated columns for every declared filter field.
func (vs *VectorStore) buildCreateTableSQL() string {
	o := vs.option
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(o.tableName)
	sb.WriteString(" (\n")
	sb.WriteString(fmt.Sprintf("    %s String,\n", o.idFieldName))
	sb.WriteString(fmt.Sprintf("    %s String,\n", o.nameFieldName))
	sb.WriteString(fmt.Sprintf("    %s String,\n", o.contentFieldName))
	sb.WriteString(fmt.Sprintf("    %s Array(Float64),\n", o.embeddingFieldName))
	sb.WriteString(fmt.Sprintf("    %s String,\n", o.metadataFieldName))
	sb.WriteString(fmt.Sprintf("    %s DateTime64(6),\n", o.createdAtFieldName))
	sb.WriteString(fmt.Sprintf("    %s DateTime64(6)", o.updatedAtFieldName))
	for _, spec := range o.filterFields {
		sb.WriteString(fmt.Sprintf(",\n    %s %s", spec.Name, spec.Type.clickhouseType()))
	}
	sb.WriteString("\n) ENGINE = ReplacingMergeTree(")
	sb.WriteString(o.updatedAtFieldName)
	sb.WriteString(")\nORDER BY ")
	sb.WriteString(o.idFieldName)
	return sb.String()
}

// selectColumns returns the ordered column list used by SELECT statements.
func (vs *VectorStore) selectColumns() []string {
	o := vs.option
	cols := []string{
		o.idFieldName,
		o.nameFieldName,
		o.contentFieldName,
		o.embeddingFieldName,
		o.metadataFieldName,
		o.createdAtFieldName,
		o.updatedAtFieldName,
	}
	for _, spec := range o.filterFields {
		cols = append(cols, spec.Name)
	}
	return cols
}

// buildSelectSQL builds "SELECT <cols> FROM <table> FINAL".
func (vs *VectorStore) buildSelectSQL() string {
	return fmt.Sprintf("SELECT %s FROM %s FINAL", strings.Join(vs.selectColumns(), ", "), vs.option.tableName)
}

// buildInsertSQL builds "INSERT INTO <table> (<cols>) VALUES (?, ?, ...)".
func (vs *VectorStore) buildInsertSQL() string {
	cols := vs.selectColumns()
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		vs.option.tableName, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
}

// insertArgs builds the ordered argument list for buildInsertSQL from a row.
func (vs *VectorStore) insertArgs(r *row) ([]any, error) {
	metadataJSON, err := marshalMetadata(r.metadata)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: marshal metadata: %w", err)
	}
	args := []any{r.id, r.name, r.content, r.embedding, metadataJSON, r.createdAt, r.updatedAt}
	filterVals, err := vs.filterFieldValues(r.metadata)
	if err != nil {
		return nil, err
	}
	args = append(args, filterVals...)
	return args, nil
}

// scanRow scans the current driver.Rows row into an internal row, decoding the
// JSON metadata and merging the declared filter-field columns back into it.
//
// When scorePtr is non-nil, an additional trailing Float64 column (the vector
// distance/product) is scanned into it. This is used by vector search, which
// appends the distance expression as the final SELECT column.
func (vs *VectorStore) scanRow(rows driver.Rows, scorePtr *float64) (*row, error) {
	r := &row{}
	var metadataStr string
	filterVals := make([]any, len(vs.option.filterFields))
	targets := []any{&r.id, &r.name, &r.content, &r.embedding, &metadataStr, &r.createdAt, &r.updatedAt}
	for i := range vs.option.filterFields {
		targets = append(targets, &filterVals[i])
	}
	if scorePtr != nil {
		targets = append(targets, scorePtr)
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, fmt.Errorf("clickhouse: scan row: %w", err)
	}
	md, err := unmarshalMetadata(metadataStr)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: decode metadata: %w", err)
	}
	r.metadata = md
	for i, spec := range vs.option.filterFields {
		if filterVals[i] != nil {
			r.metadata[spec.Name] = filterVals[i]
		}
	}
	return r, nil
}
