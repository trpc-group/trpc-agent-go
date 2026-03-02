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
	"bytes"
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

// SaveArtifact saves an artifact and records it for the current session.
//
// Args:
//   - filename: The filename of the artifact
//   - artifact: The artifact to save
//
// Returns:
//   - The version of the artifact
func (cc *CallbackContext) SaveArtifact(filename string, art *artifact.Artifact) (artifact.VersionID, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return "", err
	}
	if art == nil {
		return "", errors.New("artifact is nil")
	}
	desc, err := service.Put(
		cc.Context,
		withName(baseKey, filename),
		bytesReader(art.Data),
		artifact.WithPutMimeType(art.MimeType),
	)
	if err != nil {
		return "", err
	}
	return desc.Version, nil
}

// ResolveArtifact resolves artifact metadata and an optional URL.
func (cc *CallbackContext) ResolveArtifact(filename string, version *artifact.VersionID) (artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return service.Head(cc.Context, withName(baseKey, filename), version)
}

// LoadArtifact opens a streaming reader for an artifact.
func (cc *CallbackContext) LoadArtifact(filename string, version *artifact.VersionID) (io.ReadCloser, artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	return service.Open(cc.Context, withName(baseKey, filename), version)
}

// LoadArtifactBytes loads an artifact into memory and returns its bytes.
func (cc *CallbackContext) LoadArtifactBytes(filename string, version *artifact.VersionID) ([]byte, artifact.Descriptor, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	return artifact.ReadAll(cc.Context, service, withName(baseKey, filename), version)
}

// ListArtifacts lists the filenames of the artifacts attached to the current session.
//
// Returns:
//   - A list of artifact filenames
func (cc *CallbackContext) ListArtifacts() ([]string, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, err
	}
	var (
		out       []string
		pageToken string
	)
	for {
		items, next, err := service.List(cc.Context, artifact.KeyPrefix{
			AppName:    baseKey.AppName,
			UserID:     baseKey.UserID,
			SessionID:  baseKey.SessionID,
			Scope:      baseKey.Scope,
			NamePrefix: "",
		}, artifact.WithListLimit(200), artifact.WithListPageToken(pageToken))
		if err != nil {
			return nil, err
		}
		for _, d := range items {
			out = append(out, d.Key.Name)
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	return out, nil
}

// DeleteArtifact deletes an artifact from the current session.
//
// Args:
//   - filename: The filename of the artifact to delete
//
// Returns:
//   - An error if the operation fails
func (cc *CallbackContext) DeleteArtifact(filename string) error {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return err
	}
	return service.Delete(cc.Context, withName(baseKey, filename), artifact.DeleteAllOpt())
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//   - filename: The filename of the artifact
//
// Returns:
//   - A list of all available versions of the artifact
func (cc *CallbackContext) ListArtifactVersions(filename string) ([]artifact.VersionID, error) {
	service, baseKey, err := cc.getArtifactServiceAndBaseKey()
	if err != nil {
		return nil, err
	}
	return service.Versions(cc.Context, withName(baseKey, filename))
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

func bytesReader(b []byte) io.Reader {
	if len(b) == 0 {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(b)
}
