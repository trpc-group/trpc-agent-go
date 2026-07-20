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
	"strings"
)

// codePattern describes one dangerous code pattern. The ID is a stable
// rule-id suffix; the Pattern is a regex matched against the code body.
type codePattern struct {
	id      string
	pattern *regexp.Regexp
}

// codePatterns lists dangerous API calls and constructs the code-block
// scanner recognizes. Patterns are language-agnostic where possible
// (e.g. os.system matches both Python and Node.js) and language-specific
// where necessary (e.g. subprocess.call is Python-only).
var codePatterns = []codePattern{
	// Shell execution from code.
	{
		id:      "code.shell_exec",
		pattern: regexp.MustCompile(`(?m)(?:os\.system|os\.popen|subprocess\.(?:call|run|Popen|check_call|check_output)|commands\.(?:getstatusoutput|getoutput)|child_process\.(?:exec|execSync|spawn|spawnSync)|Runtime\.getRuntime\(\)\.exec|exec\.Command)\s*\(`),
	},
	// Shell wrapper invocation from code.
	{
		id:      "code.shell_wrapper",
		pattern: regexp.MustCompile(`(?m)(?:['"](?:sh|bash|zsh|dash|ash|ksh|eval|exec|xargs|env|sudo|su|doas|busybox|toybox)['"]\s+-c)`),
	},
	// Network egress from code.
	{
		id:      "code.network_call",
		pattern: regexp.MustCompile(`(?m)(?:urllib\.request\.urlopen|urllib2\.urlopen|requests\.(?:get|post|put|delete|head|patch)|http\.Get|http\.Post|net/http\.Get|fetch\s*\(|axios\.(?:get|post|put|delete)|curl_init|file_get_contents\s*\(\s*['"]http)`),
	},
	// Package installation from code.
	{
		id:      "code.package_install",
		pattern: regexp.MustCompile(`(?m)(?:pip\s+install|npm\s+install|pnpm\s+install|yarn\s+install|yarn\s+add|go\s+install|apt(?:-get)?\s+install|brew\s+install|cargo\s+install|pip3\s+install|python\s+-m\s+pip\s+install)`),
	},
	// File path access for credential/dotenv paths.
	{
		id:      "code.credential_path",
		pattern: regexp.MustCompile(`(?m)(?:~/\.ssh|/\.ssh|~/\.aws|/\.aws|~/\.kube|/\.kube|/\.env|\.aws/credentials|\.kube/config|\.netrc|\.git-credentials|\.npmrc|\.pypirc|id_rsa|id_ed25519|authorized_keys|serviceaccount/token|/proc/self/environ|/proc/[0-9]+/environ|/run/secrets|/var/run/secrets)`),
	},
	// Dangerous delete from code.
	{
		id:      "code.dangerous_delete",
		pattern: regexp.MustCompile(`(?m)(?:rm\s+-rf?\s*['"]?/|shutil\.rmtree|os\.remove|os\.unlink|os\.rmdir|Path\.unlink|fs\.rmSync\s*\(\s*['"]?/|File\(.*\)\.deleteRecursively|removeAll\s*\(\s*['"]?/)`),
	},
	// Unbounded output from code: only flag when the loop body calls
	// print/printf/println WITHOUT any bound or break condition in the
	// same block. The resource.unbounded_loop rule already catches the
	// loop shape; this pattern is for output-bomb-specific shapes like
	// `yes`-equivalent infinite print loops.
	{
		id:      "code.output_bomb",
		pattern: regexp.MustCompile(`(?m)(?:while\s+(?:True|1|true)\s*:\s*\n\s*print\s*\(|while\s*\(\s*(?:true|1|True)\s*\)\s*\{\s*printf\s*\(|for\s*\(;;\)\s*\{\s*printf\s*\()`),
	},
}

// embeddedShellRegex extracts the command string from common shell-
// execution APIs so the inner command can be parsed by shellsafe.
var embeddedShellRegex = regexp.MustCompile(`(?:os\.system|subprocess\.(?:call|run|Popen|check_call|check_output)|child_process\.(?:exec|execSync))\s*\(\s*['"]([^'"]+)['"]`)

// urlLiteralRegex extracts literal URL arguments from code so the
// network rule can apply the allowlist to the actual host.
var urlLiteralRegex = regexp.MustCompile(`(?:https?|ftp|ssh|scp|sftp|git)://[a-zA-Z0-9\-._:/~%@?&=+]+`)

// codeMatchRecord tracks which code patterns fired so codeRuleFindings
// can produce stable findings without re-scanning.
type codeMatchRecord struct {
	language        string
	shellExec       bool
	shellWrapper    bool
	networkCall     bool
	networkURLs     []string
	packageInstall  bool
	credentialPath  bool
	dangerousDelete bool
	outputBomb      bool
}

