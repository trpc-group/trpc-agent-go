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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestOptionsHelpers(t *testing.T) {
	o := defaultOptions

	WithDSN("file:/tmp/test.db")(&o)
	WithDriverName("custom-driver")(&o)
	WithTableName("custom_docs")(&o)
	WithMetadataTableName("custom_meta")(&o)
	WithIndexDimension(256)(&o)
	WithMaxResults(20)(&o)
	WithSkipDBInit(true)(&o)

	assert.Equal(t, "file:/tmp/test.db", o.dsn)
	assert.Equal(t, "custom-driver", o.driverName)
	assert.Equal(t, "custom_docs", o.tableName)
	assert.Equal(t, "custom_meta", o.metadataTableName)
	assert.Equal(t, 256, o.indexDimension)
	assert.Equal(t, 20, o.maxResults)
	assert.True(t, o.skipDBInit)

	WithDSN("")(&o)
	WithDriverName("")(&o)
	WithIndexDimension(0)(&o)
	WithMaxResults(0)(&o)
	assert.Equal(t, "file:/tmp/test.db", o.dsn)
	assert.Equal(t, "custom-driver", o.driverName)
	assert.Equal(t, 256, o.indexDimension)
	assert.Equal(t, 20, o.maxResults)

	assert.Panics(t, func() { WithTableName("bad-name")(&o) })
	assert.Panics(t, func() { WithMetadataTableName("bad-name")(&o) })
}

func TestNew_InvalidDriverAndSkipInit(t *testing.T) {
	store, err := New(WithSkipDBInit(true), WithMaxResults(7))
	require.NoError(t, err)
	assert.Equal(t, 7, store.opts.maxResults)
	require.NoError(t, store.Close())

	store, err = New(WithDriverName("missing-driver"), WithSkipDBInit(true))
	assert.Nil(t, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown driver")
}

func TestInitDB_ErrorOnClosedDB(t *testing.T) {
	store, err := New(WithSkipDBInit(true), WithIndexDimension(testDimension))
	require.NoError(t, err)
	require.NoError(t, store.Close())

	err = store.initDB(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite-vec is not available")
}

func TestFilterBuilderOperatorsAndHelpers(t *testing.T) {
	fb := newFilterBuilder("docs", "meta")

	frag, err := fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "name",
		Operator: searchfilter.OperatorIn,
		Value:    []string{"a", "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, "v.name IN (?,?)", frag.sql)
	assert.Equal(t, []any{"a", "b"}, frag.params)

	frag, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "metadata.lang",
		Operator: searchfilter.OperatorNotIn,
		Value:    []string{"go", "py"},
	})
	require.NoError(t, err)
	assert.Contains(t, frag.sql, "NOT EXISTS")
	assert.Equal(t, []any{"lang", "go", "py"}, frag.params)

	frag, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "content",
		Operator: searchfilter.OperatorLike,
		Value:    "sqlite",
	})
	require.NoError(t, err)
	assert.Equal(t, "v.content LIKE ?", frag.sql)
	assert.Equal(t, []any{"%sqlite%"}, frag.params)

	frag, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "metadata.lang",
		Operator: searchfilter.OperatorNotLike,
		Value:    "vec",
	})
	require.NoError(t, err)
	assert.Contains(t, frag.sql, "NOT EXISTS")
	assert.Equal(t, []any{"lang", "%vec%"}, frag.params)

	frag, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "created_at",
		Operator: searchfilter.OperatorBetween,
		Value:    []int64{1, 2},
	})
	require.NoError(t, err)
	assert.Equal(t, "v.created_at BETWEEN ? AND ?", frag.sql)
	assert.Equal(t, []any{int64(1), int64(2)}, frag.params)

	frag, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{
		Field:    "metadata.score",
		Operator: searchfilter.OperatorBetween,
		Value:    []float64{0.1, 0.9},
	})
	require.NoError(t, err)
	assert.Contains(t, frag.sql, "m.value_num BETWEEN ? AND ?")
	assert.Equal(t, []any{"score", 0.1, 0.9}, frag.params)

	_, err = fb.convertCondition(&searchfilter.UniversalFilterCondition{Operator: "??"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operator")

	_, err = fb.convertLogical(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    "bad",
	})
	require.Error(t, err)

	_, err = fb.convertIn(&searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorIn})
	require.Error(t, err)
	_, err = fb.convertIn(&searchfilter.UniversalFilterCondition{
		Field:    "name",
		Operator: searchfilter.OperatorIn,
		Value:    "bad",
	})
	require.Error(t, err)

	_, err = fb.convertLike(&searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorLike})
	require.Error(t, err)

	_, err = fb.convertBetween(&searchfilter.UniversalFilterCondition{Operator: searchfilter.OperatorBetween})
	require.Error(t, err)
	_, err = fb.convertBetween(&searchfilter.UniversalFilterCondition{
		Field:    "metadata.score",
		Operator: searchfilter.OperatorBetween,
		Value:    []int{1},
	})
	require.Error(t, err)

	assert.Equal(t, "", fb.resolveColumn("metadata.id"))
	assert.Equal(t, "id", fb.resolveColumn("id"))
	assert.Equal(t, "", fb.resolveColumn("category"))
	assert.Equal(t, "id", stripMetadataPrefix("metadata.id"))

	col, param := typedMetadataColumn(true)
	assert.Equal(t, "value_bool", col)
	assert.Equal(t, int64(1), param)

	col, param = typedMetadataColumn(12)
	assert.Equal(t, "value_num", col)
	assert.Equal(t, 12, param)

	col, param = typedMetadataColumn("go")
	assert.Equal(t, "value_text", col)
	assert.Equal(t, "go", param)

	col, param = typedMetadataColumn(struct{ X int }{X: 1})
	assert.Equal(t, "value_text", col)
	assert.Equal(t, "{1}", param)

	assert.Equal(t, "=", comparisonSQLOp(searchfilter.OperatorEqual))
	assert.Equal(t, "!=", comparisonSQLOp(searchfilter.OperatorNotEqual))
	assert.Equal(t, ">", comparisonSQLOp(searchfilter.OperatorGreaterThan))
	assert.Equal(t, ">=", comparisonSQLOp(searchfilter.OperatorGreaterThanOrEqual))
	assert.Equal(t, "<", comparisonSQLOp(searchfilter.OperatorLessThan))
	assert.Equal(t, "<=", comparisonSQLOp(searchfilter.OperatorLessThanOrEqual))
	assert.Equal(t, "=", comparisonSQLOp("unknown"))
}

