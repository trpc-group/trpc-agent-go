//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Decision is the Guard scan outcome (mirrors PermissionAction vocabulary).
type Decision string

const (
	// DecisionAllow lets the tool call proceed.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks the tool call.
	DecisionDeny Decision = "deny"
	// DecisionAsk requests human approval before execution.
	DecisionAsk Decision = "ask"
)

// RiskLevel is a coarse severity label for reports and OTel attributes.
type RiskLevel string

const (
	// RiskNone means no elevated risk was observed.
	RiskNone RiskLevel = "none"
	// RiskLow is informational.
	RiskLow RiskLevel = "low"
	// RiskMedium warrants review (typically ask).
	RiskMedium RiskLevel = "medium"
	// RiskHigh should usually deny.
	RiskHigh RiskLevel = "high"
	// RiskCritical is reserved for credential / destructive hits.
	RiskCritical RiskLevel = "critical"
)

// Finding is one matched rule.
type Finding struct {
	RuleID   string    `json:"rule_id"`
	Decision Decision  `json:"decision"`
	Risk     RiskLevel `json:"risk_level"`
	Evidence string    `json:"evidence"`
	Advice   string    `json:"recommendation"`
}

// Result is the aggregated scan report for one tool call.
type Result struct {
	Decision  Decision  `json:"decision"`
	RiskLevel RiskLevel `json:"risk_level"`
	RuleID    string    `json:"rule_id"`
	Evidence  string    `json:"evidence"`
	Advice    string    `json:"recommendation"`
	ToolName  string    `json:"tool_name"`
	Command   string    `json:"command,omitempty"`
	Backend   Backend   `json:"backend"`
	Blocked   bool      `json:"blocked"`
	Findings  []Finding `json:"findings,omitempty"`
	Redacted  bool      `json:"redacted"`
}

