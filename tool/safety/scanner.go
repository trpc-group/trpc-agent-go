//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const (
	ruleAllow           = "SAFE-ALLOW"
	ruleUnknownTool     = "SAFE-UNKNOWN-TOOL"
	ruleParse           = "SAFE-SHELL-PARSE"
	ruleCommandPolicy   = "SAFE-COMMAND-POLICY"
	ruleDangerousDelete = "SAFE-DANGEROUS-DELETE"
	ruleSensitivePath   = "SAFE-SENSITIVE-PATH"
	ruleNetwork         = "SAFE-NETWORK-DOMAIN"
	rulePipeline        = "SAFE-SHELL-PIPELINE"
	ruleDependency      = "SAFE-DEPENDENCY-INSTALL"
	ruleTimeout         = "SAFE-RESOURCE-TIMEOUT"
	ruleOutput          = "SAFE-RESOURCE-OUTPUT"
	ruleConcurrency     = "SAFE-RESOURCE-CONCURRENCY"
	ruleInfiniteLoop    = "SAFE-RESOURCE-INFINITE-LOOP"
	ruleHostPTY         = "SAFE-HOSTEXEC-PTY"
	ruleHostSession     = "SAFE-HOSTEXEC-SESSION"
	ruleHostPath        = "SAFE-HOSTEXEC-PATH"
	ruleBackground      = "SAFE-BACKGROUND-PROCESS"
	ruleEnv             = "SAFE-ENV-VAR"
	ruleSecret          = "SAFE-SECRET-REDACTION"
	ruleArgumentFailure = "SAFE-ARGUMENT-EXTRACTION"
	ruleAuditFailure    = "SAFE-AUDIT-FAILURE"
	ruleCodePolicy      = "SAFE-CODE-POLICY"
	ruleHostStartupEnv  = "SAFE-HOSTEXEC-STARTUP-ENV"
	ruleVCSMutation     = "SAFE-VCS-MUTATION"

	// These values mirror the execution defaults in tool/hostexec.
	defaultHostExecYieldMS    = 10_000
	defaultHostExecTimeoutSec = 1_800
)

var (
	urlPattern           = regexp.MustCompile(`(?i)\b(?:https?|ftps?|ssh|git)://[^\s"'` + "`" + `]+`)
	longSleepPattern     = regexp.MustCompile(`(?i)(^|[;&|[:space:]])sleep[[:space:]]+([^;&|[:space:]]+)`)
	infiniteWhilePattern = regexp.MustCompile(
		`^[[:space:]]*while[[:space:]]+(true|:)[[:space:]]*;?[[:space:]]*do\b`,
	)
	infiniteForPattern = regexp.MustCompile(
		`^[[:space:]]*for[[:space:]]*\(\([[:space:]]*;[[:space:]]*;[[:space:]]*\)\)[[:space:]]*;?[[:space:]]*do\b`,
	)
	codeStringPattern = regexp.MustCompile(
		`(?s)["']([^"'\\]*(?:\\.[^"'\\]*)*)["']`,
	)
	codeDeleteAPIPattern = regexp.MustCompile(
		`(?i)\b(?:os\.(?:remove|unlink|rmdir)|shutil\.rmtree)\s*\(`,
	)
	codeProcessAPIPattern = regexp.MustCompile(
		`(?i)\b(?:os\.(?:system|popen)|subprocess\.(?:call|check_call|check_output|popen|run))\s*\(`,
	)
	codeRMArgumentPattern = regexp.MustCompile(
		`(?i)["']rm["']`,
	)
	codeInfiniteLoopPattern = regexp.MustCompile(
		`(?im)^[[:space:]]*while[[:space:]]+(?:true|1)[[:space:]]*:`,
	)
	codeConstantLoopPattern = regexp.MustCompile(
		`(?is)\b(?:while\s*(?:\(\s*)?(?:true|1\s*==\s*1)(?:\s*\))?\s*(?::|\{)|for\s*\{\s*\})`,
	)
	codeDeletePattern = regexp.MustCompile(
		`(?i)\b(?:rmtree|rmSync|rm|unlinkSync|removeSync|RemoveAll)\s*\(`,
	)
	codeProcessPattern = regexp.MustCompile(
		`(?i)(?:\b(?:system|popen)\s*\(|\bsubprocess\.\w+\s*\(|\bchild_process\.|\bexecSync\s*\(|\bspawnSync\s*\()`,
	)
	codeNetworkPattern = regexp.MustCompile(
		`(?i)(?:\b(?:requests|httpx|axios)\.\w+\s*\(|\burllib(?:\.request)?\.urlopen\s*\(|\bsocket\.(?:create_connection|connect)\s*\(|\bfetch\s*\(|\bnet\.Dial(?:Timeout)?\s*\(|\bhttp\.(?:Get|Post|NewRequest)\s*\()`,
	)
	codeDynamicExecutionPattern = regexp.MustCompile(
		`(?i)(?:\b__import__\s*\(|\bimportlib\.\w+\s*\(|\b(?:getattr|setattr|globals|locals|vars|compile)\s*\(|\beval\s*\(|\bexec\s*\(|\bnew\s+Function\s*\(|\bimport\s*\([^"']|\brequire\s*\([^"']|\brequire\s*\([^)]*\)\s*\[)`,
	)
)

// Scanner applies a safety policy to pending tool executions.
type Scanner struct {
	policy    Policy
	policyErr error
	auditor   Auditor
	now       func() time.Time
}

// Option configures Scanner.
type Option func(*Scanner)

// WithAuditor records every scan report as a compact audit event.
func WithAuditor(a Auditor) Option {
	return func(s *Scanner) {
		s.auditor = a
	}
}