func TestMetadataHelpersAndDBErrorPaths(t *testing.T) {
	row := classifyMetadataScalar("doc-1", "name", 0, "sqlite")
	assert.Equal(t, metadataValueTypeText, row.valueType)
	assert.Equal(t, "sqlite", row.valueText.String)

	row = classifyMetadataScalar("doc-1", "enabled", 0, true)
	assert.Equal(t, metadataValueTypeBool, row.valueType)
	assert.Equal(t, int64(1), row.valueBool.Int64)

	row = classifyMetadataScalar("doc-1", "score", 0, json.Number("12.5"))
	assert.Equal(t, metadataValueTypeNum, row.valueType)
	assert.InDelta(t, 12.5, row.valueNum.Float64, 0.001)

	row = classifyMetadataScalar("doc-1", "meta", 0, map[string]any{"k": "v"})
	assert.Equal(t, metadataValueTypeJSON, row.valueType)
	assert.Equal(t, `{"k":"v"}`, row.valueJSON.String)

	rows := classifyMetadataValues("doc-1", "tags", []string{"a", "b"})
	require.Len(t, rows, 2)
	assert.Equal(t, 0, rows[0].ordinal)
	assert.Equal(t, 1, rows[1].ordinal)

	rows = classifyMetadataValues("doc-1", "empty", []string{})
	require.Len(t, rows, 1)
	assert.Equal(t, metadataValueTypeJSON, rows[0].valueType)

	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(doc_id, key, value_ordinal, value_type, value_text, value_num, value_bool, value_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.opts.metadataTableName),
		"doc-bad", "broken", 0, metadataValueTypeJSON, nil, nil, nil, "{bad json}",
	)
	require.NoError(t, err)

	_, err = store.loadStoredMetadata(ctx, "doc-bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal metadata key broken")

	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	store.opts.metadataTableName = "bad-table"
	err = store.deleteMetadataRows(ctx, tx, "doc-1")
	require.Error(t, err)
	err = store.insertMetadataRows(ctx, tx, "doc-1", map[string]any{"k": "v"})
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
}

