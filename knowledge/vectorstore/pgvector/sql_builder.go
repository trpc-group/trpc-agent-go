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
var commonFieldsStr = "*"

// queryFilterBuilder is an interface for building query filters safely.
type queryFilterBuilder interface {
	// addIDFilter adds ID filter to the query.
	addIDFilter(ids []string)
	// addMetadataFilter adds metadata filter to the query.
	addMetadataFilter(metadata map[string]any)
	// addFilterCondition adds a custom filter condition to the query.
	addFilterCondition(*condConvertResult)
}

type baseSQLBuilder struct {
	o          options
	conditions []string
	args       []any
	argIndex   int
}

func (b *baseSQLBuilder) addFilterCondition(cond *condConvertResult) {
	if cond == nil || cond.cond == "" {
		return
	}

	argNum := len(cond.args)
	indexes := make([]any, argNum)
	for i := 0; i < argNum; i++ {
		indexes[i] = b.argIndex
		b.argIndex++
	}
	c := fmt.Sprintf(cond.cond, indexes...)
	if len(b.conditions) > 0 {
		c = fmt.Sprintf("(%s)", c)
	}
	b.conditions = append(b.conditions, c)
	b.args = append(b.args, cond.args...)
}

// addIDFilter adds ID filter to the query.
func (b *baseSQLBuilder) addIDFilter(ids []string) {
	if len(ids) == 0 {
		return
	}

	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", b.argIndex)
		b.args = append(b.args, id)
		b.argIndex++
	}

	condition := fmt.Sprintf("%s IN (%s)", b.o.idFieldName, strings.Join(placeholders, ", "))
	b.conditions = append(b.conditions, condition)
}

// addMetadataFilter uses @> operator for more efficient JSONB queries.
// This method is more performant when you have GIN index on metadata column.
func (b *baseSQLBuilder) addMetadataFilter(metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}

	// Use @> operator for containment check, more efficient with GIN index.
	// Cast the parameter to JSONB to ensure proper type matching.
	condition := fmt.Sprintf("%s @> $%d::jsonb", b.o.metadataFieldName, b.argIndex)
	b.conditions = append(b.conditions, condition)

	// Convert map to JSON string for @> operator.
	metadataJSON := mapToJSON(metadata)
	b.args = append(b.args, metadataJSON)
	b.argIndex++
}

// Use SearchMode from vectorstore package.

// updateBuilder builds UPDATE SQL statements safely.
type updateBuilder struct {
	*baseSQLBuilder
	table    string
	id       string
	setParts []string
}

func newUpdateBuilder(o options, id string) *updateBuilder {
	return &updateBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:        o,
			args:     []any{id, time.Now().Unix()},
			argIndex: 3,
		},
		id:       id,
		setParts: []string{o.updatedAtFieldName + " = $2"},
	}
}

func (ub *updateBuilder) addField(field string, value any) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("%s = $%d", field, ub.argIndex))
	ub.args = append(ub.args, value)
	ub.argIndex++
}