// NewScanner creates a Scanner.
func NewScanner(policy Policy, opts ...Option) *Scanner {
	policy.normalize()
	s := &Scanner{policy: policy, policyErr: policy.Validate(), now: time.Now}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Scan inspects a pending execution and returns a structured report.
func (s *Scanner) Scan(ctx context.Context, req ScanRequest) (ScanReport, error) {
	if s == nil {
		s = NewScanner(DefaultPolicy())
	}
	if s.policyErr != nil {
		return ScanReport{}, fmt.Errorf("invalid safety policy: %w", s.policyErr)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := s.now()
	req = normalizeScanRequest(req)
	findings, envRedacted := s.scanFindings(req)
	report := buildReport(req, findings)
	report.DurationMS = s.now().Sub(start).Milliseconds()
	if report.DurationMS < 0 {
		report.DurationMS = 0
	}
	return s.finishReport(ctx, report, envRedacted)
}

func (s *Scanner) scanArgumentFailure(
	ctx context.Context,
	toolName string,
) (ScanReport, error) {
	if s == nil {
		s = NewScanner(DefaultPolicy())
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := s.now()
	req := normalizeScanRequest(ScanRequest{
		ToolName: toolName,
	})
	report := buildReport(req, []Finding{finding(
		ruleArgumentFailure,
		DecisionDeny,
		RiskHigh,
		"tool arguments could not be normalized for safety scanning",
		"Provide valid JSON arguments with all required execution fields.",
	)})
	report.DurationMS = s.now().Sub(start).Milliseconds()
	if report.DurationMS < 0 {
		report.DurationMS = 0
	}
	return s.finishReport(ctx, report, true)
}

func (s *Scanner) finishReport(
	ctx context.Context,
	report ScanReport,
	redacted bool,
) (ScanReport, error) {
	report.Command, report.Redacted = redactCommand(report.Command)
	report.Redacted = report.Redacted || redacted
	for i := range report.Evidence {
		ev, redacted := redactString(report.Evidence[i])
		report.Evidence[i] = ev
		report.Redacted = report.Redacted || redacted
	}
	for i := range report.Findings {
		ev, redacted := redactString(report.Findings[i].Evidence)
		report.Findings[i].Evidence = ev
		report.Redacted = report.Redacted || redacted
	}
	report.OTelAttributes = map[string]string{
		OTelAttrDecision:  string(report.Decision),
		OTelAttrRiskLevel: string(report.RiskLevel),
		OTelAttrRuleID:    report.RuleID,
		OTelAttrBackend:   string(report.Backend),
	}
	setSpanAttributes(ctx, report)
	if s.auditor != nil {
		if err := s.auditor.Record(AuditEvent{
			Timestamp:  s.now().UTC(),
			ToolName:   report.ToolName,
			Decision:   report.Decision,
			RiskLevel:  report.RiskLevel,
			RuleID:     report.RuleID,
			DurationMS: report.DurationMS,
			Redacted:   report.Redacted,
			Blocked:    report.Blocked,
			Backend:    report.Backend,
		}); err != nil {
			report.Decision = DecisionDeny
			report.RiskLevel = RiskHigh
			report.RuleID = ruleAuditFailure
			report.Blocked = true
			report.Recommendation = "Restore the safety audit sink before allowing tool execution."
			report.Evidence = append(report.Evidence, "safety audit event could not be recorded")
			report.OTelAttributes[OTelAttrDecision] = string(report.Decision)
			report.OTelAttributes[OTelAttrRiskLevel] = string(report.RiskLevel)
			report.OTelAttributes[OTelAttrRuleID] = report.RuleID
			setSpanAttributes(ctx, report)
			return report, fmt.Errorf("record safety audit: %w", err)
		}
	}
	select {
	case <-ctx.Done():
		return report, ctx.Err()
	default:
		return report, nil
	}
}

func setSpanAttributes(ctx context.Context, report ScanReport) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String(OTelAttrDecision, string(report.Decision)),
		attribute.String(OTelAttrRiskLevel, string(report.RiskLevel)),
		attribute.String(OTelAttrRuleID, report.RuleID),
		attribute.String(OTelAttrBackend, string(report.Backend)),
	)
}

func (s *Scanner) scanFindings(req ScanRequest) ([]Finding, bool) {
	var findings []Finding
	if req.Backend == BackendUnknown {
		action := s.policy.UnknownToolAction
		risk := RiskMedium
		evidence := "tool is not recognized by the safety guard"
		if req.Metadata.Destructive {
			action = DecisionDeny
			risk = RiskHigh
			evidence += " and publishes destructive metadata"
		} else if req.Metadata.OpenWorld {
			risk = RiskHigh
			evidence += " and publishes open-world metadata"
		}
		return append(findings, finding(
			ruleUnknownTool,
			action,
			risk,
			evidence,
			"Register a backend extractor before relying on this guard for the tool.",
		)), false
	}
	command := strings.TrimSpace(req.Command)

	shellCommand := command
	if req.validatedCode {
		shellCommand = strings.TrimSpace(req.shellCommand)
	}

	var pipe *shellsafe.Pipeline
	if shellCommand != "" || !req.validatedCode {
		var shellFindings []Finding
		pipe, shellFindings = s.scanShell(shellCommand)
		findings = append(findings, shellFindings...)
	}
	findings = append(findings, s.scanCommandPatterns(command, req, pipe)...)
	if req.Backend == BackendHostExec && hasEnvKey(req.Env, "PATH") {
		findings = append(findings, finding(
			ruleHostPath,
			DecisionDeny,
			RiskCritical,
			"hostexec explicitly overrides the executable search path",
			"Use the inherited host PATH and invoke only policy-approved executables.",
		))
	}
	if req.Backend == BackendHostExec {
		if key := firstHostStartupEnvOverride(req.Env); key != "" {
			findings = append(findings, finding(
				ruleHostStartupEnv,
				DecisionDeny,
				RiskCritical,
				"hostexec overrides execution-affecting environment variable: "+key,
				"Use the inherited host execution environment and pass data through non-executable inputs.",
			))
		}
	}
	if key, host := firstDisallowedProxyEnvHost(
		req.Env,
		s.policy.NetworkAllowlist,
	); host != "" {
		findings = append(findings, finding(
			ruleNetwork,
			DecisionDeny,
			RiskHigh,
			"environment variable "+key+
				" routes network traffic through non-allowlisted host: "+host,
			"Remove the proxy override or use an explicitly reviewed allowlisted proxy.",
		))
	}
	envFindings, envRedacted := s.scanEnv(req.Env)
	findings = append(findings, envFindings...)
	if req.Backend == BackendHostExec && req.TTY {
		findings = append(findings, finding(
			ruleHostPTY,
			s.policy.HostPTYAction,
			RiskHigh,
			"hostexec requested a PTY session",
			"Use non-interactive workspace execution unless host access and cleanup are explicitly approved.",
		))
	}
	if req.Backend == BackendHostExec &&
		!req.TTY &&
		!req.Background &&
		req.YieldTimeMS > 0 {
		findings = append(findings, finding(
			ruleHostSession,
			DecisionAsk,
			RiskMedium,
			fmt.Sprintf(
				"hostexec may return a running session after yield_time-ms=%d",
				req.YieldTimeMS,
			),
			"Use yield-time-ms=0 with a bounded timeout, or require human review and track session cleanup.",
		))
	}
	if req.Background {
		findings = append(findings, finding(
			ruleBackground,
			s.policy.BackgroundAction,
			RiskMedium,
			"command requested background execution",
			"Prefer bounded foreground execution or require a human to approve and track session cleanup.",
		))
	}
	if req.TimeoutSec > 0 && s.policy.MaxTimeoutSec > 0 &&
		req.TimeoutSec > s.policy.MaxTimeoutSec {
		findings = append(findings, finding(
			ruleTimeout,
			DecisionAsk,
			RiskMedium,
			fmt.Sprintf("timeout_sec=%d exceeds max_timeout_sec=%d", req.TimeoutSec, s.policy.MaxTimeoutSec),
			"Lower the timeout or request human review for long-running work.",
		))
	}
	if req.MaxOutputBytes > 0 && s.policy.MaxOutputBytes > 0 &&
		req.MaxOutputBytes > s.policy.MaxOutputBytes {
		findings = append(findings, finding(
			ruleOutput,
			DecisionAsk,
			RiskMedium,
			fmt.Sprintf("max_output_bytes=%d exceeds max_output_bytes=%d", req.MaxOutputBytes, s.policy.MaxOutputBytes),
			"Limit output size or write large artifacts to bounded files.",
		))
	}
	if req.Metadata.MaxResultSize > s.policy.MaxOutputBytes {
		findings = append(findings, finding(
			ruleOutput,
			DecisionAsk,
			RiskMedium,
			fmt.Sprintf(
				"tool max result size=%d exceeds max_output_bytes=%d",
				req.Metadata.MaxResultSize,
				s.policy.MaxOutputBytes,
			),
			"Lower the tool result limit or require human review.",
		))
	}
	return findings, envRedacted
}

func (s *Scanner) scanShell(command string) (*shellsafe.Pipeline, []Finding) {
	policy := shellsafe.PolicyFromLists(
		s.policy.AllowedCommands,
		baseDeniedCommands(s.policy.DeniedCommands),
	)
	if !policy.Active() {
		policy = shellsafe.PolicyFromLists(nil, []string{"__activate_implicit_policy__"})
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return nil, []Finding{finding(
			ruleParse,
			s.policy.ParseFailureAction,
			RiskMedium,
			err.Error(),
			"Rewrite the command as plain argv segments without shell expansion, redirection, substitution, or wrappers.",
		)}
	}
	var findings []Finding
	if err := policy.Check(pipe); err != nil {
		findings = append(findings, finding(
			ruleCommandPolicy,
			DecisionDeny,
			RiskHigh,
			err.Error(),
			"Use an allowed command or request a policy update for this workspace.",
		))
	}
	if len(pipe.Commands) > 1 {
		findings = append(findings, finding(
			rulePipeline,
			s.policy.PipelineAction,
			RiskMedium,
			"command contains multiple shell segments joined by a pipeline or sequencing operator",
			"Split the work into separate reviewed commands when possible.",
		))
	}
	return pipe, findings
}

func (s *Scanner) scanCommandPatterns(
	command string,
	req ScanRequest,
	pipe *shellsafe.Pipeline,
) []Finding {
	lower := strings.ToLower(command)
	findings := make([]Finding, 0, 4)
	if req.Backend == BackendCodeExec && len(req.codeBlocks) > 0 {
		findings = append(findings, s.scanCodeBlocks(req.codeBlocks)...)
	}
	if isDangerousDelete(lower, pipe) ||
		(req.Backend == BackendCodeExec &&
			isDangerousCodeDelete(command)) {
		findings = append(findings, finding(
			ruleDangerousDelete,
			DecisionDeny,
			RiskCritical,
			"command contains recursive/force deletion or targets a system directory",
			"Delete only explicit workspace-relative files after human review.",
		))
	}
	if req.Backend == BackendCodeExec &&
		codeProcessAPIPattern.MatchString(command) {
		findings = append(findings, finding(
			ruleCommandPolicy,
			DecisionDeny,
			RiskHigh,
			"code invokes a child process or shell execution API",
			"Use a directly supported, auditable operation instead of launching a nested command interpreter.",
		))
	}
	if path := firstSensitivePath(req, pipe, s.policy.ForbiddenPaths); path != "" {
		findings = append(findings, finding(
			ruleSensitivePath,
			DecisionDeny,
			RiskCritical,
			"command references forbidden path or credential marker: "+path,
			"Remove credential access and pass required values through approved secret handling.",
		))
	}
	if host := firstDisallowedHost(command, pipe, s.policy.NetworkAllowlist); host != "" {
		findings = append(findings, finding(
			ruleNetwork,
			DecisionDeny,
			RiskHigh,
			"network request targets non-allowlisted host: "+host,
			"Add the domain to network_allowlist only after reviewing data exfiltration risk.",
		))
	} else if source := firstUnverifiableNetworkSource(pipe); source != "" {
		findings = append(findings, finding(
			ruleNetwork,
			DecisionAsk,
			RiskHigh,
			"network destination cannot be statically verified because it is loaded from "+source,
			"Use an explicit allowlisted destination or require human review of the referenced configuration.",
		))
	}
	dependencyMutation := ""
	if pipe != nil || !req.validatedCode {
		dependencyMutation = firstDependencyInstall(
			lower,
			pipe,
			s.policy.DeniedCommands,
		)
	}
	if dependencyMutation != "" {
		findings = append(findings, finding(
			ruleDependency,
			s.policy.DependencyAction,
			RiskHigh,
			"command changes dependencies or the host environment: "+dependencyMutation,
			"Pin and review dependency changes before allowing installation.",
		))
	}
	if mutation := firstDestructiveVCSMutation(pipe); mutation != "" {
		findings = append(findings, finding(
			ruleVCSMutation,
			DecisionAsk,
			RiskHigh,
			"version-control command can discard or delete workspace data: "+mutation,
			"Review the affected files and preserve required work before approving the command.",
		))
	}
	sleepSeconds, unboundedSleep := sleepDurationSeconds(lower, pipe)
	if sleepSeconds > 0 && s.policy.MaxTimeoutSec > 0 &&
		sleepSeconds > int64(s.policy.MaxTimeoutSec) {
		findings = append(findings, finding(
			ruleTimeout,
			DecisionAsk,
			RiskMedium,
			fmt.Sprintf(
				"sleep duration %ds exceeds max_timeout_sec=%d",
				sleepSeconds,
				s.policy.MaxTimeoutSec,
			),
			"Use a shorter wait or a bounded polling loop.",
		))
	}
	requested, unboundedConcurrency := requestedConcurrency(pipe)
	if unboundedConcurrency ||
		requested > s.policy.MaxConcurrency {
		evidence := fmt.Sprintf(
			"requested concurrency=%d exceeds max_concurrency=%d",
			requested,
			s.policy.MaxConcurrency,
		)
		if unboundedConcurrency {
			evidence = fmt.Sprintf(
				"requested concurrency is unbounded; max_concurrency=%d",
				s.policy.MaxConcurrency,
			)
		}
		findings = append(findings, finding(
			ruleConcurrency,
			DecisionAsk,
			RiskHigh,
			evidence,
			"Lower the worker count or request human review for highly parallel execution.",
		))
	}
	if unboundedSleep ||
		isObviousInfiniteLoop(lower) ||
		(req.Backend == BackendCodeExec &&
			codeInfiniteLoopPattern.MatchString(command)) {
		findings = append(findings, finding(
			ruleInfiniteLoop,
			DecisionDeny,
			RiskHigh,
			"command contains an obvious unbounded wait or loop",
			"Use a bounded loop with an explicit iteration limit, deadline, or cancellation condition.",
		))
	}
	_, textSecret := redactString(req.Command)
	if textSecret ||
		commandTextHasCredentialOption(req.Command) ||
		commandHasInlineCredential(pipe) {
		findings = append(findings, finding(
			ruleSecret,
			DecisionDeny,
			RiskCritical,
			"command appears to contain an inline secret",
			"Move secrets out of command text and use approved secret injection.",
		))
	}
	return findings
}

func commandHasInlineCredential(
	pipe *shellsafe.Pipeline,
) bool {
	if pipe == nil {
		return false
	}
	for _, argv := range pipe.Commands {
		if len(argv) < 2 {
			continue
		}
		command := strings.ToLower(filepath.Base(argv[0]))
		for i := 1; i < len(argv); i++ {
			switch command {
			case "curl":
				for _, option := range [][2]string{
					{"-u", "--user"},
					{"-U", "--proxy-user"},
				} {
					value, consumed, ok := optionValue(
						argv,
						i,
						option[0],
						option[1],
					)
					if ok {
						if consumed {
							i++
						}
						_, password, hasPassword := strings.Cut(
							value,
							":",
						)
						if hasPassword && password != "" {
							return true
						}
					}
				}
			case "wget":
				for _, option := range []string{
					"--password",
					"--http-password",
					"--ftp-password",
					"--proxy-password",
				} {
					value, consumed, ok := optionValue(
						argv,
						i,
						"",
						option,
					)
					if !ok {
						continue
					}
					if consumed {
						i++
					}
					if value != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

func optionValue(
	argv []string,
	index int,
	short string,
	long string,
) (value string, consumedNext bool, ok bool) {
	arg := argv[index]
	switch {
	case short != "" && arg == short, long != "" && arg == long:
		if index+1 >= len(argv) {
			return "", false, true
		}
		return argv[index+1], true, true
	case short != "" &&
		strings.HasPrefix(arg, short) &&
		len(arg) > len(short):
		return strings.TrimPrefix(arg, short), false, true
	case long != "" && strings.HasPrefix(arg, long+"="):
		return strings.TrimPrefix(arg, long+"="), false, true
	default:
		return "", false, false
	}
}

func (s *Scanner) scanCodeBlocks(blocks []codeBlock) []Finding {
	var findings []Finding
	for _, block := range blocks {
		language := normalizeCodeLanguage(block.language)
		if isShellLanguage(language) {
			continue
		}
		if !isSupportedCodeLanguage(language) {
			findings = append(findings, finding(
				ruleCodePolicy,
				s.policy.ParseFailureAction,
				RiskMedium,
				fmt.Sprintf(
					"code language %q has no conservative safety analyzer",
					block.language,
				),
				"Use a supported language analyzer or require human review before execution.",
			))
			continue
		}

		findings = append(
			findings,
			s.scanLanguageAwareCode(block)...,
		)
		if codeDeletePattern.MatchString(block.code) {
			findings = append(findings, finding(
				ruleDangerousDelete,
				DecisionDeny,
				RiskCritical,
				"code invokes a filesystem deletion API",
				"Delete only explicit workspace-relative files after human review.",
			))
		}
		if codeProcessPattern.MatchString(block.code) {
			findings = append(findings, finding(
				ruleCommandPolicy,
				DecisionDeny,
				RiskHigh,
				"code invokes a child process or shell execution API",
				"Use a directly supported, auditable operation instead of launching a nested command interpreter.",
			))
		}
		if codeDynamicExecutionPattern.MatchString(block.code) {
			findings = append(findings, finding(
				ruleCodePolicy,
				DecisionAsk,
				RiskHigh,
				"code uses dynamic import, reflection, or evaluation that cannot be statically resolved",
				"Replace dynamic execution with direct, auditable API calls or require human review.",
			))
		}
		if codeConstantLoopPattern.MatchString(block.code) {
			findings = append(findings, finding(
				ruleInfiniteLoop,
				DecisionDeny,
				RiskHigh,
				"code contains an obvious loop with a constant true condition",
				"Use a bounded loop with an explicit iteration limit, deadline, or cancellation condition.",
			))
		}
		if codeNetworkPattern.MatchString(block.code) {
			hosts := codeLiteralHosts(block.code)
			if len(hosts) == 0 {
				findings = append(findings, finding(
					ruleCodePolicy,
					DecisionAsk,
					RiskHigh,
					"code performs a network operation whose destination cannot be statically verified",
					"Use a literal allowlisted destination or require human review for dynamic network access.",
				))
				continue
			}
			for _, host := range hosts {
				if hostAllowed(host, s.policy.NetworkAllowlist) {
					continue
				}
				findings = append(findings, finding(
					ruleNetwork,
					DecisionDeny,
					RiskHigh,
					"code network operation targets non-allowlisted host: "+host,
					"Add the domain to network_allowlist only after reviewing data exfiltration risk.",
				))
				break
			}
		}
	}
	return findings
}

func normalizeCodeLanguage(language string) string {
	return strings.ToLower(strings.TrimSpace(language))
}

func isShellLanguage(language string) bool {
	switch language {
	case "bash", "sh", "shell":
		return true
	default:
		return false
	}
}

func isSupportedCodeLanguage(language string) bool {
	switch language {
	case "python", "py",
		"javascript", "js", "node",
		"typescript", "ts",
		"go", "golang":
		return true
	default:
		return false
	}
}

func codeLiteralHosts(code string) []string {
	var hosts []string
	for _, match := range codeStringPattern.FindAllStringSubmatch(code, -1) {
		if len(match) != 2 {
			continue
		}
		literal := strings.TrimSpace(match[1])
		host := networkTargetHost(literal, true)
		if !plausibleNetworkHost(host) {
			continue
		}
		hosts = append(hosts, host)
	}
	return nonEmptyHosts(hosts...)
}

func plausibleNetworkHost(host string) bool {
	host = strings.TrimSpace(host)
	return net.ParseIP(host) != nil ||
		strings.Contains(host, ".") ||
		strings.EqualFold(host, "localhost")
}

func (s *Scanner) scanEnv(env map[string]string) ([]Finding, bool) {
	if len(env) == 0 {
		return nil, false
	}

	allowed := make(map[string]struct{}, len(s.policy.EnvAllowlist))
	for _, key := range s.policy.EnvAllowlist {
		allowed[strings.ToUpper(key)] = struct{}{}
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var findings []Finding
	secretRedacted := false
	for _, key := range keys {
		value := env[key]

		// allowlist 为空时不启用 key 限制，但 secret 扫描仍会继续。
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToUpper(key)]; !ok {
				findings = append(findings, finding(
					ruleEnv,
					s.policy.DisallowedEnvAction,
					RiskMedium,
					"environment variable is not allowlisted: "+key,
					"Pass only explicitly allowed environment variables to tool execution.",
				))
			}
		}

		if _, changed := redactString(key + "=" + value); changed {
			secretRedacted = true
			findings = append(findings, finding(
				ruleSecret,
				DecisionDeny,
				RiskCritical,
				"environment contains a secret-like value: "+
					key+"=[REDACTED]",
				"Move secrets to approved secret storage and avoid audit-visible env overrides.",
			))
		}
	}

	return findings, secretRedacted
}

func hasEnvKey(env map[string]string, want string) bool {
	for key := range env {
		if strings.EqualFold(strings.TrimSpace(key), want) {
			return true
		}
	}
	return false
}

func firstHostStartupEnvOverride(env map[string]string) string {
	for _, want := range []string{
		"HOME",
		"BASH_ENV",
		"ENV",
		"SHELLOPTS",
		"BASHOPTS",
		"PROMPT_COMMAND",
		"CDPATH",
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"DYLD_INSERT_LIBRARIES",
		"DYLD_LIBRARY_PATH",
		"PYTHONPATH",
		"PYTHONSTARTUP",
		"NODE_OPTIONS",
		"RUBYOPT",
		"PERL5OPT",
		"GIT_CONFIG",
		"GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_SYSTEM",
		"GIT_CONFIG_COUNT",
		"GIT_SSH",
		"GIT_SSH_COMMAND",
		"SSH_ASKPASS",
	} {
		if hasEnvKey(env, want) {
			return want
		}
	}
	return ""
}

func firstDisallowedProxyEnvHost(
	env map[string]string,
	allowlist []string,
) (string, string) {
	for key, value := range env {
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "ALL_PROXY", "HTTP_PROXY", "HTTPS_PROXY", "FTP_PROXY":
		default:
			continue
		}
		host := networkTargetHost(strings.TrimSpace(value), true)
		if plausibleNetworkHost(host) && !hostAllowed(host, allowlist) {
			return key, host
		}
	}
	return "", ""
}

func normalizeScanRequest(req ScanRequest) ScanRequest {
	req.ToolName = strings.TrimSpace(req.ToolName)
	if req.Backend == "" {
		req.Backend = backendForTool(req.ToolName)
	}
	if req.Backend == "" {
		req.Backend = BackendUnknown
	}
	return req
}

func backendForTool(name string) Backend {
	switch name {
	case "workspace_exec":
		return BackendWorkspaceExec
	case "exec_command":
		return BackendHostExec
	case "execute_code":
		return BackendCodeExec
	default:
		return BackendUnknown
	}
}

func buildReport(req ScanRequest, findings []Finding) ScanReport {
	decision := DecisionAllow
	risk := RiskLow
	rule := ruleAllow
	recommendation := "Command is allowed by the current safety policy."
	evidence := []string{"no safety rule matched"}
	if len(findings) > 0 {
		sortFindings(findings)
		top := findings[0]
		decision = top.Decision
		risk = top.RiskLevel
		rule = top.RuleID
		recommendation = top.Recommendation
		evidence = make([]string, 0, len(findings))
		for _, f := range findings {
			evidence = append(evidence, f.Evidence)
		}
	}
	return ScanReport{
		Decision:       decision,
		RiskLevel:      risk,
		RuleID:         rule,
		Evidence:       evidence,
		Recommendation: recommendation,
		ToolName:       req.ToolName,
		Command:        req.Command,
		Backend:        req.Backend,
		Blocked:        decision != DecisionAllow,
		Findings:       findings,
	}
}

func finding(
	rule string,
	decision Decision,
	risk RiskLevel,
	evidence string,
	recommendation string,
) Finding {
	if decision == "" {
		decision = DecisionAsk
	}
	return Finding{
		RuleID:         rule,
		Decision:       decision,
		RiskLevel:      risk,
		Evidence:       evidence,
		Recommendation: recommendation,
	}
}

func sortFindings(findings []Finding) {
	for i := 1; i < len(findings); i++ {
		for j := i; j > 0 && findingRank(findings[j]) > findingRank(findings[j-1]); j-- {
			findings[j], findings[j-1] = findings[j-1], findings[j]
		}
	}
}

func findingRank(f Finding) int {
	return decisionRank(f.Decision)*10 + riskRank(f.RiskLevel)
}

func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func baseDeniedCommands(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		fields := strings.Fields(s)
		if len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

func isDangerousDelete(lower string, pipe *shellsafe.Pipeline) bool {
	if strings.Contains(lower, ">/etc/") || strings.Contains(lower, "> /etc/") {
		return true
	}
	if pipe == nil {
		return strings.Contains(lower, "rm -rf") ||
			strings.Contains(lower, "rm -fr") ||
			strings.Contains(lower, "rm --recursive --force")
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 || !strings.EqualFold(filepath.Base(argv[0]), "rm") {
			continue
		}
		recursive := false
		force := false
		var targets []string
		for _, arg := range argv[1:] {
			if strings.HasPrefix(arg, "-") {
				recursive = recursive || strings.Contains(arg, "r") ||
					arg == "--recursive"
				force = force || strings.Contains(arg, "f") ||
					arg == "--force"
				continue
			}
			targets = append(targets, arg)
		}
		if recursive && force {
			return true
		}
		for _, target := range targets {
			if systemPath(target) {
				return true
			}
		}
	}
	return false
}

func systemPath(raw string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	switch clean {
	case "/", "/*", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64",
		"/proc", "/root", "/sbin", "/sys", "/usr", "/var":
		return true
	default:
		return false
	}
}

func firstSensitivePath(
	req ScanRequest,
	pipe *shellsafe.Pipeline,
	configured []string,
) string {
	candidates := []string{req.Cwd}
	if pipe != nil {
		for _, argv := range pipe.Commands {
			candidates = append(candidates, argv[1:]...)
			if len(argv) > 1 &&
				strings.EqualFold(filepath.Base(argv[0]), "git") {
				for _, arg := range argv[1:] {
					if _, objectPath, ok := strings.Cut(arg, ":"); ok &&
						objectPath != "" {
						candidates = append(candidates, objectPath)
					}
				}
			}
		}
	} else {
		candidates = append(candidates, strings.Fields(req.Command)...)
	}
	if req.Backend == BackendCodeExec {
		for _, match := range codeStringPattern.FindAllStringSubmatch(
			req.Command,
			-1,
		) {
			if len(match) == 2 {
				candidates = append(candidates, match[1])
			}
		}
	}
	for _, candidate := range candidates {
		if matched := matchSensitivePath(candidate, configured); matched != "" {
			return matched
		}
	}
	return ""
}

func matchSensitivePath(candidate string, configured []string) string {
	candidate = strings.Trim(
		strings.TrimSpace(candidate),
		`"'(){}[];|&<>`,
	)
	if candidate == "" {
		return ""
	}
	candidate = filepath.ToSlash(filepath.Clean(candidate))
	lowerCandidate := strings.ToLower(candidate)
	base := strings.ToLower(filepath.Base(candidate))
	for _, configuredPath := range configured {
		normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(
			strings.TrimSpace(configuredPath),
		)))
		if normalized == "" || normalized == "." {
			continue
		}
		configuredBase := strings.ToLower(filepath.Base(normalized))
		if strings.Contains(normalized, "/") {
			if lowerCandidate == normalized ||
				strings.HasPrefix(lowerCandidate, normalized+"/") {
				return configuredPath
			}
			withoutHome := strings.TrimPrefix(normalized, "~/")
			if withoutHome != normalized &&
				(lowerCandidate == withoutHome ||
					strings.HasSuffix(lowerCandidate, "/"+withoutHome) ||
					strings.Contains(lowerCandidate, "/"+withoutHome+"/")) {
				return configuredPath
			}
			continue
		}
		if base == configuredBase ||
			matchesSensitiveFileVariant(base, configuredBase) {
			return configuredPath
		}
	}
	return ""
}

func matchesSensitiveFileVariant(
	base string,
	configuredBase string,
) bool {
	if configuredBase == ".env" &&
		strings.HasPrefix(base, ".env.") {
		suffix := strings.TrimPrefix(base, ".env.")
		switch suffix {
		case "example", "sample", "template", "dist":
			return false
		default:
			return suffix != ""
		}
	}

	if configuredBase != "credential" &&
		configuredBase != "credentials" {
		return false
	}

	extension := strings.ToLower(filepath.Ext(base))
	if strings.TrimSuffix(base, extension) != configuredBase {
		return false
	}
	switch extension {
	case ".json", ".yaml", ".yml", ".toml", ".ini",
		".conf", ".config", ".xml", ".env":
		return true
	default:
		return false
	}
}

func firstDisallowedHost(
	command string,
	pipe *shellsafe.Pipeline,
	allowlist []string,
) string {
	var hosts []string
	if pipe != nil {
		for _, argv := range pipe.Commands {
			hosts = append(hosts, networkCommandHosts(argv)...)
		}
	} else {
		hosts = append(hosts, explicitURLHosts(command)...)
	}
	for _, host := range hosts {
		if !hostAllowed(host, allowlist) {
			return host
		}
	}
	return ""
}

func firstUnverifiableNetworkSource(
	pipe *shellsafe.Pipeline,
) string {
	if pipe == nil {
		return ""
	}
	for _, argv := range pipe.Commands {
		if len(argv) < 2 {
			continue
		}
		command := strings.ToLower(filepath.Base(argv[0]))
		switch command {
		case "curl":
			if hasOption(argv[1:], "-K", "--config") {
				return "a curl configuration file"
			}
		case "wget":
			if hasOption(argv[1:], "", "--config") {
				return "a wget configuration file"
			}
		case "git":
			if source := unverifiableGitNetworkSource(argv[1:]); source != "" {
				return source
			}
		}
	}
	return ""
}

func hasOption(args []string, short string, long string) bool {
	for _, arg := range args {
		if short != "" &&
			(arg == short ||
				(strings.HasPrefix(arg, short) && len(arg) > len(short))) {
			return true
		}
		if long != "" &&
			(arg == long || strings.HasPrefix(arg, long+"=")) {
			return true
		}
	}
	return false
}

func unverifiableGitNetworkSource(args []string) string {
	subcommandIndex := firstGitSubcommandIndex(args)
	if subcommandIndex < 0 {
		return ""
	}
	subcommand := strings.ToLower(args[subcommandIndex])
	remaining := args[subcommandIndex+1:]
	switch subcommand {
	case "fetch", "pull", "push":
		target := firstPositionalArg(remaining)
		if target == "" {
			return "Git remote configuration"
		}
		if isLocalGitTarget(target) {
			return ""
		}
		if networkTargetHost(target, false) == "" {
			return "Git remote configuration"
		}
	case "submodule":
		if len(remaining) > 0 {
			action := strings.ToLower(remaining[0])
			switch action {
			case "update", "sync", "foreach":
				return ".gitmodules or Git submodule configuration"
			}
		}
	}
	return ""
}

func isLocalGitTarget(target string) bool {
	target = strings.TrimSpace(target)
	return target == "." ||
		target == ".." ||
		strings.HasPrefix(target, "./") ||
		strings.HasPrefix(target, "../") ||
		strings.HasPrefix(target, "/") ||
		strings.HasPrefix(target, "file://")
}

func networkCommandHosts(argv []string) []string {
	if len(argv) < 2 {
		return nil
	}
	command := strings.ToLower(filepath.Base(argv[0]))
	switch command {
	case "curl", "wget":
		return downloadCommandHosts(command, argv[1:])
	case "git":
		return gitCommandHosts(argv[1:])
	case "go":
		return goCommandHosts(argv[1:])
	case "ssh", "sftp":
		if target := firstPositionalArg(argv[1:]); target != "" {
			return nonEmptyHosts(remoteHost(target))
		}
	case "scp":
		var hosts []string
		for _, arg := range argv[1:] {
			if strings.HasPrefix(arg, "-") || !strings.Contains(arg, ":") {
				continue
			}
			hosts = append(hosts, remoteHost(arg))
		}
		return nonEmptyHosts(hosts...)
	case "nc", "netcat":
		if target := firstPositionalArg(argv[1:]); target != "" {
			return nonEmptyHosts(remoteHost(target))
		}
	case "echo", "printf":
		return nil
	default:
		var hosts []string
		for _, arg := range argv[1:] {
			hosts = append(hosts, explicitURLHosts(arg)...)
		}
		return hosts
	}
	return nil
}

func downloadCommandHosts(command string, args []string) []string {
	var hosts []string
	skipNext := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if skipNext {
			skipNext = false
			continue
		}
		if command == "curl" {
			optionHosts, consumedNext, handled :=
				curlTransportOptionHosts(args, i)
			if handled {
				hosts = append(hosts, optionHosts...)
				if consumedNext {
					i++
				}
				continue
			}
		}
		if command == "wget" {
			optionHosts, consumedNext, handled :=
				wgetTransportOptionHosts(args, i)
			if handled {
				hosts = append(hosts, optionHosts...)
				if consumedNext {
					i++
				}
				continue
			}
		}
		if arg == "--" {
			for _, target := range args[i+1:] {
				hosts = append(
					hosts,
					networkTargetHost(target, true),
				)
			}
			break
		}
		if strings.HasPrefix(arg, "--url=") {
			hosts = append(
				hosts,
				networkTargetHost(
					strings.TrimPrefix(arg, "--url="),
					true,
				),
			)
			continue
		}
		if arg == "--url" {
			if i+1 < len(args) {
				i++
				hosts = append(
					hosts,
					networkTargetHost(args[i], true),
				)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			skipNext = downloadOptionTakesValue(command, arg)
			continue
		}
		hosts = append(hosts, networkTargetHost(arg, true))
	}
	return nonEmptyHosts(hosts...)
}

func wgetTransportOptionHosts(
	args []string,
	index int,
) (hosts []string, consumedNext bool, handled bool) {
	arg := args[index]
	value := ""
	switch {
	case arg == "-e" || arg == "--execute":
		if index+1 < len(args) {
			value = args[index+1]
			consumedNext = true
		}
	case strings.HasPrefix(arg, "-e") && len(arg) > 2:
		value = strings.TrimPrefix(arg, "-e")
	case strings.HasPrefix(arg, "--execute="):
		value = strings.TrimPrefix(arg, "--execute=")
	default:
		return nil, false, false
	}
	key, configuredValue, ok := strings.Cut(value, "=")
	if !ok {
		return nil, consumedNext, true
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "http_proxy", "https_proxy", "ftp_proxy":
		return nonEmptyHosts(
			networkTargetHost(
				strings.TrimSpace(configuredValue),
				true,
			),
		), consumedNext, true
	default:
		return nil, consumedNext, true
	}
}

func curlTransportOptionHosts(
	args []string,
	index int,
) (hosts []string, consumedNext bool, handled bool) {
	arg := args[index]
	option := ""
	value := ""
	needsNext := false
	switch {
	case arg == "-x" || arg == "--proxy" ||
		arg == "--preproxy":
		option = "proxy"
		needsNext = true
	case strings.HasPrefix(arg, "-x") && len(arg) > 2:
		option = "proxy"
		value = strings.TrimPrefix(arg, "-x")
	case strings.HasPrefix(arg, "--proxy="):
		option = "proxy"
		value = strings.TrimPrefix(arg, "--proxy=")
	case strings.HasPrefix(arg, "--preproxy="):
		option = "proxy"
		value = strings.TrimPrefix(arg, "--preproxy=")
	case arg == "--connect-to":
		option = "connect-to"
		needsNext = true
	case strings.HasPrefix(arg, "--connect-to="):
		option = "connect-to"
		value = strings.TrimPrefix(arg, "--connect-to=")
	case arg == "--resolve":
		option = "resolve"
		needsNext = true
	case strings.HasPrefix(arg, "--resolve="):
		option = "resolve"
		value = strings.TrimPrefix(arg, "--resolve=")
	default:
		return nil, false, false
	}

	if needsNext && index+1 < len(args) {
		value = args[index+1]
		consumedNext = true
	}
	switch option {
	case "proxy":
		return nonEmptyHosts(
			networkTargetHost(value, true),
		), consumedNext, true
	case "connect-to":
		return curlConnectToHosts(value), consumedNext, true
	case "resolve":
		return curlResolveHosts(value), consumedNext, true
	default:
		return nil, consumedNext, true
	}
}

func curlConnectToHosts(value string) []string {
	fields := strings.Split(value, ":")
	if len(fields) < 4 {
		return nil
	}
	return nonEmptyHosts(
		strings.Trim(fields[0], "[]"),
		strings.Trim(fields[2], "[]"),
	)
}

func curlResolveHosts(value string) []string {
	fields := strings.SplitN(value, ":", 3)
	if len(fields) != 3 {
		return nil
	}
	hosts := []string{strings.Trim(fields[0], "[]+-")}
	for _, address := range strings.Split(fields[2], ",") {
		hosts = append(
			hosts,
			strings.TrimSpace(
				strings.Trim(address, "[]+-"),
			),
		)
	}
	return nonEmptyHosts(hosts...)
}

func downloadOptionTakesValue(command string, option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch command {
	case "curl":
		switch option {
		case "-A", "--user-agent",
			"-b", "--cookie",
			"-c", "--cookie-jar",
			"-d", "--data", "--data-ascii", "--data-binary",
			"--data-raw", "--data-urlencode",
			"-e", "--referer",
			"-F", "--form", "--form-string",
			"-H", "--header",
			"-o", "--output",
			"-u", "--user",
			"-X", "--request",
			"--cacert", "--capath", "--cert", "--key",
			"--connect-timeout", "--max-time", "--retry",
			"--retry-delay", "--retry-max-time":
			return true
		}
	case "wget":
		switch option {
		case "-O", "--output-document",
			"-o", "--output-file",
			"-a", "--append-output",
			"-P", "--directory-prefix",
			"-U", "--user-agent",
			"--header", "--post-data", "--post-file",
			"--user", "--password",
			"--timeout", "--dns-timeout",
			"--connect-timeout", "--read-timeout",
			"--tries", "--wait", "--waitretry":
			return true
		}
	}
	return false
}

func gitCommandHosts(args []string) []string {
	hosts := gitRewriteHosts(args)
	subcommandIndex := firstGitSubcommandIndex(args)
	if subcommandIndex < 0 {
		return hosts
	}
	subcommand := strings.ToLower(args[subcommandIndex])
	remaining := args[subcommandIndex+1:]
	switch subcommand {
	case "clone":
		if target := firstGitRemoteArg(remaining); target != "" {
			hosts = append(
				hosts,
				networkTargetHost(target, false),
			)
		}
	case "fetch", "pull", "push", "ls-remote":
		if target := firstPositionalArg(remaining); target != "" {
			hosts = append(
				hosts,
				networkTargetHost(target, false),
			)
		}
	case "submodule":
		if len(remaining) > 0 &&
			strings.EqualFold(remaining[0], "add") {
			if target := firstGitRemoteArg(remaining[1:]); target != "" {
				hosts = append(
					hosts,
					networkTargetHost(target, false),
				)
			}
		}
	}
	return nonEmptyHosts(hosts...)
}

func gitRewriteHosts(args []string) []string {
	var hosts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var config string
		switch {
		case arg == "-c":
			if i+1 >= len(args) {
				continue
			}
			i++
			config = args[i]
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			config = strings.TrimPrefix(arg, "-c")
		default:
			continue
		}
		key, value, ok := strings.Cut(config, "=")
		if !ok {
			continue
		}
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if strings.HasSuffix(lowerKey, ".proxy") ||
			(strings.HasPrefix(lowerKey, "remote.") &&
				strings.HasSuffix(lowerKey, ".url")) {
			hosts = append(
				hosts,
				networkTargetHost(strings.TrimSpace(value), true),
			)
			continue
		}
		const prefix = "url."
		const suffix = ".insteadof"
		if !strings.HasPrefix(lowerKey, prefix) ||
			!strings.HasSuffix(lowerKey, suffix) {
			continue
		}
		base := strings.TrimSpace(
			key[len(prefix) : len(key)-len(suffix)],
		)
		hosts = append(
			hosts,
			networkTargetHost(base, true),
		)
	}
	return nonEmptyHosts(hosts...)
}

func firstGitSubcommandIndex(args []string) int {
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-C", "-c", "--git-dir", "--work-tree",
				"--namespace", "--super-prefix":
				skipNext = true
			}
			continue
		}
		return i
	}
	return -1
}

