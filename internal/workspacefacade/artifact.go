//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspacefacade

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// DefaultArtifactMaxBytes is the default per-file cap for artifact
// publishing flows when the caller does not provide one.
const DefaultArtifactMaxBytes int64 = 64 * 1024 * 1024

// Reasons returned by ArtifactSaveSkipReason; an empty string means
// the current invocation can persist artifacts.
const (
	SaveReasonNoInvocation = "invocation is missing from context"
	SaveReasonNoService    = "artifact service is not configured"
	SaveReasonNoSession    = "session is missing from invocation"
	SaveReasonNoSessionIDs = "session app/user/session IDs are missing"
)

// ArtifactSaveSkipReason returns a non-empty string explaining why the
// current invocation cannot persist artifacts, or "" when persistence
// is supported.
func ArtifactSaveSkipReason(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return SaveReasonNoInvocation
	}
	if inv.ArtifactService == nil {
		return SaveReasonNoService
	}
	if inv.Session == nil {
		return SaveReasonNoSession
	}
	if inv.Session.AppName == "" || inv.Session.UserID == "" || inv.Session.ID == "" {
		return SaveReasonNoSessionIDs
	}
	return ""
}

// WithArtifactContext copies the invocation's artifact service and
// session info onto ctx so codeexecutor backends can persist files.
// When the invocation does not carry that info, ctx is returned as-is.
func WithArtifactContext(ctx context.Context) context.Context {
	ctxIO := ctx
	if inv, ok := agent.InvocationFromContext(ctx); ok &&
		inv != nil && inv.ArtifactService != nil &&
		inv.Session != nil {
		ctxIO = codeexecutor.WithArtifactService(ctxIO, inv.ArtifactService)
		ctxIO = codeexecutor.WithArtifactSession(ctxIO, artifact.SessionInfo{
			AppName:   inv.Session.AppName,
			UserID:    inv.Session.UserID,
			SessionID: inv.Session.ID,
		})
	}
	return ctxIO
}
