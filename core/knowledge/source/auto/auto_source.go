// Package auto provides auto-deduction knowledge source implementation.
package auto

import (
	"context"
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/file"
	stringsource "trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/string"
	urlsource "trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/url"
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
		src := urlsource.New([]string{input})
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypePDF:
		src := file.NewPDFSource(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeCSV:
		src := file.NewCSVSource(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeExcel:
		src := file.NewExcelSource(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeJSON:
		src := file.NewJSONSource(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeTextFile:
		src := file.NewTXTSource(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeFile:
		src := file.New([]string{input})
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	case source.TypeString:
		src := stringsource.New(input)
		doc, err := src.ReadDocument(ctx)
		if err != nil {
			return "", nil, err
		}
		return doc.Content, doc.Metadata, nil
	default:
		return "", nil, fmt.Errorf("unable to deduce type for input: %s", input)
	}
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
