//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import (
	"context"
	"io"
)

// Service defines the interface for artifact storage and retrieval operations.
type Service interface {
	// SaveArtifact saves an artifact to the artifact service storage.
	//
	// The artifact is a file identified by the session info and filename.
	// After saving the artifact, a revision ID is returned to identify
	// the artifact version.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The filename of the artifact
	//   artifact: The artifact to save
	//
	// Returns:
	//   The revision ID. The first version of the artifact has a revision ID of 0.
	//   This is incremented by 1 after each successful save.
	SaveArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, artifact *Artifact) (int, error)

	// ResolveArtifact resolves an artifact reference to its metadata and an optional URL.
	//
	// This method SHOULD NOT download artifact contents.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The filename of the artifact
	//   version: The version of the artifact. If nil, the latest version will be resolved.
	//
	// Returns:
	//   The descriptor or nil if not found.
	ResolveArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (*ArtifactDescriptor, error)

	// LoadArtifact opens a streaming reader for an artifact.
	//
	// This method MUST NOT read the full artifact into memory.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The filename of the artifact
	//   version: The version of the artifact. If nil, the latest version will be loaded.
	//
	// Returns:
	//   A ReadCloser for the content, a descriptor, or (nil, nil, nil) if not found.
	LoadArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (io.ReadCloser, *ArtifactDescriptor, error)

	// LoadArtifactBytes loads an artifact into memory and returns its bytes.
	//
	// This is a convenience method for small artifacts. Callers should prefer
	// ResolveArtifact + LoadArtifact for large artifacts.
	//
	// Returns:
	//   The bytes and descriptor, or (nil, nil, nil) if not found.
	LoadArtifactBytes(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) ([]byte, *ArtifactDescriptor, error)

	// ListArtifactKeys lists all the artifact filenames within a session.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The filename of the artifact
	// Returns:
	//   A list of all artifact filenames within a session.
	ListArtifactKeys(ctx context.Context, sessionInfo SessionInfo) ([]string, error)

	// DeleteArtifact deletes an artifact.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The name of the artifact file
	//
	// Returns:
	//   An error if the operation fails.
	DeleteArtifact(ctx context.Context, sessionInfo SessionInfo, filename string) error

	// ListVersions lists all versions of an artifact.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The name of the artifact file
	//
	// Returns:
	//   A list of all available versions of the artifact.
	ListVersions(ctx context.Context, sessionInfo SessionInfo, filename string) ([]int, error)
}
