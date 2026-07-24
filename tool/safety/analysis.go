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
	"unicode/utf8"

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
		for i := 1; i < len(argv); i++ {
			tok := argv[i]
			a.AllTokens = append(a.AllTokens, tok)
			classifyToken(&a, argv, i)
		}
		classifySSHOptionTargets(&a, argv)
		classifyImplicitGitRemote(&a, argv)
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
	a := analysis{SleepSeconds: -1}
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
		for i := 1; i < len(argv); i++ {
			tok := argv[i]
			a.AllTokens = append(a.AllTokens, tok)
			classifyToken(&a, argv, i)
		}
		classifySSHOptionTargets(&a, argv)
		classifyImplicitGitRemote(&a, argv)
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

// mergeAnalysis accumulates shell's fields into a, preserving a's
// hash/summary. Parse failures are sticky: the first non-nil ParseError
// is kept and the merged pipeline stays nil so the raw-source fallback
// scans engage. A later successful parse must not erase an earlier
// failure — otherwise a multi-block input could smuggle an unparsable
// (and therefore unscanned) block past the rules by following it with a
// benign block that parses cleanly.
func mergeAnalysis(a, shell *analysis) {
	if a.Source == "" {
		a.Source = shell.Source
	} else if shell.Source != "" {
		a.Source += "\n" + shell.Source
	}
	switch {
	case shell.ParseError != nil:
		if a.ParseError == nil {
			a.ParseError = shell.ParseError
		}
		a.Pipeline = nil
	case a.ParseError != nil:
		// An earlier parse failure dominates: keep the pipeline nil so
		// the raw-source fallbacks engage.
		a.Pipeline = nil
	case shell.Pipeline != nil:
		if a.Pipeline == nil {
			a.Pipeline = shell.Pipeline
		} else {
			a.Pipeline.Commands = append(a.Pipeline.Commands, shell.Pipeline.Commands...)
		}
	}
	a.Executables = append(a.Executables, shell.Executables...)
	a.AllTokens = append(a.AllTokens, shell.AllTokens...)
	a.PathOps = append(a.PathOps, shell.PathOps...)
	a.NetworkTargets = append(a.NetworkTargets, shell.NetworkTargets...)
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
	if in.Command == "" && len(in.CodeBlocks) == 0 &&
		len(in.Args) == 0 {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(in.Command))
	for _, arg := range in.Args {
		h.Write([]byte{0})
		h.Write([]byte(arg))
	}
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
	for i, arg := range in.Args {
		if i >= 3 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, summarizeCommand(arg))
	}
	for i, b := range in.CodeBlocks {
		if i >= 2 {
			parts = append(parts, "...")
			break
		}
		hint := summarizeCommand(b.Code)
		if len(hint) > 60 {
			hint = truncateRuneSafe(hint, 57) + "..."
		}
		parts = append(parts, b.Language+":"+hint)
	}
	s := strings.Join(parts, " ")
	redacted, _ := redactString(s)
	if len(redacted) > summaryMaxLen {
		redacted = truncateRuneSafe(redacted, summaryMaxLen-3) + "..."
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

// classifyToken inspects one argv token of the pipeline segment argv and
// updates the path/network IR. argv[0] is the executable; the full
// segment is passed so network-subcommand detection judges the segment
// the token belongs to rather than rescanning the whole pipeline (in
// `git status && git clone host/x` the clone argument must be judged
// against its own segment).
//
// For network commands, only URL-like tokens (with a scheme) and tokens
// after a network-fetching subcommand (clone, fetch, push, pull) are
// treated as network targets. Bare subcommand arguments like "git status"
// or "git diff" must not be treated as network targets.
func classifyToken(a *analysis, argv []string, index int) {
	tok := argv[index]
	exec := argv[0]
	classifyGitOptionTargets(a, argv, index)
	classifyGoOptionTargets(a, argv, index)
	classifyEmbeddedOptionTargets(a, argv, index)
	classifyCommandOptionPaths(a, argv, index)
	// Strip a leading key= prefix (dd of=/etc/passwd, tar -f out.tar
	// style values) so the value is classified as a path when it looks
	// like one. Flag tokens (--output=/x) keep their own handling.
	target := tok
	if i := strings.IndexByte(target, '='); i > 0 && target[0] != '-' {
		target = target[i+1:]
	}
	if isPathLike(target) {
		op := opForCommand(exec)
		a.PathOps = append(a.PathOps, pathOp{Token: target, Op: op, Executable: exec})
	}
	if isNetworkCommandForPolicy(exec, a.ConfiguredNetworkCommands) &&
		isNetworkOptionValueForCommand(argv, index) {
		return
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
	if isNetworkCommandForPolicy(exec, a.ConfiguredNetworkCommands) && isNetworkSubcommand(argv) && !strings.HasPrefix(tok, "-") {
		if t := extractNetworkTarget(tok); t.Raw != "" && t.Host != "" {
			if shouldClassifyBareNetworkTarget(argv, index, t) {
				a.NetworkTargets = append(a.NetworkTargets, t)
			}
		}
	}
}

func classifyGitOptionTargets(
	a *analysis,
	argv []string,
	index int,
) {
	if len(argv) == 0 || basenameLower(argv[0]) != "git" {
		return
	}
	if value, ok := gitOptionValue(
		argv, index, "--bundle-uri", "",
	); ok {
		if target := extractNetworkTarget(value); target.Raw != "" {
			a.NetworkTargets = append(a.NetworkTargets, target)
		}
	}
	if gitSubcommand(argv) == "archive" {
		if value, ok := gitOptionValue(
			argv, index, "--remote", "",
		); ok {
			if target := extractNetworkTarget(value); target.Raw != "" {
				a.NetworkTargets = append(a.NetworkTargets, target)
			}
		}
	}
	for _, option := range []struct {
		long  string
		short string
		op    string
	}{
		{long: "--work-tree", op: gitWorkingTreeOperation(argv)},
		{long: "--git-dir", op: "read"},
		{long: "--separate-git-dir", op: "write"},
		{short: "-C", op: gitWorkingTreeOperation(argv)},
	} {
		value, ok := gitOptionValue(
			argv, index, option.long, option.short,
		)
		if !ok || !isPathLike(value) {
			continue
		}
		a.PathOps = append(a.PathOps, pathOp{
			Token:      value,
			Op:         option.op,
			Executable: argv[0],
		})
	}
	classifyGitWriteTargets(a, argv, index)
}

func classifyGitWriteTargets(
	a *analysis,
	argv []string,
	index int,
) {
	if value, ok := gitWriteOptionValue(argv, index); ok {
		appendGitWritePath(a, argv[0], value)
	}
	if value, ok := gitPositionalWriteValue(argv, index); ok {
		appendGitWritePath(a, argv[0], value)
	}
}

func gitWriteOptionValue(argv []string, index int) (string, bool) {
	switch gitSubcommand(argv) {
	case "archive":
		return gitOptionValue(argv, index, "--output", "-o")
	case "format-patch":
		return gitOptionValue(
			argv, index, "--output-directory", "-o",
		)
	}
	return "", false
}

func gitPositionalWriteValue(
	argv []string,
	index int,
) (string, bool) {
	positionals := gitPositionalArguments(argv)
	switch gitSubcommand(argv) {
	case "init":
		if len(positionals) > 0 && index == positionals[0] {
			return argv[index], true
		}
	case "clone":
		if len(positionals) > 1 && index == positionals[1] {
			return argv[index], true
		}
	case "bundle":
		if len(positionals) > 1 &&
			argv[positionals[0]] == "create" &&
			index == positionals[1] {
			return argv[index], true
		}
	case "worktree":
		if len(positionals) > 1 &&
			argv[positionals[0]] == "add" &&
			index == positionals[1] {
			return argv[index], true
		}
	}
	return "", false
}

func appendGitWritePath(a *analysis, executable, value string) {
	if value == "" || value == "-" {
		return
	}
	a.PathOps = append(a.PathOps, pathOp{
		Token:      value,
		Op:         "write",
		Executable: executable,
	})
}

func gitPositionalArguments(argv []string) []int {
	subcommand := gitSubcommandIndex(argv)
	if subcommand < 0 {
		return nil
	}
	var positionals []int
	for i := subcommand + 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			for j := i + 1; j < len(argv); j++ {
				positionals = append(positionals, j)
			}
			break
		}
		if strings.HasPrefix(arg, "-") {
			if (gitNetworkOptionTakesNextValue(arg) ||
				gitWriteOptionTakesNextValue(arg)) &&
				i+1 < len(argv) {
				i++
			}
			continue
		}
		positionals = append(positionals, i)
	}
	return positionals
}

func gitWriteOptionTakesNextValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	switch arg {
	case "--initial-branch", "--object-format", "--output",
		"--output-directory", "--ref-format", "--template":
		return true
	}
	return false
}

func classifyGoOptionTargets(
	a *analysis,
	argv []string,
	index int,
) {
	if len(argv) == 0 || basenameLower(argv[0]) != "go" {
		return
	}
	for _, option := range []string{
		"-o", "-blockprofile", "-coverprofile", "-cpuprofile",
		"-memprofile", "-mutexprofile", "-outputdir", "-trace",
	} {
		value, ok := goOptionValue(argv, index, option)
		if !ok || value == "" || value == "-" {
			continue
		}
		if cwd := goCommandDirectory(argv); cwd != "" &&
			isRelativePath(value) {
			value = normalizePath(cwd) + "/" + value
		}
		a.PathOps = append(a.PathOps, pathOp{
			Token:      value,
			Op:         "write",
			Executable: argv[0],
		})
	}
}

func goOptionValue(
	argv []string,
	index int,
	option string,
) (string, bool) {
	token := argv[index]
	if index > 0 && argv[index-1] == option {
		return token, true
	}
	if strings.HasPrefix(token, option+"=") {
		value := strings.TrimPrefix(token, option+"=")
		return value, value != ""
	}
	return "", false
}

