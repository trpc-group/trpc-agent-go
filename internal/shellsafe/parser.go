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
//
// Keys are kept in lower case and compared after the segment
// basename has been case-folded by normalizeName, so "SH -c …" on
// macOS's default case-insensitive APFS and "Sh" / "SH.EXE" on
// Windows are matched just like the bare "sh" form.
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
	// process runners that take a command argument and exec it
	// under their own argv[0]. Without these, "<wrapper> curl
	// http://x" passes a deny on "curl" because the policy only
	// sees the wrapper.
	"xargs": {}, "env": {}, "nohup": {}, "timeout": {},
	"sudo": {}, "su": {}, "doas": {},
	"setsid": {}, "unshare": {}, "chroot": {}, "runuser": {},
	"time": {}, "nice": {}, "ionice": {}, "taskset": {},
	"stdbuf": {}, "strace": {}, "ltrace": {},
	// script(1) records a session and -c runs an arbitrary
	// command; flock(1) acquires a lock and execs its remaining
	// argv. Both are core util-linux / coreutils tools, present
	// in default Linux / macOS installs, and would otherwise
	// satisfy an "argv[0] only" policy with their own name.
	"script": {}, "flock": {},
	// shell builtins that register code to run later or mutate
	// the parsing / resolution state of subsequent segments.
	// Without these, "trap 'curl http://x' EXIT", "alias x=curl",
	// "export PATH=./bin && allowed_cmd", or "shopt -s extdebug"
	// would defeat a deny on "curl" or an allow on bare names.
	"trap": {}, "alias": {}, "unalias": {},
	"enable": {}, "hash": {},
	"export": {}, "unset": {}, "readonly": {},
	"local": {}, "declare": {}, "typeset": {},
	"set": {}, "shopt": {},
	// cwd-changing builtins. Strict allow already rejects "./x",
	// but a cwd swap to an attacker-prepared directory plus a
	// subsequent allowed bare name still resolves through the
	// shell's CWD lookup on some PATH configurations, so block
	// them defensively. workspace_exec exposes a "cwd" parameter
	// for the legitimate case.
	"cd": {}, "pushd": {}, "popd": {},
	// shell builtins that assign to a shell variable. On a shell
	// that runs the full pipeline in a single process (e.g.
	// `sh -c "printf -v PATH ./work/bin; git"` under bash), they
	// can rewrite PATH or other resolution state before a later
	// allowed segment runs, so a deny on "curl" + allow on "git"
	// still ends up resolving git to "./work/bin/git". `printf`,
	// `let`, `mapfile` and `readarray` are bash extensions but
	// `/bin/sh` is bash on macOS and on many container images,
	// so the deny needs to be unconditional. `read` and
	// `getopts` are POSIX and assign to a named variable.
	"printf": {}, "read": {}, "getopts": {},
	"let": {}, "mapfile": {}, "readarray": {},
}

// Policy holds the executable-name allow/deny lists that should be
// applied to a parsed Pipeline. The two lists use deliberately
// asymmetric matching so the policy fails closed under workspace-
// controlled binaries:
//
//   - Deny matches the verbatim first word of each segment or its
//     basename, so a deny of "curl" rejects "curl", "/usr/bin/curl"
//     and "./curl" alike. This is the conservative direction.
//
//   - Allow matches strictly. A bare name like "echo" only allows
//     literal "echo"; "./echo" and "/usr/bin/echo" are rejected
//     because a workspace-controlled file at "./echo" can otherwise
//     smuggle past a basename-only check. To permit a specific
//     absolute or relative path, list that exact path in Allow.
//
// Case handling tracks the underlying file system's resolution
// rules so the allowlist cannot be silently widened on a
// case-sensitive FS:
//
//   - Deny + implicit deny: case-folded on every OS (a deny of
//     "curl" also rejects "Curl" and "CURL", which matters on
//     macOS's default case-insensitive APFS and on Windows; on
//     Linux the fold is defence-in-depth against workspace-
//     controlled upper-case binaries).
//
//   - Allow: pathful entries are always exact-case on every OS,
//     because we cannot reliably tell whether the workspace
//     volume is case-sensitive (macOS APFS supports opt-in
//     case-sensitive volumes, container layers can mix
//     filesystems). Folding would silently widen "./safe" to
//     admit a workspace-controlled "./SAFE" on those volumes.
//     Bare-name allow entries resolve through PATH (reset by
//     the policy to a known-good default), so they follow the
//     OS convention: case-folded on Windows and macOS, exact-
//     case on Linux. Operators who need both case variants
//     list both.
//
// On Windows both directions additionally strip common
// executable suffixes (.exe, .cmd, .bat, .com, .ps1), so a deny
// entry "cmd" rejects "cmd.exe" and an allow entry "echo"
// admits "ECHO.EXE".
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
	return p.checkSegmentForGOOS(argv, runtime.GOOS)
}

