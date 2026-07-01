//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
)

// Scanner statically evaluates a ScanInput against a Policy and produces a
// ScanReport. A Scanner is safe for concurrent use.
type Scanner struct {
	policy *Policy
	now    func() time.Time
}

// NewScanner returns a Scanner using the given policy, or DefaultPolicy when
// policy is nil.
func NewScanner(policy *Policy) *Scanner {
	if policy == nil {
		policy = DefaultPolicy()
	}
	return &Scanner{policy: policy, now: time.Now}
}

// Policy returns the scanner's active policy.
func (s *Scanner) Policy() *Policy { return s.policy }

// Scan evaluates in and returns a fully aggregated, redacted report. The
// context is accepted for symmetry and cancellation of future async rules; the
// current rule set is CPU-only and does not block.
func (s *Scanner) Scan(_ context.Context, in ScanInput) ScanReport {
	start := s.now()

	redactedCmd, cmdRedacted := redactSecrets(commandText(in), s.policy.secrets)
	report := ScanReport{
		ToolName: in.ToolName,
		Backend:  in.Backend,
		Command:  redactedCmd,
	}
	redacted := cmdRedacted

	var findings []Finding
	if in.Backend == BackendCodeExec {
		findings = append(findings, s.scanCodeBlocks(in)...)
	} else {
		findings = append(findings, s.scanCommandText(in.Command, in)...)
	}
	findings = append(findings, s.checkEnv(in)...)
	findings = append(findings, s.checkTimeout(in)...)
	findings = append(findings, s.checkInlineSecrets(in)...)

	// Redact any secret that leaked into evidence so no plaintext survives.
	for i := range findings {
		ev, r := redactSecrets(findings[i].Evidence, s.policy.secrets)
		findings[i].Evidence = ev
		seg, r2 := redactSecrets(findings[i].Segment, s.policy.secrets)
		findings[i].Segment = seg
		redacted = redacted || r || r2
	}

	report.Findings = findings
	report.Redacted = redacted
	report.aggregate()
	report.DurationMS = time.Since(start).Milliseconds()
	return report
}

// scanCommandText scans a shell command or multi-line script.
func (s *Scanner) scanCommandText(text string, in ScanInput) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []Finding
	for _, line := range splitScriptLines(text) {
		out = append(out, s.lineRegexFindings(line, in)...)
		segments, err := parsePipeline(line)
		if err != nil {
			out = append(out, s.finding(RuleUnsafeConstruct, CategoryShellBypass,
				RiskHigh, s.policy.DefaultDecisionOnParseFailure, err.Error(), line,
				"Command uses a construct that cannot be safely parsed (command substitution, redirection, subshell, ...); rewrite it as a plain pipeline or wrap it in an audited script."))
			continue
		}
		for _, argv := range segments {
			out = append(out, s.segmentFindings(argv, line, in)...)
		}
	}
	return out
}

// scanCodeBlocks scans codeexec code blocks: bash/sh blocks reuse the command
// scanner; other languages get a lighter regex pass plus secret/path checks.
func (s *Scanner) scanCodeBlocks(in ScanInput) []Finding {
	var out []Finding
	for _, b := range in.CodeBlocks {
		lang := strings.ToLower(strings.TrimSpace(b.Language))
		switch lang {
		case "bash", "sh", "shell", "":
			sub := in
			sub.Command = b.Code
			out = append(out, s.scanCommandText(b.Code, sub)...)
		default:
			out = append(out, s.scanForeignCode(b, in)...)
		}
	}
	return out
}

