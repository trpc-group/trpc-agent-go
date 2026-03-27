//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package memory contains internal helpers for resolving run-scoped memory users.
package memory

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// Session state keys keep the existing ":" namespace used by persisted
	// memory-related session data.
	sessionStateKeyUserID = "memory:user_id"
	// Runtime state keys use "." to match other run-scoped option keys merged
	// into agent.RunOptions.RuntimeState.
	runtimeStateKeyUserID = "memory.user_id"
)

type autoMemoryCursorSessionContextKey struct{}

// RuntimeState returns runtime state for one run-scoped memory user override.
func RuntimeState(userID string) map[string]any {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	return map[string]any{
		runtimeStateKeyUserID: userID,
	}
}

// ContextWithAutoMemoryCursorSession attaches the session whose state should
// store incremental auto-memory extraction markers. This lets auto memory use
// a cloned session for run-scoped user resolution while persisting progress on
// the original session.
func ContextWithAutoMemoryCursorSession(
	ctx context.Context,
	sess *session.Session,
) context.Context {
	if sess == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, autoMemoryCursorSessionContextKey{}, sess)
}

// AutoMemoryCursorSessionFromContext resolves the session whose state should be
// used for incremental auto-memory extraction markers.
func AutoMemoryCursorSessionFromContext(
	ctx context.Context,
) (*session.Session, bool) {
	if ctx == nil {
		return nil, false
	}
	sess, ok := ctx.Value(autoMemoryCursorSessionContextKey{}).(*session.Session)
	if !ok || sess == nil {
		return nil, false
	}
	return sess, true
}

func userIDFromRuntimeState(state map[string]any) (string, bool) {
	if len(state) == 0 {
		return "", false
	}
	value, ok := state[runtimeStateKeyUserID]
	if !ok {
		return "", false
	}
	userID, ok := value.(string)
	if !ok {
		return "", false
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", false
	}
	return userID, true
}

func userIDFromSessionState(sess *session.Session) (string, bool) {
	if sess == nil {
		return "", false
	}
	raw, ok := sess.GetState(sessionStateKeyUserID)
	if !ok {
		return "", false
	}
	userID := strings.TrimSpace(string(raw))
	if userID == "" {
		return "", false
	}
	return userID, true
}

// ResolveUserID resolves the effective memory user for the current run.
//
// Resolution priority is:
// 1. Runtime state override from RunOptions.RuntimeState.
// 2. Cloned-session state override used by auto memory.
// 3. sess.UserID.
func ResolveUserID(
	sess *session.Session,
	runtimeState map[string]any,
) (string, bool) {
	if userID, ok := userIDFromRuntimeState(runtimeState); ok {
		return userID, true
	}
	if userID, ok := userIDFromSessionState(sess); ok {
		return userID, true
	}
	if sess == nil {
		return "", false
	}
	userID := strings.TrimSpace(sess.UserID)
	if userID == "" {
		return "", false
	}
	return userID, true
}

// ResolveUserKey resolves the effective memory app and user for the current run.
func ResolveUserKey(
	sess *session.Session,
	runtimeState map[string]any,
) (string, string, bool) {
	if sess == nil {
		return "", "", false
	}
	appName := strings.TrimSpace(sess.AppName)
	if appName == "" {
		return "", "", false
	}
	userID, ok := ResolveUserID(sess, runtimeState)
	if !ok {
		return "", "", false
	}
	return appName, userID, true
}

// CloneSessionWithRuntimeState clones the session and carries the run-scoped
// memory user override via session state for downstream components that only
// receive a session pointer (for example, auto memory workers).
func CloneSessionWithRuntimeState(
	sess *session.Session,
	runtimeState map[string]any,
) *session.Session {
	if sess == nil {
		return nil
	}
	userID, ok := userIDFromRuntimeState(runtimeState)
	if !ok {
		return sess
	}
	cloned := sess.Clone()
	cloned.SetState(sessionStateKeyUserID, []byte(userID))
	return cloned
}
