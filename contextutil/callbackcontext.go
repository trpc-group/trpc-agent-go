// Package contextutil provides utilities for working with context.Context.
package contextutil

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// CallbackContext provides a typed wrapper around context with agent-specific operations.
// Similar to ADK Python's callback_context, this provides access to session-scoped operations
// like artifact management.
type CallbackContext struct {
	ctx        context.Context
	invocation *agent.Invocation
}

// NewCallbackContext creates a CallbackContext from a standard context.
// Returns an error if no invocation is found in the context.
func NewCallbackContext(ctx context.Context) (*CallbackContext, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, errors.New("invocation not found in context")
	}
	return &CallbackContext{
		ctx:        ctx,
		invocation: invocation,
	}, nil
}

// Context returns the underlying context.
func (cc *CallbackContext) Context() context.Context {
	return cc.ctx
}

// SaveArtifact saves an artifact and records it for the current session.
//
// Args:
//   - filename: The filename of the artifact
//   - artifact: The artifact to save
//
// Returns:
//   - The version of the artifact
func (cc *CallbackContext) SaveArtifact(filename string, artifact *artifact.Artifact) (int, error) {
	service, sessionInfo, err := cc.getArtifactServiceAndSessionInfo()
	if err != nil {
		return 0, err
	}
	return service.SaveArtifact(cc.ctx, sessionInfo, filename, artifact)
}

// LoadArtifact loads an artifact attached to the current session.
//
// Args:
//   - filename: The filename of the artifact
//   - version: The version of the artifact. If nil, the latest version will be returned.
//
// Returns:
//   - The artifact, or nil if not found
func (cc *CallbackContext) LoadArtifact(filename string, version *int) (*artifact.Artifact, error) {
	service, sessionInfo, err := cc.getArtifactServiceAndSessionInfo()
	if err != nil {
		return nil, err
	}
	return service.LoadArtifact(cc.ctx, sessionInfo, filename, version)
}

// ListArtifacts lists the filenames of the artifacts attached to the current session.
//
// Returns:
//   - A list of artifact filenames
func (cc *CallbackContext) ListArtifacts() ([]string, error) {
	service, sessionInfo, err := cc.getArtifactServiceAndSessionInfo()
	if err != nil {
		return nil, err
	}
	return service.ListArtifactKeys(cc.ctx, sessionInfo)
}

// DeleteArtifact deletes an artifact from the current session.
//
// Args:
//   - filename: The filename of the artifact to delete
//
// Returns:
//   - An error if the operation fails
func (cc *CallbackContext) DeleteArtifact(filename string) error {
	service, sessionInfo, err := cc.getArtifactServiceAndSessionInfo()
	if err != nil {
		return err
	}
	return service.DeleteArtifact(cc.ctx, sessionInfo, filename)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//   - filename: The filename of the artifact
//
// Returns:
//   - A list of all available versions of the artifact
func (cc *CallbackContext) ListArtifactVersions(filename string) ([]int, error) {
	service, sessionInfo, err := cc.getArtifactServiceAndSessionInfo()
	if err != nil {
		return nil, err
	}
	return service.ListVersions(cc.ctx, sessionInfo, filename)
}

// getArtifactServiceAndSessionInfo extracts common logic for getting artifact service and session information.
func (cc *CallbackContext) getArtifactServiceAndSessionInfo() (s artifact.Service, sessionInfo artifact.SessionInfo, err error) {
	service := cc.invocation.ArtifactService
	if service == nil {
		return nil, artifact.SessionInfo{}, errors.New("artifact service is nil in invocation")
	}

	appName, userID, sessionID, err := cc.appUserSession()
	if err != nil {
		return nil, artifact.SessionInfo{}, err
	}

	sessionInfo = artifact.SessionInfo{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}

	return service, sessionInfo, nil
}

// appUserSession extracts app name, user ID, and session ID from the invocation.
func (cc *CallbackContext) appUserSession() (appName, userID, sessionID string, err error) {
	// Try to get from session.
	if cc.invocation.Session == nil {
		return "", "", "", errors.New("invocation exists but no session available")
	}

	// Session has AppName and UserID fields.
	if cc.invocation.Session.AppName != "" && cc.invocation.Session.UserID != "" && cc.invocation.Session.ID != "" {
		return cc.invocation.Session.AppName, cc.invocation.Session.UserID, cc.invocation.Session.ID, nil
	}

	// Return error if session exists but missing required fields.
	return "", "", "", fmt.Errorf("session exists but missing appName or userID or sessionID: appName=%s, userID=%s, sessionID=%s",
		cc.invocation.Session.AppName, cc.invocation.Session.UserID, cc.invocation.Session.ID)
}
