//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebFetch(t *testing.T) {
	// Mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ensure explicit content-type for HTML, otherwise defaults might kick in differently or not match strict "text/html" logic if strict match was implemented (though I implemented strict check for text/html in code)
		// Actually, my code logic: `if mediaType == "text/html"` so I should set it.
		if r.URL.Path == "/page1" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><h1>Hello</h1><p>World</p></body></html>`)
		} else if r.URL.Path == "/page2" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><div>Foo Bar</div></body></html>`)
		} else {
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	tool := NewTool()

	// We call the tool via the interface
	args := fmt.Sprintf(`{"urls": ["%s/page1", "%s/page2"]}`, ts.URL, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok, "Response should be of type fetchResponse")

	assert.Len(t, resp.Results, 2)

	// Order isn't guaranteed due to concurrency, so we check existence.
	foundPage1 := false
	foundPage2 := false

	for _, r := range resp.Results {
		if r.RetrievedURL == ts.URL+"/page1" {
			assert.Contains(t, r.Content, "# Hello")
			assert.Contains(t, r.Content, "World")
			assert.Equal(t, http.StatusOK, r.StatusCode)
			foundPage1 = true
		}
		if r.RetrievedURL == ts.URL+"/page2" {
			assert.Contains(t, r.Content, "Foo Bar")
			assert.Equal(t, http.StatusOK, r.StatusCode)
			foundPage2 = true
		}
	}

	assert.True(t, foundPage1, "Should have found page1")
	assert.True(t, foundPage2, "Should have found page2")

	// Test 404 case
	args404 := fmt.Sprintf(`{"urls": ["%s/nonexistent"]}`, ts.URL)

	res404, err404 := tool.Call(context.Background(), []byte(args404))
	require.NoError(t, err404)

	resp404, ok404 := res404.(fetchResponse)
	require.True(t, ok404, "Response should be of type fetchResponse")
	assert.Len(t, resp404.Results, 1)
	assert.Equal(t, ts.URL+"/nonexistent", resp404.Results[0].RetrievedURL)
	assert.Equal(t, http.StatusNotFound, resp404.Results[0].StatusCode)
	assert.Contains(t, resp404.Results[0].Error, "HTTP status 404")
}

func TestWebFetch_NoURLs(t *testing.T) {
	tool := NewTool()
	res, err := tool.Call(context.Background(), []byte(`{"urls": []}`))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Empty(t, resp.Results)
	assert.Equal(t, "No URLs provided", resp.Summary)
}

func TestWebFetch_PlainText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8") // With params to test cleaning
		fmt.Fprint(w, "This is plain text content.")
	}))
	defer ts.Close()

	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok, "Response should be of type fetchResponse")
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, ts.URL, resp.Results[0].RetrievedURL)
	assert.Equal(t, http.StatusOK, resp.Results[0].StatusCode)
	assert.Equal(t, "This is plain text content.", resp.Results[0].Content)
}

func TestWebFetch_JSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"key": "value", "number": 123}`)
	}))
	defer ts.Close()

	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok, "Response should be of type fetchResponse")
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, ts.URL, resp.Results[0].RetrievedURL)
	assert.Equal(t, http.StatusOK, resp.Results[0].StatusCode)
	assert.Equal(t, `{"key": "value", "number": 123}`, resp.Results[0].Content)
}

func TestWebFetch_UnsupportedType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, `binary data`)
	}))
	defer ts.Close()

	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok, "Response should be of type fetchResponse")
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, ts.URL, resp.Results[0].RetrievedURL)
	assert.Equal(t, http.StatusOK, resp.Results[0].StatusCode) // HTTP status is 200 even if content type is unsupported
	assert.Empty(t, resp.Results[0].Content)
	assert.Contains(t, resp.Results[0].Error, "unsupported content type: application/octet-stream")
}

func TestWebFetch_PerUrlLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "1234567890")
	}))
	defer ts.Close()

	// Limit to 5 bytes
	tool := NewTool(WithMaxContentLength(5))
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "12345", resp.Results[0].Content)
}

func TestWebFetch_TotalLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "12345")
	}))
	defer ts.Close()

	// Total limit 7. Fetch two URLs (5 bytes each).
	// 1st: 5 bytes. Total 5. (OK)
	// 2nd: 5 bytes. Total 10 > 7. Truncate 2nd to 2 bytes (7-5).
	// Note: Order depends on concurrency, but results array is ordered by input.
	// The implementation applies total limit strictly on result array order.

	tool := NewTool(WithMaxTotalContentLength(7))
	args := fmt.Sprintf(`{"urls": ["%s/1", "%s/2"]}`, ts.URL, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok)
	assert.Len(t, resp.Results, 2)

	// The implementation iterates results in order.
	// Result 0: "12345" (len 5)
	// Result 1: "12" (len 2) -> Total 7

	assert.Equal(t, "12345", resp.Results[0].Content)
	assert.Equal(t, "12", resp.Results[1].Content)
}

func TestWebFetch_TruncateUTF8(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// "你好" is 6 bytes (3 per rune).
		fmt.Fprint(w, "你好")
	}))
	defer ts.Close()

	// Limit to 4 bytes.
	// "你" is 3 bytes. "好" is 3 bytes.
	// 4 bytes is not enough for "你好".
	// truncateString iterates runes.
	// Rune 1 '你': len 3. Current 3 <= 4. Keep.
	// Rune 2 '好': len 3. Current 3+3=6 > 4. Stop.
	// Result should be "你" (3 bytes).

	tool := NewTool(WithMaxContentLength(4))
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok)
	assert.Equal(t, "你", resp.Results[0].Content)
}

func TestConvertHTMLToMarkdown(t *testing.T) {
	htmlContent := `
		<html>
		<head>
			<title>Test Page</title>
			<style>body { color: red; }</style>
		</head>
		<body>
			<h1>Header</h1>
			<h2>Subheader</h2>
			<script>console.log("ignore");</script>
			<p>Paragraph text.</p>
			<ul>
				<li>Item 1</li>
				<li>Item 2</li>
			</ul>
			<p>Check <a href="http://example.com">this link</a>.</p>
			<p><b>Bold</b> and <i>Italic</i></p>
		</body>
		</html>
	`
	// Mock reader
	result, err := convertHTMLToMarkdown(strings.NewReader(htmlContent))
	require.NoError(t, err)

	// Debug output if test fails
	t.Logf("Converted Markdown:\n%s", result)

	// Check expected markdown content
	expectedParts := []string{
		"# Header",
		"## Subheader",
		"Paragraph text.",
		"- Item 1",
		"- Item 2",
		"Check [this link](http://example.com).",
		"**Bold**",
		"*Italic*",
	}

	for _, part := range expectedParts {
		assert.Contains(t, result, part)
	}

	assert.NotContains(t, result, "console.log")
	assert.NotContains(t, result, "color: red")
}
