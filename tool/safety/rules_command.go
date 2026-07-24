//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ruleCommand evaluates command allow/deny and dangerous-delete rules.
// It uses shellsafe.PolicyFromLists(...).Check(pipe) for the allow/deny
// semantics so the guard does not duplicate shellsafe's matching rules.
//
// Rule ids emitted by this rule:
//
//   - command.not_allowed       executable outside allow or in deny.
//   - command.dangerous_delete  recursive/forced delete or destructive utility.
func ruleCommand(a *analysis, p Policy) []Finding {
	var out []Finding

	// dangerous_delete: inspect argv for rm -rf, rm -fr, rm --recursive,
	// rm --force, and equivalent destructive utilities (dd of=/dev/...,
	// mkfs, shred). We look at the parsed pipeline when available; on a
	// parse failure we still inspect the raw source string.
	//
	// Only this heuristic is gated on DangerousCommands.Enabled. The
	// shellsafe allow/deny check below runs unconditionally because it
	// is the only enforcement of Policy.AllowedCommands/DeniedCommands;
	// disabling the heuristic must not disable the operator's explicit
	// command allowlist.
	if p.Rules.DangerousCommands.Enabled && hasDangerousDelete(a) {
		out = append(out, Finding{
			RuleID:         "command.dangerous_delete",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskCritical, p),
			Evidence:       "recursive/forced delete or destructive utility",
			Recommendation: "Refuse recursive or forced deletion; require an explicit allowlist entry for the specific path",
		})
	}

	// shellsafe allow/deny check on the parsed pipeline. The shellsafe
	// layer is the authoritative gate; we surface its decision as a
	// command.not_allowed finding so the audit trail records the rule
	// id. shellsafe also enforces the implicit deny set (sh, eval, ...),
	// which the shell_bypass rule separately tags.
	//
	// When the dependency rule is enabled with an explicit Action of
	// DecisionAsk and the executable is a package manager running an
	// install subcommand, the dependency rule takes precedence and we
	// suppress command.not_allowed so the audit focuses on the
	// dependency approval rather than the missing allowlist entry.
	if a.Pipeline != nil {
		sp := shellPolicy(p)
		if sp.Active() {
			if err := sp.Check(a.Pipeline); err != nil {
				risk := RiskHigh
				evidence := redactedSnippet(err.Error(), 80)
				if isShellsafeImplicitDeny(err) {
					risk = RiskHigh
				}
				if !dependencyRuleOverridesCommand(a, p) {
					out = append(out, Finding{
						RuleID:         "command.not_allowed",
						RiskLevel:      risk,
						Decision:       ruleDecision(p.Rules.DangerousCommands.Action, risk, p),
						Evidence:       evidence,
						Recommendation: "Use a command from the allowed_commands list or extend the policy explicitly",
					})
				}
			}
			if err := nestedCommandPolicyError(a.Pipeline, sp); err != nil {
				out = append(out, Finding{
					RuleID:         "command.not_allowed",
					RiskLevel:      RiskHigh,
					Decision:       DecisionDeny,
					Evidence:       redactedSnippet(err.Error(), 80),
					Recommendation: "Remove nested command execution or allow the nested command explicitly",
				})
			}
		}
	} else if a.ParseError != nil && shellPolicy(p).Active() {
		out = append(out, Finding{
			RuleID:         "command.not_allowed",
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "command could not be parsed for allowlist enforcement",
			Recommendation: "Use a structurally parseable command or an auditable workspace script",
		})
	}

	return out
}

// nestedCommandPolicyError applies the executable policy to command
// payloads hidden under otherwise allowed command runners.
func nestedCommandPolicyError(
	pipeline *shellsafe.Pipeline,
	policy shellsafe.Policy,
) error {
	for _, argv := range pipeline.Commands {
		if len(argv) == 0 {
			continue
		}
		if basenameLower(argv[0]) == "find" {
			for i := 0; i+1 < len(argv); i++ {
				switch argv[i] {
				case "-exec", "-execdir", "-ok", "-okdir":
				default:
					continue
				}
				payload := execPayload(argv[i+1:])
				if len(payload) == 0 {
					continue
				}
				if err := policy.Check(&shellsafe.Pipeline{
					Commands: [][]string{payload},
				}); err != nil {
					return fmt.Errorf("nested find command is not allowed: %w", err)
				}
				if gitHasShellAlias(payload) {
					return fmt.Errorf(
						"nested find git command configures an external command",
					)
				}
				if gitUsesExternalSubcommand(payload) {
					return fmt.Errorf(
						"nested find external git subcommand is not allowed",
					)
				}
				i += len(payload)
			}
		}
		if gitHasShellAlias(argv) {
			return fmt.Errorf("git shell alias is not allowed")
		}
		if gitUsesExternalSubcommand(argv) {
			return fmt.Errorf("external git subcommand is not allowed")
		}
	}
	return nil
}

