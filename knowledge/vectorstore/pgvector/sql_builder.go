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

// Common field list for SELECT clauses
var commonFieldsStr = fmt.Sprintf("%s, %s, %s, %s, %s, %s, %s",
	fieldID, fieldName, fieldContent, fieldVector, fieldMetadata, fieldCreatedAt, fieldUpdatedAt)

// Use SearchMode from vectorstore package

// updateBuilder builds UPDATE SQL statements safely
type updateBuilder struct {
	table    string
	id       string
	setParts []string
	args     []interface{}
	argIndex int
}

func newUpdateBuilder(table, id string) *updateBuilder {
	return &updateBuilder{
		table:    table,
		id:       id,
		setParts: []string{"updated_at = $2"},
		args:     []interface{}{id, time.Now().Unix()},
		argIndex: 3,
	}
}

func (ub *updateBuilder) addField(field string, value interface{}) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("%s = $%d", field, ub.argIndex))
	ub.args = append(ub.args, value)
	ub.argIndex++
}

func (ub *updateBuilder) build() (string, []interface{}) {
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1`, ub.table, strings.Join(ub.setParts, ", "))
	return sql, ub.args
}

// queryBuilder builds SQL queries safely without string concatenation
// It supports different search modes: vector, keyword, hybrid, and filter
type queryBuilder struct {
	// Basic query components
	table        string
	conditions   []string
	args         []interface{}
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
		args:         make([]interface{}, 0),
		argIndex:     1,
		selectClause: commonFieldsStr,
		language:     language,
	}
}

// newVectorQueryBuilder creates a builder for pure vector similarity search
func newVectorQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeVector, 0, 0)
}

// newKeywordQueryBuilder creates a builder for full-text search
func newKeywordQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeKeyword, 0, 0)
}

// newHybridQueryBuilder creates a builder for hybrid search (vector + text)
func newHybridQueryBuilder(table string, language string, vectorWeight, textWeight float64) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeHybrid, vectorWeight, textWeight)
}

// newFilterQueryBuilder creates a builder for filter-only search
func newFilterQueryBuilder(table string, language string) *queryBuilder {
	return newQueryBuilderWithMode(table, language, vectorstore.SearchModeFilter, 0, 0)
}

// deleteQueryBuilder builds DELETE SQL statements safely
type deleteQueryBuilder struct {
	table      string
	conditions []string
	args       []interface{}
	argIndex   int
}

// newDeleteQueryBuilder creates a builder for DELETE operations
func newDeleteQueryBuilder(table string) *deleteQueryBuilder {
	return &deleteQueryBuilder{
		table:      table,
		conditions: []string{"1=1"},
		args:       make([]interface{}, 0),
		argIndex:   1,
	}
}

// addMetadataFilter adds metadata filter conditions to the delete query
func (dqb *deleteQueryBuilder) addMetadataFilter(metadata map[string]interface{}) {
	if len(metadata) == 0 {
		return
	}

	condition := fmt.Sprintf("metadata @> $%d::jsonb", dqb.argIndex)
	dqb.conditions = append(dqb.conditions, condition)

	metadataJSON := mapToJSON(metadata)
	dqb.args = append(dqb.args, metadataJSON)
	dqb.argIndex++
}

// buildDeleteQuery builds the actual DELETE query
func (dqb *deleteQueryBuilder) buildDeleteQuery() (string, []interface{}) {
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", dqb.table, strings.Join(dqb.conditions, " AND "))
	return sql, dqb.args
}

// newQueryBuilderWithMode creates a query builder with specific search mode and weights
func newQueryBuilderWithMode(table, language string, mode vectorstore.SearchMode, vectorWeight, textWeight float64) *queryBuilder {
	qb := newQueryBuilder(table, language)
	qb.searchMode = mode
	qb.vectorWeight = vectorWeight
	qb.textWeight = textWeight

	// Set mode-specific configurations
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

// addKeywordSearchConditions adds both full-text search matching and optional score filtering conditions
func (qb *queryBuilder) addKeywordSearchConditions(query string, minScore float64) {
	qb.textQueryPos = qb.argIndex

	// Add full-text search condition
	qb.addFtsCondition(query)

	// Add score filter if needed
	if minScore > 0 {
		scoreCondition := fmt.Sprintf("ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d)) >= $%d",
			qb.language, qb.language, qb.textQueryPos, qb.argIndex)
		qb.conditions = append(qb.conditions, scoreCondition)
		qb.args = append(qb.args, minScore)
		qb.argIndex++
	}
}

// addHybridFtsCondition adds full-text search condition for hybrid search
func (qb *queryBuilder) addHybridFtsCondition(query string) {
	qb.textQueryPos = qb.argIndex
	qb.addFtsCondition(query)
}

func (qb *queryBuilder) addVectorArg(vector pgvector.Vector) {
	qb.args = append(qb.args, vector)
	qb.argIndex++
}

// addSelectClause is a helper method to add the select clause with score calculation
func (qb *queryBuilder) addSelectClause(scoreExpression string) {
	qb.selectClause = fmt.Sprintf("%s, %s", commonFieldsStr, scoreExpression)
}

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

// addMetadataFilter uses @> operator for more efficient JSONB queries
// This method is more performant when you have GIN index on metadata column
func (qb *queryBuilder) addMetadataFilter(metadata map[string]interface{}) {
	if len(metadata) == 0 {
		return
	}

	// Use @> operator for containment check, more efficient with GIN index
	// Cast the parameter to JSONB to ensure proper type matching
	condition := fmt.Sprintf("metadata @> $%d::jsonb", qb.argIndex)
	qb.conditions = append(qb.conditions, condition)

	// Convert map to JSON string for @> operator
	metadataJSON := mapToJSON(metadata)
	qb.args = append(qb.args, metadataJSON)
	qb.argIndex++
}

func (qb *queryBuilder) addScoreFilter(minScore float64) {
	condition := fmt.Sprintf("(1 - (embedding <=> $1)) >= %f", minScore)
	qb.conditions = append(qb.conditions, condition)
}

// addFtsCondition is a helper to add full-text search conditions
func (qb *queryBuilder) addFtsCondition(query string) {
	condition := fmt.Sprintf("to_tsvector('%s', content) @@ plainto_tsquery('%s', $%d)", qb.language, qb.language, qb.argIndex)
	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, query)
	qb.argIndex++
}

// build constructs the final SQL query based on the search mode
func (qb *queryBuilder) build(limit int) (string, []interface{}) {
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

// buildSelectClause generates the appropriate SELECT clause based on search mode
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

// buildVectorSelectClause generates SELECT clause for vector search
func (qb *queryBuilder) buildVectorSelectClause() string {
	return fmt.Sprintf("%s, 1 - (embedding <=> $1) as score", commonFieldsStr)
}

// buildHybridSelectClause generates SELECT clause for hybrid search
func (qb *queryBuilder) buildHybridSelectClause() string {
	var scoreExpr string
	if qb.textQueryPos > 0 {
		// Hybrid search: vector + text
		scoreExpr = fmt.Sprintf(
			"(1 - (embedding <=> $1)) * %.3f + ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d)) * %.3f",
			qb.vectorWeight, qb.language, qb.language, qb.textQueryPos, qb.textWeight)
	} else {
		// Pure vector search: only vector similarity
		scoreExpr = fmt.Sprintf("(1 - (embedding <=> $1)) * %.3f", qb.vectorWeight)
	}
	return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
}

// buildKeywordSelectClause generates SELECT clause for keyword search
func (qb *queryBuilder) buildKeywordSelectClause() string {
	if qb.textQueryPos > 0 {
		scoreExpr := fmt.Sprintf(
			"ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d))",
			qb.language, qb.language, qb.textQueryPos)
		return fmt.Sprintf("%s, %s as score", commonFieldsStr, scoreExpr)
	}
	return fmt.Sprintf("%s, 0.0 as score", commonFieldsStr)
}