func firstGitRemoteArg(args []string) string {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-b", "--branch",
				"-o", "--origin",
				"-u", "--upload-pack",
				"-j", "--jobs",
				"--depth", "--shallow-since",
				"--shallow-exclude", "--reference",
				"--reference-if-able", "--separate-git-dir",
				"--config", "--server-option",
				"--filter", "--template":
				skipNext = true
			}
			continue
		}
		return arg
	}
	return ""
}

func explicitURLHosts(text string) []string {
	var hosts []string
	for _, raw := range urlPattern.FindAllString(text, -1) {
		hosts = append(hosts, networkTargetHost(raw, false))
	}
	return nonEmptyHosts(hosts...)
}

func networkTargetHost(target string, allowBare bool) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if raw := urlPattern.FindString(target); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return remoteHost(raw)
		}
		if host := u.Hostname(); host != "" {
			return strings.TrimSuffix(host, ".")
		}
		return remoteHost(u.Host)
	}
	if host := scpLikeRemoteHost(target); host != "" {
		return host
	}
	if strings.Contains(target, ":") &&
		(strings.Contains(target, "@") ||
			!strings.Contains(target, "/")) {
		return remoteHost(target)
	}
	if !allowBare {
		return ""
	}
	u, err := url.Parse("//" + target)
	if err != nil {
		return remoteHost(target)
	}
	return strings.TrimSuffix(u.Hostname(), ".")
}