func (p Policy) checkSegmentForGOOS(argv []string, goos string) error {
	cmd := argv[0]
	base := basenameForGOOS(cmd, goos)
	if matchDeny(p.Deny, cmd, base, goos) {
		return fmt.Errorf(
			"command %q is denied by denied_commands", cmd,
		)
	}
	// implicitDeny keys are lower-case. Fold the basename through
	// normalizeName so "SH", "Sh", "/usr/bin/SH" and (on Windows)
	// "sh.exe" all hit the look-up via the bare "sh" key.
	if _, ok := implicitDeny[normalizeName(base, goos)]; ok {
		return implicitDenyError(cmd)
	}
	if len(p.Allow) > 0 && !matchAllow(p.Allow, cmd, base, goos) {
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

// matchDeny is the permissive direction: an entry matches the
// command's verbatim first word or its basename. All comparisons
// happen in normalised form (case-folded everywhere, .exe / .cmd
// / ... stripped on Windows), so a deny of "curl" rejects "curl",
// "Curl", "CURL", "/usr/bin/curl", "./curl" and (on Windows)
// "curl.exe" alike. This catches the case-insensitive resolvers
// on Windows and on the default macOS APFS without losing the
// basename-style matching for arbitrary paths.
func matchDeny(set []string, name, base, goos string) bool {
	nName := normalizeName(name, goos)
	nBase := normalizeName(base, goos)
	for _, n := range set {
		nEntry := normalizeName(n, goos)
		if nEntry == nName || nEntry == nBase {
			return true
		}
	}
	return false
}

// matchAllow is the strict direction: the entry must equal the
// command verbatim, OR the command must be a bare name (no path
// separator) whose basename equals the entry. Pathful inputs such
// as "./echo" or "work/bin/echo" therefore never match a bare
// allow entry "echo", which prevents a workspace-controlled file
// with an allowlisted basename from bypassing the policy.
//
// Case handling is split between the two forms:
//
//   - Pathful allow entries are always matched exact-case. The
//     entry identifies a specific workspace file, and the
//     workspace file system's case-sensitivity is not something
//     we can reliably detect (macOS APFS can be opt-in
//     case-sensitive, container layers can mix volumes, etc.).
//     Folding would silently widen "./safe" to admit a
//     workspace-controlled "./SAFE" on case-sensitive volumes,
//     so we keep it strict everywhere. Operators who need both
//     variants list both.
//
//   - Bare allow entries resolve through PATH (which the policy
//     mode resets to a known-good default), so the OS conventions
//     apply: case-folded on Windows and macOS (where their
//     default file systems already resolve "ECHO" and "echo" to
//     the same /bin/echo entry), exact-case on Linux. On Windows
//     entries also strip the executable suffix so "echo" admits
//     "ECHO.EXE".
func matchAllow(set []string, name, base, goos string) bool {
	hasPath := strings.ContainsAny(name, "/\\")
	// Bare-form fold: tracks PATH-based resolution conventions.
	bareFold := !hasPath && goos != "linux"
	bareNorm := func(s string) string {
		if !bareFold {
			return s
		}
		return normalizeName(s, goos)
	}
	bareName := bareNorm(name)
	bareBase := bareNorm(base)
	for _, n := range set {
		// Pathful comparison is always exact-case so a workspace
		// file with a differently-cased name cannot smuggle past
		// an explicit-path allow.
		if n == name {
			return true
		}
		if hasPath {
			continue
		}
		nEntry := bareNorm(n)
		if nEntry == bareName || nEntry == bareBase {
			return true
		}
	}
	return false
}

func basename(s string) string {
	return basenameForGOOS(s, runtime.GOOS)
}

// basenameForGOOS returns just the path basename of s without any
// case-folding or extension stripping. Callers that need fold
// semantics (matchDeny, matchAllow on Windows / Darwin, the
// implicit-deny look-up) run normalizeName on the result
// themselves so the policy can keep different semantics for
// allow (case-sensitive on Linux) and deny (folded everywhere).
// The goos parameter is currently only used to decide how to
// route Windows-style "\" separators - it is still accepted so
// the helper's surface mirrors normalizeName / matchDeny /
// matchAllow and is easy to wire from a single call site.
func basenameForGOOS(s, _ string) string {
	if s == "" {
		return s
	}
	clean := filepath.ToSlash(s)
	return path.Base(clean)
}

// windowsExecExts is the set of Windows executable suffixes that
// `normalizeName` strips so allow/deny entries like "cmd",
// "powershell" or "curl" match the common ".exe" form.
var windowsExecExts = []string{
	".exe", ".cmd", ".bat", ".com", ".ps1",
}

// normalizeName lower-cases the input so the policy matches the
// way mainstream OSes resolve command names: Windows and the
// default macOS APFS both look up executables case-insensitively,
// so without case-folding "CURL" or "SH -c" would slip past a
// deny on "curl" / the implicit deny on "sh" on those platforms.
// Linux file systems are case-sensitive, but the rare workspace
// that ships a distinct upper-case "Curl" binary is not a use
// case we want to silently allow past a deny, so the fold is
// applied universally for safety. On Windows the function also
// strips common executable suffixes (.exe, .cmd, .bat, .com,
// .ps1) so "cmd" matches "cmd.exe" and vice versa. Parameterised
// by goos so the Windows-only suffix branch is testable on any
// host.
func normalizeName(s, goos string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	if goos != "windows" {
		return lower
	}
	for _, ext := range windowsExecExts {
		if strings.HasSuffix(lower, ext) {
			return lower[:len(lower)-len(ext)]
		}
	}
	return lower
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
