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
	"errors"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type mockConn struct {
	driver.Conn
	pingErr         error
	prepareBatchErr error
	execErr         error
	queryErr        error
	asyncInsertErr  error
	closeErr        error
	lastBatch       *mockBatch
}

func (m *mockConn) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockConn) Close() error {
	return m.closeErr
}

func (m *mockConn) Exec(ctx context.Context, query string, args ...any) error {
	return m.execErr
}

func (m *mockConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return &mockRows{}, nil
}

func (m *mockConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return &mockRow{err: m.queryErr}
}

func (m *mockConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	if m.prepareBatchErr != nil {
		return nil, m.prepareBatchErr
	}
	m.lastBatch = &mockBatch{}
	return m.lastBatch, nil
}

func (m *mockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	return m.asyncInsertErr
}

func (m *mockConn) Select(ctx context.Context, dest any, query string, args ...any) error {
	return m.queryErr
}

type mockRows struct {
	driver.Rows
}

func (m *mockRows) Close() error {
	return nil
}

func (m *mockRows) Next() bool {
	return false
}

func (m *mockRows) Scan(dest ...any) error {
	return nil
}

func (m *mockRows) Columns() []string {
	return []string{}
}

func (m *mockRows) Err() error {
	return nil
}

type mockRow struct {
	driver.Row
	err error
}

func (m *mockRow) Scan(dest ...any) error {
	return m.err
}

func (m *mockRow) ScanStruct(dest any) error {
	return m.err
}

type mockBatch struct {
	driver.Batch
	isAborted bool
}

func (m *mockBatch) Append(v ...any) error {
	return nil
}

func (m *mockBatch) Send() error {
	return nil
}

func (m *mockBatch) Abort() error {
	m.isAborted = true
	return nil
}

func TestDefaultClientBuilder(t *testing.T) {
	tests := []struct {
		name        string
		opts        []ClientBuilderOpt
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty DSN",
			opts:        []ClientBuilderOpt{},
			wantErr:     true,
			errContains: "DSN is empty",
		},
		{
			name: "invalid DSN format",
			opts: []ClientBuilderOpt{
				WithClientBuilderDSN("invalid-dsn"),
			},
			wantErr:     true,
			errContains: "parse DSN failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := defaultClientBuilder(tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("defaultClientBuilder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error = %v, should contain %v", err, tt.errContains)
				}
			}

			if !tt.wantErr && client == nil {
				t.Error("expected non-nil client")
			}
		})
	}
}

func TestRegisterAndGetClickHouseInstance(t *testing.T) {
	clickhouseRegistry = make(map[string][]ClientBuilderOpt)

	dsn := "clickhouse://test:9000"
	RegisterClickHouseInstance("test-instance",
		WithClientBuilderDSN(dsn),
		WithExtraOptions("option1", "option2"),
	)

	opts, ok := GetClickHouseInstance("test-instance")
	if !ok {
		t.Fatal("expected instance to be registered")
	}

	if len(opts) != 2 {
		t.Errorf("expected 2 options, got %d", len(opts))
	}

	_, ok = GetClickHouseInstance("non-existent")
	if ok {
		t.Error("expected non-existent instance to not be found")
	}
}

func TestClientBuilderOpts(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithClientBuilderDSN("clickhouse://localhost:9000")(opts)
	if opts.DSN != "clickhouse://localhost:9000" {
		t.Errorf("DSN = %s, want clickhouse://localhost:9000", opts.DSN)
	}

	WithExtraOptions("opt1", "opt2")(opts)
	if len(opts.ExtraOptions) != 2 {
		t.Errorf("ExtraOptions length = %d, want 2", len(opts.ExtraOptions))
	}
}

