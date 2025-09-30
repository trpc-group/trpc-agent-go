//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Common field list for SELECT clauses.
var commonFieldsStr = fmt.Sprintf("%s, %s, %s, %s, %s, %s, %s",
	fieldID, fieldName, fieldContent, fieldVector, fieldMetadata, fieldCreatedAt, fieldUpdatedAt)

// Use SearchMode from vectorstore package.

// updateBuilder builds UPDATE SQL statements safely.
type updateBuilder struct {
	table    string
	id       string
	setParts []string
	args     []any
	argIndex int
}

func newUpdateBuilder(table, id string) *updateBuilder {
	return &updateBuilder{
		table:    table,
		id:       id,
		setParts: []string{"updated_at = $2"},
		args:     []any{id, time.Now().Unix()},
		argIndex: 3,
	}
}

func (ub *updateBuilder) addField(field string, value any) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("%s = $%d", field, ub.argIndex))
	ub.args = append(ub.args, value)
	ub.argIndex++
}

func (ub *updateBuilder) build() (string, []any) {
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1`, ub.table, strings.Join(ub.setParts, ", "))
	return sql, ub.args
}

// queryBuilder builds SQL queries safely without string concatenation.
// It supports different search modes: vector, keyword, hybrid, and filter.
type queryBuilder struct {
	// Basic query components
	table        string
	conditions   []string
	args         []any
	argIndex     int
	orderClause  string
	selectClause string
	language     string

	// Search mode specific fields
	searchMode   vectorstore.SearchMode // Type of search being performed
	vectorWeight float64                // Weight for vector similarity score (hybrid search)
	textWeight   float64                // Weight for text relevance score (hybrid search)

	// Track text query position for scoring, to avoid transfer text duplicate
	textQueryPos int
}

func newQueryBuilder(table string, language string) *queryBuilder {
	return &queryBuilder{
		table:        table,
		conditions:   []string{"1=1"},
		args:         make([]any, 0),
		argIndex:     1,
		selectClause: commonFieldsStr,
		language:     language,
	}
}

// newVectorQueryBuilder creates a builder for pure vector similarity search.
func newVectorQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeVector, 0, 0)
}

// newKeywordQueryBuilder creates a builder for full-text search.
func newKeywordQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeKeyword, 0, 0)
}

// newHybridQueryBuilder creates a builder for hybrid search (vector + text).
func newHybridQueryBuilder(table string, language string, vectorWeight, textWeight float64) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeHybrid, vectorWeight, textWeight)
}

// newFilterQueryBuilder creates a builder for filter-only search.
func newFilterQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeFilter, 0, 0)
}

// deleteSQLBuilder builds DELETE SQL statements safely with comprehensive filter support
type deleteSQLBuilder struct {
	table      string
	conditions []string
	args       []any
	argIndex   int
}

// newDeleteSQLBuilder creates a builder for DELETE operations
func newDeleteSQLBuilder(table string) *deleteSQLBuilder {
	return &deleteSQLBuilder{
		table:      table,
		conditions: []string{"1=1"},
		args:       make([]any, 0),
		argIndex:   1,
	}
}

// addIDFilter adds document ID filter conditions to the delete query
func (dsb *deleteSQLBuilder) addIDFilter(ids []string) {
	if len(ids) == 0 {
		return
	}

	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", dsb.argIndex)
		dsb.args = append(dsb.args, id)
		dsb.argIndex++
	}

	condition := fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ", "))
	dsb.conditions = append(dsb.conditions, condition)
}

// addMetadataFilter adds metadata filter conditions to the delete query
// Uses @> operator for efficient JSONB queries, same as queryBuilder implementation
func (dsb *deleteSQLBuilder) addMetadataFilter(metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}

	condition := fmt.Sprintf("metadata @> $%d::jsonb", dsb.argIndex)
	dsb.conditions = append(dsb.conditions, condition)

	// Convert map to JSON string for @> operator
	metadataJSON := mapToJSON(metadata)
	dsb.args = append(dsb.args, metadataJSON)
	dsb.argIndex++
}

// build builds the DELETE query with all conditions
func (dsb *deleteSQLBuilder) build() (string, []any) {
	whereClause := strings.Join(dsb.conditions, " AND ")
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", dsb.table, whereClause)
	return sql, dsb.args
}

// newQueryBuilderWithMode creates a query builder with specific search mode and weights
func newQueryBuilderWithMode(table, language string, mode vectorstore.SearchMode, vectorWeight, textWeight float64) *queryBuilder {
	qb := newQueryBuilder(table, language)
	qb.searchMode = mode
	qb.vectorWeight = vectorWeight
	qb.textWeight = textWeight

	// Set mode-specific configurations.
	switch mode {
	case vectorstore.SearchModeVector:
		qb.orderClause = "ORDER BY embedding <=> $1"
	case vectorstore.SearchModeKeyword:
		qb.orderClause = "ORDER BY score DESC, created_at DESC"
	case vectorstore.SearchModeHybrid:
		qb.orderClause = "ORDER BY score DESC"
	case vectorstore.SearchModeFilter:
		qb.addSelectClause("1.0 as score")
		qb.orderClause = "ORDER BY created_at DESC"
	}

	return qb
}

// addKeywordSearchConditions adds both full-text search matching and optional score filtering conditions.
func (qb *queryBuilder) addKeywordSearchConditions(query string, minScore float64) {
	qb.textQueryPos = qb.argIndex

	// Add full-text search condition.
	qb.addFtsCondition(query)

	// Add score filter if needed.
	if minScore > 0 {
		scoreCondition := fmt.Sprintf("ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d)) >= $%d",
			qb.language, qb.language, qb.textQueryPos, qb.argIndex)
		qb.conditions = append(qb.conditions, scoreCondition)
		qb.args = append(qb.args, minScore)
		qb.argIndex++
	}
}

// addHybridFtsCondition adds full-text search condition for hybrid search.
func (qb *queryBuilder) addHybridFtsCondition(query string) {
	qb.textQueryPos = qb.argIndex
	qb.addFtsCondition(query)
}

// addVectorArg adds vector argument to the query.
func (qb *queryBuilder) addVectorArg(vector pgvector.Vector) {
	qb.args = append(qb.args, vector)
	qb.argIndex++
}

// addSelectClause is a helper method to add the select clause with score calculation.
func (qb *queryBuilder) addSelectClause(scoreExpression string) {
	qb.selectClause = fmt.Sprintf("%s, %s", commonFieldsStr, scoreExpression)
}

// addIDFilter adds ID filter to the query.
func (qb *queryBuilder) addIDFilter(ids []string) {
	if len(ids) == 0 {
		return
	}

	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", qb.argIndex)
		qb.args = append(qb.args, id)
		qb.argIndex++
	}

	condition := fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ","))
	qb.conditions = append(qb.conditions, condition)
}

// addMetadataFilter uses @> operator for more efficient JSONB queries.
// This method is more performant when you have GIN index on metadata column.
func (qb *queryBuilder) addMetadataFilter(metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}

	// Use @> operator for containment check, more efficient with GIN index.
	// Cast the parameter to JSONB to ensure proper type matching.
	condition := fmt.Sprintf("metadata @> $%d::jsonb", qb.argIndex)
	qb.conditions = append(qb.conditions, condition)

	// Convert map to JSON string for @> operator.
	metadataJSON := mapToJSON(metadata)
	qb.args = append(qb.args, metadataJSON)
	qb.argIndex++
}

// addScoreFilter adds score filter to the query.
func (qb *queryBuilder) addScoreFilter(minScore float64) {
	condition := fmt.Sprintf("(1 - (embedding <=> $1)) >= %f", minScore)
	qb.conditions = append(qb.conditions, condition)
}

// addFtsCondition is a helper to add full-text search conditions.
func (qb *queryBuilder) addFtsCondition(query string) {
	condition := fmt.Sprintf("to_tsvector('%s', content) @@ plainto_tsquery('%s', $%d)", qb.language, qb.language, qb.argIndex)
	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, query)
	qb.argIndex++
}

// build constructs the final SQL query based on the search mode.
func (qb *queryBuilder) build(limit int) (string, []any) {
	finalSelectClause := qb.buildSelectClause()
	whereClause := strings.Join(qb.conditions, " AND ")

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		LIMIT %d`, finalSelectClause, qb.table, whereClause, qb.orderClause, limit)

	return sql, qb.args
}