var (
	reURL           = regexp.MustCompile(`(?i)\bhttps?://[^\s"'\\]+`)
	reSchemeLess    = regexp.MustCompile(`(?i)\b(?:curl|wget|fetch)\s+((?:[a-z0-9-]+\.)+[a-z]{2,}(?:/[^\s"']*)?)`)
	reSecret        = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|authorization|bearer)\s*[:=]\s*['"]?([^\s'"]{8,})`)
	rePEM           = regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----`)
	reSK            = regexp.MustCompile(`(?i)\bsk-[a-z0-9]{16,}\b`)
	reNetPipeInterp = regexp.MustCompile(`(?i)\b(?:curl|wget|fetch|nc|ncat|netcat)\b[^|\n]*\|\s*(?:python3?|py|node|nodejs|deno|bun|ruby|perl|php|lua|pwsh|powershell|bash|sh|zsh|dash|ash)\b`)
	// Python/Node-ish download-and-run via subprocess / os.system (code_blocks).
	reSubprocessNet = regexp.MustCompile(`(?i)(?:subprocess\.(?:run|call|popen|check_output|check_call)|os\.(?:system|popen)|child_process\.(?:exec|execSync|spawn|spawnSync))\s*\([^;\n]{0,240}\b(?:curl|wget|fetch|http\.get|urllib|requests\.get)\b`)
	reRemoteGoRun   = regexp.MustCompile(`(?i)\bgo\s+run\s+(?:https?://)?(?:[a-z0-9-]+\.)+[a-z]{2,}/[^\s'"]+`)
)

// Scan applies policy to an extracted payload.
//
// Ordering (deny wins over ask over allow):
//  1. secret / credential leakage in any text (including code_blocks / stdin)
//  2. denied paths in argv / cwd / raw text
//  3. shellsafe parse failure -> deny (fail-closed; never default allow)
//  4. shellsafe allow/deny + implicit wrapper deny
//  5. network hosts outside allowlist
//  6. resource abuse (long sleep / over policy timeout hint)
//  7. ask-command / hostexec ask policy
func Scan(ex Extracted, policy Policy) Result {
	res := Result{
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		ToolName:  ex.ToolName,
		Command:   ex.Command,
		Backend:   ex.Backend,
		Advice:    "safe to execute under current policy",
	}

	texts := collectTexts(ex)
	if f, ok := scanSecrets(texts); ok {
		res.Command = redactSecrets(res.Command)
		res.Redacted = true
		return finalize(res, f)
	}
	if f, ok := scanDeniedPaths(ex, policy.DeniedPaths); ok {
		return finalize(res, f)
	}
	if f, ok := scanEnvAllowlist(ex, policy.AllowedEnvVars); ok {
		return finalize(res, f)
	}

	if strings.TrimSpace(ex.Command) != "" {
		res = scanShellCommand(res, ex, policy)
		if res.Decision == DecisionDeny {
			return res
		}
	}
	// Always scan stdin / code_blocks for deletion, network, and install
	// patterns — even when a benign command is also present.
	res = scanNonShellPayload(res, ex, policy)
	if res.Decision == DecisionDeny {
		return res
	}

	if f, ok := scanResourceAbuse(ex, policy); ok {
		res = finalize(res, f)
	}
	if f, ok := scanInfiniteLoop(ex); ok && res.Decision != DecisionDeny {
		res = finalize(res, f)
	}
	if ex.Backend == BackendHost && policy.HostExecRequiresAsk && res.Decision == DecisionAllow {
		res = finalize(res, Finding{
			RuleID:   "hostexec.long_session_risk",
			Decision: DecisionAsk,
			Risk:     RiskMedium,
			Evidence: "hostexec/exec_command can retain PTY sessions and host processes",
			Advice:   "require approval for host execution or restrict to workspace_exec",
		})
	}
	return finishResult(res)
}

func collectTexts(ex Extracted) []string {
	texts := []string{ex.RawText, ex.Command, ex.Stdin, ex.Cwd}
	texts = append(texts, ex.Paths...)
	return append(texts, ex.CodeBlocks...)
}

func scanShellCommand(res Result, ex Extracted, policy Policy) Result {
	pipe, err := shellsafe.Parse(ex.Command)
	if err != nil {
		return finalize(res, Finding{
			RuleID:   "shellsafe.unparsable",
			Decision: DecisionDeny,
			Risk:     RiskHigh,
			Evidence: err.Error(),
			Advice:   "refuse commands shellsafe cannot parse; rewrite as a simple pipeline or deny",
		})
	}
	// Prefer pipe / remote-go-run before the generic shellsafe wrapper deny so
	// curl|sh reports pipe_network_to_interpreter instead of only "sh denied".
	if f, ok := scanPipeToInterpreter(pipe); ok {
		return finalize(res, f)
	}
	if f, ok := scanRemoteGoRun(pipe); ok {
		return finalize(res, f)
	}
	shellPol := policy.shellPolicy()
	// shellsafe.Check only applies its built-in wrapper denies when the
	// policy is Active(); the sentinel keeps Deny non-empty without matching
	// real executables.
	if !shellPol.Active() {
		shellPol.Deny = []string{"__trpc_agent_safety_sentinel__"}
	}
	if err := shellPol.Check(pipe); err != nil {
		return finalize(res, Finding{
			RuleID:   "shellsafe.policy",
			Decision: DecisionDeny,
			Risk:     RiskHigh,
			Evidence: err.Error(),
			Advice:   "adjust allowed_commands / denied_commands or wrap the use in an auditable workspace script",
		})
	}
	if f, ok := scanDangerousDeletion(ex.Command); ok {
		return finalize(res, f)
	}
	if f, ok := scanNetwork(pipe, ex.Command, policy.AllowedHosts); ok {
		return finalize(res, f)
	}
	if f, ok := scanAskCommands(pipe, policy.AskCommands); ok {
		return finalize(res, f)
	}
	return res
}

func scanNonShellPayload(res Result, ex Extracted, policy Policy) Result {
	// stdin / code_blocks / path-ish fields (path, uri, url, …). Paths are
	// included so MCP-style tools without a shell command still hit network
	// and install checks; denied_paths already ran earlier on the same set.
	parts := make([]string, 0, 2+len(ex.CodeBlocks)+len(ex.Paths))
	if ex.Stdin != "" {
		parts = append(parts, ex.Stdin)
	}
	parts = append(parts, ex.CodeBlocks...)
	parts = append(parts, ex.Paths...)
	extraText := strings.TrimSpace(strings.Join(parts, "\n"))
	if extraText == "" {
		return res
	}
	if f, ok := scanDangerousDeletion(extraText); ok {
		return finalize(res, f)
	}
	if f, ok := scanTextPipeInterpreter(extraText); ok {
		return finalize(res, f)
	}
	if f, ok := scanCodeSubprocessNetwork(extraText); ok {
		return finalize(res, f)
	}
	if f, ok := scanTextRemoteGoRun(extraText); ok {
		return finalize(res, f)
	}
	if f, ok := scanNetworkFromText(extraText, policy.AllowedHosts); ok {
		return finalize(res, f)
	}
	if looksLikeInstall(extraText) {
		return finalize(res, Finding{
			RuleID:   "code.install_mutation",
			Decision: DecisionAsk,
			Risk:     RiskMedium,
			Evidence: "payload appears to install packages or mutate environment",
			Advice:   "require human review before executing dependency-changing code",
		})
	}
	return res
}

func scanTextPipeInterpreter(text string) (Finding, bool) {
	if text == "" || !reNetPipeInterp.MatchString(text) {
		return Finding{}, false
	}
	return Finding{
		RuleID:   "shell.pipe_network_to_interpreter",
		Decision: DecisionDeny,
		Risk:     RiskHigh,
		Evidence: "payload pipes network output into an interpreter",
		Advice:   "download to a reviewed artifact first; do not pipe remote content into python/node/sh/bash/…",
	}, true
}

func scanCodeSubprocessNetwork(text string) (Finding, bool) {
	if text == "" || !reSubprocessNet.MatchString(text) {
		return Finding{}, false
	}
	return Finding{
		RuleID:   "code.subprocess_network",
		Decision: DecisionDeny,
		Risk:     RiskHigh,
		Evidence: "code spawns a network client via subprocess/os.system/child_process",
		Advice:   "fetch outside the sandbox with a reviewed allowlist, or pass a local artifact path",
	}, true
}

func scanTextRemoteGoRun(text string) (Finding, bool) {
	if text == "" || !reRemoteGoRun.MatchString(text) {
		return Finding{}, false
	}
	return Finding{
		RuleID:   "shell.remote_go_run",
		Decision: DecisionDeny,
		Risk:     RiskHigh,
		Evidence: "payload contains go run of a remote module path",
		Advice:   "vendor or clone the module locally, review it, then go run ./path",
	}, true
}

func finishResult(res Result) Result {
	if res.Decision == DecisionAllow && res.RuleID == "" {
		res.RuleID = "allow"
		res.Evidence = "no denying or ask rule matched"
	}
	res.Blocked = res.Decision == DecisionDeny || res.Decision == DecisionAsk
	return res
}

func finalize(base Result, f Finding) Result {
	if containsSecretEvidence(f.Evidence) {
		f.Evidence = redactSecrets(f.Evidence)
	}
	base.Findings = append(base.Findings, f)
	switch {
	case f.Decision == DecisionDeny || base.Decision != DecisionDeny && f.Decision == DecisionAsk:
		base.Decision = f.Decision
		base.RiskLevel = f.Risk
		base.RuleID = f.RuleID
		base.Evidence = f.Evidence
		base.Advice = f.Advice
	case base.Decision == DecisionAllow && riskRank(f.Risk) > riskRank(base.RiskLevel):
		base.RiskLevel = f.Risk
	}
	base.Blocked = base.Decision == DecisionDeny || base.Decision == DecisionAsk
	if containsSecretEvidence(f.Evidence) || containsSecretEvidence(base.Evidence) ||
		containsSecretEvidence(base.Command) {
		base.Redacted = true
		base.Evidence = redactSecrets(base.Evidence)
		base.Command = redactSecrets(base.Command)
	}
	return base
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

func scanSecrets(texts []string) (Finding, bool) {
	for _, t := range texts {
		if t == "" {
			continue
		}
		if rePEM.MatchString(t) {
			return Finding{
				RuleID:   "secret.private_key",
				Decision: DecisionDeny,
				Risk:     RiskCritical,
				Evidence: "private key material detected in tool arguments",
				Advice:   "never pass private keys to tools; use secret stores",
			}, true
		}
		if m := reSK.FindString(t); m != "" {
			return Finding{
				RuleID:   "secret.api_token",
				Decision: DecisionDeny,
				Risk:     RiskCritical,
				Evidence: "credential-like token " + redactSecrets(m),
				Advice:   "remove secrets from commands; use env injection outside the model transcript",
			}, true
		}
		if m := reSecret.FindStringSubmatch(t); len(m) > 0 {
			return Finding{
				RuleID:   "secret.credential_field",
				Decision: DecisionDeny,
				Risk:     RiskCritical,
				Evidence: "credential field " + redactSecrets(m[0]),
				Advice:   "strip credentials from tool arguments before execution",
			}, true
		}
	}
	return Finding{}, false
}

func scanDeniedPaths(ex Extracted, denied []string) (Finding, bool) {
	if len(denied) == 0 {
		return Finding{}, false
	}
	candidates := []string{ex.Command, ex.Stdin, ex.Cwd, ex.RawText}
	candidates = append(candidates, ex.Paths...)
	candidates = append(candidates, ex.CodeBlocks...)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		lower := strings.ToLower(c)
		for _, d := range denied {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if pathHit(lower, strings.ToLower(d)) {
				return Finding{
					RuleID:   "path.denied",
					Decision: DecisionDeny,
					Risk:     RiskCritical,
					Evidence: "denied path marker " + d,
					Advice:   "do not read or write credential/system paths from tools",
				}, true
			}
		}
	}
	return Finding{}, false
}

func scanEnvAllowlist(ex Extracted, allowed []string) (Finding, bool) {
	if len(allowed) == 0 || len(ex.Env) == 0 {
		return Finding{}, false
	}
	allow := map[string]struct{}{}
	for _, a := range allowed {
		allow[strings.ToUpper(strings.TrimSpace(a))] = struct{}{}
	}
	for k := range ex.Env {
		key := strings.ToUpper(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		if _, ok := allow[key]; !ok {
			return Finding{
				RuleID:   "env.not_allowed",
				Decision: DecisionDeny,
				Risk:     RiskHigh,
				Evidence: "environment variable " + k + " is not in allowed_env_vars",
				Advice:   "drop the env override or add it to allowed_env_vars in the policy file",
			}, true
		}
	}
	return Finding{}, false
}

func pathHit(text, marker string) bool {
	if marker == "" {
		return false
	}
	markerLower := strings.ToLower(marker)
	// ".env" style markers match the exact basename and variants such as
	// ".env.local" / ".env.production", without treating ".environment" as a hit.
	strictExt := strings.HasPrefix(marker, ".") && !strings.Contains(marker[1:], "/")
	if !strictExt && strings.Contains(strings.ToLower(text), markerLower) {
		return true
	}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || r == '=' || r == ','
	}) {
		p := strings.Trim(part, `"'`)
		pl := strings.ToLower(p)
		if strictExt {
			base := strings.ToLower(filepath.Base(p))
			if base == markerLower || strings.HasPrefix(base, markerLower+".") {
				return true
			}
			continue
		}
		if strings.Contains(pl, markerLower) {
			return true
		}
		base := strings.ToLower(filepath.Base(p))
		if base == strings.ToLower(path.Base(marker)) || base == markerLower {
			return true
		}
	}
	return false
}

func scanDangerousDeletion(text string) (Finding, bool) {
	lower := strings.ToLower(text)
	patterns := []string{
		"rm -rf /", "rm -rf/*", "rm -fr /",
		"rm -rf ~", "rm -rf $home",
		"del /s /q", "rd /s /q",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return Finding{
				RuleID:   "danger.destructive_delete",
				Decision: DecisionDeny,
				Risk:     RiskCritical,
				Evidence: "destructive delete pattern " + p,
				Advice:   "block recursive deletes of system or home roots",
			}, true
		}
	}
	if strings.Contains(lower, "rm ") && (strings.Contains(lower, " -rf") ||
		strings.Contains(lower, " -fr") || strings.Contains(lower, " -r -f") ||
		strings.Contains(lower, " -f -r")) {
		return Finding{
			RuleID:   "danger.destructive_delete",
			Decision: DecisionDeny,
			Risk:     RiskCritical,
			Evidence: "recursive force delete via rm",
			Advice:   "block rm -rf style deletions unless explicitly allowlisted by operators",
		}, true
	}
	return Finding{}, false
}

