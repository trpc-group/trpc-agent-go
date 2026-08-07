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

func TestTestMissingRule_ExportedFunc(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `package handler

func HandleRequest() error {
	return nil
}

func processInternal() {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_TEST_MISSING_FUNC", findings[0].RuleID)
	assert.Contains(t, findings[0].Title, "HandleRequest")
	assert.Contains(t, findings[0].Title, "without corresponding test")
}

func TestTestMissingRule_WithTestContent(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `package handler

func HandleRequest() error {
	return nil
}

// TestHandleRequest is in the real test file.
func TestHandleRequest(t *testing.T) {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestTestMissingRule_NonGoFile(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "README.md"}
	content := `# Project`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestTestMissingRule_TestFile(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "handler_test.go", IsTestFile: true}
	content := `package handler

func TestHandle(t *testing.T) {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestTestMissingRule_UnexportedOnly(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "helper.go"}

	content := `package helper

func helperFunc() {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestTestMissingRule_ExportedType(t *testing.T) {
	rule := NewTestMissingRule()
	file := finding.ChangedFileInfo{File: "model.go"}

	content := `package model

type User struct {
	Name string
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings) // type without test file detection is separate
}

func TestTestFileMissingRule_WithExports(t *testing.T) {
	rule := NewTestFileMissingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `package handler

func HandleRequest() {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_TEST_FILE_MISSING", findings[0].RuleID)
}

func TestTestFileMissingRule_TestFound(t *testing.T) {
	rule := NewTestFileMissingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `package handler
func HandleRequest() {}

func TestHandleRequest(t *testing.T) {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}
