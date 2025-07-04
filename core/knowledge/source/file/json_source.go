// Package file provides file-based knowledge source implementations.
package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// JSONSource represents a knowledge source for JSON files.
type JSONSource struct {
	filePath string
	name     string
	metadata map[string]interface{}
}

// NewJSONSource creates a new JSON knowledge source.
func NewJSONSource(filePath string) *JSONSource {
	return &JSONSource{
		filePath: filePath,
		name:     "JSON Source",
		metadata: make(map[string]interface{}),
	}
}

// ReadDocument reads the JSON file and returns a document.
func (s *JSONSource) ReadDocument(ctx context.Context) (*document.Document, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open JSON file: %w", err)
	}
	defer f.Close()

	var data interface{}
	dec := json.NewDecoder(f)
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode JSON file: %w", err)
	}

	contentBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	metadata := map[string]interface{}{
		"source":    source.TypeJSON,
		"file_path": s.filePath,
	}
	for k, v := range s.metadata {
		metadata[k] = v
	}

	doc := &document.Document{
		ID:        s.filePath,
		Name:      s.name,
		Content:   string(contentBytes),
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return doc, nil
}

// Name returns the name of this source.
func (s *JSONSource) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *JSONSource) Type() string {
	return source.TypeJSON
}
