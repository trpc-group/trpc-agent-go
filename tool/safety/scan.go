//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"net/url"
	"path"
	"path/filepath"
	"regexp"
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
	reURL        = regexp.MustCompile(`(?i)\bhttps?://[^\s"'\\]+`)
	reSchemeLess = regexp.MustCompile(`(?i)\b(?:curl|wget|fetch)\s+((?:[a-z0-9-]+\.)+[a-z]{2,}(?:/[^\s"']*)?)`)
	reSecret     = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|authorization|bearer)\s*[:=]\s*['"]?([^\s'"]{8,})`)
	rePEM        = regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----`)
	reSK         = regexp.MustCompile(`(?i)\bsk-[a-z0-9]{16,}\b`)
)

// Scan applies policy to an extracted payload.
//
// Ordering (deny wins over ask over allow):
//  1. secret / credential leakage in any text (including code_blocks / stdin)
//  2. denied paths in argv / cwd / raw text
//  3. shellsafe parse failure 鈫?deny (fail-closed; never default allow)
//  4. shellsafe allow/deny + implicit wrapper deny
//  5. network hosts outside allowlist
//  6. ask-command / hostexec ask policy
func Scan(ex Extracted, policy Policy) Result {
	res := Result{
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		ToolName:  ex.ToolName,
		Command:   ex.Command,
		Backend:   ex.Backend,
		Advice:    "safe to execute under current policy",
	}

	texts := []string{ex.RawText, ex.Command, ex.Stdin, ex.Cwd}
	texts = append(texts, ex.CodeBlocks...)

	if f, ok := scanSecrets(texts); ok {
		// Never leave the original command (with tokens) on the result —
		// reports and audit consumers serialize Result.Command.
		res.Command = redactSecrets(res.Command)
		res.Redacted = true
		return finalize(res, f)
	}
	if f, ok := scanDeniedPaths(ex, policy.DeniedPaths); ok {
		return finalize(res, f)
	}

	// Command path: prefer shellsafe. Code-only payloads skip shell parse.
	if strings.TrimSpace(ex.Command) != "" {
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
		shellPol := policy.ShellPolicy()
		// shellsafe applies its implicit wrapper deny only when the policy is
		// Active(). Keep Guard fail-closed even if an overlay cleared both lists.
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
		// Dangerous deletion patterns that may pass basename policy if rm is
		// not listed 鈥?keep an explicit content check for the issue's 100% bar.
		if f, ok := scanDangerousDeletion(ex.Command); ok {
			return finalize(res, f)
		}
		if f, ok := scanNetwork(pipe, ex.Command, policy.AllowedHosts); ok {
			return finalize(res, f)
		}
		if f, ok := scanAskCommands(pipe, policy.AskCommands); ok {
			res = finalize(res, f)
		}
	} else if len(ex.CodeBlocks) > 0 {
		joined := strings.Join(ex.CodeBlocks, "\n")
		if f, ok := scanDangerousDeletion(joined); ok {
			return finalize(res, f)
		}
		if f, ok := scanNetworkFromText(joined, policy.AllowedHosts); ok {
			return finalize(res, f)
		}
		// Code execution without a shell command still warrants review for installs.
		if looksLikeInstall(joined) {
			res = finalize(res, Finding{
				RuleID:   "code.install_mutation",
				Decision: DecisionAsk,
				Risk:     RiskMedium,
				Evidence: "code block appears to install packages or mutate environment",
				Advice:   "require human review before executing dependency-changing code",
			})
		}
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

	if res.Decision == DecisionAllow && res.RuleID == "" {
		res.RuleID = "allow"
		res.Evidence = "no denying or ask rule matched"
	}
	res.Blocked = res.Decision == DecisionDeny || res.Decision == DecisionAsk
	return res
}

func finalize(base Result, f Finding) Result {
	base.Findings = append(base.Findings, f)
	// Deny always wins; ask wins over allow.
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

func pathHit(text, marker string) bool {
	if marker == "" {
		return false
	}
	if strings.Contains(text, marker) {
		return true
	}
	// Also match flag forms: --env-file=.env / --config=/etc/secrets
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || r == '=' || r == ','
	}) {
		p := strings.Trim(part, `"'`)
		if strings.Contains(strings.ToLower(p), marker) {
			return true
		}
		base := strings.ToLower(filepath.Base(p))
		if base == strings.ToLower(path.Base(marker)) || base == strings.ToLower(marker) {
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
	// Broader: rm with -rf/-fr anywhere.
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
			// Still scan argv for URLs in any command.
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
	// Scheme-less curl/wget targets: `curl evil.example/x`
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
	// host/path or host:port
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
		// Suffix matching is opt-in only: operators list ".github.com" when
		// they intend every subdomain. A bare "api.github.com" must NOT admit
		// "evil.api.github.com" (common allowlist footgun).
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
		if _, ok := askSet[base]; ok {
			// go test is safe; go install / get need ask.
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
	}
	return Finding{}, false
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