func goCommandDirectory(argv []string) string {
	for i := 1; i < len(argv); i++ {
		switch {
		case argv[i] == "-C" && i+1 < len(argv):
			return argv[i+1]
		case strings.HasPrefix(argv[i], "-C="):
			return strings.TrimPrefix(argv[i], "-C=")
		case strings.HasPrefix(argv[i], "-C") && len(argv[i]) > 2:
			return argv[i][2:]
		case !strings.HasPrefix(argv[i], "-"):
			return ""
		}
	}
	return ""
}

func classifyCommandOptionPaths(
	a *analysis,
	argv []string,
	index int,
) {
	if len(argv) == 0 {
		return
	}
	switch basenameLower(argv[0]) {
	case "grep", "rg":
		value, ok := commandOptionValue(
			argv, index, "--file", 'f', "ABCDdefm",
		)
		if ok && isPathLike(value) {
			a.PathOps = append(a.PathOps, pathOp{
				Token:      value,
				Op:         "read",
				Executable: argv[0],
			})
		}
	}
}

func commandOptionValue(
	argv []string,
	index int,
	long string,
	short byte,
	optionsWithValue string,
) (string, bool) {
	token := argv[index]
	if index > 0 {
		previous := argv[index-1]
		if longOptionMatches(previous, long) {
			return token, true
		}
		if option, _, takesNext, ok := shortOptionValue(
			previous, short, optionsWithValue,
		); ok && option == short && takesNext {
			return token, true
		}
	}
	if name, value, ok := strings.Cut(token, "="); ok &&
		longOptionMatches(name, long) {
		return value, value != ""
	}
	option, value, takesNext, ok := shortOptionValue(
		token, short, optionsWithValue,
	)
	if !ok || option != short || takesNext {
		return "", false
	}
	return value, value != ""
}

func shortOptionValue(
	token string,
	target byte,
	optionsWithValue string,
) (option byte, value string, takesNext bool, ok bool) {
	if len(token) < 2 || token[0] != '-' ||
		strings.HasPrefix(token, "--") {
		return 0, "", false, false
	}
	for i := 1; i < len(token); i++ {
		option = token[i]
		if option == target {
			if i == len(token)-1 {
				return option, "", true, true
			}
			return option, token[i+1:], false, true
		}
		if strings.ContainsRune(optionsWithValue, rune(option)) {
			return 0, "", false, false
		}
	}
	return 0, "", false, false
}

