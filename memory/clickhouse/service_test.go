//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

type mockClient struct {
	queryRowFunc func(dest []any) error
	queryFunc    func(query string, args ...any) (driver.Rows, error)
	execFunc     func(query string, args ...any) error
	closeErr     error
	closed       bool
}

func (m *mockClient) Exec(_ context.Context, query string, args ...any) error {
	if m.execFunc != nil {
		return m.execFunc(query, args...)
	}
	return nil
}

func (m *mockClient) Query(_ context.Context, query string, args ...any) (driver.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(query, args...)
	}
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
	return m.closeErr
}

type mockRows struct {
	driver.Rows
	values  []string
	current int
	scanErr error
}

func (m *mockRows) Next() bool {
	m.current++
	return m.current < len(m.values)
}

func (m *mockRows) Scan(dest ...any) error {
	if m.scanErr != nil {
		return m.scanErr
	}
	*dest[0].(*string) = m.values[m.current]
	return nil
}

func (m *mockRows) Close() error { return nil }

type memoryStoreClient struct {
	mockClient
	entries map[string]string
	order   []string
}

func newMemoryStoreClient() *memoryStoreClient {
	client := &memoryStoreClient{entries: make(map[string]string)}
	client.execFunc = client.exec
	client.queryFunc = client.query
	return client
}

func (m *memoryStoreClient) exec(query string, args ...any) error {
	if !strings.Contains(query, "INSERT INTO") {
		return nil
	}
	id := args[2].(string)
	if len(args) == 7 {
		delete(m.entries, id)
		return nil
	}
	if _, ok := m.entries[id]; !ok {
		m.order = append(m.order, id)
	}
	m.entries[id] = args[3].(string)
	return nil
}

func (m *memoryStoreClient) query(query string, args ...any) (driver.Rows, error) {
	if len(args) == 3 {
		value, ok := m.entries[args[2].(string)]
		if !ok {
			return &mockRows{current: -1}, nil
		}
		return &mockRows{values: []string{value}, current: -1}, nil
	}
	values := make([]string, 0, len(m.entries))
	for i := len(m.order) - 1; i >= 0; i-- {
		if value, ok := m.entries[m.order[i]]; ok {
			values = append(values, value)
		}
	}
	if strings.Contains(query, "LIMIT 1") && len(values) > 1 {
		values = values[:1]
	}
	return &mockRows{values: values, current: -1}, nil
}

func newServiceWithClient(t *testing.T, client storage.Client, options ...ServiceOpt) *Service {
	t.Helper()
	previousBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(previousBuilder)
	})
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	options = append(options, WithClickHouseDSN("clickhouse://localhost/default"))
	service, err := NewService(options...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return service
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

func TestServiceMemoryLifecycle(t *testing.T) {
	client := newMemoryStoreClient()
	service := newServiceWithClient(t, client,
		WithMemoryLimit(2),
		WithMinSearchScore(0),
		WithMaxResults(10),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}

	if err := service.AddMemory(ctx, userKey, "alpha coffee", []string{"drink"}); err != nil {
		t.Fatalf("AddMemory(alpha) error = %v", err)
	}
	if err := service.AddMemory(ctx, userKey, "beta tennis", []string{"sport"}); err != nil {
		t.Fatalf("AddMemory(beta) error = %v", err)
	}
	if err := service.AddMemory(ctx, userKey, "alpha coffee", []string{"drink"}); err != nil {
		t.Fatalf("AddMemory(existing at limit) error = %v", err)
	}
	if err := service.AddMemory(ctx, userKey, "third", nil); err == nil {
		t.Fatal("AddMemory() error = nil, want memory limit error")
	}

	entries, err := service.ReadMemories(ctx, userKey, 1)
	if err != nil {
		t.Fatalf("ReadMemories() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ReadMemories() len = %d, want 1", len(entries))
	}
	results, err := service.SearchMemories(ctx, userKey, "coffee")
	if err != nil {
		t.Fatalf("SearchMemories() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchMemories() len = %d, want 1", len(results))
	}

	all, err := service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		t.Fatalf("ReadMemories(all) error = %v", err)
	}
	var alpha *memory.Entry
	for _, entry := range all {
		if entry.Memory.Memory == "alpha coffee" {
			alpha = entry
		}
	}
	if alpha == nil {
		t.Fatal("alpha memory not found")
	}
	updateResult := &memory.UpdateResult{}
	if err := service.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: alpha.ID,
	}, "alpha espresso", []string{"drink"}, memory.WithUpdateResult(updateResult)); err != nil {
		t.Fatalf("UpdateMemory() error = %v", err)
	}
	if updateResult.MemoryID == "" || updateResult.MemoryID == alpha.ID {
		t.Fatalf("UpdateMemory() result ID = %q, want rotated ID", updateResult.MemoryID)
	}
	if err := service.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: updateResult.MemoryID,
	}); err != nil {
		t.Fatalf("DeleteMemory() error = %v", err)
	}
	if err := service.ClearMemories(ctx, userKey); err != nil {
		t.Fatalf("ClearMemories() error = %v", err)
	}
	entries, err = service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		t.Fatalf("ReadMemories(after clear) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ReadMemories(after clear) len = %d, want 0", len(entries))
	}
	if len(service.Tools()) == 0 {
		t.Fatal("Tools() returned no default tools")
	}
	if err := service.EnqueueAutoMemoryJob(ctx, nil); err != nil {
		t.Fatalf("EnqueueAutoMemoryJob() error = %v", err)
	}
}

