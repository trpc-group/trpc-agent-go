//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// CallbackContext provides a typed wrapper around context with agent-specific operations.
// Similar to ADK Python's callback_context, this provides access to session-scoped operations
// like artifact management.
type CallbackContext struct {
	context.Context
	invocation *Invocation
	// State is the delta-aware state of the current session.
	State session.StateMap
}

// NewCallbackContext creates a CallbackContext from a standard context.
// Returns an error if no invocation is found in the context.
func NewCallbackContext(ctx context.Context) (*CallbackContext, error) {
	invocation, ok := InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, errors.New("invocation not found in context")
	}
	var state = make(session.StateMap)
	if invocation.Session != nil && invocation.Session.State != nil {
		state = invocation.Session.State
	}
	return &CallbackContext{
		Context:    ctx,
		invocation: invocation,
		State:      state,
	}, nil
}

// PutArtifact stores a new version of the artifact content and returns its metadata.
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) PutArtifact(req *artifact.PutRequest, opts ...artifact.PutOption) (*artifact.PutResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("put artifact request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.Put(cc.Context, &r, opts...)
}

// HeadArtifact resolves an artifact version to its metadata and an optional URL.
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) HeadArtifact(req *artifact.HeadRequest, opts ...artifact.HeadOption) (*artifact.HeadResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("head artifact request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.Head(cc.Context, &r, opts...)
}

// OpenArtifact opens a streaming reader for an artifact version.
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) OpenArtifact(req *artifact.OpenRequest, opts ...artifact.OpenOption) (*artifact.OpenResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("open artifact request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.Open(cc.Context, &r, opts...)
}

// ListArtifacts lists artifacts within the current session scope.
//
// It returns descriptors for the latest version of each artifact and a nextPageToken.
// nextPageToken is empty when there are no more results.
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) ListArtifacts(req *artifact.ListRequest, opts ...artifact.ListOption) (*artifact.ListResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("list artifacts request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.List(cc.Context, &r, opts...)
}

// DeleteArtifact deletes artifact versions identified by name within the current session scope.
//
// When version is nil, it deletes all versions.
// When version is non-nil, it deletes the specified version.
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) DeleteArtifact(req *artifact.DeleteRequest, opts ...artifact.DeleteOption) (*artifact.DeleteResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("delete artifact request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.Delete(cc.Context, &r, opts...)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//   - name: The name of the artifact
//
// Returns:
//   - A list of all available versions of the artifact
//
// The provided request's AppName/UserID/SessionID are ignored and filled from the invocation session.
func (cc *CallbackContext) ListArtifactVersions(req *artifact.VersionsRequest, opts ...artifact.VersionsOption) (*artifact.VersionsResponse, error) {
	service, appName, userID, sessionID, err := cc.getArtifactServiceAndBase()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, errors.New("list artifact versions request is nil")
	}
	r := *req
	r.AppName = appName
	r.UserID = userID
	r.SessionID = sessionID
	return service.Versions(cc.Context, &r, opts...)
}

// getArtifactServiceAndBase extracts common logic for getting artifact service and the session base namespace.
func (cc *CallbackContext) getArtifactServiceAndBase() (s artifact.Service, appName, userID, sessionID string, err error) {
	service := cc.invocation.ArtifactService
	if service == nil {
		return nil, "", "", "", errors.New("artifact service is nil in invocation")
	}

	a, u, sid, err := cc.appUserSession()
	if err != nil {
		return nil, "", "", "", err
	}

	return service, a, u, sid, nil
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