func gitOptionValue(
	argv []string,
	index int,
	long string,
	short string,
) (string, bool) {
	token := argv[index]
	if index > 0 {
		previous := argv[index-1]
		if short != "" && previous == short {
			return token, true
		}
		if long != "" && (previous == long ||
			gitLongOptionAbbreviation(previous, long)) {
			return token, true
		}
	}
	if long != "" {
		if name, value, ok := strings.Cut(token, "="); ok &&
			(name == long || gitLongOptionAbbreviation(name, long)) {
			return value, value != ""
		}
	}
	if short != "" && strings.HasPrefix(token, short) &&
		len(token) > len(short) {
		return token[len(short):], true
	}
	return "", false
}

func gitWorkingTreeOperation(argv []string) string {
	switch gitSubcommand(argv) {
	case "clean":
		return "delete"
	case "am", "apply", "checkout", "cherry-pick", "clone",
		"merge", "rebase", "reset", "restore", "revert", "switch":
		return "write"
	}
	return "execute"
}

func classifyEmbeddedOptionTargets(
	a *analysis,
	argv []string,
	index int,
) {
	exec := argv[0]
	if basenameLower(exec) != "curl" {
		return
	}
	token := argv[index]
	if strings.HasPrefix(token, "--url=") {
		value := strings.TrimPrefix(token, "--url=")
		if target := extractNetworkTarget(value); target.Raw != "" {
			a.NetworkTargets = append(a.NetworkTargets, target)
		}
	}
	if value, ok := curlFileOperand(argv, index); ok && isPathLike(value) {
		a.PathOps = append(a.PathOps, pathOp{
			Token:      value,
			Op:         "read",
			Executable: exec,
		})
	}
	if value, ok := curlWriteOperand(argv, index); ok &&
		isPathLike(value) {
		a.PathOps = append(a.PathOps, pathOp{
			Token:      value,
			Op:         "write",
			Executable: exec,
		})
	}
	if value, ok := curlWriteOutValue(argv, index); ok {
		for _, match := range curlWriteOutPathRegex.FindAllStringSubmatch(
			value, -1,
		) {
			if len(match) < 2 {
				continue
			}
			target := strings.TrimPrefix(match[1], ">>")
			if !isPathLike(target) {
				continue
			}
			a.PathOps = append(a.PathOps, pathOp{
				Token:      target,
				Op:         "write",
				Executable: exec,
			})
		}
	}
}

func curlFileOperand(argv []string, index int) (string, bool) {
	token := argv[index]
	if index > 0 {
		if value, ok := curlFileOptionValue(
			argv[index-1], token,
		); ok {
			return value, true
		}
		if option, _, takesNext, ok := curlShortFileOption(
			argv[index-1],
		); ok && takesNext {
			return curlShortFileValue(option, token)
		}
	}
	if name, value, ok := strings.Cut(token, "="); ok {
		return curlFileOptionValue(name, value)
	}
	return curlShortFileOperand(token)
}

func curlFileOptionValue(option, value string) (string, bool) {
	switch option {
	case "-d":
		return trimAtFile(value)
	case "-F":
		return curlFormFile(value)
	case "-T":
		return value, value != "-"
	case "-H":
		return trimAtFile(value)
	}
	if matchesAnyLongOption(
		option, "--data", "--data-ascii", "--data-binary",
		"--data-raw", "--expand-data", "--json",
	) {
		return trimAtFile(value)
	}
	if matchesAnyLongOption(
		option, "--data-urlencode", "--url-query",
	) {
		return curlDataURLEncodeFile(value)
	}
	if matchesAnyLongOption(
		option, "--variable", "--expand-variable",
	) {
		return curlNamedAtFile(value)
	}
	if longOptionMatches(option, "--form") {
		return curlFormFile(value)
	}
	if longOptionMatches(option, "--upload-file") {
		return value, value != "-"
	}
	if matchesAnyLongOption(
		option, "--header", "--proxy-header",
	) {
		return trimAtFile(value)
	}
	return "", false
}