func gitUsesExternalSubcommand(argv []string) bool {
	if len(argv) == 0 || basenameLower(argv[0]) != "git" {
		return false
	}
	subcommand := gitSubcommand(argv)
	return subcommand != "" && !isGitSubcommandName(subcommand)
}

// gitHasShellAlias reports whether argv configures Git to execute an
// external command, either for this invocation or persistently.
func gitHasShellAlias(argv []string) bool {
	if len(argv) == 0 || basenameLower(argv[0]) != "git" {
		return false
	}
	if gitConfigCommandExecutesCommand(argv) {
		return true
	}
	for i := 1; i < len(argv); i++ {
		if argv[i] == "--" {
			break
		}
		if gitProgramFlag(argv, i) {
			return true
		}
		if gitConfigEnvExecutesCommand(argv, &i) {
			return true
		}
		if setting, ok := gitCloneConfigSetting(argv, &i); ok {
			name, value, valid := strings.Cut(setting, "=")
			if !valid || gitConfigExecutesCommand(name, value) {
				return true
			}
			continue
		}
		var setting string
		switch {
		case argv[i] == "-c" && i+1 < len(argv):
			setting = argv[i+1]
			i++
		case strings.HasPrefix(argv[i], "-c") && len(argv[i]) > 2:
			setting = argv[i][2:]
		}
		name, value, ok := strings.Cut(setting, "=")
		if ok && gitConfigExecutesCommand(name, value) {
			return true
		}
	}
	return false
}

func gitCloneConfigSetting(
	argv []string,
	index *int,
) (string, bool) {
	if gitSubcommand(argv) != "clone" {
		return "", false
	}
	arg := argv[*index]
	if (arg == "--config" ||
		gitLongOptionAbbreviation(arg, "--config")) &&
		!strings.Contains(arg, "=") {
		if *index+1 >= len(argv) {
			return "", true
		}
		*index++
		return argv[*index], true
	}
	if name, value, ok := strings.Cut(arg, "="); ok &&
		(name == "--config" ||
			gitLongOptionAbbreviation(name, "--config")) {
		return value, true
	}
	return "", false
}

func gitConfigCommandExecutesCommand(argv []string) bool {
	index := gitSubcommandIndex(argv)
	if index < 0 || !strings.EqualFold(argv[index], "config") {
		return false
	}
	args := argv[index+1:]
	readOnly := false
	for _, arg := range args {
		if gitLongOptionAbbreviation(arg, "--edit") {
			return true
		}
		switch arg {
		case "--get", "--get-all", "--get-regexp", "--get-urlmatch",
			"--list", "-l", "--show-origin", "--show-scope",
			"--get-color", "--get-colorbool":
			readOnly = true
		case "--edit", "-e", "edit":
			return true
		}
	}
	if readOnly {
		return false
	}
	for i, arg := range args {
		name := strings.TrimSpace(arg)
		if gitConfigNameCanExecute(name) && i+1 < len(args) {
			return gitConfigExecutesCommand(name, args[i+1])
		}
		if (arg == "--rename-section" ||
			gitLongOptionAbbreviation(arg, "--rename-section") ||
			arg == "rename-section") &&
			i+2 < len(args) &&
			gitConfigSectionCanExecute(args[i+2]) {
			return true
		}
	}
	return false
}

func gitConfigSectionCanExecute(section string) bool {
	section = strings.ToLower(strings.TrimSpace(section))
	switch section {
	case "alias", "core", "credential", "gpg", "include",
		"protocol", "sequence":
		return true
	}
	return strings.HasPrefix(section, "diff") ||
		strings.HasPrefix(section, "difftool") ||
		strings.HasPrefix(section, "filter") ||
		strings.HasPrefix(section, "http") ||
		strings.HasPrefix(section, "merge") ||
		strings.HasPrefix(section, "remote") ||
		strings.HasPrefix(section, "url")
}

