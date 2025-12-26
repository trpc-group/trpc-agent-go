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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// tests the creation of a new Wikipedia client.
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

// TestSearch tests the Search method with a mock server.
func TestSearch(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		// Verify query parameters
		query := r.URL.Query()
		if query.Get("action") != "query" {
			t.Error("Expected action=query")
		}
		if query.Get("list") != "search" {
			t.Error("Expected list=search")
		}
		if query.Get("format") != "json" {
			t.Error("Expected format=json")
		}

		searchQuery := query.Get("srsearch")
		if searchQuery == "" {
			t.Error("Expected srsearch parameter")
		}

		// Return mock response
		response := SearchResponse{
			BatchComplete: "",
		}
		response.Query.SearchInfo.TotalHits = 1
		response.Query.Search = []SearchResult{
			{
				NS:        0,
				Title:     "Test Article",
				PageID:    12345,
				Size:      10000,
				WordCount: 1000,
				Snippet:   "This is a test article snippet",
				Timestamp: "2025-01-01T00:00:00Z",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client with mock server URL
	client := New(server.URL, "test-agent", &http.Client{Timeout: 5 * time.Second})

	// Perform search
	result, err := client.Search("test query", 5)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	// Verify result
	if result == nil {
		t.Fatal("Search() returned nil result")
	}

	if len(result.Query.Search) != 1 {
		t.Errorf("Expected 1 search result, got %d", len(result.Query.Search))
	}

	if result.Query.Search[0].Title != "Test Article" {
		t.Errorf("Expected title 'Test Article', got '%s'", result.Query.Search[0].Title)
	}

	if result.Query.Search[0].PageID != 12345 {
		t.Errorf("Expected pageID 12345, got %d", result.Query.Search[0].PageID)
	}
}

// TestSearchEmptyQuery tests Search with an empty query.
func TestSearchEmptyQuery(t *testing.T) {
	client := New("https://en.wikipedia.org/w/api.php", "test-agent", &http.Client{})

	_, err := client.Search("", 5)
	if err == nil {
		t.Error("Expected error for empty query, got nil")
	}
}

// TestSearchInvalidLimit tests Search with invalid limit values.
func TestSearchInvalidLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := r.URL.Query().Get("srlimit")
		if limit != "5" {
			t.Errorf("Expected default limit '5', got '%s'", limit)
		}

		response := SearchResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(server.URL, "test-agent", &http.Client{})

	// Test with zero limit (should default to 5)
	_, err := client.Search("test", 0)
	if err != nil {
		t.Errorf("Search() with zero limit failed: %v", err)
	}

	// Test with negative limit (should default to 5)
	_, err = client.Search("test", -1)
	if err != nil {
		t.Errorf("Search() with negative limit failed: %v", err)
	}
}

// TestSearchHTTPError tests Search with HTTP error response.
func TestSearchHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL, "test-agent", &http.Client{})

	_, err := client.Search("test", 5)
	if err == nil {
		t.Error("Expected error for HTTP 500, got nil")
	}
}

// TestSearchInvalidJSON tests Search with invalid JSON response.
func TestSearchInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := New(server.URL, "test-agent", &http.Client{})

	_, err := client.Search("test", 5)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

// TestSearchMultipleResults tests Search with multiple results.
func TestSearchMultipleResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := SearchResponse{
			BatchComplete: "",
		}
		response.Query.SearchInfo.TotalHits = 3
		response.Query.Search = []SearchResult{
			{Title: "Result 1", PageID: 1},
			{Title: "Result 2", PageID: 2},
			{Title: "Result 3", PageID: 3},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(server.URL, "test-agent", &http.Client{})

	result, err := client.Search("test", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(result.Query.Search) != 3 {
		t.Errorf("Expected 3 results, got %d", len(result.Query.Search))
	}

	if result.Query.SearchInfo.TotalHits != 3 {
		t.Errorf("Expected TotalHits 3, got %d", result.Query.SearchInfo.TotalHits)
	}
}
