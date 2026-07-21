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
	if !p.Rules.DangerousCommands.Enabled {
		return nil
	}
	var out []Finding

	// dangerous_delete: inspect argv for rm -rf, rm -fr, rm --recursive,
	// rm --force, and equivalent destructive utilities (dd of=/dev/...,
	// mkfs, shred). We look at the parsed pipeline when available; on a
	// parse failure we still inspect the raw source string.
	if hasDangerousDelete(a) {
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
		}
	}

	return out
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
		if execPayloadIsDangerous(execPayload(argv[i+1:])) {
			return true
		}
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
	clean := strings.TrimRight(p, "/")
	switch clean {
	case "", "/", "/etc", "/usr", "/bin", "/sbin", "/boot", "/proc",
		"/sys", "/root", "/lib", "/lib64", "/var", "/dev",
		"/run", "/var/run", "/run/secrets", "/var/run/secrets":
		return true
	}
	// /proc/<pid>/environ and /proc/self/environ are system paths.
	if strings.HasPrefix(clean, "/proc/") && strings.HasSuffix(clean, "/environ") {
		return true
	}
	// /run/secrets/* and /var/run/secrets/* are runtime secret mounts.
	if strings.HasPrefix(clean, "/run/secrets/") || strings.HasPrefix(clean, "/var/run/secrets/") {
		return true
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
	}
	// Empty action: fall back to the risk threshold.
	threshold := p.thresholdFor(risk)
	if threshold == "" {
		return DecisionDeny
	}
	return threshold
}
