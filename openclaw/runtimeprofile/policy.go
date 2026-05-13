//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runtimeprofile

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// WorkspaceFromContext returns the active profile workspace policy.
func WorkspaceFromContext(ctx context.Context) (WorkspacePolicy, bool) {
	profile, ok := ProfileFromContext(ctx)
	if !ok {
		return WorkspacePolicy{}, false
	}
	return cloneWorkspacePolicy(profile.Workspace), true
}

// CredentialPolicyFromContext returns the active profile credential policy.
func CredentialPolicyFromContext(
	ctx context.Context,
) (CredentialPolicy, bool) {
	profile, ok := ProfileFromContext(ctx)
	if !ok {
		return CredentialPolicy{}, false
	}
	return CredentialPolicy{
		AllowedRefs: copyStrings(profile.Credentials.AllowedRefs),
	}, true
}

// ResolveWorkdir applies the profile workspace default and allowed roots.
func ResolveWorkdir(ctx context.Context, requested string) (string, error) {
	profile, ok := ProfileFromContext(ctx)
	if !ok {
		return strings.TrimSpace(requested), nil
	}
	policy := profile.Workspace
	allowedRoots := cleanStrings(policy.AllowedRoots)
	base := strings.TrimSpace(policy.Workdir)
	workdir := strings.TrimSpace(requested)
	if workdir == "" {
		workdir = base
	} else if !filepath.IsAbs(workdir) && base != "" {
		workdir = filepath.Join(base, workdir)
	}
	if workdir != "" {
		workdir = filepath.Clean(workdir)
	}
	if len(allowedRoots) == 0 {
		return workdir, nil
	}
	if workdir == "" || !filepath.IsAbs(workdir) {
		return "", ErrWorkspaceDenied
	}
	allowed, err := isPathAllowed(workdir, allowedRoots)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", fmt.Errorf("%w: %s", ErrWorkspaceDenied, workdir)
	}
	return workdir, nil
}

// CheckCredentialRef returns an error when ref is denied by the profile.
func CheckCredentialRef(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	profile, ok := ProfileFromContext(ctx)
	if !ok {
		return nil
	}
	allowedRefs := cleanStrings(profile.Credentials.AllowedRefs)
	if len(allowedRefs) == 0 {
		return nil
	}
	if _, ok := nameSet(allowedRefs)[ref]; ok {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCredentialDenied, ref)
}

// SkillVisibilityFilter filters skills according to runtime profile policy.
func SkillVisibilityFilter(ctx context.Context, summary skill.Summary) bool {
	return skillVisibleForProfile(ctx, summary, nil)
}

// SkillPathResolver resolves the filesystem path for a skill name.
type SkillPathResolver interface {
	Path(name string) (string, error)
}

// SkillVisibilityFilterForRepository filters skills by include/exclude and
// optional profile roots.
func SkillVisibilityFilterForRepository(
	resolver SkillPathResolver,
) skill.VisibilityFilter {
	return func(ctx context.Context, summary skill.Summary) bool {
		return skillVisibleForProfile(ctx, summary, resolver)
	}
}

func skillVisibleForProfile(
	ctx context.Context,
	summary skill.Summary,
	resolver SkillPathResolver,
) bool {
	profile, ok := ProfileFromContext(ctx)
	if !ok {
		return true
	}
	name := strings.TrimSpace(summary.Name)
	if name == "" {
		return false
	}
	include := nameSet(cleanStrings(profile.Skills.Include))
	exclude := nameSet(cleanStrings(profile.Skills.Exclude))
	if _, blocked := exclude[name]; blocked {
		return false
	}
	if len(include) == 0 {
		return skillInAllowedRoots(name, profile.Skills.Roots, resolver)
	}
	_, allowed := include[name]
	return allowed && skillInAllowedRoots(name, profile.Skills.Roots, resolver)
}

func skillInAllowedRoots(
	name string,
	roots []string,
	resolver SkillPathResolver,
) bool {
	if len(cleanStrings(roots)) == 0 {
		return true
	}
	if resolver == nil {
		return false
	}
	path, err := resolver.Path(name)
	if err != nil {
		return false
	}
	allowed, err := isPathAllowed(path, roots)
	return err == nil && allowed
}

func isPathAllowed(path string, roots []string) (bool, error) {
	target, err := comparablePath(path)
	if err != nil {
		return false, err
	}
	for _, root := range cleanStrings(roots) {
		allowedRoot, err := comparablePath(root)
		if err != nil {
			return false, err
		}
		rel, err := filepath.Rel(allowedRoot, target)
		if err != nil {
			return false, err
		}
		if rel == "." || (rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true, nil
		}
	}
	return false, nil
}

func comparablePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func cloneWorkspacePolicy(policy WorkspacePolicy) WorkspacePolicy {
	policy.Workdir = strings.TrimSpace(policy.Workdir)
	policy.AllowedRoots = copyStrings(policy.AllowedRoots)
	return policy
}
