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
	// RawArgs are opaque string values harvested from a non-shell tool
	// call (for example the field values of an MCP tool's JSON
	// arguments). They are scanned for secrets, sensitive paths,
	// destructive fragments, dependency installs and network hosts, but
	// are never treated as a single shell command line. Optional.
	RawArgs []string
	// Workdir is the requested working directory. Relative path
	// operands are resolved against it before the sensitive-path rule
	// runs, so a denied file reached via a workdir cannot slip through.
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

	sc.workdir = req.Workdir
	sc.scanMetadata(req)
	sc.scanEnv(req)
	sc.scanExecParams(req)
	// A denied path can be reached via {workdir:"/etc", command:"cat
	// shadow"} without the command text containing the fragment, so the
	// workdir itself is checked against the sensitive-path rule.
	if strings.TrimSpace(req.Workdir) != "" {
		sc.scanSensitivePaths(req.Workdir)
	}

	// Command line analysis.
	if strings.TrimSpace(req.Command) != "" {
		sc.scanCommand(req.Command, report.Backend)
	}
	// argv analysis (already-split commands).
	if len(req.Args) > 0 {
		sc.scanArgv(req.Args)
	}
	// RawArgs: opaque field values from a non-shell (e.g. MCP) tool.
	// Each value is scanned individually and any URL-shaped value has
	// its host checked, but they are never joined into a shell command.
	if len(req.RawArgs) > 0 {
		sc.scanRawArgs(req.RawArgs)
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
	// workdir is the request working directory used to resolve relative
	// path operands before the sensitive-path rule runs.
	workdir string
	// seen deduplicates findings by rule + evidence so the same issue
	// reported by two overlapping passes (e.g. the raw-text and parsed-
	// argv recursive-delete checks) appears once.
	seen map[string]struct{}
}

func (s *scanner) add(f Finding) {
	f.Evidence = s.redactString(f.Evidence)
	key := f.RuleID + "\x00" + f.Evidence
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	if _, dup := s.seen[key]; dup {
		return
	}
	s.seen[key] = struct{}{}
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
	// Per-segment structural checks: recursive-delete flag parsing and
	// relative-path resolution need the split argv, which the raw-text
	// substring rules above cannot see.
	for _, argv := range pipe.Commands {
		s.scanArgvStructure(argv)
	}
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
	s.scanArgvStructure(argv)
}