func gitProgramFlag(argv []string, index int) bool {
	arg := argv[index]
	if strings.HasPrefix(arg, "--exec-path=") &&
		strings.TrimPrefix(arg, "--exec-path=") != "" {
		return true
	}
	if gitLongOptionAbbreviation(arg, "--exec-path") &&
		strings.Contains(arg, "=") {
		return true
	}
	switch arg {
	case "--upload-pack", "--receive-pack", "--exec",
		"--extcmd":
		return index+1 < len(argv)
	case "-u":
		return gitSubcommand(argv) == "clone" && index+1 < len(argv)
	case "-x":
		return gitSubcommand(argv) == "difftool" && index+1 < len(argv)
	}
	if gitSubcommand(argv) == "difftool" &&
		strings.HasPrefix(arg, "-x") && len(arg) > 2 {
		return true
	}
	for _, prefix := range []string{
		"--upload-pack=", "--receive-pack=", "--exec=", "--extcmd=",
	} {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	for _, option := range []string{
		"--upload-pack", "--receive-pack", "--exec", "--extcmd",
	} {
		if gitLongOptionAbbreviation(arg, option) {
			if strings.Contains(arg, "=") {
				return true
			}
			return index+1 < len(argv)
		}
	}
	return gitSubcommand(argv) == "clone" &&
		strings.HasPrefix(arg, "-u") && len(arg) > 2
}

func gitConfigEnvExecutesCommand(argv []string, index *int) bool {
	arg := argv[*index]
	var setting string
	switch {
	case (arg == "--config-env" ||
		gitLongOptionAbbreviation(arg, "--config-env")) &&
		!strings.Contains(arg, "=") &&
		*index+1 < len(argv):
		*index++
		setting = argv[*index]
	case (strings.HasPrefix(arg, "--config-env=") ||
		gitLongOptionAbbreviation(arg, "--config-env")) &&
		strings.Contains(arg, "="):
		_, setting, _ = strings.Cut(arg, "=")
	default:
		return false
	}
	name, _, ok := strings.Cut(setting, "=")
	if !ok {
		return true
	}
	return gitConfigNameCanExecute(name)
}

func gitLongOptionAbbreviation(arg, full string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return len(name) >= len("--xxx") &&
		name != full &&
		strings.HasPrefix(full, name)
}

func gitSubcommand(argv []string) string {
	index := gitSubcommandIndex(argv)
	if index < 0 {
		return ""
	}
	return strings.ToLower(argv[index])
}

func gitSubcommandIndex(argv []string) int {
	optionsWithValue := map[string]bool{
		"-C": true, "-c": true, "--git-dir": true,
		"--work-tree": true, "--namespace": true,
		"--super-prefix": true, "--config-env": true,
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if optionsWithValue[arg] && i+1 < len(argv) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-C") && len(arg) > 2 ||
			strings.HasPrefix(arg, "--git-dir=") ||
			strings.HasPrefix(arg, "--work-tree=") ||
			strings.HasPrefix(arg, "--namespace=") ||
			strings.HasPrefix(arg, "--super-prefix=") ||
			strings.HasPrefix(arg, "--config-env=") {
			continue
		}
		if arg == "--" {
			if i+1 < len(argv) {
				return i + 1
			}
			return -1
		}
		if !strings.HasPrefix(arg, "-") {
			return i
		}
	}
	return -1
}

func gitConfigExecutesCommand(name, value string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	if strings.HasPrefix(name, "alias.") {
		return strings.HasPrefix(value, "!")
	}
	if name == "protocol.allow" ||
		strings.HasPrefix(name, "protocol.") &&
			strings.HasSuffix(name, ".allow") {
		return !strings.EqualFold(value, "never")
	}
	return value != "" && gitConfigNameCanExecute(name)
}

var gitCommandConfigNames = map[string]bool{
	"core.sshcommand":        true,
	"core.gitproxy":          true,
	"core.fsmonitor":         true,
	"core.editor":            true,
	"core.hookspath":         true,
	"core.pager":             true,
	"core.askpass":           true,
	"sequence.editor":        true,
	"gpg.program":            true,
	"diff.external":          true,
	"interactive.difffilter": true,
	"credential.helper":      true,
	"include.path":           true,
	"protocol.ext.allow":     true,
	"protocol.allow":         true,
	"http.proxy":             true,
	"https.proxy":            true,
	"http.curloptresolve":    true,
	"http.followredirects":   true,
}

func gitConfigNameCanExecute(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if gitCommandConfigNames[name] {
		return true
	}
	if strings.HasPrefix(name, "alias.") {
		return true
	}
	return gitDiffConfigCanExecute(name) ||
		gitUIConfigCanExecute(name) ||
		gitTransportConfigCanExecute(name)
}

func gitDiffConfigCanExecute(name string) bool {
	if strings.HasPrefix(name, "diff.") &&
		hasAnySuffix(name, ".command", ".textconv") {
		return true
	}
	if strings.HasPrefix(name, "difftool.") &&
		hasAnySuffix(name, ".cmd", ".path") {
		return true
	}
	if strings.HasPrefix(name, "merge.") &&
		strings.HasSuffix(name, ".driver") {
		return true
	}
	if strings.HasPrefix(name, "mergetool.") &&
		hasAnySuffix(name, ".cmd", ".path") {
		return true
	}
	if strings.HasPrefix(name, "filter.") {
		return hasAnySuffix(name, ".clean", ".smudge", ".process")
	}
	return false
}

func gitUIConfigCanExecute(name string) bool {
	if strings.HasPrefix(name, "credential.") {
		return strings.HasSuffix(name, ".helper")
	}
	if strings.HasPrefix(name, "gpg.") {
		return strings.HasSuffix(name, ".program")
	}
	if strings.HasPrefix(name, "pager.") {
		return true
	}
	if strings.HasPrefix(name, "browser.") ||
		strings.HasPrefix(name, "man.") {
		return strings.HasSuffix(name, ".cmd")
	}
	if strings.HasPrefix(name, "tar.") {
		return strings.HasSuffix(name, ".command")
	}
	if strings.HasPrefix(name, "submodule.") {
		return strings.HasSuffix(name, ".update")
	}
	return false
}

func gitTransportConfigCanExecute(name string) bool {
	if strings.HasPrefix(name, "protocol.") {
		return strings.HasSuffix(name, ".allow")
	}
	if strings.HasPrefix(name, "includeif.") {
		return strings.HasSuffix(name, ".path")
	}
	if strings.HasPrefix(name, "url.") {
		return hasAnySuffix(name, ".insteadof", ".pushinsteadof")
	}
	if strings.HasPrefix(name, "http.") {
		return hasAnySuffix(
			name, ".proxy", ".curloptresolve", ".followredirects",
		)
	}
	if strings.HasPrefix(name, "remote.") {
		return hasAnySuffix(
			name, ".proxy", ".uploadpack", ".receivepack", ".vcs",
		)
	}
	return false
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

// dependencyRuleOverridesCommand returns true when the dependency rule
// is enabled with DecisionAsk action and the analysis shows a package
// manager install command. In that case the dependency rule's explicit
// ask action takes precedence over the command rule's threshold-based
// deny, matching the plan's "rule action override before risk threshold"
// semantics.
func dependencyRuleOverridesCommand(a *analysis, p Policy) bool {
	if !p.Rules.Dependencies.Enabled {
		return false
	}
	if p.Rules.Dependencies.Action != DecisionAsk {
		return false
	}
	return a.InstallPackages
}

// hasDangerousDelete returns true when the analysis shows a recursive or
// forced delete, or a destructive utility targeting a system path.
func hasDangerousDelete(a *analysis) bool {
	if a == nil {
		return false
	}
	if a.Pipeline != nil {
		for _, argv := range a.Pipeline.Commands {
			if pipelineSegmentIsDangerous(argv) {
				return true
			}
		}
		// The command parsed successfully and no segment is dangerous;
		// the raw-source scan is a fallback for parse failures only, so
		// quoted literals like `echo "rm -rf /"` are not flagged.
		return false
	}
	return rawSourceHasDangerousDelete(a.Source)
}

// pipelineSegmentIsDangerous inspects one parsed argv for rm -rf, dd of=,
// mkfs, shred, and find -delete patterns.
func pipelineSegmentIsDangerous(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	base := basenameLower(argv[0])
	if isDangerousBaseCommand(base, argv) {
		return true
	}
	if isDangerousFindCommand(argv) {
		return true
	}
	if strings.HasPrefix(base, "mkfs.") {
		return true
	}
	return false
}

// isDangerousBaseCommand checks rm, dd, mkfs, shred.
func isDangerousBaseCommand(base string, argv []string) bool {
	if base == "git" && gitSubcommand(argv) == "clean" {
		return gitCleanDeletes(argv)
	}
	switch base {
	case "rm":
		return hasRecursiveFlag(argv) && hasForceOrRootTarget(argv) || targetsRootPath(argv)
	case "dd":
		return hasFlagPrefix(argv, "of=/dev/")
	case "mkfs":
		return true
	case "shred":
		return hasRecursiveFlag(argv)
	}
	return false
}

func gitCleanDeletes(argv []string) bool {
	force := false
	dryRun := false
	interactive := false
	index := gitSubcommandIndex(argv)
	for i := index + 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		if arg == "--force" {
			force = true
			continue
		}
		if arg == "--dry-run" {
			dryRun = true
			continue
		}
		if arg == "--no-dry-run" {
			dryRun = false
			continue
		}
		if arg == "--interactive" {
			interactive = true
			continue
		}
		if strings.HasPrefix(arg, "-e") {
			if arg == "-e" {
				i++
			}
			continue
		}
		if len(arg) > 1 && arg[0] == '-' && arg[1] != '-' {
			force = force || strings.ContainsRune(arg[1:], 'f')
			dryRun = dryRun || strings.ContainsRune(arg[1:], 'n')
			interactive = interactive ||
				strings.ContainsRune(arg[1:], 'i')
		}
	}
	return (force || interactive) && !dryRun
}

// isDangerousFindCommand checks find -delete and destructive find -exec
// payloads (including commands nested under shell wrappers).
func isDangerousFindCommand(argv []string) bool {
	if basenameLower(argv[0]) != "find" {
		return false
	}
	if hasFlag(argv, "-delete", "--delete") {
		return true
	}
	return findHasDestructiveExec(argv)
}

// findHasDestructiveExec checks for -exec/-execdir/-ok/-okdir payloads
// that hide a destructive or denied command. The complete payload is
// analyzed as a nested command: the executable is checked against the
// destructive-binary set and the shell-wrapper implicit deny set, its
// arguments against the destructive-argument checks, and its tokens
// against the raw destructive-source patterns.
func findHasDestructiveExec(argv []string) bool {
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		payload := execPayload(argv[i+1:])
		if execPayloadIsDangerous(payload) {
			return true
		}
		// Skip the consumed payload so a token like "-exec" inside it
		// is not re-evaluated as a new find flag.
		i += len(payload)
	}
	return false
}

