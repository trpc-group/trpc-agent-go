//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package workspacefacade hosts implementation primitives shared by the
// codeexecutor/workspaceio facade and the tool/workspaceexec LLM tools.
// Nothing in this package is part of the public API.
package workspacefacade

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// HasGlobMeta reports whether s contains any glob metacharacter.
func HasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// IsWorkspaceEnvPath reports whether s starts with a recognized
// workspace env-prefixed path such as $WORK_DIR/... or
// ${SKILLS_DIR}/....
func IsWorkspaceEnvPath(s string) bool {
	return hasEnvPrefix(s, codeexecutor.WorkspaceEnvDirKey) ||
		hasEnvPrefix(s, codeexecutor.EnvSkillsDir) ||
		hasEnvPrefix(s, codeexecutor.EnvWorkDir) ||
		hasEnvPrefix(s, codeexecutor.EnvOutputDir) ||
		hasEnvPrefix(s, codeexecutor.EnvRunDir)
}

// NormalizeArtifactPath validates a single-file path used by artifact
// publishing entry points (workspace_save_artifact LLM tool and
// Workspace.SaveArtifact). Globs and parent traversal are rejected.
// The returned path is always workspace-relative, clean, and confirmed
// to live under one of the supported publish roots (work/, out/,
// runs/).
func NormalizeArtifactPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" {
		return "", errors.New("path is required")
	}
	if HasGlobMeta(s) {
		return "", errors.New("path must not contain glob patterns")
	}
	if IsWorkspaceEnvPath(s) {
		out := codeexecutor.NormalizeGlobs([]string{s})
		if len(out) == 0 {
			return "", errors.New("invalid path")
		}
		s = out[0]
	}
	if strings.HasPrefix(s, "/") {
		rel := strings.TrimPrefix(path.Clean(s), "/")
		if rel == "" || rel == "." {
			return "", errors.New("path must point to a file inside the workspace")
		}
		if !IsAllowedPublishArtifactPath(rel) {
			return "", fmt.Errorf(
				"path must stay under supported artifact roots such as work/, out/, or runs/: %q",
				raw,
			)
		}
		return rel, nil
	}
	rel := path.Clean(s)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("path must stay within the workspace")
	}
	if !IsAllowedPublishArtifactPath(rel) {
		return "", fmt.Errorf(
			"path must stay under supported artifact roots such as work/, out/, or runs/: %q",
			raw,
		)
	}
	return rel, nil
}

// IsAllowedPublishArtifactPath reports whether rel resolves to a
// workspace path under work/, out/, or runs/.
func IsAllowedPublishArtifactPath(rel string) bool {
	switch {
	case rel == codeexecutor.DirWork || strings.HasPrefix(rel, codeexecutor.DirWork+"/"):
		return true
	case rel == codeexecutor.DirOut || strings.HasPrefix(rel, codeexecutor.DirOut+"/"):
		return true
	case rel == codeexecutor.DirRuns || strings.HasPrefix(rel, codeexecutor.DirRuns+"/"):
		return true
	default:
		return false
	}
}

const (
	envVarPrefix = "$"
	envVarLBrace = "${"
	envVarRBrace = "}"
)

// hasEnvPrefix reports whether s starts with an env reference such as
// $name or ${name} followed by either nothing or a path separator.
func hasEnvPrefix(s, name string) bool {
	if strings.HasPrefix(s, envVarPrefix+name) {
		tail := s[len(envVarPrefix+name):]
		return tail == "" || strings.HasPrefix(tail, "/") || strings.HasPrefix(tail, "\\")
	}
	prefix := envVarLBrace + name + envVarRBrace
	if strings.HasPrefix(s, prefix) {
		tail := s[len(prefix):]
		return tail == "" || strings.HasPrefix(tail, "/") || strings.HasPrefix(tail, "\\")
	}
	return false
}
