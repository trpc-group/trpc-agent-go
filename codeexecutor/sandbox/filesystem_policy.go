//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// fileSystemAccess describes a filesystem rule's access mode.
type fileSystemAccess string

const (
	// accessRead grants read-only access.
	accessRead fileSystemAccess = "read"
	// accessWrite grants read and write access.
	accessWrite fileSystemAccess = "write"
	// accessNone denies both reads and writes.
	accessNone fileSystemAccess = "none"
)

// fileSystemRuleKind describes how a filesystem rule target is interpreted.
type fileSystemRuleKind string

const (
	// rulePath targets a concrete path. Relative paths are workspace-relative;
	// absolute paths are host paths.
	rulePath fileSystemRuleKind = "path"
	// ruleSpecial targets a well-known sandbox path.
	ruleSpecial fileSystemRuleKind = "special"
	// ruleGlob targets a workspace-relative glob.
	ruleGlob fileSystemRuleKind = "glob"
)

// specialPath identifies well-known session-scoped directories.
type specialPath string

const (
	// specialRoot matches the whole sandbox workspace.
	specialRoot specialPath = "root"
	// specialWorkspace matches the session workspace directory.
	specialWorkspace specialPath = "workspace"
	// specialWork matches the workspace work directory.
	specialWork specialPath = "work"
	// specialHome matches the workspace home directory.
	specialHome specialPath = "home"
	// specialTmp matches the workspace tmp directory.
	specialTmp specialPath = "tmp"
	// specialRuns matches the workspace runs directory.
	specialRuns specialPath = "runs"
	// specialOut matches the workspace output directory.
	specialOut specialPath = "out"
	// specialSkills matches the workspace skills directory.
	specialSkills specialPath = "skills"
)

// fileSystemRule declares one filesystem access rule.
type fileSystemRule struct {
	Kind    fileSystemRuleKind
	Access  fileSystemAccess
	Path    string
	Special specialPath
	Glob    string
}

// fileSystemPolicy is the filesystem portion of a PermissionProfile.
type fileSystemPolicy struct {
	Rules             []fileSystemRule
	ProtectedMetadata []string
}