func TestServiceValidationAndNotFound(t *testing.T) {
	service := newServiceWithClient(t, newMemoryStoreClient(), WithSkipDBInit(true))
	ctx := context.Background()
	badUser := memory.UserKey{UserID: "user"}
	badKey := memory.Key{AppName: "app", UserID: "user"}

	if err := service.AddMemory(ctx, badUser, "x", nil); err == nil {
		t.Fatal("AddMemory() error = nil for invalid user key")
	}
	if _, err := service.ReadMemories(ctx, badUser, 0); err == nil {
		t.Fatal("ReadMemories() error = nil for invalid user key")
	}
	if _, err := service.SearchMemories(ctx, badUser, "x"); err == nil {
		t.Fatal("SearchMemories() error = nil for invalid user key")
	}
	if err := service.UpdateMemory(ctx, badKey, "x", nil); err == nil {
		t.Fatal("UpdateMemory() error = nil for invalid memory key")
	}
	if err := service.DeleteMemory(ctx, badKey); err == nil {
		t.Fatal("DeleteMemory() error = nil for invalid memory key")
	}
	if err := service.ClearMemories(ctx, badUser); err == nil {
		t.Fatal("ClearMemories() error = nil for invalid user key")
	}

	missing := memory.Key{AppName: "app", UserID: "user", MemoryID: "missing"}
	if err := service.UpdateMemory(ctx, missing, "x", nil); err == nil {
		t.Fatal("UpdateMemory() error = nil for missing memory")
	}
	if err := service.DeleteMemory(ctx, missing); err == nil {
		t.Fatal("DeleteMemory() error = nil for missing memory")
	}
}

func TestServiceStorageErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	memoryKey := memory.Key{AppName: "app", UserID: "user", MemoryID: "id"}
	queryErr := errors.New("query failed")

	client := &mockClient{
		queryFunc: func(string, ...any) (driver.Rows, error) {
			return nil, queryErr
		},
	}
	service := newServiceWithClient(t, client, WithSkipDBInit(true))
	if err := service.AddMemory(ctx, userKey, "x", nil); !errors.Is(err, queryErr) {
		t.Fatalf("AddMemory() error = %v, want %v", err, queryErr)
	}
	if err := service.UpdateMemory(ctx, memoryKey, "x", nil); !errors.Is(err, queryErr) {
		t.Fatalf("UpdateMemory() error = %v, want %v", err, queryErr)
	}
	if err := service.DeleteMemory(ctx, memoryKey); !errors.Is(err, queryErr) {
		t.Fatalf("DeleteMemory() error = %v, want %v", err, queryErr)
	}
	if err := service.ClearMemories(ctx, userKey); !errors.Is(err, queryErr) {
		t.Fatalf("ClearMemories() error = %v, want %v", err, queryErr)
	}
	if _, err := service.ReadMemories(ctx, userKey, 0); !errors.Is(err, queryErr) {
		t.Fatalf("ReadMemories() error = %v, want %v", err, queryErr)
	}

	scanErr := errors.New("scan failed")
	client.queryFunc = func(string, ...any) (driver.Rows, error) {
		return &mockRows{values: []string{"unused"}, current: -1, scanErr: scanErr}, nil
	}
	if _, err := service.ReadMemories(ctx, userKey, 0); !errors.Is(err, scanErr) {
		t.Fatalf("ReadMemories(scan) error = %v, want %v", err, scanErr)
	}
	if err := service.UpdateMemory(ctx, memoryKey, "x", nil); !errors.Is(err, scanErr) {
		t.Fatalf("UpdateMemory(scan) error = %v, want %v", err, scanErr)
	}

	client.queryFunc = func(string, ...any) (driver.Rows, error) {
		return &mockRows{values: []string{"{"}, current: -1}, nil
	}
	if _, err := service.ReadMemories(ctx, userKey, 0); err == nil {
		t.Fatal("ReadMemories(decode) error = nil")
	}
	if err := service.DeleteMemory(ctx, memoryKey); err == nil {
		t.Fatal("DeleteMemory(decode) error = nil")
	}
}

