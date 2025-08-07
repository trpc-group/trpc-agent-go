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
//	sessionInfo: The session information (app name, user ID, session ID)
//	filename: The filename of the artifact
//	version: The version of the artifact. If nil, the latest version will be returned.
//
// Returns:
//
//	The artifact, or nil if not found.
func LoadArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (*Artifact, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.LoadArtifact(ctx, sessionInfo, filename, version)
}

// SaveArtifact saves an artifact and records it for the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	sessionInfo: The session information (app name, user ID, session ID)
//	filename: The filename of the artifact
//	artifact: The artifact to save
//
// Returns:
//
//	The version of the artifact.
func SaveArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, artifact *Artifact) (int, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return 0, errors.New("artifact service is not initialized")
	}

	return service.SaveArtifact(ctx, sessionInfo, filename, artifact)
}

// ListArtifacts lists the filenames of the artifacts attached to the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	sessionInfo: The session information (app name, user ID, session ID)
//
// Returns:
//
//	A list of artifact filenames.
func ListArtifacts(ctx context.Context, sessionInfo SessionInfo) ([]string, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.ListArtifactKeys(ctx, sessionInfo)
}

// DeleteArtifact deletes an artifact from the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	sessionInfo: The session information (app name, user ID, session ID)
//	filename: The filename of the artifact to delete
//
// Returns:
//
//	An error if the operation fails.
func DeleteArtifact(ctx context.Context, sessionInfo SessionInfo, filename string) error {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return errors.New("artifact service is not initialized")
	}

	return service.DeleteArtifact(ctx, sessionInfo, filename)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	sessionInfo: The session information (app name, user ID, session ID)
//	filename: The filename of the artifact
//
// Returns:
//
//	A list of all available versions of the artifact.
func ListArtifactVersions(ctx context.Context, sessionInfo SessionInfo, filename string) ([]int, error) {
	service, ok := ServiceFromContext(ctx)
	if !ok {
		return nil, errors.New("artifact service is not initialized")
	}

	return service.ListVersions(ctx, sessionInfo, filename)
}
