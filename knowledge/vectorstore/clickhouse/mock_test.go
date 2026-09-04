//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// mockClient is a mock implementation of storage.Client for testing.
type mockClient struct {
	execFunc        func(ctx context.Context, query string, args ...any) error
	queryFunc       func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	queryRowFunc    func(ctx context.Context, dest []any, query string, args ...any) error
	batchInsertFunc func(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error
	closeFunc       func() error

	// execCalls records every Exec invocation for assertions.
	execCalls []execCall
	// queryCalls records every Query invocation for assertions.
	queryCalls []queryCall
}

type execCall struct {
	query string
	args  []any
}

type queryCall struct {
	query string
	args  []any
}

func (m *mockClient) Exec(ctx context.Context, query string, args ...any) error {
	m.execCalls = append(m.execCalls, execCall{query: query, args: append([]any(nil), args...)})
	if m.execFunc != nil {
		return m.execFunc(ctx, query, args...)
	}
	return nil
}

func (m *mockClient) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	m.queryCalls = append(m.queryCalls, queryCall{query: query, args: append([]any(nil), args...)})
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, args...)
	}
	return &mockRows{}, nil
}

func (m *mockClient) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	if m.queryRowFunc != nil {
		return m.queryRowFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockClient) QueryToStruct(ctx context.Context, dest any, query string, args ...any) error {
	return nil
}

func (m *mockClient) QueryToStructs(ctx context.Context, dest any, query string, args ...any) error {
	return nil
}

func (m *mockClient) BatchInsert(ctx context.Context, query string, fn storage.BatchFn, opts ...driver.PrepareBatchOption) error {
	if m.batchInsertFunc != nil {
		return m.batchInsertFunc(ctx, query, fn, opts...)
	}
	return nil
}

func (m *mockClient) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	return nil
}

func (m *mockClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// mockRows is a mock implementation of driver.Rows.
type mockRows struct {
	driver.Rows
	data    [][]any
	current int
	err     error
}

func newMockRows(data [][]any) *mockRows {
	return &mockRows{data: data, current: -1}
}

func (m *mockRows) Next() bool {
	m.current++
	return m.current < len(m.data)
}

func (m *mockRows) Scan(dest ...any) error {
	if m.current < 0 || m.current >= len(m.data) {
		return nil
	}
	row := m.data[m.current]
	for i := 0; i < len(row) && i < len(dest); i++ {
		if err := setDest(dest[i], row[i]); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockRows) Close() error { return nil }

func (m *mockRows) Err() error { return m.err }

// setDest assigns a source value to a destination pointer via reflection. It
// supports *string, *int64, *uint64, *float64, *[]float64, *time.Time and *any.
//
// A non-convertible pair is reported as an error rather than silently skipped,
// so tests cannot pass with scan destinations the real driver would reject.
func setDest(dest, src any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr {
		return fmt.Errorf("mock: scan destination %T is not a pointer", dest)
	}
	elem := dv.Elem()
	if src == nil {
		// Leave zero value for nil sources.
		return nil
	}
	sv := reflect.ValueOf(src)

	// *any: set the interface value directly.
	if elem.Kind() == reflect.Interface {
		elem.Set(sv)
		return nil
	}
	if sv.Type().ConvertibleTo(elem.Type()) {
		elem.Set(sv.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("mock: converting %s to %s is unsupported", sv.Type(), elem.Type())
}
