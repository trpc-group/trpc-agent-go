// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package shellsafe parses a user-supplied shell command and applies
// an allow/deny policy to the executable name of every pipeline
// segment.
//
// The parser is deliberately conservative. It accepts only a tiny
// subset of bash: "plain" call expressions whose words are made up
// of literals, single-quoted strings and pure (expansion-free)
// double-quoted strings, joined by the safe operators '|', '&&',
// '||' and ';'. Everything that could
// re-introduce arbitrary code through shell features - command and
// parameter substitution ($(), backticks, $VAR), redirections,
// process substitution, subshells, brace expansion, control flow,
// background operators, leading variable assignments and so on - is
// rejected before any policy lookup happens, so a deny on "curl"
// cannot be sidestepped via $(c\url) or "${X}url".
//
// On top of the parser, Policy enforces a built-in deny list of
// shell wrappers and re-executing builtins (sh, bash, zsh, busybox,
// eval, exec, source, ., command, xargs, env, sudo, su, ...). These
// can turn an otherwise harmless allowlist into a foothold for
// arbitrary execution (e.g. "eval curl http://x" passes a curl-only
// deny when only argv[0] is checked), so they are blocked
// unconditionally whenever a policy is active and cannot be
// overridden via the explicit Allow list. If you legitimately need
// one of them, wrap it in an auditable script under the workspace
// and allow the script instead.
//
// The parser implementation is isolated behind the package-private
// commandParser seam. The default backend (parser_simple.go) is a
// hand-rolled, dependency-free lexer that covers the common 80% of
// commands (literal argv joined by safe operators). A richer
// backend - full bash AST, controlled redirections, glob support -
// can be added later by implementing the same seam and pointing
// parseCommand at it; the public API, Pipeline / Policy types and
// implicit deny set do not need to change.
package shellsafe

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Pipeline is the parsed and validated form of a command. Operators
// between segments are intentionally discarded: once the structural
// validation has accepted the command, the per-segment executable
// name is the only thing the policy looks at.
type Pipeline struct {
	// Commands lists the segment argv slices in source order.
	Commands [][]string
}

// commandParser is the package-private seam between the public,
// dependency-free API of shellsafe and the underlying bash parser
// implementation. It takes a normalised, non-empty command string
// and returns either a flat slice of "plain" pipeline segments
// (each segment is its argv) or a structured rejection error
// describing the first disallowed construct.
//
// The default implementation lives in parser_simple.go and is a
// hand-rolled lexer. Replacing the parser - whether by adopting a
// third-party bash AST library or by growing this one - only
// requires implementing a function with this signature and updating
// the line below; nothing else in the package depends on the parser
// internals.
type commandParser func(src string) ([][]string, error)

// parseCommand is wired at package init by the implementation file.
// Tests may temporarily replace it through withParser.
var parseCommand commandParser = parseCommandSimple

// Parse validates command against the structural rules described in
// the package doc and returns the resulting Pipeline. The returned
// error mentions the first construct that caused the rejection so
// callers can surface it verbatim to the model.
func Parse(command string) (*Pipeline, error) {
	src := strings.TrimSpace(command)
	if src == "" {
		return nil, errors.New("command is empty")
	}
	cmds, err := parseCommand(src)
	if err != nil {
		return nil, err
	}
	if len(cmds) == 0 {
		return nil, errors.New("command is empty")
	}
	return &Pipeline{Commands: cmds}, nil
}

// withParser swaps the active parser for the duration of the
// returned cleanup function and returns the previous parser. It is
// intended for tests that exercise the Pipeline / Policy layer
// without exercising the underlying parser implementation.
func withParser(p commandParser) (restore func()) {
	prev := parseCommand
	parseCommand = p
	return func() { parseCommand = prev }
}

// implicitDeny is the set of executable names that are always
// denied whenever a policy is active. Membership cannot be
// overridden by the user's Allow list: these are command runners,
// shell wrappers and re-executing builtins that can launch
// arbitrary code without their argv[0] being the dangerous command
// (e.g. "eval curl http://x", "sh -c 'curl ...'", "busybox sh -c
// '...'", "xargs curl http://x"), so allowing them would defeat
// the very purpose of an allow/deny list. If you legitimately need
// one, wrap the desired use in an auditable script under the
// workspace and put that script in the Allow list instead.
var implicitDeny = map[string]struct{}{
	// shell wrappers
	"sh": {}, "bash": {}, "zsh": {}, "ash": {}, "dash": {},
	"ksh": {}, "mksh": {}, "fish": {},
	"pwsh": {}, "powershell": {}, "cmd": {},
	// busybox / toybox multiplex into shell wrappers via "busybox sh
	// -c …", which would otherwise pass an allowlist that contains
	// "busybox".
	"busybox": {}, "toybox": {},
	// shell-builtin re-executers
	"eval": {}, "exec": {}, "command": {}, "source": {}, ".": {},
	"builtin": {},
	// process runners that take a command argument
	"xargs": {}, "env": {}, "nohup": {}, "timeout": {},
	"sudo": {}, "su": {}, "doas": {},
	"setsid": {}, "unshare": {}, "chroot": {}, "runuser": {},
}