func (s *Scanner) scanForeignCode(b CodeBlock, in ScanInput) []Finding {
	var out []Finding
	if pyDangerRe.MatchString(b.Code) {
		out = append(out, s.finding(RulePythonDangerousAPI, CategoryShellBypass,
			RiskHigh, DecisionAsk, pyDangerRe.FindString(b.Code), b.Language+" block",
			"Code invokes shell/eval APIs; review before executing in a sandbox."))
	}
	// Scan any shell command the code shells out to (os.system/subprocess),
	// so an embedded `rm -rf /` or egress is caught by the full command rule
	// set instead of being downgraded to a generic dangerous-API ask.
	for _, cmd := range extractForeignCommands(b.Code) {
		out = append(out, s.scanCommandText(cmd, in)...)
	}
	for _, tok := range tokenizeLoose(b.Code) {
		if pat, ok := s.policy.matchesDeniedPath(tok); ok {
			out = append(out, s.finding(RuleReadSecret, CategoryDangerousCommand,
				RiskCritical, DecisionDeny, "path="+normalizePathArg(tok)+" ("+pat+")", b.Language+" block",
				"Code references a secret/credential path; execution is blocked."))
			break
		}
	}
	return out
}

// checkInlineSecrets flags secrets embedded directly in the command/code.
func (s *Scanner) checkInlineSecrets(in ScanInput) []Finding {
	names := matchedSecretNames(commandText(in), s.policy.secrets)
	if len(names) == 0 {
		return nil
	}
	return []Finding{s.finding("secret.inline", CategorySensitiveLeak,
		RiskHigh, DecisionDeny, "inline secret: "+strings.Join(names, ", "), "",
		"Inline secrets must not be passed on the command line; use a secret store or env var.")}
}

// checkEnv flags per-call environment overrides: sensitive keys via
// internal/envscrub's block list, and — when env_whitelist is configured — any
// key outside that whitelist. Keys are visited in sorted order so the primary
// finding and audit rule id are deterministic regardless of map iteration.
func (s *Scanner) checkEnv(in ScanInput) []Finding {
	if len(in.Env) == 0 {
		return nil
	}
	caseInsensitive := runtime.GOOS == "windows"
	keys := make([]string, 0, len(in.Env))
	for k := range in.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Finding
	for _, k := range keys {
		switch {
		case envscrub.IsBlocked(k, caseInsensitive) || envscrub.IsMalformedKey(k):
			out = append(out, s.finding(RuleEnvMutation, CategoryDependencyChange,
				RiskMedium, DecisionAsk, "env="+k, "",
				"Sensitive environment override (PATH/LD_PRELOAD/BASH_ENV/...) requires review."))
		case len(s.policy.envWhitelistSet) > 0 && !s.policy.envAllowed(k):
			out = append(out, s.finding(RuleEnvNotWhitelisted, CategoryDependencyChange,
				RiskMedium, DecisionAsk, "env="+k, "",
				"Environment override is not in env_whitelist; approve it or add the key to the policy."))
		}
	}
	return out
}

// checkTimeout flags a requested timeout larger than the policy maximum.
func (s *Scanner) checkTimeout(in ScanInput) []Finding {
	if in.TimeoutSec > 0 && in.TimeoutSec > s.policy.Limits.MaxTimeoutSec {
		return []Finding{s.finding(RuleTimeoutExceeds, CategoryResourceAbuse,
			RiskMedium, DecisionAsk, "timeout_sec="+strconv.Itoa(in.TimeoutSec), "",
			"Requested timeout exceeds the configured maximum.")}
	}
	return nil
}

// commandText returns the text used for secret detection: the command for exec
// backends, or the concatenated code for codeexec.
func commandText(in ScanInput) string {
	if in.Backend == BackendCodeExec && len(in.CodeBlocks) > 0 {
		var b strings.Builder
		for i, cb := range in.CodeBlocks {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(cb.Code)
		}
		return b.String()
	}
	return in.Command
}

// tokenizeLoose splits arbitrary code into candidate path/word tokens by
// breaking on whitespace and common punctuation.
func tokenizeLoose(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '(', ')', ',', ';', '"', '\'', '=', '+', '[', ']', '{', '}':
			return true
		}
		return false
	})
}
