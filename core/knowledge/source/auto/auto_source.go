// Package auto provides auto-detection knowledge source implementation.
package auto

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/readerfactory"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
	dirsource "trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/core/knowledge/source/url"
)

const (
	defaultAutoSourceName = "Auto Source"
	autoSourceType        = "auto"
)

// Source represents a knowledge source that automatically detects the source type.
type Source struct {
	inputs        []string
	name          string
	metadata      map[string]interface{}
	readerFactory *readerfactory.Factory
}

// New creates a new auto knowledge source.
func New(inputs []string, opts ...Option) *Source {
	sourceObj := &Source{
		inputs:        inputs,
		name:          defaultAutoSourceName,
		metadata:      make(map[string]interface{}),
		readerFactory: readerfactory.NewFactory(), // Use default config.
	}

	// Apply options.
	for _, opt := range opts {
		opt(sourceObj)
	}

	return sourceObj
}

// ReadDocuments automatically detects the source type and reads documents.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.inputs) == 0 {
		return nil, fmt.Errorf("no inputs provided")
	}

	var allDocuments []*document.Document

	for _, input := range s.inputs {
		documents, err := s.processInput(input)
		if err != nil {
			return nil, fmt.Errorf("failed to process input %s: %w", input, err)
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
	return source.TypeAuto
}

// processInput determines the input type and processes it accordingly.
func (s *Source) processInput(input string) ([]*document.Document, error) {
	// Check if it's a URL.
	if s.isURL(input) {
		return s.processAsURL(input)
	}

	// Check if it's a directory.
	if s.isDirectory(input) {
		return s.processAsDirectory(input)
	}

	// Check if it's a file.
	if s.isFile(input) {
		return s.processAsFile(input)
	}

	// If none of the above, treat as text content.
	return s.processAsText(input)
}

// isURL checks if the input is a valid URL.
func (s *Source) isURL(input string) bool {
	parsedURL, err := url.Parse(input)
	return err == nil && parsedURL.Scheme != "" && parsedURL.Host != ""
}

// isDirectory checks if the input is a directory.
func (s *Source) isDirectory(input string) bool {
	info, err := os.Stat(input)
	return err == nil && info.IsDir()
}

// isFile checks if the input is a file.
func (s *Source) isFile(input string) bool {
	info, err := os.Stat(input)
	return err == nil && info.Mode().IsRegular()
}

// processAsURL processes the input as a URL.
func (s *Source) processAsURL(input string) ([]*document.Document, error) {
	urlSource := urlsource.New([]string{input})
	urlSource.SetReaderFactory(s.readerFactory)

	// Copy metadata.
	for k, v := range s.metadata {
		urlSource.SetMetadata(k, v)
	}

	return urlSource.ReadDocuments(context.Background())
}

// processAsDirectory processes the input as a directory.
func (s *Source) processAsDirectory(input string) ([]*document.Document, error) {
	dirSource := dirsource.New(input)
	dirSource.SetReaderFactory(s.readerFactory)

	// Copy metadata.
	for k, v := range s.metadata {
		dirSource.SetMetadata(k, v)
	}

	return dirSource.ReadDocuments(context.Background())
}

// processAsFile processes the input as a file.
func (s *Source) processAsFile(input string) ([]*document.Document, error) {
	fileSource := filesource.New([]string{input})
	fileSource.SetReaderFactory(s.readerFactory)

	// Copy metadata.
	for k, v := range s.metadata {
		fileSource.SetMetadata(k, v)
	}

	return fileSource.ReadDocuments(context.Background())
}

// processAsText processes the input as text content.
func (s *Source) processAsText(input string) ([]*document.Document, error) {
	// Create a text reader and process the input as text.
	reader := s.readerFactory.CreateReader("document.txt")
	return reader.Read(input, "text_input")
}

// SetReaderFactory sets the reader factory for this source.
func (s *Source) SetReaderFactory(factory *readerfactory.Factory) {
	s.readerFactory = factory
}
