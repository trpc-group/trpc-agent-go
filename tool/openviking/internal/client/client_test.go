//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func writeOK(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
}

func TestReadSendsParamsAndSetsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("uri"); got != "viking://resources/a" {
			t.Errorf("uri = %q", got)
		}
		if got := r.URL.Query().Get("offset"); got != "2" {
			t.Errorf("offset = %q, want 2", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit = %q, want 5", got)
		}
		if got := r.Header.Get("X-API-Key"); got != "secret" {
			t.Errorf("X-API-Key = %q", got)
		}
		if got := r.Header.Get("X-OpenViking-User"); got != "alice" {
			t.Errorf("X-OpenViking-User = %q", got)
		}
		writeOK(w, "file body")
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "secret", User: "alice"})
	content, err := c.Read(context.Background(), "viking://resources/a", 2, 5)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "file body" {
		t.Errorf("content = %q", content)
	}
}

func TestNonRecoverableErrorIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"error":  map[string]any{"code": "NOT_FOUND", "message": "missing"},
		})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Find(context.Background(), FindRequest{Query: "q"})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("non-recoverable error should not retry, calls = %d", calls.Load())
	}
}

func TestReadOnlyRetriesOnTransientHTTPStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Non-JSON gateway error: must still be classified as transient.
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("upstream overloaded"))
			return
		}
		writeOK(w, "recovered")
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	content, err := c.Read(context.Background(), "viking://resources/a", 0, -1)
	if err != nil {
		t.Fatalf("Read after retry: %v", err)
	}
	if content != "recovered" {
		t.Errorf("content = %q, want recovered", content)
	}
	if calls.Load() != 2 {
		t.Errorf("transient 503 should retry once, calls = %d", calls.Load())
	}
}

func TestNonTransientHTTPStatusNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad query"))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if _, err := c.Find(context.Background(), FindRequest{Query: "q"}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("non-transient 400 should not retry, calls = %d", calls.Load())
	}
}

func TestReadOnlyRetriesOnceThenFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"error":  map[string]any{"code": "DEADLINE_EXCEEDED", "message": "slow"},
		})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Find(context.Background(), FindRequest{Query: "q"})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 2 {
		t.Errorf("recoverable error should retry once, calls = %d", calls.Load())
	}
}