func TestSearchDeleteMetadataAndHelperBranches(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "a",
		Name:    "alpha",
		Content: "hello sqlite",
		Metadata: map[string]any{
			"lang": "go",
		},
	}, testEmbedding(1, 0, 0, 0)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "b",
		Name:    "beta",
		Content: "hello vectors",
		Metadata: map[string]any{
			"lang": "py",
		},
	}, testEmbedding(0, 1, 0, 0)))

	_, err := store.Search(ctx, nil)
	require.Error(t, err)

	result, err := store.Search(ctx, &vectorstore.SearchQuery{Vector: testEmbedding(1, 0, 0, 0)})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results)
	assert.Equal(t, "a", result.Results[0].Document.ID)

	result, err = store.Search(ctx, &vectorstore.SearchQuery{
		Vector:   testEmbedding(1, 0, 0, 0),
		MinScore: 1.1,
	})
	require.NoError(t, err)
	assert.Len(t, result.Results, 0)

	result, err = store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: 99,
		Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"lang": "py"}},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "b", result.Results[0].Document.ID)

	_, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(0))
	require.Error(t, err)

	err = store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true), vectorstore.WithDeleteDocumentIDs([]string{"a"}))
	require.Error(t, err)
	err = store.DeleteByFilter(ctx)
	require.Error(t, err)

	ids, err := store.collectFilteredIDs(ctx, []string{"x", "y"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, ids)

	ids, err = store.collectFilteredIDs(ctx, nil, map[string]any{"lang": "go"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ids)
	ids, err = store.collectFilteredIDsFromCondition(ctx, nil, &searchfilter.UniversalFilterCondition{
		Field:    "metadata.lang",
		Operator: searchfilter.OperatorEqual,
		Value:    "py",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, ids)
}

func TestUpdateByFilterMetadataAndUtilityHelpers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:            "doc-1",
		Content:       "v1",
		EmbeddingText: "old embedding text",
		Metadata: map[string]any{
			"lang": "go",
		},
	}, testEmbedding(1, 0, 0, 0)))

	updated, err := store.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc-1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"embedding_text":   "new embedding text",
			"metadata.version": 2,
			"embedding":        testEmbedding(0, 1, 0, 0),
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)

	doc, emb, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "new embedding text", doc.EmbeddingText)
	assert.Equal(t, float64(2), doc.Metadata["version"])
	assert.Equal(t, testDimension, len(emb))

	updated, err = store.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
			Field:    "metadata.lang",
			Operator: searchfilter.OperatorEqual,
			Value:    "missing",
		}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"name": "never",
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(0), updated)

	assert.NoError(t, (&Store{}).Close())
	assert.Nil(t, deserializeEmbedding(nil))

	meta, text := splitInternalMetadata(withInternalMetadata(map[string]any{"lang": "go"}, "embed me"))
	assert.Equal(t, "embed me", text)
	assert.Equal(t, "go", meta["lang"])

	meta, text = splitInternalMetadata(map[string]any{internalEmbeddingTextMetadataKey: "only text"})
	assert.Nil(t, meta)
	assert.Equal(t, "only text", text)

	assert.Nil(t, withInternalMetadata(nil, ""))

	jsonText, err := marshalMetadata(nil)
	require.NoError(t, err)
	assert.Equal(t, "{}", jsonText)

	_, err = marshalMetadata(map[string]any{"bad": func() {}})
	require.Error(t, err)

	meta, err = unmarshalMetadata("")
	require.NoError(t, err)
	assert.Nil(t, meta)

	_, err = unmarshalMetadata("{bad json}")
	require.Error(t, err)

	assert.Equal(t, map[string]any{"a": 1}, reconcileStoredMetadata(map[string]any{"a": 1}, nil))
	assert.Equal(t, map[string]any{"b": 2}, reconcileStoredMetadata(nil, map[string]any{"b": 2}))
	assert.Equal(t, []any{"x"}, reconcileStoredMetadata(
		map[string]any{"tags": []any{"x"}},
		map[string]any{"tags": "x"},
	)["tags"])
}

func TestUpdateByFilter_RejectsUnsupportedFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "doc-1",
		Content: "v1",
	}, testEmbedding(1, 0, 0, 0)))

	for _, updates := range []map[string]any{
		{"id": "new-id"},
		{"updated_at": int64(1)},
		{"unknown": "value"},
		{"metadata.": "bad"},
	} {
		updated, err := store.UpdateByFilter(ctx,
			vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc-1"}),
			vectorstore.WithUpdateByFilterUpdates(updates),
		)
		require.Error(t, err)
		assert.Equal(t, int64(0), updated)
	}

	doc, _, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "doc-1", doc.ID)
	assert.Equal(t, "v1", doc.Content)
}

func TestGetUpdateDeleteValidationBranches(t *testing.T) {
	store := newTestStore(t)

	_, _, err := store.Get(context.Background(), "")
	assert.ErrorIs(t, err, errDocIDEmpty)

	err = store.Update(context.Background(), nil, testEmbedding(1))
	assert.ErrorIs(t, err, errDocNil)
	err = store.Update(context.Background(), &document.Document{}, testEmbedding(1))
	assert.ErrorIs(t, err, errDocIDEmpty)
	err = store.Update(context.Background(), &document.Document{ID: "x"}, nil)
	assert.ErrorIs(t, err, errEmbeddingEmpty)

	err = store.Delete(context.Background(), "")
	assert.ErrorIs(t, err, errDocIDEmpty)
}