func scpLikeRemoteHost(target string) string {
	target = strings.TrimSpace(target)
	colon := strings.Index(target, ":")
	if colon <= 0 {
		return ""
	}
	if slash := strings.Index(target, "/"); slash >= 0 &&
		slash < colon {
		return ""
	}
	host := target[:colon]
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	host = strings.Trim(host, "[]")
	if net.ParseIP(host) != nil ||
		strings.Contains(host, ".") ||
		strings.EqualFold(host, "localhost") {
		return strings.TrimSuffix(host, ".")
	}
	return ""
}

func nonEmptyHosts(hosts ...string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host != "" {
			out = append(out, host)
		}
	}
	return out
}

func goCommandHosts(args []string) []string {
	subcommandIndex := firstGoSubcommandIndex(args)
	if subcommandIndex < 0 {
		return nil
	}
	subcommand := strings.ToLower(args[subcommandIndex])
	switch subcommand {
	case "get", "install", "run":
	default:
		return nil
	}

	var hosts []string
	skipNext := false
	for _, arg := range args[subcommandIndex+1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			skipNext = goOptionTakesValue(arg)
			continue
		}
		if host := goModuleHost(arg); host != "" {
			hosts = append(hosts, host)
		}
	}
	return nonEmptyHosts(hosts...)
}

