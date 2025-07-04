// Package file provides file-based knowledge source implementations.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// TXTSource represents a knowledge source for plain text files (.txt, .md).
type TXTSource struct {
	filePath string
	name     string
	metadata map[string]interface{}
}

// NewTXTSource creates a new TXT knowledge source.
func NewTXTSource(filePath string) *TXTSource {
	return &TXTSource{
		filePath: filePath,
		name:     "Text File Source",
		metadata: make(map[string]interface{}),
	}
}

// ReadDocument reads the text file and returns a document.
func (s *TXTSource) ReadDocument(ctx context.Context) (*document.Document, error) {
	fileInfo, err := os.Stat(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", s.filePath)
	}
	content, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeTextFile
	metadata[source.MetaFilePath] = s.filePath
	metadata[source.MetaFileName] = filepath.Base(s.filePath)
	metadata[source.MetaFileExt] = filepath.Ext(s.filePath)
	metadata[source.MetaFileSize] = fileInfo.Size()
	metadata[source.MetaFileMode] = fileInfo.Mode().String()
	metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()
	metadata[source.MetaContentLength] = len(content)
	doc := &document.Document{
		ID:        s.filePath,
		Name:      s.name,
		Content:   string(content),
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return doc, nil
}

// Name returns the name of this source.
func (s *TXTSource) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *TXTSource) Type() string {
	return source.TypeTextFile
}
