//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Backend labels for the built-in execution paths.
const (
	BackendWorkspaceExec = "workspaceexec"
	BackendHostExec      = "hostexec"
	BackendCodeExec      = "codeexec"
	BackendUnknown       = "unknown"
)

// maxEnvelopeBytes caps the total request size the scanner will
// inspect. Requests beyond this are treated as resource abuse rather
// than scanned in full, so a hostile caller cannot exhaust CPU by
// submitting a giant payload.
const maxEnvelopeBytes = 1 << 20 // 1 MiB

// CodeBlock is a single unit of code submitted to a code executor.
type CodeBlock struct {
	Language string
	Code     string
}

// Request is the normalised input to Scan. Callers either build it
// directly or let the permission bridge derive it from a framework
// tool call.
type Request struct {
	// ToolName is the model-visible tool name.
	ToolName string
	// Backend identifies the execution backend (Backend* constants).
	Backend string
	// Command is the shell command line to run. Empty for pure
	// code-execution requests.
	Command string
	// CodeBlocks holds code submitted to a code executor. The first
	// line of each block is analysed as a command when it looks like
	// a shell invocation; the whole block is scanned for secrets and
	// sensitive paths.
	CodeBlocks []CodeBlock
	// Args are pre-split argv values (used by hostexec/codeexec paths
	// that already have structured arguments). Optional.
	Args []string
	// Workdir is the requested working directory.
	Workdir string
	// Env is the environment overlay requested for the call.
	Env map[string]string
	// TimeoutSec is the requested timeout in seconds (0 = default).
	TimeoutSec int
	// Background reports a request for a detached/long-lived session.
	Background bool
	// TTY reports a request for an interactive PTY session.
	TTY bool
	// Destructive mirrors tool.ToolMetadata.Destructive and is the
	// only metadata flag Scan's scanMetadata decision path weighs.
	Destructive bool
	// OpenWorld mirrors tool.ToolMetadata.OpenWorld. It is consulted
	// only by the guard when deciding whether to defensively scan an
	// unmapped tool (see guard.toScanRequest); Scan itself does not
	// weigh it.
	OpenWorld bool
	// Malformed reports that the caller could not parse the tool
	// arguments into a request. The scanner then emits a parse-error
	// finding and applies policy.ParseErrorDecision, so unparsable
	// input fails closed instead of scanning an empty command.
	Malformed bool
}

// Scan evaluates req against policy and returns a structured report.
// It never executes anything and never returns an error: a scan that
// cannot reason about the input fails closed via policy.ParseErrorDecision.
func Scan(req Request, policy Policy) Report {
	start := time.Now()
	report := Report{
		ToolName:  firstNonEmpty(req.ToolName, "unknown"),
		Backend:   firstNonEmpty(req.Backend, BackendUnknown),
		ScannedAt: start.UTC(),
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
	}

	sc := &scanner{policy: policy}

	if req.Malformed {
		sc.add(Finding{
			RuleID:         RuleParseError,
			RiskLevel:      RiskHigh,
			Decision:       policy.ParseErrorDecision,
			Evidence:       "tool arguments could not be parsed into a safety request",
			Recommendation: "send well-formed arguments; malformed execution requests are refused",
		})
		return sc.finish(&report, start)
	}

	if oversized(req) {
		sc.add(Finding{
			RuleID:         RuleResourceAbuse,
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "request payload exceeds " + strconv.Itoa(maxEnvelopeBytes) + " bytes",
			Recommendation: "split the work into smaller calls or run it in an isolated sandbox",
		})
		return sc.finish(&report, start)
	}

	sc.scanMetadata(req)
	sc.scanEnv(req)
	sc.scanExecParams(req)

	// Command line analysis.
	if strings.TrimSpace(req.Command) != "" {
		sc.scanCommand(req.Command, report.Backend)
	}
	// argv analysis (already-split commands).
	if len(req.Args) > 0 {
		sc.scanArgv(req.Args)
	}
	// Code blocks: analyse each block's text and its leading command.
	for _, b := range req.CodeBlocks {
		sc.scanCodeBlock(b)
	}

	report.Command = sc.redactString(commandPreview(req))
	return sc.finish(&report, start)
}

// scanner accumulates findings for one request.
type scanner struct {
	policy    Policy
	findings  []Finding
	sawSecret bool
}

func (s *scanner) add(f Finding) {
	f.Evidence = s.redactString(f.Evidence)
	s.findings = append(s.findings, f)
}

func (s *scanner) finish(report *Report, start time.Time) Report {
	decision := DecisionAllow
	risk := RiskNone
	for _, f := range s.findings {
		decision = stricter(decision, f.Decision)
		risk = maxRisk(risk, f.RiskLevel)
	}
	report.Findings = s.findings
	report.Decision = decision
	report.RiskLevel = risk
	report.Blocked = decision != DecisionAllow
	report.Redacted = s.sawSecret && s.policy.redact()
	report.DurationMS = time.Since(start).Milliseconds()
	return *report
}

