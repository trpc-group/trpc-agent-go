//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// analysis is the one-pass IR shared by every rule. The scanner parses a
// shell command exactly once via shellsafe and runs deterministic rules
// in a fixed order against this structure. Rules do not reparse or
// launch subprocesses.
type analysis struct {
	// Source is the original command string (rules read but never
	// persist it; reports carry only a redacted summary and hash).
	Source string
	// Pipeline is the parsed shell pipeline. Nil when Parse fails.
	Pipeline *shellsafe.Pipeline
	// ParseError is the shellsafe parse failure, if any.
	ParseError error

	// Executables lists the argv[0] of every pipeline segment in order.
	Executables []string
	// AllTokens lists every argv token across every segment. Path-like
	// and URL-like tokens are extracted from here.
	AllTokens []string
	// PathOps records the operation context for path-like tokens by
	// the executable that consumes them (read/write/delete/execute).
	PathOps []pathOp
	// NetworkTargets lists URL/host candidates extracted from known
	// network commands and explicit URL-looking arguments.
	NetworkTargets []networkTarget
	// EnvNames lists environment variable names referenced in the
	// command (assignments are rejected by shellsafe, but explicit
	// -E VAR or env VAR=val forms still surface here when present).
	EnvNames []string
	// ConfiguredNetworkCommands lists the network commands from the
	// policy's Network.Commands field. classifyToken uses this to
	// decide whether a token is a network target for a configured
	// downloader that is not in the built-in set.
	ConfiguredNetworkCommands []string
	// HasSubstitution is true when shellsafe reported a substitution-
	// style parse failure (command/parameter/arithmetic/process).
	HasSubstitution bool
	// HasRedirection is true when shellsafe reported a redirection
	// failure.
	HasRedirection bool
	// HasBackground is true when shellsafe reported a background '&'.
	HasBackground bool
	// WrapperNames records argv[0] values that match the shellsafe
	// implicit deny set (sh, bash, eval, env, sudo, xargs, ...).
	WrapperNames []string
	// SleepSeconds is the largest sleep duration observed in seconds,
	// or -1 when no sleep was found.
	SleepSeconds int64
	// HasUnboundedLoop is true when a code block contains while(true)/
	// for(;;) or equivalent.
	HasUnboundedLoop bool
	// HasOutputBomb is true when a command pattern matches an unbounded
	// output generator (yes, dd without count, seq without end, ...).
	HasOutputBomb bool
	// InstallPackages is true when a package manager install command
	// was detected.
	InstallPackages bool
	// CommandSummary is a truncated, redacted representation of the
	// source for inclusion in reports.
	CommandSummary string
	// CommandHash is a SHA-256 hex digest of the source.
	CommandHash string
	// codeMatches records which code patterns fired during scanCodeBlock
	// so codeRuleFindings can produce stable findings.
	codeMatches []*codeMatchRecord
}

type pathOp struct {
	// Token is the path-like argument.
	Token string
	// Op is one of "read", "write", "delete", "execute".
	Op string
	// Executable is the command that consumes the token.
	Executable string
}

type networkTarget struct {
	// Raw is the URL or host candidate as it appeared.
	Raw string
	// Host is the parsed host, lowercased.
	Host string
	// Scheme is the URL scheme when present.
	Scheme string
	// Malformed is true when the URL could not be parsed.
	Malformed bool
}

// analyzeShell parses src via shellsafe and extracts the shared IR fields
// without executing anything. It returns an analysis whose ParseError is
// non-nil when shellsafe rejects the command.
func analyzeShell(src string) analysis {
	return analyzeShellWithCommands(src, nil)
}

