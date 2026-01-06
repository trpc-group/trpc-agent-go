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
	"reflect"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// mockClient is a mock implementation of storage.Client for testing.
type mockClient struct {
	execFunc          func(ctx context.Context, query string, args ...any) error
	queryFunc         func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	queryRowFunc      func(ctx context.Context, dest []any, query string, args ...any) error
	queryToStructFunc func(ctx context.Context, dest any, query string, args ...any) error
	batchInsertFunc   func(ctx context.Context, query string, fn clickhouse.BatchFn, opts ...driver.PrepareBatchOption) error
	closeFunc         func() error
}

func (m *mockClient) Exec(ctx context.Context, query string, args ...any) error {
	if m.execFunc != nil {
		return m.execFunc(ctx, query, args...)
	}
	return nil
}

func (m *mockClient) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
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
	if m.queryToStructFunc != nil {
		return m.queryToStructFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockClient) QueryToStructs(ctx context.Context, dest any, query string, args ...any) error {
	// Not used in session service yet
	return nil
}

func (m *mockClient) BatchInsert(ctx context.Context, query string, fn clickhouse.BatchFn, opts ...driver.PrepareBatchOption) error {
	if m.batchInsertFunc != nil {
		return m.batchInsertFunc(ctx, query, fn, opts...)
	}
	// Simulate batch execution
	batch := &mockBatch{}
	return fn(batch)
}

func (m *mockClient) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	// Not used in session service yet
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
	data     [][]any
	current  int
	scanFunc func(dest ...any) error
}

func newMockRows(data [][]any) *mockRows {
	return &mockRows{
		data:    data,
		current: -1,
	}
}

func (m *mockRows) Next() bool {
	m.current++
	return m.current < len(m.data)
}

func (m *mockRows) Scan(dest ...any) error {
	if m.scanFunc != nil {
		return m.scanFunc(dest...)
	}
	if m.current < 0 || m.current >= len(m.data) {
		return nil
	}
	row := m.data[m.current]
	for i, val := range row {
		if i < len(dest) {
			destVal := reflect.ValueOf(dest[i])
			if destVal.Kind() == reflect.Ptr {
				valVal := reflect.ValueOf(val)
				if valVal.IsValid() {
					// Handle special types like time.Time
					if destVal.Elem().Type() == reflect.TypeOf(time.Time{}) && valVal.Type() == reflect.TypeOf(time.Time{}) {
						destVal.Elem().Set(valVal)
					} else if destVal.Elem().Type() == reflect.TypeOf(&time.Time{}) && valVal.Type() == reflect.TypeOf(&time.Time{}) {
						destVal.Elem().Set(valVal)
					} else {
						// Basic types
						if valVal.Type().ConvertibleTo(destVal.Elem().Type()) {
							destVal.Elem().Set(valVal.Convert(destVal.Elem().Type()))
						}
					}
				}
			}
		}
	}
	return nil
}

func (m *mockRows) Close() error {
	return nil
}

func (m *mockRows) Err() error {
	return nil
}

// mockBatch is a mock implementation of driver.Batch.
type mockBatch struct {
	driver.Batch
	appendFunc func(v ...any) error
	rows       [][]any
}

func (m *mockBatch) Append(v ...any) error {
	if m.appendFunc != nil {
		return m.appendFunc(v...)
	}
	m.rows = append(m.rows, v)
	return nil
}

func (m *mockBatch) Send() error {
	return nil
}
