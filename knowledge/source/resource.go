//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package source

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Resource is one normalized, source-relative text resource together with the
// documents produced from the same captured source input.
type Resource struct {
	// Path is the stable path relative to the logical source root.
	Path string
	// Content is the complete normalized UTF-8 text before chunking.
	Content string
	// ModifiedAt is the source modification time when available.
	ModifiedAt time.Time
	// Documents are chunks or structured entities produced from the resource.
	Documents []*document.Document
}

// ResourceSource is an optional Source capability for loading persistent
// source-level text together with its derived vector documents. Sources that
// do not implement it remain valid vector-only sources.
type ResourceSource interface {
	Source

	// ReadResources captures source input once per resource and produces both
	// Content and Documents from that input. Implementations must not reconstruct
	// Content from Documents.
	ReadResources(ctx context.Context) ([]*Resource, error)
}
