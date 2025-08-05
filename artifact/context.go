package artifact

import (
	"context"
	"errors"
)

// ContextWithService returns a copy of parent with service set as the current ArtifactService.
func ContextWithService(ctx context.Context, service Service) context.Context {
	return context.WithValue(ctx, artifactServiceKey{}, service)
}

// ServiceFromContext retrieves the artifact service from the context.
func ServiceFromContext(ctx context.Context) (Service, bool) {
	service, ok := ctx.Value(artifactServiceKey{}).(Service)
	return service, ok
}

type artifactServiceKey struct{}

// LoadArtifact loads an artifact attached to the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	userID: The user ID
//	sessionID: The session ID
//	filename: The filename of the artifact
//	version: The version of the artifact. If nil, the latest version will be returned.
//
// Returns:
//
//	The artifact, or nil if not found.
func LoadArtifact(ctx context.Context, appName, userID, sessionID, filename string, version *int) (*Artifact, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.LoadArtifact(ctx, appName, userID, sessionID, filename, version)
}

// SaveArtifact saves an artifact and records it for the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	userID: The user ID
//	sessionID: The session ID
//	filename: The filename of the artifact
//	artifact: The artifact to save
//
// Returns:
//
//	The version of the artifact.
func SaveArtifact(ctx context.Context, appName, userID, sessionID, filename string, artifact *Artifact) (int, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return 0, errors.New("artifact service is not initialized")
	}

	return service.SaveArtifact(ctx, appName, userID, sessionID, filename, artifact)
}

// ListArtifacts lists the filenames of the artifacts attached to the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	userID: The user ID
//	sessionID: The session ID
//
// Returns:
//
//	A list of artifact filenames.
func ListArtifacts(ctx context.Context, appName, userID, sessionID string) ([]string, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.ListArtifactKeys(ctx, appName, userID, sessionID)
}

// DeleteArtifact deletes an artifact from the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	userID: The user ID
//	sessionID: The session ID
//	filename: The filename of the artifact to delete
//
// Returns:
//
//	An error if the operation fails.
func DeleteArtifact(ctx context.Context, appName, userID, sessionID, filename string) error {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return errors.New("artifact service is not initialized")
	}

	return service.DeleteArtifact(ctx, appName, userID, sessionID, filename)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	userID: The user ID
//	sessionID: The session ID
//	filename: The filename of the artifact
//
// Returns:
//
//	A list of all available versions of the artifact.
func ListArtifactVersions(ctx context.Context, appName, userID, sessionID, filename string) ([]int, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.ListVersions(ctx, appName, userID, sessionID, filename)
}
