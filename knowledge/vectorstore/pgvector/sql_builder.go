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
	"encoding/json"
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
	if len(b.conditions) > 1 {
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
	setParts []string
}

func newUpdateBuilder(o options, id string) *updateBuilder {
	return &updateBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:        o,
			args:     []any{id, time.Now().Unix()},
			argIndex: 3,
		},
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

type metadataUpdate struct {
	fieldArgIndex int // argument index for field name (used in ARRAY[$n])
	valueArgIndex int // argument index for value (jsonb)
}

// updateByFilterBuilder builds UPDATE SQL statements with filter conditions.
type updateByFilterBuilder struct {
	*baseSQLBuilder
	setParts        []string
	metadataUpdates []metadataUpdate
}

// newUpdateByFilterBuilder creates a builder for UPDATE operations with filters.
func newUpdateByFilterBuilder(o options) *updateByFilterBuilder {
	return &updateByFilterBuilder{
		baseSQLBuilder: &baseSQLBuilder{
			o:          o,
			conditions: []string{"1=1"},
			args:       []any{time.Now().Unix()},
			argIndex:   2,
		},
		setParts:        []string{o.updatedAtFieldName + " = $1"},
		metadataUpdates: make([]metadataUpdate, 0),
	}
}

// addField adds a field to update.
func (ub *updateByFilterBuilder) addField(field string, value any) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("%s = $%d", field, ub.argIndex))
	ub.args = append(ub.args, value)
	ub.argIndex++
}

// addMetadataField updates a specific metadata field using jsonb_set.
// field should be the metadata key (without "metadata." prefix).
// Uses SQL ARRAY constructor with parameterized field name to prevent injection.
func (ub *updateByFilterBuilder) addMetadataField(field string, value any) error {
	// Convert value to JSON string for jsonb_set
	jsonValue, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata value for field %q: %w", field, err)
	}

	// Store metadata update with parameterized field name and value
	// Uses ARRAY[$n]::text[] in SQL to construct the path, avoiding driver-specific array types
	fieldArgIndex := ub.argIndex
	ub.args = append(ub.args, field) // plain string, SQL will wrap it in ARRAY[]
	ub.argIndex++

	valueArgIndex := ub.argIndex
	ub.args = append(ub.args, string(jsonValue)) // value as jsonb
	ub.argIndex++

	ub.metadataUpdates = append(ub.metadataUpdates, metadataUpdate{
		fieldArgIndex: fieldArgIndex,
		valueArgIndex: valueArgIndex,
	})

	return nil
}

// addEmbeddingField adds an embedding field to update.
func (ub *updateByFilterBuilder) addEmbeddingField(value []float64) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("%s = $%d", ub.o.embeddingFieldName, ub.argIndex))
	// Convert float64 to float32 for pgvector
	float32Vec := make([]float32, len(value))
	for i, v := range value {
		float32Vec[i] = float32(v)
	}
	ub.args = append(ub.args, pgvector.NewVector(float32Vec))
	ub.argIndex++
}