// buildSelectClause generates the appropriate SELECT clause based on search mode.
func (qb *queryBuilder) buildSelectClause() string {
	switch qb.searchMode {
	case vectorstore.SearchModeVector:
		return qb.buildVectorSelectClause()
	case vectorstore.SearchModeHybrid:
		return qb.buildHybridSelectClause()
	case vectorstore.SearchModeKeyword:
		return qb.buildKeywordSelectClause()
	default:
		return qb.selectClause
	}
}

// buildVectorSelectClause generates SELECT clause for vector search.
func (qb *queryBuilder) buildVectorSelectClause() string {
	return fmt.Sprintf("%s, 1 - (embedding <=> $1) as score", commonFieldsStr)
}

// buildHybridSelectClause generates SELECT clause for hybrid search.
func (qb *queryBuilder) buildHybridSelectClause() string {
	var scoreExpr string
	if qb.textQueryPos > 0 {
		// Hybrid search: vector + text.
		scoreExpr = fmt.Sprintf(
			"(1 - (embedding <=> $1)) * %.3f + ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d)) * %.3f",
			qb.vectorWeight, qb.language, qb.language, qb.textQueryPos, qb.textWeight)
	} else {
		// Pure vector search: only vector similarity.
		scoreExpr = fmt.Sprintf("(1 - (embedding <=> $1)) * %.3f", qb.vectorWeight)
	}
	return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
}