func curlShortFileOperand(token string) (string, bool) {
	option, value, takesNext, ok := curlShortFileOption(token)
	if !ok || takesNext {
		return "", false
	}
	return curlShortFileValue(option, value)
}

func curlShortFileOption(
	token string,
) (option byte, value string, takesNext bool, ok bool) {
	return curlShortPathOption(token, "dFTH")
}

func curlShortPathOption(
	token string,
	pathOptions string,
) (option byte, value string, takesNext bool, ok bool) {
	if len(token) < 2 || token[0] != '-' ||
		strings.HasPrefix(token, "--") {
		return 0, "", false, false
	}
	for i := 1; i < len(token); i++ {
		option = token[i]
		if strings.ContainsRune(pathOptions, rune(option)) {
			if i == len(token)-1 {
				return option, "", true, true
			}
			return option, token[i+1:], false, true
		}
		if strings.ContainsRune(
			curlShortOptionsWithValue, rune(option),
		) {
			return 0, "", false, false
		}
	}
	return 0, "", false, false
}

func curlShortFileValue(option byte, value string) (string, bool) {
	switch option {
	case 'd', 'H':
		return trimAtFile(value)
	case 'F':
		return curlFormFile(value)
	case 'T':
		return value, value != "-"
	}
	return "", false
}

func curlWriteOperand(argv []string, index int) (string, bool) {
	token := argv[index]
	if index > 0 {
		if value, ok := curlWriteOptionValue(
			argv[index-1], token,
		); ok {
			return value, true
		}
		if _, _, takesNext, ok := curlShortPathOption(
			argv[index-1], "cDo",
		); ok && takesNext {
			return token, token != "-"
		}
	}
	if name, value, ok := strings.Cut(token, "="); ok {
		return curlWriteOptionValue(name, value)
	}
	_, value, takesNext, ok := curlShortPathOption(token, "cDo")
	if !ok || takesNext {
		return "", false
	}
	return value, value != "-"
}

func curlWriteOptionValue(option, value string) (string, bool) {
	switch option {
	case "-c", "-D", "-o":
		return value, value != "-"
	}
	if matchesAnyLongOption(
		option, "--cookie-jar", "--dump-header", "--output",
		"--output-dir", "--trace", "--trace-ascii",
		"--etag-save", "--libcurl", "--stderr",
	) {
		return value, value != "-"
	}
	return "", false
}

var curlWriteOutPathRegex = regexp.MustCompile(
	`%output\{((?:>>)?[^}\r\n]+)\}`,
)

func curlWriteOutValue(argv []string, index int) (string, bool) {
	token := argv[index]
	if index > 0 {
		if argv[index-1] == "-w" ||
			longOptionMatches(argv[index-1], "--write-out") {
			return token, true
		}
		if option, _, takesNext, ok := curlShortPathOption(
			argv[index-1], "w",
		); ok && option == 'w' && takesNext {
			return token, true
		}
	}
	if name, value, ok := strings.Cut(token, "="); ok &&
		longOptionMatches(name, "--write-out") {
		return value, true
	}
	option, value, takesNext, ok := curlShortPathOption(token, "w")
	if !ok || option != 'w' || takesNext {
		return "", false
	}
	return value, true
}

func trimAtFile(value string) (string, bool) {
	if !strings.HasPrefix(value, "@") || len(value) == 1 {
		return "", false
	}
	return strings.TrimPrefix(value, "@"), true
}

func curlDataURLEncodeFile(value string) (string, bool) {
	at := strings.LastIndexByte(value, '@')
	if at < 0 || at+1 >= len(value) {
		return "", false
	}
	return value[at+1:], true
}

func curlNamedAtFile(value string) (string, bool) {
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at+1 >= len(value) {
		return "", false
	}
	return value[at+1:], true
}

func curlFormFile(value string) (string, bool) {
	eq := strings.IndexByte(value, '=')
	if eq < 0 || eq+2 >= len(value) {
		return "", false
	}
	file := value[eq+1:]
	if file[0] != '@' && file[0] != '<' {
		return "", false
	}
	file = file[1:]
	if semicolon := strings.IndexByte(file, ';'); semicolon >= 0 {
		file = file[:semicolon]
	}
	return file, file != ""
}