func TestClientOperations(t *testing.T) {
	conn := &mockConn{}
	client := newDefaultClient(conn)
	ctx := context.Background()

	t.Run("Exec", func(t *testing.T) {
		err := client.Exec(ctx, "INSERT INTO table VALUES (?)", 123)
		if err != nil {
			t.Errorf("Exec failed: %v", err)
		}
	})

	t.Run("Query", func(t *testing.T) {
		rows, err := client.Query(ctx, "SELECT * FROM table")
		if err != nil {
			t.Errorf("Query failed: %v", err)
		}
		if rows == nil {
			t.Error("expected non-nil rows")
		}
	})

	t.Run("QueryRow", func(t *testing.T) {
		var dest []any
		err := client.QueryRow(ctx, dest, "SELECT * FROM table WHERE id = ?", 1)
		if err != nil {
			t.Errorf("QueryRow failed: %v", err)
		}
	})

	t.Run("QueryToStruct", func(t *testing.T) {
		var dest struct{}
		err := client.QueryToStruct(ctx, &dest, "SELECT * FROM table WHERE id = ?", 1)
		if err != nil {
			t.Errorf("QueryToStruct failed: %v", err)
		}
	})

	t.Run("QueryToStructs", func(t *testing.T) {
		var dest []struct{}
		err := client.QueryToStructs(ctx, &dest, "SELECT * FROM table")
		if err != nil {
			t.Errorf("QueryToStructs failed: %v", err)
		}
	})

	t.Run("BatchInsert", func(t *testing.T) {
		err := client.BatchInsert(ctx, "INSERT INTO table", func(batch driver.Batch) error {
			return batch.Append(123)
		})
		if err != nil {
			t.Errorf("BatchInsert failed: %v", err)
		}
	})

	t.Run("BatchInsertPrepareError", func(t *testing.T) {
		conn.prepareBatchErr = errors.New("prepare error")
		defer func() { conn.prepareBatchErr = nil }()
		err := client.BatchInsert(ctx, "INSERT INTO table", func(batch driver.Batch) error {
			return nil
		})
		if err == nil || !contains(err.Error(), "prepare batch failed") {
			t.Errorf("BatchInsert expected prepare error, got %v", err)
		}
	})

	t.Run("BatchInsertFnError", func(t *testing.T) {
		err := client.BatchInsert(ctx, "INSERT INTO table", func(batch driver.Batch) error {
			return errors.New("fn error")
		})
		if err == nil || err.Error() != "fn error" {
			t.Errorf("BatchInsert expected fn error, got %v", err)
		}
		if !conn.lastBatch.isAborted {
			t.Error("expected batch to be aborted")
		}
	})

	t.Run("BatchInsertPanic", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic")
			}
			if !conn.lastBatch.isAborted {
				t.Error("expected batch to be aborted after panic")
			}
		}()
		client.BatchInsert(ctx, "INSERT INTO table", func(batch driver.Batch) error {
			panic("oops")
		})
	})

	t.Run("AsyncInsert", func(t *testing.T) {
		err := client.AsyncInsert(ctx, "INSERT INTO table VALUES (?)", true, 123)
		if err != nil {
			t.Errorf("AsyncInsert failed: %v", err)
		}
	})

	t.Run("Close", func(t *testing.T) {
		err := client.Close()
		if err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})
}

func TestDefaultClientBuilderErrors(t *testing.T) {
	// Test Ping error (connection refused)
	// We use a local address that is unlikely to have a ClickHouse server running
	dsn := "clickhouse://localhost:54321"
	_, err := defaultClientBuilder(WithClientBuilderDSN(dsn))
	if err == nil {
		t.Error("expected ping failure error")
	} else if !contains(err.Error(), "ping failed") && !contains(err.Error(), "connect failed") {
		// depending on driver version/behavior it might fail at Open or Ping
		// But "connect failed" is wrapped around Open error, "ping failed" around Ping error.
		// If the port is closed, Open might succeed (lazy) but Ping fails, or Open fails.
		// Let's verify what we get.
		t.Logf("Got error: %v", err)
	}
}

func TestSetAndGetClientBuilder(t *testing.T) {
	originalBuilder := globalBuilder
	defer func() { globalBuilder = originalBuilder }()

	customBuilder := func(builderOpts ...ClientBuilderOpt) (Client, error) {
		return nil, errors.New("custom builder")
	}

	SetClientBuilder(customBuilder)
	builder := GetClientBuilder()

	_, err := builder()
	if err == nil || err.Error() != "custom builder" {
		t.Errorf("expected custom builder error, got %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