// execPayload returns the command tokens of a find -exec clause,
// stopping at the + or ; terminator.
func execPayload(tokens []string) []string {
	for j, tok := range tokens {
		if tok == "+" || tok == ";" {
			return tokens[:j]
		}
	}
	return tokens
}

// execPayloadIsDangerous analyzes one find -exec payload as a nested
// command. A bare rm/shred/dd is destructive regardless of its
// arguments; a shell wrapper or command runner (sh -c, bash -c, env,
// xargs, sudo, ...) can re-exec an arbitrary denied command under the
// allowed find argv[0], so it is denied via the same implicit deny set
// the shellsafe layer applies to pipeline segments. The remaining
// payloads are checked for destructive arguments (dd of=/dev/...,
// mkfs, nested find -delete) and destructive source patterns
// (python -c 'shutil.rmtree(...)' and similar interpreter payloads).
func execPayloadIsDangerous(payload []string) bool {
	if len(payload) == 0 {
		return false
	}
	switch basenameLower(payload[0]) {
	case "rm", "shred", "dd":
		return true
	}
	if isWrapperName(payload[0]) {
		return true
	}
	if pipelineSegmentIsDangerous(payload) {
		return true
	}
	for _, tok := range payload {
		if rawSourceHasDangerousDelete(tok) {
			return true
		}
	}
	return false
}

