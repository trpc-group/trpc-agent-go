//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strconv"
	"strings"
)

// Rule identifiers. These are stable and appear in reports, audit records and
// OpenTelemetry attributes.
const (
	RuleDangerousDelete    = "cmd.dangerous_delete"
	RuleDangerousPerms     = "cmd.dangerous_perms"
	RuleDeniedCommand      = "cmd.denied"
	RuleNotAllowed         = "cmd.not_allowed"
	RuleReadSecret         = "fs.read_secret"
	RuleOverwriteSystem    = "fs.overwrite_system"
	RuleNetNonWhitelist    = "net.non_whitelist"
	RuleNetAllowedDomain   = "net.allowed_domain"
	RuleNetUnknownTarget   = "net.unknown_target"
	RuleReverseShell       = "net.reverse_shell"
	RuleUnsafeConstruct    = "shell.unsafe_construct"
	RuleInterpreterInline  = "shell.interpreter_inline"
	RuleCommandRunner      = "shell.command_runner"
	RuleHostLongSession    = "host.long_session"
	RuleHostPrivilege      = "host.privilege"
	RuleDependencyInstall  = "deps.install"
	RuleEnvMutation        = "env.mutation"
	RuleEnvNotWhitelisted  = "env.not_whitelisted"
	RuleTimeoutExceeds     = "res.timeout_exceeds"
	RuleOutputFlood        = "res.output_flood"
	RulePythonDangerousAPI = "code.dangerous_api"
	RuleUnparseableArgs    = "args.unparseable"
)