func firstGoSubcommandIndex(args []string) int {
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			skipNext = arg == "-C"
			continue
		}
		return i
	}
	return -1
}

func goOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-C", "-exec", "-mod", "-modfile", "-overlay",
		"-p", "-tags", "-toolexec":
		return true
	default:
		return false
	}
}

func goModuleHost(argument string) string {
	argument = strings.TrimSpace(argument)
	if argument == "" ||
		strings.HasPrefix(argument, ".") ||
		strings.HasPrefix(argument, "/") ||
		strings.HasSuffix(argument, ".go") {
		return ""
	}
	if at := strings.Index(argument, "@"); at >= 0 {
		argument = argument[:at]
	}
	first, _, _ := strings.Cut(argument, "/")
	if net.ParseIP(first) != nil ||
		strings.Contains(first, ".") {
		return strings.TrimSuffix(first, ".")
	}
	return ""
}

func firstPositionalArg(args []string) string {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-i", "-p", "-l", "-o", "-F", "-J", "-S", "-W", "-w":
				skipNext = true
			}
			continue
		}
		return arg
	}
	return ""
}

func remoteHost(target string) string {
	target = strings.TrimSpace(target)
	if at := strings.LastIndex(target, "@"); at >= 0 {
		target = target[at+1:]
	}
	if strings.HasPrefix(target, "[") {
		if end := strings.Index(target, "]"); end > 0 {
			return target[1:end]
		}
	}
	if colon := strings.Index(target, ":"); colon >= 0 {
		target = target[:colon]
	}
	return strings.TrimSuffix(target, ".")
}

