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

func TestErrorHandlingRule_SilentIgnore(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func process() error {
	_ = doSomething()
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_ERROR_SILENT_IGNORE", findings[0].RuleID)
	assert.Equal(t, finding.CategoryErrorHandling, findings[0].Category)
	assert.Equal(t, 2, findings[0].Line)
	assert.Contains(t, findings[0].Evidence, "_ =")
}

func TestErrorHandlingRule_NoSilentIgnore(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func process() error {
	err := doSomething()
	if err != nil {
		return err
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestErrorHandlingRule_NonGoFile(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "script.py"}
	content := `_ = some_function()`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestErrorHandlingRule_RecoverWithoutDefer(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "panic.go"}

	content := `func handle() {
	recover()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_ERROR_SILENT_IGNORE", findings[0].RuleID)
	assert.Contains(t, findings[0].Title, "recover()")
}

func TestErrorHandlingRule_RecoverWithDefer(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "panic.go"}

	content := `func handle() {
	defer func() { recover() }()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestErrorNoReturnRule_Check(t *testing.T) {
	rule := NewErrorNoReturnRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func process() error {
	if err := validate(); err != nil {
		_ = err
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_ERROR_NO_RETURN", findings[0].RuleID)
}

func TestErrorNoReturnRule_WithReturn(t *testing.T) {
	rule := NewErrorNoReturnRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func process() error {
	if err := validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestErrorHandlingRule_IfErrWithLog(t *testing.T) {
	rule := NewErrorHandlingRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func process() {
	if err := do(); err != nil {
		log.Printf("failed: %v", err)
	}
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	// ErrorHandlingRule checks for `if err != nil` patterns, not `if err := ...`
	assert.Empty(t, findings)
}