func shouldClassifyBareNetworkTarget(
	argv []string,
	index int,
	target networkTarget,
) bool {
	base := basenameLower(argv[0])
	switch base {
	case "git":
		if index != firstGitRemoteArgument(argv) {
			return false
		}
		if gitSubcommand(argv) != "clone" {
			return true
		}
		token := argv[index]
		return strings.ContainsAny(token, "@:") ||
			strings.HasPrefix(token, "[") ||
			strings.Contains(target.Host, ".") ||
			net.ParseIP(target.Host) != nil
	case "scp", "sftp":
		token := argv[index]
		return strings.ContainsAny(token, ":@") ||
			strings.HasPrefix(token, "[")
	case "ssh":
		return index == firstNetworkArgument(argv, sshOptionsWithValue)
	case "curl":
		return !isNetworkOptionValue(
			argv, index, curlOptionsWithValue,
			curlShortOptionsWithValue,
		)
	case "wget":
		return !isNetworkOptionValue(
			argv, index, wgetOptionsWithValue, "",
		)
	case "aria2", "aria2c":
		return !isNetworkOptionValue(
			argv, index, ariaOptionsWithValue, "",
		)
	}

	return target.Malformed || strings.Contains(target.Host, ".") ||
		net.ParseIP(target.Host) != nil
}

func classifyImplicitGitRemote(a *analysis, argv []string) {
	if len(argv) == 0 || basenameLower(argv[0]) != "git" {
		return
	}
	switch gitSubcommand(argv) {
	case "fetch", "pull", "push":
	default:
		return
	}
	if firstGitRemoteArgument(argv) >= 0 {
		return
	}
	a.NetworkTargets = append(a.NetworkTargets, networkTarget{
		Raw:       "git:configured-remote",
		Malformed: true,
	})
}

func firstGitRemoteArgument(argv []string) int {
	index := gitSubcommandIndex(argv)
	if index < 0 {
		return -1
	}
	for i := index + 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			if i+1 < len(argv) {
				return i + 1
			}
			return -1
		}
		if strings.HasPrefix(arg, "-") {
			if gitNetworkOptionTakesNextValue(arg) && i+1 < len(argv) {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

func gitNetworkOptionTakesNextValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	switch arg {
	case "-b", "--branch", "-c", "--config", "-j", "--jobs",
		"-o", "--origin", "-s", "--strategy", "-u",
		"--upload-pack", "--bundle-uri", "--deepen", "--depth",
		"--exec", "--filter", "--negotiation-tip",
		"--push-option", "--receive-pack", "--reference",
		"--reference-if-able", "--refmap", "--repo",
		"--revision", "--server-option", "--shallow-exclude",
		"--shallow-since", "--sort", "--strategy-option",
		"--separate-git-dir", "-X":
		return true
	}
	return false
}

func firstNetworkArgument(
	argv []string,
	optionsWithValue map[string]bool,
) int {
	for i := 1; i < len(argv); i++ {
		if isNetworkOptionValue(argv, i, optionsWithValue,
			sshShortOptionsWithValue) {
			continue
		}
		if !strings.HasPrefix(argv[i], "-") {
			return i
		}
	}
	return -1
}

func isNetworkOptionValue(
	argv []string,
	index int,
	optionsWithValue map[string]bool,
	shortOptionsWithValue string,
) bool {
	if index <= 0 {
		return false
	}
	if optionsWithValue[argv[index-1]] {
		return true
	}
	return bundledOptionTakesNextValue(
		argv[index-1], shortOptionsWithValue,
	)
}

func isNetworkOptionValueForCommand(argv []string, index int) bool {
	switch basenameLower(argv[0]) {
	case "ssh":
		return isNetworkOptionValue(
			argv, index, sshOptionsWithValue,
			sshShortOptionsWithValue,
		)
	case "curl":
		return isNetworkOptionValue(
			argv, index, curlOptionsWithValue,
			curlShortOptionsWithValue,
		)
	case "wget":
		return isNetworkOptionValue(
			argv, index, wgetOptionsWithValue, "",
		)
	case "aria2", "aria2c":
		return isNetworkOptionValue(
			argv, index, ariaOptionsWithValue, "",
		)
	}
	return false
}

func bundledOptionTakesNextValue(
	option string,
	valueOptions string,
) bool {
	if len(option) < 2 || option[0] != '-' ||
		strings.HasPrefix(option, "--") {
		return false
	}
	bundle := option[1:]
	for i := 0; i < len(bundle); i++ {
		if strings.ContainsRune(valueOptions, rune(bundle[i])) {
			return i == len(bundle)-1
		}
	}
	return false
}

func classifySSHOptionTargets(a *analysis, argv []string) {
	if len(argv) == 0 {
		return
	}
	switch basenameLower(argv[0]) {
	case "ssh", "scp", "sftp":
	default:
		return
	}
	for _, value := range sshOptionValues(argv, 'J') {
		appendSSHHosts(a, value)
	}
	for _, setting := range sshOptionValues(argv, 'o') {
		classifySSHSettingTarget(a, setting)
	}
}

func sshOptionValues(argv []string, option byte) []string {
	var values []string
	for i := 1; i < len(argv); i++ {
		value, takesNext, ok := bundledShortOptionValue(
			argv[i], option,
		)
		if !ok {
			continue
		}
		if takesNext {
			if i+1 >= len(argv) {
				continue
			}
			i++
			value = argv[i]
		}
		values = append(values, value)
	}
	return values
}

func bundledShortOptionValue(
	arg string,
	option byte,
) (value string, takesNext bool, ok bool) {
	if len(arg) < 2 || arg[0] != '-' ||
		strings.HasPrefix(arg, "--") {
		return "", false, false
	}
	bundle := arg[1:]
	index := strings.IndexByte(bundle, option)
	if index < 0 {
		return "", false, false
	}
	if index == len(bundle)-1 {
		return "", true, true
	}
	return bundle[index+1:], false, true
}

func parseSSHSetting(setting string) (string, string, bool) {
	if name, value, ok := strings.Cut(setting, "="); ok {
		return strings.TrimSpace(name), strings.TrimSpace(value), true
	}
	fields := strings.Fields(setting)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], strings.Join(fields[1:], " "), true
}