// scanCommand parses the command line with shellsafe and runs the
// per-segment rules. Unparsable commands fail closed.
func (s *scanner) scanCommand(command, backend string) {
	// Secret and sensitive-path checks run on the raw text so they
	// fire even when the command cannot be parsed structurally.
	s.scanSecrets(command)
	s.scanSensitivePaths(command)
	s.scanDestructive(command)
	s.scanDependency(command)
	s.scanResourceText(command)

	pipe, err := shellsafe.Parse(command)
	if err != nil {
		s.add(Finding{
			RuleID:         RuleParseError,
			RiskLevel:      RiskHigh,
			Decision:       s.policy.ParseErrorDecision,
			Evidence:       "shellsafe could not parse command conservatively: " + err.Error(),
			Recommendation: "rewrite the command without shell substitution/redirection, or wrap it in an auditable workspace script",
		})
		// A command containing $(...) / backticks / redirections is
		// also flagged as a shell-bypass attempt so the finding set
		// carries the risk category, not only the parse failure.
		s.flagShellBypass(command)
		return
	}
	s.applyCommandPolicy(pipe)
	s.scanNetwork(pipe)
	s.scanPipelineLimits(pipe)
}

// scanArgv runs the segment rules on an already-split argv. It mirrors
// scanCommand's text checks (including resource abuse) so the hostexec/
// codeexec Args path is not weaker than the Command path.
func (s *scanner) scanArgv(argv []string) {
	joined := strings.Join(argv, " ")
	s.scanSecrets(joined)
	s.scanSensitivePaths(joined)
	s.scanDestructive(joined)
	s.scanDependency(joined)
	s.scanResourceText(joined)
	pipe := &shellsafe.Pipeline{Commands: [][]string{argv}}
	s.applyCommandPolicy(pipe)
	s.scanNetwork(pipe)
}

// applyCommandPolicy enforces the allow/deny lists via shellsafe. Its
// implicit deny set already blocks shell wrappers and re-executing
// builtins, which is exactly the shell-bypass category.
func (s *scanner) applyCommandPolicy(pipe *shellsafe.Pipeline) {
	pol := shellsafe.PolicyFromLists(s.policy.AllowedCommands, s.policy.DeniedCommands)
	if !pol.Active() {
		// Even without explicit lists, block the wrapper set so a
		// bare "sh -c" is escalated. Seed a deny that only triggers
		// the implicit set by adding a sentinel to Deny is wrong;
		// instead re-check wrappers directly.
		s.flagWrapperSegments(pipe)
		return
	}
	if err := pol.Check(pipe); err != nil {
		msg := err.Error()
		rule := RuleCommandPolicy
		risk := RiskHigh
		if strings.Contains(msg, "built-in policy") {
			rule = RuleShellBypass
			risk = RiskHigh
		}
		s.add(Finding{
			RuleID:         rule,
			RiskLevel:      risk,
			Decision:       DecisionDeny,
			Evidence:       msg,
			Recommendation: "adjust allowed_commands/denied_commands or run through an approved wrapper script",
		})
	}
}

// flagWrapperSegments reports shell wrappers / re-executing builtins
// even when no explicit allow/deny list is configured. Hard bypass
// interpreters (sh -c, eval, ...) are denied; softer process runners
// (sudo, env, timeout, ...) escalate to an ask.
func (s *scanner) flagWrapperSegments(pipe *shellsafe.Pipeline) {
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := strings.ToLower(lastPathSegment(argv[0]))
		if _, ok := hardBypass[base]; ok {
			s.add(Finding{
				RuleID:         RuleShellBypass,
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       "shell interpreter or re-executing builtin: " + argv[0],
				Recommendation: "invoke the target binary directly or wrap the use in an auditable workspace script",
			})
			return
		}
		if _, ok := softWrapper[base]; ok {
			s.add(Finding{
				RuleID:         RuleShellBypass,
				RiskLevel:      RiskMedium,
				Decision:       DecisionAsk,
				Evidence:       "process runner or privilege wrapper: " + argv[0],
				Recommendation: "confirm the wrapped command is intended; prefer calling the target binary directly",
			})
			return
		}
	}
}

func (s *scanner) flagShellBypass(command string) {
	lc := strings.ToLower(command)
	for _, marker := range []string{"$(", "`", "${", "eval ", "sh -c", "bash -c", "|", ">", "<"} {
		if strings.Contains(lc, marker) {
			s.add(Finding{
				RuleID:         RuleShellBypass,
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       "shell bypass construct detected: " + marker,
				Recommendation: "remove command substitution, eval, pipes or redirections; use structured arguments",
			})
			return
		}
	}
}

