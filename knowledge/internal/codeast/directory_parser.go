//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeast

import (
	"path/filepath"
	"sync"
)

// FileType constants for directory parser registration.
// These mirror source.FileReaderType values and live here to avoid import cycles
// between codeast and source packages.
const (
	FileTypeGo    = "go"
	FileTypeProto = "proto"
)

// ParseOption configures a ParseDirectory call.
type ParseOption func(*parseOptions)

type parseOptions struct {
	concurrency  int
	includeFiles []string
}

// WithParseConcurrency sets the parser concurrency.
// Zero or negative values mean use the parser's default.
func WithParseConcurrency(n int) ParseOption {
	return func(o *parseOptions) {
		o.concurrency = n
	}
}

// WithParseIncludeFiles limits directory parsing to the given absolute or
// directory-relative files when the parser supports scoped loading.
func WithParseIncludeFiles(files []string) ParseOption {
	return func(o *parseOptions) {
		o.includeFiles = append([]string(nil), files...)
	}
}

// ParseConcurrency resolves the concurrency value from the given options.
func ParseConcurrency(opts []ParseOption) int {
	o := &parseOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o.concurrency
}

// ParseIncludeFiles resolves the include-file list from the given options.
func ParseIncludeFiles(opts []ParseOption) []string {
	o := &parseOptions{}
	for _, opt := range opts {
		opt(o)
	}
	if len(o.includeFiles) == 0 {
		return nil
	}
	files := make([]string, 0, len(o.includeFiles))
	for _, file := range o.includeFiles {
		if file == "" {
			continue
		}
		if abs, err := filepath.Abs(file); err == nil {
			file = abs
		}
		files = append(files, filepath.Clean(file))
	}
	return files
}

// DirectoryParser parses code under a directory into a code AST result.
type DirectoryParser interface {
	ParseDirectory(dirPath string, opts ...ParseOption) (*Result, error)
}

var (
	directoryParsersMu sync.RWMutex
	directoryParsers   = map[string]DirectoryParser{}
)

// RegisterDirectoryParser registers a directory parser for the given file type.
// Last registration wins.
func RegisterDirectoryParser(fileType string, parser DirectoryParser) {
	directoryParsersMu.Lock()
	defer directoryParsersMu.Unlock()
	directoryParsers[fileType] = parser
}

// GetDirectoryParser returns the registered directory parser for the given file type.
func GetDirectoryParser(fileType string) (DirectoryParser, bool) {
	directoryParsersMu.RLock()
	defer directoryParsersMu.RUnlock()
	p, ok := directoryParsers[fileType]
	return p, ok
}