func classifySSHSettingTarget(a *analysis, setting string) {
	name, target, ok := parseSSHSetting(setting)
	if !ok {
		return
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hostname":
		appendSSHHosts(a, target)
	case "proxyjump":
		appendSSHHosts(a, target)
	}
}

func appendSSHHosts(a *analysis, value string) {
	for _, host := range strings.Split(value, ",") {
		host = strings.TrimSpace(host)
		if host == "" || strings.EqualFold(host, "none") {
			continue
		}
		target := extractNetworkTarget(host)
		if target.Raw != "" {
			a.NetworkTargets = append(a.NetworkTargets, target)
		}
	}
}

const (
	sshShortOptionsWithValue  = "BbcDEeFIiJLlmOoPpQRSWw"
	curlShortOptionsWithValue = "AbcdeFHoTuXxDwmECPQrtYyz"
	wgetShortOptionsWithValue = "aABDeiIlOoPQRtTUwWXYZ"
)

var sshOptionsWithValue = map[string]bool{
	"-B": true, "-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
	"-F": true, "-I": true, "-i": true, "-J": true, "-L": true,
	"-l": true, "-m": true, "-O": true, "-o": true, "-p": true,
	"-P": true, "-Q": true, "-R": true, "-S": true, "-W": true,
	"-w": true,
}

var curlOptionsWithValue = map[string]bool{
	"-A": true, "--user-agent": true, "-b": true, "--cookie": true,
	"-c": true, "--cookie-jar": true, "-d": true, "--data": true,
	"--data-raw": true, "--data-binary": true, "-e": true,
	"--referer": true, "-F": true, "--form": true, "-H": true,
	"--header": true, "-o": true, "--output": true, "-u": true,
	"--user": true, "-x": true, "--proxy": true,
	"--connect-to": true, "--resolve": true, "--unix-socket": true,
	"--abstract-unix-socket": true, "-X": true, "--request": true,
	"--cacert": true, "--cert": true, "--key": true, "-D": true,
	"--dump-header": true, "--output-dir": true, "-T": true,
	"--upload-file": true, "-w": true, "--write-out": true,
	"--form-string": true, "--json": true, "--max-time": true,
	"--connect-timeout": true, "--retry": true, "--retry-delay": true,
	"--retry-max-time": true, "--request-target": true,
	"--url-query": true, "--variable": true, "--expand-data": true,
	"--expand-variable": true,
}

