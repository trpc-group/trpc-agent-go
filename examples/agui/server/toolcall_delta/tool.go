//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const createDocumentToolName = "create_document"

type createDocumentArgs struct {
	Title   string `json:"title" description:"A short title for the generated document."`
	Content string `json:"content" description:"The complete generated document content to save."`
}

type createDocumentResult struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Bytes      int    `json:"bytes"`
}

type savedDocument struct {
	ID        string
	Title     string
	Content   string
	CreatedAt time.Time
}

type documentStore struct {
	mu        sync.Mutex
	nextID    int64
	documents map[string]savedDocument
}

func newDocumentStore() *documentStore {
	return &documentStore{documents: make(map[string]savedDocument)}
}

func newCreateDocumentTool(store *documentStore) tool.Tool {
	return function.NewFunctionTool(
		store.createDocument,
		function.WithName(createDocumentToolName),
		function.WithDescription("Save a generated document. The content argument should contain the complete generated document body."),
	)
}

func (s *documentStore) createDocument(ctx context.Context, args createDocumentArgs) (createDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return createDocumentResult{}, err
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = "Untitled Document"
	}
	content := args.Content
	if strings.TrimSpace(content) == "" {
		return createDocumentResult{}, fmt.Errorf("content is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return createDocumentResult{}, err
	}
	createdAt := time.Now()
	s.nextID++
	id := fmt.Sprintf("doc_%d", s.nextID)
	s.documents[id] = savedDocument{
		ID:        id,
		Title:     title,
		Content:   content,
		CreatedAt: createdAt,
	}
	return createDocumentResult{
		DocumentID: id,
		Title:      title,
		Bytes:      len(content),
	}, nil
}
