// Package file provides file-based knowledge source implementations.
package file

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// CSVSource represents a knowledge source for CSV files.
type CSVSource struct {
	filePath string
	name     string
	metadata map[string]interface{}
}

// NewCSVSource creates a new CSV knowledge source.
func NewCSVSource(filePath string) *CSVSource {
	return &CSVSource{
		filePath: filePath,
		name:     "CSV Source",
		metadata: make(map[string]interface{}),
	}
}

// ReadDocument reads the CSV file and returns a document.
func (s *CSVSource) ReadDocument(ctx context.Context) (*document.Document, error) {
	f, err := os.Open(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV file: %w", err)
	}

	var sb strings.Builder
	for _, row := range records {
		sb.WriteString(strings.Join(row, ", "))
		sb.WriteString("\n")
	}

	metadata := map[string]interface{}{
		"source":    source.TypeCSV,
		"file_path": s.filePath,
		"row_count": len(records),
	}
	for k, v := range s.metadata {
		metadata[k] = v
	}

	doc := &document.Document{
		ID:        s.filePath,
		Name:      s.name,
		Content:   sb.String(),
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return doc, nil
}

// Name returns the name of this source.
func (s *CSVSource) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *CSVSource) Type() string {
	return source.TypeCSV
}