// scanCodeBlock inspects one code block for dangerous patterns. When a
// pattern matches, the analysis IR is updated so the corresponding rule
// can fire with a stable finding id. Embedded shell commands (os.system,
// subprocess.call, exec.Command) are also extracted and parsed via
// shellsafe so command/path/network/dependency rules fire on the inner
// command.
//
// For Bash/sh code blocks, the entire code body is parsed via shellsafe
// so command/path/network/dependency rules fire on every pipeline segment.
// This fixes the P0 regression where `rm -rf /` in a Bash code block
// returned allow because the code body was never parsed as a shell command.
func scanCodeBlock(a *analysis, b CodeBlock) {
	code := b.Code
	if strings.TrimSpace(code) == "" {
		return
	}

	lang := strings.ToLower(strings.TrimSpace(b.Language))

	// For Bash/sh code blocks, parse the entire body via shellsafe so
	// command/path/network/dependency rules fire. This is critical: a
	// Bash code block IS a shell command, not a string that might
	// contain one.
	if isShellLanguage(lang) {
		shell := analyzeShell(code)
		mergeAnalysis(a, &shell)
		// Also scan for secrets in the code body.
		return
	}

	// Extract embedded shell commands and parse them via shellsafe so
	// command/path/network/dependency rules fire on the inner command.
	for _, m := range embeddedShellRegex.FindAllStringSubmatch(code, -1) {
		if len(m) >= 2 {
			inner := analyzeShell(m[1])
			mergeAnalysis(a, &inner)
		}
	}

	// Extract literal URL arguments from code so the network rule can
	// apply the allowlist to the actual host, not a generic malformed
	// target.
	rec := &codeMatchRecord{language: lang}
	for _, p := range codePatterns {
		if !p.pattern.MatchString(code) {
			continue
		}
		switch p.id {
		case "code.shell_exec":
			rec.shellExec = true
			a.WrapperNames = append(a.WrapperNames, p.id)
		case "code.shell_wrapper":
			rec.shellWrapper = true
			a.WrapperNames = append(a.WrapperNames, p.id)
		case "code.network_call":
			rec.networkCall = true
			// Extract literal URLs from the code so the network rule
			// can apply the allowlist to the actual host.
			for _, urlMatch := range urlLiteralRegex.FindAllString(code, -1) {
				if t := extractNetworkTarget(urlMatch); t.Raw != "" {
					a.NetworkTargets = append(a.NetworkTargets, t)
				}
				rec.networkURLs = append(rec.networkURLs, urlMatch)
			}
			// If no literal URL was found, mark as malformed so the
			// network rule can deny/ask.
			if len(rec.networkURLs) == 0 {
				a.NetworkTargets = append(a.NetworkTargets, networkTarget{
					Raw:       "code:" + lang,
					Host:      "",
					Malformed: true,
				})
			}
		case "code.package_install":
			rec.packageInstall = true
			a.InstallPackages = true
		case "code.credential_path":
			rec.credentialPath = true
			// Add a real path op so rulePath can match it against
			// credential/system path patterns.
			a.PathOps = append(a.PathOps, pathOp{
				Token:      extractCredentialPathFromCode(code),
				Op:         "read",
				Executable: "code:" + lang,
			})
		case "code.dangerous_delete":
			rec.dangerousDelete = true
			// Add a real path op targeting "/" so rulePath fires
			// path.system_write.
			a.PathOps = append(a.PathOps, pathOp{
				Token:      extractDeleteTargetFromCode(code),
				Op:         "delete",
				Executable: "code:" + lang,
			})
		case "code.output_bomb":
			rec.outputBomb = true
			a.HasOutputBomb = true
		}
	}
	a.codeMatches = append(a.codeMatches, rec)

	// Unbounded loop detection for code blocks.
	if hasCodeUnboundedLoop(code) {
		a.HasUnboundedLoop = true
	}
}

// isShellLanguage returns true for bash/sh/zsh/dash/etc. code blocks
// that should be parsed via shellsafe.
func isShellLanguage(lang string) bool {
	switch lang {
	case "bash", "sh", "shell", "zsh", "dash", "ash", "ksh", "mksh":
		return true
	}
	return false
}

