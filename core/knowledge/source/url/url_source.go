// Package url provides URL-based knowledge source implementation.
package url

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source represents a knowledge source for web content.
type Source struct {
	urls       []string
	name       string
	metadata   map[string]interface{}
	httpClient *http.Client
}

// New creates a new URL knowledge source.
func New(urls []string, opts ...Option) *Source {
	source := &Source{
		urls:       urls,
		name:       "URL Source", // Default name.
		metadata:   make(map[string]interface{}),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	// Apply options.
	for _, opt := range opts {
		opt(source)
	}

	return source
}

// ReadDocument reads all URLs and returns a combined document.
func (s *Source) ReadDocument(ctx context.Context) (*document.Document, error) {
	if len(s.urls) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	var allContent strings.Builder
	var allMetadata []map[string]interface{}

	for _, urlStr := range s.urls {
		content, metadata, err := s.processURL(ctx, urlStr)
		if err != nil {
			return nil, fmt.Errorf("failed to process URL %s: %w", urlStr, err)
		}
		allContent.WriteString(content)
		allContent.WriteString("\n\n")
		allMetadata = append(allMetadata, metadata)
	}

	return s.createDocument(allContent.String(), allMetadata), nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return "url"
}

// processURL processes a single URL and returns its content and metadata.
func (s *Source) processURL(ctx context.Context, urlStr string) (string, map[string]interface{}, error) {
	// Validate URL.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Fetch content from URL.
	content, err := s.fetchURL(ctx, urlStr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch URL: %w", err)
	}

	// Prepare metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "url"
	metadata["url"] = urlStr
	metadata["url_host"] = parsedURL.Host
	metadata["url_path"] = parsedURL.Path
	metadata["url_scheme"] = parsedURL.Scheme
	metadata["content_length"] = len(content)

	return content, metadata, nil
}

// fetchURL fetches content from a URL.
func (s *Source) fetchURL(ctx context.Context, urlStr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set user agent to avoid being blocked.
	req.Header.Set("User-Agent", "trpc-agent-go/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// createDocument creates a document from combined URL content.
func (s *Source) createDocument(content string, urlMetadata []map[string]interface{}) *document.Document {
	// Generate ID based on URLs.
	hash := md5.Sum([]byte(strings.Join(s.urls, "|")))
	id := fmt.Sprintf("url_%x", hash[:8])

	// Generate name from first URL.
	name := "Multiple URLs"
	if len(s.urls) > 0 {
		name = s.generateName(s.urls[0])
		if len(s.urls) > 1 {
			name += fmt.Sprintf(" and %d more", len(s.urls)-1)
		}
	}

	// Combine metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "url"
	metadata["url_count"] = len(s.urls)
	metadata["urls"] = s.urls
	metadata["content_length"] = len(content)

	return &document.Document{
		ID:        id,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// generateName generates a name for the document based on URL.
func (s *Source) generateName(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "Web Content"
	}

	// Use hostname as base name.
	name := parsedURL.Host

	// Add path if it's not just "/".
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) > 0 {
			lastPart := pathParts[len(pathParts)-1]
			if lastPart != "" {
				name = lastPart
			}
		}
	}

	// Clean up the name.
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")

	if name == "" {
		name = "Web Content"
	}

	return name
}
