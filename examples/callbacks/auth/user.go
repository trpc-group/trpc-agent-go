//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "slices"

// UserContext represents the user information and permissions.
type UserContext struct {
	UserID      string   `json:"user_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// Role definitions.
const (
	roleAdmin = "admin"
	roleUser  = "user"
	roleGuest = "guest"
)

// Permission definitions.
const (
	permissionRead   = "read"
	permissionWrite  = "write"
	permissionDelete = "delete"
	permissionList   = "list"
)

// Tool name definitions.
const (
	toolReadFile   = "read_file"
	toolWriteFile  = "write_file"
	toolDeleteFile = "delete_file"
	toolListFiles  = "list_files"
)

// Tool permission requirements.
var toolPermissions = map[string][]string{
	toolReadFile:   {permissionRead},
	toolWriteFile:  {permissionWrite},
	toolDeleteFile: {permissionDelete},
	toolListFiles:  {permissionList},
}

// getPermissionsForRole returns the permissions for a given role.
func getPermissionsForRole(role string) []string {
	switch role {
	case roleAdmin:
		return []string{permissionRead, permissionWrite, permissionDelete, permissionList}
	case roleUser:
		return []string{permissionRead, permissionWrite, permissionList}
	case roleGuest:
		return []string{permissionRead, permissionList}
	default:
		return []string{}
	}
}

// hasPermission checks if the user has the required permission for a tool.
func hasPermission(userCtx *UserContext, toolName string) bool {
	if userCtx == nil {
		return false
	}

	// Get required permissions for the tool.
	requiredPerms, ok := toolPermissions[toolName]
	if !ok {
		// Tool not found in permission map, allow by default.
		return true
	}

	// Check if user has all required permissions.
	for _, requiredPerm := range requiredPerms {
		found := slices.Contains(userCtx.Permissions, requiredPerm)
		if !found {
			return false
		}
	}

	return true
}

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	Timestamp string `json:"timestamp"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	ToolName  string `json:"tool_name"`
	Args      string `json:"args,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}
