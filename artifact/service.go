//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import "context"

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

	// LoadArtifact gets an artifact from the artifact service storage.
	//
	// The artifact is a file identified by the session info and filename.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//   filename: The filename of the artifact
	//   version: The version of the artifact. If nil, the latest version will be returned.
	//
	// Returns:
	//   The artifact or nil if not found.
	LoadArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (*Artifact, error)

	// ListArtifactKeys lists all the artifact filenames within a session.
	//
	// Args:
	//   ctx: The context for the operation
	//   sessionInfo: The session information (app name, user ID, session ID)
	//
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