func scanNetwork(pipe *shellsafe.Pipeline, command string, allowed []string) (Finding, bool) {
	if pipe == nil {
		return scanNetworkFromText(command, allowed)
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := strings.ToLower(filepath.Base(argv[0]))
		base = strings.TrimSuffix(base, ".exe")
		if !isNetworkClient(base) {
			continue
		}
		for _, arg := range argv[1:] {
			host := hostOf(arg)
			if host == "" {
				continue
			}
			if !hostAllowed(host, allowed) {
				return Finding{
					RuleID:   "network.denied_host",
					Decision: DecisionDeny,
					Risk:     RiskHigh,
					Evidence: "outbound host " + host + " not in allowed_hosts",
					Advice:   "add the host to allowed_hosts or remove the network call",
				}, true
			}
		}
	}
	return scanNetworkFromText(command, allowed)
}

func scanNetworkFromText(text string, allowed []string) (Finding, bool) {
	for _, m := range reURL.FindAllString(text, -1) {
		host := hostOf(m)
		if host == "" {
			continue
		}
		if !hostAllowed(host, allowed) {
			return Finding{
				RuleID:   "network.denied_host",
				Decision: DecisionDeny,
				Risk:     RiskHigh,
				Evidence: "outbound host " + host + " not in allowed_hosts",
				Advice:   "add the host to allowed_hosts or remove the network call",
			}, true
		}
	}
	for _, m := range reSchemeLess.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		host := hostOf(m[1])
		if host == "" {
			host = strings.Split(m[1], "/")[0]
		}
		if host == "" {
			continue
		}
		if !hostAllowed(host, allowed) {
			return Finding{
				RuleID:   "network.denied_host",
				Decision: DecisionDeny,
				Risk:     RiskHigh,
				Evidence: "outbound host " + host + " not in allowed_hosts",
				Advice:   "add the host to allowed_hosts or remove the network call",
			}, true
		}
	}
	return Finding{}, false
}

