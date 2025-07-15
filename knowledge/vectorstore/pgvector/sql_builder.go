package pgvector

import (
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
)

var commonFiledsStr = fmt.Sprintf("%s, %s, %s, %s, %s, %s, %s",
	fieldID, fieldName, fieldContent, fieldVector, fieldMetadata, fieldCreatedAt, fieldUpdatedAt)

// updateBuilder builds UPDATE SQL statements safely
type updateBuilder struct {
	table    string
	id       string
	setParts []string
	args     []interface{}
	argIndex int
	language string
}

func newUpdateBuilder(table, id, language string) *updateBuilder {
	return &updateBuilder{
		table:    table,
		id:       id,
		setParts: []string{"updated_at = $2"},
		args:     []interface{}{id, time.Now().Unix()},
		argIndex: 3,
		language: language,
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
type queryBuilder struct {
	table             string
	conditions        []string
	args              []interface{}
	argIndex          int
	havingClause      string
	orderClause       string
	selectClause      string
	language          string
	textQueryArgIndex int // Holds the argument index for the text query.
}

func newQueryBuilder(table string, language string) *queryBuilder {
	return &queryBuilder{
		table:        table,
		conditions:   []string{"1=1"},
		args:         make([]interface{}, 0),
		argIndex:     1,
		selectClause: commonFiledsStr,
		language:     language, // Default to English text search configuration
	}
}

// Vector search builder
func newVectorQueryBuilder(table string, language string) *queryBuilder {
	qb := newQueryBuilder(table, language)
	qb.addSelectClause("1 - (embedding <=> $1) as score")
	qb.orderClause = "ORDER BY embedding <=> $1"
	return qb
}

// Keyword search builder with full-text search scoring
func newKeywordQueryBuilder(table string, language string) *queryBuilder {
	qb := newQueryBuilder(table, language)
	qb.addSelectClause(fmt.Sprintf("ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%%d)) as score", qb.language, qb.language))
	qb.orderClause = "ORDER BY score DESC, created_at DESC"
	return qb
}

// Hybrid search builder (vector + keyword)
func newHybridQueryBuilder(table string, language string, vectorWeight, textWeight float64) *queryBuilder {
	qb := newQueryBuilder(table, language)
	scoreExpression := fmt.Sprintf("(1 - (embedding <=> $1)) * %.3f + ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%%d)) * %.3f as score", vectorWeight, qb.language, qb.language, textWeight)
	qb.addSelectClause(scoreExpression)
	qb.orderClause = "ORDER BY score DESC"
	return qb
}

// addKeywordSearchConditions adds both full-text search matching and optional score filtering conditions
func (qb *queryBuilder) addKeywordSearchConditions(query string, minScore float64) {
	// Capture the argument index for the text query before adding it.
	qb.textQueryArgIndex = qb.argIndex

	// This condition ensures that the query matches the document's content.
	ftsCondition := fmt.Sprintf("to_tsvector('%s', content) @@ plainto_tsquery('%s', $%d)", qb.language, qb.language, qb.argIndex)
	qb.conditions = append(qb.conditions, ftsCondition)

	// If a minimum score is specified, add a condition to filter by it.
	if minScore > 0 {
		scoreCondition := fmt.Sprintf("ts_rank_cd(to_tsvector('%s', content), plainto_tsquery('%s', $%d)) >= $%d",
			qb.language, qb.language, qb.argIndex, qb.argIndex+1)
		qb.conditions = append(qb.conditions, scoreCondition)

		qb.args = append(qb.args, query, minScore)
		qb.argIndex += 2
	} else {
		qb.args = append(qb.args, query)
		qb.argIndex++
	}
}

// addHybridFtsCondition adds full-text search condition for hybrid search
func (qb *queryBuilder) addHybridFtsCondition(query string) {
	// Capture the argument index for the text query. Used for scoring in SELECT.
	qb.textQueryArgIndex = qb.argIndex

	// Add the full-text search condition to the WHERE clause.
	condition := fmt.Sprintf("to_tsvector('%s', content) @@ plainto_tsquery('%s', $%d)", qb.language, qb.language, qb.argIndex)
	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, query)
	qb.argIndex++
}

// Filter-only search builder
func newFilterQueryBuilder(table string, language string) *queryBuilder {
	qb := newQueryBuilder(table, language)
	qb.addSelectClause("1.0 as score")
	qb.orderClause = "ORDER BY created_at DESC"
	return qb
}

func (qb *queryBuilder) addVectorArg(vector pgvector.Vector) {
	qb.args = append(qb.args, vector)
	qb.argIndex++
}

// addSelectClause is a helper method to add the select clause with score calculation
func (qb *queryBuilder) addSelectClause(scoreExpression string) {
	qb.selectClause = fmt.Sprintf("%s, %s", commonFiledsStr, scoreExpression)
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
	// Use WHERE clause instead of HAVING since we don't need aggregation
	condition := fmt.Sprintf("(1 - (embedding <=> $1)) >= %f", minScore)
	qb.conditions = append(qb.conditions, condition)
}

// build constructs the final SQL query.
func (qb *queryBuilder) build(limit int) (string, []interface{}) {
	finalSelectClause := qb.selectClause
	// If a text query argument was added, format the select clause to include its index for scoring.
	if qb.textQueryArgIndex > 0 {
		finalSelectClause = fmt.Sprintf(qb.selectClause, qb.textQueryArgIndex)
	}

	whereClause := strings.Join(qb.conditions, " AND ")

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		%s
		LIMIT %d`, finalSelectClause, qb.table, whereClause, qb.havingClause, qb.orderClause, limit)

	return sql, qb.args
}