func (ub *updateBuilder) build() (string, []any) {
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE %s = $1`, ub.o.table, strings.Join(ub.setParts, ", "), ub.o.idFieldName)
	return sql, ub.args
}

// queryBuilder builds SQL queries safely without string concatenation.
// It supports different search modes: vector, keyword, hybrid, and filter.
type queryBuilder struct {
	// Basic query components
	*baseSQLBuilder
	orderClause  string
	selectClause string

	// Search mode specific fields
	searchMode   vectorstore.SearchMode // Type of search being performed
	vectorWeight float64                // Weight for vector similarity score (hybrid search)
	textWeight   float64                // Weight for text relevance score (hybrid search)

	// Track text query position for scoring, to avoid transfer text duplicate
	textQueryPos int
}

func newQueryBuilder(o options) *queryBuilder {
	return &queryBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:          o,
			conditions: []string{"1=1"},
			args:       make([]any, 0),
			argIndex:   1,
		},
		selectClause: fmt.Sprintf("%s, 0.0 as score", commonFieldsStr),
	}
}

// newVectorQueryBuilder creates a builder for pure vector similarity search.
func newVectorQueryBuilder(o options) *queryBuilder {
	return newQueryBuilderWithMode(o, vectorstore.SearchModeVector, 0, 0)
}

// newKeywordQueryBuilder creates a builder for full-text search.
func newKeywordQueryBuilder(o options) *queryBuilder {
	return newQueryBuilderWithMode(o, vectorstore.SearchModeKeyword, 0, 0)
}

// newHybridQueryBuilder creates a builder for hybrid search (vector + text).
func newHybridQueryBuilder(o options, vectorWeight, textWeight float64) *queryBuilder {
	return newQueryBuilderWithMode(o, vectorstore.SearchModeHybrid, vectorWeight, textWeight)
}

// newFilterQueryBuilder creates a builder for filter-only search.
func newFilterQueryBuilder(o options) *queryBuilder {
	return newQueryBuilderWithMode(o, vectorstore.SearchModeFilter, 0, 0)
}

// deleteSQLBuilder builds DELETE SQL statements safely with comprehensive filter support
type deleteSQLBuilder struct {
	*baseSQLBuilder
}

// newDeleteSQLBuilder creates a builder for DELETE operations
func newDeleteSQLBuilder(o options) *deleteSQLBuilder {
	return &deleteSQLBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:          o,
			conditions: []string{"1=1"},
			args:       make([]any, 0),
			argIndex:   1,
		},
	}
}

// build builds the DELETE query with all conditions
func (dsb *deleteSQLBuilder) build() (string, []any) {
	whereClause := strings.Join(dsb.conditions, " AND ")
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", dsb.o.table, whereClause)
	return sql, dsb.args
}

// newQueryBuilderWithMode creates a query builder with specific search mode and weights
func newQueryBuilderWithMode(o options, mode vectorstore.SearchMode, vectorWeight, textWeight float64) *queryBuilder {
	qb := newQueryBuilder(o)
	qb.searchMode = mode
	qb.vectorWeight = vectorWeight
	qb.textWeight = textWeight

	// Set mode-specific configurations.
	switch mode {
	case vectorstore.SearchModeVector:
		qb.orderClause = fmt.Sprintf("ORDER BY %s <=> $1", o.embeddingFieldName)
	case vectorstore.SearchModeKeyword:
		qb.orderClause = fmt.Sprintf("ORDER BY score DESC, %s DESC", o.createdAtFieldName)
	case vectorstore.SearchModeHybrid:
		qb.orderClause = "ORDER BY score DESC"
	case vectorstore.SearchModeFilter:
		qb.addSelectClause("1.0 as score")
		qb.orderClause = fmt.Sprintf("ORDER BY %s DESC", o.createdAtFieldName)
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
		scoreCondition := fmt.Sprintf("ts_rank_cd(to_tsvector('%s', %s), plainto_tsquery('%s', $%d)) >= $%d",
			qb.o.language, qb.o.contentFieldName,
			qb.o.language, qb.textQueryPos,
			qb.argIndex)
		qb.conditions = append(qb.conditions, scoreCondition)
		qb.args = append(qb.args, minScore)
		qb.argIndex++
	}
}

// addHybridFtsCondition sets up text query for hybrid search scoring.
func (qb *queryBuilder) addHybridFtsCondition(query string) {
	qb.textQueryPos = qb.argIndex
	qb.args = append(qb.args, query)
	qb.argIndex++
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

// addScoreFilter adds score filter to the query.
func (qb *queryBuilder) addScoreFilter(minScore float64) {
	condition := fmt.Sprintf("(1 - (%s <=> $1)) >= %f", qb.o.embeddingFieldName, minScore)
	qb.conditions = append(qb.conditions, condition)
}

// addFtsCondition is a helper to add full-text search conditions.
func (qb *queryBuilder) addFtsCondition(query string) {
	condition := fmt.Sprintf("to_tsvector('%s', %s) @@ plainto_tsquery('%s', $%d)",
		qb.o.language, qb.o.contentFieldName,
		qb.o.language, qb.argIndex)
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
		LIMIT %d`, finalSelectClause, qb.o.table, whereClause, qb.orderClause, limit)

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
	return fmt.Sprintf("%s, 1 - (%s <=> $1) as score", commonFieldsStr, qb.o.embeddingFieldName)
}