func isNetworkClient(base string) bool {
	switch base {
	case "curl", "wget", "fetch", "nc", "ncat", "netcat", "ssh", "scp", "sftp", "ftp":
		return true
	default:
		return false
	}
}

func isInterpreter(base string) bool {
	switch base {
	case "python", "python2", "python3", "py",
		"node", "nodejs", "deno", "bun",
		"ruby", "perl", "php", "lua", "osascript",
		"pwsh", "powershell", "powershell.exe",
		// curl|sh is the classic download-and-run bypass.
		"sh", "bash", "zsh", "dash", "ash":
		return true
	default:
		return false
	}
}

// scanRemoteGoRun denies `go run host/path…` (module path with a dotted host).
// Local paths (./…, ../…, /abs) stay under ask_commands for plain `go`.
func scanRemoteGoRun(pipe *shellsafe.Pipeline) (Finding, bool) {
	if pipe == nil {
		return Finding{}, false
	}
	for _, argv := range pipe.Commands {
		if commandBase(argv) != "go" || len(argv) < 3 {
			continue
		}
		if !strings.EqualFold(argv[1], "run") {
			continue
		}
		pkg := strings.TrimSpace(argv[2])
		if !looksLikeRemoteGoPkg(pkg) {
			continue
		}
		return Finding{
			RuleID:   "shell.remote_go_run",
			Decision: DecisionDeny,
			Risk:     RiskHigh,
			Evidence: "go run fetches and executes a remote module path",
			Advice:   "vendor or clone the module locally, review it, then go run ./path",
		}, true
	}
	return Finding{}, false
}