// rawSourceHasDangerousDelete does a best-effort scan of the raw source
// when shellsafe parsing failed. We accept some false-positive risk on
// unparsable commands because they are already high-risk; the
// parse-failure rule will also fire.
func rawSourceHasDangerousDelete(src string) bool {
	if src == "" {
		return false
	}
	low := strings.ToLower(src)
	if strings.Contains(low, "rm -rf") || strings.Contains(low, "rm -fr") ||
		strings.Contains(low, "rm --recursive --force") {
		return true
	}
	if strings.Contains(low, "rm -rf /") || strings.Contains(low, "rm -rf /*") {
		return true
	}
	if strings.Contains(low, "find / -delete") || strings.Contains(low, "find . -delete") {
		return true
	}
	if strings.Contains(low, "shutil.rmtree") || strings.Contains(low, "os.remove(") {
		return true
	}
	return false
}

// hasRecursiveFlag returns true when argv contains -r, -R, --recursive,
// possibly combined with other short flags (-rf, -fr).
func hasRecursiveFlag(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "-r" || a == "-R" || a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			if strings.ContainsAny(a, "rR") {
				return true
			}
		}
		if strings.HasPrefix(a, "--recursive=") {
			return true
		}
	}
	return false
}

// hasForceOrRootTarget returns true when argv contains -f, --force, or
// targets a root/system path.
func hasForceOrRootTarget(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "-f" || a == "--force" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			if strings.Contains(a, "f") {
				return true
			}
		}
		if isRootOrSystemPath(a) {
			return true
		}
	}
	return false
}