// analyzeShellWithCommands is analyzeShell with the policy's configured
// network commands seeded before token classification, so configured
// downloaders are recognized as network commands on the shell path.
func analyzeShellWithCommands(src string, configured []string) analysis {
	a := analysis{Source: src, SleepSeconds: -1}
	a.ConfiguredNetworkCommands = configured
	a.CommandHash = hashCommand(src)
	a.CommandSummary = summarizeCommand(src)

	pipe, err := shellsafe.Parse(src)
	if err != nil {
		a.ParseError = err
		classifyParseError(&a, err)
		return a
	}
	a.Pipeline = pipe
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		exec := argv[0]
		a.Executables = append(a.Executables, exec)
		if isWrapperName(exec) {
			a.WrapperNames = append(a.WrapperNames, exec)
		}
		for _, tok := range argv[1:] {
			a.AllTokens = append(a.AllTokens, tok)
			classifyToken(&a, exec, tok)
		}
		if isSleepCommand(exec, argv) {
			if secs := sleepSeconds(argv); secs >= 0 && secs > a.SleepSeconds {
				a.SleepSeconds = secs
			}
		}
		if isInstallCommand(exec, argv) {
			a.InstallPackages = true
		}
		if isOutputBomb(exec, argv) {
			a.HasOutputBomb = true
		}
	}
	return a
}

// buildAnalysis constructs the shared IR from a ScanInput. It separates
// three input shapes:
//
//  1. Shell command (in.Command non-empty): parsed once via shellsafe.
//     Rules inspect the parsed pipeline.
//
//  2. Explicit argv (in.Args non-empty, in.Command empty): assembled
//     into a synthetic single-segment pipeline without shellsafe parsing.
//     Command/path/network rules inspect the argv tokens.
//
//  3. Code blocks (in.CodeBlocks non-empty): each block is scanned for
//     dangerous APIs, shell wrappers, network calls, package installs,
//     file paths, and unbounded loops. The block's language determines
//     which patterns are recognized. When a block contains a shell
//     invocation (os.system, subprocess.call, exec.Command), the inner
//     command is also parsed via shellsafe so command/path/network rules
//     fire on the embedded command.
//
// When the input has no command, no args, and no code blocks, the
// analysis is empty and does NOT produce a shell.parse_failure finding.
// This fixes the P1 issue where safe execute_code calls were denied
// because shellsafe.Parse("") returns an error.
// buildAnalysis constructs the shared IR from a ScanInput and the
// policy. The policy's Network.Commands list is injected into the
// analysis BEFORE token classification so configured downloaders are
// recognized as network commands during the first pass. This fixes the
// P1 regression where ConfiguredNetworkCommands was set after
// buildAnalysis completed, making it ineffective.
func buildAnalysis(in ScanInput, p Policy) analysis {
	a := analysis{Source: in.Command, SleepSeconds: -1}
	a.ConfiguredNetworkCommands = p.Network.Commands
	// Build a combined summary/hash from command + code blocks so
	// code-only scripts get a non-empty summary and hash in the report.
	a.CommandHash = hashAnalysisInput(in)
	a.CommandSummary = summarizeAnalysisInput(in)
	if strings.TrimSpace(in.Command) != "" {
		shell := analyzeShellWithCommands(in.Command, p.Network.Commands)
		shell.CommandHash = a.CommandHash
		shell.CommandSummary = a.CommandSummary
		mergeAnalysis(&a, &shell)
	}

	// Explicit argv: build a synthetic pipeline segment.
	if len(in.Args) > 0 && a.Pipeline == nil {
		argv := make([]string, len(in.Args))
		copy(argv, in.Args)
		synthetic := &shellsafe.Pipeline{Commands: [][]string{argv}}
		a.Pipeline = synthetic
		exec := argv[0]
		a.Executables = append(a.Executables, exec)
		if isWrapperName(exec) {
			a.WrapperNames = append(a.WrapperNames, exec)
		}
		for _, tok := range argv[1:] {
			a.AllTokens = append(a.AllTokens, tok)
			classifyToken(&a, exec, tok)
		}
		if isSleepCommand(exec, argv) {
			if secs := sleepSeconds(argv); secs >= 0 && secs > a.SleepSeconds {
				a.SleepSeconds = secs
			}
		}
		if isInstallCommand(exec, argv) {
			a.InstallPackages = true
		}
		if isOutputBomb(exec, argv) {
			a.HasOutputBomb = true
		}
	}

	// Code blocks: scan each block for dangerous patterns.
	for _, b := range in.CodeBlocks {
		scanCodeBlock(&a, b)
	}

	return a
}

