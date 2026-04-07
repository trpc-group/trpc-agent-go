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
	"context"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Tests for enablePgvectorExtension ---

func TestEnablePgvectorExtension_Success(
	t *testing.T,
) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))

	err = enablePgvectorExtension(
		context.Background(), client,
	)
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnablePgvectorExtension_Error(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnError(fmt.Errorf("extension not available"))

	err = enablePgvectorExtension(
		context.Background(), client,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"enable pgvector extension")
}

// --- Tests for createTables ---

func TestCreateTables_Success(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	// Expect one exec per table definition.
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}

	err = createTables(
		context.Background(), client, "", "",
	)
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTables_Error(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnError(fmt.Errorf("table error"))

	err = createTables(
		context.Background(), client, "", "",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create table")
}

func TestCreateTables_WithSchemaAndPrefix(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}

	err = createTables(
		context.Background(), client,
		"myschema", "prefix_",
	)
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for createIndexes ---

func TestCreateIndexes_Success(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	for range indexDefs {
		mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}

	err = createIndexes(
		context.Background(), client, "", "",
	)
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateIndexes_Error(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(
			sqlmock.QueryMatcherRegexp,
		),
	)
	require.NoError(t, err)
	defer db.Close()

	client := &mockPostgresClient{db: db}
	mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
		WillReturnError(fmt.Errorf("index error"))

	err = createIndexes(
		context.Background(), client, "", "",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create index")
}

// --- Tests for addVectorColumns ---

func expectEmbeddingColumnDimensionQuery(
	mock sqlmock.Sqlmock,
	dim int,
) {
	mock.ExpectQuery(
		"SELECT format_type\\(a\\.atttypid, a\\.atttypmod\\)",
	).WithArgs(
		"session_events",
		"",
	).WillReturnRows(
		sqlmock.NewRows([]string{"format_type"}).AddRow(
			fmt.Sprintf("vector(%d)", dim),
		),
	)
}

func TestAddVectorColumns_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.indexDimension = defaultIndexDimension
	// Expect 4 ALTER TABLE statements.
	for i := 0; i < 4; i++ {
		mock.ExpectExec("ALTER TABLE").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	expectEmbeddingColumnDimensionQuery(
		mock,
		defaultIndexDimension,
	)

	err := s.addVectorColumns(context.Background())
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddVectorColumns_Error(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE").
		WillReturnError(fmt.Errorf("alter error"))

	err := s.addVectorColumns(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "alter table failed")
}

func TestAddVectorColumns_DimensionMismatch(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.indexDimension = defaultIndexDimension
	for i := 0; i < 4; i++ {
		mock.ExpectExec("ALTER TABLE").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	expectEmbeddingColumnDimensionQuery(mock, 768)

	err := s.addVectorColumns(context.Background())
	require.Error(t, err)
	require.Contains(
		t,
		err.Error(),
		"embedding column dimension mismatch",
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for createTextSearchIndex ---

func TestCreateTextSearchIndex_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("CREATE INDEX IF NOT EXISTS.*USING gin").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.createTextSearchIndex(context.Background())
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTextSearchIndex_Error(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec("CREATE INDEX IF NOT EXISTS.*USING gin").
		WillReturnError(fmt.Errorf("gin index error"))

	err := s.createTextSearchIndex(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create GIN index failed")
}

// --- Tests for createHNSWIndex ---

func TestCreateHNSWIndex_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.hnswM = defaultHNSWM
	s.opts.hnswEf = defaultHNSWEf
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS.*USING hnsw").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := s.createHNSWIndex(context.Background())
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndex_Error(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.hnswM = defaultHNSWM
	s.opts.hnswEf = defaultHNSWEf
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnError(
			fmt.Errorf("hnsw index error"),
		)

	err := s.createHNSWIndex(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"create HNSW index failed")
}

// --- Tests for initDB ---

func TestInitDB_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.indexDimension = defaultIndexDimension
	s.opts.hnswM = defaultHNSWM
	s.opts.hnswEf = defaultHNSWEf

	// enablePgvectorExtension.
	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	// createTables: one per table def.
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// createIndexes: one per index def.
	for range indexDefs {
		mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// addVectorColumns: 4 ALTER TABLE.
	for i := 0; i < 4; i++ {
		mock.ExpectExec("ALTER TABLE").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	expectEmbeddingColumnDimensionQuery(
		mock,
		defaultIndexDimension,
	)
	// createTextSearchIndex.
	mock.ExpectExec(
		"CREATE INDEX IF NOT EXISTS.*USING gin",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	// createHNSWIndex.
	mock.ExpectExec(
		"CREATE INDEX IF NOT EXISTS.*USING hnsw",
	).WillReturnResult(sqlmock.NewResult(0, 0))

	// Should not return an error.
	require.NoError(t, s.initDB(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_HNSWIndexFailureIsNonFatal(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.indexDimension = defaultIndexDimension
	s.opts.hnswM = defaultHNSWM
	s.opts.hnswEf = defaultHNSWEf

	// enablePgvectorExtension.
	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	// createTables.
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// createIndexes.
	for range indexDefs {
		mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// addVectorColumns.
	for i := 0; i < 4; i++ {
		mock.ExpectExec("ALTER TABLE").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	expectEmbeddingColumnDimensionQuery(
		mock,
		defaultIndexDimension,
	)
	// createTextSearchIndex.
	mock.ExpectExec(
		"CREATE INDEX IF NOT EXISTS.*USING gin",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	// createHNSWIndex fails — non-fatal.
	mock.ExpectExec(
		"CREATE INDEX IF NOT EXISTS",
	).WillReturnError(
		fmt.Errorf("pgvector not installed"),
	)

	// Should not return an error even if HNSW fails.
	require.NoError(t, s.initDB(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for initDB error paths ---

func TestInitDB_ErrorsOnExtensionError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnError(
		fmt.Errorf("extension error"),
	)

	err := s.initDB(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"enable pgvector extension failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_ErrorsOnCreateTablesError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	// First table creation fails.
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnError(fmt.Errorf("table error"))

	err := s.initDB(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tables failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_ErrorsOnCreateIndexesError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// First index creation fails.
	mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
		WillReturnError(fmt.Errorf("index error"))

	err := s.initDB(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "create indexes failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInitDB_ErrorsOnAddVectorColumnsError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	s.opts.indexDimension = defaultIndexDimension
	mock.ExpectExec(
		"CREATE EXTENSION IF NOT EXISTS vector",
	).WillReturnResult(sqlmock.NewResult(0, 0))
	for range tableDefs {
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	for range indexDefs {
		mock.ExpectExec("CREATE.*INDEX IF NOT EXISTS").
			WillReturnResult(
				sqlmock.NewResult(0, 0),
			)
	}
	// First ALTER TABLE fails.
	mock.ExpectExec("ALTER TABLE").
		WillReturnError(
			fmt.Errorf("alter column error"),
		)

	err := s.initDB(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "add vector columns failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
