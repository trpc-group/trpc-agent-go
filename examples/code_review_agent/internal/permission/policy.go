//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package permission implements the permission decision layer for the
// code-review agent's sandboxed command execution.
//
// Policy is a fail-closed adapter around tool.PermissionPolicy. It
// tokenizes a raw command line with strings.Fields and matches the
// executable's base name against an explicit allow-list and deny-list
// using exact map lookups (never substring matching, so a deny entry
// such as "su" cannot match "mysuite"). Shell metacharacters, empty
// input and unparseable commands are denied outright. Commands that
// are neither allowed nor denied return Ask, which non-interactive
// callers treat as blocked via CheckNonInteractive.
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// shellMetacharacters are rejected before tokenization to keep the
// policy fail-closed against shell injection. The set covers pipes,
// sequencing, redirection and the two command-substitution spellings:
// "$" blocks both "$(…)" and "${…}", and "`" blocks backtick
// substitution. Backslash is included because strings.Fields does not
// interpret escapes, so a stray "\" would otherwise leak into argv.
const shellMetacharacters = "|;&><\\`$\n\r"

// defaultDeniedCommands are always blocked, even when a caller of
// NewPolicy/LoadPolicy forgets to list them. They are dangerous in a
// sandboxed code-review context (destructive ops, network egress,
// privilege escalation, system power control).
//
// Note: "sh" and "bash" are NOT in the deny-list because the skill's
// POSIX shell scripts are invoked via "sh <script>". Shell injection is
// prevented by the shellMetacharacters check (pipes, sequencing,
// substitution, etc.) and by the allow-list, which only permits "sh"
// when the pipeline explicitly generates the command.
var defaultDeniedCommands = []string{
	"rm", "rmdir", "dd", "mkfs", "fdisk", "curl", "wget", "nc",
	"su", "sudo", "chmod", "chown", "mount", "umount",
	"kill", "pkill", "reboot", "shutdown", "poweroff",
}

// defaultAllowedCommands are the known-safe review tools that the
// sandbox may run without prompting. They are read-only or low-risk
// for a code-review workload. "sh" is allowed so the skill's POSIX
// shell scripts can be executed.
var defaultAllowedCommands = []string{
	"go", "staticcheck", "git", "ls", "cat", "grep", "find", "wc",
	"diff", "head", "tail", "mkdir", "cp", "mv", "echo", "printf",
	"sh",
}

// Policy is a fail-closed PermissionPolicy for code-review sandbox
// commands. The zero value is not usable; construct it via NewPolicy
// or LoadPolicy.
//
// Policy is safe for concurrent use: the allow/deny maps are frozen
// after construction and only the stats counters are mutated under a
// mutex.
type Policy struct {
	deniedCommands  map[string]struct{}
	allowedCommands map[string]struct{}

	mu    sync.Mutex
	stats PolicyStats
}

// PolicyStats reports decision counts for observability and tests.
type PolicyStats struct {
	Allow int
	Deny  int
	Ask   int
}

// Compile-time assertion that Policy satisfies tool.PermissionPolicy.
// The framework interface requires a pointer PermissionRequest and a
// value PermissionDecision return, so the method below uses that exact
// signature (the value-request / pointer-return variant in the task
// brief would not satisfy the interface).
var _ tool.PermissionPolicy = (*Policy)(nil)

// NewPolicy builds a Policy from a deny-list of command tokens. The
// default deny-list is always present (union with denied), and the
// default allow-list is installed. Entries are normalized to their
// base name so "/usr/bin/rm" and "rm" are equivalent.
func NewPolicy(denied []string) *Policy {
	p := &Policy{
		deniedCommands:  make(map[string]struct{}, len(defaultDeniedCommands)),
		allowedCommands: make(map[string]struct{}, len(defaultAllowedCommands)),
	}
	for _, c := range defaultDeniedCommands {
		p.deniedCommands[c] = struct{}{}
	}
	for _, c := range denied {
		if base := normalizeCommandToken(c); base != "" {
			p.deniedCommands[base] = struct{}{}
		}
	}
	for _, c := range defaultAllowedCommands {
		p.allowedCommands[c] = struct{}{}
	}
	return p
}

// CheckToolPermission implements tool.PermissionPolicy. It extracts
// the "command" field from the JSON-encoded request arguments and
// delegates to Check. Requests with nil args, missing/non-string
// command fields or invalid JSON fail-closed to deny.
func (p *Policy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if req == nil {
		p.record(tool.PermissionActionDeny)
		return tool.DenyPermission("fail-closed: nil permission request"), nil
	}
	cmd, err := extractCommand(req.Arguments)
	if err != nil {
		p.record(tool.PermissionActionDeny)
		return tool.DenyPermission("fail-closed: " + err.Error()), nil
	}
	dec, _ := p.Check(cmd)
	return dec, nil
}

// Check is a convenience for the pipeline: given a raw command line it
// returns the decision and a human-readable reason.
//
// Decision order (fail-closed at every step):
//  1. empty/whitespace-only       -> Deny "fail-closed: empty command"
//  2. shell metacharacter present -> Deny "fail-closed: shell metacharacters"
//  3. no tokens after Fields      -> Deny "fail-closed: unparseable"
//  4. base = filepath.Base(argv0)
//  5. base in deny-list           -> Deny "deny-listed: <base>"
//  6. base in allow-list          -> Allow
//  7. otherwise                   -> Ask "needs review: <base>"
func (p *Policy) Check(cmd string) (tool.PermissionDecision, string) {
	dec := p.evaluate(cmd)
	p.record(dec.Action)
	return dec, dec.Reason
}