// scanNetwork inspects each segment for network-egress executables
// and checks their target host against the allowlist.
func (s *scanner) scanNetwork(pipe *shellsafe.Pipeline) {
	egress := s.egressCommands()
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := strings.ToLower(lastPathSegment(argv[0]))
		if _, ok := egress[base]; !ok {
			continue
		}
		host := firstHost(argv[1:])
		if host != "" && s.hostAllowed(host) {
			continue
		}
		ev := "network egress via " + base
		if host != "" {
			ev += " to " + host
		}
		s.add(Finding{
			RuleID:         RuleNetworkEgress,
			RiskLevel:      RiskHigh,
			Decision:       s.policy.Network.Decision,
			Evidence:       ev,
			Recommendation: "add the destination host to network.allowed_hosts if it is trusted",
		})
	}
}

func (s *scanner) scanPipelineLimits(pipe *shellsafe.Pipeline) {
	if s.policy.Limits.MaxPipelineSegments <= 0 {
		return
	}
	if len(pipe.Commands) > s.policy.Limits.MaxPipelineSegments {
		s.add(Finding{
			RuleID:         RuleResourceAbuse,
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "pipeline has " + strconv.Itoa(len(pipe.Commands)) + " segments",
			Recommendation: "split large pipelines or raise limits.max_pipeline_segments",
		})
	}
}

// scanSecrets detects credential-shaped substrings.
func (s *scanner) scanSecrets(text string) {
	for _, m := range detectSecrets(text) {
		s.sawSecret = true
		s.add(Finding{
			RuleID:         RuleSecretLeak,
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "possible secret: " + m,
			Recommendation: "remove inline credentials; read secrets from a secret manager at runtime",
		})
	}
}

// scanSensitivePaths flags references to denied filesystem paths.
func (s *scanner) scanSensitivePaths(text string) {
	lc := strings.ToLower(text)
	for _, p := range s.policy.DeniedPaths {
		if p == "" {
			continue
		}
		if strings.Contains(lc, strings.ToLower(p)) {
			s.add(Finding{
				RuleID:         RuleSensitivePath,
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       "sensitive path access: " + p,
				Recommendation: "do not read credential or key material; use scoped, injected secrets",
			})
		}
	}
}

// scanDestructive matches destructive command patterns.
func (s *scanner) scanDestructive(text string) {
	lc := strings.ToLower(text)
	if loc := destructiveRmRe.FindString(text); loc != "" {
		s.add(Finding{
			RuleID:         RuleDangerousCommand,
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       "recursive delete of a system path: " + strings.TrimSpace(loc),
			Recommendation: "this operation is irreversible; scope the path or run only inside a disposable sandbox",
		})
	}
	patterns := append([]string{}, destructivePatterns...)
	patterns = append(patterns, s.policy.DestructivePatterns...)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(lc, strings.ToLower(p)) {
			s.add(Finding{
				RuleID:         RuleDangerousCommand,
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       "destructive command pattern: " + p,
				Recommendation: "this operation is irreversible; run only inside a disposable sandbox",
			})
		}
	}
}

// scanDependency flags package-manager install commands.
func (s *scanner) scanDependency(text string) {
	// lc is padded with a leading and trailing space so the single
	// boundary-aware check below matches whole words/phrases without
	// firing on substrings (e.g. "install" inside "reinstall").
	lc := " " + strings.ToLower(text) + " "
	for _, p := range dependencyInstallPatterns {
		if strings.Contains(lc, " "+p+" ") {
			s.add(Finding{
				RuleID:         RuleDependencyChange,
				RiskLevel:      RiskMedium,
				Decision:       s.policy.DependencyInstallDecision,
				Evidence:       "dependency/environment change: " + p,
				Recommendation: "pin and vendor dependencies; install only from trusted, reviewed sources",
			})
			return
		}
	}
}

// scanResourceText flags obvious resource-abuse patterns in raw text
// (long sleeps, infinite loops, unbounded output sources).
func (s *scanner) scanResourceText(text string) {
	lc := strings.ToLower(text)
	if strings.Contains(lc, "while true") || strings.Contains(lc, "while :;") ||
		strings.Contains(lc, "for(;;)") || hasYesCommand(lc) {
		s.add(Finding{
			RuleID:         RuleResourceAbuse,
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "possible infinite loop / unbounded output",
			Recommendation: "bound the loop and cap output; set an explicit timeout",
		})
	}
	for _, src := range infiniteSources {
		if strings.Contains(lc, src) {
			s.add(Finding{
				RuleID:         RuleResourceAbuse,
				RiskLevel:      RiskMedium,
				Decision:       DecisionAsk,
				Evidence:       "reads unbounded source: " + src,
				Recommendation: "cap the amount read (head -c) and set an output limit",
			})
			break
		}
	}
	if s.policy.Limits.MaxSleepSec > 0 {
		if sec, ok := longestSleep(lc); ok && sec > s.policy.Limits.MaxSleepSec {
			s.add(Finding{
				RuleID:         RuleResourceAbuse,
				RiskLevel:      RiskLow,
				Decision:       DecisionAsk,
				Evidence:       "long sleep: " + strconv.Itoa(sec) + "s",
				Recommendation: "avoid long blocking sleeps or raise limits.max_sleep_sec",
			})
		}
	}
}

