//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package artifact provides internal utilities for artifact implementations.
package artifact

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// FileHasUserNamespace checks if the filename has a user namespace.
// Files with user namespace start with "user:" prefix.
func FileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}

// BuildArtifactPath constructs the artifact path for storage.
// The path format depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     {app_name}/{user_id}/user/{filename}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}
func BuildArtifactPath(sessionInfo artifact.SessionInfo, filename string) string {
	if FileHasUserNamespace(filename) {
		return fmt.Sprintf("%s/%s/user/%s", sessionInfo.AppName, sessionInfo.UserID, filename)
	}
	return fmt.Sprintf("%s/%s/%s/%s", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
}

// BuildObjectName constructs the object name for versioned storage (like COS).
// The object name format depends on whether the filename has a user namespace:
//   - For files with user namespace (starting with "user:"):
//     {app_name}/{user_id}/user/{filename}/{version}
//   - For regular session-scoped files:
//     {app_name}/{user_id}/{session_id}/{filename}/{version}
func BuildObjectName(sessionInfo artifact.SessionInfo, filename string, version int) string {
	if FileHasUserNamespace(filename) {
		return fmt.Sprintf("%s/%s/user/%s/%d", sessionInfo.AppName, sessionInfo.UserID, filename, version)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename, version)
}

// BuildObjectNamePrefix constructs the object name prefix for listing versions.
// This is used to list all versions of a specific artifact.
func BuildObjectNamePrefix(sessionInfo artifact.SessionInfo, filename string) string {
	if FileHasUserNamespace(filename) {
		return fmt.Sprintf("%s/%s/user/%s/", sessionInfo.AppName, sessionInfo.UserID, filename)
	}
	return fmt.Sprintf("%s/%s/%s/%s/", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID, filename)
}

// BuildSessionPrefix constructs the prefix for session-scoped artifacts.
func BuildSessionPrefix(sessionInfo artifact.SessionInfo) string {
	return fmt.Sprintf("%s/%s/%s/", sessionInfo.AppName, sessionInfo.UserID, sessionInfo.SessionID)
}

// BuildUserNamespacePrefix constructs the prefix for user-namespaced artifacts.
func BuildUserNamespacePrefix(sessionInfo artifact.SessionInfo) string {
	return fmt.Sprintf("%s/%s/user/", sessionInfo.AppName, sessionInfo.UserID)
}
