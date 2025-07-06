// Package url provides URL-based knowledge source implementation.
package url

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/readerfactory"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

const (
	defaultURLSourceName = "URL Source"
	urlSourceType        = "url"
)

var defaultClient = &http.Client{Timeout: 30 * time.Second}

// Source represents a knowledge source for URL-based content.
type Source struct {
	urls          []string
	name          string
	metadata      map[string]interface{}
	readerFactory *readerfactory.Factory
	httpClient    *http.Client
	timeout       time.Duration
}

// New creates a new URL knowledge source.
func New(urls []string, opts ...Option) *Source {
	sourceObj := &Source{
		urls:          urls,
		name:          defaultURLSourceName,
		metadata:      make(map[string]interface{}),
		readerFactory: readerfactory.NewFactory(), // Use default config.
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		timeout:       30 * time.Second,
	}

	// Apply options.
	for _, opt := range opts {
		opt(sourceObj)
	}

	return sourceObj
}

// ReadDocuments downloads content from all URLs and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.urls) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	var allDocuments []*document.Document

	for _, urlStr := range s.urls {
		documents, err := s.processURL(urlStr)
		if err != nil {
			return nil, fmt.Errorf("failed to process URL %s: %w", urlStr, err)
		}
		allDocuments = append(allDocuments, documents...)
	}

	return allDocuments, nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return source.TypeURL
}

// processURL downloads content from a URL and returns its documents.
func (s *Source) processURL(urlStr string) ([]*document.Document, error) {
	// Parse the URL.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Create HTTP request with context.
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set user agent to avoid being blocked.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KnowledgeSource/1.0)")

	// Make the request.
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Read the response body.
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Create metadata for this URL.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeURL
	metadata[source.MetaURL] = urlStr
	metadata[source.MetaURLHost] = parsedURL.Host
	metadata[source.MetaURLPath] = parsedURL.Path
	metadata[source.MetaURLScheme] = parsedURL.Scheme
	metadata[source.MetaContentLength] = len(content)

	// Determine the content type and file name.
	contentType := resp.Header.Get("Content-Type")
	fileName := s.getFileName(parsedURL, contentType)

	// Create the appropriate reader based on content type or file extension.
	var reader reader.Reader
	if contentType != "" {
		reader = s.readerFactory.CreateReaderByContentType(contentType)
	} else {
		// Fall back to file extension.
		reader = s.readerFactory.CreateReader(fileName)
	}

	// Read the content and create documents.
	documents, err := reader.Read(string(content), fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read content with reader: %w", err)
	}

	// Add metadata to all documents.
	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]interface{})
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
	}

	return documents, nil
}

// getFileName extracts a file name from the URL or content type.
func (s *Source) getFileName(parsedURL *url.URL, contentType string) string {
	// Try to get file name from URL path.
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		fileName := filepath.Base(parsedURL.Path)
		if fileName != "" && fileName != "." {
			return fileName
		}
	}

	// Try to get file name from content type.
	if contentType != "" {
		parts := strings.Split(contentType, ";")
		mainType := strings.TrimSpace(parts[0])

		switch {
		case strings.Contains(mainType, "text/html"):
			return "index.html"
		case strings.Contains(mainType, "text/plain"):
			return "document.txt"
		case strings.Contains(mainType, "application/json"):
			return "document.json"
		case strings.Contains(mainType, "text/csv"):
			return "document.csv"
		case strings.Contains(mainType, "application/pdf"):
			return "document.pdf"
		default:
			return "document"
		}
	}

	// Fall back to host name.
	if parsedURL.Host != "" {
		return parsedURL.Host + ".txt"
	}

	return "document.txt"
}

// SetReaderFactory sets the reader factory for this source.
func (s *Source) SetReaderFactory(factory *readerfactory.Factory) {
	s.readerFactory = factory
}

// SetMetadata sets metadata for this source.
func (s *Source) SetMetadata(key string, value interface{}) {
	if s.metadata == nil {
		s.metadata = make(map[string]interface{})
	}
	s.metadata[key] = value
}
