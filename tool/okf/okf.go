//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package okf provides a vendor-neutral, read-only view over an Open Knowledge
// Format (OKF) bundle and adapts it into agent tools.
//
// OKF (https://github.com/GoogleCloudPlatform/knowledge-catalog) represents
// knowledge as a directory of markdown files with YAML frontmatter. Each
// non-reserved .md file is one "concept" whose identity is its bundle-relative
// path with the .md suffix removed. Concepts reference each other with ordinary
// markdown links, forming a graph.
//
// This package defines the Store abstraction (the OKF "capability") and, via
// NewToolSet, exposes it to an LLM agent as three tools: okf_list (progressive
// disclosure), okf_read (read one concept + its links) and okf_find (locate
// concepts). A local, directory-backed Store lives in the localokf sub-package;
// a remote Store (a knowledge-catalog service, a git-bundle server, ...) only
// needs to satisfy the same interface to be swapped in.
package okf

import (
	"context"
	"errors"
)

// Reserved filenames defined by the OKF spec. They are never treated as
// concepts.
const (
	// IndexFile enumerates a directory's contents for progressive disclosure.
	IndexFile = "index.md"
	// LogFile records change history in ISO-8601 date-grouped entries.
	LogFile = "log.md"
)

// ErrUnsupported is returned by a Store whose backend cannot provide a
// capability (for example a remote store with no search index answering Find).
var ErrUnsupported = errors.New("okf: capability not supported by backend")

// ErrNotFound is returned (wrapped, without leaking a filesystem path) by a
// Store when a requested concept does not exist, so callers can distinguish it
// from I/O errors with errors.Is.
var ErrNotFound = errors.New("okf: concept not found")

// Frontmatter holds the reserved OKF frontmatter fields plus any
// producer-defined extensions. Only Type is required by the spec; consumers
// must tolerate everything else being absent.
type Frontmatter struct {
	Type        string         `json:"type" yaml:"type"`                                   // REQUIRED: the only mandatory field.
	Title       string         `json:"title,omitempty" yaml:"title,omitempty"`             // RECOMMENDED.
	Description string         `json:"description,omitempty" yaml:"description,omitempty"` // RECOMMENDED single line.
	Resource    string         `json:"resource,omitempty" yaml:"resource,omitempty"`       // RECOMMENDED canonical URI.
	Tags        []string       `json:"tags,omitempty" yaml:"tags,omitempty"`               // OPTIONAL.
	Timestamp   string         `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`     // OPTIONAL, ISO-8601 (kept verbatim).
	OKFVersion  string         `json:"okf_version,omitempty" yaml:"okf_version,omitempty"` // OPTIONAL, only in the root index.md.
	Extra       map[string]any `json:"extra,omitempty" yaml:",inline"`                     // Unknown / producer keys, preserved (nested under "extra" in tool JSON).
}

// Link is one outgoing markdown link from a concept body, normalized to the
// bundle-relative concept id it targets. Links are untyped directed edges; the
// relationship's meaning lives in the surrounding prose.
type Link struct {
	Target string `json:"target"`         // Normalized concept id (path minus .md).
	Text   string `json:"text,omitempty"` // The markdown link text.
}

// Concept is one OKF concept: its parsed frontmatter, markdown body and links.
type Concept struct {
	ID          string      `json:"id"`                  // Bundle-relative path minus .md.
	Frontmatter Frontmatter `json:"frontmatter"`         //
	Body        string      `json:"body"`                // Markdown body with frontmatter stripped.
	Links       []Link      `json:"links,omitempty"`     // Outgoing links to related concepts.
	Truncated   bool        `json:"truncated,omitempty"` // True if Body was truncated by a size cap.
}

// ConceptMeta is the lightweight card used in listings and search hits: enough
// for an agent to decide whether to read the full concept.
type ConceptMeta struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// Listing is the result of listing one directory (progressive disclosure).
type Listing struct {
	Dir        string        `json:"dir"`                   // "" == bundle root.
	Index      string        `json:"index,omitempty"`       // index.md content, if present.
	OKFVersion string        `json:"okf_version,omitempty"` // Bundle version, parsed from the root index.md only.
	Concepts   []ConceptMeta `json:"concepts,omitempty"`    // Concepts directly under Dir (reserved files excluded).
	Subdirs    []string      `json:"subdirs,omitempty"`     // Immediate sub-directories.
}

// Query describes a Find request. Text is matched against title/description/
// body; Type and Tags filter on frontmatter.
type Query struct {
	Text  string   `json:"text,omitempty"`
	Type  string   `json:"type,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Limit int      `json:"limit,omitempty"` // 0 == backend default.
}

// Hit is one Find result.
type Hit struct {
	ConceptMeta
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score,omitempty"` // 0 == unranked (local); >0 == backend relevance.
}

// Store is a read-only OKF concept repository.
//
// Implementations MUST tolerate, per OKF v0.1 consumer conformance: missing
// optional fields, unknown types, unknown frontmatter keys, broken links and a
// missing index.md. A backend without search may return ErrUnsupported from
// Find (or the caller can drop the find tool with WithFindEnabled(false)).
type Store interface {
	// List returns the listing for dir ("" == bundle root).
	List(ctx context.Context, dir string) (Listing, error)
	// Read returns the concept identified by its bundle-relative id (no .md).
	Read(ctx context.Context, conceptID string) (Concept, error)
	// Find returns concepts matching q, best-effort.
	Find(ctx context.Context, q Query) ([]Hit, error)
}