// buildHybridSelectClause generates SELECT clause for hybrid search.
// Uses COALESCE to handle cases where text search doesn't match, returning 0 for text score.
func (qb *queryBuilder) buildHybridSelectClause() string {
	var scoreExpr string
	if qb.textQueryPos > 0 {
		// Hybrid search: vector + text.
		// Use COALESCE to return 0 for text score when there's no match.
		scoreExpr = fmt.Sprintf(
			"(1 - (%s <=> $1)) * %.3f + COALESCE(ts_rank_cd(to_tsvector('%s', %s), plainto_tsquery('%s', $%d)), 0) * %.3f",
			qb.o.embeddingFieldName, qb.vectorWeight,
			qb.o.language, qb.o.contentFieldName,
			qb.o.language, qb.textQueryPos, qb.textWeight)
	} else {
		// Pure vector search: only vector similarity.
		scoreExpr = fmt.Sprintf("(1 - (%s <=> $1)) * %.3f", qb.o.embeddingFieldName, qb.vectorWeight)
	}
	return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
}

// buildKeywordSelectClause generates SELECT clause for keyword search.
func (qb *queryBuilder) buildKeywordSelectClause() string {
	if qb.textQueryPos > 0 {
		scoreExpr := fmt.Sprintf(
			"ts_rank_cd(to_tsvector('%s', %s), plainto_tsquery('%s', $%d))",
			qb.o.language, qb.o.contentFieldName,
			qb.o.language, qb.textQueryPos)
		return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
	}
	return fmt.Sprintf("%s, 0.0 as score", commonFieldsStr)
}

// metadataQueryBuilder builds SQL queries specifically for metadata retrieval
type metadataQueryBuilder struct {
	*baseSQLBuilder
}

// newMetadataQueryBuilder creates a builder for metadata queries
func newMetadataQueryBuilder(o options) *metadataQueryBuilder {
	return &metadataQueryBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:          o,
			conditions: []string{"1=1"},
			args:       make([]any, 0),
			argIndex:   1,
		},
	}
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
		SELECT *, 0.0 as score
		FROM %s
		WHERE %s
		ORDER BY %s
		LIMIT %s OFFSET %s`,
		mqb.o.table, whereClause, mqb.o.createdAtFieldName, limitPlaceholder, offsetPlaceholder)

	return sql, mqb.args
}

// countQueryBuilder builds SQL COUNT queries for document counting
type countQueryBuilder struct {
	*baseSQLBuilder
}

// newCountQueryBuilder creates a builder for count queries
func newCountQueryBuilder(o options) *countQueryBuilder {
	return &countQueryBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:          o,
			conditions: []string{"1=1"},
			args:       make([]any, 0),
			argIndex:   1,
		},
	}
}

// build builds the COUNT query
func (cqb *countQueryBuilder) build() (string, []any) {
	whereClause := strings.Join(cqb.conditions, " AND ")
	sql := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", cqb.o.table, whereClause)
	return sql, cqb.args
}

func buildUpsertSQL(o options) string {
	return fmt.Sprintf(sqlUpsertDocument, o.table,
		o.idFieldName, o.nameFieldName, o.contentFieldName, o.embeddingFieldName, o.metadataFieldName, o.createdAtFieldName, o.updatedAtFieldName,
		o.idFieldName,
		o.nameFieldName, o.nameFieldName,
		o.contentFieldName, o.contentFieldName,
		o.embeddingFieldName, o.embeddingFieldName,
		o.metadataFieldName, o.metadataFieldName,
		o.updatedAtFieldName, o.updatedAtFieldName,
	)
}
