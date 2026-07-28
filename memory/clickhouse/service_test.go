//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

type mockClient struct {
	queryRowFunc func(dest []any) error
	closed       bool
}

func (m *mockClient) Exec(context.Context, string, ...any) error { return nil }

func (m *mockClient) Query(context.Context, string, ...any) (driver.Rows, error) {
	return nil, nil
}

func (m *mockClient) QueryRow(_ context.Context, dest []any, _ string, _ ...any) error {
	if m.queryRowFunc != nil {
		return m.queryRowFunc(dest)
	}
	return nil
}

func (m *mockClient) QueryToStruct(context.Context, any, string, ...any) error {
	return nil
}

func (m *mockClient) QueryToStructs(context.Context, any, string, ...any) error {
	return nil
}

func (m *mockClient) BatchInsert(
	context.Context,
	string,
	storage.BatchFn,
	...driver.PrepareBatchOption,
) error {
	return nil
}

func (m *mockClient) AsyncInsert(context.Context, string, bool, ...any) error {
	return nil
}

func (m *mockClient) Close() error {
	m.closed = true
	return nil
}

func TestNewServiceSeedsLatestWriteVersion(t *testing.T) {
	previousBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(previousBuilder)
	})

	persisted := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	client := &mockClient{
		queryRowFunc: func(dest []any) error {
			*dest[0].(*int64) = persisted.UnixMicro()
			return nil
		},
	}
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})

	service, err := NewService(
		WithClickHouseDSN("clickhouse://localhost:9000/default"),
		WithSkipDBInit(true),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if !service.lastWriteAt.Equal(persisted) {
		t.Fatalf("lastWriteAt = %v, want %v", service.lastWriteAt, persisted)
	}
	if got, want := service.nextWriteAt(), persisted.Add(time.Microsecond); !got.Equal(want) {
		t.Fatalf("nextWriteAt() = %v, want %v", got, want)
	}
}

func TestNewServiceClosesClientWhenVersionSeedFails(t *testing.T) {
	previousBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(previousBuilder)
	})

	seedErr := errors.New("seed failed")
	client := &mockClient{
		queryRowFunc: func([]any) error {
			return seedErr
		},
	}
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})

	service, err := NewService(
		WithClickHouseDSN("clickhouse://localhost:9000/default"),
		WithSkipDBInit(true),
	)
	if !errors.Is(err, seedErr) {
		t.Fatalf("NewService() error = %v, want %v", err, seedErr)
	}
	if service != nil {
		t.Fatalf("NewService() service = %v, want nil", service)
	}
	if !client.closed {
		t.Fatal("NewService() did not close client after seed failure")
	}
}
