package artifact

import (
	"context"
)

// Service defines the interface for artifact storage and retrieval operations.
// This corresponds to BaseArtifactService in the Python implementation.
type Service interface {
	// SaveArtifact saves an artifact to the artifact service storage.
	//
	// The artifact is a file identified by the app name, user ID, session ID, and
	// filename. After saving the artifact, a revision ID is returned to identify
	// the artifact version.
	//
	// Args:
	//   ctx: The context for the operation
	//   appName: The app name
	//   userID: The user ID
	//   sessionID: The session ID
	//   filename: The filename of the artifact
	//   artifact: The artifact to save
	//
	// Returns:
	//   The revision ID. The first version of the artifact has a revision ID of 0.
	//   This is incremented by 1 after each successful save.
	SaveArtifact(ctx context.Context, appName, userID, sessionID, filename string, artifact *Artifact) (int, error)

	// LoadArtifact gets an artifact from the artifact service storage.
	//
	// The artifact is a file identified by the app name, user ID, session ID, and
	// filename.
	//
	// Args:
	//   ctx: The context for the operation
	//   appName: The app name
	//   userID: The user ID
	//   sessionID: The session ID
	//   filename: The filename of the artifact
	//   version: The version of the artifact. If nil, the latest version will be returned.
	//
	// Returns:
	//   The artifact or nil if not found.
	LoadArtifact(ctx context.Context, appName, userID, sessionID, filename string, version *int) (*Artifact, error)

	// ListArtifactKeys lists all the artifact filenames within a session.
	//
	// Args:
	//   ctx: The context for the operation
	//   appName: The name of the application
	//   userID: The ID of the user
	//   sessionID: The ID of the session
	//
	// Returns:
	//   A list of all artifact filenames within a session.
	ListArtifactKeys(ctx context.Context, appName, userID, sessionID string) ([]string, error)

	// DeleteArtifact deletes an artifact.
	//
	// Args:
	//   ctx: The context for the operation
	//   appName: The name of the application
	//   userID: The ID of the user
	//   sessionID: The ID of the session
	//   filename: The name of the artifact file
	DeleteArtifact(ctx context.Context, appName, userID, sessionID, filename string) error

	// ListVersions lists all versions of an artifact.
	//
	// Args:
	//   ctx: The context for the operation
	//   appName: The name of the application
	//   userID: The ID of the user
	//   sessionID: The ID of the session
	//   filename: The name of the artifact file
	//
	// Returns:
	//   A list of all available versions of the artifact.
	ListVersions(ctx context.Context, appName, userID, sessionID, filename string) ([]int, error)
}
