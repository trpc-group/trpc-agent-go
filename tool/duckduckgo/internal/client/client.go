// Package client provides an HTTP client for DuckDuckGo Instant Answer API.
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client provides methods to interact with DuckDuckGo Instant Answer API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	userAgent  string
}

// New creates a new DuckDuckGo client with the provided configuration.
func New(baseURL, userAgent string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    baseURL,
		userAgent:  userAgent,
		httpClient: httpClient,
	}
}

// FlexibleString is a type that can unmarshal both strings and numbers.
type FlexibleString string

// UnmarshalJSON implements json.Unmarshaler for FlexibleString.
func (fs *FlexibleString) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*fs = FlexibleString(s)
		return nil
	}

	// Try to unmarshal as a number.
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*fs = FlexibleString(n.String())
		return nil
	}

	// If both fail, try to unmarshal as any type and convert to string.
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	*fs = FlexibleString(fmt.Sprintf("%v", v))
	return nil
}

// String returns the string representation.
func (fs FlexibleString) String() string {
	return string(fs)
}

// Response represents the response from DuckDuckGo Instant Answer API.
type Response struct {
	Type             string         `json:"Type"`
	Redirect         string         `json:"Redirect"`
	Definition       string         `json:"Definition"`
	DefinitionSource string         `json:"DefinitionSource"`
	Heading          string         `json:"Heading"`
	Image            string         `json:"Image"`
	ImageWidth       FlexibleString `json:"ImageWidth"`
	ImageHeight      FlexibleString `json:"ImageHeight"`
	Abstract         string         `json:"Abstract"`
	AbstractText     string         `json:"AbstractText"`
	AbstractSource   string         `json:"AbstractSource"`
	AbstractURL      string         `json:"AbstractURL"`
	Answer           string         `json:"Answer"`
	AnswerType       string         `json:"AnswerType"`
	RelatedTopics    []RelatedTopic `json:"RelatedTopics"`
	Results          []Result       `json:"Results"`
	DefinitionURL    string         `json:"DefinitionURL"`
}

// RelatedTopic represents a related topic from DuckDuckGo.
type RelatedTopic struct {
	Result   string `json:"Result"`
	Icon     Icon   `json:"Icon"`
	Text     string `json:"Text"`
	FirstURL string `json:"FirstURL"`
}

// Icon represents an icon from DuckDuckGo.
type Icon struct {
	URL    string         `json:"URL"`
	Height FlexibleString `json:"Height"`
	Width  FlexibleString `json:"Width"`
}

// Result represents a search result from DuckDuckGo.
type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Search performs a search query using DuckDuckGo Instant Answer API.
func (c *Client) Search(query string) (*Response, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	// Prepare the request URL.
	reqURL := fmt.Sprintf("%s/?q=%s&format=json&no_html=1&skip_disambig=1",
		c.baseURL, url.QueryEscape(query))

	// Create the HTTP request.
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers.
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	// Perform the request.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	// Read response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the JSON response.
	var response Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}
