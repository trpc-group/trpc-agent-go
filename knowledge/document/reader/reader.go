//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package reader defines the interface for document readers.
// This interface allows reading from any io.Reader source, such as files or HTTP responses.
package reader

import (
	"io"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Reader interface for different document readers.
type Reader interface {
	// ReadFromReader reads content from an io.Reader and returns a list of documents.
	// The name parameter is used to identify the source (e.g., filename, URL).
	ReadFromReader(name string, r io.Reader) ([]*document.Document, error)

	// ReadFromFile reads content from a file path and returns a list of documents.
	ReadFromFile(filePath string) ([]*document.Document, error)

	// ReadFromURL reads content from a URL and returns a list of documents.
	ReadFromURL(url string) ([]*document.Document, error)

	// Name returns the name of this reader.
	Name() string
}
