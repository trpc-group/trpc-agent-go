//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"context"
	"errors"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
)

var (
	// ErrResourceNotFound means the requested source or path is not persisted.
	ErrResourceNotFound = resourcestore.ErrNotFound
	// ErrResourceRepresentationUnavailable means the resource cannot be read as text.
	ErrResourceRepresentationUnavailable = resourcestore.ErrRepresentationUnavailable
	// ErrResourcePermissionDenied means the store rejected access to the resource.
	ErrResourcePermissionDenied = resourcestore.ErrPermissionDenied
	// ErrResourceStoreUnavailable means the resource store cannot currently be reached.
	ErrResourceStoreUnavailable = resourcestore.ErrUnavailable
	// ErrResourceCapabilityUnavailable means no ResourceStore is configured.
	ErrResourceCapabilityUnavailable = errors.New("knowledge resource capability unavailable")
	// ErrResourceLimitExceeded means a resource operation exceeded a service limit.
	ErrResourceLimitExceeded = errors.New("knowledge resource limit exceeded")
)

// ResourceKnowledge is an optional, independent capability for browsing and
// reading persisted file-like content. It does not imply semantic Search
// support, and Knowledge does not require implementations to provide it.
type ResourceKnowledge interface {
	// ListSources lists sources persisted in the resource store.
	ListSources(ctx context.Context, req *ListSourcesRequest) (*ListSourcesResult, error)

	// ListResources lists direct children from a persisted source tree.
	ListResources(ctx context.Context, req *ListResourcesRequest) (*ListResourcesResult, error)

	// ReadResource reads a bounded line range from persisted resource content.
	ReadResource(ctx context.Context, req *ReadResourceRequest) (*ReadResourceResult, error)

	// GrepResource finds bounded context blocks in persisted resource content.
	GrepResource(ctx context.Context, req *GrepResourceRequest) (*GrepResourceResult, error)
}

// SourceInfo describes one persisted source without exposing provider details.
type SourceInfo = resourcestore.SourceInfo

// ResourceInfo describes one persisted file or directory.
type ResourceInfo = resourcestore.ResourceInfo

// ListSourcesRequest is reserved for future source filters.
type ListSourcesRequest struct{}

// ListSourcesResult contains persisted resource sources.
type ListSourcesResult struct {
	Sources []*SourceInfo `json:"sources"`
}

// ListResourcesRequest configures direct-child resource listing. An empty
// ParentPath addresses the persisted source root.
type ListResourcesRequest struct {
	// SourceID identifies the source to browse and is required.
	SourceID string `json:"source_id" jsonschema:"description=Stable source ID to browse,required"`
	// ParentPath is a source-relative directory path. Empty means the source root.
	ParentPath string `json:"parent_path,omitempty" jsonschema:"description=Source-relative parent path; omit for the source root"`
}

// ListResourcesResult contains direct children from the persisted source snapshot.
type ListResourcesResult struct {
	Resources []*ResourceInfo `json:"resources"`
}

// ReadResourceRequest requests an inclusive, one-based line range from persisted content.
type ReadResourceRequest struct {
	// SourceID identifies the source and is required.
	SourceID string `json:"source_id" jsonschema:"description=Stable source ID,required"`
	// Path is a safe source-relative resource path and is required.
	Path string `json:"path" jsonschema:"description=Source-relative resource path,required"`
	// StartLine is the inclusive one-based start line. Non-positive values mean line 1.
	StartLine int `json:"start_line,omitempty" jsonschema:"description=Inclusive one-based start line; defaults to 1"`
	// EndLine is the inclusive one-based end line. Non-positive values select a bounded window.
	EndLine int `json:"end_line,omitempty" jsonschema:"description=Inclusive one-based end line; defaults to a bounded window"`
}

// ReadResourceResult contains a bounded line range from persisted resource content.
type ReadResourceResult struct {
	SourceID  string   `json:"source_id"`
	Path      string   `json:"path"`
	Lines     []string `json:"lines"`
	StartLine int      `json:"start_line"`
	// EndLine is zero when the requested range contains no persisted lines.
	EndLine       int  `json:"end_line"`
	NextStartLine int  `json:"next_start_line,omitempty"`
	EOF           bool `json:"eof"`
}

// GrepResourceRequest configures a bounded pattern search over persisted content.
type GrepResourceRequest struct {
	SourceID   string `json:"source_id" jsonschema:"description=Stable source ID,required"`
	Path       string `json:"path" jsonschema:"description=Source-relative resource path,required"`
	Pattern    string `json:"pattern" jsonschema:"description=Non-empty literal or regular expression,required"`
	Regex      bool   `json:"regex,omitempty" jsonschema:"description=Treat pattern as a regular expression"`
	Before     int    `json:"before,omitempty" jsonschema:"description=Context lines before each match"`
	After      int    `json:"after,omitempty" jsonschema:"description=Context lines after each match"`
	MaxMatches int    `json:"max_matches,omitempty" jsonschema:"description=Maximum number of matches to return"`
}

// GrepBlock is one merged context range. MatchLines contains the one-based
// lines that matched Pattern within the block.
type GrepBlock struct {
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
	Lines      []string `json:"lines"`
	MatchLines []int    `json:"match_lines"`
}

// GrepResourceResult contains bounded matches from persisted resource content.
type GrepResourceResult struct {
	SourceID  string       `json:"source_id"`
	Path      string       `json:"path"`
	Blocks    []*GrepBlock `json:"blocks"`
	Truncated bool         `json:"truncated"`
}

func cleanResourcePath(value string) (string, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\x00') {
		return "", false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
		strings.HasPrefix(cleaned, "/") || hasResourceScheme(cleaned) || isWindowsAbsolutePath(cleaned) {
		return "", false
	}
	return cleaned, true
}

func cleanOptionalResourcePath(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", true
	}
	return cleanResourcePath(value)
}

func isWindowsAbsolutePath(value string) bool {
	if len(value) < 3 || value[1] != ':' || value[2] != '/' {
		return false
	}
	return (value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')
}

func hasResourceScheme(value string) bool {
	colon := strings.IndexByte(value, ':')
	if colon <= 0 {
		return false
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 && slash < colon {
		return false
	}
	for index := 0; index < colon; index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(index > 0 && ((character >= '0' && character <= '9') || character == '+' || character == '-' || character == '.')) {
			continue
		}
		return false
	}
	return true
}
