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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestWritePathsNeverRetried(t *testing.T) {
	// Writes must never be retried even on an otherwise recoverable error, so a
	// transient failure cannot duplicate a session/message/resource side effect.
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*Client) error
	}{
		{"CreateSession", func(c *Client) error { _, err := c.CreateSession(ctx, "s1"); return err }},
		{"AddMessage", func(c *Client) error { _, err := c.AddMessage(ctx, "s1", "user", "hi"); return err }},
		{"CommitSession", func(c *Client) error { _, err := c.CommitSession(ctx, "s1"); return err }},
		{"AddResource", func(c *Client) error { _, err := c.AddResource(ctx, "p", "", "", false); return err }},
		{"AddSkill", func(c *Client) error { _, err := c.AddSkill(ctx, "data", false); return err }},
		{"Remove", func(c *Client) error { _, err := c.Remove(ctx, "viking://x", false); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "error",
					"error":  map[string]any{"code": "UNAVAILABLE", "message": "down"},
				})
			}))
			defer srv.Close()

			c := New(Config{BaseURL: srv.URL})
			if err := tc.call(c); err == nil {
				t.Fatal("expected error")
			}
			if calls.Load() != 1 {
				t.Errorf("write path must not retry, calls = %d", calls.Load())
			}
		})
	}
}

func TestContextCanceledNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeOK(w, map[string]any{})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(Config{BaseURL: srv.URL})
	if _, err := c.Find(ctx, FindRequest{Query: "q"}); err == nil {
		t.Fatal("expected error from canceled context")
	}
	if calls.Load() > 1 {
		t.Errorf("canceled context must not trigger a retry, calls = %d", calls.Load())
	}
}

// TestClientEndpoints exercises every client method against a routing mock so
// request construction (method, path, query, body, auth headers) and result
// decoding stay covered.
func TestClientEndpoints(t *testing.T) {
	type captured struct {
		method  string
		path    string
		query   string
		body    map[string]any
		headers http.Header
	}
	var mu sync.Mutex
	var last captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			_ = json.Unmarshal(data, &body)
		}
		mu.Lock()
		last = captured{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body, headers: r.Header.Clone()}
		mu.Unlock()
		// Content endpoints decode into a string; everything else is raw JSON.
		if strings.HasPrefix(r.URL.Path, "/api/v1/content/") {
			writeOK(w, "content-body")
			return
		}
		writeOK(w, map[string]any{"ok": true})
	}))
	defer srv.Close()

	ctx := context.Background()
	c := New(Config{BaseURL: srv.URL, APIKey: "k", Account: "acct", User: "u", Agent: "ag"})

	score := 0.5
	if _, err := c.Search(ctx, FindRequest{Query: "q", TargetURI: "viking://r", SessionID: "s1", Limit: 3, ScoreThreshold: &score}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	mu.Lock()
	if last.body["session_id"] != "s1" || last.body["target_uri"] != "viking://r" {
		t.Errorf("search body = %v", last.body)
	}
	for _, h := range []string{"X-Api-Key", "X-Openviking-Account", "X-Openviking-User", "X-Openviking-Agent"} {
		if last.headers.Get(h) == "" {
			t.Errorf("missing auth header %s", h)
		}
	}
	mu.Unlock()

	if got, err := c.Abstract(ctx, "viking://r"); err != nil || got != "content-body" {
		t.Errorf("Abstract = %q, err = %v", got, err)
	}
	if got, err := c.Overview(ctx, "viking://r"); err != nil || got != "content-body" {
		t.Errorf("Overview = %q, err = %v", got, err)
	}
	if _, err := c.Ls(ctx, "viking://r", true); err != nil {
		t.Fatalf("Ls: %v", err)
	}
	mu.Lock()
	if last.path != "/api/v1/fs/ls" || !strings.Contains(last.query, "recursive=true") {
		t.Errorf("ls path/query = %s?%s", last.path, last.query)
	}
	mu.Unlock()

	if _, err := c.Glob(ctx, "*.go", ""); err != nil {
		t.Fatalf("Glob: %v", err)
	}
	mu.Lock()
	if last.body["uri"] != "viking://" { // empty uri defaults to root
		t.Errorf("glob default uri = %v", last.body["uri"])
	}
	mu.Unlock()

	if _, err := c.Grep(ctx, "viking://r", "func", true, 10); err != nil {
		t.Fatalf("Grep: %v", err)
	}
	mu.Lock()
	if last.body["case_insensitive"] != true || last.body["node_limit"].(float64) != 10 {
		t.Errorf("grep body = %v", last.body)
	}
	mu.Unlock()

	if _, err := c.CreateSession(ctx, "s1"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := c.AddMessage(ctx, "s 1", "user", "hi"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	mu.Lock()
	// r.URL.Path is already unescaped; the wire form was sessions/s%201/messages.
	if last.path != "/api/v1/sessions/s 1/messages" {
		t.Errorf("addmessage path = %s", last.path)
	}
	mu.Unlock()

	if _, err := c.CommitSession(ctx, "s1"); err != nil {
		t.Fatalf("CommitSession: %v", err)
	}
	if _, err := c.AddResource(ctx, "https://x", "viking://to", "viking://parent", true); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	mu.Lock()
	if last.body["to"] != "viking://to" || last.body["parent"] != "viking://parent" || last.body["wait"] != true {
		t.Errorf("addresource body = %v", last.body)
	}
	mu.Unlock()

	if _, err := c.AddSkill(ctx, "skill-data", false); err != nil {
		t.Fatalf("AddSkill: %v", err)
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := c.Remove(ctx, "viking://x", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	mu.Lock()
	if last.method != http.MethodDelete || last.path != "/api/v1/fs" {
		t.Errorf("remove method/path = %s %s", last.method, last.path)
	}
	mu.Unlock()
}

func TestCloseIsNoOp(t *testing.T) {
	if err := New(Config{}).Close(); err != nil {
		t.Errorf("Close = %v", err)
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