// buildKeywordSelectClause generates SELECT clause for keyword search.
func (qb *queryBuilder) buildKeywordSelectClause() string {
	if qb.textQueryPos > 0 {
		scoreExpr := fmt.Sprintf(
			"ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d))",
			qb.language, qb.language, qb.textQueryPos)
		return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
	}
	return fmt.Sprintf("%s, 0.0 as score", commonFieldsStr)
}

// metadataQueryBuilder builds SQL queries specifically for metadata retrieval
type metadataQueryBuilder struct {
	table      string
	conditions []string
	args       []any
	argIndex   int
}

// newMetadataQueryBuilder creates a builder for metadata queries
func newMetadataQueryBuilder(table string) *metadataQueryBuilder {
	return &metadataQueryBuilder{
		table:      table,
		conditions: []string{"1=1"},
		args:       make([]any, 0),
		argIndex:   1,
	}
}

// addIDFilter adds document ID filter conditions to the metadata query
func (mqb *metadataQueryBuilder) addIDFilter(ids []string) {
	if len(ids) == 0 {
		return
	}

	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", mqb.argIndex)
		mqb.args = append(mqb.args, id)
		mqb.argIndex++
	}

	condition := fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ", "))
	mqb.conditions = append(mqb.conditions, condition)
}

// addMetadataFilter adds metadata filter conditions to the metadata query
func (mqb *metadataQueryBuilder) addMetadataFilter(metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}

	condition := fmt.Sprintf("metadata @> $%d::jsonb", mqb.argIndex)
	mqb.conditions = append(mqb.conditions, condition)

	// Convert map to JSON string for @> operator
	metadataJSON := mapToJSON(metadata)
	mqb.args = append(mqb.args, metadataJSON)
	mqb.argIndex++
}

// buildWithPagination builds the metadata query with pagination support
func (mqb *metadataQueryBuilder) buildWithPagination(limit, offset int) (string, []any) {
	whereClause := strings.Join(mqb.conditions, " AND ")

	// Add limit and offset as parameters
	limitPlaceholder := fmt.Sprintf("$%d", mqb.argIndex)
	mqb.args = append(mqb.args, limit)
	mqb.argIndex++

	offsetPlaceholder := fmt.Sprintf("$%d", mqb.argIndex)
	mqb.args = append(mqb.args, offset)

	sql := fmt.Sprintf(`
		SELECT id, metadata
		FROM %s
		WHERE %s
		ORDER BY created_at
		LIMIT %s OFFSET %s`,
		mqb.table, whereClause, limitPlaceholder, offsetPlaceholder)

	return sql, mqb.args
}

// countQueryBuilder builds SQL COUNT queries for document counting
type countQueryBuilder struct {
	table      string
	conditions []string
	args       []any
	argIndex   int
}

// newCountQueryBuilder creates a builder for count queries
func newCountQueryBuilder(table string) *countQueryBuilder {
	return &countQueryBuilder{
		table:      table,
		conditions: []string{"1=1"},
		args:       make([]any, 0),
		argIndex:   1,
	}
}

// addMetadataFilter adds metadata filter conditions to the count query
func (cqb *countQueryBuilder) addMetadataFilter(metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}

	condition := fmt.Sprintf("metadata @> $%d::jsonb", cqb.argIndex)
	cqb.conditions = append(cqb.conditions, condition)

	// Convert map to JSON string for @> operator
	metadataJSON := mapToJSON(metadata)
	cqb.args = append(cqb.args, metadataJSON)
	cqb.argIndex++
}

// build builds the COUNT query
func (cqb *countQueryBuilder) build() (string, []any) {
	whereClause := strings.Join(cqb.conditions, " AND ")
	sql := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", cqb.table, whereClause)
	return sql, cqb.args
}
