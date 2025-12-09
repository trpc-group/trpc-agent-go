//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package urlfilter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebFetch_DomainFiltering(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	// ts.URL looks like http://127.0.0.1:xxxxx
	// We'll use 127.0.0.1 for filtering tests.

	t.Run("AllowedDomains", func(t *testing.T) {
		validators := []URLValidator{
			{
				Filter: NewAllowPatternsFilter([]string{"127.0.0.1"}),
				ErrMsg: "URL does not match any allowed pattern",
			},
		}
		err := CheckURL(validators, ts.URL)
		assert.NoError(t, err)
	})

	t.Run("AllowedDomains_Blocked", func(t *testing.T) {
		validators := []URLValidator{
			{
				Filter: NewAllowPatternsFilter([]string{"example.com"}),
				ErrMsg: "URL does not match any allowed pattern",
			},
		}
		err := CheckURL(validators, ts.URL)
		assert.Contains(t, err.Error(), "does not match any allowed pattern")
	})

	t.Run("BlockedDomains", func(t *testing.T) {
		validators := []URLValidator{
			{
				Filter: NewBlockPatternFilter("127.0.0.1"),
				ErrMsg: "URL matches blocked pattern: 127.0.0.1",
			},
		}
		err := CheckURL(validators, ts.URL)
		assert.Contains(t, err.Error(), "matches blocked pattern")
	})

	t.Run("AllowedDomains_Subpath", func(t *testing.T) {
		validators := []URLValidator{
			{
				Filter: NewAllowPatternsFilter([]string{"127.0.0.1/docs"}),
				ErrMsg: "URL does not match any allowed pattern",
			},
		}

		// Allowed path
		errOK := CheckURL(validators, ts.URL+"/docs/page1")
		assert.NoError(t, errOK, "Should allow /docs/page1")

		// Blocked path
		errBlock := CheckURL(validators, ts.URL+"/admin")
		assert.Contains(t, errBlock.Error(), "not match any allowed pattern", "Should block /admin")
	})

	t.Run("BlockedDomains_Subpath", func(t *testing.T) {
		validators := []URLValidator{
			{
				Filter: NewBlockPatternFilter("127.0.0.1/private"),
				ErrMsg: "URL matches blocked pattern: 127.0.0.1/private",
			},
		}

		// Allowed path (not blocked)
		errOK := CheckURL(validators, ts.URL+"/public")
		assert.NoError(t, errOK, "Should allow /public")

		// Blocked path
		errBlock := CheckURL(validators, ts.URL+"/private/secret")
		assert.Contains(t, errBlock.Error(), "matches blocked pattern", "Should block /private/secret")
	})

	t.Run("CustomURLFilter", func(t *testing.T) {
		// Filter that only allows paths containing "secure"
		filter := func(u string) bool {
			return strings.Contains(u, "secure")
		}
		validators := []URLValidator{
			{
				Filter: filter,
				ErrMsg: "URL rejected by custom filter",
			},
		}

		// Allowed path
		errOK := CheckURL(validators, ts.URL+"/secure/page")
		assert.NoError(t, errOK, "Should allow /secure/page")

		// Blocked path
		errBlock := CheckURL(validators, ts.URL+"/unsafe/page")
		assert.Contains(t, errBlock.Error(), "rejected by custom filter", "Should block /unsafe/page")
	})
}

func TestMatchHost(t *testing.T) {
	tests := []struct {
		host   string
		target string
		want   bool
	}{
		{"example.com", "example.com", true},
		{"www.example.com", "example.com", true},
		{"sub.www.example.com", "example.com", true},
		{"example.com", "google.com", false},
		{"notexample.com", "example.com", false}, // Suffix but not dot separator
		{"example.com.evil.com", "example.com", false},
	}

	for _, tt := range tests {
		got := matchHost(tt.host, tt.target)
		assert.Equal(t, tt.want, got, "matchHost(%q, %q)", tt.host, tt.target)
	}
}

func TestMatchPattern(t *testing.T) {
	// Helper to create URL
	parse := func(s string) *url.URL {
		u, _ := url.Parse(s)
		return u
	}

	tests := []struct {
		urlStr  string
		pattern string
		want    bool
	}{
		// Host only
		{"http://example.com", "example.com", true},
		{"http://www.example.com", "example.com", true},
		{"http://google.com", "example.com", false},

		// Host + Path
		{"http://example.com/docs", "example.com/docs", true},
		{"http://example.com/docs/api", "example.com/docs", true},
		{"http://example.com/other", "example.com/docs", false},
		{"http://example.com/docserver", "example.com/docs", false}, // boundary check
		{"http://example.com", "example.com/docs", false},           // path too short

		// Subdomains
		{"http://www.example.com/docs", "example.com/docs", true},

		// Trailing slash in pattern
		{"http://example.com/docs/", "example.com/docs/", true},
		{"http://example.com/docs", "example.com/docs/", false}, // url path missing slash
		{"http://example.com/docs/api", "example.com/docs/", true},

		// Trailing slash in URL
		{"http://example.com/docs/", "example.com/docs", true},
	}

	for _, tt := range tests {
		u := parse(tt.urlStr)
		got := matchPattern(u, tt.pattern)
		assert.Equal(t, tt.want, got, "matchPattern(%q, %q)", tt.urlStr, tt.pattern)
	}
}

// Tests moved from webfetch_coverage_test.go that relate to urlfilter logic
func TestNewBlockPatternFilter_InvalidURL(t *testing.T) {
	filter := NewBlockPatternFilter("example.com")
	// Pass an invalid URL string that url.Parse fails on.
	// Control character in URL path
	assert.False(t, filter("http://example.com/\x00"))
}

func TestNewAllowPatternsFilter_InvalidURL(t *testing.T) {
	filter := NewAllowPatternsFilter([]string{"example.com"})
	assert.False(t, filter("http://example.com/\x00"))
}