var wgetOptionsWithValue = map[string]bool{
	"-O": true, "--output-document": true, "-o": true,
	"--output-file": true, "-P": true, "--directory-prefix": true,
	"--header": true, "--user-agent": true, "--post-data": true,
	"--post-file": true, "--user": true, "--password": true,
	"-e": true, "--execute": true,
}

var ariaOptionsWithValue = map[string]bool{
	"-d": true, "--dir": true, "-o": true, "--out": true,
	"--header": true, "--user-agent": true,
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
		"sparse-checkout", "multi-pack-index", "maintenance",
		"help", "version":
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

// isNetworkSubcommand returns true when the pipeline segment argv is a
// network-fetching invocation. For git, only clone/fetch/push/pull/
// ls-remote are network subcommands; status, diff, log, add, commit,
// etc. are local operations. The judgment is per-segment: callers pass
// the argv of the segment the token belongs to.
func isNetworkSubcommand(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if basenameLower(argv[0]) == "git" {
		switch gitSubcommand(argv) {
		case "clone", "fetch", "push", "pull", "ls-remote":
			return true
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
	if strings.HasPrefix(tok, `\`) {
		return true
	}
	if len(tok) >= 3 && tok[1] == ':' &&
		(tok[2] == '/' || tok[2] == '\\') {
		return true
	}
	if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
		return true
	}
	// .env, .aws/credentials, etc.
	if strings.HasPrefix(tok, ".") && len(tok) > 1 && tok[1] != '.' && tok[1] != '/' {
		return true
	}
	if strings.ContainsAny(tok, `/\`) {
		return true
	}
	return false
}

// opForCommand returns the operation context for a path-like token
// consumed by exec. `find` is normally a read operation, but `find -delete`
// and `find -exec rm` are destructive and are classified as delete.
func opForCommand(exec string) string {
	switch basenameLower(exec) {
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
		redacted = truncateRuneSafe(redacted, summaryMaxLen-3) + "..."
	}
	return redacted
}

// truncateRuneSafe cuts s to at most max bytes without splitting a
// multi-byte UTF-8 rune, mirroring limitString's rune safety.
func truncateRuneSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
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
// service names are hard-denied, while other bare hosts are reported as
// ambiguous so the network rule can apply the allowlist or deny.
func extractNetworkTarget(tok string) networkTarget {
	t := networkTarget{Raw: tok}
	if looksLikeURL(tok) {
		_, host, scheme, err := parseURL(tok)
		if err != nil {
			t.Malformed = true
			return t
		}
		t.Host = host
		t.Scheme = scheme
		return t
	}
	// Bare host:port or host/path arguments to ssh/scp/sftp, including
	// SCP-like user@host:path forms (git clone git@github.com:org/repo):
	// the user prefix is not part of the host being matched.
	host := tok
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end > 0 {
			suffix := host[end+1:]
			if suffix != "" && suffix[0] != ':' && suffix[0] != '/' {
				t.Malformed = true
			}
			host = host[1:end]
		} else {
			t.Malformed = true
		}
	} else if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	// A single trailing dot (the DNS root label in an FQDN) does not
	// change the domain being matched.
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return networkTarget{}
	}
	// Bare hosts are scanned: localhost, loopback/metadata IPs, and
	// ambiguous targets are hard-denied by the network rule. Bare hosts
	// that contain a dot are treated as domain candidates. Bare hosts
	// without a dot (localhost, a subcommand argument like "status", or
	// an internal host) are marked malformed so the network rule can
	// deny or ask rather than silently allow.
	t.Host = host
	t.Scheme = ""
	if isAmbiguousHost(host) || !strings.Contains(host, ".") {
		t.Malformed = true
	}
	return t
}
