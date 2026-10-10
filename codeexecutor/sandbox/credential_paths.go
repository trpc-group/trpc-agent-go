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

// defaultCredentialDenyPaths returns host credential paths that managed profiles
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
		filepath.Join(home, ".config", "hub"),
		filepath.Join(home, ".cargo", "credentials"),
		filepath.Join(home, ".cargo", "credentials.toml"),
		filepath.Join(home, ".terraform.d", "credentials.tfrc.json"),
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
	return credentialExplicitlyGranted(profile, credAbs, credAbs)
}

// skipGrantedHomeCredentialMask reports whether a granted-mode profile can skip
// masking a home credential because that subtree is not mounted.
func skipGrantedHomeCredentialMask(profile PermissionProfile, home, cred string) bool {
	if profile.exposesHostRoot() || home == "" || cred == "" || !sameOrChild(home, cred) {
		return false
	}
	return !hostPathHasRule(profile, cred, accessRead)
}
