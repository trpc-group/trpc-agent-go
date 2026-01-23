//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fileref provides a public API for parsing and reading file
// references like workspace://... and artifact://....
//
// These references are used by tools to share a unified file view.
package fileref

import (
	"context"

	internal "trpc.group/trpc-go/trpc-agent-go/internal/fileref"
)

const (
	// SchemeArtifact is the artifact:// file ref scheme.
	SchemeArtifact = internal.SchemeArtifact
	// SchemeWorkspace is the workspace:// file ref scheme.
	SchemeWorkspace = internal.SchemeWorkspace

	// ArtifactPrefix is the "artifact://" prefix.
	ArtifactPrefix = internal.ArtifactPrefix
	// WorkspacePrefix is the "workspace://" prefix.
	WorkspacePrefix = internal.WorkspacePrefix
)

// Ref is a parsed file reference.
//
// When Scheme is empty, Path is a caller-defined local path
// (for example, relative to a file tool base directory).
type Ref = internal.Ref

// WorkspaceFile is a read-only view of an exported skill_run output.
// It is safe to pass across tools because it contains inline content.
type WorkspaceFile struct {
	Name     string
	Content  string
	MIMEType string
}

// WorkspaceRef builds a workspace:// reference for the given relative path.
func WorkspaceRef(rel string) string {
	return internal.WorkspaceRef(rel)
}

// Parse parses raw into a Ref.
//
// When the returned Ref has an empty Scheme, the caller should treat Path as
// a local path (for example, relative to a tool base directory).
func Parse(raw string) (Ref, error) {
	return internal.Parse(raw)
}

// TryRead reads raw if it is a supported file reference.
//
// When handled is false, raw is not a reference and the caller should treat
// it as a local path.
func TryRead(
	ctx context.Context,
	raw string,
) (string, string, bool, error) {
	return internal.TryRead(ctx, raw)
}

// WorkspaceFiles returns files exported from skill_run output_files in ctx.
func WorkspaceFiles(ctx context.Context) []WorkspaceFile {
	files := internal.WorkspaceFiles(ctx)
	if len(files) == 0 {
		return nil
	}

	out := make([]WorkspaceFile, 0, len(files))
	for _, f := range files {
		out = append(out, WorkspaceFile{
			Name:     f.Name,
			Content:  f.Content,
			MIMEType: f.MIMEType,
		})
	}
	return out
}
