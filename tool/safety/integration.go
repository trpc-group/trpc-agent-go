//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// CommandPolicyLists returns the allow and deny command lists a caller
// should pass to workspaceexec when wiring the guard's policy into the
// existing shellsafe/CleanEnv execution path. Empty entries and blanks
// are removed so the result is safe to spread into WithAllowedCommands /
// WithDeniedCommands.
//
// Example:
//
//	allow, deny := safety.CommandPolicyLists(policy)
//	workspaceTool := workspaceexec.NewExecTool(
//	    executor,
//	    workspaceexec.WithAllowedCommands(allow...),
//	    workspaceexec.WithDeniedCommands(deny...),
//	)
//
// This activates the existing shellsafe/CleanEnv path when the application
// chooses command lists. The guard itself remains reusable for hostexec,
// codeexec, and MCP tools.
func CommandPolicyLists(p Policy) (allow []string, deny []string) {
	allow = cleanStringList(p.AllowedCommands)
	deny = cleanStringList(p.DeniedCommands)
	return allow, deny
}

// cleanStringList trims and drops blank entries from s. Returns nil for an
// empty result so workspaceexec sees "no list" rather than a one-element
// blank list.
func cleanStringList(s []string) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shellPolicy builds a shellsafe.Policy from the safety policy. The
// shellsafe layer applies the implicit deny set of shell wrappers and
// re-executing builtins (sh, bash, eval, env, sudo, xargs, ...). The
// safety guard adds its own findings for those names so the audit trail
// records a stable rule id, but the shellsafe check remains the
// authoritative command-policy gate.
func shellPolicy(p Policy) shellsafe.Policy {
	allow, deny := CommandPolicyLists(p)
	return shellsafe.PolicyFromLists(allow, deny)
}