func hostAllowed(host string, allowlist []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, allowed := range allowlist {
			if strings.EqualFold(strings.TrimSpace(allowed), host) {
				return true
			}
		}
		return false
	}
	for _, allowed := range allowlist {
		allowed = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(allowed)), ".")
		if allowed == "" {
			continue
		}
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func firstDependencyInstall(
	lower string,
	pipe *shellsafe.Pipeline,
	configured []string,
) string {
	if pipe != nil {
		for _, argv := range pipe.Commands {
			if len(argv) < 2 {
				continue
			}

			executable := strings.ToLower(
				filepath.Base(argv[0]),
			)
			args := lowerArgs(argv[1:])
			if mutation := dependencyMutation(executable, args); mutation != "" {
				return mutation
			}
			for _, check := range configured {
				fields := strings.Fields(strings.ToLower(strings.TrimSpace(check)))
				if len(fields) < 2 ||
					executable != strings.ToLower(filepath.Base(fields[0])) ||
					len(args) < len(fields)-1 {
					continue
				}
				matches := true
				for i := 1; i < len(fields); i++ {
					if args[i-1] != fields[i] {
						matches = false
						break
					}
				}
				if matches {
					return strings.Join(fields, " ")
				}
			}
		}

		return ""
	}

	// Parse 失败时没有可信 argv。这里保留保守的文本扫描，
	// 避免复杂 shell 语法把依赖安装行为藏起来。
	checks := dependencyMutationPhrases()
	for _, check := range configured {
		if len(strings.Fields(check)) >= 2 {
			checks = append(checks, check)
		}
	}
	for _, pattern := range checks {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return pattern
		}
	}

	return ""
}

func lowerArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = strings.ToLower(arg)
	}
	return out
}

func dependencyMutation(executable string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	subcommand := args[0]
	switch executable {
	case "go":
		switch subcommand {
		case "get", "install":
			return executable + " " + subcommand
		case "run":
			for _, arg := range args[1:] {
				if strings.Contains(arg, "@") &&
					goModuleHost(arg) != "" {
					return "go run remote module"
				}
			}
		case "env":
			for _, arg := range args[1:] {
				if arg == "-w" || strings.HasPrefix(arg, "-w=") {
					return "go env -w"
				}
			}
		}
	case "npm":
		if stringInSet(subcommand,
			"install", "i", "add", "ci", "update", "uninstall", "remove") {
			return executable + " " + subcommand
		}
	case "pnpm", "yarn":
		if stringInSet(subcommand,
			"install", "add", "update", "upgrade", "remove") {
			return executable + " " + subcommand
		}
	case "pip", "pip3":
		if stringInSet(subcommand, "install", "uninstall") {
			return executable + " " + subcommand
		}
	case "cargo", "gem":
		if stringInSet(subcommand, "install", "uninstall", "update") {
			return executable + " " + subcommand
		}
	case "apt", "apt-get", "brew":
		if stringInSet(subcommand, "install", "remove", "upgrade", "update") {
			return executable + " " + subcommand
		}
	}
	return ""
}

func dependencyMutationPhrases() []string {
	return []string{
		"go get", "go install", "go env -w",
		"npm install", "npm add", "npm ci",
		"npm update", "npm uninstall", "npm remove",
		"pnpm install", "pnpm add", "pnpm update",
		"pnpm upgrade", "pnpm remove",
		"yarn install", "yarn add", "yarn update",
		"yarn upgrade", "yarn remove",
		"pip install", "pip uninstall",
		"pip3 install", "pip3 uninstall",
		"cargo install", "cargo uninstall", "cargo update",
		"gem install", "gem uninstall", "gem update",
		"apt install", "apt remove", "apt upgrade", "apt update",
		"apt-get install", "apt-get remove",
		"apt-get upgrade", "apt-get update",
		"brew install", "brew remove", "brew upgrade", "brew update",
	}
}

func stringInSet(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func firstDestructiveVCSMutation(pipe *shellsafe.Pipeline) string {
	if pipe == nil {
		return ""
	}
	for _, argv := range pipe.Commands {
		if len(argv) < 2 ||
			!strings.EqualFold(filepath.Base(argv[0]), "git") {
			continue
		}
		index := firstGitSubcommandIndex(argv[1:])
		if index < 0 {
			continue
		}
		args := argv[1:]
		subcommand := strings.ToLower(args[index])
		remaining := args[index+1:]
		switch subcommand {
		case "clean":
			return "git clean"
		case "reset":
			for _, arg := range remaining {
				if arg == "--hard" {
					return "git reset --hard"
				}
			}
		}
	}
	return ""
}

func sleepDurationSeconds(
	lower string,
	pipe *shellsafe.Pipeline,
) (seconds int64, unbounded bool) {
	if pipe != nil {
		for _, argv := range pipe.Commands {
			if len(argv) < 2 ||
				!strings.EqualFold(
					filepath.Base(argv[0]),
					"sleep",
				) {
				continue
			}
			for _, operand := range argv[1:] {
				value, infinite, ok := parseSleepOperand(operand)
				if infinite {
					return 0, true
				}
				if ok {
					seconds += value
				}
			}
			return seconds, false
		}
		return 0, false
	}

	match := longSleepPattern.FindStringSubmatch(lower)
	if len(match) != 3 {
		return 0, false
	}
	value, infinite, ok := parseSleepOperand(match[2])
	if !ok {
		return 0, infinite
	}
	return value, infinite
}

func parseSleepOperand(
	raw string,
) (seconds int64, unbounded bool, ok bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "inf", "infinity":
		return 0, true, true
	}

	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if value < 0 {
			return 0, false, false
		}
		return value, false, true
	}

	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		if value < 0 ||
			math.IsNaN(value) ||
			math.IsInf(value, 0) {
			return 0, false, false
		}
		const maxInt64 = int64(^uint64(0) >> 1)
		if value >= float64(maxInt64) {
			return maxInt64, false, true
		}
		return int64(math.Ceil(value)), false, true
	}

	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseFloat(
			strings.TrimSuffix(raw, "d"),
			64,
		)
		if err != nil || days < 0 ||
			math.IsNaN(days) || math.IsInf(days, 0) {
			return 0, false, false
		}
		const secondsPerDay = float64(24 * 60 * 60)
		const maxInt64 = int64(^uint64(0) >> 1)
		seconds := days * secondsPerDay
		if seconds >= float64(maxInt64) {
			return maxInt64, false, true
		}
		return int64(math.Ceil(seconds)), false, true
	}

	duration, err := time.ParseDuration(raw)
	if err != nil || duration < 0 {
		return 0, false, false
	}
	seconds = int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds, false, true
}

func requestedConcurrency(
	pipe *shellsafe.Pipeline,
) (maximum int, unbounded bool) {
	if pipe == nil {
		return 0, false
	}
	for _, argv := range pipe.Commands {
		if len(argv) < 2 {
			continue
		}
		command := strings.ToLower(filepath.Base(argv[0]))
		start := 1
		shortFlag := ""
		longFlag := ""
		switch command {
		case "go":
			if len(argv) < 3 ||
				!strings.EqualFold(argv[1], "test") {
				continue
			}
			start = 2
			for _, flag := range []string{"-p", "-parallel"} {
				value, _ := integerFlagValue(
					argv[start:],
					flag,
					"",
					false,
					false,
				)
				if value > maximum {
					maximum = value
				}
			}
			continue
		case "xargs":
			shortFlag = "-P"
			longFlag = "--max-procs"
		case "make", "gmake":
			shortFlag = "-j"
			longFlag = "--jobs"
		default:
			continue
		}
		unboundedWithoutValue := command == "make" ||
			command == "gmake"
		zeroIsUnbounded := unboundedWithoutValue ||
			command == "xargs"
		value, flagUnbounded := integerFlagValue(
			argv[start:],
			shortFlag,
			longFlag,
			unboundedWithoutValue,
			zeroIsUnbounded,
		)
		if flagUnbounded {
			unbounded = true
		}
		if value > maximum {
			maximum = value
		}
	}
	return maximum, unbounded
}

func integerFlagValue(
	args []string,
	shortFlag string,
	longFlag string,
	unboundedWithoutValue bool,
	zeroIsUnbounded bool,
) (value int, unbounded bool) {
	for i, arg := range args {
		if arg == shortFlag || (longFlag != "" && arg == longFlag) {
			if i+1 < len(args) {
				value, err := strconv.Atoi(args[i+1])
				if err == nil {
					return concurrencyFlagValue(
						value,
						zeroIsUnbounded,
					)
				}
			}
			return 0, unboundedWithoutValue
		}
		for _, flag := range []string{shortFlag, longFlag} {
			if flag == "" {
				continue
			}
			if strings.HasPrefix(arg, flag+"=") {
				value, err := strconv.Atoi(
					strings.TrimPrefix(arg, flag+"="),
				)
				if err != nil {
					return 0, false
				}
				return concurrencyFlagValue(
					value,
					zeroIsUnbounded,
				)
			}
			if flag != "-p" &&
				strings.HasPrefix(arg, flag) &&
				len(arg) > len(flag) {
				value, err := strconv.Atoi(arg[len(flag):])
				if err != nil {
					return 0, false
				}
				return concurrencyFlagValue(
					value,
					zeroIsUnbounded,
				)
			}
		}
	}
	return 0, false
}

