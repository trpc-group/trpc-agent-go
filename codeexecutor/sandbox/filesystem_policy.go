//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// FileSystemAccess describes a filesystem rule's access mode.
type FileSystemAccess string

const (
	// AccessRead grants read-only access.
	AccessRead FileSystemAccess = "read"
	// AccessWrite grants read and write access.
	AccessWrite FileSystemAccess = "write"
	// AccessNone denies both reads and writes.
	AccessNone FileSystemAccess = "none"
)

// FileSystemRuleKind describes how a filesystem rule target is interpreted.
type FileSystemRuleKind string

const (
	// RulePath targets a concrete path. Relative paths are workspace-relative;
	// absolute paths are host paths.
	RulePath FileSystemRuleKind = "path"
	// RuleSpecial targets a well-known sandbox path.
	RuleSpecial FileSystemRuleKind = "special"
	// RuleGlob targets a workspace-relative glob.
	RuleGlob FileSystemRuleKind = "glob"
)

// SpecialPath identifies well-known session-scoped directories.
type SpecialPath string

const (
	// SpecialRoot matches the whole sandbox workspace.
	SpecialRoot SpecialPath = "root"
	// SpecialWorkspace matches the session workspace directory.
	SpecialWorkspace SpecialPath = "workspace"
	// SpecialWork matches the workspace work directory.
	SpecialWork SpecialPath = "work"
	// SpecialHome matches the workspace home directory.
	SpecialHome SpecialPath = "home"
	// SpecialTmp matches the workspace tmp directory.
	SpecialTmp SpecialPath = "tmp"
	// SpecialRuns matches the workspace runs directory.
	SpecialRuns SpecialPath = "runs"
	// SpecialOut matches the workspace output directory.
	SpecialOut SpecialPath = "out"
	// SpecialSkills matches the workspace skills directory.
	SpecialSkills SpecialPath = "skills"
)

// FileSystemRule declares one filesystem access rule.
type FileSystemRule struct {
	Kind    FileSystemRuleKind
	Access  FileSystemAccess
	Path    string
	Special SpecialPath
	Glob    string
}

// FileSystemPolicy is the filesystem portion of a PermissionProfile.
type FileSystemPolicy struct {
	Rules             []FileSystemRule
	ProtectedMetadata []string
}