// scanMetadata weighs tool metadata flags.
func (s *scanner) scanMetadata(req Request) {
	if req.Destructive {
		s.add(Finding{
			RuleID:         RuleDestructiveIntent,
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "tool metadata marks the call as destructive",
			Recommendation: "confirm the destructive operation is intended before execution",
		})
	}
}

// scanEnv checks environment-variable names against the policy.
func (s *scanner) scanEnv(req Request) {
	if len(req.Env) == 0 {
		return
	}
	names := make([]string, 0, len(req.Env))
	for k := range req.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	denied := toLowerSet(s.policy.Env.DeniedNames)
	allowed := toLowerSet(s.policy.Env.AllowedNames)
	for _, name := range names {
		val := req.Env[name]
		s.scanSecrets(name + "=" + val)
		if _, bad := denied[strings.ToLower(name)]; bad {
			s.add(Finding{
				RuleID:         RuleEnvPolicy,
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       "denied environment variable: " + name,
				Recommendation: "remove the variable; it can alter loader or shell behaviour",
			})
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(name)]; !ok {
				s.add(Finding{
					RuleID:         RuleEnvPolicy,
					RiskLevel:      RiskLow,
					Decision:       DecisionAsk,
					Evidence:       "environment variable not in allowlist: " + name,
					Recommendation: "add the name to env.allowed_names if it is required",
				})
			}
		}
	}
}

// scanExecParams weighs execution parameters (timeout, background, TTY).
func (s *scanner) scanExecParams(req Request) {
	if s.policy.Limits.MaxTimeoutSec > 0 && req.TimeoutSec > s.policy.Limits.MaxTimeoutSec {
		s.add(Finding{
			RuleID:         RuleResourceAbuse,
			RiskLevel:      RiskLow,
			Decision:       DecisionAsk,
			Evidence:       "requested timeout " + strconv.Itoa(req.TimeoutSec) + "s exceeds limit",
			Recommendation: "lower the timeout or raise limits.max_timeout_sec",
		})
	}
	if req.Backend != BackendHostExec {
		return
	}
	if req.Background && !s.policy.HostExec.AllowBackground {
		s.add(Finding{
			RuleID:         RuleHostExecRisk,
			RiskLevel:      RiskHigh,
			Decision:       s.policy.HostExec.Decision,
			Evidence:       "host background session requested",
			Recommendation: "background host sessions can leave processes running; require review or set host_exec.allow_background",
		})
	}
	if req.TTY && !s.policy.HostExec.AllowPTY {
		s.add(Finding{
			RuleID:         RuleHostExecRisk,
			RiskLevel:      RiskHigh,
			Decision:       s.policy.HostExec.Decision,
			Evidence:       "host PTY session requested",
			Recommendation: "interactive host PTYs bypass command logging; require review or set host_exec.allow_pty",
		})
	}
}

// scanCodeBlock analyses one code block for secrets, sensitive paths
// and destructive host bridges (os.system, subprocess, exec.Command).
func (s *scanner) scanCodeBlock(b CodeBlock) {
	s.scanSecrets(b.Code)
	s.scanSensitivePaths(b.Code)
	s.scanDestructive(b.Code)
	lc := strings.ToLower(b.Code)
	for _, bridge := range hostBridgePatterns {
		if strings.Contains(lc, bridge) {
			s.add(Finding{
				RuleID:         RuleHostExecRisk,
				RiskLevel:      RiskMedium,
				Decision:       DecisionAsk,
				Evidence:       "code shells out to the host via " + bridge,
				Recommendation: "run untrusted code in codeexecutor/container or E2B, never on the host",
			})
			return
		}
	}
}

func (s *scanner) egressCommands() map[string]struct{} {
	if len(s.policy.Network.EgressCommands) > 0 {
		return toLowerSet(s.policy.Network.EgressCommands)
	}
	return defaultEgress
}

func (s *scanner) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, h := range s.policy.Network.AllowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if strings.HasPrefix(h, "*.") {
			if host == h[2:] || strings.HasSuffix(host, h[1:]) {
				return true
			}
			continue
		}
		if host == h {
			return true
		}
	}
	return false
}

func (s *scanner) redactString(text string) string {
	if !s.policy.redact() {
		return text
	}
	return redactSecrets(text)
}
