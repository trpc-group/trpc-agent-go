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
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// policyCanonicalPath resolves existing ancestors as well as symlink aliases.
// Missing protection targets retain their lexical spelling for future files.
func policyCanonicalPath(path string) string {
	if resolved, err := canonicalizeExistingPath(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (r *Runtime) sessionProtectionRoot(ws codeexecutor.Workspace) string {
	root := policyCanonicalPath(filepath.Join(r.root, "sandbox"))
	if !sameOrChild(root, policyCanonicalPath(ws.Path)) {
		return ""
	}
	return root
}

// credentialExplicitlyGranted requires an allow rule at or inside a protected
// credential root. Granting HOME or another ancestor does not remove protection.
func credentialExplicitlyGranted(profile PermissionProfile, credential, target string) bool {
	credential, target = policyCanonicalPath(credential), policyCanonicalPath(target)
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != rulePath || !filepath.IsAbs(rule.Path) || !accessCanRead(rule.Access) {
			continue
		}
		path := policyCanonicalPath(rule.Path)
		if sameOrChild(credential, path) && sameOrChild(path, target) {
			return true
		}
	}
	return false
}

func (r *Runtime) checkHostRead(profile PermissionProfile, ws codeexecutor.Workspace, path string) error {
	if err := validateFileSystemRules(profile); err != nil {
		return err
	}
	if profile.enforcement() == enforcementDisabled {
		return nil
	}
	canonical := policyCanonicalPath(path)
	workspace := policyCanonicalPath(ws.Path)
	sessions := r.sessionProtectionRoot(ws)
	if sessions != "" && sameOrChild(sessions, canonical) && !sameOrChild(workspace, canonical) {
		return deniedf(ErrPathDenied, "read", path, "outside current session scope")
	}
	for _, credential := range defaultCredentialDenyPaths() {
		if sameOrChild(policyCanonicalPath(credential), canonical) && !sameOrChild(workspace, canonical) && !credentialExplicitlyGranted(profile, credential, canonical) {
			return deniedf(ErrPathDenied, "read", path, "protected credential path")
		}
	}
	rel, err := filepath.Rel(ws.Path, path)
	if err != nil {
		return err
	}
	access, _, err := r.resolveAccess(profile, ws, filepath.ToSlash(rel), path)
	if err != nil {
		return err
	}
	if !accessCanRead(access) {
		return deniedf(ErrPathDenied, "read", path, "host path requires read access")
	}
	return nil
}
