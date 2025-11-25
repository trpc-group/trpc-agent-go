//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package httpfetch

import (
	"context"
	"errors"
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
			assert.Equal(t, "text/html", r.ContentType)
			foundPage1 = true
		}
		if r.RetrievedURL == ts.URL+"/page2" {
			assert.Contains(t, r.Content, "Foo Bar")
			assert.Equal(t, http.StatusOK, r.StatusCode)
			assert.Equal(t, "text/html", r.ContentType)
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
	assert.Equal(t, "", resp404.Results[0].ContentType) // 404 response might not have a content type
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
	assert.Equal(t, "text/plain", resp.Results[0].ContentType)
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
	assert.Equal(t, "application/json", resp.Results[0].ContentType)
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
	assert.Equal(t, http.StatusOK, resp.Results[0].StatusCode)
	assert.Equal(t, "application/octet-stream", resp.Results[0].ContentType)
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
	assert.Equal(t, "text/plain", resp.Results[0].ContentType)
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
	assert.Equal(t, "text/plain", resp.Results[0].ContentType)
	assert.Equal(t, "12", resp.Results[1].Content)
	assert.Equal(t, "text/plain", resp.Results[1].ContentType)
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
	assert.Equal(t, "text/plain", resp.Results[0].ContentType)
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
	t.Logf("Converted Markdown:%s", result)

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

func TestWebFetch_WithHTTPClient(t *testing.T) {
	client := &http.Client{Timeout: defaultTimeout}
	tool := NewTool(WithHTTPClient(client))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)
	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "OK", resp.Results[0].Content)
}

// failReader is an io.Reader that returns an error on Read.
type failReader struct{}

func (f *failReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("simulated read error")
}

func TestReadBodyAsString_Error(t *testing.T) {
	_, err := readBodyAsString(&failReader{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read response body")
}

func TestConvertHTMLToMarkdown_ReadError(t *testing.T) {
	_, err := convertHTMLToMarkdown(&failReader{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "simulated read error")
}

func TestTruncateString_EdgeCases(t *testing.T) {
	assert.Equal(t, "", truncateString("hello", 0))
	assert.Equal(t, "", truncateString("hello", -1))
	assert.Equal(t, "h", truncateString("hello", 1))
	// Test UTF-8 splitting
	// "你好" -> bytes: [e4 bd a0 e5 a5 bd]
	// limit 4. "你" is 3 bytes. "好" is 3 bytes. 3 <= 4. Result "你"
	assert.Equal(t, "你", truncateString("你好", 4))
	// limit 2. "你" is 3 bytes. 0+3 > 2. Result ""
	assert.Equal(t, "", truncateString("你好", 2))
}

func TestWebFetch_InvalidURL(t *testing.T) {
	// Test http.NewRequestWithContext error
	// URL with control character should fail parsing/request creation
	tool := NewTool()
	// A URL with a space is invalid for NewRequest
	args := `{"urls": ["http://example.com/ foo"]}`
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)
	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.NotEmpty(t, resp.Results[0].Error)
}

func TestWebFetch_ClientDoError(t *testing.T) {
	// Simulate a client error (e.g., connection refused)
	// We can use a closed server URL or invalid port
	tool := NewTool()
	args := `{"urls": ["http://127.0.0.1:0"]}` // Invalid port 0 usually fails immediately or connection refused
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)
	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.NotEmpty(t, resp.Results[0].Error)
}

func TestFetch_TotalLimitExact(t *testing.T) {
	// Test exact limit match
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "abc")
	}))
	defer ts.Close()

	tool := NewTool(WithMaxTotalContentLength(3))
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)
	resp := res.(fetchResponse)
	assert.Equal(t, "abc", resp.Results[0].Content)
}

func TestFetch_TotalLimitExceeded(t *testing.T) {
	// Test limit exceeded where next item is skipped entirely?
	// Code:
	// if currentTotal >= t.maxTotalContentLength { results[i].Content = ""; Error = "..." }

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "abc")
	}))
	defer ts.Close()

	// Limit 2. Fetch "abc". Should be truncated to "ab". Next should be skipped.
	tool := NewTool(WithMaxTotalContentLength(2))
	args := fmt.Sprintf(`{"urls": ["%s/1", "%s/2"]}`, ts.URL, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)
	resp := res.(fetchResponse)

	assert.Equal(t, "ab", resp.Results[0].Content)

	// The second one should be marked as truncated/skipped
	// In loop: currentTotal becomes 2.
	// Next iter: currentTotal (2) >= limit (2).
	assert.Empty(t, resp.Results[1].Content)
	assert.Contains(t, resp.Results[1].Error, "truncated due to total length limit")
}

// ============================================================================
// URL Filtering Tests
// ============================================================================

func TestWebFetch_WithAllowedDomains(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Allowed content")
	}))
	defer ts.Close()

	// Only allow localhost
	tool := NewTool(WithAllowedDomains([]string{"127.0.0.1"}))

	// This should be allowed
	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "Allowed content", resp.Results[0].Content)
	assert.Empty(t, resp.Results[0].Error)
}

func TestWebFetch_WithAllowedDomains_Blocked(t *testing.T) {
	// Only allow example.com
	tool := NewTool(WithAllowedDomains([]string{"example.com"}))

	// Try to fetch from google.com (should be blocked)
	args := `{"urls": ["https://google.com"]}`
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.NotEmpty(t, resp.Results[0].Error)
	assert.Contains(t, resp.Results[0].Error, "does not match any allowed pattern")
}