// mergeAnalysis copies shell's fields into a, preserving a's hash/summary.
func mergeAnalysis(a, shell *analysis) {
	a.Source = shell.Source
	a.Pipeline = shell.Pipeline
	a.ParseError = shell.ParseError
	a.Executables = append(a.Executables, shell.Executables...)
	a.AllTokens = append(a.AllTokens, shell.AllTokens...)
	a.PathOps = append(a.PathOps, shell.PathOps...)
	a.NetworkTargets = append(a.NetworkTargets, shell.NetworkTargets...)
	a.EnvNames = append(a.EnvNames, shell.EnvNames...)
	a.HasSubstitution = a.HasSubstitution || shell.HasSubstitution
	a.HasRedirection = a.HasRedirection || shell.HasRedirection
	a.HasBackground = a.HasBackground || shell.HasBackground
	a.WrapperNames = append(a.WrapperNames, shell.WrapperNames...)
	if shell.SleepSeconds >= 0 && shell.SleepSeconds > a.SleepSeconds {
		a.SleepSeconds = shell.SleepSeconds
	}
	a.HasOutputBomb = a.HasOutputBomb || shell.HasOutputBomb
	a.InstallPackages = a.InstallPackages || shell.InstallPackages
}

// hashAnalysisInput returns a SHA-256 hex digest of the command plus
// code blocks, so code-only scripts get a non-empty hash.
func hashAnalysisInput(in ScanInput) string {
	if in.Command == "" && len(in.CodeBlocks) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(in.Command))
	for _, b := range in.CodeBlocks {
		h.Write([]byte(b.Language))
		h.Write([]byte{0})
		h.Write([]byte(b.Code))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

// summarizeAnalysisInput returns a truncated, redacted representation of
// the command plus a short code-block hint, so code-only scripts get a
// non-empty summary in the report.
func summarizeAnalysisInput(in ScanInput) string {
	var parts []string
	if strings.TrimSpace(in.Command) != "" {
		parts = append(parts, summarizeCommand(in.Command))
	}
	for i, b := range in.CodeBlocks {
		if i >= 2 {
			parts = append(parts, "...")
			break
		}
		hint := summarizeCommand(b.Code)
		if len(hint) > 60 {
			hint = hint[:57] + "..."
		}
		parts = append(parts, b.Language+":"+hint)
	}
	s := strings.Join(parts, " ")
	redacted, _ := redactString(s)
	if len(redacted) > summaryMaxLen {
		redacted = redacted[:summaryMaxLen-3] + "..."
	}
	return redacted
}

// classifyParseError sets HasSubstitution/HasRedirection/HasBackground
// based on the shellsafe error message so the shell-bypass rule can
// produce a stable finding id.
func classifyParseError(a *analysis, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "command substitution"),
		strings.Contains(msg, "parameter expansion"),
		strings.Contains(msg, "arithmetic expansion"),
		strings.Contains(msg, "process substitution"),
		strings.Contains(msg, "backtick"):
		a.HasSubstitution = true
	case strings.Contains(msg, "redirection"):
		a.HasRedirection = true
	case strings.Contains(msg, "background"):
		a.HasBackground = true
	}
}

