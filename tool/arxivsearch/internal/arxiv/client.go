//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package arxiv provides a client for the arxiv search API.
package arxiv

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	baseURL        = "https://export.arxiv.org/api/query"
	defaultTimeout = 30 * time.Second
)

// Client arXiv API client
type Client struct {
	BaseURL     string
	config      ClientConfig
	httpClient  *http.Client
	lastRequest *time.Time
}

// NewClient create a new arXiv API client
func NewClient(config ClientConfig) *Client {
	if config.PageSize <= 0 {
		config.PageSize = 100
	}
	if config.DelaySeconds <= 0 {
		config.DelaySeconds = 3 * time.Second
	}
	if config.NumRetries <= 0 {
		config.NumRetries = 3
	}
	base := config.BaseURL
	if base == "" {
		base = baseURL
	}

	return &Client{
		BaseURL:    base,
		config:     config,
		httpClient: resolveHTTPClient(config),
	}
}

// HTTPClient returns the underlying *http.Client used for arXiv API requests.
// Intended for tests/diagnostics; callers must not mutate the returned client.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// resolveHTTPClient builds the final *http.Client from config, applying
// nil fallback and timeout override via shallow copy - the caller's
// original client is never mutated.
func resolveHTTPClient(cfg ClientConfig) *http.Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.Timeout != nil {
		cloned := *httpClient
		cloned.Timeout = *cfg.Timeout
		httpClient = &cloned
	}
	return httpClient
}

// Search arXiv for papers matching the given search criteria
func (c *Client) Search(search Search) ([]Result, error) {
	var results []Result
	var pageSize int
	if search.MaxResults != nil {
		pageSize = *search.MaxResults
	} else {
		pageSize = c.config.PageSize
	}

	queryURL, err := c.buildQueryURL(search, 0, pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to build query URL: %w", err)
	}

	feed, err := c.fetchPage(queryURL, true)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %w", err)
	}

	for _, entry := range feed.Entries {
		result, err := parseEntry(entry)
		if err != nil {
			continue
		}
		results = append(results, result)
	}

	if search.MaxResults != nil && len(results) < *search.MaxResults {
		totalResults, _ := strconv.Atoi(feed.TotalResults)
		for start := len(results); start < totalResults && start < *search.MaxResults; start += pageSize {
			pageURL, err := c.buildQueryURL(search, start, pageSize)
			if err != nil {
				break
			}

			pageFeed, err := c.fetchPage(pageURL, false)
			if err != nil {
				break
			}

			for _, entry := range pageFeed.Entries {
				if len(results) >= *search.MaxResults {
					break
				}
				result, err := parseEntry(entry)
				if err != nil {
					continue
				}
				results = append(results, result)
			}
		}
	}

	return results, nil
}

// buildQueryURL builds the query URL for the search
func (c *Client) buildQueryURL(search Search, start, maxResults int) (string, error) {
	params := url.Values{}
	query, err := buildSearchQuery(search)
	if err != nil {
		return "", err
	}
	if query != "" {
		params.Add("search_query", query)
	}
	if len(search.IDList) > 0 {
		params.Add("id_list", strings.Join(search.IDList, ","))
	}
	if len(search.SortOrder) > 0 {
		params.Add("sortOrder", string(search.SortOrder))
	}
	if len(search.SortBy) > 0 {
		params.Add("sortBy", string(search.SortBy))
	}
	if start >= 0 {
		params.Add("start", strconv.Itoa(start))
	}
	if maxResults > 0 {
		params.Add("max_results", strconv.Itoa(maxResults))
	}

	return c.BaseURL + "?" + params.Encode(), nil
}

func buildSearchQuery(search Search) (string, error) {
	query := strings.TrimSpace(search.Query)
	dateClause, err := submittedDateClause(
		search.SubmittedDateFrom,
		search.SubmittedDateTo,
	)
	if err != nil {
		return "", err
	}
	if dateClause == "" {
		return query, nil
	}
	if query == "" {
		return dateClause, nil
	}
	return query + " AND " + dateClause, nil
}

func submittedDateClause(from string, to string) (string, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" && to == "" {
		return "", nil
	}
	if from == "" {
		from = "199001010000"
	}
	if to == "" {
		to = "999912312359"
	}
	fromDate, err := normalizeArxivDateBound(from, false)
	if err != nil {
		return "", fmt.Errorf("invalid submitted_date_from: %w", err)
	}
	toDate, err := normalizeArxivDateBound(to, true)
	if err != nil {
		return "", fmt.Errorf("invalid submitted_date_to: %w", err)
	}
	return fmt.Sprintf(
		"submittedDate:[%s TO %s]",
		fromDate,
		toDate,
	), nil
}