// Policy holds the executable-name allow/deny lists that should be
// applied to a parsed Pipeline. Both lists are matched against the
// verbatim first word of each segment and against its basename, so
// a list of "curl" rejects both "curl" and "/usr/bin/curl". On
// Windows the basename is also matched after stripping common
// executable suffixes (.exe, .cmd, .bat, .com, .ps1), so a deny
// entry "cmd" rejects "cmd.exe".
//
// Precedence: explicit Deny > implicit deny > explicit Allow >
// implicit allow. When at least one of the lists is non-empty the
// implicit deny set is also applied unconditionally; users cannot
// override it by re-listing a shell wrapper in Allow.
type Policy struct {
	Allow []string
	Deny  []string
}

// PolicyFromLists returns a Policy with the given allow/deny lists.
// Empty / blank entries are skipped so callers can hand off
// env-variable splits without further cleanup.
func PolicyFromLists(allow, deny []string) Policy {
	return Policy{Allow: cleanList(allow), Deny: cleanList(deny)}
}

// Active reports whether the policy will reject anything beyond a
// parse error. A zero Policy is treated as "no policy": Parse is
// not even called.
func (p Policy) Active() bool {
	return len(p.Allow) > 0 || len(p.Deny) > 0
}

// CheckCommand parses command and applies the policy to every
// resulting pipeline segment. It is a convenience wrapper around
// Parse + Check.
func CheckCommand(command string, policy Policy) error {
	if !policy.Active() {
		return nil
	}
	pipe, err := Parse(command)
	if err != nil {
		return err
	}
	return policy.Check(pipe)
}

// Check applies the allow/deny lists (and the implicit deny) to
// every segment of pipe. A zero-valued Policy (no Allow, no Deny)
// is treated as inactive and accepts every pipe verbatim, mirroring
// the contract of CheckCommand and the Policy doc above; the
// implicit deny set only kicks in once at least one explicit list
// is configured.
func (p Policy) Check(pipe *Pipeline) error {
	if pipe == nil {
		return errors.New("nil pipeline")
	}
	if !p.Active() {
		return nil
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		if err := p.checkSegment(argv); err != nil {
			return err
		}
	}
	return nil
}

// checkSegment enforces the precedence documented on Policy:
// explicit Deny > implicit deny > explicit Allow > implicit allow.
// The implicit deny set is unconditional and cannot be bypassed by
// listing a shell wrapper in Allow.
func (p Policy) checkSegment(argv []string) error {
	cmd := argv[0]
	base := basename(cmd)
	if matchName(p.Deny, cmd, base) {
		return fmt.Errorf(
			"command %q is denied by denied_commands", cmd,
		)
	}
	if _, ok := implicitDeny[cmd]; ok {
		return implicitDenyError(cmd)
	}
	if _, ok := implicitDeny[base]; ok {
		return implicitDenyError(cmd)
	}
	if len(p.Allow) > 0 && !matchName(p.Allow, cmd, base) {
		return fmt.Errorf(
			"command %q is not in allowed_commands", cmd,
		)
	}
	return nil
}

func implicitDenyError(cmd string) error {
	return fmt.Errorf(
		"command %q is denied by built-in policy because it is a "+
			"shell wrapper or re-executing builtin that can bypass "+
			"the allow/deny list (eval curl ..., sh -c '...', "+
			"busybox sh ..., xargs ..., env CMD ..., sudo ..., "+
			"etc.). This deny is unconditional under policy mode; "+
			"wrap the desired use in an auditable workspace script "+
			"and allow the script instead.",
		cmd,
	)
}

func matchName(set []string, name, base string) bool {
	for _, n := range set {
		if n == name || n == base {
			return true
		}
	}
	return false
}

func basename(s string) string {
	if s == "" {
		return s
	}
	clean := filepath.ToSlash(s)
	return normalizeName(path.Base(clean), runtime.GOOS)
}

// windowsExecExts is the set of Windows executable suffixes that
// `normalizeName` strips so allow/deny entries like "cmd",
// "powershell" or "curl" match the common ".exe" form.
var windowsExecExts = []string{
	".exe", ".cmd", ".bat", ".com", ".ps1",
}

// normalizeName strips OS-specific executable suffixes that would
// otherwise let a name like "cmd.exe" slip past an entry of "cmd".
// On non-Windows OSes the input is returned unchanged so Linux
// command resolution is unaffected. Lifted into its own helper
// (parameterised by goos) so the Windows branch is testable on any
// host.
func normalizeName(base, goos string) string {
	if goos != "windows" || base == "" {
		return base
	}
	lower := strings.ToLower(base)
	for _, ext := range windowsExecExts {
		if strings.HasSuffix(lower, ext) {
			return base[:len(base)-len(ext)]
		}
	}
	return base
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SplitList parses a comma- or whitespace-separated list of command
// names into a slice. It is the canonical helper for reading
// allow/deny lists from environment variables.
func SplitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' ||
			r == '\n' || r == '\r'
	})
	return cleanList(fields)
}

// PreviewList renders up to max entries of in for inclusion in
// human-readable error messages and tool descriptions.
func PreviewList(in []string, max int) string {
	if len(in) == 0 {
		return ""
	}
	if max <= 0 || max >= len(in) {
		return strings.Join(in, ", ")
	}
	return strings.Join(in[:max], ", ") +
		fmt.Sprintf(", ... (%d more)", len(in)-max)
}
