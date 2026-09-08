//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package youcom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestOption(t *testing.T) {
	type testCase struct {
		name    string
		opts    []Option
		wantCfg config
	}

	testCases := []testCase{
		{
			name:    "empty",
			opts:    nil,
			wantCfg: config{},
		},
		{
			name: "with options",
			opts: []Option{
				WithAPIKey("key-123"),
				WithBaseURL("https://example.com/api/search"),
				WithUserAgent("custom-agent/2.0"),
				WithNumResults(5),
				WithCountry("de"),
				WithSafeSearch("strict"),
			},
			wantCfg: config{
				apiKey:     "key-123",
				baseURL:    "https://example.com/api/search",
				userAgent:  "custom-agent/2.0",
				numResults: 5,
				country:    "DE",
				safeSearch: "strict",
			},
		},
		{
			name: "num results clamped to max",
			opts: []Option{
				WithNumResults(50),
			},
			wantCfg: config{
				numResults: maxResults,
			},
		},
		{
			name: "num results clamped to default",
			opts: []Option{
				WithNumResults(-3),
			},
			wantCfg: config{
				numResults: maxResults,
			},
		},
		{
			name: "invalid safe search ignored",
			opts: []Option{
				WithSafeSearch("bogus"),
			},
			wantCfg: config{
				safeSearch: "",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config{}
			for _, opt := range tc.opts {
				opt(cfg)
			}
			if !reflect.DeepEqual(*cfg, tc.wantCfg) {
				t.Errorf("got config %+v, want %+v", *cfg, tc.wantCfg)
			}
		})
	}
}

func TestNewToolSet(t *testing.T) {
	type testCase struct {
		name      string
		opts      []Option
		wantErr   bool
		wantTools int
	}

	testCases := []testCase{
		{
			name:    "missing api key",
			opts:    nil,
			wantErr: true,
		},
		{
			name: "empty api key",
			opts: []Option{
				WithAPIKey("   "),
			},
			wantErr: true,
		},
		{
			name: "with api key",
			opts: []Option{
				WithAPIKey("key-123"),
			},
			wantErr:   false,
			wantTools: 1,
		},
		{
			name: "with api key and base url",
			opts: []Option{
				WithAPIKey("key-123"),
				WithBaseURL("https://example.com/api/search"),
				WithNumResults(5),
				WithCountry("US"),
				WithSafeSearch("moderate"),
			},
			wantErr:   false,
			wantTools: 1,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toolSet, err := NewToolSet(tc.opts...)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(toolSet.tools) != tc.wantTools {
				t.Errorf("got %d tools, want %d", len(toolSet.tools), tc.wantTools)
			}
		})
	}
}

func TestTools(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	tools := toolSet.Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
}

func TestName(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if toolSet.Name() != "youcom" {
		t.Fatalf("got name %s, want youcom", toolSet.Name())
	}
}

func TestClose(t *testing.T) {
	toolSet, err := NewToolSet(WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}
	if err := toolSet.Close(); err != nil {
		t.Fatalf("failed to close tool set: %v", err)
	}
}