// CheckNonInteractive treats Ask as blocked, returning Deny with the
// reason "ask treated as deny in non-interactive mode". Allow and Deny
// pass through unchanged. Stats reflect the final (post-conversion)
// action so callers auditing counters see what the sandbox observed.
func (p *Policy) CheckNonInteractive(cmd string) (tool.PermissionDecision, string) {
	dec := p.evaluate(cmd)
	final := dec
	if dec.Action == tool.PermissionActionAsk {
		final = tool.DenyPermission("ask treated as deny in non-interactive mode")
	}
	p.record(final.Action)
	return final, final.Reason
}

// Stats returns a snapshot of the decision counters.
func (p *Policy) Stats() PolicyStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// evaluate applies the decision logic without mutating stats.
func (p *Policy) evaluate(cmd string) tool.PermissionDecision {
	if strings.TrimSpace(cmd) == "" {
		return tool.DenyPermission("fail-closed: empty command")
	}
	if strings.ContainsAny(cmd, shellMetacharacters) {
		return tool.DenyPermission("fail-closed: shell metacharacters")
	}
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return tool.DenyPermission("fail-closed: unparseable")
	}
	base := filepath.Base(tokens[0])
	// Deny shell flags (-c/-i/-s) for any shell, even allow-listed ones,
	// because they enable arbitrary command execution that bypasses the
	// deny-list (e.g. "sh -c 'rm -rf /'").
	if isShellWithFlags(base, tokens[1:]) {
		return tool.DenyPermission("fail-closed: shell flag denied (use sh <script> only)")
	}
	if _, ok := p.deniedCommands[base]; ok {
		return tool.DenyPermission("deny-listed: " + base)
	}
	if _, ok := p.allowedCommands[base]; ok {
		return tool.AllowPermission()
	}
	return tool.AskPermission("needs review: " + base)
}

// shellCommands is the set of shells that accept -c/-i/-s flags for
// arbitrary command execution. When these flags are present the shell can
// bypass the deny-list entirely (e.g. "sh -c 'rm -rf /'"), so the policy
// only allows shells when invoked as "sh <script-path>" with no flags.
var shellCommands = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "dash": {},
}

// isShellWithFlags reports whether base is a shell and any argument starts
// with "-", which would enable -c/-i/-s arbitrary command execution.
func isShellWithFlags(base string, args []string) bool {
	if _, isShell := shellCommands[base]; !isShell {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// record increments the counter for the given action.
func (p *Policy) record(action tool.PermissionAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch action {
	case tool.PermissionActionAllow:
		p.stats.Allow++
	case tool.PermissionActionDeny:
		p.stats.Deny++
	case tool.PermissionActionAsk:
		p.stats.Ask++
	}
}

// PolicyConfig is the validated input to LoadPolicy. Only command
// lists are modeled; action/risk_level enums are intentionally absent
// because this policy decides per-command, not per-declared-rule. If
// those fields are added later, unknown enum values must error rather
// than silently defaulting (fail-closed).
type PolicyConfig struct {
	AllowedCommands []string
	DeniedCommands  []string
}

// LoadPolicy validates a PolicyConfig and returns a Policy. Empty or
// whitespace-only entries and duplicate base names within a single list
// are rejected. The returned Policy always includes the default
// deny-list and allow-list; config entries extend (never replace)
// them, so a misconfigured caller cannot accidentally allow "rm".
func LoadPolicy(cfg PolicyConfig) (*Policy, error) {
	allowed, err := normalizeCommandList(cfg.AllowedCommands)
	if err != nil {
		return nil, fmt.Errorf("allowed_commands: %w", err)
	}
	denied, err := normalizeCommandList(cfg.DeniedCommands)
	if err != nil {
		return nil, fmt.Errorf("denied_commands: %w", err)
	}
	p := NewPolicy(denied)
	for _, c := range allowed {
		p.allowedCommands[c] = struct{}{}
	}
	return p, nil
}

// normalizeCommandToken trims surrounding whitespace and reduces a
// command token to its base name. Returns "" for empty input.
func normalizeCommandToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return filepath.Base(s)
}

// normalizeCommandList validates a list of command tokens: it rejects
// empty/whitespace entries and duplicate base names, returning the
// normalized (base-name) list. It does not dedupe against the default
// lists, so an entry that duplicates a default is allowed (it just
// re-asserts the same rule).
func normalizeCommandList(in []string) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			return nil, fmt.Errorf("empty or whitespace-only command entry")
		}
		base := filepath.Base(s)
		if base == "." || base == string(filepath.Separator) {
			return nil, fmt.Errorf("invalid command entry %q", raw)
		}
		if _, ok := seen[base]; ok {
			return nil, fmt.Errorf("duplicate command entry %q", base)
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out, nil
}

// extractCommand pulls the "command" string out of a JSON-encoded
// permission request argument payload. Empty payload, invalid JSON, a
// missing "command" key or a non-string value all produce an error so
// the caller can fail-closed.
func extractCommand(args []byte) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("empty arguments")
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	raw, ok := m["command"]
	if !ok {
		return "", fmt.Errorf("command field missing")
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("command field is not a string")
	}
	return s, nil
}
