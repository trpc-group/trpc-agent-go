//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package arxiv

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestWithSortOrder with sort order
func TestWithSortOrder(t *testing.T) {
	type testCase struct {
		name      string
		sortOrder SortOrder
		wantOrder SortOrder
	}

	testCases := []testCase{
		{
			name:      "set ascending order",
			sortOrder: SortOrderAscending,
			wantOrder: SortOrderAscending,
		},
		{
			name:      "set descending order",
			sortOrder: SortOrderDescending,
			wantOrder: SortOrderDescending,
		},
		{
			name:      "empty sort order",
			sortOrder: "",
			wantOrder: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			search := &Search{}

			opt := WithSortOrder(tc.sortOrder)

			opt(search)

			assert.Equal(t, tc.wantOrder, search.SortOrder, "SortOrder mismatch")
		})
	}
}

// TestResult_GetShortID with short ID
func TestResult_GetShortID(t *testing.T) {
	tests := []struct {
		name string
		r    *Result
		want string
	}{
		{
			name: "standard arxiv url",
			r:    &Result{EntryID: "http://arxiv.org/abs/1706.03762v1"},
			want: "1706.03762v1",
		},
		{
			name: "no arxiv pattern in url",
			r:    &Result{EntryID: "https://example.com/papers/12345"},
			want: "https://example.com/papers/12345",
		},
		{
			name: "empty entry id",
			r:    &Result{EntryID: ""},
			want: "",
		},
		{
			name: "arxiv pattern in middle",
			r:    &Result{EntryID: "https://sub.arxiv.org/abs/9876.54321v3"},
			want: "9876.54321v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.GetShortID(); got != tt.want {
				t.Errorf("Result.GetShortID() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestArxivError_Error with different error messages
func TestArxivError_Error(t *testing.T) {
	type fields struct {
		URL     string
		Retry   int
		Message string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "normal error message",
			fields: fields{
				Message: "paper not found",
				URL:     "http://arxiv.org/abs/1234.5678",
				Retry:   3,
			},
			want: "paper not found (http://arxiv.org/abs/1234.5678)",
		},
		{
			name: "empty message",
			fields: fields{
				Message: "",
				URL:     "http://arxiv.org/search",
				Retry:   0,
			},
			want: " (http://arxiv.org/search)",
		},
		{
			name: "empty URL",
			fields: fields{
				Message: "connection timeout",
				URL:     "",
				Retry:   2,
			},
			want: "connection timeout ()",
		},
		{
			name: "special characters in message",
			fields: fields{
				Message: "invalid format: <html>",
				URL:     "http://arxiv.org",
				Retry:   1,
			},
			want: "invalid format: <html> (http://arxiv.org)",
		},
		{
			name: "whitespace trimming",
			fields: fields{
				Message: "  unexpected response  ",
				URL:     "  http://arxiv.org/  ",
				Retry:   0,
			},
			want: "  unexpected response   (  http://arxiv.org/  )",
		},
		{
			name: "both empty fields",
			fields: fields{
				Message: "",
				URL:     "",
				Retry:   5,
			},
			want: " ()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ArxivError{
				URL:     tt.fields.URL,
				Retry:   tt.fields.Retry,
				Message: tt.fields.Message,
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("ArxivError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResult_GetSourceURL get source url
func TestResult_GetSourceURL(t *testing.T) {
	tests := []struct {
		name string
		r    *Result
		want string
	}{
		{
			name: "empty PdfURL",
			r:    &Result{PdfURL: ""},
			want: "",
		},
		{
			name: "standard PdfURL replacement",
			r:    &Result{PdfURL: "http://arxiv.org/pdf/1706.03762v1"},
			want: "http://arxiv.org/src/1706.03762v1",
		},
		{
			name: "no pdf path in URL",
			r:    &Result{PdfURL: "http://example.com/papers/1234.pdf"},
			want: "http://example.com/papers/1234.pdf",
		},
		{
			name: "multiple pdf segments in URL",
			r:    &Result{PdfURL: "http://arxiv.org/pdf/old/pdf/1234v1"},
			want: "http://arxiv.org/src/old/pdf/1234v1",
		},
		{
			name: "case sensitive replacement",
			r:    &Result{PdfURL: "http://arxiv.org/PDF/1234.5678v2"},
			want: "http://arxiv.org/PDF/1234.5678v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r.GetSourceURL()
			assert.Equal(t, tt.want, got, "GetSourceURL() mismatch")
		})
	}
}

// TestWithSortBy test with sort by
func TestWithSortBy(t *testing.T) {
	tests := []struct {
		name   string
		sortBy SortCriterion
		want   SortCriterion
	}{
		{
			name:   "empty sort criterion",
			sortBy: "",
			want:   "",
		},
		{
			name:   "valid relevance sort",
			sortBy: "relevance",
			want:   "relevance",
		},
		{
			name:   "valid lastUpdated sort",
			sortBy: "lastUpdatedDate",
			want:   "lastUpdatedDate",
		},
		{
			name:   "special characters sort",
			sortBy: "custom-sort-v1",
			want:   "custom-sort-v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			search := &Search{}

			option := WithSortBy(tt.sortBy)

			option(search)

			assert.Equal(t, tt.want, search.SortBy, "SortBy field mismatch")
		})
	}
}

// TestWithIDList test with id list
func TestWithIDList(t *testing.T) {
	type args struct {
		ids []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "normal case with multiple IDs",
			args: args{ids: []string{"123", "456"}},
			want: []string{"123", "456"},
		},
		{
			name: "empty ID list",
			args: args{ids: []string{}},
			want: []string{},
		},
		{
			name: "single ID",
			args: args{ids: []string{"789"}},
			want: []string{"789"},
		},
		{
			name: "IDs with empty string",
			args: args{ids: []string{"", "abc"}},
			want: []string{"", "abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opt := WithIDList(tt.args.ids...)

			s := &Search{}
			opt(s)

			assert.Equal(t, tt.want, s.IDList, "IDList should match expected value")
		})
	}
}

// TestResult_GetDefaultFilename test get default filename
func TestResult_GetDefaultFilename(t *testing.T) {
	type args struct {
		extension string
	}
	tests := []struct {
		name string
		r    *Result
		args args
		want string
	}{
		{
			name: "normal case with extension",
			r: &Result{
				EntryID: "http://arxiv.org/abs/1706.03762v1",
				Title:   "Test Paper",
			},
			args: args{extension: "txt"},
			want: "1706.03762v1.Test_Paper.txt",
		},
		{
			name: "default extension when empty",
			r: &Result{
				EntryID: "http://arxiv.org/abs/1234.5678v1",
				Title:   "Default Extension",
			},
			args: args{extension: ""},
			want: "1234.5678v1.Default_Extension.pdf",
		},
		{
			name: "empty title handling",
			r: &Result{
				EntryID: "http://arxiv.org/abs/9876.5432v1",
				Title:   "",
			},
			args: args{extension: "pdf"},
			want: "9876.5432v1.UNTITLED.pdf",
		},
		{
			name: "special characters in title",
			r: &Result{
				EntryID: "http://arxiv.org/abs/1234.5678v1",
				Title:   "Hello!@World#$",
			},
			args: args{extension: "txt"},
			want: "1234.5678v1.Hello__World__.txt",
		},
		{
			name: "shortID with slash replacement",
			r: &Result{
				EntryID: "http://arxiv.org/abs/123/456v1",
				Title:   "Multi_Segment_ID",
			},
			args: args{extension: "md"},
			want: "123_456v1.Multi_Segment_ID.md",
		},
		{
			name: "mixed case with all special chars",
			r: &Result{
				EntryID: "http://arxiv.org/abs/2023.0000v1",
				Title:   "Thesis: AI & ML (2023)",
			},
			args: args{extension: "docx"},
			want: "2023.0000v1.Thesis__AI___ML__2023_.docx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.GetDefaultFilename(tt.args.extension); got != tt.want {
				t.Errorf("Result.GetDefaultFilename() = %v, want %v", got, tt.want)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

// TestNewSearch tests the NewSearch function.
func TestNewSearch(t *testing.T) {
	type args struct {
		query   string
		options []SearchOption
	}
	tests := []struct {
		name string
		args args
		want Search
	}{
		{
			name: "default options",
			args: args{
				query:   "test query",
				options: nil,
			},
			want: Search{
				Query:     "test query",
				IDList:    []string{},
				SortBy:    SortCriterionRelevance,
				SortOrder: SortOrderDescending,
			},
		},
		{
			name: "with IDList and MaxResults",
			args: args{
				query: "idlist query",
				options: []SearchOption{
					WithIDList("1234.5678", "8765.4321"),
					WithMaxResults(15),
				},
			},
			want: Search{
				Query:      "idlist query",
				IDList:     []string{"1234.5678", "8765.4321"},
				MaxResults: intPtr(15),
				SortBy:     SortCriterionRelevance,
				SortOrder:  SortOrderDescending,
			},
		},
		{
			name: "with sort options",
			args: args{
				query: "sort query",
				options: []SearchOption{
					WithSortBy(SortCriterionLastUpdatedDate),
					WithSortOrder(SortOrderAscending),
				},
			},
			want: Search{
				Query:     "sort query",
				IDList:    []string{},
				SortBy:    SortCriterionLastUpdatedDate,
				SortOrder: SortOrderAscending,
			},
		},
		{
			name: "combined options",
			args: args{
				query: "combined query",
				options: []SearchOption{
					WithIDList("9999.8888"),
					WithMaxResults(5),
					WithSortBy(SortCriterionSubmittedDate),
					WithSortOrder(SortOrderDescending),
				},
			},
			want: Search{
				Query:      "combined query",
				IDList:     []string{"9999.8888"},
				MaxResults: intPtr(5),
				SortBy:     SortCriterionSubmittedDate,
				SortOrder:  SortOrderDescending,
			},
		},
		{
			name: "empty query",
			args: args{
				query:   "",
				options: []SearchOption{WithIDList("0000.1111")},
			},
			want: Search{
				Query:     "",
				IDList:    []string{"0000.1111"},
				SortBy:    SortCriterionRelevance,
				SortOrder: SortOrderDescending,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewSearch(tt.args.query, tt.args.options...)

			if tt.want.MaxResults != nil {
				if got.MaxResults == nil || *got.MaxResults != *tt.want.MaxResults {
					t.Errorf("MaxResults mismatch: got %v, want %v", got.MaxResults, tt.want.MaxResults)
				}
			} else {
				if got.MaxResults != nil {
					t.Errorf("MaxResults should be nil, got %v", got.MaxResults)
				}
			}

			gotCopy := got
			wantCopy := tt.want
			gotCopy.MaxResults = nil
			wantCopy.MaxResults = nil

			if !reflect.DeepEqual(gotCopy, wantCopy) {
				t.Errorf("NewSearch() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDefaultClient default client
func TestDefaultClient(t *testing.T) {
	t.Run("should return client with default configuration", func(t *testing.T) {

		client := DefaultClient()

		expectedConfig := DefaultConfig()
		assert.Equal(t, expectedConfig, client.config, "Client config mismatch")

		assert.Equal(t, "https://export.arxiv.org/api/query", client.BaseURL, "BaseURL mismatch")

		assert.NotNil(t, client.httpClient, "HTTP client should be initialized")
		assert.Equal(t, 30*time.Second, client.httpClient.Timeout, "HTTP client timeout mismatch")

		assert.Nil(t, client.lastRequest, "Last request should be nil initially")
	})
}
