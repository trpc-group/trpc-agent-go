//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package document provides a document internal utils.
package document

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// CreateDocument creates a new document with the given content and name.
func CreateDocument(content string, name string) *document.Document {
	return &document.Document{
		ID:        GenerateDocumentID(name, content),
		Name:      name,
		Content:   content,
		Metadata:  make(map[string]any),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// GenerateDocumentID generates a unique ID for a document.
// Uses content hash for identification and random bytes for uniqueness.
func GenerateDocumentID(name string, content string) string {
	// Content hash (first 8 bytes = 16 hex chars)
	hash := sha256.Sum256([]byte(content))
	contentHash := hex.EncodeToString(hash[:8])

	// Random bytes for uniqueness (8 bytes = 16 hex chars)
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to timestamp-based uniqueness if crypto/rand fails
		ts := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			randomBytes[i] = byte(ts >> (i * 8))
		}
	}
	randomStr := hex.EncodeToString(randomBytes)

	return strings.ReplaceAll(name, " ", "_") + "_" + contentHash + "_" + randomStr
}
