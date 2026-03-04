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
	"io"

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

// PutArtifact stores a new version of the artifact content and returns its descriptor.
func (cc *CallbackContext) PutArtifact(name string, r io.Reader, opts ...artifact.PutOption) (artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if r == nil {
		return artifact.Descriptor{}, errors.New("artifact reader is nil")
	}
	return service.Put(cc.Context, withName(baseKey, name), r, opts...)
}

// HeadArtifact resolves an artifact version to its metadata and an optional URL.
func (cc *CallbackContext) HeadArtifact(name string, version *artifact.VersionID) (artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return service.Head(cc.Context, withName(baseKey, name), version)
}

// OpenArtifact opens a streaming reader for an artifact version.
func (cc *CallbackContext) OpenArtifact(name string, version *artifact.VersionID) (io.ReadCloser, artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	return service.Open(cc.Context, withName(baseKey, name), version)
}

// ListArtifacts lists artifacts within the current session scope.
//
// It returns descriptors for the latest version of each artifact and a nextPageToken.
// nextPageToken is empty when there are no more results.
func (cc *CallbackContext) ListArtifacts(namePrefix string, opts ...artifact.ListOption) ([]artifact.Descriptor, string, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, "", err
	}
	return service.List(cc.Context, artifact.KeyPrefix{
		AppName:    baseKey.AppName,
		UserID:     baseKey.UserID,
		SessionID:  baseKey.SessionID,
		Scope:      baseKey.Scope,
		NamePrefix: namePrefix,
	}, opts...)
}

// DeleteArtifact deletes artifact versions identified by name within the current session scope.
//
// By default (no opts), it deletes all versions.
func (cc *CallbackContext) DeleteArtifact(name string, opts ...artifact.DeleteOption) error {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return err
	}
	return service.Delete(cc.Context, withName(baseKey, name), opts...)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//   - name: The name of the artifact
//
// Returns:
//   - A list of all available versions of the artifact
func (cc *CallbackContext) ListArtifactVersions(name string) ([]artifact.VersionID, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, err
	}
	return service.Versions(cc.Context, withName(baseKey, name))
}

// getArtifactServiceAndBaseKey extracts common logic for getting artifact service and the session base key.
func (cc *CallbackContext) getArtifactServiceAndBaseKey() (s artifact.Service, baseKey artifact.Key, err error) {
	service := cc.invocation.ArtifactService
	if service == nil {
		return nil, artifact.Key{}, errors.New("artifact service is nil in invocation")
	}

	appName, userID, sessionID, err := cc.appUserSession()
	if err != nil {
		return nil, artifact.Key{}, err
	}

	return service, artifact.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Scope:     artifact.ScopeSession,
	}, nil
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

func withName(base artifact.Key, name string) artifact.Key {
	base.Name = name
	return base
}
