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
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/file"
)

const (
	defaultAutoSourceName   = "Auto Source"
	defaultMixedContentName = "Mixed Content"
	defaultAutoContentName  = "Auto Content"
	defaultUserAgent        = "trpc-agent-go/1.0"
	defaultTimeout          = 30 * time.Second
	nameTruncateLength      = 50
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
		name:       defaultAutoSourceName, // Default name.
		metadata:   make(map[string]interface{}),
		httpClient: &http.Client{Timeout: defaultTimeout},
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
	return source.TypeAuto
}

// processInput processes a single input and returns its content and metadata.
func (s *Source) processInput(ctx context.Context, input string) (string, map[string]interface{}, error) {
	inputType := s.deduceInputType(input)

	switch inputType {
	case source.TypeURL:
		return s.processURL(ctx, input)
	case source.TypePDF:
		pdfSrc := file.NewPDFSource(input)
		doc, err := pdfSrc.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeCSV:
		csvSrc := file.NewCSVSource(input)
		doc, err := csvSrc.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeExcel:
		excelSrc := file.NewExcelSource(input)
		doc, err := excelSrc.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeJSON:
		jsonSrc := file.NewJSONSource(input)
		doc, err := jsonSrc.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeTextFile:
		txtSrc := file.NewTXTSource(input)
		doc, err := txtSrc.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeFile:
		// fallback for unknown file types
		return s.processFile(input)
	case source.TypeString:
		return s.processText(input)
	default:
		return "", nil, fmt.Errorf("unable to deduce type for input: %s", input)
	}
}

// processURL processes a URL input.
func (s *Source) processURL(ctx context.Context, urlStr string) (string, map[string]interface{}, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid URL: %w", err)
	}
	content, err := s.fetchURL(ctx, urlStr)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
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
	return content, metadata, nil
}

// processFile processes a file input.
func (s *Source) processFile(filePath string) (string, map[string]interface{}, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read file: %w", err)
	}
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeFile
	metadata[source.MetaFilePath] = filePath
	metadata[source.MetaFileName] = filepath.Base(filePath)
	metadata[source.MetaFileExt] = filepath.Ext(filePath)
	metadata[source.MetaFileSize] = fileInfo.Size()
	metadata[source.MetaFileMode] = fileInfo.Mode().String()
	metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()
	metadata[source.MetaContentLength] = len(content)
	return string(content), metadata, nil
}

// processText processes a text input.
func (s *Source) processText(text string) (string, map[string]interface{}, error) {
	if text == "" {
		return "", nil, fmt.Errorf("content cannot be empty")
	}
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeString
	metadata[source.MetaContentLength] = len(text)
	return text, metadata, nil
}

// fetchURL fetches content from a URL.
func (s *Source) fetchURL(ctx context.Context, urlStr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	// Set user agent to avoid being blocked.
	req.Header.Set("User-Agent", defaultUserAgent)
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
	hash := md5.Sum([]byte(strings.Join(s.inputs, "|")))
	id := fmt.Sprintf("auto_%x", hash[:8])
	name := defaultMixedContentName
	if len(s.inputs) == 1 {
		name = s.generateName(s.inputs[0])
	} else {
		name = fmt.Sprintf("%s (%d items)", defaultMixedContentName, len(s.inputs))
	}
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeAuto
	metadata[source.MetaInputCount] = len(s.inputs)
	metadata[source.MetaInputs] = s.inputs
	metadata[source.MetaContentLength] = len(content)
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
	lines := strings.Split(input, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		name := strings.TrimSpace(lines[0])
		if len(name) > nameTruncateLength {
			name = name[:nameTruncateLength] + "..."
		}
		return name
	}
	return defaultAutoContentName
}

// deduceInputType determines the type of input (URL, file, or text).
func (s *Source) deduceInputType(input string) string {
	if s.isURL(input) {
		return source.TypeURL
	}
	if s.isFilePath(input) {
		ext := strings.ToLower(filepath.Ext(input))
		switch ext {
		case ".pdf":
			return source.TypePDF
		case ".csv":
			return source.TypeCSV
		case ".xlsx", ".xls":
			return source.TypeExcel
		case ".json":
			return source.TypeJSON
		case ".txt", ".md":
			return source.TypeTextFile
		default:
			return source.TypeFile
		}
	}
	return source.TypeString
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