// newFakeYoucomAPI returns an httptest server that emits the given results
// as a You.com Web Search API response and records the latest request.
func newFakeYoucomAPI(t *testing.T, results []youcomAPIResult) (*httptest.Server, **http.Request) {
	t.Helper()
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			captured = r
			w.Header().Set("Content-Type", "application/json")
			resp := youcomAPIResponse{Results: results}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("encode fake response: %v", err)
			}
		},
	))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestSearchTool(t *testing.T) {
	fakeResults := []youcomAPIResult{
		{
			URL:      "https://example.com/go",
			Title:    "Example Go page",
			Snippets: []string{" An excerpt about Go. ", ""},
		},
		{
			URL:          "https://example.com/preview",
			Title:        "Example with thumbnail",
			Snippets:     []string{"Another excerpt."},
			ThumbnailURL: "https://example.com/thumb.png",
		},
		{
			// No URL and no title: must be dropped from output.
			Snippets: []string{"orphan snippet"},
		},
	}
	srv, captured := newFakeYoucomAPI(t, fakeResults)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
		WithNumResults(5),
		WithCountry("US"),
		WithSafeSearch("moderate"),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "golang testing"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("search call failed: %v", err)
	}

	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if resp.Query != "golang testing" {
		t.Errorf("expected query 'golang testing', got %q", resp.Query)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].URL != "https://example.com/go" {
		t.Errorf("unexpected URL: %q", resp.Results[0].URL)
	}
	if resp.Results[0].Title != "Example Go page" {
		t.Errorf("unexpected title: %q", resp.Results[0].Title)
	}
	// Empty snippets must be dropped, non-empty kept.
	if len(resp.Results[0].Snippets) != 1 ||
		resp.Results[0].Snippets[0] != "An excerpt about Go." {
		t.Errorf("unexpected snippets: %#v", resp.Results[0].Snippets)
	}
	if resp.Results[1].ThumbnailURL != "https://example.com/thumb.png" {
		t.Errorf("unexpected thumbnail URL: %q", resp.Results[1].ThumbnailURL)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error field: %q", resp.Error)
	}

	// Verify the request reached the fake API with the expected parameters.
	req := *captured
	if got := req.URL.Query().Get("query"); got != "golang testing" {
		t.Errorf("expected query param 'golang testing', got %q", got)
	}
	if got := req.URL.Query().Get("num_results"); got != "5" {
		t.Errorf("expected num_results param '5', got %q", got)
	}
	if got := req.URL.Query().Get("country"); got != "US" {
		t.Errorf("expected country param 'US', got %q", got)
	}
	if got := req.URL.Query().Get("safesearch"); got != "moderate" {
		t.Errorf("expected safesearch param 'moderate', got %q", got)
	}
	if got := req.Header.Get("X-API-Key"); got != "key-123" {
		t.Errorf("expected X-API-Key header, got %q", got)
	}
}

func TestSearchToolRequestOverrides(t *testing.T) {
	srv, captured := newFakeYoucomAPI(t, []youcomAPIResult{
		{URL: "https://example.com/1", Title: "One"},
	})

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{
		Query:      "override test",
		NumResults: 2,
		Country:    "de",
		SafeSearch: "strict",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := searchTool.Call(context.Background(), reqJSON); err != nil {
		t.Fatalf("search call failed: %v", err)
	}

	req := *captured
	if got := req.URL.Query().Get("num_results"); got != "2" {
		t.Errorf("expected num_results param '2', got %q", got)
	}
	if got := req.URL.Query().Get("country"); got != "DE" {
		t.Errorf("expected country param 'DE', got %q", got)
	}
	if got := req.URL.Query().Get("safesearch"); got != "strict" {
		t.Errorf("expected safesearch param 'strict', got %q", got)
	}
}

func TestSearchToolEmptyQuery(t *testing.T) {
	srv, _ := newFakeYoucomAPI(t, nil)

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "   "})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected error for empty query, got nil")
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
	if !strings.Contains(resp.Error, "empty search query") {
		t.Errorf("unexpected error field: %q", resp.Error)
	}
}

func TestSearchToolServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		},
	))
	defer srv.Close()

	toolSet, err := NewToolSet(
		WithAPIKey("key-123"),
		WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("failed to create tool set: %v", err)
	}

	searchTool, ok := toolSet.tools[0].(interface {
		Call(context.Context, []byte) (any, error)
	})
	if !ok {
		t.Fatalf("tool does not implement Call")
	}

	reqJSON, err := json.Marshal(searchRequest{Query: "anything"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	result, err := searchTool.Call(context.Background(), reqJSON)
	if err == nil {
		t.Fatalf("expected error on 401, got nil")
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
	resp, ok := result.(searchResponse)
	if !ok {
		t.Fatalf("expected searchResponse type, got %T", result)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Results))
	}
}
