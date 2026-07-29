//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubSessionService is a minimal session.Service for testing factory registration.
type stubSessionService struct{ name string }

func (s *stubSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, options ...session.Option) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}
func (s *stubSessionService) Close() error { return nil }

// stubMemoryService is a minimal memory.Service for testing factory registration.
type stubMemoryService struct{ name string }

func (s *stubMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, mem string, topics []string, opts ...memory.AddOption) error {
	return fmt.Errorf("not implemented")
}
func (s *stubMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, mem string, topics []string, opts ...memory.UpdateOption) error {
	return fmt.Errorf("not implemented")
}
func (s *stubMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return fmt.Errorf("not implemented")
}
func (s *stubMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return fmt.Errorf("not implemented")
}
func (s *stubMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubMemoryService) Tools() []tool.Tool { return nil }
func (s *stubMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return fmt.Errorf("not implemented")
}
func (s *stubMemoryService) Close() error { return nil }

// TestRegisterSessionFactory_AndList tests registering session factories and listing registered backends.
func TestRegisterSessionFactory_AndList(t *testing.T) {
	origFactories := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origFactories }()

	RegisterSessionFactory("test_a", func(ctx context.Context, dbURL string) (session.Service, error) {
		return &stubSessionService{name: "a"}, nil
	})
	RegisterSessionFactory("test_b", func(ctx context.Context, dbURL string) (session.Service, error) {
		return &stubSessionService{name: "b"}, nil
	})

	names := RegisteredSessionBackends()
	expected := []string{"test_a", "test_b"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d backends, got %d: %v", len(expected), len(names), names)
	}
	if names[0] != "test_a" || names[1] != "test_b" {
		t.Errorf("expected sorted [test_a test_b], got %v", names)
	}
}

// TestRegisterMemoryFactory_AndList tests registering memory factories and listing registered backends.
func TestRegisterMemoryFactory_AndList(t *testing.T) {
	origFactories := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origFactories }()

	RegisterMemoryFactory("mem_x", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return &stubMemoryService{name: "x"}, nil
	})
	RegisterMemoryFactory("mem_y", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return &stubMemoryService{name: "y"}, nil
	})

	names := RegisteredMemoryBackends()
	expected := []string{"mem_x", "mem_y"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d backends, got %d: %v", len(expected), len(names), names)
	}
	if names[0] != "mem_x" || names[1] != "mem_y" {
		t.Errorf("expected sorted [mem_x mem_y], got %v", names)
	}
}

// TestRegisteredSessionBackends_Empty tests listing when no factories are registered.
func TestRegisteredSessionBackends_Empty(t *testing.T) {
	origFactories := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origFactories }()

	names := RegisteredSessionBackends()
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

// TestRegisteredMemoryBackends_Empty tests listing when no factories are registered.
func TestRegisteredMemoryBackends_Empty(t *testing.T) {
	origFactories := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origFactories }()

	names := RegisteredMemoryBackends()
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

// TestNewSessionService_Unregistered tests error path for unknown backend.
func TestNewSessionService_Unregistered(t *testing.T) {
	origFactories := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origFactories }()

	_, err := NewSessionService(context.Background(), "nonexistent", "")
	if err == nil {
		t.Error("expected error for unregistered session backend")
	}
}

// TestNewMemoryService_Unregistered tests error path for unknown backend.
func TestNewMemoryService_Unregistered(t *testing.T) {
	origFactories := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origFactories }()

	_, err := NewMemoryService(context.Background(), "nonexistent", "")
	if err == nil {
		t.Error("expected error for unregistered memory backend")
	}
}

// TestNewSessionService_Success tests the success path.
func TestNewSessionService_Success(t *testing.T) {
	origFactories := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origFactories }()

	RegisterSessionFactory("test_svc", func(ctx context.Context, dbURL string) (session.Service, error) {
		return &stubSessionService{name: "test_svc"}, nil
	})

	svc, err := NewSessionService(context.Background(), "test_svc", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if svc == nil {
		t.Error("expected non-nil service")
	}
}

// TestNewMemoryService_Success tests the success path.
func TestNewMemoryService_Success(t *testing.T) {
	origFactories := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origFactories }()

	RegisterMemoryFactory("test_mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return &stubMemoryService{name: "test_mem"}, nil
	})

	svc, err := NewMemoryService(context.Background(), "test_mem", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if svc == nil {
		t.Error("expected non-nil service")
	}
}

// TestNewSessionService_FactoryError tests that factory errors propagate.
func TestNewSessionService_FactoryError(t *testing.T) {
	origFactories := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origFactories }()

	RegisterSessionFactory("bad_svc", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errors.New("factory error")
	})

	_, err := NewSessionService(context.Background(), "bad_svc", "")
	if err == nil {
		t.Error("expected factory error to propagate")
	}
	if err.Error() != "factory error" {
		t.Errorf("expected 'factory error', got: %v", err)
	}
}
