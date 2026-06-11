//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// ShellEnvironmentPolicyInherit controls which host environment variables are
// visible before filters, overrides, per-run env, and sandbox runtime variables
// are applied.
type ShellEnvironmentPolicyInherit string

const (
	// ShellEnvironmentPolicyInheritAll inherits the full host environment by
	// default, matching Codex shell behavior. Use Core or None for stricter
	// sandbox deployments.
	ShellEnvironmentPolicyInheritAll ShellEnvironmentPolicyInherit = "all"
	// ShellEnvironmentPolicyInheritCore includes only shell startup variables
	// such as PATH, HOME, locale, user, and temporary-directory variables.
	ShellEnvironmentPolicyInheritCore ShellEnvironmentPolicyInherit = "core"
	// ShellEnvironmentPolicyInheritNone starts from an empty caller-controlled
	// environment.
	ShellEnvironmentPolicyInheritNone ShellEnvironmentPolicyInherit = "none"
)

// ShellEnvironmentPolicy controls the caller-controlled environment for shell
// commands. Resolution follows Codex semantics: inherit, optional default
// secret-name excludes, custom excludes, set overrides, per-run env overlays,
// final IncludeOnly allow-list, then sandbox-owned runtime variables.
type ShellEnvironmentPolicy struct {
	Inherit              ShellEnvironmentPolicyInherit
	ApplyDefaultExcludes bool
	Exclude              []string
	Set                  map[string]string
	IncludeOnly          []string
}
