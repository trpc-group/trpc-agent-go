//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultformat

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testResult struct {
	Output string
}

func TestFormatterFuncFormatsTypedResult(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")
	formatter := FormatterFunc[testResult](func(
		ctx context.Context,
		result testResult,
	) (string, error) {
		assert.Equal(t, "value", ctx.Value(contextKey{}))
		return "<output>" + result.Output + "</output>", nil
	})

	got, err := formatter.Format(ctx, testResult{Output: "done"})

	require.NoError(t, err)
	assert.Equal(t, "<output>done</output>", got)
}

func TestFormatterFuncRejectsUnexpectedType(t *testing.T) {
	formatter := FormatterFunc[testResult](func(
		context.Context,
		testResult,
	) (string, error) {
		return "unused", nil
	})

	_, err := formatter.Format(context.Background(), "wrong")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected resultformat.testResult")
	assert.Contains(t, err.Error(), "got string")
}

func TestFormatterFuncReturnsFormattingError(t *testing.T) {
	wantErr := errors.New("format failed")
	formatter := FormatterFunc[testResult](func(
		context.Context,
		testResult,
	) (string, error) {
		return "", wantErr
	})

	_, err := formatter.Format(context.Background(), testResult{})

	require.ErrorIs(t, err, wantErr)
}

func TestFormatterFuncRejectsNilFunction(t *testing.T) {
	var formatter FormatterFunc[testResult]

	_, err := formatter.Format(context.Background(), testResult{})

	require.EqualError(t, err, "resultformat: FormatterFunc is nil")
}

func TestFormatterFuncAcceptsNilForNilableType(t *testing.T) {
	formatter := FormatterFunc[*testResult](func(
		_ context.Context,
		result *testResult,
	) (string, error) {
		if result == nil {
			return "nil", nil
		}
		return result.Output, nil
	})

	got, err := formatter.Format(context.Background(), nil)

	require.NoError(t, err)
	assert.Equal(t, "nil", got)
}

func TestFormatterFuncRejectsNilForValueType(t *testing.T) {
	formatter := FormatterFunc[testResult](func(
		context.Context,
		testResult,
	) (string, error) {
		return "unused", nil
	})

	_, err := formatter.Format(context.Background(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected resultformat.testResult")
	assert.Contains(t, err.Error(), "got <nil>")
}
