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

func TestClient_Search_Success(t *testing.T) {
	// Mock DuckDuckGo API response
	mockResponse := `{
		"Abstract": "Beijing is the capital of China",
		"AbstractText": "Beijing is the capital of China and one of the most populous cities in the world.",
		"AbstractSource": "Wikipedia",
		"AbstractURL": "https://en.wikipedia.org/wiki/Beijing",
		"Image": "",
		"ImageHeight": "",
		"ImageWidth": "",
		"Heading": "Beijing",
		"Answer": "",
		"AnswerType": "",
		"Definition": "",
		"DefinitionSource": "",
		"DefinitionURL": "",
		"Type": "A",
		"Redirect": "",
		"RelatedTopics": [
			{
				"Result": "<a href=\"https://duckduckgo.com/Beijing_Capital_International_Airport\">Beijing Capital International Airport</a> - Beijing Capital International Airport is the main international airport serving Beijing.",
				"Icon": {
					"URL": "",
					"Height": "",
					"Width": ""
				},
				"Text": "Beijing Capital International Airport - Beijing Capital International Airport is the main international airport serving Beijing.",
				"FirstURL": "https://duckduckgo.com/Beijing_Capital_International_Airport"
			},
			{
				"Result": "<a href=\"https://duckduckgo.com/Weather_in_Beijing\">Weather in Beijing</a> - Current weather conditions in Beijing, China.",
				"Icon": {
					"URL": "",
					"Height": "",
					"Width": ""
				},
				"Text": "Weather in Beijing - Current weather conditions in Beijing, China.",
				"FirstURL": "https://duckduckgo.com/Weather_in_Beijing"
			}
		],
		"Results": []
	}`

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	// Create client with test server URL
	client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})

	// Test search
	response, err := client.Search("Beijing weather")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Validate response
	if response.AbstractText == "" {
		t.Error("Expected AbstractText to be set")
	}
	if len(response.RelatedTopics) != 2 {
		t.Errorf("Expected 2 related topics, got %d", len(response.RelatedTopics))
	}
	if response.RelatedTopics[0].Text == "" {
		t.Error("Expected first related topic to have text")
	}
	if response.RelatedTopics[0].FirstURL == "" {
		t.Error("Expected first related topic to have URL")
	}
}

