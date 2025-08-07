//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package agent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TransferInfo contains information about a pending agent transfer.
type TransferInfo struct {
	// TargetAgentName is the name of the agent to transfer control to.
	TargetAgentName string
	// Message is the message to send to the target agent.
	Message string
	// EndInvocation indicates whether to end the current invocation after transfer.
	EndInvocation bool
}

// Invocation represents the context for a flow execution.
type Invocation struct {
	// Agent is the agent that is being invoked.
	Agent Agent
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// Branch is the branch identifier for hierarchical event filtering.
	Branch string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Session is the session that is being used for the invocation.
	Session *session.Session
	// ArtifactService is the service for managing artifacts.
	ArtifactService artifact.Service
	// Model is the model that is being used for the invocation.
	Model model.Model
	// Message is the message that is being sent to the agent.
	Message model.Message
	// EventCompletionCh is used to signal when events are written to session.
	EventCompletionCh <-chan string
	// RunOptions is the options for the Run method.
	RunOptions RunOptions
	// TransferInfo contains information about a pending agent transfer.
	TransferInfo *TransferInfo
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *Callbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.Callbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.Callbacks
}

type invocationKey struct{}

// NewContextWithInvocation creates a new context with the invocation.
func NewContextWithInvocation(ctx context.Context, invocation *Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocation)
}

// InvocationFromContext returns the invocation from the context.
func InvocationFromContext(ctx context.Context) (*Invocation, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(*Invocation)
	return invocation, ok
}

// RunOptions is the options for the Run method.
type RunOptions struct{}

// LoadArtifact loads an artifact attached to the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	filename: The filename of the artifact
//	version: The version of the artifact. If nil, the latest version will be returned.
//
// Returns:
//
//	The artifact, or nil if not found.
func LoadArtifact(ctx context.Context, filename string, version *int) (*artifact.Artifact, error) {
	service, sessionInfo, err := getArtifactServiceAndSessionInfo(ctx)
	if err != nil {
		return nil, err
	}
	return service.LoadArtifact(ctx, sessionInfo, filename, version)
}

// SaveArtifact saves an artifact and records it for the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	filename: The filename of the artifact
//	artifact: The artifact to save
//
// Returns:
//
//	The version of the artifact.
func SaveArtifact(ctx context.Context, filename string, artifact *artifact.Artifact) (int, error) {
	service, sessionInfo, err := getArtifactServiceAndSessionInfo(ctx)
	if err != nil {
		return 0, err
	}
	return service.SaveArtifact(ctx, sessionInfo, filename, artifact)
}

// ListArtifacts lists the filenames of the artifacts attached to the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//
// Returns:
//
//	A list of artifact filenames.
func ListArtifacts(ctx context.Context) ([]string, error) {
	service, sessionInfo, err := getArtifactServiceAndSessionInfo(ctx)
	if err != nil {
		return nil, err
	}
	return service.ListArtifactKeys(ctx, sessionInfo)
}

// DeleteArtifact deletes an artifact from the current session.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	appName: The name of the application
//	filename: The filename of the artifact to delete
//
// Returns:
//
//	An error if the operation fails.
func DeleteArtifact(ctx context.Context, filename string) error {
	service, sessionInfo, err := getArtifactServiceAndSessionInfo(ctx)
	if err != nil {
		return err
	}
	return service.DeleteArtifact(ctx, sessionInfo, filename)
}

// ListArtifactVersions lists all versions of an artifact.
//
// Args:
//
//	ctx: The context containing the artifact service and session information
//	filename: The filename of the artifact
//
// Returns:
//
//	A list of all available versions of the artifact.
func ListArtifactVersions(ctx context.Context, filename string) ([]int, error) {
	service, sessionInfo, err := getArtifactServiceAndSessionInfo(ctx)
	if err != nil {
		return nil, err
	}
	return service.ListVersions(ctx, sessionInfo, filename)
}

// getArtifactServiceAndSessionInfo extracts common logic for getting artifact service and session information.
func getArtifactServiceAndSessionInfo(ctx context.Context) (s artifact.Service, sessionInfo artifact.SessionInfo, err error) {
	invocation, ok := InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, artifact.SessionInfo{}, errors.New("invocation is nil or not found in context")
	}

	service := invocation.ArtifactService
	if service == nil {
		return nil, artifact.SessionInfo{}, errors.New("artifact service is nil in invocation")
	}

	appName, userID, sessionID, err := appUserSession(invocation)
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

func appUserSession(invocation *Invocation) (appName, userID, sessionID string, err error) {
	// Try to get from session.
	if invocation.Session == nil {
		return "", "", "", errors.New("invocation exists but no session available")
	}

	// Session has AppName and UserID fields.
	if invocation.Session.AppName != "" && invocation.Session.UserID != "" && invocation.Session.ID != "" {
		return invocation.Session.AppName, invocation.Session.UserID, invocation.Session.ID, nil
	}

	// Return error if session exists but missing required fields.
	return "", "", "", fmt.Errorf("session exists but missing appName or userID or sessionID: appName=%s, userID=%s, sessionID=%s",
		invocation.Session.AppName, invocation.Session.UserID, invocation.Session.ID)
}