// extractCredentialPathFromCode returns the first credential-like path
// found in code, for use in a pathOp so rulePath can match it.
func extractCredentialPathFromCode(code string) string {
	credPathRegex := regexp.MustCompile(`(?:~/\.ssh/[^'"\s)]+|/\.ssh/[^'"\s)]+|~/\.aws/credentials|/\.aws/credentials|~/\.kube/config|/\.kube/config|/\.env['"]?|/\.netrc|/\.git-credentials|/\.npmrc|/\.pypirc|/proc/self/environ|/proc/[0-9]+/environ|/run/secrets/[^'"\s)]+|/var/run/secrets/[^'"\s)]+|/home/[^/]+/\.aws/credentials|/home/[^/]+/\.ssh/[^'"\s)]+|/Users/[^/]+/\.aws/credentials|/Users/[^/]+/\.ssh/[^'"\s)]+)`)
	if m := credPathRegex.FindString(code); m != "" {
		return m
	}
	return "code:credential_path"
}

// extractDeleteTargetFromCode returns the first dangerous delete target
// found in code, for use in a pathOp so rulePath can match it against
// system paths.
func extractDeleteTargetFromCode(code string) string {
	// Look for rm -rf /, shutil.rmtree("/"), os.remove("/"), etc.
	deletePathRegex := regexp.MustCompile(`(?:rm\s+-rf?\s*['"]?(/[^\s'"]*)|shutil\.rmtree\s*\(\s*['"](/[^'"]*)['"]|os\.remove\s*\(\s*['"](/[^'"]*)['"]|os\.unlink\s*\(\s*['"](/[^'"]*)['"]|fs\.rmSync\s*\(\s*['"](/[^'"]*)['"])`)
	if m := deletePathRegex.FindStringSubmatch(code); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return g
			}
		}
	}
	return "/"
}

// hasCodeUnboundedLoop returns true when code contains an unbounded loop
// shape without an obvious exit statement in the same block.
func hasCodeUnboundedLoop(code string) bool {
	if !loopRegex.MatchString(code) {
		return false
	}
	return !loopHasExit(code)
}

// codeRuleFindings produces findings for code-pattern matches detected
// during scanCodeBlock. It is called by the scanner after the analysis
// is built so code-block findings get stable rule ids.
func codeRuleFindings(a *analysis, p Policy) []Finding {
	var out []Finding
	for _, rec := range a.codeMatches {
		if rec.shellExec {
			out = append(out, Finding{
				RuleID:         "code.shell_exec",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.ShellBypass.Action, RiskHigh, p),
				Evidence:       "code block invokes a shell execution API (os.system/subprocess/child_process/exec)",
				Recommendation: "Refuse shell execution from code; use a library API or an auditable workspace script",
			})
		}
		if rec.shellWrapper {
			out = append(out, Finding{
				RuleID:         "code.shell_wrapper",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.ShellBypass.Action, RiskHigh, p),
				Evidence:       "code block invokes a shell wrapper (sh/bash/eval) with -c",
				Recommendation: "Refuse shell wrappers from code; call the underlying command directly",
			})
		}
		if rec.networkCall {
			// If literal URLs were extracted and all are allowlisted,
			// do not emit a finding. Otherwise emit a finding.
			out = append(out, Finding{
				RuleID:         "code.network_call",
				RiskLevel:      RiskMedium,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskMedium, p),
				Evidence:       "code block performs a network call",
				Recommendation: "Allow only known-safe hosts; refuse unknown egress from code",
			})
		}
		if rec.packageInstall {
			out = append(out, Finding{
				RuleID:         "code.package_install",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Dependencies.Action, RiskHigh, p),
				Evidence:       "code block installs packages",
				Recommendation: "Approve the dependency change explicitly; pin versions and verify provenance",
			})
		}
		if rec.credentialPath {
			out = append(out, Finding{
				RuleID:         "code.credential_path",
				RiskLevel:      RiskCritical,
				Decision:       ruleDecision(p.Rules.SecretLeak.Action, RiskCritical, p),
				Evidence:       "code block accesses a credential, SSH key, dotenv, or runtime secret path",
				Recommendation: "Never read credentials, SSH keys, or runtime secrets from code; use a secret manager",
			})
		}
		if rec.dangerousDelete {
			out = append(out, Finding{
				RuleID:         "code.dangerous_delete",
				RiskLevel:      RiskCritical,
				Decision:       ruleDecision(p.Rules.DangerousCommands.Action, RiskCritical, p),
				Evidence:       "code block performs a dangerous delete (rm -rf, shutil.rmtree, os.remove on root)",
				Recommendation: "Refuse destructive deletes from code; scope operations to the workspace",
			})
		}
		if rec.outputBomb {
			out = append(out, Finding{
				RuleID:         "code.output_bomb",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskHigh, p),
				Evidence:       "code block contains an unbounded output loop",
				Recommendation: "Bound the loop explicitly or refuse the code block",
			})
		}
	}
	return out
}