func TestWebFetch_WithBlockedDomains(t *testing.T) {
	// Block malicious.com
	tool := NewTool(WithBlockedDomains([]string{"malicious.com"}))

	// Try to fetch from blocked domain
	args := `{"urls": ["https://malicious.com/page"]}`
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.NotEmpty(t, resp.Results[0].Error)
	assert.Contains(t, resp.Results[0].Error, "matches blocked pattern")
}

func TestWebFetch_WithBlockedDomains_Allowed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Not blocked")
	}))
	defer ts.Close()

	// Block example.com but allow localhost
	tool := NewTool(WithBlockedDomains([]string{"example.com"}))

	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "Not blocked", resp.Results[0].Content)
	assert.Empty(t, resp.Results[0].Error)
}

func TestWebFetch_CombinedFilters(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Success")
	}))
	defer ts.Close()

	// Allow 127.0.0.1 and block nothing specific
	tool := NewTool(
		WithAllowedDomains([]string{"127.0.0.1"}),
		WithBlockedDomains([]string{"evil.com"}),
	)

	args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)
	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "Success", resp.Results[0].Content)
}

// ============================================================================
// Additional Edge Case Tests
// ============================================================================

func TestWebFetch_DuplicateURLs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "content")
	}))
	defer ts.Close()

	// Submit same URL twice - should be deduplicated
	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["%s", "%s", "%s"]}`, ts.URL, ts.URL, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	// Should only fetch once due to deduplication
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "content", resp.Results[0].Content)
	assert.Contains(t, resp.Summary, "Fetched 1 URLs")
}

func TestWebFetch_EmptyURLs(t *testing.T) {
	// Test with empty strings in URL list
	tool := NewTool()
	args := `{"urls": ["", "  ", ""]}`

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Empty(t, resp.Results)
	// After trimming and deduplication, empty URLs result in "Fetched 0 URLs"
	assert.Equal(t, "Fetched 0 URLs", resp.Summary)
}

func TestWebFetch_MixedEmptyAndValid(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "valid")
	}))
	defer ts.Close()

	// Mix of empty and valid URLs
	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["", "%s", "  ", "%s", ""]}`, ts.URL, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	// Should only fetch the one unique valid URL
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "valid", resp.Results[0].Content)
}

func TestWebFetch_MaxURLsLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer ts.Close()

	// Generate 25 unique URLs (more than maxURLs=20)
	urls := make([]string, 25)
	for i := 0; i < 25; i++ {
		urls[i] = fmt.Sprintf("%s/page%d", ts.URL, i)
	}

	tool := NewTool()
	args := fmt.Sprintf(`{"urls": ["%s"]}`, strings.Join(urls, `","`))

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	// Should only fetch first 20 due to limit
	assert.Len(t, resp.Results, 20)
	assert.Contains(t, resp.Summary, "Fetched 20 URLs")
}

func TestWebFetch_SupportedTextTypes(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		content     string
		supported   bool
	}{
		{"application/json", "application/json", `{"test": "json"}`, true},
		{"text/plain", "text/plain", "plain text", true},
		{"text/xml", "text/xml", "<xml>data</xml>", true},
		{"text/css", "text/css", "body { color: red; }", true},
		{"text/javascript", "text/javascript", "console.log('test');", true},
		{"text/csv", "text/csv", "a,b,c\n1,2,3", true},
		{"text/rtf", "text/rtf", "{\\rtf1 test}", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				fmt.Fprint(w, tt.content)
			}))
			defer ts.Close()

			tool := NewTool()
			args := fmt.Sprintf(`{"urls": ["%s"]}`, ts.URL)

			res, err := tool.Call(context.Background(), []byte(args))
			require.NoError(t, err)

			resp := res.(fetchResponse)
			assert.Len(t, resp.Results, 1)

			if tt.supported {
				assert.Equal(t, tt.content, resp.Results[0].Content)
				assert.Empty(t, resp.Results[0].Error)
			} else {
				assert.NotEmpty(t, resp.Results[0].Error)
			}
		})
	}
}

func TestIsSupportedTextType(t *testing.T) {
	// Direct function test
	assert.True(t, isSupportedTextType("application/json"))
	assert.True(t, isSupportedTextType("text/plain"))
	assert.True(t, isSupportedTextType("text/xml"))
	assert.True(t, isSupportedTextType("text/css"))
	assert.True(t, isSupportedTextType("text/javascript"))
	assert.True(t, isSupportedTextType("text/csv"))
	assert.True(t, isSupportedTextType("text/rtf"))

	assert.False(t, isSupportedTextType("application/pdf"))
	assert.False(t, isSupportedTextType("image/png"))
	assert.False(t, isSupportedTextType("video/mp4"))
	assert.False(t, isSupportedTextType(""))
}

func TestWebFetch_TotalLimitWithError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "error") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "12345")
	}))
	defer ts.Close()

	// Total limit 10. First URL errors, second succeeds
	tool := NewTool(WithMaxTotalContentLength(10))
	args := fmt.Sprintf(`{"urls": ["%s/error", "%s/ok"]}`, ts.URL, ts.URL)

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	resp := res.(fetchResponse)
	assert.Len(t, resp.Results, 2)

	// First should have error (not counted toward total)
	assert.NotEmpty(t, resp.Results[0].Error)

	// Second should succeed with full content
	assert.Equal(t, "12345", resp.Results[1].Content)
}

func TestTruncateString_ExactLength(t *testing.T) {
	// Test when string length equals limit
	assert.Equal(t, "hello", truncateString("hello", 5))
	assert.Equal(t, "hello", truncateString("hello", 10))
}
