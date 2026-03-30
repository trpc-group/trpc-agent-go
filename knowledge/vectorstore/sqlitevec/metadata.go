//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlitevec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// metadataValueType enumerates the typed columns in the metadata index table.
const (
	metadataValueTypeText = "text"
	metadataValueTypeNum  = "num"
	metadataValueTypeBool = "bool"
	metadataValueTypeJSON = "json"
)

// metadataRow represents a single row in the metadata index table.
type metadataRow struct {
	docID     string
	key       string
	ordinal   int
	valueType string
	valueText sql.NullString
	valueNum  sql.NullFloat64
	valueBool sql.NullInt64
	valueJSON sql.NullString
}

// insertMetadataRows inserts expanded metadata rows in the given transaction.
func (s *Store) insertMetadataRows(ctx context.Context, tx *sql.Tx, docID string, metadata map[string]any) error {
	if len(metadata) == 0 {
		return nil
	}

	const insertSQL = `INSERT OR REPLACE INTO %s
	(doc_id, key, value_ordinal, value_type, value_text, value_num, value_bool, value_json)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	query := fmt.Sprintf(insertSQL, s.opts.metadataTableName)

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare metadata insert: %w", err)
	}
	defer stmt.Close()

	for key, value := range metadata {
		rows := classifyMetadataValues(docID, key, value)
		for _, row := range rows {
			if _, err := stmt.ExecContext(ctx,
				row.docID,
				row.key,
				row.ordinal,
				row.valueType,
				row.valueText,
				row.valueNum,
				row.valueBool,
				row.valueJSON,
			); err != nil {
				return fmt.Errorf("insert metadata key=%s ordinal=%d: %w", key, row.ordinal, err)
			}
		}
	}
	return nil
}

// deleteMetadataRows deletes all metadata rows for the given document id.
func (s *Store) deleteMetadataRows(ctx context.Context, tx *sql.Tx, docID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE doc_id = ?`, s.opts.metadataTableName)
	if _, err := tx.ExecContext(ctx, query, docID); err != nil {
		return fmt.Errorf("delete metadata for doc %s: %w", docID, err)
	}
	return nil
}

// loadStoredMetadata loads metadata for a document from the metadata index table.
// It reconstructs the original stored metadata shape using value_json and ordinal.
func (s *Store) loadStoredMetadata(ctx context.Context, docID string) (map[string]any, error) {
	query := fmt.Sprintf(`SELECT key, value_ordinal, value_json
FROM %s
WHERE doc_id = ?
ORDER BY key ASC, value_ordinal ASC`, s.opts.metadataTableName)

	rows, err := s.db.QueryContext(ctx, query, docID)
	if err != nil {
		return nil, fmt.Errorf("load metadata for doc %s: %w", docID, err)
	}
	defer rows.Close()

	type entry struct {
		ordinal int
		value   any
	}

	grouped := make(map[string][]entry)
	arrayKeys := make(map[string]bool)
	for rows.Next() {
		var (
			key     string
			ordinal int
			rawJSON sql.NullString
		)
		if err := rows.Scan(&key, &ordinal, &rawJSON); err != nil {
			return nil, fmt.Errorf("scan metadata for doc %s: %w", docID, err)
		}

		var value any
		if rawJSON.Valid && rawJSON.String != "" {
			if err := json.Unmarshal([]byte(rawJSON.String), &value); err != nil {
				return nil, fmt.Errorf("unmarshal metadata key %s for doc %s: %w", key, docID, err)
			}
		}
		grouped[key] = append(grouped[key], entry{ordinal: ordinal, value: value})
		if ordinal > 0 {
			arrayKeys[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metadata for doc %s: %w", docID, err)
	}
	if len(grouped) == 0 {
		return nil, nil
	}

	stored := make(map[string]any, len(grouped))
	for key, entries := range grouped {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].ordinal < entries[j].ordinal
		})

		if !arrayKeys[key] && len(entries) == 1 && entries[0].ordinal == 0 {
			stored[key] = entries[0].value
			continue
		}

		values := make([]any, 0, len(entries))
		for _, item := range entries {
			values = append(values, item.value)
		}
		stored[key] = values
	}

	return stored, nil
}

// classifyMetadataValues determines the typed representation of a metadata value.
// Scalar values produce a single row with ordinal 0. Arrays/slices produce one
// row per element.
func classifyMetadataValues(docID, key string, value any) []metadataRow {
	if value == nil {
		return []metadataRow{classifyMetadataScalar(docID, key, 0, nil)}
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		rows := make([]metadataRow, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			rows = append(rows, classifyMetadataScalar(docID, key, i, rv.Index(i).Interface()))
		}
		if len(rows) == 0 {
			return []metadataRow{classifyMetadataScalar(docID, key, 0, value)}
		}
		return rows
	default:
		return []metadataRow{classifyMetadataScalar(docID, key, 0, value)}
	}
}

func classifyMetadataScalar(docID, key string, ordinal int, value any) metadataRow {
	row := metadataRow{
		docID:   docID,
		key:     key,
		ordinal: ordinal,
	}

	// Serialize original JSON for round-trip.
	jsonBytes, _ := json.Marshal(value)
	row.valueJSON = sql.NullString{String: string(jsonBytes), Valid: true}

	switch v := value.(type) {
	case string:
		row.valueType = metadataValueTypeText
		row.valueText = sql.NullString{String: v, Valid: true}
	case bool:
		row.valueType = metadataValueTypeBool
		boolInt := int64(0)
		if v {
			boolInt = 1
		}
		row.valueBool = sql.NullInt64{Int64: boolInt, Valid: true}
	case int:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case int8:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case int16:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case int32:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case int64:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case uint:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case uint8:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case uint16:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case uint32:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case uint64:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case float32:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: float64(v), Valid: true}
	case float64:
		row.valueType = metadataValueTypeNum
		row.valueNum = sql.NullFloat64{Float64: v, Valid: true}
	case json.Number:
		row.valueType = metadataValueTypeNum
		if f, err := v.Float64(); err == nil {
			row.valueNum = sql.NullFloat64{Float64: f, Valid: true}
		}
	default:
		// Complex types (slices, maps, etc.) are stored as JSON only.
		row.valueType = metadataValueTypeJSON
	}

	return row
}
