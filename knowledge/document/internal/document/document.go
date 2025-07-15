//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package document provides a document internal utils.
package document

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// CreateDocument creates a new document with the given content and name.
func CreateDocument(content string, name string) *document.Document {
	return &document.Document{
		ID:        GenerateDocumentID(name),
		Name:      name,
		Content:   content,
		Metadata:  make(map[string]interface{}),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// GenerateDocumentID generates a unique ID for a document.
func GenerateDocumentID(name string) string {
	// Simple ID generation based on name and timestamp.
	return strings.ReplaceAll(name, " ", "_") + "_" + time.Now().Format("20060102150405")
}
