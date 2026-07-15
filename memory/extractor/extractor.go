//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package extractor provides memory extraction functionality for trpc-agent-go.
// It includes automatic memory extraction from conversations using LLM.
package extractor

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MemoryExtractor defines the interface for extracting memories from
// conversations.
type MemoryExtractor interface {
	// Extract analyzes the conversation and returns memory operations.
	// It does not modify the memory store directly.
	// The messages parameter contains the conversation messages to analyze.
	// The existing parameter contains current user memories for deduplication.
	Extract(ctx context.Context, messages []model.Message,
		existing []*memory.Entry) ([]*Operation, error)

	// ShouldExtract checks if extraction should be triggered based on context.
	// Returns true if extraction should proceed, false to skip.
	// When no checkers are configured, always returns true.
	ShouldExtract(ctx *ExtractionContext) bool

	// SetPrompt updates the extractor's prompt dynamically.
	// The prompt will be used as the system message for memory extraction.
	// If an empty prompt is provided, it will be ignored and the current
	// prompt will remain unchanged.
	SetPrompt(prompt string)

	// SetModel updates the extractor's model dynamically.
	// This allows switching to different models at runtime based on different
	// scenarios or requirements. If nil is provided, it will be ignored and
	// the current model will remain unchanged.
	SetModel(m model.Model)

	// Metadata returns metadata about the extractor configuration.
	Metadata() map[string]any
}

// Operation represents a memory operation to be executed.
type Operation struct {
	// Type is the type of operation (add, update, delete).
	Type OperationType
	// Memory is the memory content.
	Memory string
	// MemoryID is required for update/delete operations.
	MemoryID string
	// Topics are optional topics for the memory.
	Topics []string

	// Episodic memory fields.
	MemoryKind   memory.Kind // "fact" or "episode".
	EventTime    *time.Time  // When the event occurred.
	Participants []string    // People involved in the event.
	Location     string      // Where the event took place.
}

// OperationType defines the type of memory operation.
type OperationType string

// Operation types.
const (
	OperationAdd    OperationType = "add"
	OperationUpdate OperationType = "update"
	OperationDelete OperationType = "delete"
	OperationClear  OperationType = "clear"
)

// contextKey is an unexported type for context keys in this package.
type contextKey struct{}

// referenceDateKey is the context key for the reference date.
var referenceDateKey = contextKey{}

// WithReferenceDate returns a copy of ctx with the reference date set.
// The extractor uses this date to resolve relative time expressions
// (e.g. "yesterday", "last week") in conversation messages.
// When not set, the extractor falls back to time.Now().UTC().
func WithReferenceDate(
	ctx context.Context, t time.Time,
) context.Context {
	return context.WithValue(ctx, referenceDateKey, t)
}

// ReferenceDateFromContext extracts the reference date from ctx.
// Returns the reference date and true if set, or zero time and
// false otherwise.
func ReferenceDateFromContext(
	ctx context.Context,
) (time.Time, bool) {
	t, ok := ctx.Value(referenceDateKey).(time.Time)
	return t, ok
}