func normalizeArxivDateBound(raw string, upper bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty date")
	}
	digits := dateDigits(raw)
	switch len(digits) {
	case 8:
		if upper {
			digits += "2359"
		} else {
			digits += "0000"
		}
	case 12:
	default:
		return "", fmt.Errorf(
			"expected YYYY-MM-DD, YYYYMMDD, or YYYYMMDDHHMM",
		)
	}
	if _, err := time.Parse("200601021504", digits); err != nil {
		return "", err
	}
	return digits, nil
}

func dateDigits(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fetchPage fetches a page of results
func (c *Client) fetchPage(url string, firstPage bool) (*AtomFeed, error) {
	if c.lastRequest != nil {
		sinceLast := time.Since(*c.lastRequest)
		if sinceLast < c.config.DelaySeconds {
			time.Sleep(c.config.DelaySeconds - sinceLast)
		}
	}

	var lastErr error
	for i := 0; i < c.config.NumRetries; i++ {
		resp, err := c.httpClient.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP error: %d", resp.StatusCode)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		var feed AtomFeed
		if err := xml.Unmarshal(body, &feed); err != nil {
			lastErr = err
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		if !firstPage && len(feed.Entries) == 0 {
			lastErr = fmt.Errorf("no results found")
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		now := time.Now()
		c.lastRequest = &now
		return &feed, nil
	}

	return nil, fmt.Errorf("failed to get feed after %d retries: %w", c.config.NumRetries, lastErr)
}

// AtomFeed atom feed structure
type AtomFeed struct {
	XMLName      xml.Name    `xml:"feed"`
	Title        string      `xml:"title"`
	ID           string      `xml:"id"`
	Updated      string      `xml:"updated"`
	TotalResults string      `xml:"totalResults"`
	StartIndex   string      `xml:"startIndex"`
	ItemsPerPage string      `xml:"itemsPerPage"`
	Entries      []AtomEntry `xml:"entry"`
}

// AtomEntry atom entry structure
type AtomEntry struct {
	ID         string         `xml:"id"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published"`
	Title      string         `xml:"title"`
	Summary    string         `xml:"summary"`
	Authors    []AtomAuthor   `xml:"author"`
	Categories []AtomCategory `xml:"category"`
	Links      []AtomLink     `xml:"link"`
}

// AtomAuthor atom author structure
type AtomAuthor struct {
	Name string `xml:"name"`
}

// AtomCategory atom category structure
type AtomCategory struct {
	Term string `xml:"term,attr"`
}

// AtomLink atom link structure
type AtomLink struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

// ArxivEntry arxiv entry structure
type ArxivEntry struct {
	Comment         string `xml:"http://arxiv.org/schemas/atom comment"`
	JournalRef      string `xml:"http://arxiv.org/schemas/atom journal_ref"`
	DOI             string `xml:"http://arxiv.org/schemas/atom doi"`
	PrimaryCategory struct {
		Term string `xml:"term,attr"`
	} `xml:"http://arxiv.org/schemas/atom primary_category"`
}

// parseEntry parse atom entry to result
func parseEntry(entry AtomEntry) (Result, error) {
	updated, _ := time.Parse(time.RFC3339, entry.Updated)
	published, _ := time.Parse(time.RFC3339, entry.Published)

	authors := make([]Author, len(entry.Authors))
	for i, author := range entry.Authors {
		authors[i] = Author{Name: author.Name}
	}

	categories := make([]string, len(entry.Categories))
	for i, category := range entry.Categories {
		categories[i] = category.Term
	}

	var primaryCategory string
	if len(categories) > 0 {
		primaryCategory = categories[0]
	}

	links := make([]Link, len(entry.Links))
	var pdfURL string
	for i, link := range entry.Links {
		links[i] = Link{
			Href:        link.Href,
			Title:       link.Title,
			Rel:         link.Rel,
			ContentType: link.Type,
		}
		if link.Rel == "related" && link.Type == "application/pdf" {
			pdfURL = link.Href
		}
	}

	entryID := entry.ID
	if strings.Contains(entryID, "arxiv.org/abs/") {
		entryID = strings.Split(entryID, "arxiv.org/abs/")[1]
	}

	return Result{
		EntryID:         entryID,
		Updated:         updated,
		Published:       published,
		Title:           strings.TrimSpace(entry.Title),
		Authors:         authors,
		Summary:         strings.TrimSpace(entry.Summary),
		Comment:         "",
		JournalRef:      "",
		DOI:             "",
		PrimaryCategory: primaryCategory,
		Categories:      categories,
		Links:           links,
		PdfURL:          pdfURL,
	}, nil
}