// scanArgvStructure runs the checks that need a split argv rather than
// raw text: recursive-delete flag parsing (rm -r -f /, rm -rf --) and
// sensitive paths resolved relative to the request workdir.
func (s *scanner) scanArgvStructure(argv []string) {
	if ev, ok := analyzeRm(argv); ok {
		s.add(Finding{
			RuleID:         RuleDangerousCommand,
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       ev,
			Recommendation: "this operation is irreversible; scope the path or run only inside a disposable sandbox",
		})
	}
	s.scanResolvedPaths(argv)
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
// and checks EVERY target host against the allowlist. A command such
// as "curl https://ok.example/a https://evil.example/b" is reported
// because the second destination is not allowlisted, even though the
// first is. A segment whose destinations cannot be extracted at all
// (an egress client with no host-shaped operand) still reports once so
// it is never silently allowed.
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
		hosts := allHosts(argv[1:])
		if len(hosts) == 0 {
			// Egress client with no identifiable destination: fail to
			// the configured egress decision rather than allow.
			s.add(Finding{
				RuleID:         RuleNetworkEgress,
				RiskLevel:      RiskHigh,
				Decision:       s.policy.Network.Decision,
				Evidence:       "network egress via " + base + " with no verifiable destination",
				Recommendation: "pass an explicit destination host so it can be checked against network.allowed_hosts",
			})
			continue
		}
		for _, host := range hosts {
			if s.hostAllowed(host) {
				continue
			}
			s.add(Finding{
				RuleID:         RuleNetworkEgress,
				RiskLevel:      RiskHigh,
				Decision:       s.policy.Network.Decision,
				Evidence:       "network egress via " + base + " to " + host,
				Recommendation: "add the destination host to network.allowed_hosts if it is trusted",
			})
		}
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

// scanSensitivePaths flags references to denied filesystem paths. The
// text is normalised first (redundant "/" collapsed, "\" folded to
// "/") so "cat /etc//shadow" cannot dodge the "/etc/shadow" fragment.
func (s *scanner) scanSensitivePaths(text string) {
	lc := collapseSlashes(strings.ToLower(text))
	for _, p := range s.policy.DeniedPaths {
		if p == "" {
			continue
		}
		if strings.Contains(lc, collapseSlashes(strings.ToLower(p))) {
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

// scanResolvedPaths resolves relative path operands of one argv against
// the request workdir and re-checks the denied-path list, so a denied
// file reached as {workdir:"/etc", command:"cat shadow"} is caught even
// though neither the command nor the workdir alone contains the
// fragment.
func (s *scanner) scanResolvedPaths(argv []string) {
	if s.workdir == "" || len(argv) < 2 {
		return
	}
	base := collapseSlashes(strings.TrimRight(strings.ToLower(s.workdir), "/"))
	for _, tok := range argv[1:] {
		tok = strings.Trim(strings.TrimSpace(tok), `"'`)
		if tok == "" || strings.HasPrefix(tok, "-") {
			continue
		}
		// Only relative operands need joining; absolute ones were
		// already covered by the raw-text pass.
		if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
			continue
		}
		resolved := collapseSlashes(base + "/" + strings.ToLower(tok))
		for _, p := range s.policy.DeniedPaths {
			if p == "" {
				continue
			}
			if strings.Contains(resolved, collapseSlashes(strings.ToLower(p))) {
				s.add(Finding{
					RuleID:         RuleSensitivePath,
					RiskLevel:      RiskHigh,
					Decision:       DecisionDeny,
					Evidence:       "sensitive path access via workdir: " + s.workdir + "/" + tok,
					Recommendation: "do not read credential or key material; use scoped, injected secrets",
				})
			}
		}
	}
}

// scanRawArgs scans opaque field values from a non-shell tool call
// (for example the JSON argument values of an MCP tool). Each value is
// run through the text rules and, when it is URL-shaped, its host is
// checked against the egress allowlist — without ever concatenating the
// values into a shell command line (which would misparse structured
// JSON and could mask a non-allowlisted URL).
func (s *scanner) scanRawArgs(values []string) {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		s.scanSecrets(v)
		s.scanSensitivePaths(v)
		s.scanDestructive(v)
		s.scanDependency(v)
		if looksLikeURL(v) {
			host := hostFromToken(v)
			if host != "" && !s.hostAllowed(host) {
				s.add(Finding{
					RuleID:         RuleNetworkEgress,
					RiskLevel:      RiskHigh,
					Decision:       s.policy.Network.Decision,
					Evidence:       "network destination in tool arguments: " + host,
					Recommendation: "add the destination host to network.allowed_hosts if it is trusted",
				})
			}
		}
	}
}

// scanDestructive matches destructive command patterns in raw text.
// Recursive "rm" deletes are handled structurally by analyzeRm on the
// parsed argv (see scanArgvStructure); this text pass additionally
// tokenises any "rm" run it can find so a catastrophic delete inside a
// non-shell code block (e.g. os.system("rm -r -f /")) is still caught
// even though it never reaches shellsafe.
func (s *scanner) scanDestructive(text string) {
	lc := strings.ToLower(text)
	for _, seg := range rmSegments(text) {
		if ev, ok := analyzeRm(seg); ok {
			s.add(Finding{
				RuleID:         RuleDangerousCommand,
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       ev,
				Recommendation: "this operation is irreversible; scope the path or run only inside a disposable sandbox",
			})
			break
		}
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
			rec := "cap the amount read (head -c) and set an output limit"
			if s.policy.Limits.MaxOutputBytes > 0 {
				rec += "; policy advises at most " +
					strconv.FormatInt(s.policy.Limits.MaxOutputBytes, 10) + " bytes"
			}
			s.add(Finding{
				RuleID:         RuleResourceAbuse,
				RiskLevel:      RiskMedium,
				Decision:       DecisionAsk,
				Evidence:       "reads unbounded source: " + src,
				Recommendation: rec,
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
// For shell-language blocks it additionally routes each executable
// statement through the same structural command rules used for a shell
// command line (command policy, network egress, recursive delete,
// dependency install), so a Bash block that runs "curl
// https://evil.example" or "pip install x" is no longer weaker than
// the equivalent workspace_exec command.
func (s *scanner) scanCodeBlock(b CodeBlock) {
	s.scanSecrets(b.Code)
	s.scanSensitivePaths(b.Code)
	s.scanDestructive(b.Code)
	s.scanDependency(b.Code)
	s.scanResourceText(b.Code)

	if isShellLanguage(b.Language) {
		s.scanShellScript(b.Code)
	}

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

// scanShellScript applies the per-command structural rules to each
// line of a shell-language code block. A line that cannot be parsed
// conservatively fails closed via the parse-error decision, matching
// the command-line path.
func (s *scanner) scanShellScript(code string) {
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pipe, err := shellsafe.Parse(line)
		if err != nil {
			s.add(Finding{
				RuleID:         RuleParseError,
				RiskLevel:      RiskHigh,
				Decision:       s.policy.ParseErrorDecision,
				Evidence:       "shell code block line could not be parsed conservatively: " + err.Error(),
				Recommendation: "avoid shell substitution/redirection in executed code, or run it in an isolated sandbox",
			})
			s.flagShellBypass(line)
			continue
		}
		s.applyCommandPolicy(pipe)
		s.scanNetwork(pipe)
		s.scanPipelineLimits(pipe)
		for _, argv := range pipe.Commands {
			s.scanArgvStructure(argv)
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