func concurrencyFlagValue(
	value int,
	zeroIsUnbounded bool,
) (int, bool) {
	if value == 0 && zeroIsUnbounded {
		return 0, true
	}
	if value < 0 {
		return 0, false
	}
	return value, false
}

func isObviousInfiniteLoop(lower string) bool {
	return infiniteWhilePattern.MatchString(lower) ||
		infiniteForPattern.MatchString(lower)
}

func isDangerousCodeDelete(code string) bool {
	if codeDeleteAPIPattern.MatchString(code) {
		return true
	}
	if !codeProcessAPIPattern.MatchString(code) ||
		!codeRMArgumentPattern.MatchString(code) {
		return false
	}
	lower := strings.ToLower(code)
	return strings.Contains(lower, `"-rf"`) ||
		strings.Contains(lower, `'-rf'`) ||
		strings.Contains(lower, `"-fr"`) ||
		strings.Contains(lower, `'-fr'`) ||
		strings.Contains(lower, `"--recursive"`) ||
		strings.Contains(lower, `'--recursive'`)
}

// ScanRequestFromArgs extracts safety-relevant fields from a tool call.
func ScanRequestFromArgs(toolName string, args []byte) (ScanRequest, error) {
	switch toolName {
	case "workspace_exec":
		return scanWorkspaceExec(toolName, args)
	case "exec_command":
		return scanHostExec(toolName, args)
	case "execute_code":
		return scanCodeExec(toolName, args)
	default:
		return ScanRequest{ToolName: toolName, Backend: BackendUnknown}, nil
	}
}

func scanWorkspaceExec(toolName string, args []byte) (ScanRequest, error) {
	var in struct {
		Command       string            `json:"command"`
		Cwd           string            `json:"cwd"`
		Env           map[string]string `json:"env"`
		Timeout       int               `json:"timeout"`
		TimeoutSec    *int              `json:"timeout_sec"`
		TimeoutSecOld *int              `json:"timeoutSec"`
		Background    bool              `json:"background"`
		TTY           *bool             `json:"tty"`
		PTY           *bool             `json:"pty"`
		MaxOutput     int               `json:"max_output_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ScanRequest{}, fmt.Errorf(
			"extract workspace_exec arguments: %w",
			err,
		)
	}

	if strings.TrimSpace(in.Command) == "" {
		return ScanRequest{}, fmt.Errorf(
			"extract workspace_exec arguments: command is required",
		)
	}
	if in.MaxOutput < 0 {
		return ScanRequest{}, fmt.Errorf(
			"extract workspace_exec arguments: " +
				"max_output_bytes must not be negative",
		)
	}

	timeout := firstIntValue(
		0,
		in.TimeoutSec,
		in.TimeoutSecOld,
	)
	if timeout <= 0 {
		timeout = in.Timeout
	}

	return ScanRequest{
		ToolName:       toolName,
		Command:        in.Command,
		Backend:        BackendWorkspaceExec,
		Cwd:            filepath.ToSlash(in.Cwd),
		Env:            in.Env,
		TimeoutSec:     timeout,
		Background:     in.Background,
		TTY:            firstBoolValue(in.TTY, in.PTY),
		MaxOutputBytes: in.MaxOutput,
	}, nil
}

func scanHostExec(toolName string, args []byte) (ScanRequest, error) {
	var in struct {
		Command       string            `json:"command"`
		Workdir       string            `json:"workdir"`
		Env           map[string]string `json:"env"`
		YieldTimeMS   *int              `json:"yield-time_ms"`
		YieldMs       *int              `json:"yieldMs"`
		TimeoutSec    *int              `json:"timeout_sec"`
		TimeoutSecOld *int              `json:"timeoutSec"`
		Background    bool              `json:"background"`
		TTY           *bool             `json:"tty"`
		PTY           *bool             `json:"pty"`
		MaxOutput     int               `json:"max_output_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ScanRequest{}, fmt.Errorf(
			"extract hostexec arguments: %w",
			err,
		)
	}

	if strings.TrimSpace(in.Command) == "" {
		return ScanRequest{}, fmt.Errorf(
			"extract hostexec arguments: command is required",
		)

	}
	if in.MaxOutput < 0 {
		return ScanRequest{}, fmt.Errorf(
			"extract hostexec arguments: " +
				"max_output_bytes must not be negative",
		)
	}
	yield := defaultHostExecYieldMS
	if value := firstIntPointer(in.YieldTimeMS, in.YieldMs); value != nil &&
		*value >= 0 {
		yield = *value
	}
	timeout := defaultHostExecTimeoutSec
	if value := firstIntPointer(
		in.TimeoutSec,
		in.TimeoutSecOld,
	); value != nil && *value > 0 {
		timeout = *value
	}
	return ScanRequest{
		ToolName:       toolName,
		Command:        in.Command,
		Backend:        BackendHostExec,
		Cwd:            filepath.ToSlash(in.Workdir),
		Env:            in.Env,
		TimeoutSec:     timeout,
		YieldTimeMS:    yield,
		Background:     in.Background,
		TTY:            firstBoolValue(in.TTY, in.PTY),
		MaxOutputBytes: in.MaxOutput,
	}, nil
}

func scanCodeExec(toolName string, args []byte) (ScanRequest, error) {
	var in struct {
		CodeBlocks json.RawMessage `json:"code_blocks"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ScanRequest{}, fmt.Errorf(
			"extract codeexec arguments: %w",
			err,
		)
	}
	command, shellCommand, blocks, err := codeBlocksCommand(in.CodeBlocks)
	if err != nil {
		return ScanRequest{}, fmt.Errorf(
			"extract codeexec arguments: %w",
			err,
		)
	}
	return ScanRequest{
		ToolName:      toolName,
		Command:       command,
		Backend:       BackendCodeExec,
		validatedCode: true,
		shellCommand:  shellCommand,
		codeBlocks:    blocks,
	}, nil
}

func codeBlocksCommand(
	raw json.RawMessage,
) (
	command string,
	shellCommand string,
	scanBlocks []codeBlock,
	err error,
) {
	if len(raw) == 0 {
		return "", "", nil, fmt.Errorf("code_blocks is required")
	}

	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return "", "", nil, err
	}

	if s, ok := val.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return "", "", nil, err
		}
	}

	if val == nil {
		return "", "", nil, fmt.Errorf("code_blocks is required")
	}

	type codeBlockInput struct {
		Language string `json:"language"`
		Code     string `json:"code"`
	}

	var blocks []codeBlockInput

	switch val.(type) {
	case []any:
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", "", nil, err
		}
		if len(blocks) == 0 {
			return "", "", nil, fmt.Errorf(
				"code_blocks must contain at least one block",
			)
		}

	case map[string]any:
		var block codeBlockInput
		if err := json.Unmarshal(raw, &block); err != nil {
			return "", "", nil, err
		}
		blocks = []codeBlockInput{block}

	default:
		return "", "", nil, fmt.Errorf(
			"code_blocks must be an array, object, or JSON string",
		)
	}

	var parts []string
	var shellParts []string

	for i, block := range blocks {
		if strings.TrimSpace(block.Code) == "" {
			return "", "", nil, fmt.Errorf(
				"code_blocks[%d].code is required",
				i,
			)
		}

		scanBlocks = append(scanBlocks, codeBlock{
			language: block.Language,
			code:     block.Code,
		})
		if isShellLanguage(normalizeCodeLanguage(block.Language)) {
			parts = append(parts, block.Code)
			shellParts = append(shellParts, block.Code)
			continue
		}

		parts = append(parts, block.Code)
	}

	return strings.Join(parts, "\n"),
		strings.Join(shellParts, "\n"),
		scanBlocks,
		nil
}

func firstIntValue(base int, ptrs ...*int) int {
	if base > 0 {
		return base
	}
	for _, p := range ptrs {
		if p != nil {
			return *p
		}
	}
	return 0
}

func firstIntPointer(ptrs ...*int) *int {
	for _, p := range ptrs {
		if p != nil {
			return p
		}
	}
	return nil
}

func firstBoolValue(ptrs ...*bool) bool {
	for _, p := range ptrs {
		if p != nil {
			return *p
		}
	}
	return false
}