func looksLikeRemoteGoPkg(pkg string) bool {
	if pkg == "" {
		return false
	}
	// Local / absolute paths are not remote module fetches.
	if strings.HasPrefix(pkg, ".") || strings.HasPrefix(pkg, "/") ||
		strings.HasPrefix(pkg, `\`) {
		return false
	}
	if len(pkg) >= 2 && pkg[1] == ':' { // Windows drive
		return false
	}
	host, _, ok := strings.Cut(pkg, "/")
	if !ok || host == "" {
		return false
	}
	// Module paths look like github.com/org/repo[…].
	return strings.Contains(host, ".")
}

func commandBase(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	base := strings.ToLower(filepath.Base(argv[0]))
	return strings.TrimSuffix(base, ".exe")
}

// scanPipeToInterpreter catches pipelines that feed an interpreter.
// Network-to-interpreter (even allowlisted hosts) and local-to-interpreter
// both deny: static review cannot tell whether the left-hand side is trusted.
func scanPipeToInterpreter(pipe *shellsafe.Pipeline) (Finding, bool) {
	if pipe == nil || len(pipe.Commands) < 2 {
		return Finding{}, false
	}
	hasNet := false
	hasInterp := false
	for _, argv := range pipe.Commands {
		base := commandBase(argv)
		if isNetworkClient(base) {
			hasNet = true
		}
		if isInterpreter(base) {
			hasInterp = true
		}
	}
	if !hasInterp {
		return Finding{}, false
	}
	rule := "shell.pipe_to_interpreter"
	evidence := "pipeline feeds data into an interpreter"
	if hasNet {
		rule = "shell.pipe_network_to_interpreter"
		evidence = "pipeline feeds network output into an interpreter"
	}
	return Finding{
		RuleID:   rule,
		Decision: DecisionDeny,
		Risk:     RiskHigh,
		Evidence: evidence,
		Advice:   "avoid piping into python/node/sh/bash/…; write a reviewed script artifact instead",
	}, true
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	host := raw
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	if !strings.Contains(host, ".") && host != "localhost" {
		return ""
	}
	return host
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if host == a {
			return true
		}
		// Suffix matching is opt-in only via leading-dot entries.
		if strings.HasPrefix(a, ".") && strings.HasSuffix(host, a) {
			return true
		}
	}
	return false
}

func scanAskCommands(pipe *shellsafe.Pipeline, ask []string) (Finding, bool) {
	if pipe == nil || len(ask) == 0 {
		return Finding{}, false
	}
	askSet := map[string]struct{}{}
	for _, a := range ask {
		askSet[strings.ToLower(a)] = struct{}{}
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := strings.ToLower(filepath.Base(argv[0]))
		base = strings.TrimSuffix(base, ".exe")
		if _, ok := askSet[base]; !ok {
			continue
		}
		if base == "go" && len(argv) > 1 {
			sub := argv[1]
			if sub == "test" || sub == "version" || sub == "env" || sub == "fmt" || sub == "vet" {
				continue
			}
		}
		return Finding{
			RuleID:   "ask.dependency_or_mutation",
			Decision: DecisionAsk,
			Risk:     RiskMedium,
			Evidence: "command " + base + " requires human review under ask_commands",
			Advice:   "approve only after reviewing package sources and intended mutation",
		}, true
	}
	return Finding{}, false
}

func scanResourceAbuse(ex Extracted, policy Policy) (Finding, bool) {
	texts := collectTexts(ex)
	joined := strings.ToLower(strings.Join(texts, "\n"))
	if sec, ok := parseSleepSeconds(joined); ok {
		limit := policy.MaxTimeoutSeconds
		if limit <= 0 {
			limit = 60
		}
		if sec >= limit {
			return Finding{
				RuleID:   "resource.long_sleep",
				Decision: DecisionAsk,
				Risk:     RiskMedium,
				Evidence: "sleep duration meets or exceeds max_timeout_seconds scan hint",
				Advice:   "shorten the wait, raise the scan hint after review, and keep executor timeouts enabled",
			}, true
		}
	}
	if policy.MaxOutputBytes > 0 {
		for _, t := range texts {
			if len(t) > policy.MaxOutputBytes {
				return Finding{
					RuleID:   "resource.oversized_payload",
					Decision: DecisionAsk,
					Risk:     RiskMedium,
					Evidence: "tool argument size exceeds max_output_bytes scan hint",
					Advice:   "trim the payload, raise the scan hint after review, and keep executor output caps enabled",
				}, true
			}
		}
	}
	return Finding{}, false
}

func parseSleepSeconds(text string) (int, bool) {
	// Match common forms: sleep 99999 / sleep 99999s / sleep 10m / sleep 1h.
	// Skip unparsable tokens so "sleep 0.5; sleep 99999" still finds the long wait.
	fields := strings.Fields(text)
	best := 0
	found := false
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "sleep" {
			continue
		}
		sec, ok := parseDurationToken(fields[i+1])
		if !ok {
			continue
		}
		if !found || sec > best {
			best = sec
			found = true
		}
	}
	return best, found
}

func parseDurationToken(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(raw, "h"):
		mult = 3600
		raw = strings.TrimSuffix(raw, "h")
	case strings.HasSuffix(raw, "m"):
		mult = 60
		raw = strings.TrimSuffix(raw, "m")
	case strings.HasSuffix(raw, "s"):
		raw = strings.TrimSuffix(raw, "s")
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return int(float64(n) * mult), true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && f >= 0 {
		return int(f * mult), true
	}
	return 0, false
}

func looksLikeInstall(code string) bool {
	lower := strings.ToLower(code)
	needles := []string{
		"pip install", "pip3 install", "npm install", "yarn add",
		"go install", "apt install", "apt-get install", "brew install",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

func scanInfiniteLoop(ex Extracted) (Finding, bool) {
	texts := collectTexts(ex)
	patterns := []string{
		"while true", "while (true)", "while(true)",
		"for(;;)", "for (; ;)",
	}
	for _, t := range texts {
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		for _, p := range patterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				return Finding{
					RuleID:   "resource.infinite_loop",
					Decision: DecisionAsk,
					Risk:     RiskMedium,
					Evidence: "payload appears to contain an unbounded loop",
					Advice:   "add a termination condition or require human review before execution",
				}, true
			}
		}
	}
	return Finding{}, false
}

func containsSecretEvidence(s string) bool {
	return reSK.MatchString(s) || reSecret.MatchString(s) || rePEM.MatchString(s)
}

func redactSecrets(s string) string {
	s = reSK.ReplaceAllString(s, "sk-***REDACTED***")
	s = rePEM.ReplaceAllString(s, "-----BEGIN PRIVATE KEY----- ***REDACTED***")
	s = reSecret.ReplaceAllStringFunc(s, func(m string) string {
		parts := strings.SplitN(m, "=", 2)
		if len(parts) == 2 {
			return parts[0] + "=***REDACTED***"
		}
		parts = strings.SplitN(m, ":", 2)
		if len(parts) == 2 {
			return parts[0] + ":***REDACTED***"
		}
		return "***REDACTED***"
	})
	return s
}