// build builds the UPDATE query with all conditions.
func (ub *updateByFilterBuilder) build() (string, []any) {
	// Combine all metadata updates into a single assignment using chained jsonb_set
	// Uses ARRAY[$n]::text[] to construct path from parameterized field name
	// This approach is driver-agnostic and prevents injection
	if len(ub.metadataUpdates) > 0 {
		expr := fmt.Sprintf("COALESCE(%s, '{}'::jsonb)", ub.o.metadataFieldName)
		for _, update := range ub.metadataUpdates {
			expr = fmt.Sprintf("jsonb_set(%s, ARRAY[$%d]::text[], $%d::jsonb)", expr, update.fieldArgIndex, update.valueArgIndex)
		}
		ub.setParts = append(ub.setParts, fmt.Sprintf("%s = %s", ub.o.metadataFieldName, expr))
	}

	whereClause := strings.Join(ub.conditions, " AND ")
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE %s`, ub.o.table, strings.Join(ub.setParts, ", "), whereClause)
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
		selectClause: fmt.Sprintf("%s, 0.0 as vector_score, 0.0 as text_score, 0.0 as score", commonFieldsStr),
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
		qb.orderClause = "ORDER BY (vector_score * " + fmt.Sprintf("%.3f", vectorWeight) + " + text_score * " + fmt.Sprintf("%.3f", textWeight) + ") DESC"
	case vectorstore.SearchModeFilter:
		qb.addSelectClause("0.0 as vector_score, 0.0 as text_score, 1.0 as score")
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
		scoreExpr := qb.getKeywordScoreExpr()
		scoreCondition := fmt.Sprintf("%s >= $%d", scoreExpr, qb.argIndex)
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
	var scoreExpr string
	switch qb.searchMode {
	case vectorstore.SearchModeVector:
		scoreExpr = qb.getVectorScoreExpr()
	case vectorstore.SearchModeHybrid:
		scoreExpr = qb.getHybridScoreExpr()
	default:
		// Fallback for unexpected modes, though likely unused
		scoreExpr = qb.getVectorScoreExpr()
	}

	condition := fmt.Sprintf("%s >= %f", scoreExpr, minScore)
	qb.conditions = append(qb.conditions, condition)
}

// addFtsCondition is a helper to add full-text search conditions.
func (qb *queryBuilder) addFtsCondition(query string) {
	condition := fmt.Sprintf("to_tsvector('%s', %s) @@ %s('%s', $%d)",
		qb.o.language, qb.o.contentFieldName,
		qb.o.sparseQueryFunc, qb.o.language, qb.argIndex)
	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, query)
	qb.argIndex++
}

// build constructs the final SQL query based on the search mode.
func (qb *queryBuilder) build(limit int) (string, []any) {
	whereClause := strings.Join(qb.conditions, " AND ")

	// Use subquery for vector/keyword/hybrid search to avoid duplicate calculations
	switch qb.searchMode {
	case vectorstore.SearchModeVector:
		return qb.buildVectorQueryWithSubquery(whereClause, limit)
	case vectorstore.SearchModeKeyword:
		return qb.buildKeywordQueryWithSubquery(whereClause, limit)
	case vectorstore.SearchModeHybrid:
		return qb.buildHybridQueryWithSubquery(whereClause, limit)
	default:
		// Filter search or other modes use direct query
		finalSelectClause := qb.buildSelectClause()
		sql := fmt.Sprintf(`
			SELECT %s
			FROM %s
			WHERE %s
			%s
			LIMIT %d`, finalSelectClause, qb.o.table, whereClause, qb.orderClause, limit)
		return sql, qb.args
	}
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
	vExpr := qb.getVectorScoreExpr()
	return fmt.Sprintf("%s, %s as vector_score, 0.0 as text_score, %s as score", commonFieldsStr, vExpr, vExpr)
}

// buildVectorQueryWithSubquery generates vector search query using subquery.
// Inner query calculates vector_score once, applies ORDER BY and LIMIT for performance.
// Outer query reuses the computed score to avoid duplicate calculations.
func (qb *queryBuilder) buildVectorQueryWithSubquery(whereClause string, limit int) (string, []any) {
	vExpr := qb.getVectorScoreExpr()

	// Use vector index operator for efficient ordering, then expose vector_score for outer query
	sql := fmt.Sprintf(`
		SELECT *, vector_score as score
		FROM (
			SELECT %s, %s as vector_score, 0.0 as text_score
			FROM %s
			WHERE %s
			ORDER BY %s <=> $1
			LIMIT %d
		) subq`, commonFieldsStr, vExpr, qb.o.table, whereClause, qb.o.embeddingFieldName, limit)

	return sql, qb.args
}

// buildHybridQueryWithSubquery generates hybrid search query using subquery.
// Inner query calculates vector_score and text_score once, then orders by hybrid score.
// Outer query reuses the computed scores to avoid duplicate calculations.
func (qb *queryBuilder) buildHybridQueryWithSubquery(whereClause string, limit int) (string, []any) {
	vExpr := qb.getVectorScoreExpr()

	tExpr := "0.0"
	if qb.textQueryPos > 0 {
		rankExpr := fmt.Sprintf("COALESCE(%s(to_tsvector('%s', %s), %s('%s', $%d)), 0)",
			qb.o.sparseRankFunc, qb.o.language, qb.o.contentFieldName,
			qb.o.sparseQueryFunc, qb.o.language, qb.textQueryPos)
		tExpr = fmt.Sprintf("(%s / (%s + %.4f))", rankExpr, rankExpr, qb.o.sparseNormConstant)
	}

	hybridScoreExpr := fmt.Sprintf("(%s * %.3f + %s * %.3f)", vExpr, qb.vectorWeight, tExpr, qb.textWeight)

	sql := fmt.Sprintf(`
		SELECT *, (vector_score * %.3f + text_score * %.3f) as score
		FROM (
			SELECT %s, %s as vector_score, %s as text_score
			FROM %s
			WHERE %s
			ORDER BY %s DESC
			LIMIT %d
		) subq`, qb.vectorWeight, qb.textWeight, commonFieldsStr, vExpr, tExpr, qb.o.table, whereClause, hybridScoreExpr, limit)

	return sql, qb.args
}

// buildHybridSelectClause generates SELECT clause for hybrid search.
func (qb *queryBuilder) buildHybridSelectClause() string {
	vExpr := qb.getVectorScoreExpr()

	tExpr := "0.0"
	if qb.textQueryPos > 0 {
		rankExpr := fmt.Sprintf("COALESCE(%s(to_tsvector('%s', %s), %s('%s', $%d)), 0)",
			qb.o.sparseRankFunc, qb.o.language, qb.o.contentFieldName,
			qb.o.sparseQueryFunc, qb.o.language, qb.textQueryPos)
		tExpr = fmt.Sprintf("(%s / (%s + %.4f))", rankExpr, rankExpr, qb.o.sparseNormConstant)
	}

	hybridScoreExpr := fmt.Sprintf("(%s * %.3f + %s * %.3f)", vExpr, qb.vectorWeight, tExpr, qb.textWeight)

	return fmt.Sprintf("%s, %s as vector_score, %s as text_score, %s as score",
		commonFieldsStr, vExpr, tExpr, hybridScoreExpr)
}

// buildKeywordSelectClause generates SELECT clause for keyword search.
func (qb *queryBuilder) buildKeywordSelectClause() string {
	if qb.textQueryPos > 0 {
		tExpr := qb.getKeywordScoreExpr()
		return fmt.Sprintf("%s, 0.0 as vector_score, %s as text_score, %s as score", commonFieldsStr, tExpr, tExpr)
	}
	return fmt.Sprintf("%s, 0.0 as vector_score, 0.0 as text_score, 0.0 as score", commonFieldsStr)
}

// buildKeywordQueryWithSubquery generates keyword search query using subquery.
// Inner query calculates text_score once, applies ORDER BY and LIMIT for performance.
// Outer query reuses the computed score to avoid duplicate calculations.
func (qb *queryBuilder) buildKeywordQueryWithSubquery(whereClause string, limit int) (string, []any) {
	if qb.textQueryPos <= 0 {
		// No text query, return simple query
		sql := fmt.Sprintf(`
			SELECT %s, 0.0 as vector_score, 0.0 as text_score, 0.0 as score
			FROM %s
			WHERE %s
			ORDER BY %s DESC
			LIMIT %d`, commonFieldsStr, qb.o.table, whereClause, qb.o.createdAtFieldName, limit)
		return sql, qb.args
	}

	tExpr := qb.getKeywordScoreExpr()

	sql := fmt.Sprintf(`
		SELECT *, text_score as score
		FROM (
			SELECT %s, 0.0 as vector_score, %s as text_score
			FROM %s
			WHERE %s
			ORDER BY %s DESC, %s DESC
			LIMIT %d
		) subq`, commonFieldsStr, tExpr, qb.o.table, whereClause, tExpr, qb.o.createdAtFieldName, limit)

	return sql, qb.args
}

// Helper methods to generate score expressions
// These ensure consistency between SELECT clauses and WHERE filters

// getVectorScoreExpr returns the expression for normalized vector similarity score [0, 1].
//
// Mathematical derivation:
// - Cosine Distance d ∈ [0, 2]: d = 1 - cosine_similarity
//   - d = 0: vectors are identical
//   - d = 1: vectors are orthogonal
//   - d = 2: vectors are opposite
//
// - Cosine Similarity s = 1 - d ∈ [-1, 1]
// - Normalized Score = (s + 1) / 2 = (2 - d) / 2 = 1 - d/2 ∈ [0, 1]
//
// This normalization maps cosine distance [0, 2] to a similarity score [0, 1],
// where higher scores indicate greater similarity.
func (qb *queryBuilder) getVectorScoreExpr() string {
	return fmt.Sprintf("(1.0 - (%s <=> $1) / 2.0)", qb.o.embeddingFieldName)
}

// getKeywordScoreExpr returns the expression for normalized text rank score [0, 1)
// Formula: rank / (rank + c) where c is sparseNormConstant
func (qb *queryBuilder) getKeywordScoreExpr() string {
	if qb.textQueryPos <= 0 {
		return "0.0"
	}
	rankExpr := fmt.Sprintf("%s(to_tsvector('%s', %s), %s('%s', $%d))",
		qb.o.sparseRankFunc, qb.o.language, qb.o.contentFieldName,
		qb.o.sparseQueryFunc, qb.o.language, qb.textQueryPos)

	// Use COALESCE to handle potential nulls if used in contexts where match isn't guaranteed (though usually is)
	return fmt.Sprintf("(%s / (%s + %.4f))", rankExpr, rankExpr, qb.o.sparseNormConstant)
}

// getHybridScoreExpr returns the expression for weighted hybrid score [0, 1]
// Formula: (vector_score * wv) + (text_score * wt)
func (qb *queryBuilder) getHybridScoreExpr() string {
	vExpr := qb.getVectorScoreExpr()

	if qb.textQueryPos > 0 {
		rankExpr := fmt.Sprintf("COALESCE(%s(to_tsvector('%s', %s), %s('%s', $%d)), 0)",
			qb.o.sparseRankFunc, qb.o.language, qb.o.contentFieldName,
			qb.o.sparseQueryFunc, qb.o.language, qb.textQueryPos)
		tExpr := fmt.Sprintf("(%s / (%s + %.4f))", rankExpr, rankExpr, qb.o.sparseNormConstant)
		return fmt.Sprintf("(%s * %.3f + %s * %.3f)", vExpr, qb.vectorWeight, tExpr, qb.textWeight)
	}

	return fmt.Sprintf("(%s * %.3f)", vExpr, qb.vectorWeight)
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
		SELECT *, 0.0 as vector_score, 0.0 as text_score, 0.0 as score
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
