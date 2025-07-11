package pgvector

import (
	"fmt"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
)

var publicFieldsStr = fmt.Sprintf("id, %s, %s, %s, %s, %s, %s", fieldName, fieldContent, fieldVector, fieldMetadata, fieldCreatedAt, fieldUpdatedAt)

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

func (ub *updateBuilder) addContentTSVector(content string) {
	ub.setParts = append(ub.setParts, fmt.Sprintf("content_tsvector = to_tsvector('english', $%d)", ub.argIndex))
	ub.args = append(ub.args, content)
	ub.argIndex++
}

func (ub *updateBuilder) build() (string, []interface{}) {
	sql := fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1`, ub.table, strings.Join(ub.setParts, ", "))
	return sql, ub.args
}

// queryBuilder builds SQL queries safely without string concatenation
type queryBuilder struct {
	table        string
	conditions   []string
	args         []interface{}
	argIndex     int
	havingClause string
	orderClause  string
	selectClause string
	tsConfig     string // Text search configuration (e.g., 'english', 'simple')
}

func newQueryBuilder(table string) *queryBuilder {
	return &queryBuilder{
		table:        table,
		conditions:   []string{"1=1"},
		args:         make([]interface{}, 0),
		argIndex:     1,
		selectClause: publicFieldsStr,
		tsConfig:     "english", // Default to English text search configuration
	}
}

// Vector search builder
func newVectorQueryBuilder(table string) *queryBuilder {
	qb := newQueryBuilder(table)
	qb.selectClause = fmt.Sprintf("%s, 1 - (embedding <=> $1) as score", publicFieldsStr)
	qb.orderClause = "ORDER BY embedding <=> $1"
	return qb
}

// Keyword search builder with full-text search scoring
func newKeywordQueryBuilder(table string) *queryBuilder {
	qb := newQueryBuilder(table)
	qb.selectClause = fmt.Sprintf("%s, ts_rank_cd(content_tsvector, plainto_tsquery('%s', $%%d)) as score", publicFieldsStr, qb.tsConfig)
	qb.orderClause = "ORDER BY score DESC, created_at DESC"
	return qb
}

// Hybrid search builder (vector + keyword)
func newHybridQueryBuilder(table string, vectorWeight, textWeight float64) *queryBuilder {
	qb := newQueryBuilder(table)
	qb.selectClause = fmt.Sprintf("%s, (1 - (embedding <=> $1)) * %.3f + ts_rank_cd(content_tsvector, plainto_tsquery('%s', $%%d)) * %.3f as score", publicFieldsStr, vectorWeight, qb.tsConfig, textWeight)
	qb.orderClause = "ORDER BY score DESC"
	return qb
}

// Filter-only search builder
func newFilterQueryBuilder(table string) *queryBuilder {
	qb := newQueryBuilder(table)
	qb.selectClause = fmt.Sprintf("%s, 1.0 as score", publicFieldsStr)
	qb.orderClause = "ORDER BY created_at DESC"
	return qb
}

func (qb *queryBuilder) addVectorArg(vector pgvector.Vector) {
	qb.args = append(qb.args, vector)
	qb.argIndex++
}

func (qb *queryBuilder) addFullTextSearch(query string) {
	// Use pre-computed tsvector for better performance
	condition := fmt.Sprintf("content_tsvector @@ plainto_tsquery('%s', $%d)", qb.tsConfig, qb.argIndex)
	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, query)
	qb.argIndex++
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
	condition := fmt.Sprintf("metadata @> $%d", qb.argIndex)
	qb.conditions = append(qb.conditions, condition)

	// Convert map to JSON string for @> operator
	metadataJSON := mapToJSON(metadata)
	qb.args = append(qb.args, metadataJSON)
	qb.argIndex++
}

// addMetadataPathFilter queries specific JSON paths with various operators
// Examples:
//
//	#> path extraction: metadata #> '{user,profile,name}'
//	#>> path as text: metadata #>> '{user,profile,name}'
//	? key exists: metadata ? 'user'
//	?& all keys exist: metadata ?& array['user','settings']
//	?| any key exists: metadata ?| array['user','admin']
func (qb *queryBuilder) addMetadataPathFilter(path []string, operator string, value interface{}) {
	var condition string
	var arg interface{}

	pathStr := `{` + fmt.Sprintf(`"%s"`, strings.Join(path, `","`)) + `}`

	switch operator {
	case "exists":
		// Check if path exists
		condition = fmt.Sprintf("metadata #> '%s' IS NOT NULL", pathStr)
		qb.conditions = append(qb.conditions, condition)
		return
	case "equals":
		// Path equals value (as JSON)
		condition = fmt.Sprintf("metadata #> '%s' = $%d", pathStr, qb.argIndex)
		arg = fmt.Sprintf(`"%v"`, value) // Wrap in quotes for JSON
	case "text_equals":
		// Path equals value (as text)
		condition = fmt.Sprintf("metadata #>> '%s' = $%d", pathStr, qb.argIndex)
		arg = fmt.Sprintf("%v", value)
	case "contains":
		// Path contains value (for arrays/objects)
		condition = fmt.Sprintf("metadata #> '%s' @> $%d", pathStr, qb.argIndex)
		arg = fmt.Sprintf(`"%v"`, value)
	default:
		return // Unsupported operator
	}

	qb.conditions = append(qb.conditions, condition)
	qb.args = append(qb.args, arg)
	qb.argIndex++
}

func (qb *queryBuilder) addScoreFilter(minScore float64) {
	qb.havingClause = fmt.Sprintf(" HAVING 1 - (embedding <=> $1) >= %f", minScore)
}

// Vector search SQL builder
func (qb *queryBuilder) buildVectorSearch(limit int) (string, []interface{}) {
	whereClause := strings.Join(qb.conditions, " AND ")

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		%s
		LIMIT %d`, qb.selectClause, qb.table, whereClause, qb.havingClause, qb.orderClause, limit)

	return sql, qb.args
}

// Keyword search SQL builder with full-text search
func (qb *queryBuilder) buildKeywordSearch(limit int) (string, []interface{}) {
	whereClause := strings.Join(qb.conditions, " AND ")

	// Build the select clause with proper text search scoring
	selectClause := fmt.Sprintf(qb.selectClause, qb.argIndex-1)

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		LIMIT %d`, selectClause, qb.table, whereClause, qb.orderClause, limit)

	return sql, qb.args
}

// Hybrid search SQL builder with combined scoring
func (qb *queryBuilder) buildHybridSearch(limit int) (string, []interface{}) {
	whereClause := strings.Join(qb.conditions, " AND ")

	// Build the select clause with combined vector and text search scoring
	selectClause := fmt.Sprintf(qb.selectClause, qb.argIndex-1)

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		%s
		LIMIT %d`, selectClause, qb.table, whereClause, qb.havingClause, qb.orderClause, limit)

	return sql, qb.args
}

// Filter search SQL builder
func (qb *queryBuilder) buildFilterSearch(limit int) (string, []interface{}) {
	whereClause := strings.Join(qb.conditions, " AND ")

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE %s
		%s
		LIMIT %d`, qb.selectClause, qb.table, whereClause, qb.orderClause, limit)

	return sql, qb.args
}