// classifyToken inspects one argv token and updates path/network/env IR.
// For network commands, only URL-like tokens (with a scheme) and tokens
// after a network-fetching subcommand (clone, fetch, push, pull) are
// treated as network targets. Bare subcommand arguments like "git status"
// or "git diff" must not be treated as network targets.
func classifyToken(a *analysis, exec, tok string) {
	if isPathLike(tok) {
		op := opForCommand(exec)
		a.PathOps = append(a.PathOps, pathOp{Token: tok, Op: op, Executable: exec})
	}
	if looksLikeURL(tok) {
		if t := extractNetworkTarget(tok); t.Raw != "" {
			a.NetworkTargets = append(a.NetworkTargets, t)
		}
		return
	}
	// Skip tokens that are known subcommand names for git. `git clone`
	// should not treat `clone` as a network target; only the URL
	// argument after it is a target.
	if isGitSubcommandName(tok) {
		return
	}
	// For git, only clone/fetch/push/pull subcommands produce network
	// targets from bare host/path arguments.
	if isNetworkCommandForPolicy(exec, a.ConfiguredNetworkCommands) && isNetworkSubcommand(exec, a) && !strings.HasPrefix(tok, "-") {
		if t := extractNetworkTarget(tok); t.Raw != "" && t.Host != "" {
			// Only add targets that have a real host (with a dot or
			// an IP). Skip ambiguous bare names that would produce
			// false positives.
			if strings.Contains(t.Host, ".") || net.ParseIP(t.Host) != nil {
				a.NetworkTargets = append(a.NetworkTargets, t)
			}
		}
	}
	if strings.HasPrefix(tok, "-") {
		// -E VAR, -u VAR, --env VAR style flags surface env names.
		if i := strings.Index(tok, "="); i > 0 {
			a.EnvNames = append(a.EnvNames, tok[:i])
		}
	}
}

// isGitSubcommandName returns true for known git subcommand names that
// should not be treated as network targets.
func isGitSubcommandName(tok string) bool {
	switch tok {
	case "clone", "fetch", "push", "pull", "ls-remote",
		"status", "diff", "log", "show", "add", "commit", "checkout",
		"branch", "merge", "rebase", "stash", "tag", "remote",
		"config", "init", "restore", "switch", "rm", "mv", "clean",
		"reset", "revert", "cherry-pick", "bisect", "reflog",
		"blame", "shortlog", "describe", "format-patch", "am",
		"apply", "archive", "bundle", "fsck", "gc", "prune",
		"rev-parse", "cat-file", "ls-tree", "ls-files", "grep",
		"name-rev", "rev-list", "show-ref", "update-ref", "symbolic-ref",
		"for-each-ref", "pack-refs", "count-objects", "unpack-objects",
		"verify-pack", "strip", "stripping", "submodule", "worktree",
		"sparse-checkout", "multi-pack-index", "maintenance":
		return true
	}
	return false
}

// isNetworkCommandForPolicy returns true when exec is a built-in network
// command OR is in the policy's configured network commands list.
func isNetworkCommandForPolicy(exec string, configured []string) bool {
	if isNetworkCommand(exec) {
		return true
	}
	base := basenameLower(exec)
	for _, c := range configured {
		if basenameLower(c) == base {
			return true
		}
	}
	return false
}

// isNetworkSubcommand returns true when the current pipeline segment
// for exec is a network-fetching subcommand. For git, only clone/fetch/
// push/pull are network subcommands; status, diff, log, add, commit,
// etc. are local operations.
func isNetworkSubcommand(exec string, a *analysis) bool {
	if len(a.Executables) == 0 {
		return false
	}
	base := basenameLower(exec)
	if base == "git" {
		// The first non-flag argument after git is the subcommand.
		if a.Pipeline != nil {
			for _, argv := range a.Pipeline.Commands {
				if len(argv) == 0 || basenameLower(argv[0]) != "git" {
					continue
				}
				for _, arg := range argv[1:] {
					if strings.HasPrefix(arg, "-") {
						continue
					}
					switch arg {
					case "clone", "fetch", "push", "pull", "ls-remote":
						return true
					}
					return false
				}
			}
		}
		return false
	}
	// For ssh/scp/sftp/curl/wget, any bare host argument is a network
	// target.
	return true
}

