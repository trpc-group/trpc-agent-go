//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestDBLifecycleRule_BeginNoRollback(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func createUser(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// no defer tx.Rollback()
	tx.Exec("INSERT ...")
	return tx.Commit()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_DB_TX_NO_ROLLBACK", findings[0].RuleID)
	assert.Equal(t, finding.CategoryDBLifecycle, findings[0].Category)
	assert.Equal(t, 2, findings[0].Line)
}

func TestDBLifecycleRule_BeginWithRollback(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func createUser(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec("INSERT ...")
	return tx.Commit()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestDBLifecycleRule_BeginTxNoRollback(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func createUser(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_ = tx
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
}

func TestDBLifecycleRule_DeferRowsClose(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func query(db *sql.DB) {
	rows, _ := db.Query("SELECT 1")
	defer rows.Close()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_DB_TX_NO_ROLLBACK", findings[0].RuleID)
	assert.Contains(t, findings[0].Title, "rows deferred Close without Err() check")
}

func TestDBLifecycleRule_HTTPInTransaction(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func handle(db *sql.DB) {
	tx, _ := db.Begin()
	// ... some work ...
	resp, _ := http.Get("https://api.example.com")
	_ = resp
	tx.Commit()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 2) // no rollback + HTTP in tx
}

func TestDBLifecycleRule_NonGoFile(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.py"}
	content := `db.begin()`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestDBRowsErrCheckRule_MissingErrCheck(t *testing.T) {
	rule := NewDBRowsErrCheckRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func query(db *sql.DB) {
	rows, _ := db.Query("SELECT id FROM users")
	defer rows.Close()
	for rows.Next() {
		var id int
		rows.Scan(&id)
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_DB_ROWS_NO_ERRCHECK", findings[0].RuleID)
}

func TestDBRowsErrCheckRule_WithErrCheck(t *testing.T) {
	rule := NewDBRowsErrCheckRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func query(db *sql.DB) {
	rows, _ := db.Query("SELECT id FROM users")
	defer rows.Close()
	for rows.Next() {
		var id int
		rows.Scan(&id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestDBLifecycleRule_ConnPoolConfig(t *testing.T) {
	rule := NewDBLifecycleRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func initDB() *sql.DB {
	db, _ := sql.Open("sqlite3", "test.db")
	// no pool config
	return db
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_DB_TX_NO_ROLLBACK", findings[0].RuleID)
	assert.Contains(t, findings[0].Title, "pool configuration")
}