var (
	forkBombRe     = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|[^}]*&[^}]*\}`)
	reverseShellRe = regexp.MustCompile(`(?i)(/dev/tcp/|/dev/udp/|\bnc\b[^|;]*\s-e\b|\bncat\b[^|;]*\s-e\b|\bbash\s+-i\b|\bsh\s+-i\b|\bmkfifo\b)`)
	pyDangerRe     = regexp.MustCompile(`(?i)(os\.system|subprocess\.(Popen|call|run|check_output)|pty\.spawn|\beval\(|\bexec\()`)
	pyShellCallRe  = regexp.MustCompile(`(?is)(?:os\.system|subprocess\.(?:Popen|call|run|check_output)|commands\.getoutput|pty\.spawn)\s*\(([^)]*)\)`)
	quotedRe       = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)
)

// extractForeignCommands pulls candidate shell command strings out of foreign
// (e.g. Python) code that shells out via os.system/subprocess, joining the
// quoted arguments so the embedded command can be scanned with the full
// command rule set rather than only flagged as a dangerous API.
func extractForeignCommands(code string) []string {
	var cmds []string
	for _, call := range pyShellCallRe.FindAllStringSubmatch(code, -1) {
		var parts []string
		for _, q := range quotedRe.FindAllStringSubmatch(call[1], -1) {
			lit := q[1]
			if lit == "" {
				lit = q[2]
			}
			if lit != "" {
				parts = append(parts, lit)
			}
		}
		if len(parts) > 0 {
			cmds = append(cmds, strings.Join(parts, " "))
		}
	}
	return cmds
}

var systemDirs = []string{
	"/etc", "/usr", "/bin", "/sbin", "/var", "/boot", "/lib", "/lib64",
	"/sys", "/root", "/opt", "/dev",
}

var interpreters = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "ash": {}, "dash": {}, "ksh": {},
	"mksh": {}, "fish": {}, "pwsh": {}, "powershell": {}, "cmd": {},
	"eval": {}, "exec": {}, "source": {},
}

var dashCInterpreters = map[string]struct{}{
	"python": {}, "python3": {}, "perl": {}, "node": {}, "ruby": {}, "php": {},
}

// commandRunners are process-runner / wrapper commands that take another
// command as an argument and exec it under their own argv[0]. Because the
// dangerous command sits in the arguments, argv[0]-based rules (network,
// dangerous-delete, ...) never see it — e.g. "env curl http://evil",
// "xargs curl ...", "timeout 5 curl ...", "busybox sh -c ...". This set
// mirrors internal/shellsafe's implicit-deny list so the guard fails closed
// on the same wrappers shellsafe.CheckCommand blocks; wrap the intended use
// in an audited workspace script and allow that instead. sudo/su/doas are
// covered by the more specific privilege rule and intentionally omitted here.
var commandRunners = map[string]struct{}{
	"env": {}, "xargs": {}, "timeout": {}, "nohup": {}, "nice": {},
	"ionice": {}, "taskset": {}, "stdbuf": {}, "setsid": {}, "unshare": {},
	"chroot": {}, "runuser": {}, "time": {}, "strace": {}, "ltrace": {},
	"busybox": {}, "toybox": {}, "script": {}, "flock": {},
}

// segmentFindings evaluates the rules that apply to a single parsed pipeline
// segment (one argv). line is the source line used for the Segment field.
func (s *Scanner) segmentFindings(argv []string, line string, in ScanInput) []Finding {
	if len(argv) == 0 {
		return nil
	}
	var out []Finding
	base := commandBase(argv[0])
	seg := strings.Join(argv, " ")

	// 1. Dangerous filesystem destruction.
	out = append(out, s.dangerousDelete(base, argv, seg, line)...)
	// 2. Secret / credential path access.
	out = append(out, s.deniedPathAccess(argv, seg, line)...)
	// 3. Overwrite of system paths by a writer command.
	out = append(out, s.overwriteSystem(base, argv, seg, line)...)
	// 4. Denied command names. rm is also on the denied list; dangerousDelete
	// adds a more severe finding for the -rf form, but a plain denied rm must
	// still be flagged here.
	if _, denied := s.policy.deniedCmdSet[base]; denied {
		out = append(out, s.finding(RuleDeniedCommand, CategoryDangerousCommand,
			RiskHigh, DecisionDeny, seg, line,
			"Command is on the denied list; remove it or wrap it in an audited script."))
	}
	// 5. Network egress.
	out = append(out, s.network(base, argv, seg, line, in)...)
	// 6. Shell interpreters / inline code execution.
	out = append(out, s.interpreterInline(base, argv, seg, line)...)
	// 6b. Process-runner / wrapper commands that exec an arbitrary sub-command.
	if _, ok := commandRunners[base]; ok {
		out = append(out, s.finding(RuleCommandRunner, CategoryShellBypass,
			RiskHigh, DecisionDeny, seg, line,
			"Command runner/wrapper can exec an arbitrary sub-command that bypasses argv[0] policy; wrap the intent in an audited script and allow that."))
	}
	// 7. Privilege escalation.
	if base == "sudo" || base == "su" || base == "doas" {
		out = append(out, s.finding(RuleHostPrivilege, CategoryHostExecRisk,
			RiskCritical, DecisionDeny, seg, line,
			"Privilege escalation is not permitted for tool execution."))
	}
	// 8. Long-running / host session risk.
	out = append(out, s.longSession(base, argv, seg, line, in)...)
	// 9. Dependency installs.
	out = append(out, s.dependencyInstall(base, argv, seg, line)...)
	// 10. Resource abuse.
	out = append(out, s.resourceAbuse(base, argv, seg, line)...)
	// 11. Optional strict allowlist.
	if s.policy.EnforceAllowlist && len(out) == 0 {
		if _, ok := s.policy.allowedCmdSet[base]; !ok {
			out = append(out, s.finding(RuleNotAllowed, CategoryDangerousCommand,
				RiskMedium, DecisionAsk, seg, line,
				"Command is not on the allowlist; approve it or add it to allowed_commands."))
		}
	}
	return out
}

func (s *Scanner) dangerousDelete(base string, argv []string, seg, line string) []Finding {
	switch {
	case base == "rm" && hasRecursiveForce(argv):
		risk := RiskHigh
		if targetsDangerousRoot(argv) {
			risk = RiskCritical
		}
		return []Finding{s.finding(RuleDangerousDelete, CategoryDangerousCommand,
			risk, DecisionDeny, seg, line,
			"Recursive force delete is blocked; scope deletions to explicit safe paths.")}
	case base == "dd" && argvContainsPrefix(argv, "of=/dev/"):
		return []Finding{s.finding(RuleDangerousDelete, CategoryDangerousCommand,
			RiskCritical, DecisionDeny, seg, line, "Writing to a raw device is blocked.")}
	case base == "shred", strings.HasPrefix(base, "mkfs"):
		return []Finding{s.finding(RuleDangerousDelete, CategoryDangerousCommand,
			RiskCritical, DecisionDeny, seg, line, "Destructive disk operation is blocked.")}
	case base == "find" && (argvContains(argv, "-delete") || argvContains(argv, "-exec") || argvContains(argv, "-execdir")):
		risk := RiskHigh
		if targetsDangerousRoot(argv) {
			risk = RiskCritical
		}
		return []Finding{s.finding(RuleDangerousDelete, CategoryDangerousCommand,
			risk, DecisionDeny, seg, line,
			"find -delete/-exec performs recursive deletion or arbitrary execution; scope it to explicit safe paths in an audited script.")}
	}
	return nil
}

func (s *Scanner) deniedPathAccess(argv []string, seg, line string) []Finding {
	for _, a := range argv[1:] {
		if isFlag(a) {
			continue
		}
		if pat, ok := s.policy.matchesDeniedPath(a); ok {
			return []Finding{s.finding(RuleReadSecret, CategoryDangerousCommand,
				RiskCritical, DecisionDeny, "path="+normalizePathArg(a)+" ("+pat+")", line,
				"Access to secret/credential paths is blocked.")}
		}
	}
	_ = seg
	return nil
}

func (s *Scanner) overwriteSystem(base string, argv []string, seg, line string) []Finding {
	writers := map[string]struct{}{"cp": {}, "mv": {}, "tee": {}, "truncate": {}, "ln": {}, "install": {}}
	if _, ok := writers[base]; !ok {
		return nil
	}
	for _, a := range argv[1:] {
		if isFlag(a) {
			continue
		}
		n := normalizePathArg(a)
		for _, d := range systemDirs {
			if n == d || strings.HasPrefix(n, d+"/") {
				return []Finding{s.finding(RuleOverwriteSystem, CategoryDangerousCommand,
					RiskHigh, DecisionDeny, seg, line,
					"Writing into system directories is blocked.")}
			}
		}
	}
	return nil
}

func (s *Scanner) network(base string, argv []string, seg, line string, in ScanInput) []Finding {
	// Reverse-shell forms via nc/ncat -e are critical regardless of host.
	if (base == "nc" || base == "ncat") && (argvHasFlagLetter(argv, 'e') || argvContains(argv, "-lvp") || argvContains(argv, "-l")) {
		return []Finding{s.finding(RuleReverseShell, CategoryNetworkExfil,
			RiskCritical, DecisionDeny, seg, line,
			"Reverse-shell / listener form of a network tool is blocked.")}
	}
	if _, ok := s.policy.networkCmdSet[base]; !ok {
		return nil
	}
	host, found := extractHost(argv)
	if !found {
		return []Finding{s.finding(RuleNetUnknownTarget, CategoryNetworkExfil,
			RiskMedium, DecisionAsk, seg, line,
			"Network command with an undetermined target requires review.")}
	}
	if s.policy.isDomainAllowed(host) {
		return []Finding{s.finding(RuleNetAllowedDomain, CategoryNetworkExfil,
			RiskLow, DecisionAllow, "host="+host, line,
			"Network target is on the allowlist.")}
	}
	_ = in
	return []Finding{s.finding(RuleNetNonWhitelist, CategoryNetworkExfil,
		RiskCritical, DecisionDeny, "host="+host, line,
		"Network egress to a non-allowlisted host is blocked; add the domain to network.allowed_domains if intended.")}
}

func (s *Scanner) interpreterInline(base string, argv []string, seg, line string) []Finding {
	if _, ok := interpreters[base]; ok {
		return []Finding{s.finding(RuleInterpreterInline, CategoryShellBypass,
			RiskHigh, DecisionDeny, seg, line,
			"Shell wrappers / re-executing interpreters can bypass the policy; wrap the intent in an audited script.")}
	}
	if _, ok := dashCInterpreters[base]; ok && (argvContains(argv, "-c") || argvContains(argv, "-e")) {
		return []Finding{s.finding(RuleInterpreterInline, CategoryShellBypass,
			RiskHigh, DecisionDeny, seg, line,
			"Inline interpreter execution (-c/-e) is blocked; run an audited script file instead.")}
	}
	return nil
}

func (s *Scanner) longSession(base string, argv []string, seg, line string, in ScanInput) []Finding {
	long := false
	switch {
	case base == "screen", base == "tmux", base == "disown":
		long = true
	case base == "tail" && argvHasFlagLetter(argv, 'f'):
		long = true
	case base == "watch":
		long = true
	}
	if !long {
		return nil
	}
	risk := RiskMedium
	if in.Backend == BackendHostExec {
		risk = bumpRisk(risk)
	}
	return []Finding{s.finding(RuleHostLongSession, CategoryHostExecRisk,
		risk, DecisionAsk, seg, line,
		"Long-running/background session on this backend requires review and cleanup.")}
}

func (s *Scanner) dependencyInstall(base string, argv []string, seg, line string) []Finding {
	for _, r := range s.policy.DependencyInstall.Patterns {
		if commandBase(r.Cmd) == base && argsContainAll(argv[1:], r.ArgsPrefix) {
			return []Finding{s.finding(RuleDependencyInstall, CategoryDependencyChange,
				RiskMedium, s.policy.DependencyInstall.Decision, seg, line,
				"Dependency installation changes the environment and requires review.")}
		}
	}
	return nil
}

func (s *Scanner) resourceAbuse(base string, argv []string, seg, line string) []Finding {
	if base == "sleep" && sleepExceeds(argv, s.policy.Limits.MaxTimeoutSec) {
		return []Finding{s.finding(RuleTimeoutExceeds, CategoryResourceAbuse,
			RiskMedium, DecisionAsk, seg, line,
			"Sleep/timeout exceeds the configured maximum; reduce it or request approval.")}
	}
	if base == "yes" {
		return []Finding{s.finding(RuleOutputFlood, CategoryResourceAbuse,
			RiskLow, DecisionAsk, seg, line,
			"Command can flood output; bound it or request approval.")}
	}
	return nil
}

// lineRegexFindings evaluates whole-line regex rules that do not need a parsed
// pipeline (fork bombs, reverse shells). Secret detection is handled centrally
// in the scanner so it also runs on parse-failure lines.
func (s *Scanner) lineRegexFindings(line string, _ ScanInput) []Finding {
	var out []Finding
	if forkBombRe.MatchString(line) {
		out = append(out, s.finding(RuleOutputFlood, CategoryResourceAbuse,
			RiskCritical, DecisionDeny, "fork bomb", line,
			"Fork-bomb pattern detected; execution is blocked."))
	}
	if reverseShellRe.MatchString(line) {
		out = append(out, s.finding(RuleReverseShell, CategoryNetworkExfil,
			RiskCritical, DecisionDeny, "reverse shell", line,
			"Reverse-shell pattern detected; execution is blocked."))
	}
	return out
}

// finding builds a Finding, applying the policy risk override for its rule id.
func (s *Scanner) finding(ruleID, category string, risk RiskLevel, decision Decision, evidence, segment, rec string) Finding {
	return Finding{
		RuleID:         ruleID,
		Category:       category,
		RiskLevel:      s.policy.riskFor(ruleID, risk),
		Decision:       decision,
		Evidence:       evidence,
		Recommendation: rec,
		Segment:        segment,
	}
}

// ---- small helpers ----

func hasRecursiveForce(argv []string) bool {
	rec, force := false, false
	for _, a := range argv[1:] {
		if !isFlag(a) {
			continue
		}
		switch a {
		case "--recursive":
			rec = true
			continue
		case "--force":
			force = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		for _, c := range a[1:] {
			switch c {
			case 'r', 'R':
				rec = true
			case 'f':
				force = true
			}
		}
	}
	return rec && force
}

func targetsDangerousRoot(argv []string) bool {
	for _, a := range argv[1:] {
		if isFlag(a) {
			continue
		}
		n := normalizePathArg(a)
		if n == "/" || n == "/*" || n == "~" || n == "~/" || n == "*" {
			return true
		}
		n = strings.TrimSuffix(n, "/")
		for _, d := range systemDirs {
			if n == d || strings.HasPrefix(n, d+"/") {
				return true
			}
		}
	}
	return false
}

func sleepExceeds(argv []string, max int) bool {
	for _, a := range argv[1:] {
		if isFlag(a) {
			continue
		}
		if secs, ok := parseDurationSeconds(a); ok && secs > float64(max) {
			return true
		}
	}
	return false
}

// parseDurationSeconds parses a GNU-sleep-style duration into seconds. It
// accepts bare numbers, floats, unit suffixes (s/m/h/d) and inf/infinity, so
// `sleep 5m`, `sleep 1.5`, `sleep 2h` and huge/overflow values are all handled
// rather than silently treated as zero.
func parseDurationSeconds(a string) (float64, bool) {
	la := strings.ToLower(strings.TrimSpace(a))
	if la == "inf" || la == "infinity" {
		return 1e18, true
	}
	mult := 1.0
	if n := len(la); n > 0 {
		switch la[n-1] {
		case 's':
			la = la[:n-1]
		case 'm':
			mult, la = 60, la[:n-1]
		case 'h':
			mult, la = 3600, la[:n-1]
		case 'd':
			mult, la = 86400, la[:n-1]
		}
	}
	if la == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(la, 64)
	if err != nil {
		return 0, false
	}
	return v * mult, true
}

func argsContainAll(args, needles []string) bool {
	if len(needles) == 0 {
		return false
	}
	for _, n := range needles {
		if !argvContains(args, n) {
			return false
		}
	}
	return true
}

func argvContains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func argvContainsPrefix(argv []string, prefix string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// argvHasFlagLetter reports whether any short-flag token bundles the letter c,
// e.g. detects 'f' in "-f" or "-rf".
func argvHasFlagLetter(argv []string, letter rune) bool {
	for _, a := range argv[1:] {
		if !isFlag(a) || strings.HasPrefix(a, "--") {
			continue
		}
		if strings.ContainsRune(a[1:], letter) {
			return true
		}
	}
	return false
}

func bumpRisk(r RiskLevel) RiskLevel {
	switch r {
	case RiskLow:
		return RiskMedium
	case RiskMedium:
		return RiskHigh
	case RiskHigh:
		return RiskCritical
	default:
		return r
	}
}