// isPathLike returns true when tok contains a path separator or starts
// with ~ or . or looks like an absolute path.
func isPathLike(tok string) bool {
	if tok == "" {
		return false
	}
	if strings.HasPrefix(tok, "~") {
		return true
	}
	if strings.HasPrefix(tok, "/") {
		return true
	}
	if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
		return true
	}
	// .env, .aws/credentials, etc.
	if strings.HasPrefix(tok, ".") && len(tok) > 1 && tok[1] != '.' && tok[1] != '/' {
		return true
	}
	if strings.ContainsAny(tok, "/") {
		return true
	}
	return false
}

// opForCommand returns the operation context for a path-like token
// consumed by exec. `find` is normally a read operation, but `find -delete`
// and `find -exec rm` are destructive and are classified as delete.
func opForCommand(exec string) string {
	switch exec {
	case "rm", "rmdir", "unlink":
		return "delete"
	case "mv", "cp", "tee", "install", "dd", "truncate", "shred":
		return "write"
	case "cat", "head", "tail", "less", "more", "grep",
		"ls", "stat", "file", "wc", "od", "hexdump", "strings":
		return "read"
	case ">", ">>":
		return "write"
	}
	if strings.HasPrefix(exec, ">") {
		return "write"
	}
	return "execute"
}

// hashCommand returns the SHA-256 hex digest of src, used for audit
// correlation without storing the raw command.
func hashCommand(src string) string {
	if src == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// summaryMaxLen is the maximum length of a command summary in a report.
const summaryMaxLen = 160

// summarizeCommand returns a truncated, redacted representation of src.
// It scans the FULL source for secrets first, then truncates the
// redacted result. The previous order (truncate-then-redact) could leave
// the original prefix of a token spanning the truncation boundary in the
// report, because the truncated prefix no longer matched the full secret
// regex. Scan-then-truncate guarantees no raw secret value reaches the
// report or audit trail, even when the token crosses the boundary.
func summarizeCommand(src string) string {
	s := strings.TrimSpace(src)
	redacted, _ := redactString(s)
	if len(redacted) > summaryMaxLen {
		redacted = redacted[:summaryMaxLen-3] + "..."
	}
	return redacted
}

// urlRegex matches explicit http(s):// or ssh:// URLs.
var urlRegex = regexp.MustCompile(`^(https?|ftp|ssh|scp|sftp|git)://[^\s]+`)

// looksLikeURL returns true when tok starts with a known URL scheme.
func looksLikeURL(tok string) bool {
	return urlRegex.MatchString(tok)
}

// extractNetworkTarget parses tok as a URL or bare host. Malformed,
// non-ASCII, IP-literal, loopback, link-local, and metadata targets are
// reported as malformed so the network rule can deny them. A bare host
// without a dot is still scanned: localhost, loopback IPs, and metadata
// service names are hard-dened, while other bare hosts are reported as
// ambiguous so the network rule can apply the allowlist or deny.
func extractNetworkTarget(tok string) networkTarget {
	t := networkTarget{Raw: tok}
	if looksLikeURL(tok) {
		u, host, scheme, err := parseURL(tok)
		if err != nil {
			t.Malformed = true
			return t
		}
		t.Host = host
		t.Scheme = scheme
		_ = u
		return t
	}
	// Bare host:port or host/path arguments to ssh/scp/sftp.
	host := tok
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return networkTarget{}
	}
	// Bare hosts are scanned: localhost, loopback/metadata IPs, and
	// ambiguous targets are hard-dened by the network rule. Bare hosts
	// that contain a dot are treated as domain candidates. Bare hosts
	// without a dot that are NOT localhost/etc are reported as
	// malformed so the network rule can deny or ask rather than
	// silently allow.
	t.Host = host
	t.Scheme = ""
	if !strings.Contains(host, ".") {
		// localhost and similar are meaningful targets that must be
		// checked; other bare names are ambiguous.
		if host == "localhost" {
			t.Malformed = true
		} else {
			// Could be a subcommand argument (git status) or an
			// internal host. Mark as ambiguous so the network rule
			// can decide based on the allowlist.
			t.Malformed = true
		}
	}
	return t
}