func TestClient_Search_EmptyQuery(t *testing.T) {
	client := New("https://api.duckduckgo.com", "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	_, err := client.Search("")
	if err == nil {
		t.Error("Expected error for empty query")
	}
}

func TestClient_Search_InstantAnswer(t *testing.T) {
	// Mock response with instant answer
	mockResponse := `{
		"Abstract": "",
		"AbstractText": "",
		"Answer": "25°C, Partly cloudy",
		"AnswerType": "weather",
		"Definition": "",
		"Heading": "Weather in Beijing",
		"Image": "",
		"ImageHeight": "0",
		"ImageWidth": "0",
		"RelatedTopics": [],
		"Results": [],
		"Type": "A"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	response, err := client.Search("weather Beijing")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if response.Answer != "25°C, Partly cloudy" {
		t.Errorf("Expected answer '25°C, Partly cloudy', got '%s'", response.Answer)
	}
	if response.AnswerType != "weather" {
		t.Errorf("Expected answer type 'weather', got '%s'", response.AnswerType)
	}
}

func TestClient_Search_Definition(t *testing.T) {
	// Mock response with definition
	mockResponse := `{
		"Abstract": "",
		"AbstractText": "",
		"Answer": "",
		"AnswerType": "",
		"Definition": "Large Language Model (LLM) is a type of artificial intelligence model.",
		"DefinitionSource": "Wikipedia",
		"DefinitionURL": "https://en.wikipedia.org/wiki/Large_language_model",
		"Heading": "Large Language Model",
		"Image": "",
		"ImageHeight": "",
		"ImageWidth": "",
		"RelatedTopics": [],
		"Results": [],
		"Type": "D"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	response, err := client.Search("LLM definition")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if response.Definition == "" {
		t.Error("Expected definition to be set")
	}
	if response.DefinitionSource != "Wikipedia" {
		t.Errorf("Expected definition source 'Wikipedia', got '%s'", response.DefinitionSource)
	}
}

func TestResponseJSONUnmarshaling(t *testing.T) {
	// Test that our Response struct can handle various field types
	testCases := []struct {
		name     string
		jsonData string
		wantErr  bool
	}{
		{
			name: "string image dimensions",
			jsonData: `{
				"ImageWidth": "100",
				"ImageHeight": "200",
				"Answer": "test"
			}`,
			wantErr: false,
		},
		{
			name: "empty image dimensions",
			jsonData: `{
				"ImageWidth": "",
				"ImageHeight": "",
				"Answer": "test"
			}`,
			wantErr: false,
		},
		{
			name: "missing image dimensions",
			jsonData: `{
				"Answer": "test"
			}`,
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var response Response
			err := json.Unmarshal([]byte(tc.jsonData), &response)
			if (err != nil) != tc.wantErr {
				t.Errorf("JSON unmarshal error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestFlexibleString_UnmarshalJSON(t *testing.T) {
	testCases := []struct {
		name     string
		jsonData string
		expected string
		wantErr  bool
	}{
		{
			name:     "string value",
			jsonData: `"hello"`,
			expected: "hello",
			wantErr:  false,
		},
		{
			name:     "integer value",
			jsonData: `123`,
			expected: "123",
			wantErr:  false,
		},
		{
			name:     "float value",
			jsonData: `123.45`,
			expected: "123.45",
			wantErr:  false,
		},
		{
			name:     "empty string",
			jsonData: `""`,
			expected: "",
			wantErr:  false,
		},
		{
			name:     "zero",
			jsonData: `0`,
			expected: "0",
			wantErr:  false,
		},
		{
			name:     "boolean true",
			jsonData: `true`,
			expected: "true",
			wantErr:  false,
		},
		{
			name:     "boolean false",
			jsonData: `false`,
			expected: "false",
			wantErr:  false,
		},
		{
			name:     "null value",
			jsonData: `null`,
			expected: "",
			wantErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var fs FlexibleString
			err := json.Unmarshal([]byte(tc.jsonData), &fs)
			if (err != nil) != tc.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !tc.wantErr && fs.String() != tc.expected {
				t.Errorf("UnmarshalJSON() got = %v, want %v", fs.String(), tc.expected)
			}
		})
	}
}

func TestFlexibleString_String(t *testing.T) {
	testCases := []struct {
		name     string
		fs       FlexibleString
		expected string
	}{
		{
			name:     "normal string",
			fs:       FlexibleString("test"),
			expected: "test",
		},
		{
			name:     "empty string",
			fs:       FlexibleString(""),
			expected: "",
		},
		{
			name:     "numeric string",
			fs:       FlexibleString("123"),
			expected: "123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.fs.String()
			if result != tc.expected {
				t.Errorf("String() = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestClient_Search_InvalidRequest(t *testing.T) {
	// Test with invalid URL (this will cause NewRequest to fail)
	client := New("ht!tp://invalid url with spaces", "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	_, err := client.Search("test query")
	if err == nil {
		t.Error("Expected error for invalid base URL")
	}
}

func TestClient_Search_NetworkError(t *testing.T) {
	// Test with unreachable server
	client := New("http://localhost:1", "test-agent/1.0", &http.Client{Timeout: 1 * time.Millisecond})
	_, err := client.Search("test query")
	if err == nil {
		t.Error("Expected error for network failure")
	}
}

func TestClient_Search_InvalidJSON(t *testing.T) {
	// Test with invalid JSON response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	_, err := client.Search("test query")
	if err == nil {
		t.Error("Expected error for invalid JSON response")
	}
	if !contains(err.Error(), "failed to parse response") {
		t.Errorf("Expected parse error, got: %v", err)
	}
}

func TestClient_Search_NonOKStatus(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"Bad Request", http.StatusBadRequest},
		{"Not Found", http.StatusNotFound},
		{"Internal Server Error", http.StatusInternalServerError},
		{"Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
			_, err := client.Search("test query")
			if err == nil {
				t.Errorf("Expected error for status code %d", tc.statusCode)
			}
		})
	}
}

func TestClient_Search_Headers(t *testing.T) {
	customUserAgent := "custom-test-agent/2.0"
	var receivedUserAgent string
	var receivedAccept string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUserAgent = r.Header.Get("User-Agent")
		receivedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Answer": "test"}`))
	}))
	defer server.Close()

	client := New(server.URL, customUserAgent, &http.Client{Timeout: 30 * time.Second})
	_, err := client.Search("test query")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if receivedUserAgent != customUserAgent {
		t.Errorf("Expected User-Agent %q, got %q", customUserAgent, receivedUserAgent)
	}
	if receivedAccept != "application/json" {
		t.Errorf("Expected Accept header 'application/json', got %q", receivedAccept)
	}
}

func TestClient_Search_QueryEscaping(t *testing.T) {
	var receivedQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Answer": "test"}`))
	}))
	defer server.Close()

	client := New(server.URL, "test-agent/1.0", &http.Client{Timeout: 30 * time.Second})
	testQuery := "test query with spaces & special chars"
	_, err := client.Search(testQuery)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if receivedQuery != testQuery {
		t.Errorf("Expected query %q, got %q", testQuery, receivedQuery)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
