//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package client provides Wikipedia API client.
package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNew tests the creation of a new Wikipedia client.
func TestNew(t *testing.T) {
	baseURL := "https://en.wikipedia.org/w/api.php"
	userAgent := "test-agent"
	httpClient := &http.Client{Timeout: 10 * time.Second}

	client := New(baseURL, userAgent, httpClient)

	if client == nil {
		t.Fatal("New() returned nil")
	}
	if client.baseURL != baseURL {
		t.Errorf("Expected baseURL '%s', got '%s'", baseURL, client.baseURL)
	}
	if client.userAgent != userAgent {
		t.Errorf("Expected userAgent '%s', got '%s'", userAgent, client.userAgent)
	}
	if client.httpClient != httpClient {
		t.Error("httpClient not set correctly")
	}
}

// mockSearchServer creates a mock HTTP server for search tests.
func mockSearchServer(t *testing.T, validator func(*http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if validator != nil {
			validator(r)
		}
		response := SearchResponse{}
		response.Query.SearchInfo.TotalHits = 1
		response.Query.Search = []SearchResult{
			{Title: "Test", PageID: 123, Snippet: "snippet"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
}

// mockPageServer creates a mock HTTP server for page content tests.
func mockPageServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := PageContentResponse{}
		response.Query.Pages = map[string]PageContent{
			"123": {PageID: 123, Title: "Test", Extract: "content", FullURL: "http://test"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
}

// TestSearchMethods tests all search-related methods with table-driven approach.
func TestSearchMethods(t *testing.T) {
	tests := []struct {
		name      string
		method    func(*Client) error
		wantError bool
	}{
		{"Search", func(c *Client) error { _, err := c.Search("test", 5); return err }, false},
		{"SearchEmpty", func(c *Client) error { _, err := c.Search("", 5); return err }, true},
		{"SearchInvalidLimit", func(c *Client) error { _, err := c.Search("test", 0); return err }, false},
		{"QuickSearch", func(c *Client) error { _, err := c.QuickSearch("test", 5); return err }, false},
		{"QuickSearchEmpty", func(c *Client) error { _, err := c.QuickSearch("", 5); return err }, true},
		{"DetailedSearch", func(c *Client) error { _, err := c.DetailedSearch("test", 5, false); return err }, false},
		{"DetailedSearchAll", func(c *Client) error { _, err := c.DetailedSearch("test", 5, true); return err }, false},
		{"DetailedSearchEmpty", func(c *Client) error { _, err := c.DetailedSearch("", 5, false); return err }, true},
		{"ExactTitleSearch", func(c *Client) error { _, err := c.ExactTitleSearch("test"); return err }, false},
		{"ExactTitleSearchEmpty", func(c *Client) error { _, err := c.ExactTitleSearch(""); return err }, true},
		{"PrefixSearch", func(c *Client) error { _, err := c.PrefixSearch("test", 5); return err }, false},
		{"PrefixSearchEmpty", func(c *Client) error { _, err := c.PrefixSearch("", 5); return err }, true},
		{"FullTextSearch", func(c *Client) error {
			_, err := c.FullTextSearch("test", 5, []int{0}, true)
			return err
		}, false},
		{"FullTextSearchEmpty", func(c *Client) error {
			_, err := c.FullTextSearch("", 5, nil, false)
			return err
		}, true},
		{"AdvancedSearch", func(c *Client) error {
			_, err := c.AdvancedSearch(SearchOptions{Query: "test", Limit: 5})
			return err
		}, false},
		{"AdvancedSearchEmpty", func(c *Client) error {
			_, err := c.AdvancedSearch(SearchOptions{Query: "", Limit: 5})
			return err
		}, true},
		{"AdvancedSearchFull", func(c *Client) error {
			_, err := c.AdvancedSearch(SearchOptions{
				Query:           "test",
				Limit:           10,
				Offset:          5,
				SearchWhat:      "text",
				Properties:      "snippet",
				Namespaces:      []int{0, 14},
				Sort:            "relevance",
				EnableRedirects: true,
			})
			return err
		}, false},
	}

	server := mockSearchServer(t, nil)
	defer server.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(server.URL, "test", &http.Client{})
			err := tt.method(client)
			if (err != nil) != tt.wantError {
				t.Errorf("wantError=%v, got err=%v", tt.wantError, err)
			}
		})
	}
}

// TestPageContentMethods tests page content retrieval methods.
func TestPageContentMethods(t *testing.T) {
	tests := []struct {
		name      string
		method    func(*Client) error
		wantError bool
	}{
		{"GetPageContent", func(c *Client) error { _, err := c.GetPageContent("test"); return err }, false},
		{"GetPageContentEmpty", func(c *Client) error { _, err := c.GetPageContent(""); return err }, true},
		{"GetPageSummary", func(c *Client) error { _, err := c.GetPageSummary("test"); return err }, false},
		{"GetPageSummaryEmpty", func(c *Client) error { _, err := c.GetPageSummary(""); return err }, true},
	}

	server := mockPageServer(t)
	defer server.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(server.URL, "test", &http.Client{})
			err := tt.method(client)
			if (err != nil) != tt.wantError {
				t.Errorf("wantError=%v, got err=%v", tt.wantError, err)
			}
		})
	}
}

// TestHTTPErrors tests HTTP error handling across all methods.
func TestHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL, "test", &http.Client{})

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Search", func() error { _, err := client.Search("test", 5); return err }},
		{"QuickSearch", func() error { _, err := client.QuickSearch("test", 5); return err }},
		{"DetailedSearch", func() error { _, err := client.DetailedSearch("test", 5, false); return err }},
		{"GetPageContent", func() error { _, err := client.GetPageContent("test"); return err }},
		{"GetPageSummary", func() error { _, err := client.GetPageSummary("test"); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Error("Expected error for HTTP 500, got nil")
			}
		})
	}
}

// TestInvalidJSON tests invalid JSON handling.
func TestInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := New(server.URL, "test", &http.Client{})

	if _, err := client.Search("test", 5); err == nil {
		t.Error("Expected error for invalid JSON")
	}
	if _, err := client.GetPageContent("test"); err == nil {
		t.Error("Expected error for invalid JSON")
	}
}
