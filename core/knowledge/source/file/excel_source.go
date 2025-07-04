// Package file provides file-based knowledge source implementations.
package file

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// ExcelSource represents a knowledge source for Excel files.
type ExcelSource struct {
	filePath string
	name     string
	metadata map[string]interface{}
}

// NewExcelSource creates a new Excel knowledge source.
func NewExcelSource(filePath string) *ExcelSource {
	return &ExcelSource{
		filePath: filePath,
		name:     "Excel Source",
		metadata: make(map[string]interface{}),
	}
}

// ReadDocument reads the Excel file and returns a document.
func (s *ExcelSource) ReadDocument(ctx context.Context) (*document.Document, error) {
	// TODO: Implement Excel reading logic here (e.g., using github.com/xuri/excelize).
	return nil, fmt.Errorf("Excel extraction not implemented yet")
}

// Name returns the name of this source.
func (s *ExcelSource) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *ExcelSource) Type() string {
	return source.TypeExcel
}
