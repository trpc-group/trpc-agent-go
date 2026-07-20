//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path/filepath"
	"strings"
)

// ruleEnvName evaluates the environment variable name whitelist. When
// the policy declares an EnvWhitelist, any env var in ScanInput.Env
// whose name is not in the whitelist produces a finding. This prevents
// environment injection of arbitrary variables (e.g. LD_PRELOAD,
// PYTHONPATH, PATH overrides) that could bypass the executor's clean
// environment.
//
// In addition to the whitelist, certain variable names are ALWAYS denied
// for hostexec/workspace_exec backends because they can redirect
// executable resolution or load arbitrary code, defeating the command
// allowlist:
//   - PATH: can redirect allowlisted commands to attacker binaries.
//   - LD_PRELOAD, LD_LIBRARY_PATH, DYLD_LIBRARY_PATH: can inject code.
//   - PYTHONPATH, PYTHONHOME, NODE_PATH: can inject modules.
//   - IFS, BASH_ENV, ENV: can alter shell parsing.
//
// Rule ids:
//
//   - env.non_whitelisted_name   env var name not in the whitelist.
//   - env.dangerous_override     env var can hijack executable/module resolution.
func ruleEnvName(in ScanInput, p Policy) []Finding {
	if len(in.Env) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(p.EnvWhitelist))
	for _, name := range p.EnvWhitelist {
		allowed[strings.ToUpper(name)] = true
	}
	var out []Finding
	seen := map[string]bool{}
	for name := range in.Env {
		upper := strings.ToUpper(name)
		// Dangerous override variables are always denied for
		// command-executing backends, even when they appear in the
		// whitelist. The whitelist allows the name to be PRESENT, but
		// the guard still flags it so the audit records the risk and
		// the operator can review.
		if isDangerousEnvOverride(name) &&
			(in.Backend == BackendHostExec || in.Backend == BackendWorkspaceExec) {
			if seen[upper] {
				continue
			}
			seen[upper] = true
			out = append(out, Finding{
				RuleID:         "env.dangerous_override",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskHigh, p),
				Evidence:       "env var can hijack executable or module resolution: " + name,
				Recommendation: "Do not allow caller-supplied PATH/LD_PRELOAD/PYTHONPATH; use a clean environment with trusted resolution",
			})
			continue
		}
		if len(p.EnvWhitelist) == 0 {
			continue
		}
		if allowed[upper] {
			continue
		}
		if seen[upper] {
			continue
		}
		seen[upper] = true
		out = append(out, Finding{
			RuleID:         "env.non_whitelisted_name",
			RiskLevel:      RiskMedium,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskMedium, p),
			Evidence:       "env var name not in whitelist: " + name,
			Recommendation: "Add the variable to env_whitelist or remove it from the request",
		})
	}
	return out
}

// isDangerousEnvOverride returns true for environment variable names
// that can redirect executable resolution or load arbitrary code, which
// would defeat the command allowlist.
func isDangerousEnvOverride(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH",
		"DYLD_INSERT_LIBRARIES", "PYTHONPATH", "PYTHONHOME",
		"NODE_PATH", "NODE_OPTIONS", "RUBYLIB", "RUBYOPT",
		"PERL5LIB", "PERLLIB", "CLASSPATH", "JAVA_TOOL_OPTIONS",
		"IFS", "BASH_ENV", "ENV", "SHELLOPTS",
		"GLIBC_TUNABLES", "HISTFILE":
		return true
	}
	return false
}

// ruleCwd evaluates the working directory against the policy's denied
// paths and a workspace-boundary heuristic. When the policy declares
// DeniedPaths or DeniedPathGlobs, a cwd that is inside a denied path
// produces a finding. An absolute cwd outside the workspace (e.g. /etc,
// /root, ~/.ssh) is always denied regardless of configuration because
// the executor would resolve relative path arguments against it.
//
// Rule ids:
//
//   - cwd.denied               cwd is in denied_paths or denied_path_globs.
//   - cwd.system_path          cwd is a system path (/, /etc, /root, ...).
//   - cwd.ssh_or_credential    cwd is ~/.ssh or a credential directory.
func ruleCwd(in ScanInput, p Policy) []Finding {
	cwd := strings.TrimSpace(in.Cwd)
	if cwd == "" {
		return nil
	}
	normalized := normalizePath(cwd)
	// Lexical clean to catch /etc/../etc, /etc/./, etc.
	if cleaned := filepath.Clean(normalized); cleaned != "" {
		normalized = cleaned
	}

	var out []Finding
	if isRootOrSystemPath(normalized) {
		out = append(out, Finding{
			RuleID:         "cwd.system_path",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskHigh, p),
			Evidence:       "cwd is a system path: " + redactedPath(normalized),
			Recommendation: "Set cwd to a workspace directory; refuse system paths",
		})
	}
	if isSSHPath(normalized) || isCredentialPath(normalized) {
		out = append(out, Finding{
			RuleID:         "cwd.ssh_or_credential",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
			Evidence:       "cwd is an SSH or credential directory",
			Recommendation: "Never use ~/.ssh or a credential directory as the working directory",
		})
	}
	if matchesDeniedPath(normalized, p) {
		out = append(out, Finding{
			RuleID:         "cwd.denied",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskHigh, p),
			Evidence:       "cwd matches a denied path pattern: " + redactedPath(normalized),
			Recommendation: "Set cwd to a path that is not in denied_paths or denied_path_globs",
		})
	}
	return out
}

// ruleUnknownTool handles the fail-closed semantics for tools that do
// not have a registered profile. The plan's fixed decision 5 says:
//
//   - Known execution tools with invalid arguments are denied (handled
//     by the decode error path in CheckToolPermission).
//   - Unknown MCP-like tools with command-shaped arguments require ask
//     unless an explicit ToolProfile is registered.
//   - Tools with no recognized command surface (e.g. an MCP search tool
//     with no command field) are allowed to pass through the guard;
//     their safety is the MCP server's responsibility.
//
// Rule ids:
//
//   - unknown.command_shaped_tool   unregistered tool with a command
//     field; requires human review.
func ruleUnknownTool(in ScanInput, a *analysis, p Policy, profiles profileRegistry) []Finding {
	if _, ok := profiles.lookup(in.ToolName); ok {
		return nil
	}
	if _, ok := profiles.lookup(in.ToolProfile); ok {
		return nil
	}
	// If the tool has no command and no code blocks, it is a non-
	// execution MCP tool; allow it through.
	if in.Command == "" && len(in.CodeBlocks) == 0 && len(in.Args) == 0 {
		return nil
	}
	// Unknown tool with a command-shaped argument: ask.
	return []Finding{{
		RuleID:         "unknown.command_shaped_tool",
		RiskLevel:      RiskMedium,
		Decision:       DecisionAsk,
		Evidence:       "tool profile not registered; execution shape requires human review",
		Recommendation: "Register a ToolProfile for this tool or remove the command field from its schema",
	}}
}