// targetsRootPath returns true when any argv token is a root/system path.
func targetsRootPath(argv []string) bool {
	for _, a := range argv[1:] {
		if isRootOrSystemPath(a) {
			return true
		}
	}
	return false
}

// isRootOrSystemPath returns true for /, /etc, /usr, /bin, /sbin, /boot,
// /proc, /sys, /root, /run, /var/run, and equivalents. The root path
// "/" normalizes to "" after TrimRight and is matched explicitly.
// /proc and /run are included because /proc/self/environ and
// /run/secrets/* expose credentials and environment secrets.
func isRootOrSystemPath(p string) bool {
	clean := path.Clean(strings.ReplaceAll(
		filepath.ToSlash(p), `\`, `/`,
	))
	if clean == "." || clean == "" {
		return false
	}
	low := strings.ToLower(clean)
	if low == "/" {
		return true
	}
	if isWindowsSystemPath(low) {
		return true
	}
	for _, root := range []string{
		"/etc", "/usr", "/bin", "/sbin", "/boot", "/proc",
		"/sys", "/root", "/lib", "/lib64", "/var", "/dev", "/run",
		"/opt", "/srv", "/system", "/library", "/applications",
		"/private/etc", "/private/var",
	} {
		if low == root || isDescendant(low, root) {
			return true
		}
	}
	return false
}

func isWindowsSystemPath(clean string) bool {
	if len(clean) < 3 || clean[1] != ':' || clean[2] != '/' {
		return false
	}
	rest := strings.ToLower(clean[2:])
	if rest == "/" {
		return true
	}
	for _, root := range []string{
		"/windows", "/program files", "/program files (x86)",
		"/programdata", "/system volume information",
	} {
		if rest == root || isDescendant(rest, root) {
			return true
		}
	}
	return false
}

// isShellsafeImplicitDeny returns true when err is a shellsafe implicit
// deny (wrapper) error. The shellsafe package formats these with the
// phrase "shell wrapper or re-executing builtin".
func isShellsafeImplicitDeny(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "shell wrapper or re-executing builtin")
}

// ruleDecision applies the rule action override first, then the risk
// threshold. A critical finding always denies. For non-critical rules,
// an explicit allow action returns allow (the operator has chosen to
// accept that risk category); an explicit deny or ask action returns
// that action directly. When the action is empty, the risk threshold
// decides.
func ruleDecision(action Decision, risk RiskLevel, p Policy) Decision {
	if risk == RiskCritical {
		// Critical rules cannot be allowed or asked regardless of the
		// configured action; the safety invariant is that critical
		// findings always deny.
		return DecisionDeny
	}
	switch action {
	case DecisionAllow:
		return DecisionAllow
	case DecisionDeny:
		return DecisionDeny
	case DecisionAsk:
		return DecisionAsk
	case DecisionInherit:
		// Fall through to the risk threshold below.
	}
	// Empty action: fall back to the risk threshold.
	threshold := p.thresholdFor(risk)
	if threshold == "" {
		return DecisionDeny
	}
	return threshold
}
