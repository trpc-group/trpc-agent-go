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

func TestResourceLeakRule_OsOpenNoClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "read.go"}

	content := `func readFile() {
	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		return err
	}
	// use f, no defer close
	_ = f
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_RESOURCE_NO_CLOSE", findings[0].RuleID)
	assert.Equal(t, finding.CategoryResourceLeak, findings[0].Category)
	assert.Equal(t, 2, findings[0].Line)
}

func TestResourceLeakRule_OsOpenWithClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "read.go"}

	content := `func readFile() {
	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		return err
	}
	defer f.Close()
	// use f
	_ = f
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestResourceLeakRule_HTTPGetNoClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "http.go"}

	content := `func fetch() {
	resp, err := http.Get("https://example.com")
	if err != nil {
		return err
	}
	// resp.Body not closed
	_ = resp
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
}

func TestResourceLeakRule_HTTPGetWithClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "http.go"}

	content := `func fetch() {
	resp, err := http.Get("https://example.com")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_ = resp
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestResourceLeakRule_DBQueryNoClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func query(db *sql.DB) {
	rows, err := db.Query("SELECT * FROM users")
	if err != nil {
		return err
	}
	// rows not closed
	for rows.Next() {
		// scan
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, 2, findings[0].Line)
}

func TestResourceLeakRule_DBQueryWithClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "db.go"}

	content := `func query(db *sql.DB) {
	rows, err := db.Query("SELECT * FROM users")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		// scan
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestResourceLeakRule_NonGoFile(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "read.py"}
	content := `f = open("/tmp/file.txt")`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestResourceLeakRule_CleanCode(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "safe.go"}

	content := `func readFile() string {
	data, _ := os.ReadFile("/tmp/file.txt")
	return string(data)
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestResourceLeakRule_OsCreateNoClose(t *testing.T) {
	rule := NewResourceLeakRule()
	file := finding.ChangedFileInfo{File: "write.go"}

	content := `func writeFile() {
	f, _ := os.Create("/tmp/out.txt")
	_ = f
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
}
