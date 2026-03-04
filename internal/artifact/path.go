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

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// BuildArtifactPath constructs the artifact path for storage.
//
// Built-in implementations use SessionID presence to select the namespace:
//   - If SessionID is empty:
//     {app_name}/{user_id}/user/{name}
//   - If SessionID is not empty:
//     {app_name}/{user_id}/{session_id}/{name}
func BuildArtifactPath(key artifact.Key) string {
	if key.SessionID == "" {
		return fmt.Sprintf("%s/%s/user/%s", key.AppName, key.UserID, key.Name)
	}
	return fmt.Sprintf("%s/%s/%s/%s", key.AppName, key.UserID, key.SessionID, key.Name)
}

// BuildObjectName constructs the object name for versioned storage (like COS).
// The object name format is:
//
//	{artifact_path}/{version_id}
func BuildObjectName(key artifact.Key, version artifact.VersionID) string {
	return fmt.Sprintf("%s/%s", BuildArtifactPath(key), version)
}

// BuildObjectNamePrefix constructs the object name prefix for listing versions.
// This is used to list all versions of a specific artifact.
func BuildObjectNamePrefix(key artifact.Key) string {
	return fmt.Sprintf("%s/", BuildArtifactPath(key))
}

// BuildSessionPrefix constructs the prefix for session-scoped artifacts.
func BuildSessionPrefix(appName, userID, sessionID string) string {
	return fmt.Sprintf("%s/%s/%s/", appName, userID, sessionID)
}

// BuildUserNamespacePrefix constructs the prefix for user-namespaced artifacts.
func BuildUserNamespacePrefix(appName, userID string) string {
	return fmt.Sprintf("%s/%s/user/", appName, userID)
}

// BuildListPrefix builds the object prefix for listing artifacts under a namespace key.
//
// key.Name is ignored.
func BuildListPrefix(key artifact.Key) string {
	if key.SessionID == "" {
		return fmt.Sprintf("%s/%s/user/", key.AppName, key.UserID)
	}
	return fmt.Sprintf("%s/%s/%s/", key.AppName, key.UserID, key.SessionID)
}