func TestBuildScoredDocumentAndLoadStoredMetadataEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	meta, err := store.loadStoredMetadata(ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, meta)

	scored, err := store.buildScoredDocument(ctx, "doc-1", sql.NullString{}, sql.NullString{}, sql.NullString{}, 1, 2, 0.5)
	require.NoError(t, err)
	assert.Equal(t, "doc-1", scored.Document.ID)
	assert.Equal(t, 0.5, scored.Score)
	assert.Nil(t, scored.Document.Metadata)
}

func TestClassifyMetadataScalar_CoversScalarTypes(t *testing.T) {
	int64One := int64(1)

	testCases := []struct {
		name       string
		value      any
		wantType   string
		wantText   string
		wantNum    float64
		wantNumSet bool
		wantBool   int64
		wantBoolOk bool
		wantJSON   string
	}{
		{name: "nil", value: nil, wantType: metadataValueTypeJSON, wantJSON: "null"},
		{name: "string", value: "sqlite", wantType: metadataValueTypeText, wantText: "sqlite", wantJSON: `"sqlite"`},
		{name: "bool false", value: false, wantType: metadataValueTypeBool, wantBool: 0, wantBoolOk: true, wantJSON: "false"},
		{name: "int8", value: int8(8), wantType: metadataValueTypeNum, wantNum: 8, wantNumSet: true, wantJSON: "8"},
		{name: "int16", value: int16(16), wantType: metadataValueTypeNum, wantNum: 16, wantNumSet: true, wantJSON: "16"},
		{name: "int32", value: int32(32), wantType: metadataValueTypeNum, wantNum: 32, wantNumSet: true, wantJSON: "32"},
		{name: "int64", value: int64(64), wantType: metadataValueTypeNum, wantNum: 64, wantNumSet: true, wantJSON: "64"},
		{name: "uint", value: uint(7), wantType: metadataValueTypeNum, wantNum: 7, wantNumSet: true, wantJSON: "7"},
		{name: "uint8", value: uint8(8), wantType: metadataValueTypeNum, wantNum: 8, wantNumSet: true, wantJSON: "8"},
		{name: "uint16", value: uint16(16), wantType: metadataValueTypeNum, wantNum: 16, wantNumSet: true, wantJSON: "16"},
		{name: "uint32", value: uint32(32), wantType: metadataValueTypeNum, wantNum: 32, wantNumSet: true, wantJSON: "32"},
		{name: "uint64", value: uint64(64), wantType: metadataValueTypeNum, wantNum: 64, wantNumSet: true, wantJSON: "64"},
		{name: "float32", value: float32(3.5), wantType: metadataValueTypeNum, wantNum: 3.5, wantNumSet: true, wantJSON: "3.5"},
		{name: "float64", value: 7.25, wantType: metadataValueTypeNum, wantNum: 7.25, wantNumSet: true, wantJSON: "7.25"},
		{name: "bad json number", value: json.Number("NaN"), wantType: metadataValueTypeNum, wantJSON: ""},
		{name: "complex", value: map[string]any{"x": int64One}, wantType: metadataValueTypeJSON, wantJSON: `{"x":1}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			row := classifyMetadataScalar("doc-1", "key", 0, tc.value)
			assert.Equal(t, tc.wantType, row.valueType)
			assert.Equal(t, tc.wantJSON, row.valueJSON.String)
			assert.True(t, row.valueJSON.Valid)

			if tc.wantText != "" {
				assert.Equal(t, tc.wantText, row.valueText.String)
				assert.True(t, row.valueText.Valid)
			}
			if tc.wantNumSet {
				assert.InDelta(t, tc.wantNum, row.valueNum.Float64, 0.0001)
				assert.True(t, row.valueNum.Valid)
			}
			if tc.wantBoolOk {
				assert.Equal(t, tc.wantBool, row.valueBool.Int64)
				assert.True(t, row.valueBool.Valid)
			}
		})
	}

	rows := classifyMetadataValues("doc-1", "arr", [2]int{1, 2})
	require.Len(t, rows, 2)
	assert.Equal(t, 0, rows[0].ordinal)
	assert.Equal(t, 1, rows[1].ordinal)
}

func TestInitDB_ErrorPathsForInvalidTableNames(t *testing.T) {
	ctx := context.Background()

	store := newTestStore(t)
	store.opts.tableName = "bad-table"
	err := store.initDB(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create vec0 table")

	store2 := newTestStore(t)
	store2.opts.metadataTableName = "bad-meta"
	err = store2.initDB(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create metadata table")
}