func TestServiceWriteAndRotationErrors(t *testing.T) {
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	store := newMemoryStoreClient()
	service := newServiceWithClient(t, store, WithSkipDBInit(true), WithMemoryLimit(0))
	if err := service.AddMemory(ctx, userKey, "source", nil); err != nil {
		t.Fatalf("AddMemory(source) error = %v", err)
	}
	if err := service.AddMemory(ctx, userKey, "target", nil); err != nil {
		t.Fatalf("AddMemory(target) error = %v", err)
	}
	entries, err := service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		t.Fatalf("ReadMemories() error = %v", err)
	}
	ids := make(map[string]string)
	for _, entry := range entries {
		ids[entry.Memory.Memory] = entry.ID
	}
	err = service.UpdateMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: ids["source"],
	}, "target", nil)
	if err == nil {
		t.Fatal("UpdateMemory() error = nil for duplicate target identity")
	}

	writeErr := errors.New("write failed")
	store.execFunc = func(query string, args ...any) error {
		if strings.Contains(query, "INSERT INTO") {
			return writeErr
		}
		return nil
	}
	if err := service.AddMemory(ctx, userKey, "new", nil); !errors.Is(err, writeErr) {
		t.Fatalf("AddMemory(write) error = %v, want %v", err, writeErr)
	}
	if err := service.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: ids["source"],
	}); !errors.Is(err, writeErr) {
		t.Fatalf("DeleteMemory(write) error = %v, want %v", err, writeErr)
	}
	if err := service.ClearMemories(ctx, userKey); !errors.Is(err, writeErr) {
		t.Fatalf("ClearMemories(write) error = %v, want %v", err, writeErr)
	}
}

func TestNewServiceConfigurationErrors(t *testing.T) {
	previousBuilder := storage.GetClientBuilder()
	t.Cleanup(func() {
		storage.SetClientBuilder(previousBuilder)
	})

	builderErr := errors.New("builder failed")
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, builderErr
	})
	if _, err := NewService(WithClickHouseDSN("dsn")); !errors.Is(err, builderErr) {
		t.Fatalf("NewService(builder) error = %v, want %v", err, builderErr)
	}
	if _, err := NewService(WithClickHouseInstance("missing")); err == nil {
		t.Fatal("NewService(instance) error = nil for missing instance")
	}

	client := &mockClient{
		execFunc: func(string, ...any) error {
			return fmt.Errorf("schema failed")
		},
	}
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	if _, err := NewService(WithClickHouseDSN("dsn")); err == nil {
		t.Fatal("NewService(init) error = nil")
	}
	if !client.closed {
		t.Fatal("NewService(init) did not close client")
	}
}

func TestReadEntriesNormalizesStoredEntry(t *testing.T) {
	now := time.Now().UTC()
	data, err := json.Marshal(&memory.Entry{
		ID: "id", AppName: "app", UserID: "user",
		Memory: &memory.Memory{Memory: "value"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	client := &mockClient{
		queryFunc: func(string, ...any) (driver.Rows, error) {
			return &mockRows{values: []string{string(data)}, current: -1}, nil
		},
	}
	service := newServiceWithClient(t, client, WithSkipDBInit(true))
	entries, err := service.ReadMemories(
		context.Background(), memory.UserKey{AppName: "app", UserID: "user"}, 0,
	)
	if err != nil {
		t.Fatalf("ReadMemories() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Memory == nil {
		t.Fatalf("ReadMemories() = %#v, want one normalized entry", entries)
	}
}
