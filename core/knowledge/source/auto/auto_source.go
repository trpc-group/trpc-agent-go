// Package auto provides auto-deduction knowledge source implementation.
package auto

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source represents a knowledge source that automatically deduces the type of input.
// It can handle URLs, file paths, and plain text content.
type Source struct {
	inputs     []string
	name       string
	metadata   map[string]interface{}
	httpClient *http.Client
}

// New creates a new auto-deduction knowledge source.
func New(inputs []string, opts ...Option) *Source {
	source := &Source{
		inputs:     inputs,
		name:       "Auto Source", // Default name.
		metadata:   make(map[string]interface{}),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	// Apply options.
	for _, opt := range opts {
		opt(source)
	}

	return source
}

// ReadDocument reads all inputs and returns a combined document.
func (s *Source) ReadDocument(ctx context.Context) (*document.Document, error) {
	if len(s.inputs) == 0 {
		return nil, fmt.Errorf("no inputs provided")
	}

	var allContent strings.Builder
	var allMetadata []map[string]interface{}

	for _, input := range s.inputs {
		content, metadata, err := s.processInput(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to process input %s: %w", input, err)
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
	return "auto"
}

// processInput processes a single input and returns its content and metadata.
func (s *Source) processInput(ctx context.Context, input string) (string, map[string]interface{}, error) {
	inputType := s.deduceInputType(input)

	switch inputType {
	case "url":
		return s.processURL(ctx, input)
	case "file":
		return s.processFile(input)
	case "text":
		return s.processText(input)
	default:
		return "", nil, fmt.Errorf("unable to deduce type for input: %s", input)
	}
}

// processURL processes a URL input.
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

// processFile processes a file input.
func (s *Source) processFile(filePath string) (string, map[string]interface{}, error) {
	// Get file info.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Check if it's a regular file.
	if !fileInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("not a regular file: %s", filePath)
	}

	// Read file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Prepare metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "file"
	metadata["file_path"] = filePath
	metadata["file_name"] = filepath.Base(filePath)
	metadata["file_ext"] = filepath.Ext(filePath)
	metadata["file_size"] = fileInfo.Size()
	metadata["file_mode"] = fileInfo.Mode().String()
	metadata["modified_at"] = fileInfo.ModTime().UTC()
	metadata["content_length"] = len(content)

	return string(content), metadata, nil
}

// processText processes a text input.
func (s *Source) processText(text string) (string, map[string]interface{}, error) {
	if text == "" {
		return "", nil, fmt.Errorf("content cannot be empty")
	}

	// Prepare metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "string"
	metadata["content_length"] = len(text)

	return text, metadata, nil
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

// createDocument creates a document from combined input content.
func (s *Source) createDocument(content string, inputMetadata []map[string]interface{}) *document.Document {
	// Generate ID based on inputs.
	hash := md5.Sum([]byte(strings.Join(s.inputs, "|")))
	id := fmt.Sprintf("auto_%x", hash[:8])

	// Generate name.
	name := "Mixed Content"
	if len(s.inputs) == 1 {
		name = s.generateName(s.inputs[0])
	} else {
		name = fmt.Sprintf("Mixed Content (%d items)", len(s.inputs))
	}

	// Combine metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "auto"
	metadata["input_count"] = len(s.inputs)
	metadata["inputs"] = s.inputs
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

// generateName generates a name for the document based on input.
func (s *Source) generateName(input string) string {
	if s.isURL(input) {
		parsedURL, err := url.Parse(input)
		if err == nil {
			return parsedURL.Host
		}
	}

	if s.isFilePath(input) {
		return filepath.Base(input)
	}

	// For text, use first line.
	lines := strings.Split(input, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		name := strings.TrimSpace(lines[0])
		if len(name) > 50 {
			name = name[:50] + "..."
		}
		return name
	}

	return "Auto Content"
}

// deduceInputType determines the type of input (URL, file, or text).
func (s *Source) deduceInputType(input string) string {
	// Check if it's a URL.
	if s.isURL(input) {
		return "url"
	}

	// Check if it's a file path.
	if s.isFilePath(input) {
		return "file"
	}

	// Default to text content.
	return "text"
}

// isURL checks if the input is a valid URL.
func (s *Source) isURL(input string) bool {
	// Check for common URL schemes.
	urlSchemes := []string{"http://", "https://", "ftp://", "sftp://"}
	for _, scheme := range urlSchemes {
		if strings.HasPrefix(strings.ToLower(input), scheme) {
			_, err := url.Parse(input)
			return err == nil
		}
	}

	// Check if it looks like a URL without scheme.
	if strings.Contains(input, ".") && (strings.Contains(input, "/") || strings.Contains(input, ":")) {
		// Try to parse as URL with http scheme.
		testURL := "http://" + input
		_, err := url.Parse(testURL)
		return err == nil
	}

	return false
}

// isFilePath checks if the input is a valid file path.
func (s *Source) isFilePath(input string) bool {
	// Check if it's an absolute path.
	if filepath.IsAbs(input) {
		_, err := os.Stat(input)
		return err == nil
	}

	// Check if it's a relative path that exists.
	if _, err := os.Stat(input); err == nil {
		return true
	}

	// Check if it has a file extension and looks like a file path.
	ext := filepath.Ext(input)
	if ext != "" && !strings.Contains(input, "://") {
		// Common file extensions.
		commonExts := []string{".txt", ".md", ".pdf", ".csv", ".xlsx", ".json", ".xml", ".html", ".htm"}
		for _, commonExt := range commonExts {
			if strings.EqualFold(ext, commonExt) {
				return true
			}
		}
	}

	return false
}
