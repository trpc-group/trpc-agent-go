//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os"
	"path/filepath"
)

// defaultCredentialDenyPaths returns host credential paths that Linux profiles
// mask when visible. An exact path grant reopens a path; a parent or child
// grant does not unmask the whole directory.
func defaultCredentialDenyPaths() []string {
	paths := []string{
		"/etc/shadow",
		"/etc/gshadow",
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return paths
	}
	return append(paths,
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".azure"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".config", "gh"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".npmrc"),
		filepath.Join(home, ".pypirc"),
		filepath.Join(home, ".git-credentials"),
		filepath.Join(home, ".config", "git", "credentials"),
	)
}

func skipDefaultCredentialDeny(profile PermissionProfile, cred string) bool {
	if cred == "" {
		return true
	}
	credAbs, err := filepath.Abs(cred)
	if err != nil {
		return true
	}
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != rulePath || rule.Path == "" || !filepath.IsAbs(rule.Path) {
			continue
		}
		if rule.Access != accessRead && rule.Access != accessWrite {
			continue
		}
		if filepath.Clean(rule.Path) == credAbs {
			return true
		}
	}
	return false
}

// skipIsolatedHomeCredentialMask reports whether an isolated profile can skip
// masking a home credential because that subtree is not mounted.
func skipIsolatedHomeCredentialMask(profile PermissionProfile, home, cred string) bool {
	if profile.exposesHostRoot() || home == "" || cred == "" || !sameOrChild(home, cred) {
		return false
	}
	return !hostPathHasRule(profile, cred, accessRead)
}
